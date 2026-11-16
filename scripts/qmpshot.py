#!/usr/bin/env python3
"""Take one screenshot from a running qemu over QMP.

    python3 qmpshot.py /disks/os/bliss/gq.sock /disks/os/bliss/shot.png

ONE CONNECTION AND ONE HANDSHAKE. `qmp_capabilities` is per-connection, so a second socket that
sends only `screendump` is refused with "Expecting capabilities negotiation" - which an earlier
version of this printed as success because it never read the reply.
"""
import json
import socket
import sys


def main(sock_path, dest):
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    s.settimeout(60)
    s.connect(sock_path)
    fh = s.makefile("rwb")

    def reply():
        while True:
            line = fh.readline()
            if not line:
                raise SystemExit("the QMP socket closed")
            msg = json.loads(line)
            if "event" in msg:
                continue
            if "error" in msg:
                raise SystemExit(f"qmp: {msg['error'].get('desc')}")
            return msg

    reply()                                     # the greeting
    for cmd in ({"execute": "qmp_capabilities"},
                {"execute": "screendump",
                 "arguments": {"filename": dest, "format": "png"}}):
        fh.write((json.dumps(cmd) + "\n").encode())
        fh.flush()
        reply()
    s.close()
    print("ok")


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2])
