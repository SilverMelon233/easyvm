#!/usr/bin/env python3
"""Type GRUB commands at a running guest's `grub>` prompt, one key at a time.

    python3 grubtype.py /disks/os/bliss/lqmp.sock out.png "ls (hd0,gpt2)/"

WHY ONE KEY AT A TIME, WITH A PICTURE AFTER EACH LINE

GRUB's line editor has no paste and no line discipline this side can rely on: `sendkey` delivers
one qcode, and a character that is not on a US layout has no qcode at all. So the map below is
explicit and a character missing from it is an error rather than a silent skip - the first attempt
at this dropped `(` and `)` and typed `ls hd0,gpt2/`, which GRUB answered with `Unknown command`.
"""
import json
import socket
import sys
import time

# The qcodes `sendkey` accepts. Shifted characters are spelled `shift-<key>`.
KEYS = {
    " ": "spc", "-": "minus", "=": "equal", ".": "dot", "/": "slash", ",": "comma",
    "(": "shift-9", ")": "shift-0", "_": "shift-minus", "*": "shift-8", "'": "apostrophe",
    ":": "shift-semicolon", "$": "shift-4", "{": "shift-bracket_left",
    "}": "shift-bracket_right",
}
for c in "abcdefghijklmnopqrstuvwxyz0123456789":
    KEYS[c] = c
for c in "ABCDEFGHIJKLMNOPQRSTUVWXYZ":
    KEYS[c] = "shift-" + c.lower()


class Qmp:
    def __init__(self, path):
        self.s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.s.settimeout(60)
        self.s.connect(path)
        self.fh = self.s.makefile("rwb")
        self._reply()
        self.send({"execute": "qmp_capabilities"})

    def _reply(self):
        while True:
            line = self.fh.readline()
            if not line:
                raise SystemExit("the QMP socket closed")
            msg = json.loads(line)
            if "event" in msg:
                continue
            if "error" in msg:
                raise SystemExit(f"qmp: {msg['error'].get('desc')}")
            return msg

    def send(self, cmd):
        self.fh.write((json.dumps(cmd) + "\n").encode())
        self.fh.flush()
        return self._reply()

    def key(self, name):
        self.send({"execute": "human-monitor-command",
                   "arguments": {"command-line": f"sendkey {name}"}})
        time.sleep(0.04)

    def line(self, text):
        for ch in text:
            name = KEYS.get(ch)
            if name is None:
                raise SystemExit(f"no qcode for {ch!r} in {text!r}")
            self.key(name)
        self.key("ret")

    def shot(self, dest):
        self.send({"execute": "screendump",
                   "arguments": {"filename": dest, "format": "png"}})


def main(argv):
    sock, dest, *lines = argv
    q = Qmp(sock)
    for line in lines:
        q.line(line)
        time.sleep(1.5)
    q.shot(dest)
    print("ok")


if __name__ == "__main__":
    main(sys.argv[1:])
