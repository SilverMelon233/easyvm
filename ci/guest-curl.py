#!/usr/bin/env python3
"""Ask the guest to curl one destination, through the daemon's `/api/exec`.

WHY THE GUEST AND NOT THE POD: the egress table is consulted when a connection crosses this daemon's
proxy or its tun. A curl run on the pod itself would exercise neither and would pass whatever the rules
said.

WHY `/api/exec` AND NOT `/api/shell`: the shell requests a pty, and a pty echoes every line it is sent.
MEASURED - a marker used to find the end of the output was matched by the echo of the command that was
about to print it, so the reader returned before the command had run. `/api/exec` has no pty, so the
output is the output.

THE EXIT STATUS IS PRINTED, NOT ASSERTED ON. A blocked destination and a dead destination both make
curl exit non-zero, and the test's evidence is the proxies' logs rather than the guest's exit code. What
this prints is the raw material for the workflow's log, and the assertions run there.
"""
import json
import sys
import urllib.request

TIMEOUT = 40


def main():
    if len(sys.argv) < 3:
        raise SystemExit("usage: guest-curl.py <api-url> <host>")
    api, host = sys.argv[1], sys.argv[2]

    # `wget` RATHER THAN `curl`, BECAUSE THE ALPINE ROOTFS HAS NO `curl` AND DOES NOT NEED ONE.
    #
    # MEASURED: `ash: curl: not found` (exit 127) for every request, and installing curl into a running
    # guest fails - `apk` cannot write its database as an unprivileged user:
    #
    #	ERROR: Unable to lock database: Permission denied
    #
    # `busybox` ships `wget` (`CONFIG_WGET=y` in Alpine's busyboxconfig) and `alpine-base` ships
    # busybox, so the client is already there. `-T 6` is wget's timeout and `-O -` writes the body to
    # stdout, which is what this needs to see.
    command = "wget -q -T 6 -O - http://%s/ 2>&1; echo EXIT:$?" % host
    req = urllib.request.Request(
        api + "/api/exec", method="POST",
        data=json.dumps({"command": command, "timeout_seconds": 20}).encode(),
        headers={"Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(req, timeout=TIMEOUT) as r:
            body = json.load(r)
    except Exception as e:
        print("--- %s: the daemon did not answer: %s" % (host, e))
        return

    print("--- %s" % host)
    if body.get("error"):
        print("    daemon: %s" % body["error"])
    out = (body.get("stdout") or "").strip()
    err = (body.get("stderr") or "").strip()
    if out:
        print("    stdout: %s" % out)
    if err:
        print("    stderr: %s" % err)
    if not out and not err:
        print("    (nothing - which is what a refused connection looks like)")


if __name__ == "__main__":
    main()
