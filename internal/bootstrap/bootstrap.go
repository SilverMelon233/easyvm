// Package bootstrap writes the files that make a share run itself at login.
//
// THE SPLIT IS THE SAFETY PROPERTY OF THIS PACKAGE: it only WRITES. The `scp:` step after each one
// moves the file in, and the `ssh:` step after that runs it. A single step that wrote, copied and
// ran would report `the launcher install exited 1` and name neither the file nor the action - which
// is what the Python implementation's first version did.
//
// THE SCRIPT COMES FROM THE SHARE; THE LOG AND THE MARKER DO NOT. Anything the launcher writes goes
// under the guest's own home, never into the share, because the share is what is being served and a
// file written back into it would be a file the next stage inherits.
package bootstrap

import (
	"fmt"
	"strings"
)

// Family is which guest the files are for.
type Family string

const (
	Windows Family = "windows"
	MacOS   Family = "macos"
	Linux   Family = "linux"
)

// ScriptName is the file a guest looks for in the share.
func ScriptName(f Family) string {
	if f == Windows {
		return "bootstrap.ps1"
	}
	return "bootstrap.sh"
}

// LauncherName is the launcher written on the host and copied in.
func LauncherName(f Family) string {
	if f == Windows {
		return "bootstrap-launcher.ps1"
	}
	return "bootstrap-launcher.sh"
}

// The two files the launcher keeps in the guest's own home, never in the share.
const (
	LogName    = "bootstrap.log"
	MarkerName = "bootstrap.done"
)

// Options is everything a launcher needs to know, and every field is a fact about the run.
type Options struct {
	// Family is which guest this is for.
	Family Family
	// Host is the address the share is served on - the bridge, 172.30.0.1.
	Host string
	// Share is the share's name.
	Share string
	// User and Password are the share account. The password is on the command line deliberately:
	// the guest is a throwaway VM on a private bridge, and the alternative - a credential file - is
	// one more thing to copy in before the mount can be attempted.
	User     string
	Password string
	// ShareURL is the directory the script is fetched from, as a URL - `http://172.30.0.1:8080/shared/`.
	//
	// THE SHARE IS HTTP RATHER THAN SMB, and this field is what the launcher fetches. It used to be a
	// MOUNT, and every guest needed a different client for it: `mount -t cifs` on Linux, Apple's
	// mount_smbfs on macOS, `net use` on Windows - and on FreeBSD NOTHING IN THE BASE SYSTEM COULD
	// CONNECT, because netsmb speaks SMB1 on NetBIOS 139 while smbd serves SMB3 on 445. HTTP needs no
	// package, no kernel module, no mount and no privilege, and every guest already has curl or its
	// own equivalent.
	ShareURL string
}

// Launcher is the script a guest runs at login. It waits for the share, runs the script on it once,
// and records what happened.
//
// EVERY FAILURE IS WRITTEN DOWN AND NONE OF THEM FAIL LOUDLY. A login hook that does nothing is the
// correct behaviour for a machine nobody has configured yet, and a hook that fails visibly on a
// published disk reads as a broken disk rather than as a share that was not there.
func Launcher(o Options) string {
	if o.Family == Windows {
		return windowsLauncher(o)
	}
	return posixLauncher(o)
}

// posixLauncher is the macOS and Linux form.
func posixLauncher(o Options) string {
	script := ScriptName(o.Family)
	return fmt.Sprintf(`#!/bin/sh
# Run the share's %[1]s at login, once, and leave a log beside it.
#
# INSTALLED BY THIS REPOSITORY. It is deliberately a shell script in the guest: the disk carries no
# Python, and an interpreter would be one more thing the fetch has to wait for.
#
# THE SCRIPT COMES OVER HTTP, NOT FROM A MOUNT. This used to wait for ~/shared to be mounted - SMB on
# every platform, with a different client each time - and FreeBSD's client could not connect at all:
# netsmb speaks SMB1 on NetBIOS 139 while smbd serves SMB3 on 445, so the kernel got a TCP RST. The
# daemon serves the same directory on its own port, at %[4]s, and every guest already has a way to
# fetch a URL. No package, no kernel module, no mount, no privilege.
#
# THE LOG AND THE MARKER ARE NOT IN THE SHARE, because the share is what is being served: a file
# written back into it would be one the next stage inherits.
LOG="$HOME/%[2]s"
MARKER="$HOME/%[3]s"
URL="%[4]s%[1]s"
FETCHED="$HOME/.bootstrap-script"

say() { echo "$@" >> "$LOG"; }

# A MARKER FROM A PREVIOUS LOGIN MEANS THIS HAS ALREADY RUN. Removing it is how a stage asks for a
# second run, and nothing else does.
[ -f "$MARKER" ] && exit 0

# THE SERVER MAY NOT BE READY YET. A login trigger can fire before the daemon is listening, so this
# retries rather than checking once - and "not there YET" and "not there" must not look alike, which
# is why the message at the end says how many attempts were made.
i=0
while [ "$i" -lt 30 ]; do
    i=$((i + 1))
    if curl -fsS --max-time 10 "$URL" -o "$FETCHED" 2>>"$LOG"; then
        break
    fi
    sleep 1
done

# THE FALLBACK: NO SCRIPT, NO RUN - and say so, because "the hook ran and nothing happened" is
# otherwise indistinguishable from "the hook never ran".
if [ ! -f "$FETCHED" ]; then
    say "[$(date)] fetched nothing from $URL after $i attempt(s); nothing to run"
    exit 0
fi

say "[$(date)] fetched $URL"
if /bin/sh "$FETCHED" >> "$LOG" 2>&1; then
    # THE MARKER IS WRITTEN ONLY ON SUCCESS, so a failing bootstrap tries again at the next login.
    date > "$MARKER"
    say "[$(date)] ok"
else
    say "[$(date)] FAILED; it will run again at the next login"
fi
`, script, LogName, MarkerName, o.ShareURL)
}

// windowsLauncher is the Windows form.
//
// A NETWORK LOGON CANNOT REUSE A STORED CREDENTIAL. `cmdkey` answers ERROR_NO_SUCH_LOGON_SESSION -
// "Network logon sessions do not have an associated credential set" - and Win32-OpenSSH hardcodes
// that logon type, so a credential written from an ssh session could not exist.
//
// WHAT IT CAN DO IS OPEN A SESSION WITH AN EXPLICIT ONE, inside this process. That is why the
// credential is here and not in the credential vault, and why the mount is `New-SmbMapping` rather
// than `net use`: the account has no password, and a positional empty argument collapses in
// PowerShell - every argument after it shifts left and `net use` reads a switch as the password.
func windowsLauncher(o Options) string {
	script := ScriptName(o.Family)
	return fmt.Sprintf(`# Run the share's %[1]s at logon, once, and leave a log beside it.
#
# INSTALLED BY THIS REPOSITORY, started by Task Scheduler through
# `+"`powershell.exe -WindowStyle Hidden`"+`, so no console appears.
#
# THE SCRIPT COMES OVER HTTP, NOT FROM A MAPPED SHARE. It used to map the daemon's smbd with a
# stored credential, which meant a logon-type workaround and a drive-less mapping rebuilt on every
# retry. The daemon serves the same directory on its own port, and Windows already ships the client:
# Invoke-WebRequest.
$Base   = '%[3]s'
$Script = '%[3]s%[1]s'

# THE SCRIPT COMES FROM THE URL; THE LOG AND THE MARKER DO NOT.
$Log     = Join-Path $env:USERPROFILE '%[4]s'
$Marker  = Join-Path $env:USERPROFILE '%[5]s'
$Fetched = Join-Path $env:USERPROFILE '.bootstrap-script.ps1'

function Say($message) {
    Add-Content -Path $Log -Value "$(Get-Date -Format 'yyyy-MM-dd HH:mm:ss') $message" `+"`"+`
        -ErrorAction SilentlyContinue
}

# A MARKER FROM A PREVIOUS LOGON MEANS THIS HAS ALREADY RUN.
if (Test-Path $Marker) { exit 0 }

# THE SERVER MAY NOT BE READY YET, so this retries - and reports how many attempts it made, because
# "not there YET" and "not there" must not look alike.
$got = $false
for ($i = 1; $i -le 30; $i++) {
    try {
        Invoke-WebRequest -Uri $Script -OutFile $Fetched -UseBasicParsing -TimeoutSec 10 -ErrorAction Stop
        $got = $true
        break
    } catch {
        if ($i -eq 30) { Say ("fetch -> " + $_.Exception.Message) }
        Start-Sleep -Seconds 1
    }
}

if (-not $got) {
    Say "fetched nothing from $Script after $i attempt(s); nothing to run"
    exit 0
}

Say "fetched $Script"
try {
    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $Fetched *>&1 |
        ForEach-Object { Say $_ }
    if ($LASTEXITCODE -ne 0) { throw "exit $LASTEXITCODE" }
    (Get-Date).ToString('o') | Set-Content -Path $Marker
    Say "ok"
} catch {
    Say ("FAILED: " + $_.Exception.Message + "; it will run again at the next logon")
}
`, script, o.User, o.ShareURL, LogName, MarkerName)
}

// MacOSAgentPlist is the LaunchAgent that runs the launcher at login.
//
// `ProgramArguments` NAMES /bin/sh AND THE LAUNCHER rather than the launcher alone: launchd runs a
// programme and does not consult a shebang.
//
// TWO THINGS IN HERE ARE LOAD-BEARING AND BOTH WERE MEASURED:
//
//	RunAtLoad     fires at one instant, and that instant is not always usable - a Keyboard Setup
//	              Assistant appeared 36 seconds after the password went in on the recorded run.
//	StartInterval 90 is that retry. It is PAST the launcher's own one-minute wait for ~/shared, so
//	              the retry cannot overlap the first attempt, and the probe waits 180s, so at most
//	              two fit. A shorter interval would start a second copy while the first waits.
func MacOSAgentPlist(launcherPath string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>org.vm.bootstrap</string>
    <key>ProgramArguments</key>
    <array>
        <string>/bin/sh</string>
        <string>%s</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>StartInterval</key>
    <integer>90</integer>
    <key>KeepAlive</key>
    <false/>
    <key>StandardOutPath</key>
    <string>/tmp/org.vm.bootstrap.out</string>
    <key>StandardErrorPath</key>
    <string>/tmp/org.vm.bootstrap.err</string>
</dict>
</plist>
`, launcherPath)
}

// WindowsTaskScript registers the logon task.
//
// `/RL LIMITED` AND NOT `/RL HIGHEST`: the hook does not need elevation - it writes to the user's own
// profile and to the share - and a task that asked for it would fail to start for a user who was not
// in the administrators group.
func WindowsTaskScript(launcherPath string) string {
	// THE BACKTICK IS POWERSHELL'S LINE-CONTINUATION AND QUOTE-ESCAPE, and Go's raw strings cannot
	// contain one - so the body is built by joining lines and the two backticks are named.
	bt := "`"
	lines := []string{
		"# Register the logon task that runs the share's bootstrap once.",
		"#",
		"# INSTALLED BY THIS REPOSITORY.",
		"$ErrorActionPreference = 'Stop'",
		"",
		`$launcher = "` + launcherPath + `"`,
		`$action = "powershell.exe -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File ` +
			bt + `"$launcher` + bt + `""`,
		"",
		"schtasks /Create /F /TN \"vm-bootstrap\" /TR $action /SC ONLOGON /RL LIMITED | Out-String | Write-Output",
		"",
		"if ($LASTEXITCODE -ne 0) {",
		"    Write-Output \"SCHTASKS_FAILED: $LASTEXITCODE\"",
		"    exit 1",
		"}",
		"Write-Output \"BOOTSTRAP_TASK_REGISTERED\"",
	}
	// /RL LIMITED AND NOT /RL HIGHEST: the hook does not need elevation - it writes to the user's own
	// profile and to the share - and a task that asked for it would fail to start for a user who was
	// not in the administrators group.
	return strings.Join(lines, "\n") + "\n"
}
