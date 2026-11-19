#!/usr/bin/env python3
"""Apply the four-outcome policy the egress end-to-end test asserts on.

WHY THIS IS A FILE RATHER THAN A HEREDOC IN THE WORKFLOW: YAML block scalars do not strip the
indentation they require, so a `python3 - <<'PY'` inside `run: |` arrives at the interpreter with ten
leading spaces on every line and fails on its own first statement. The alternatives - `<<-` (which only
strips tabs) and a `python3 -c` one-liner - are worse to read than this file.
"""
import json
import sys
import urllib.request

API = "http://127.0.0.1:8081"
TIMEOUT = 30


def main():
    if len(sys.argv) < 3:
        raise SystemExit("usage: apply-policy.py <primary-port> <backup-port>")
    primary, backup = sys.argv[1], sys.argv[2]

    policy = {
        # AN UNMATCHED DESTINATION IS REFUSED. This is the half that catches a rule engine silently
        # allowing what it did not understand, and it is what makes the fourth assertion meaningful.
        "default": "block",
        "upstreams": [
            # `default` IS THE ONE THE DAEMON WAS STARTED WITH, restated because a POST replaces the
            # table: without it, a rule that says plain `proxy` would name an upstream that no longer
            # exists.
            {"name": "default", "http": "127.0.0.1:" + primary,
             "socks5": "127.0.0.1:" + str(int(primary) + 1000)},
            {"name": "primary", "http": "127.0.0.1:" + primary},
            {"name": "backup", "http": "127.0.0.1:" + backup},
        ],
        "rules": [
            {"match": "203.0.113.10", "action": "proxy:primary"},
            {"match": "203.0.113.20", "action": "proxy:backup"},
            {"match": "203.0.113.30", "action": "direct"},
        ],
    }
    req = urllib.request.Request(
        API + "/api/egress", method="POST",
        data=json.dumps(policy).encode(),
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=TIMEOUT) as r:
        body = json.load(r)
    print(json.dumps(body, indent=2)[:1200])
    if not body.get("ok"):
        raise SystemExit("the daemon refused the policy: %s" % body.get("error"))
    if body.get("unresolved"):
        # A NAME THAT DID NOT RESOLVE IS NOT A FAILURE of the policy, but this test uses literals only
        # - so an unresolved entry means something changed that this test should not silently accept.
        raise SystemExit("unexpected unresolved names: %s" % body["unresolved"])


if __name__ == "__main__":
    main()
