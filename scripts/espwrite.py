#!/usr/bin/env python3
"""Put an EFI loader into a disk's ESP, writing only the bytes that changed.

    python3 espwrite.py disk.qcow2 /tmp/efiimg/boot/grub/out/efi/boot

WHY THIS IS NEEDED AT ALL

The BlissOS installer finishes with "Congratulations! BlissOS-16.9.7 is installed successfully" and
writes NOTHING to the ESP: measured on this disk, `/EFI/` is an empty directory, `/EFI/BLISSOS`
does not exist, and `/EFI/BOOT` does not exist. Meanwhile the system partition is complete -
`/android-2024-10-11/{kernel,initrd.img,system.img}` and `/boot/grub/android.cfg` - and that
`android.cfg` contains

    add_os_if_exists /EFI/BlissOS/${grub}.efi BlissOS blissos

a reference to a loader the installer never placed. So the disk boots to

    BdsDxe: No bootable option or device was found.

THE LOADER IS IN THE ISO, WHICH IS WHY THIS NEEDS NO NETWORK AND NO GRUB PACKAGE:
`boot/grub/efi.img` is a 15 MiB FAT image holding exactly `EFI/BOOT/BOOTx64.EFI` and its
siblings. Copying them to the ESP's removable-media path is what makes the firmware find the disk
without any NVRAM entry - the same conclusion easydiskbake's `bliss_esp.py` reached, by a
different route (it copies from `/EFI/BLISSOS`, which their installer apparently writes and this
one does not).

WHY THE FAT IS EDITED IN PLACE

The alternative is `mtools` or a full convert; neither is here, and the ESP is 260 MiB inside a
32 GiB qcow2. `qemu-io` addresses any offset, so this reads the FAT32 structures, allocates
clusters for the new files, writes them, and patches the root directory - then writes back ONLY
the sectors it touched.
"""
import struct
import subprocess
import sys

SECTOR = 512
ESP_TYPE = bytes.fromhex("28732ac11ff8d211ba4b00a0c93ec93b")

# 0x0FFFFFFF is the FAT32 end-of-chain marker; 0x0FFFFFF8..0x0FFFFFFF all mean "last cluster".
EOC = 0x0FFFFFFF
FREE = 0x00000000


def qio(disk, *commands):
    out = subprocess.run(["qemu-io", "-f", "qcow2"] + [c for cmd in commands for c in ("-c", cmd)]
                         + [disk], capture_output=True, text=True)
    return out.stdout + out.stderr


def read_at(disk, offset, length):
    """Read `length` bytes at `offset`, chunked so the hex dump stays small."""
    data = bytearray()
    done = 0
    chunk = 1 << 20
    while done < length:
        take = min(chunk, length - done)
        out = subprocess.run(
            ["qemu-io", "-f", "qcow2", "-r", "-c", f"read -v {offset + done} {take}", disk],
            capture_output=True, text=True)
        if out.returncode != 0:
            raise SystemExit(f"read failed at {offset + done}: {out.stderr[-200:]}")
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


def write_at(disk, offset, payload):
    """Write `payload` at `offset` through a temporary file - qemu-io reads a source file."""
    import os
    import tempfile
    with tempfile.NamedTemporaryFile(delete=False) as fh:
        fh.write(payload)
        src = fh.name
    try:
        out = subprocess.run(
            ["qemu-io", "-f", "qcow2", "-c",
             f"write -s {src} {offset} {len(payload)}", disk],
            capture_output=True, text=True)
        if out.returncode != 0:
            raise SystemExit(f"write failed at {offset}: {(out.stdout + out.stderr)[-300:]}")
    finally:
        os.unlink(src)


def partitions(disk):
    header = read_at(disk, SECTOR, 92)
    if header[:8] != b"EFI PART":
        raise SystemExit(f"{disk}: no GPT header at LBA 1")
    entry_lba = struct.unpack("<Q", header[72:80])[0]
    count = struct.unpack("<I", header[80:84])[0]
    size = struct.unpack("<I", header[84:88])[0]
    table = read_at(disk, entry_lba * SECTOR, count * size)
    parts = []
    for i in range(count):
        e = table[i * size:(i + 1) * size]
        if e[:16] == bytes(16):
            continue
        first, last = struct.unpack("<QQ", e[32:48])
        parts.append({"index": i + 1, "type": e[:16],
                      "offset": first * SECTOR, "bytes": (last - first + 1) * SECTOR,
                      "name": e[56:56 + 72].decode("utf-16-le").rstrip("\x00")})
    return parts


class Fat32:
    """A FAT32 partition, read whole, edited in memory, written back by the sectors touched."""

    def __init__(self, image):
        self.img = bytearray(image)
        self.bps = struct.unpack("<H", self.img[11:13])[0]
        self.spc = self.img[13]
        self.reserved = struct.unpack("<H", self.img[14:16])[0]
        self.nfats = self.img[16]
        self.fat_sectors = struct.unpack("<I", self.img[36:40])[0]
        self.root = struct.unpack("<I", self.img[44:48])[0]
        self.cluster_bytes = self.bps * self.spc
        self.fat_off = self.reserved * self.bps
        self.data_off = (self.reserved + self.nfats * self.fat_sectors) * self.bps
        self.max_cluster = self.fat_sectors * self.bps // 4

    # --- clusters ---------------------------------------------------------------------

    def get(self, cluster):
        off = self.fat_off + cluster * 4
        return struct.unpack("<I", self.img[off:off + 4])[0] & 0x0FFFFFFF

    def set(self, cluster, value):
        # BOTH COPIES. A FAT32 has `nfats` tables and Windows/most firmware trust the second if the
        # first is damaged; updating one leaves the filesystem subtly inconsistent.
        for n in range(self.nfats):
            off = (self.reserved + n * self.fat_sectors) * self.bps + cluster * 4
            existing = struct.unpack("<I", self.img[off:off + 4])[0]
            struct.pack_into("<I", self.img, off, (existing & 0xF0000000) | (value & 0x0FFFFFFF))

    def cluster_at(self, cluster):
        start = self.data_off + (cluster - 2) * self.cluster_bytes
        return start

    def allocate(self, count):
        """`count` free clusters, chained in order."""
        found = []
        for c in range(2, self.max_cluster):
            if self.get(c) == FREE:
                found.append(c)
                if len(found) == count:
                    break
        if len(found) < count:
            raise SystemExit(f"the ESP has room for {len(found)} clusters, needs {count}")
        for i, c in enumerate(found):
            self.set(c, found[i + 1] if i + 1 < len(found) else EOC)
        return found

    # --- directories ------------------------------------------------------------------

    def read_chain(self, cluster):
        out = bytearray()
        seen = set()
        while 2 <= cluster < min(self.max_cluster, 0x0FFFFFF8) and cluster not in seen:
            seen.add(cluster)
            start = self.cluster_at(cluster)
            out += self.img[start:start + self.cluster_bytes]
            cluster = self.get(cluster)
        return bytes(out)

    def entries(self, cluster):
        data = self.read_chain(cluster)
        out = []
        for off in range(0, len(data), 32):
            e = data[off:off + 32]
            if len(e) < 32 or e[0] == 0:
                break
            if e[0] == 0xE5 or e[11] & 0x0F == 0x0F or e[11] & 0x08:
                continue
            name = (e[0:8].rstrip(b" ").decode("ascii", "replace")
                    + "." + e[8:11].rstrip(b" ").decode("ascii", "replace")).rstrip(".")
            # FstClusHI IS AT OFFSET 20 AND FstClusLO AT 26 - THE OPPOSITE OF THE ORDER THE NAMES
            # ARE WRITTEN IN THE SPEC'S TABLE, and reading them the other way is silent. Measured:
            # the installer's `/EFI` entry is
            #     20-21 = 0000 (HI)      26-27 = 0300 (LO)
            # so a `LO << 16 | HI` read gives 196608, which is past the end of a 66,432-entry FAT and
            # makes a populated directory look EMPTY. That is why the first version of this script
            # reported `/EFI` as a directory with no cluster and then crashed on `chain[-1]`.
            first = struct.unpack("<H", e[20:22])[0] << 16 | struct.unpack("<H", e[26:28])[0]
            out.append({"name": name, "attr": e[11], "cluster": first,
                        "size": struct.unpack("<I", e[28:32])[0], "off": off})
        return out

    def find(self, cluster, name):
        for e in self.entries(cluster):
            if e["name"].upper() == name.upper():
                return e
        return None

    def free_entry_offset(self, cluster):
        """A slot in this directory's chain, extending it by one cluster if it is full."""
        chain = list(self._chain(cluster))
        for c in chain:
            base = self.cluster_at(c)
            for off in range(0, self.cluster_bytes, 32):
                if self.img[base + off] in (0x00, 0xE5):
                    return base + off
        # FULL: one more cluster, linked to the end and zeroed.
        new = self.allocate(1)[0]
        last = chain[-1]
        self.set(last, new)
        self.set(new, EOC)
        start = self.cluster_at(new)
        self.img[start:start + self.cluster_bytes] = bytes(self.cluster_bytes)
        return start

    def _chain(self, cluster):
        seen = set()
        while 2 <= cluster < min(self.max_cluster, 0x0FFFFFF8) and cluster not in seen:
            seen.add(cluster)
            yield cluster
            cluster = self.get(cluster)

    def mkdir(self, parent, name):
        existing = self.find(parent, name)
        if existing:
            return existing["cluster"]
        cluster = self.allocate(1)[0]
        start = self.cluster_at(cluster)
        self.img[start:start + self.cluster_bytes] = bytes(self.cluster_bytes)
        # `.` and `..` are required; some firmware walks them.
        for entry_name, target in ((".", cluster), ("..", parent if parent != self.root else 0)):
            e = bytearray(32)
            e[0:11] = entry_name.ljust(11).encode("ascii")
            e[11] = 0x10
            struct.pack_into("<H", e, 20, (target >> 16) & 0xFFFF)
            struct.pack_into("<H", e, 26, target & 0xFFFF)
            struct.pack_into("<I", e, 28, 0)
            struct.pack_into("<H", e, 24, 0)
            self.img[start + (0 if entry_name == "." else 32):
                     start + (0 if entry_name == "." else 32) + 32] = e
        self._write_entry(parent, name, 0x10, cluster, 0)
        return cluster

    def _write_entry(self, dir_cluster, name, attr, cluster, size):
        base = self.free_entry_offset(dir_cluster)
        e = bytearray(32)
        stem, _, ext = name.upper().partition(".")
        e[0:8] = stem.ljust(8).encode("ascii")[:8]
        e[8:11] = ext.ljust(3).encode("ascii")[:3]
        e[11] = attr
        struct.pack_into("<H", e, 20, (cluster >> 16) & 0xFFFF)
        struct.pack_into("<H", e, 26, cluster & 0xFFFF)
        struct.pack_into("<I", e, 28, size)
        self.img[base:base + 32] = e

    def write_file(self, dir_cluster, name, payload):
        need = (len(payload) + self.cluster_bytes - 1) // self.cluster_bytes or 1
        clusters = self.allocate(need)
        for i, c in enumerate(clusters):
            start = self.cluster_at(c)
            piece = payload[i * self.cluster_bytes:(i + 1) * self.cluster_bytes]
            self.img[start:start + self.cluster_bytes] = bytes(self.cluster_bytes)
            self.img[start:start + len(piece)] = piece
        self._write_entry(dir_cluster, name, 0x20, clusters[0], len(payload))
        return clusters[0]


def main(argv):
    disk, src = argv[0], argv[1]
    esp = next((p for p in partitions(disk) if p["type"] == ESP_TYPE), None)
    if esp is None:
        raise SystemExit("no EFI System Partition")

    # THE WHOLE ESP IS READ: 260 MiB. The FAT tables and the root directory can be anywhere in it,
    # and a partial read is what makes a cluster walk run off the end of the buffer.
    image = read_at(disk, esp["offset"], esp["bytes"])
    if image[510:512] != b"\x55\xaa":
        raise SystemExit("the ESP has no FAT boot sector")
    fat = Fat32(image)

    efi = fat.mkdir(fat.root, "EFI")
    boot = fat.mkdir(efi, "BOOT")
    import os
    written = 0
    for name in sorted(os.listdir(src)):
        path = os.path.join(src, name)
        if not os.path.isfile(path):
            continue
        with open(path, "rb") as fh:
            payload = fh.read()
        fat.write_file(boot, name.upper(), payload)
        print(f"  {name.upper():14} {len(payload):>10,} B  -> /EFI/BOOT/{name.upper()}")
        written += len(payload)

    # AND `/EFI/BlissOS`, BECAUSE THE INSTALLER'S OWN grub.cfg ASKS FOR IT:
    #     add_os_if_exists /EFI/BlissOS/${grub}.efi BlissOS blissos
    # A menu entry that points at a missing loader is a menu entry that cannot be chosen.
    blissos = fat.mkdir(efi, "BlissOS")
    for name in ("grubx64.efi", "mmx64.efi"):
        path = os.path.join(src, name)
        if os.path.isfile(path):
            with open(path, "rb") as fh:
                payload = fh.read()
            fat.write_file(blissos, name.upper(), payload)
            print(f"  {name.upper():14} {len(payload):>10,} B  -> /EFI/BlissOS/{name.upper()}")

    # THE WRITE-BACK IS THE SECTORS THAT CHANGED, NOT THE PARTITION.
    # A full 260 MiB write allocates 260 MiB of clusters in the qcow2 even though ~5 MiB changed,
    # which grows the published image for no reason.
    changed = []
    for off in range(0, len(fat.img), 64 << 10):
        a = bytes(image[off:off + (64 << 10)])
        b = bytes(fat.img[off:off + (64 << 10)])
        if a != b:
            changed.append((off, b))
    for off, payload in changed:
        write_at(disk, esp["offset"] + off, payload)
    print(f"  wrote {len(changed)} changed block(s) back to the ESP")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
