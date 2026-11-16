#!/usr/bin/env python3
"""Ask a guest's adbd for a service directly, and report what it answers.

    python3 adbservice.py 172.30.0.2 5555 "root:"

WHY THIS IS SEPARATE FROM `adb shell`: `root:` and `unroot:` are SERVICES, not shell commands.
`adb root` opens a stream named `root:`, adbd restarts itself as uid 0, and the stream closes. Asking
for it through `adb shell` asks the shell to restart the daemon underneath itself, which is why
`adb shell adb root` never works.

THE ANSWER IS THE WHOLE POINT. adbd replies with one of:

	"adbd is already running as root"        already root  - nothing to do
	"restarting adbd as root"                it is doing it now
	"adbd cannot run as root in production builds"   ro.debuggable=0, which is THIS guest
"""
import socket
import struct
import sys
import zlib

A_CNXN = 0x4E584E43
A_AUTH = 0x48545541
A_OPEN = 0x4E45504F
A_OKAY = 0x59414B4F
A_WRTE = 0x45545257
A_CLSE = 0x45534C43


def send(fh, command, arg0, arg1, payload=b""):
    magic = command ^ 0xFFFFFFFF
    fh.write(struct.pack("<IIIIII", command, arg0, arg1,
                         len(payload), zlib.crc32(payload) & 0xFFFFFFFF, magic) + payload)
    fh.flush()


def recv(fh):
    header = fh.read(24)
    if len(header) < 24:
        return None
    command, arg0, arg1, length, _crc, _magic = struct.unpack("<IIIIII", header)
    payload = fh.read(length) if length else b""
    return command, arg0, arg1, payload


def main(host, port, service):
    s = socket.create_connection((host, int(port)), timeout=15)
    fh = s.makefile("rwb")
    send(fh, A_CNXN, 0x01000001, 4096, b"host::\x00")
    while True:
        msg = recv(fh)
        if msg is None:
            print("  the device closed the connection during the handshake")
            return 1
        if msg[0] == A_CNXN:
            break
        if msg[0] == A_AUTH:
            print("  adbd wants a key (secure adb); cannot continue")
            return 1

    send(fh, A_OPEN, 1, 0, service.encode() + b"\x00")
    while True:
        msg = recv(fh)
        if msg is None:
            print("  the device closed the connection")
            return 1
        command, _arg0, _arg1, payload = msg
        if command == A_OKAY:
            print("  stream opened")
            continue
        if command == A_WRTE:
            send(fh, A_OKAY, 1, 1)
            text = payload.decode("utf-8", "replace").strip()
            if text:
                print(f"  SAYS: {text}")
            continue
        if command == A_CLSE:
            print("  stream closed")
            return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1], sys.argv[2], sys.argv[3]))
