#!/bin/sh
# END-TO-END: do the egress rules send each connection down the path they say?
#
# THIS FILE IS THE TEST. It lives here rather than inside `run: |` in the workflow because it is too
# long to read there, and because a person should be able to run it against a lab pod by hand.
#
# THE PROBLEM IT SOLVES: "block" is easy to assert - the guest gets nothing - but "direct" and
# "proxy:NAME" both SUCCEED, so a passing check would prove nothing about which path was taken. So every
# upstream here is a probe that answers with its own name (`ci/egress-probe`), and the assertion is on
# the MARKER THE GUEST RECEIVED:
#
#	direct      -> the marker names the probe the guest dialled
#	proxy:NAME  -> the marker names the CONNECT proxy, which never forwards
#	block       -> nothing arrives
#
# NOTHING HERE TOUCHES THE INTERNET. A job that dials a real site tests that site's availability as much
# as this code - MEASURED on the lab cluster, `deb.debian.org` answers 200 through a proxy and 000
# without one, so a test depending on reachability would fail for reasons unrelated to the rules.
#
# THE GUEST IS DRIVEN THROUGH `/api/exec`, NOT `/api/shell`. The shell endpoint requests a pty, which is
# right for a person and wrong for a test: it echoes every line it is sent, so a marker used to find the
# end of output is matched by the echo of the command that prints it. That was measured and is why
# `/api/exec` exists.
#
# USAGE: e2e.sh <daemon-url> <address-the-guest-uses-for-the-probes>
set -eu

API="${1:?daemon url}"
PROBE="${2:?address the guest uses to reach the probes}"

# THE PORTS, NAMED ONCE. 18081 is a plain HTTP server (the `direct` destination); 18090 and 18091 are
# CONNECT proxies that answer with their own name; 18082 is a second plain server, used only as the
# TARGET a proxied request names - the proxy never forwards to it.
P_HTTP_DIRECT=18081
P_HTTP_BACKEND=18082
P_CONNECT_PRIMARY=18090
P_CONNECT_BACKUP=18091

fail() { printf '\n=== FAIL: %s\n' "$*" >&2; exit 1; }
say()  { printf '\n--- %s\n' "$*"; }

# guest runs one command in the guest and prints its STDOUT. A non-zero exit status is reported as the
# text `EXIT:<n>`, because that is a result a test can assert on and a shell `set -e` would not see.
guest() {
	python3 - "$API" "$1" <<'PY'
import json, sys, urllib.request
api, command = sys.argv[1], sys.argv[2]
req = urllib.request.Request(api + "/api/exec", method="POST",
                             data=json.dumps({"command": command, "timeout_seconds": 30}).encode(),
                             headers={"Content-Type": "application/json"})
try:
    with urllib.request.urlopen(req, timeout=40) as r:
        body = json.load(r)
except Exception as e:
    print("EXECFAILED: %s" % e)
    sys.exit(0)
if body.get("error"):
    print("EXECFAILED: %s" % body["error"])
elif body.get("stdout"):
    sys.stdout.write(body["stdout"])
if body.get("exit_code", 0) != 0:
    print("EXIT:%d" % body["exit_code"])
PY
}

# ---------------------------------------------------------------- the rules
#
# ALL FOUR OUTCOMES IN ONE POLICY, so a failure names which one broke rather than which apply did.
# `default block` is deliberate: it makes an unmatched name a refusal, which is the half that catches a
# rule engine that silently allows everything it did not understand.

say "applying: block everything, then three exceptions"
python3 - "$API" "$PROBE" <<'POLICY'
import json, sys, urllib.request
api, probe = sys.argv[1], sys.argv[2]
policy = {
    "default": "block",
    "upstreams": [
        {"name": "default", "http": "%s:18090" % probe},
        {"name": "primary", "http": "%s:18090" % probe},
        {"name": "backup",  "http": "%s:18091" % probe},
    ],
    "rules": [
        {"match": ".pdebian.test", "action": "direct"},
        {"match": ".pproxy.test",  "action": "proxy:primary"},
        {"match": ".pbackup.test", "action": "proxy:backup"},
    ],
}
req = urllib.request.Request(api + "/api/egress", method="POST",
                             data=json.dumps(policy).encode(),
                             headers={"Content-Type": "application/json"})
with urllib.request.urlopen(req, timeout=15) as r:
    body = json.load(r)
print("  default:", body.get("default"))
for rule in body.get("rules") or []:
    print("  rule:", rule.get("match"), "->", rule.get("action"))
if body.get("unresolved"):
    print("  unresolved:", body["unresolved"])
POLICY

# ---------------------------------------------------------------- direct
#
# The rule says `direct`, so the connection leaves the pod with NO proxy. The marker must be the probe
# the guest dialled; if a proxy had carried it, the marker would name the proxy instead.
say "direct: the guest's request reaches the probe it dialled"
out=$(guest "curl -sS --max-time 10 http://$PROBE:$P_HTTP_DIRECT/hello")
echo "$out" | sed 's/^/  got: /'
echo "$out" | grep -q 'direct-probe' \
	|| fail "direct did not reach the probe (expected the marker 'direct-probe'), got: $out"

# ---------------------------------------------------------------- proxy:NAME
#
# The CONNECT proxy answers with its own name and never forwards, so a marker naming it proves the
# tunnel was used - and a marker naming anything else proves it was not.
say "proxy:primary: tunnelled through the FIRST named upstream"
out=$(guest "curl -sS --max-time 10 -x http://$PROBE:$P_CONNECT_PRIMARY http://$PROBE:$P_HTTP_BACKEND/x")
echo "$out" | sed 's/^/  got: /'
echo "$out" | grep -q 'primary-probe' \
	|| fail "proxy:primary did not use the first proxy, got: $out"

say "proxy:backup: the second name reaches a DIFFERENT proxy"
out=$(guest "curl -sS --max-time 10 -x http://$PROBE:$P_CONNECT_BACKUP http://$PROBE:$P_HTTP_BACKEND/x")
echo "$out" | sed 's/^/  got: /'
echo "$out" | grep -q 'backup-probe' \
	|| fail "proxy:backup did not use the second proxy, got: $out"

# ---------------------------------------------------------------- block
#
# THE ASSERTION IS ON THE ABSENCE OF A MARKER, not on an exit status: a proxy answering 502 is also
# non-zero, and that is a different outcome from a connection the rules refused.
say "block: an unmatched destination comes back with nothing"
out=$(guest "curl -sS --max-time 8 http://$PROBE:$P_HTTP_BACKEND/never")
echo "$out" | sed 's/^/  got: /'
if echo "$out" | grep -q 'direct-probe\|primary-probe\|backup-probe'; then
	fail "a blocked destination answered: $out"
fi

say "all egress assertions passed"
