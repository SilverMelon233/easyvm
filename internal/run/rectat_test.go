package run

import "testing"

// The macos recovery frame is 640x480 and its rects were written at 1920x1080 - the file says so with
// `rect_at:`. Scaling by the FRAME instead of that size asked for a 1400-wide crop of a 640-wide image:
//
//	crop [760, 200, 1400, 560] is not inside a 640x480 frame
func TestRectAtScalesByTheScreenItWasWrittenFor(t *testing.T) {
	r, err := parseRectAt([]any{760, 200, 1400, 560}, [2]int{1920, 1080})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Pixels || r.AtW != 1920 || r.AtH != 1080 {
		t.Fatalf("rect_at was dropped: %+v", r)
	}
	// A PIXEL RECT STAYS PIXELS; the division happens where the frame is known.
	fw, fh := 640.0, 480.0
	x0 := r.XFrom / float64(r.AtW) * fw
	x1 := r.XTo / float64(r.AtW) * fw
	y0 := r.From / float64(r.AtH) * fh
	y1 := r.To / float64(r.AtH) * fh
	if x0 < 0 || y0 < 0 || x1 > fw || y1 > fh {
		t.Fatalf("scaled to [%.0f %.0f %.0f %.0f], outside a %.0fx%.0f frame", x0, y0, x1, y1, fw, fh)
	}
	// AND WITHOUT `rect_at:` THE FRAME IS THE ONLY ANSWER, which is the old behaviour and still right
	// for a file that never named a size.
	bare, err := parseRectAt([]any{760, 200, 1400, 560}, [2]int{})
	if err != nil {
		t.Fatal(err)
	}
	if bare.AtW != 0 {
		t.Fatalf("a rect with no `rect_at:` invented a size: %+v", bare)
	}
	t.Logf("1920x1080 rect [760 200 1400 560] -> [%.0f %.0f %.0f %.0f] on 640x480", x0, y0, x1, y1)
}
