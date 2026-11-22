#!/bin/sh
# Capture ONE frame through the desktop's own portal and report whether it is a real
# desktop. Runs INSIDE the guest, over ssh, with no screendump anywhere.
#
# Exit 0 = a drawn desktop; non-zero = a black/uniform frame, and the output says which.
#
# WHY THE MEAN IS THE TEST, MEASURED on this image: a healthy Plasma desktop, a desktop
# whose output was in DPMS-off, and a stream that never attached ALL produced a PNG of the
# same 3146804 bytes, because the encoder writes the raw frame size. Size proves nothing;
# brightness does - a real desktop measures ~128, DPMS-off measures 0.0.
set -u

PORT="${EAZYSTREAM_PORT:-48079}"
OUT=/tmp/portal-frame.png
TMPL=/tmp/est-shot-portal.py

# ALWAYS WRITE OUR OWN, WITH THIS ARGUMENT ORDER. MEASURED: an image can already carry a
# /tmp/est-shot.py from an earlier probe, and reusing whatever is there made this script call
# it with the wrong arguments.
cat > "$TMPL" <<'PY'
import socket, struct, sys
port, out = int(sys.argv[1]), sys.argv[2]
s = socket.create_connection(("127.0.0.1", port), timeout=20)
s.settimeout(30)
def recvn(n):
    b = b""
    while len(b) < n:
        c = s.recv(n - len(b))
        if not c:
            raise SystemExit("est: connection closed")
        b += c
    return b
s.sendall(struct.pack(">BBI", 0x01, 0, 0))
typ, flags, ln = struct.unpack(">BBI", recvn(6))
if ln:
    recvn(ln)
s.sendall(struct.pack(">BBI", 0x40, 0, 4) + struct.pack(">I", 1) + struct.pack(">B", 0))
for _ in range(900):
    typ, flags, ln = struct.unpack(">BBI", recvn(6))
    payload = recvn(ln) if ln else b""
    if typ == 0x41:
        rid, plen = struct.unpack(">II", payload[:8])
        open(out, "wb").write(payload[8:8+plen])
        sys.exit(0)
raise SystemExit("est: no ShotResp")
PY

python3 "$TMPL" "$PORT" "$OUT" || exit 2
[ -s "$OUT" ] || { echo "portal-capture: no png"; exit 3; }

# THE MEAN BRIGHTNESS, DECODED HERE. A fallback by file size was MEASURED WRONG: a black
# frame and a real desktop both write 3146804 bytes on this image.
read -r MEAN DIMS <<EOF2
$(python3 - "$OUT" <<'PY'
import sys, zlib, struct
d = open(sys.argv[1], "rb").read()
pos, idat, w, h, bd, ct = 8, b"", 0, 0, 0, 0
while pos < len(d):
    ln = struct.unpack(">I", d[pos:pos+4])[0]
    typ = d[pos+4:pos+8]
    data = d[pos+8:pos+8+ln]
    pos += 12 + ln
    if typ == b"IHDR":
        w, h, bd, ct = struct.unpack(">IIBB", data[:10])
    elif typ == b"IDAT":
        idat += data
    elif typ == b"IEND":
        break
raw = zlib.decompress(idat)
ch = {0: 1, 2: 3, 3: 1, 4: 2, 6: 4}[ct]
stride = w * ch + 1
total = float(0)
n = 0
prev = bytearray(w * ch)
for y in range(h):
    off = y * stride
    f = raw[off]
    row = bytearray(raw[off+1:off+stride])
    if f == 2:
        for i in range(len(row)):
            row[i] = (row[i] + prev[i]) & 0xFF
    elif f in (1, 4):
        for i in range(ch, len(row)):
            row[i] = (row[i] + row[i-ch]) & 0xFF
    prev = row
    if y % 8:
        continue
    for x in range(0, w, 8):
        px = row[x*ch:(x+1)*ch]
        if ch >= 3:
            total += (px[0] + px[1] + px[2]) / 3.0
        else:
            total += px[0]
        n += 1
print("%.1f %dx%d" % (total / max(n, 1), w, h))
PY
)
EOF2

if awk -v m="${MEAN:-0}" 'BEGIN{exit !(m > 5)}'; then
	echo "portal-capture: PASS mean=$MEAN frame=$DIMS"
	exit 0
fi
echo "portal-capture: FAIL mean=${MEAN:-0} (black or DPMS off) frame=$DIMS"
exit 4
