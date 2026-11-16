package bootstrap

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"howett.net/plist"
)

// THE GENERATED FILES ARE SHELL AND PLIST, so they are CHECKED AS SUCH rather than by substring.
// A launcher with a missing `fi` is accepted by every string comparison and fails on the guest,
// where the failure is a login hook that silently does nothing - the exact symptom the Python
// implementation spent runs distinguishing from "the hook never ran".

func macOSOptions() Options {
	return Options{Family: MacOS, Host: "172.30.0.1", Share: "shared", User: "guest"}
}

func windowsOptions() Options {
	return Options{Family: Windows, Host: "172.30.0.1", Share: "shared", User: "guest"}
}

func writeTemp(t *testing.T, name, body string) string {
	t.Helper()
	path := t.TempDir() + "/" + name
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A SHELL SCRIPT THAT DOES NOT PARSE IS A HOOK THAT DOES NOTHING.
func TestShellScriptsParse(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no /bin/sh here to check with")
	}
	for _, tc := range []struct{ name, body string }{
		{"launcher.sh", Launcher(macOSOptions())},
		{"launcher-linux.sh", Launcher(Options{Family: Linux})},
	} {
		path := writeTemp(t, tc.name, tc.body)
		out, err := exec.Command(sh, "-n", path).CombinedOutput()
		if err != nil {
			t.Fatalf("%s does not parse: %v\n%s\n--- the script ---\n%s",
				tc.name, err, out, tc.body)
		}
	}
}

// A PLIST THAT DOES NOT PARSE IS AN AGENT LAUNCHD IGNORES.
func TestPlistsParse(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"agent.plist", MacOSAgentPlist("/Users/vmuser/bootstrap-launcher.sh")},
	} {
		var d map[string]any
		_, err := plist.Unmarshal([]byte(tc.body), &d)
		if err != nil {
			t.Fatalf("%s does not parse: %v\n--- the plist ---\n%s", tc.name, err, tc.body)
		}
		if d["Label"] == "" {
			t.Fatalf("%s has no Label", tc.name)
		}
		if d["RunAtLoad"] != true {
			t.Fatalf("%s must RunAtLoad; a login agent that does not is one that never fires",
				tc.name)
		}
		// KEEPALIVE FALSE, BECAUSE A BOOTSTRAP IS ONE SHOT. A supervisor would restart a failing
		// bootstrap forever, which on a published disk is a machine that never settles.
		if d["KeepAlive"] != false {
			t.Fatalf("%s must not KeepAlive", tc.name)
		}
	}
}

// THE AGENT RETRIES ONCE, AND THE INTERVAL IS PAST THE LAUNCHER'S OWN WAIT.
//
// `RunAtLoad` fires at one instant and that instant is not always usable: on run 42971367742 a
// Keyboard Setup Assistant appeared 36 seconds AFTER the password went in, so the session was
// still assembling itself when launchd ran the job. A shorter StartInterval would start a second
// copy while the first is still waiting for ~/shared.
func TestTheAgentRetriesPastTheLaunchersOwnWait(t *testing.T) {
	const launcherWait = 30 // seconds the launcher sleeps for, per the script
	var d map[string]any
	if _, err := plist.Unmarshal([]byte(MacOSAgentPlist("/x")), &d); err != nil {
		t.Fatal(err)
	}
	interval, ok := asInt(d["StartInterval"])
	if !ok {
		t.Fatalf("StartInterval is %T, want an integer", d["StartInterval"])
	}
	if interval <= launcherWait {
		t.Fatalf("StartInterval is %d, not past the launcher's own %d-second wait: the retry "+
			"would start a second copy while the first is still waiting", interval, launcherWait)
	}
}

// THE LAUNCHER FETCHES OVER HTTP AND DOES NOT WAIT FOR A MOUNT.
//
// THIS TEST REPLACES ONE THAT ASSERTED `mount_smbfs` BEHAVIOUR - an unmount, an rmdir, a "NOT_EMPTY"
// refusal and the `//guest:@host/share` spelling. All of that existed to make SMB work, and SMB is
// gone: FreeBSD's client could not connect to this server at all (netsmb speaks SMB1 on NetBIOS 139;
// smbd serves SMB3 on 445), and working around it took a ports package, a base filesystem, a
// root-only mount option and a mount table that names the bind.
//
// WHAT MATTERS NOW IS THAT THE FETCH RETRIES AND THAT ITS TWO FAILURES ARE DISTINGUISHABLE.
func TestTheLauncherFetchesOverHTTPAndRetries(t *testing.T) {
	body := Launcher(Options{Family: MacOS, ShareURL: "http://172.30.0.1:8080/shared/"})

	for _, want := range []struct{ text, why string }{
		{"http://172.30.0.1:8080/shared/", "the URL it fetches from must be the daemon's share"},
		{"curl -fsS", "a fetch that does not fail on an HTTP error would run an error page as a script"},
		{"--max-time", "a hung fetch must not hold the login hook open"},
		{"-o \"$FETCHED\"", "the body must land in a file, because sh runs a file"},
		{"after $i attempt(s)", "the log must say how many attempts were made, so \"not there YET\" " +
			"and \"not there\" do not look alike"},
	} {
		if !strings.Contains(body, want.text) {
			t.Fatalf("the launcher lost %q: %s", want.text, want.why)
		}
	}
	// AND IT MUST NOT STILL BE WAITING FOR A MOUNT: that is the mechanism this replaced.
	for _, gone := range []string{"mount_smbfs", "SHARED=", "~/shared/"} {
		if strings.Contains(body, gone) {
			t.Fatalf("the launcher still contains %q, which is the mount this no longer waits for", gone)
		}
	}
}

// THE LOG AND THE MARKER ARE NOT IN THE SHARE.
//
// The share is what is being served, so a file written back into it is one the next stage inherits -
// and a marker inherited that way makes a second run skip its own work.
func TestTheLogAndMarkerAreInTheProfileNotTheShare(t *testing.T) {
	for _, fam := range []Family{MacOS, Windows} {
		body := Launcher(Options{Family: fam, Host: "h", Share: "s", User: "guest"})
		if !strings.Contains(body, LogName) || !strings.Contains(body, MarkerName) {
			t.Fatalf("%s launcher does not name its log and marker", fam)
		}
		// The paths are under the home/profile, and the share is a separate variable.
		if fam == MacOS {
			if !strings.Contains(body, `LOG="$HOME/`+LogName+`"`) {
				t.Fatalf("the macOS log is not under $HOME:\n%s", body)
			}
		} else {
			if !strings.Contains(body, "Join-Path $env:USERPROFILE") {
				t.Fatalf("the Windows log is not under the profile:\n%s", body)
			}
		}
	}
}

// A FAILED FETCH MUST SAY WHAT THE SERVER ANSWERED, AND THE WINDOWS LAUNCHER USES ITS OWN CLIENT.
//
// THIS REPLACES A TEST THAT ASSERTED `New-SmbMapping` AND ITS `-ErrorAction Stop`. That whole branch
// existed to map an SMB share with no password, which Windows refuses by policy - and the mapping is
// gone. `Invoke-WebRequest` is in the box and needs no credential at all.
func TestTheWindowsLauncherFetchesAndRecordsTheAnswer(t *testing.T) {
	body := Launcher(Options{Family: Windows, ShareURL: "http://172.30.0.1:8080/shared/"})
	for _, want := range []struct{ text, why string }{
		{"Invoke-WebRequest", "PowerShell ships this; no client package and no credential"},
		{"-OutFile $Fetched", "the body must land in a file that powershell can run"},
		{"-ErrorAction Stop", "a refusal must be an exception, not a warning the status never sees"},
		{`Say ("fetch -> "`, "the failure must reach the log"},
		{"after $i attempt(s)", "the log must say how many attempts were made"},
	} {
		if strings.Contains(body, want.text) == false {
			t.Fatalf("the windows launcher lost %q: %s", want.text, want.why)
		}
	}
	if strings.Contains(body, "New-SmbMapping") || strings.Contains(body, "net use ") {
		t.Fatal("the windows launcher still maps an SMB share; the share is HTTP now")
	}
}

// THE TASK IS LIMITED, NOT HIGHEST, AND IT SAYS SO WHEN SCHTASKS REFUSES.
func TestTheTaskIsLimitedAndReportsRefusal(t *testing.T) {
	body := WindowsTaskScript(`C:/Users/vmuser/bootstrap-launcher.ps1`)
	if !strings.Contains(body, "/RL LIMITED") {
		t.Fatal("the task must be /RL LIMITED: it writes to the profile and the share, and " +
			"/RL HIGHEST would refuse to start for a non-administrator")
	}
	if !strings.Contains(body, "/SC ONLOGON") {
		t.Fatal("the task must be ONLOGON; the launcher runs when a session exists")
	}
	if !strings.Contains(body, "SCHTASKS_FAILED") {
		t.Fatal("a refused schtasks must be named; a task that was never registered otherwise " +
			"reads as a hook that did nothing")
	}
	// AND THE ACTION QUOTES THE PATH, because the launcher's path is under a profile that may
	// contain a space.
	if !strings.Contains(body, "-File `\"$launcher`\"") {
		t.Fatalf("the action must quote the launcher path; got:\n%s", body)
	}
}

func asInt(v any) (int, bool) {
	switch t := v.(type) {
	case int64:
		return int(t), true
	case uint64:
		return int(t), true
	case int:
		return t, true
	}
	return 0, false
}
