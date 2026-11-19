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

    command = "curl -sS --max-time 6 http://%s/ ; echo EXIT:$?" % host
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
