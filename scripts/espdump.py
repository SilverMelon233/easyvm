#!/usr/bin/env python3
"""Print the FAT32 structures of a disk's ESP, for diagnosing one that will not boot."""
import struct
import subprocess
import sys

SECTOR = 512
ESP_TYPE = bytes.fromhex("28732ac11ff8d211ba4b00a0c93ec93b")


def read_at(disk, offset, length):
    data = bytearray()
    done = 0
    while done < length:
        take = min(1 << 20, length - done)
        out = subprocess.run(
            ["qemu-io", "-f", "qcow2", "-r", "-c", f"read -v {offset + done} {take}", disk],
            capture_output=True, text=True)
        for line in out.stdout.splitlines():
            if ":" not in line:
                continue
            _, _, rest = line.partition(":")
            for token in rest.split()[:16]:
                if len(token) == 2:
                    try:
                        data.append(int(token, 16))
                    except ValueError:
                        break
        done += take
    return bytes(data[:length])


def main(disk):
    header = read_at(disk, SECTOR, 92)
    entry_lba = struct.unpack("<Q", header[72:80])[0]
    table = read_at(disk, entry_lba * SECTOR, 128 * 128)
    off = None
    for i in range(128):
        e = table[i * 128:(i + 1) * 128]
        if e[:16] == ESP_TYPE:
            first, last = struct.unpack("<QQ", e[32:48])
            off = first * SECTOR
            print(f"ESP at {off}, {(last - first + 1) * SECTOR >> 20} MiB")
            break
    if off is None:
        raise SystemExit("no ESP")

    bs = read_at(disk, off, SECTOR)
    bps = struct.unpack("<H", bs[11:13])[0]
    spc = bs[13]
    res = struct.unpack("<H", bs[14:16])[0]
    nf = bs[16]
    fs = struct.unpack("<I", bs[36:40])[0]
    root = struct.unpack("<I", bs[44:48])[0]
    cb = bps * spc
    data_start = (res + nf * fs) * bps
    print(f"  bps={bps} spc={spc} reserved={res} nfats={nf} fatsz={fs} root={root}")
    print(f"  cluster_bytes={cb} data_start={data_start}")

    fat = read_at(disk, off + res * bps, 32)
    for i in range(5):
        v = struct.unpack("<I", fat[i * 4:i * 4 + 4])[0] & 0x0FFFFFFF
        print(f"  FAT[{i}] = {v:08x}")

    def cluster_bytes(cluster):
        return read_at(disk, off + data_start + (cluster - 2) * cb, cb)

    def chain(cluster):
        seen = set()
        lim = fs * bps // 4
        while 2 <= cluster < min(lim, 0x0FFFFFF8) and cluster not in seen:
            seen.add(cluster)
            yield cluster
            v = read_at(disk, off + res * bps + cluster * 4, 4)
            cluster = struct.unpack("<I", v)[0] & 0x0FFFFFFF

    def entries(cluster):
        out = []
        for c in chain(cluster):
            block = cluster_bytes(c)
            for o in range(0, len(block), 32):
                e = block[o:o + 32]
                if len(e) < 32 or e[0] == 0:
                    return out
                if e[0] == 0xE5 or e[11] & 0x0F == 0x0F:
                    continue
                name = (e[0:8].rstrip(b" ").decode("ascii", "replace") + "." +
                        e[8:11].rstrip(b" ").decode("ascii", "replace")).rstrip(".")
                fc = struct.unpack("<H", e[20:22])[0] << 16 | struct.unpack("<H", e[26:28])[0]
                size = struct.unpack("<I", e[28:32])[0]
                out.append((name, e[11], fc, size))
        return out

    print("  root:")
    for name, attr, fc, size in entries(root):
        print(f"    {name:16} attr={attr:02x} cluster={fc} size={size}")
        if attr & 0x10 and name not in (".", ".."):
            for n2, a2, f2, s2 in entries(fc):
                print(f"      {n2:14} attr={a2:02x} cluster={f2} size={s2}")
                if a2 & 0x10 and n2 not in (".", "..") and f2 >= 2:
                    for n3, a3, f3, s3 in entries(f2):
                        print(f"          {n3:12} attr={a3:02x} size={s3}")


if __name__ == "__main__":
    main(sys.argv[1])
