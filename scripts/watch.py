#!/usr/bin/env python3
"""Screenshot a guest every second and print the OCR of each, to find WHEN a screen appears.

    python3 watch.py /disks/os/bliss/gq.sock 40

WHY A CLOCK RATHER THAN A GUESS: BlissOS's GRUB menu draws ~12-15 s after qemu starts and its
timeout is 30 s, so the window is real but narrow. A walkthrough that waits 55 s photographs the
live desktop (measured), and one that waits 14 s photographed a black frame (measured). What is
needed is the actual second the menu is up, which is what this prints - one line per second, with
the words tesseract read off the frame.
"""
import json
import os
import socket
import subprocess
import sys
import time


class Qmp:
    def __init__(self, path):
        self.s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.s.settimeout(30)
        self.s.connect(path)
        self.fh = self.s.makefile("rwb")
        self._reply()
        self.send({"execute": "qmp_capabilities"})

    def _reply(self):
        while True:
            line = self.fh.readline()
            if not line:
                raise SystemExit("socket closed")
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

    def shot(self, dest):
        self.send({"execute": "screendump",
                   "arguments": {"filename": dest, "format": "png"}})


def main(sock, seconds):
    q = Qmp(sock)
    dest = "/tmp/watch.png"
    for i in range(int(seconds)):
        q.shot(dest)
        out = subprocess.run(["tesseract", dest, "-"], capture_output=True, text=True).stdout
        words = " ".join(out.split())[:110]
        print(f"{i:3d}s  {words if words else '(nothing)'}", flush=True)
        time.sleep(1)
    q.s.close()


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2])
