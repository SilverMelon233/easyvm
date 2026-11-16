#!/usr/bin/env python3
"""Click absolute coordinates on a qemu guest over QMP, then screenshot.

    python3 qmpclick.py SOCK OUT.png 510,557 824,690

WHY ABSOLUTE AND NOT KEYBOARD: Android's "Select a Home app" chooser is a pointer-driven dialog. It
has no keyboard focus ring, and arrow keys move nothing - measured on the first attempt, where four
`down` presses and a Return left the chooser exactly as it was.

`input-send-event` WITH AN ABSOLUTE AXIS IS THE API, and it needs `usb-tablet` on the guest, which
`boot:` attaches for every family: a PS/2 mouse reports relative motion and an absolute event sent to
it is refused with "Input handler not found for event type abs".
"""
import json
import socket
import sys
import time


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

    def click(self, x, y):
        # THE MOVE AND THE BUTTON ARE SEPARATE EVENTS: a click sent as one event with the position
        # attached lands at the pointer's PREVIOUS position on some guests, because the pointer is
        # updated after the button state is latched.
        self.send({"execute": "input-send-event", "arguments": {"events": [
            {"type": "abs", "data": {"axis": "x", "value": x}},
            {"type": "abs", "data": {"axis": "y", "value": y}},
        ]}})
        time.sleep(0.25)
        self.send({"execute": "input-send-event", "arguments": {"events": [
            {"type": "btn", "data": {"down": True, "button": "left"}},
        ]}})
        time.sleep(0.12)
        self.send({"execute": "input-send-event", "arguments": {"events": [
            {"type": "btn", "data": {"down": False, "button": "left"}},
        ]}})
        time.sleep(0.6)

    def shot(self, dest):
        self.send({"execute": "screendump",
                   "arguments": {"filename": dest, "format": "png"}})


def main(argv):
    sock, dest = argv[0], argv[1]
    q = Qmp(sock)
    for spec in argv[2:]:
        x, y = (int(v) for v in spec.split(","))
        q.click(x, y)
        print(f"  clicked {x},{y}", flush=True)
    time.sleep(1.0)
    q.shot(dest)
    print("ok")


if __name__ == "__main__":
    main(sys.argv[1:])
