#!/usr/bin/env python3
"""Send a sequence of keys to a guest over QMP, then screenshot.

    python3 sendkeys.py SOCK OUT.png ret down down shift-e shift-f 0 0 ret

WHY ONE KEY AT A TIME WITH A GAP: cgdisk reads a line at a time, and a burst that arrives faster
than it re-reads its input loses characters - measured, where `260M` arrived as `2` and the size
prompt advanced with a sector count nobody typed.
"""
import json
import socket
import sys
import time


def main(sock_path, dest, *keys):
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

    def send(cmd):
        fh.write((json.dumps(cmd) + "\n").encode())
        fh.flush()
        return reply()

    reply()
    send({"execute": "qmp_capabilities"})
    for key in keys:
        send({"execute": "human-monitor-command",
              "arguments": {"command-line": f"sendkey {key}"}})
        time.sleep(0.28)
    time.sleep(1.2)
    send({"execute": "screendump", "arguments": {"filename": dest, "format": "png"}})
    s.close()
    print("ok")


if __name__ == "__main__":
    main(*sys.argv[1:])
