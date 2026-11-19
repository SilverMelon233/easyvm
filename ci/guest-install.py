#!/usr/bin/env python3
"""Install one package in the guest, through `/api/exec`.

WHY NOT IN THE ROOTFS: adding a package to `alpine-rootfs.sh` would rebuild `rootfs-alpine` - a
published artifact every consumer pulls - for a dependency only this test has. The guest is a throwaway
machine in a CI job, and installing into it is what a person would do.

WHY `doas` THEN `apk`: the guest's ssh account is unprivileged, and `doas` may not be installed either.
The fallback is a plain `apk`, which succeeds when the account is root.
"""
import json
import sys
import urllib.request

API = "http://127.0.0.1:8081"


def main():
    if len(sys.argv) < 2:
        raise SystemExit("usage: guest-install.py <package>")
    pkg = sys.argv[1]
    cmd = "doas apk add --no-cache %s || apk add --no-cache %s" % (pkg, pkg)
    req = urllib.request.Request(
        API + "/api/exec", method="POST",
        data=json.dumps({"command": cmd, "timeout_seconds": 120}).encode(),
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=140) as r:
        body = json.load(r)
    print("exit:", body.get("exit_code"))
    for stream in ("stdout", "stderr"):
        text = (body.get(stream) or "").strip()
        if text:
            print("--- %s ---" % stream)
            print(text[-600:])
    if body.get("exit_code") != 0:
        raise SystemExit("could not install %s in the guest" % pkg)


if __name__ == "__main__":
    main()
