#!/usr/bin/env python3
"""A proxy that says what it carried, for the egress end-to-end test.

WHY THIS EXISTS RATHER THAN 3PROXY. The job runs inside the project's own container - that is what
makes `/dev/kvm` available, and every other workflow here does the same - so a second container cannot
be started. 3proxy would have to be installed on every run, from a Debian package that may or may not
be in the base image's archive at that moment. THIS is forty lines of standard library, and the only
thing the test needs from it is a log line per request.

WHAT IT MUST DO, AND NO MORE:

	HTTP CONNECT   enough for the TCP half. The reference only reads the request line, answers 200, and
	               records the destination - it never forwards, because the assertion is about WHICH
	               proxy was chosen, not about what the far end says
	SOCKS5 UDP     the UDP half's transport. It reads the datagram's header for the destination, records
	               it, and answers with an empty datagram so the relay sees a reply

THE LOG LINE IS THE ASSERTION. One per request, in the form

	<proto> <destination-ip>:<port>

so the test can grep for `connect`/`udp` and an address, and can prove a `direct` destination appears in
NO proxy log - which is the only way to prove a connection did not go through one.

IT IS DELIBERATELY NOT A PROXY. Nothing is forwarded to the internet: a test that reached a real host
would be testing that host's availability, and MEASURED on the lab cluster, `deb.debian.org` answers 200
through a proxy and 000 without one - so reachability would make this job fail for reasons unrelated to
the rules.
"""
import argparse
import socket
import struct
import sys
import threading


def log(path, proto, ip, port):
    line = "%s %s:%d\n" % (proto, ip, port)
    with open(path, "a") as f:
        f.write(line)
    sys.stdout.write("proxy[%s] %s" % (path, line))
    sys.stdout.flush()


def serve_connect(port, logpath):
    srv = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    srv.bind(("0.0.0.0", port))
    srv.listen(64)
    print("connect listening on %d" % port, flush=True)

    while True:
        conn, _ = srv.accept()
        threading.Thread(target=handle_connect, args=(conn, logpath), daemon=True).start()


def handle_connect(conn, logpath):
    try:
        conn.settimeout(10)
        buf = b""
        while b"\r\n" not in buf:
            d = conn.recv(4096)
            if not d:
                return
            buf += d
        line = buf.split(b"\r\n", 1)[0].decode(errors="replace")
        # `CONNECT 1.2.3.4:443 HTTP/1.1`
        parts = line.split()
        if len(parts) < 2:
            return
        target = parts[1]
        host, _, port = target.rpartition(":")
        log(logpath, "connect", host, int(port or 0))
        # THE 200 IS THE WHOLE PROTOCOL. A client that receives anything else closes; nothing is
        # forwarded because the assertion is about the choice, not the destination.
        conn.sendall(b"HTTP/1.1 200 Connection established\r\n\r\n")
        # AND A COMPLETE HTTP RESPONSE, because the guest is speaking HTTP through this tunnel and
        # will parse whatever arrives. A bare 200 would leave its client waiting for a body.
        body = b"proxy\n"
        conn.sendall(b"HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: %d\r\n"
                     b"Connection: close\r\n\r\n%s" % (len(body), body))
    except Exception:
        pass
    finally:
        try:
            conn.close()
        except Exception:
            pass


def serve_socks(port, logpath):
    srv = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    srv.bind(("0.0.0.0", port))
    srv.listen(64)
    print("socks5 listening on %d" % port, flush=True)

    while True:
        conn, _ = srv.accept()
        threading.Thread(target=handle_socks, args=(conn, logpath), daemon=True).start()


def handle_socks(ctl, logpath):
    """SOCKS5 control connection; UDP ASSOCIATE hands back a relay socket."""
    try:
        ctl.settimeout(30)
        hdr = ctl.recv(2)
        if len(hdr) < 2 or hdr[0] != 5:
            return
        ctl.recv(hdr[1] if len(hdr) > 1 else 0)
        ctl.sendall(b"\x05\x00")  # no auth
        req = ctl.recv(10)
        if len(req) < 10 or req[1] != 3:  # only UDP ASSOCIATE
            ctl.sendall(b"\x05\x07\x00\x01" + b"\x00" * 6)
            return

        relay = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        relay.bind(("0.0.0.0", 0))
        rport = relay.getsockname()[1]
        ctl.sendall(b"\x05\x00\x00\x01" + b"\x00" * 4 + struct.pack(">H", rport))

        # THE RELAY DIES WITH THE CONTROL CONNECTION, which is what SOCKS5 specifies and what the
        # daemon's session table relies on.
        def pump():
            relay.settimeout(120)
            try:
                while True:
                    data, peer = relay.recvfrom(65535)
                    if len(data) < 10:
                        continue
                    atyp = data[3]
                    if atyp == 1:
                        ip = socket.inet_ntoa(data[4:8])
                        dport = struct.unpack(">H", data[8:10])[0]
                        payload = data[10:]
                    elif atyp == 4:
                        ip = socket.inet_ntop(socket.AF_INET6, data[4:20])
                        dport = struct.unpack(">H", data[20:22])[0]
                        payload = data[22:]
                    else:
                        continue
                    log(logpath, "udp", ip, dport)
                    # AN EMPTY REPLY, WHICH IS ALL THE RELAY NEEDS: it proves the datagram was carried
                    # and gives the session table something to write back into the tun.
                    reply = b"\x00\x00\x00\x01" + socket.inet_aton(ip) + \
                            struct.pack(">H", dport) + b"udp\n"
                    relay.sendto(reply, peer)
                    _ = payload
            except Exception:
                pass
            finally:
                relay.close()

        threading.Thread(target=pump, daemon=True).start()
        # HOLD THE CONTROL CONNECTION OPEN, so the association outlives the request.
        while ctl.recv(1024):
            pass
    except Exception:
        pass
    finally:
        try:
            ctl.close()
        except Exception:
            pass


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--connect-port", type=int, required=True)
    ap.add_argument("--socks-port", type=int, required=True)
    ap.add_argument("--log", required=True)
    a = ap.parse_args()
    open(a.log, "w").close()
    threading.Thread(target=serve_socks, args=(a.socks_port, a.log), daemon=True).start()
    serve_connect(a.connect_port, a.log)


if __name__ == "__main__":
    main()
