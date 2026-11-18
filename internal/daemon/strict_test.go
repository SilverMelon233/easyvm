package daemon

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/easydev-org/easyvm/internal/protocol"
)

// A REFUSAL MUST NOT LOOK LIKE A SUCCESS, AND A BAD FIELD MUST NOT BE IGNORED.
//
// PORTED FROM UPSTREAM (`10631ed`), WHICH MEASURED THESE THREE SHAPES ON A RUNNING DAEMON:
//
//	POST /api/click {"xx": 100, "yy": 200}   the keys do not match, both decoded to zero, the click
//	                                         landed at (0, 0) and the reply was {"ok":true}
//	POST /api/click {}                       the same path with no body at all
//	POST /api/click {"x":1,"y":2}            (no machine) answered 200 with {"ok":false} in the body,
//	                                         which `curl -f` cannot see
//
// THE FIRST TWO ARE NOW A 400 THAT NAMES THE FIELD and the third a 409. This test is what keeps them
// that way; the alternative - reading the JSON body for "ok" - is the habit that let the first two
// survive.
//
// IT DRIVES THE REAL TRANSPORT. A handler called directly would prove nothing about the status a
// client actually reads.
func TestPointerRoutesRefuseBadInputInsteadOfClickingTheCorner(t *testing.T) {
	startDaemon(t)
	base := "http://" + os.Getenv("EASYVM_HTTP")

	post := func(path, body string) (int, string) {
		t.Helper()
		resp, err := http.Post(base+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("%s %s: %v", path, body, err)
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(out)
	}

	cases := []struct {
		name   string
		body   string
		status int
		// mustMention is the word the error has to contain, so a wrong message is caught too: an
		// error that says "bad request" for a misspelled key sends the reader nowhere.
		mustMention string
	}{
		{
			name:        "misspelled keys are refused, not silently zeroed",
			body:        `{"xx": 100, "yy": 200}`,
			status:      http.StatusBadRequest,
			mustMention: "x",
		},
		{
			name:        "an empty body names both missing coordinates",
			body:        `{}`,
			status:      http.StatusBadRequest,
			mustMention: "x",
		},
		{
			name:        "only y is named as the missing one",
			body:        `{"x": 10}`,
			status:      http.StatusBadRequest,
			mustMention: "y",
		},
		{
			name:        "only x is named as the missing one",
			body:        `{"y": 10}`,
			status:      http.StatusBadRequest,
			mustMention: "x",
		},
		{
			// WELL-FORMED, AND REFUSED BY THE MACHINE: there is no guest to click.
			name:        "no machine is a conflict, not a 200",
			body:        `{"x": 10, "y": 20}`,
			status:      http.StatusConflict,
			mustMention: "",
		},
		{
			// THE POINT IS VALID AND THE EXTRA KEY IS NOT, so only `DisallowUnknownFields` can catch
			// this one - `xyOf` would pass it.
			name:        "an unknown field beside a valid point is refused",
			body:        `{"x": 10, "y": 20, "settle": 5}`,
			status:      http.StatusBadRequest,
			mustMention: "settle",
		},
		{
			name:        "two JSON objects in one body is refused",
			body:        `{"x":1,"y":2}{"x":3,"y":4}`,
			status:      http.StatusBadRequest,
			mustMention: "ONE JSON object",
		},
	}

	for _, c := range cases {
		code, body := post("/api/click", c.body)
		if code != c.status {
			t.Errorf("%s: %s -> %d, want %d (body %s)", c.name, c.body, code, c.status, body)
		}
		if c.mustMention != "" && !strings.Contains(body, c.mustMention) {
			t.Errorf("%s: the error does not mention %q, so it sends the reader nowhere: %s",
				c.name, c.mustMention, body)
		}
		// NO REFUSAL MAY CARRY "ok":true, WHICH IS THE WHOLE POINT OF THE STATUS.
		if c.status != http.StatusOK && strings.Contains(body, `"ok":true`) {
			t.Errorf("%s: a %d carried {\"ok\":true}", c.name, c.status)
		}
	}
}

// A COORDINATE OF ZERO IS A PLACE, AND ITS ABSENCE IS NOT THE SAME VALUE.
//
// THE STRUCT USED `int` WITH `omitempty`, so `(0, 0)` - the top-left pixel, which a walkthrough may
// name - was DROPPED from the wire and arrived as "the caller sent nothing". A pointer makes `&0` and
// `nil` different values, which is what lets one be a click and the other a 400.
func TestACoordinateOfZeroIsCarriedAndAMissingOneIsNil(t *testing.T) {
	zero := 0
	wire, err := json.Marshal(protocol.Request{Command: protocol.CommandClick, X: &zero, Y: &zero})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), `"x":0`) || !strings.Contains(string(wire), `"y":0`) {
		t.Fatalf("`x: 0` was omitted from %s - the daemon cannot tell the corner from nothing", wire)
	}

	// AND AN ABSENT ONE DECODES TO nil, not to zero.
	var req protocol.Request
	if err := json.Unmarshal([]byte(`{"command":"click"}`), &req); err != nil {
		t.Fatal(err)
	}
	if req.X != nil || req.Y != nil {
		t.Fatalf("an absent coordinate decoded to %v/%v, so it is indistinguishable from (0, 0)",
			req.X, req.Y)
	}
	if _, _, err := xyOf("click", req); err == nil {
		t.Fatal("a request with no x or y was accepted")
	}
}
