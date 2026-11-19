package daemon

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/crypto/ssh"

	"github.com/easydev-org/easyvm/internal/guest"
)

// estPort is where easystream listens INSIDE THE GUEST. It is not a host port: easystream runs in the
// guest's own kernel, so there is nothing to forward and no qemu `hostfwd` to configure - the daemon
// dials it over the ssh connection it already holds.
const estPort = 48079

// estHTTPPort is easystream's other surface: its web UI, `/est/ws` byte pipe and `/a11y/*` API.
const estHTTPPort = 48080

// estStreamPath is the WebSocket the front end connects to, and it is deliberately NOT the path the
// easystream daemon itself uses (`/est/ws`). This daemon sits in front of that daemon, so the outer
// path is namespaced: a browser types one address, and this page is the only origin.
//
// THE BROWSER'S PATH IS `/est/ws` AND THE GUEST'S IS ALSO `/est/ws`; the indirection is the `est/`
// prefix this handler is mounted under. Keeping both names identical would make a future reverse
// proxy to the guest's whole HTTP surface mean something different from this one endpoint.
const estStreamPath = "/est/ws"

// estViewerPath serves easystream's own web UI, which the dashboard links to.
const estViewerPath = "/est/"

// The protocol types this proxy has to UNDERSTAND, and only these. Everything else is forwarded
// byte-for-byte: the EST framing is type(1) flags(1) length(u32) payload, so a proxy that must not
// parse video can still read the envelope and pass the body through.
const (
	estHello = 0x01
	estVideo = 0x02
	estAuth  = 0x03
)

// handleEstWS is the browser-facing end of the est stream, and it does ONE HONEST THING: it speaks
// just enough of the protocol to decide whether the viewer may control the guest, then relays.
//
// WHY A PROXY AT ALL, RATHER THAN POINTING THE BROWSER AT THE GUEST. easystream listens on the
// guest's LOOPBACK - there is no route to it from the pod, let alone from a browser - and the guest
// has no TLS. This daemon already holds an authenticated ssh connection to that guest, so
// `ssh.Client.Dial` is the one door that exists. It is the same reason the shell is ssh.
func (d *Daemon) handleEstWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusInternalError, "closing")

	ctx := r.Context()
	writeError := func(msg string) {
		// ONE JSON LINE, which is the shape `app.js` reads to report WHY est is unavailable instead
		// of showing a black canvas. A binary-only pipe would leave the page guessing.
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"error":"`+msg+`"}`))
	}

	// READ-ONLY IS DECIDED HERE, AT THE SERVER. `?view=1` means the viewer token is not sent on to
	// easystream, so its `auth::authorize(None)` answers VIEW and every mutation is refused by the
	// daemon that owns the screen. A switch in the browser would be a courtesy; this is a rule.
	// easystream's own HTTP surface refuses mutations for a VIEW connection, which is where the
	// enforcement lives - this proxy only has to not send a token it was not asked to send.
	wantControl := r.URL.Query().Get("view") != "1"

	d.mu.Lock()
	slot := d.machine
	d.mu.Unlock()
	if slot == nil || slot.driver == nil {
		writeError("no machine is running")
		return
	}
	drv, ok := slot.driver.(*Driver)
	if !ok {
		writeError("this machine's driver has no est stream")
		return
	}

	// THE DIAL GOES THROUGH SSH, SO IT CANNOT BLOCK THE PAGE FOREVER. A guest whose easystream has
	// not started yet would otherwise leave the WebSocket open and silent.
	var raw *ssh.Client
	dialErr := drv.DoGuest(func(c *guest.Client) error {
		raw = c.Raw()
		return nil
	})
	if dialErr != nil || raw == nil {
		writeError("cannot reach the guest over ssh")
		return
	}

	type dialed struct {
		c   net.Conn
		err error
	}
	ch := make(chan dialed, 1)
	go func() {
		c, err := raw.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(estPort)))
		ch <- dialed{c, err}
	}()

	var upstream net.Conn
	select {
	case <-ctx.Done():
		return
	case <-time.After(10 * time.Second):
		writeError("the guest's est port did not answer within 10s")
		return
	case d := <-ch:
		if d.err != nil {
			writeError("connecting to the guest's est port: " + d.err.Error())
			return
		}
		upstream = d.c
	}
	defer upstream.Close()

	// ONE READER ON THE UPSTREAM, PUMPED INTO THE WEBSOCKET. Reading and writing on this side are
	// separate goroutines because both directions are long-lived: a viewer that stops sending must
	// keep receiving frames.
	frames := make(chan []byte, 128)
	go func() {
		defer close(frames)
		buf := make([]byte, 64<<10)
		for {
			n, rerr := upstream.Read(buf)
			if n > 0 {
				b := make([]byte, n)
				copy(b, buf[:n])
				select {
				case frames <- b:
				case <-ctx.Done():
					return
				}
			}
			if rerr != nil {
				return
			}
		}
	}()

	// BROWSER -> GUEST, with the ONE piece of protocol this proxy edits.
	//
	// A VIEW CONNECTION MUST NOT BE ABLE TO ASK FOR CONTROL. est's `Auth` frame (0x03) carries the
	// token that upgrades a connection; dropping it when `?view=1` is what makes the read-only
	// switch real rather than decorative. Every other frame - KeyFrameReq, ConfigSet, pointer and
	// key events - is forwarded untouched.
	go func() {
		for {
			typ, data, rerr := conn.Read(ctx)
			if rerr != nil {
				_ = upstream.Close()
				return
			}
			if typ != websocket.MessageBinary {
				continue
			}
			if !wantControl && len(data) >= 1 && data[0] == estAuth {
				continue // the token is withheld; the guest stays in VIEW
			}
			if _, werr := upstream.Write(data); werr != nil {
				_ = upstream.Close()
				return
			}
		}
	}()

	for b := range frames {
		if err := conn.Write(ctx, websocket.MessageBinary, b); err != nil {
			return
		}
	}
}

// handleEstViewer proxies easystream's own web UI, so the dashboard can link to a working page
// rather than one that only exists inside the guest.
//
// IT IS A PLAIN REVERSE PROXY OVER THE SAME SSH DOOR. easystream's UI is mount-relative (its app.js
// resolves `est/ws` and `capabilities` against `location.pathname`), so serving it under `/est/`
// works without rewriting a single byte of it.
func (d *Daemon) handleEstViewer(w http.ResponseWriter, r *http.Request) {
	// THE BANNER THE DASHBOARD SHOWS INSTEAD OF A BROKEN PANEL. A guest with no easystream answers
	// nothing, and "connection refused" from a proxy is less useful than the reason.
	d.mu.Lock()
	slot := d.machine
	d.mu.Unlock()
	if slot == nil || slot.driver == nil {
		http.Error(w, "no machine is running", http.StatusServiceUnavailable)
		return
	}
	drv, ok := slot.driver.(*Driver)
	if !ok {
		http.Error(w, "this machine's driver has no est stream", http.StatusServiceUnavailable)
		return
	}
	var raw *ssh.Client
	if err := drv.DoGuest(func(c *guest.Client) error { raw = c.Raw(); return nil }); err != nil || raw == nil {
		http.Error(w, "cannot reach the guest over ssh", http.StatusServiceUnavailable)
		return
	}

	// THE PATH IS FORWARDED AS-IS. `app.js` builds `base + 'est/ws'` from its own location, so the
	// trailing slash of `/est/` is part of the contract and is not normalised away here.
	target := strings.TrimPrefix(r.URL.Path, "/est")
	if target == "" {
		target = "/"
	}
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}

	upstream, err := raw.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(estHTTPPort)))
	if err != nil {
		http.Error(w, "connecting to the guest's easystream: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer upstream.Close()

	// A MINIMAL HTTP/1.1 FORWARD, because the connection is an ssh channel rather than a socket this
	// process can hand to `httputil.ReverseProxy` - that expects a `net.Conn` it can dial itself.
	out := r.Method + " " + target + " HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n"
	for k, vs := range r.Header {
		if strings.EqualFold(k, "Connection") {
			continue
		}
		for _, v := range vs {
			out += k + ": " + v + "\r\n"
		}
	}
	out += "\r\n"
	if _, err := upstream.Write([]byte(out)); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	// THE RESPONSE IS PARSED JUST ENOUGH TO RELAY STATUS AND HEADERS; the body is copied. Using
	// `http.ReadResponse` here means the framing (chunked or not) is handled by the stdlib rather
	// than by this file.
	resp, err := http.ReadResponse(bufio.NewReader(upstream), r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
