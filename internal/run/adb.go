package run

// THE THREE ADB STEPS, FOR A GUEST WHOSE REMOTE ACCESS IS adbd RATHER THAN sshd.
//
// Android HAS NO sshd AND DOES NOT NEED ONE. Its adbd is already running on 5555 when the system
// comes up, and it answers this repository's own client over the tap network:
//
//	measured on a BlissOS guest this repository installed
//	sent     CNXN host::
//	device   CNXN version=0x1000001 maxdata=4096 banner=...ro.product.model=Generic Android-x86_64
//	         features=shell_v2,cmd,stat_v2,ls_v2,apex,abb,remount_shell,sendrecv_v2,...
//
// CNXN AND NOT AUTH: `ro.adb.secure=0` on that image, so no RSA key and no "Allow USB debugging?"
// dialog. A guest built with secure adb answers AUTH and this reports that as a sentence.
//
// WHY THE THREE KEYS AND NOT ONE. They are the same three facts ssh has: is there a shell, is it
// root, and can it run this. `adb_connect:` PROVES there is one, and it is a step of its own for the
// reason `require_ssh:` is: a walk whose first `adb:` fails spends its budget on a machine that was
// never reachable, and the failure names the command instead of the connection.

import (
	"fmt"
	"strings"
	"time"

	"github.com/easydev-org/easyvm/internal/adb"
	"github.com/easydev-org/easyvm/internal/walk"
)

// adbGuest is the connection a step asked for, kept on the executor beside the ssh one.
//
// IT IS A FIELD RATHER THAN A Driver METHOD because adb is one family's transport, not a property of
// every machine: a Windows guest has no adbd and a `Driver.ADB()` would be a method that five of six
// implementations answer with nil.
type adbGuest struct {
	client *adb.Client
	addr   string
	host   string
	port   int
}

// doADBConnect attaches to the guest's adbd and proves it answers.
func (e *Executor) doADBConnect(s walk.Step) error {
	addr := s.Get("adb_serial")
	if addr == "" {
		// THE GUEST'S OWN ADDRESS ON THE TAP, WHICH IS THE ONE `boot:` MADE.
		//
		// The bridge is 172.30.0.1 and the first lease is 172.30.0.2 - measured on every tap guest
		// this repository has started. A caller that needs another address says `adb_serial:`.
		addr = "172.30.0.2:5555"
	}
	host, port := addr, 5555
	if i := strings.LastIndexByte(addr, ':'); i >= 0 {
		host = addr[:i]
		fmt.Sscanf(addr[i+1:], "%d", &port)
	}

	timeout := 300 * time.Second
	if v, err := s.Float("timeout", 300); err == nil && v > 0 {
		timeout = time.Duration(v * float64(time.Second))
	}
	// IT WAITS, FOR THE REASON `require_ssh:` WAITS: adbd is started by the system, and a `boot:`
	// step is over when qemu is running - which is minutes before Android has finished booting. A
	// single attempt measured `no route to host` on a guest that came up thirty seconds later.
	e.Driver.Log(fmt.Sprintf("  waiting up to %s for adbd on %s", timeout, addr))
	deadline := time.Now().Add(timeout)
	var client *adb.Client
	var lastErr error
	for attempt := 1; ; attempt++ {
		client, lastErr = adb.Dial(host, port, 20*time.Second)
		if lastErr == nil {
			break
		}
		if time.Now().After(deadline) {
			return fail(s.Name, "no adbd on %s within %s: %v. An android guest runs adbd on 5555 "+
				"once the system is up, and a `boot:` with a tap network is what makes it "+
				"reachable", addr, timeout, lastErr)
		}
		if attempt == 1 || attempt%5 == 0 {
			e.Driver.Log(fmt.Sprintf("  still waiting (%v)", lastErr))
		}
		time.Sleep(5 * time.Second)
	}
	e.adb = &adbGuest{client: client, addr: addr, host: host, port: port}
	e.Driver.Log(fmt.Sprintf("  adb: attached to %s", addr))
	e.Driver.Log(fmt.Sprintf("  adb: %s", client.Banner))

	// AND IT HAS TO ANSWER A COMMAND, not merely accept a socket. The handshake proves a device is
	// there; the command proves it can run one, which is what every later step needs.
	out, code, err := client.Run("echo adb-ok", timeout)
	if err != nil {
		return fail(s.Name, "the handshake succeeded and the first command failed: %v", err)
	}
	if code != 0 || !strings.Contains(out, "adb-ok") {
		return fail(s.Name, "`echo adb-ok` exited %d and printed %q", code, out)
	}
	return nil
}

// doADBPrivileged proves the adb shell can change system state, and says which uid it is.
//
// IT IS NOT `adb root`, AND THE MEASUREMENT IS WHY. `adb root` is a service adbd offers only when it
// was built with `ro.debuggable=1`; this guest answers it by restarting into the same uid it already
// had. What the step is FOR is the thing every later step needs:
//
//	MEASURED on a BlissOS guest this repository installed
//	id                    uid=2000(shell) ... context=u:r:shell:s0
//	which su              nothing - /system/{bin,xbin}/su and /sbin/su are all absent
//	appops set ... deny   exit 0, and `appops get` then reads back "deny"
//	appops set ... allow  exit 0, and it reads back "allow"
//	cmd package set-home-activity ...   "Success"
//	settings put screen_off_timeout     reads back
//
// SO uid 2000 IS ENOUGH: Android gives the adb shell the SELinux domain `u:r:shell:s0`, which owns
// `appops`, `settings`, `cmd` and `setprop`. A check that DEMANDED root would refuse a guest on which
// every one of those works - which is exactly what the first version of this step did.
//
// THE PROOF IS A WRITE THAT READS BACK, not an identity. `id -un` says who you are; it does not say
// what you can do, and the two are different questions on Android.
func (e *Executor) doADBPrivileged(s walk.Step) error {
	if e.adb == nil {
		return fail(s.Name, "no adb connection: an `adb_connect:` step has to come first")
	}
	reconnect := func() error {
		return e.adb.client.Reconnect(e.adb.host, e.adb.port, 20*time.Second)
	}
	run := func(cmd string) (string, int, error) {
		if err := reconnect(); err != nil {
			return "", -1, err
		}
		return e.adb.client.Run(cmd, 30*time.Second)
	}

	who, code, err := run("id")
	if err != nil {
		return fail(s.Name, "%v", err)
	}
	if code != 0 {
		return fail(s.Name, "`id` exited %d: %q", code, who)
	}

	// A WRITE AND A READ. `settings put system screen_off_timeout` is idempotent, so setting it here
	// is the same value the next step sets - the point is that the shell can change a system setting
	// at all, and that reading it back agrees.
	const probe = "2147483647"
	if _, code, err := run("settings put system screen_off_timeout " + probe); err != nil || code != 0 {
		return fail(s.Name, "the adb shell cannot change a system setting (exit %d, %v): %s",
			code, err, firstLineOf(who))
	}
	back, code, err := run("settings get system screen_off_timeout")
	if err != nil || code != 0 || strings.TrimSpace(back) != probe {
		return fail(s.Name, "`settings put` exited %d and reading it back gave %q, want %q: the "+
			"shell can reach the settings service but cannot change it", code, strings.TrimSpace(back),
			probe)
	}

	e.Driver.Log(fmt.Sprintf("  adb: the shell can change system state (%s)",
		strings.SplitN(strings.TrimSpace(who), " ", 2)[0]))
	return nil
}

// doADB runs one command in the guest and fails on a non-zero exit.
func (e *Executor) doADB(s walk.Step) error {
	if e.adb == nil {
		return fail(s.Name, "no adb connection: an `adb_connect:` step has to come first")
	}
	command := s.Get("adb")
	if command == "" {
		return fail(s.Name, "`adb:` names no command")
	}
	timeout := 60 * time.Second
	if v, err := s.Float("timeout", 60); err == nil && v > 0 {
		timeout = time.Duration(v * float64(time.Second))
	}
	// EACH COMMAND STARTS ON A FRESH SOCKET. Measured on this guest: four commands down ONE
	// connection gave the first right, the second an exit of -1, the third the SECOND's output, and
	// the fourth nothing - because a closed adb stream's packets are still in flight when the next
	// stream opens, and the ids no longer line up.
	if err := e.adb.client.Reconnect(e.adb.host, e.adb.port, timeout); err != nil {
		return fail(s.Name, "reconnecting to adbd: %v", err)
	}
	out, code, err := e.adb.client.Run(command, timeout)

	// THE COMMAND, ITS EXIT STATUS AND ITS OWN OUTPUT, in the shape `ssh:` logs - a step that fails
	// has to say what the guest said, not only that it failed.
	e.Driver.Log(fmt.Sprintf("  adb: %s -> %d", oneLine(command), code))
	for _, line := range headAndTail(out, 8, 4) {
		e.Driver.Log("    " + line)
	}
	if err != nil {
		return fail(s.Name, "%v", err)
	}
	if code != 0 {
		return fail(s.Name, "`%s` exited %d in the guest: %s", oneLine(command), code,
			firstLineOf(out))
	}
	return nil
}
