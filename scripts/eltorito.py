#!/usr/bin/env python3
"""Print a hybrid ISO's El Torito boot catalog: which platforms it can boot and from where.

    python3 eltorito.py /disks/isos/bliss16.iso

WHY THIS ANSWERS A QUESTION ABOUT FIRMWARE RATHER THAN ABOUT FILES

"Can this ISO boot under UEFI?" is not answered by whether it contains a `.efi` file - plenty do and
are still BIOS-only, because the boot catalog has no `0xEF` entry and UEFI never looks at them. The
catalog is the firmware's TABLE OF CONTENTS: one entry per platform, each naming the LBA of a boot
image. So this reads the catalog, which is where the answer is.
"""
import struct
import sys


def read_at(path, offset, length):
    with open(path, "rb") as fh:
        fh.seek(offset)
        return fh.read(length)


def main(path):
    # The primary volume descriptor is at sector 16; the descriptors run until a terminator (255).
    catalog_lba = None
    for sector in range(16, 32):
        vd = read_at(path, sector * 2048, 2048)
        if len(vd) < 2048:
            break
        kind = vd[0]
        if kind == 255:
            break
        if kind == 0:
            # A Boot Record Volume Descriptor: its 4-byte little-endian pointer at 0x47 names the
            # El Torito catalog.
            catalog_lba = struct.unpack("<I", vd[0x47:0x47 + 4])[0]
            print(f"boot record at sector {sector}, catalog at LBA {catalog_lba}")
            break
    if catalog_lba is None:
        raise SystemExit("no El Torito boot record: this ISO is not bootable at all")

    cat = read_at(path, catalog_lba * 2048, 2048)
    if cat[0] != 0x01:
        raise SystemExit(f"the catalog's first byte is {cat[0]:#04x}, not 0x01: this is not a "
                         f"validation entry, so the pointer at 0x47 is not a catalog LBA")
    print(f"  validation entry: platform={cat[4]:#04x} id={cat[5:29].decode('ascii','replace').rstrip(chr(0))!r}")

    # THE CATALOG IS A HEADER (32 bytes) FOLLOWED BY ENTRIES, AND IT ENDS AT THE FIRST NON-ENTRY.
    #
    # MEASURED: a loop that stopped only at the 2048-byte boundary read past the real end and
    # printed sixty entries of zeros, all labelled "not bootable at LBA 0" - which reads like an ISO
    # with no UEFI support and was purely the parser. An entry's indicator is 0x88 (bootable) or 0x00
    # (not), and a section header is 0x90/0x91; ANYTHING ELSE is past the table.
    off = 32
    index = 0
    while off + 32 <= 2048:
        e = cat[off:off + 32]
        indicator = e[0]
        if indicator in (0x90, 0x91):
            platform = e[1]
            label = {0x00: "x86 BIOS", 0xEF: "UEFI"}.get(platform, f"platform {platform:#04x}")
            print(f"  -- section: {label}")
            off += 32
            index += 1
            continue
        if indicator not in (0x00, 0x88):
            break
        platform = e[1]
        sectors = struct.unpack("<H", e[6:8])[0]
        lba = struct.unpack("<I", e[8:12])[0]
        label = {0x00: "x86 BIOS", 0xEF: "UEFI", 0x01: "PowerPC", 0x02: "Mac"}.get(
            platform, f"platform {platform:#04x}")
        mark = "bootable" if indicator == 0x88 else "not bootable"
        print(f"  entry {index}: {label:10} {mark:13} image at LBA {lba:>7} ({sectors} sectors)")
        index += 1
        off += 32
    if index == 0:
        print("  NO ENTRIES - the catalog is empty")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1]))
