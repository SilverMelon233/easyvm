package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/easydev-org/easyvm/internal/protocol"
)

// decodeStrict reads ONE Request and refuses anything else.
//
// `json.NewDecoder(r.Body).Decode(&req)` IS WHAT THIS REPLACES, and it ignores two things that matter:
//
//	A MISSPELLED KEY. `{"xx": 100, "y": 200}` decoded to a zero X and clicked (0, 0) - the field the
//	caller meant was dropped with no error, and the request that arrived was a different request.
//
//	A SECOND OBJECT. `{"x":1,"y":2}{"x":3}` decodes the first and leaves the rest in the stream, so a
//	body that is not one request was acted on as if it were.
//
// PROPERTY, NOT PREFERENCE: a daemon that answers `{"ok":true}` to a request it did not perform is
// worse than one that refuses, because the caller has no way to find out.
func decodeStrict(w http.ResponseWriter, r *http.Request) (protocol.Request, error) {
	var req protocol.Request
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return protocol.Request{}, fmt.Errorf("the body must be a Request: %w", err)
	}
	if dec.More() {
		return protocol.Request{}, fmt.Errorf("the body must be ONE JSON object and it has more " +
			"after the first")
	}
	return req, nil
}

// respond writes a Response AND MAPS ITS VERDICT ONTO THE STATUS CODE.
//
// A REFUSAL IS NOT A 200. The socket transport has no status codes - a refusal is `OK: false` and an
// `error` string - and this layer inherited that shape literally, so
//
//	curl -f -d '{"x":99999,"y":1}' .../api/click   ->  exit 0, body {"ok":true}
//	curl -f -d '{"x":1}'           .../api/click   ->  exit 0, body {"ok":false,...}
//
// The first line is the one that hurt: an impossible coordinate answered 200. The second is the one a
// caller could not detect without reading the body, and `curl -f` is exactly the tool that trusts the
// status.
//
// 409 FOR A REFUSAL RATHER THAN 400, because the request was well-formed and the MACHINE's state is
// what disagreed with it (no machine running, a run in progress). A MALFORMED BODY IS A 400 and is
// raised by the decoder above, before this is reached.
func respond(w http.ResponseWriter, r *http.Request, resp protocol.Response) {
	code := http.StatusOK
	if !resp.OK {
		code = http.StatusConflict
	}
	writeJSON(w, code, resp)
}

// handlePointer runs the routes that name a point.
//
// THE REQUEST IS NOT REBUILT. `handleClick` used to construct
// `protocol.Request{Command: CommandClick, X: req.X, Y: req.Y}` - a NARROWER type than the one the
// socket accepts - which is how a misspelled key became a successful click: the evidence was discarded
// one line above the code that needed it. The command is the only thing that is set; everything else is
// what arrived.
func (d *Daemon) handlePointer(w http.ResponseWriter, r *http.Request, command string) {
	req, err := decodeStrict(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.Command = command
	// A MISSING COORDINATE IS A 400 AND NOT A 409, AND THE DIFFERENCE IS THE STATUS A CALLER READS.
	//
	// `d.handle` also refuses it - the SOCKET transport needs that check - but by then all this layer
	// knows is `OK:false`, which it maps to 409. "Your body carried no x" is a 400 and "there is no
	// machine to click" is a 409, and a caller that retries on 409 would retry a body that will never
	// have an x in it.
	//
	// AN OUT-OF-FRAME COORDINATE IS THE THIRD CASE AND ALSO A 400: the number cannot name a pixel on
	// this machine, so there is nothing to retry and nothing the machine's state can change.
	if x, y, err := xyOf(command, req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	} else if fw, fh := d.frameSize(); fw > 0 && fh > 0 {
		if x < 0 || y < 0 || x >= fw || y >= fh {
			http.Error(w, fmt.Sprintf("%s to (%d, %d): outside the %dx%d frame, so this coordinate "+
				"cannot name a pixel. A coordinate past an edge is a coordinate measured on another "+
				"screen, and clamping it would move the pointer somewhere nobody asked for",
				command, x, y, fw, fh), http.StatusBadRequest)
			return
		}
	}
	respond(w, r, d.handle(req))
}
