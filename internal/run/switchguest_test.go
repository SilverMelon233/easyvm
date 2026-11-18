package run

import (
	"strings"
	"testing"
	"time"
)

// ONE WALK, TWO GUESTS: stop the first and start a second on the same daemon.
//
// THE QUESTION THIS ANSWERS: can a walkthrough show one operating system, stop it, and show another -
// so that ONE recording contains both desktops? That is what the xfce-then-openbox e2e test needs, and
// what `macos/26/steps/resume.yaml` relies on in the other direction (a guest that is already there).
//
// THE MACHINE SLOT IS THE THING THAT DECIDES. `doBoot` refuses a different `os:` while a machine is
// RUNNING - "one daemon holds one machine" - and that check is guarded by `IsRunning()`, so a stopped
// machine is not a machine. Whether the second boot is allowed is therefore a question about whether
// `Stop` leaves the driver able to start again.
//
// IT IS TESTED HERE BECAUSE IT WAS NEVER TESTED: `fakeMachine.IsRunning` answered `started > 0` and
// `Stop` did not clear it, so every test that could have asked this question was answered "still
// running" by the fake rather than by the code.
func TestOneWalkCanStopAGuestAndStartAnother(t *testing.T) {
	d := &fakeDriver{}
	ex, steps := newExec(d,
		map[string]any{"name": "boot windows", "boot": true, "os": "windows", "iso": "win.iso"},
		map[string]any{"name": "stop it", "shutdown": "kill", "timeout": float64(60)},
		map[string]any{"name": "boot linux", "boot": true, "os": "linux", "iso": "linux.iso"},
	)
	ex.Work = t.TempDir()
	if _, err := ex.Run(steps); err != nil {
		t.Fatalf("a walk that stops one guest and starts another: %v", err)
	}

	if d.started != 2 {
		t.Fatalf("%d boot(s) happened and the walk asked for two. A daemon that refuses the second "+
			"cannot record a walkthrough of two operating systems", d.started)
	}
	if len(d.stopped) != 1 || d.stopped[0] != "kill" {
		t.Fatalf("stops = %v, want one kill", d.stopped)
	}
	if d.boot == nil || d.boot.OS != "linux" {
		t.Fatalf("the second boot asked for %+v, and the walk said os: linux", d.boot)
	}
}

// TestASecondBootOfTheSameRunningGuestIsANoOp is the other half: a `boot:` against a machine that is
// ALREADY UP does not start a second one. Measured reasoning is in `doBoot` - a sandbox boots once and
// every later stage says "this machine must be running", which it is.
func TestASecondBootOfTheSameRunningGuestIsANoOp(t *testing.T) {
	d := &fakeDriver{}
	ex, steps := newExec(d,
		map[string]any{"name": "boot", "boot": true, "os": "linux", "iso": "x.iso"},
		map[string]any{"name": "boot again", "boot": true, "os": "linux", "iso": "x.iso"},
	)
	ex.Work = t.TempDir()
	if _, err := ex.Run(steps); err != nil {
		t.Fatal(err)
	}
	if d.started != 1 {
		t.Fatalf("%d boots for a machine that was already up; the second must be a no-op", d.started)
	}
}

// TestADifferentGuestOnARunningMachineIsRefused is the guard: the second guest must not land on the
// first one's screen, because every click after it would be aimed at a pane that is not there.
func TestADifferentGuestOnARunningMachineIsRefused(t *testing.T) {
	d := &fakeDriver{}
	ex, steps := newExec(d,
		map[string]any{"name": "boot windows", "boot": true, "os": "windows", "iso": "win.iso"},
		map[string]any{"name": "boot linux without stopping", "boot": true, "os": "linux", "iso": "l.iso"},
	)
	ex.Work = t.TempDir()
	_, err := ex.Run(steps)
	if err == nil {
		t.Fatal("a second guest was started on a running machine; every click after it would be " +
			"aimed at a screen that is not the one the file describes")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Fatalf("the refusal does not say a machine is already up: %v", err)
	}
	_ = time.Second
}
