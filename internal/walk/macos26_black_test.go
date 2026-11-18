package walk

import (
	"os"
	"strings"
	"testing"
)

// THE LOOKS MUST TREAT A BLACK FRAME AS "STOP WAITING", AND THIS IS THE TEST THAT KEEPS IT SO.
//
// MEASURED ON RUN 44190274785: the install finished writing and the screen went black - reads 63..65
// said "About 17 / 16 minutes remaining", every read from 66 to 270 said `0 word(s)`, and the video
// shows the installer's own Apple-logo restart followed by identical black frames. `broken pipe` was
// 0, so the machine was alive. With `mode: says_any` the loop waited for words that were never
// coming and the stage failed on a disk that was already written.
//
// `says_any` cannot express "or nothing was drawn"; `matches` with `^$` can, and the `reset:` step
// that already follows each look is what turns the black screen into a Setup Assistant (measured
// locally: reboot -> "Select Your Country or Region").
func TestMacos26LooksEndOnABlackFrame(t *testing.T) {
	const file = "../../macos/26/steps/setup.yaml"
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	src := string(raw)

	looks := 0
	for _, line := range strings.Split(src, "\n") {
		if strings.Contains(line, "the copy runs, look") {
			looks++
		}
	}
	if looks == 0 {
		t.Fatalf("%s has no looks; this test is looking at the wrong file", file)
	}

	// Every look's check must be the regex form, and the regex must accept the empty reading.
	if got := strings.Count(src, "mode: matches\n      pattern: '^$|"); got != looks {
		t.Errorf("%d of %d looks carry the black-frame pattern; each look must end when nothing is "+
			"drawn, or the run waits out its whole ceiling on a finished install "+
			"(easydev-org/easyvm run 44190274785)", got, looks)
	}
	if !strings.Contains(src, "|update mac automatically|choose your look") {
		t.Error("the pattern no longer accepts the later panes; a look that only knows the country " +
			"list would end early on the update pane and be reset for no reason")
	}
}
