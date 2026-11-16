#!/usr/bin/env python3
"""Speak the first two packets of the adb protocol to a guest, and report what it answers.

    python3 adbprobe.py 172.30.0.2 5555

WHY THIS EXISTS: the question "can adb replace ssh here" turns on ONE fact that is cheaper to
measure than to reason about - whether adbd accepts a host it has never seen. It answers CNXN when
`ro.adb.secure=0` or when the host's key is already in /data/misc/adb/adb_keys, and it answers
AUTH TOKEN when it wants an RSA signature. Those two replies are the difference between "adb can be
driven from a program" and "adb needs a human to press Allow".

THE PREVIOUS ATTEMPT AT THIS TIMED OUT, AND THE BUG WAS THE HEADER. An adb message is SIX 32-bit
words, not five:

	command, arg0, arg1, data_length, data_crc32, magic

and `magic` is `command ^ 0xffffffff`. The first version sent command/arg0/arg1/length/crc and then
the payload, so adbd read the first four bytes of the payload as `magic`, disagreed, and dropped the
connection - which looks exactly like a device that refuses unauthenticated hosts.
"""
import socket
import struct
import sys
import zlib

# Protocol constants, named as `system/core/adb/protocol.h` names them.
A_CNXN = 0x4E584E43
A_AUTH = 0x48545541
A_OPEN = 0x4E45504F
A_OKAY = 0x59414B4F
A_WRTE = 0x45545257
A_CLSE = 0x45534C43
A_SYNC = 0x434E5953

NAME = {
    A_CNXN: "CNXN", A_AUTH: "AUTH", A_OPEN: "OPEN", A_OKAY: "OKAY",
    A_WRTE: "WRTE", A_CLSE: "CLSE", A_SYNC: "SYNC",
}


def send(fh, command, arg0, arg1, payload=b""):
    magic = command ^ 0xFFFFFFFF
    crc = zlib.crc32(payload) & 0xFFFFFFFF
    fh.write(struct.pack("<IIIIII", command, arg0, arg1, len(payload), crc, magic) + payload)
    fh.flush()


def recv(fh):
    header = fh.read(24)
    if len(header) < 24:
        return None, b""
    command, arg0, arg1, length, crc, magic = struct.unpack("<IIIIII", header)
    payload = fh.read(length) if length else b""
    return (command, arg0, arg1, magic, payload), payload


def main(host, port):
    s = socket.create_connection((host, int(port)), timeout=10)
    fh = s.makefile("rwb")

    # THE HOST DECLARES ITSELF FIRST. adbd never speaks unprompted, which is why a bare `recv` on
    # this socket times out on a perfectly healthy device.
    send(fh, A_CNXN, 0x01000001, 4096, b"host::\x00")
    print("  sent     CNXN host::")

    for _ in range(3):
        msg, payload = recv(fh)
        if msg is None:
            print("  the device closed the connection")
            return 1
        command, arg0, arg1, magic, _ = msg
        name = NAME.get(command, hex(command))
        if magic != (command ^ 0xFFFFFFFF):
            print(f"  {name}: BAD MAGIC - the device is not speaking adb here")
            return 1
        if command == A_CNXN:
            print(f"  device   CNXN version={arg0:#x} maxdata={arg1} banner={payload!r}")
            print()
            print("  VERDICT: no authorisation needed - this host is trusted already.")
            return 0
        if command == A_AUTH:
            kind = {1: "AUTH_TOKEN (wants a signature)", 2: "AUTH_SIGNATURE",
                    3: "AUTH_RSAPUBLICKEY"}.get(arg0, f"type {arg0}")
            print(f"  device   {name} {kind} ({len(payload)} bytes)")
            print()
            print("  VERDICT: adbd wants an RSA key it does not have. A HUMAN has to press Allow,")
            print("           or the key has to be seeded in /data/misc/adb/adb_keys first.")
            return 2
        print(f"  device   {name} arg0={arg0:#x} arg1={arg1}")
    return 1


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1], sys.argv[2]))
