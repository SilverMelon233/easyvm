package daemon

import (
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"

	"github.com/coder/websocket"

	easyvmnet "github.com/easydev-org/easyvm/internal/net"
)

// estPort is easystream's ONLY port. `est://` is a TCP protocol that carries video, input, a11y
// messages and screenshots on one connection (see EST.md), and as of the change that removed that
// daemon's HTTP surface it is the only thing it serves.
const estPort = 48079

// workerPort is easyworker's Connect RPC. It sits beside est, and the pair is the contract:
//
//	easystream  48079  est://   video, input, a11y, screenshot
//	easyworker  48080  HTTP     the Connect RPC
const workerPort = 48080

// estPath is the WebSocket the dashboard's est panel connects to.
//
// IT EXISTS BECAUSE A BROWSER CANNOT OPEN A RAW TCP SOCKET. easystream speaks est over TCP; a page
// speaks WebSocket. This handler is that translation and NOTHING ELSE - it does not parse a single
// est frame. Every byte from the guest is wrapped in a binary WebSocket message and every binary
// message from the page is written straight to the socket, which is the whole of what a bridge
// needs to do when both sides are already a byte stream.
const estPath = "/est/ws"

// handleEstWS bridges the browser's WebSocket to the guest's est socket.
func (d *Daemon) handleEstWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// THE PAGE IS SERVED BY THIS SAME SERVER. The daemon cannot know the name a Service
		// publishes it under, so the origin check is off; the socket carries the guest's screen.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return // Accept has already written the failure.
	}
	defer conn.Close(websocket.StatusInternalError, "closing")

	ctx := r.Context()
	refuse := func(msg string) {
		// ONE JSON LINE, WHICH IS THE SHAPE app.js READS to say why est is unavailable instead of
		// drawing a black canvas. A binary-only pipe would leave the page guessing.
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"error":"`+msg+`"}`))
	}

	upstream, err := net.Dial("tcp", net.JoinHostPort(easyvmnet.GuestIP, strconv.Itoa(estPort)))
	if err != nil {
		// THE SENTENCE NAMES BOTH HALVES: the address is the guest's and the port is easystream's,
		// so a reader knows which of the two to go and look at.
		refuse("the guest's est port: " + err.Error())
		return
	}
	defer upstream.Close()

	// A READER AND A WRITER, NOT ONE LOOP. Both directions are long-lived - a viewer that stops
	// sending must keep receiving - and a single loop would make the browser's silence stall video.
	frames := make(chan []byte, 64)
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
				if !errors.Is(rerr, io.EOF) {
					select {
					case frames <- []byte(`{"error":"est socket: ` + rerr.Error() + `"}`):
					default:
					}
				}
				return
			}
		}
	}()

	go func() {
		for {
			typ, data, rerr := conn.Read(ctx)
			if rerr != nil {
				_ = upstream.Close()
				return
			}
			if typ != websocket.MessageBinary {
				// READ-ONLY IS DECIDED BY WHAT ARRIVES, AND A TEXT FRAME IS NOT PART OF est. The
				// viewer flag is expressed in the subprotocol list instead - a control connection
				// sends `[est, token]`, a viewer sends `[est]` - so there is nothing to enforce here
				// beyond dropping frames that are not the protocol.
				continue
			}
			if _, werr := upstream.Write(data); werr != nil {
				_ = upstream.Close()
				return
			}
		}
	}()

	for b := range frames {
		if len(b) > 0 && b[0] == '{' {
			if conn.Write(ctx, websocket.MessageText, b) != nil {
				return
			}
			continue
		}
		if err := conn.Write(ctx, websocket.MessageBinary, b); err != nil {
			return
		}
	}
}

// handleWorker proxies easyworker's Connect RPC, which is ordinary HTTP.
//
// IT IS A PLAIN REVERSE PROXY, because unlike est there is nothing to translate: Connect speaks
// HTTP/1.1 with protobuf bodies, and `httputil.ReverseProxy` forwards that, including the
// server-streaming `WatchJob` (which is chunked, not a separate protocol).
func (d *Daemon) handleWorker(w http.ResponseWriter, r *http.Request) {
	d.workerProxy().ServeHTTP(w, r)
}
