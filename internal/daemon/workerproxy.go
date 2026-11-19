package daemon

import (
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"

	easyvmnet "github.com/easydev-org/easyvm/internal/net"
)

// workerPath is where easyworker's Connect RPC is mounted under this daemon.
const workerPath = "/worker/"

// workerPrefixInGuest is the path the RPC handlers live at on easyworker itself. It is `/` because
// that service has no other surface any more - the panel, the `/desktop/` proxy and `/stream` were
// removed, so everything it serves is RPC.
const workerPrefixInGuest = "/"

// workerProxy builds the reverse proxy for easyworker.
//
// A FUNCTION RATHER THAN A FIELD because the target is the GUEST's address and the guest changes: a
// cached proxy would keep a transport pooled against a machine that has been replaced, and a
// microvm boots in seconds so a fresh dial costs nothing worth pooling for.
func (d *Daemon) workerProxy() *httputil.ReverseProxy {
	target := net.JoinHostPort(easyvmnet.GuestIP, strconv.Itoa(workerPort))
	return &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			req.URL.Host = target
			// THE PREFIX IS STRIPPED: `/worker/worker.v1.WorkerService/Info` reaches the guest as
			// `/worker.v1.WorkerService/Info`, which is the path Connect generates.
			req.URL.Path = stripWorkerPrefix(req.URL.Path)
			req.Host = target
		},
		Transport: &http.Transport{
			// NO POOLING, for the same reason the est bridge dials fresh: a connection held against
			// a guest that has since been replaced would answer from the previous boot.
			MaxIdleConns:       0,
			DisableKeepAlives:  true,
			DisableCompression: true,
			ForceAttemptHTTP2:  false,
		},
		// CONNECT STREAMING NEEDS NO BUFFERING. `WatchJob` is a server-streaming RPC: the browser
		// shows job output as it arrives, and a proxy that batched it would turn live output into a
		// pause followed by everything at once.
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// THE ONE FAILURE WORTH SPELLING OUT: the guest has no easyworker, or the machine has
			// just gone away. The dashboard shows this text beside an empty panel.
			http.Error(w, "cannot reach easyworker in the guest: "+err.Error(),
				http.StatusBadGateway)
		},
	}
}

// stripWorkerPrefix turns `/worker/x` into `/x`, and `/worker/` into `/`.
func stripWorkerPrefix(p string) string {
	if len(p) <= len(workerPath) {
		return workerPrefixInGuest
	}
	return "/" + p[len(workerPath):]
}
