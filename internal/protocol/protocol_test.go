package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

// A COORDINATE OF ZERO IS A REAL PIXEL AND MUST SURVIVE THE WIRE.
//
// `X int \`json:"x,omitempty"\`` DROPPED IT. A walkthrough may legitimately click the top-left
// corner, and with `omitempty` that request serialised WITHOUT the field - so a daemon reading the
// JSON could not tell "the corner" from "the caller sent nothing".
//
// THE TYPE IS `*int` NOW, WHICH MAKES THOSE TWO FACTS DIFFERENT VALUES: `&0` and `nil`. This test
// pins both halves, because a future "simplification" back to `int` would compile, pass every other
// test, and quietly make a whole class of request indistinguishable from a broken one.
func TestACoordinateOfZeroIsCarriedAndAMissingOneIsNil(t *testing.T) {
	zero := 0
	x, y := 100, 200

	cases := []struct {
		name string
		in   Request
		// wantX and wantY describe what must arrive at the daemon: nil means "no coordinate".
		missing   bool
		wantXZero bool
	}{
		{name: "the corner survives", in: Request{X: &zero, Y: &zero}, wantXZero: true},
		{name: "ordinary point", in: Request{X: &x, Y: &y}},
		{name: "nothing is nil, not zero", in: Request{}, missing: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(tc.in)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var out Request
			if err := json.Unmarshal(body, &out); err != nil {
				t.Fatalf("unmarshal %s: %v", body, err)
			}
			if tc.missing {
				if out.X != nil || out.Y != nil {
					t.Fatalf("a request with no coordinates decoded to x=%v y=%v; a zero value "+
						"would make it indistinguishable from a real click at the corner",
						show(out.X), show(out.Y))
				}
				return
			}
			if out.X == nil || out.Y == nil {
				t.Fatalf("the coordinates did not survive %s: x=%v y=%v", body, show(out.X), show(out.Y))
			}
			if tc.wantXZero && *out.X != 0 {
				t.Fatalf("x came back as %d, want 0", *out.X)
			}
		})
	}
}

// AN UNKNOWN FIELD IN A REQUEST IS REFUSED BY THE DECODER THAT THE DAEMON USES.
//
// The rule lives in `decodeStrict` (internal/daemon), and this test pins the property it depends on:
// `encoding/json` DOES silently ignore unknown fields unless told otherwise. If a future standard
// library change made that the default the daemon would lose the check without any diff here - so the
// test asserts the behaviour rather than the setting.
func TestUnknownFieldsAreIgnoredByPlainDecodeWhichIsWhyTheDaemonDisallowsThem(t *testing.T) {
	var plain Request
	if err := json.Unmarshal([]byte(`{"x": 1, "y": 2, "pixel_x": 300}`), &plain); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// THE DANGEROUS PROPERTY, STATED: a misspelled field decodes to a request that looks complete and
	// carries the wrong numbers - here, 1 and 2 are present and 300 was silently dropped.
	if plain.X == nil || *plain.X != 1 {
		t.Fatalf("x is %v, want the 1 that was sent", show(plain.X))
	}

	// AND WITH THE FLAG THE DAEMON USES, THE SAME BODY IS REFUSED AND THE MESSAGE NAMES THE FIELD.
	dec := json.NewDecoder(strings.NewReader(`{"x": 1, "y": 2, "pixel_x": 300}`))
	dec.DisallowUnknownFields()
	var strict Request
	err := dec.Decode(&strict)
	if err == nil {
		t.Fatal("a body with an unknown field was accepted; the daemon's decodeStrict would then be " +
			"reading a truth it invented")
	}
	if !strings.Contains(err.Error(), "pixel_x") {
		t.Fatalf("the error does not name the offending field:\n\t%v", err)
	}
}

func show(p *int) any {
	if p == nil {
		return "nil"
	}
	return *p
}
