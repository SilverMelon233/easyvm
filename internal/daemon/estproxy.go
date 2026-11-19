package daemon

import (
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"

	easyvmnet "github.com/easydev-org/easyvm/internal/net"
)

// estHTTPPort is easystream's HTTP surface INSIDE THE GUEST: its web UI, its `/est/ws` byte pipe and
// its `/a11y/*` API. It is the only easystream port reachable from outside, and the reason is worth
// recording because it looks like an arbitrary choice.
//
// MEASURED from easystream's own startup line on a Debian guest:
//
//	easystream http on 0.0.0.0:48080     <- every address, so the bridge DNAT reaches it
//	easystream est server on 127.0.0.1:48079   <- the GUEST's loopback, and only that
//
// SO 48079 CANNOT BE PROXIED AND DOES NOT NEED TO BE. easystream's own `/est/ws` is served on 48080
// and connects to its loopback internally - it IS the bridge from the outside to the stream. A
// second bridge in this daemon would be another 250 lines of est framing to keep in step with a
// protocol that already has an HTTP front end.
const estHTTPPort = 48080

// estPrefix is where easystream is mounted under this daemon.
//
// `app.js` RESOLVES ITS OWN PATHS AGAINST `location.pathname` - `base + 'est/ws'`,
// `base + 'capabilities'` - so the vendor UI works under any prefix without a byte being rewritten.
// That is why a plain prefix-stripping proxy is enough, and why the trailing slash is part of the
// contract: `/est` without it would resolve the base to `/` and the page would ask this daemon for
// `/est/ws` instead of the guest's.
const estPrefix = "/est/"

// handleEst proxies easystream, whole.
//
// ONE HANDLER, NOT TWO. The previous version had a WebSocket handler that parsed est frames and an
// HTTP handler for the page - and the WebSocket one existed only because 48079 was believed
// unreachable. It is unreachable, and it does not matter: `/est/ws` on 48080 carries the same
// stream, and a reverse proxy forwards WebSocket upgrades without knowing what a frame is.
func (d *Daemon) handleEst(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	slot := d.machine
	d.mu.Unlock()
	if slot == nil || slot.driver == nil {
		// A SENTENCE, NOT A 502 FROM A PROXY. "no machine is running" is a thing a person can act
		// on; "bad gateway" from a proxy that never had an upstream is not.
		http.Error(w, "no machine is running", http.StatusServiceUnavailable)
		return
	}
	if _, ok := slot.driver.(*Driver); !ok {
		http.Error(w, "this machine's driver has no est stream", http.StatusServiceUnavailable)
		return
	}

	target := net.JoinHostPort(easyvmnet.GuestIP, strconv.Itoa(estHTTPPort))
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			// THE PREFIX IS STRIPPED AND THE PATH IS FORWARDED AS-IS BEYOND IT. `/est/app.js` asks
			// the guest for `/app.js`, and `/est/est/ws` asks for `/est/ws` - the second one is what
			// the mount-relative `app.js` builds when it is served under `/est/`, and it is correct
			// rather than doubled.
			req.URL.Scheme = "http"
			req.URL.Host = target
			req.URL.Path = stripEstPrefix(r.URL.Path)
			req.Host = target
		},
		// NO CONNECTION POOLING, BECAUSE THE MACHINE OUTLIVES NOTHING. A pooled connection against a
		// guest that has since been replaced would serve a page from the previous boot; a microvm
		// boots in seconds, so the cost of a fresh dial is not worth that class of bug.
		Transport: &http.Transport{
			MaxIdleConns:        0,
			DisableKeepAlives:   true,
			DisableCompression:  true, // the guest already decides; re-compressing breaks ranges
			ForceAttemptHTTP2:   false,
		},
		// WEBSOCKET UPGRADES PASS THROUGH UNTOUCHED. `httputil.ReverseProxy` handles a `101
		// Switching Protocols` by hijacking both sides and copying bytes, which is exactly the
		// transparent pipe easystream's own front end expects.
		FlushInterval: -1, // no buffering: a video frame must not wait for a second one
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// THE ONE FAILURE WORTH SPELLING OUT: a guest whose easystream has not started yet, or
			// a machine that has just gone away. The browser console shows this text.
			http.Error(w, "cannot reach easystream in the guest: "+err.Error(),
				http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}

// stripEstPrefix turns `/est/x` into `/x` and `/est/` into `/`.
func stripEstPrefix(p string) string {
	if len(p) <= len(estPrefix) {
		return "/"
	}
	return p[len(estPrefix)-1:]
}
