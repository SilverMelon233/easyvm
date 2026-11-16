package run

// A GPT WRITTEN INTO A QCOW2, FOR AN INSTALLER THAT EXPECTS ONE TO BE THERE.
//
// WHY THIS IS NOT A CALL TO sgdisk OR sfdisk: neither is in this repository's image - `which sgdisk
// parted sfdisk fdisk` finds nothing in the container the stages run in - and a 128-entry GPT is
// 16 KiB of bytes at fixed offsets and three CRCs. Installing a partitioning tool to write them is
// more code to trust than writing them.
//
// THE TWO LAYOUT FACTS WERE BOTH MEASURED, BOTH BY GETTING THEM WRONG FIRST:
//
//	vda1 starting at LBA 34        the installer lists NO partitions and answers
//	                               "No ESP is selected, setup will use the old detection method"
//	                               - the first partition must be 1 MiB aligned, at LBA 2048
//	an ESP GUID of ...C931C5D8     "This is not an EFI System Partition, please check again"
//	                               - the type is C12A7328-F81F-11D2-BA4B-00A0C93EC93B and nothing
//	                               else, digit for digit
//
// AND THE TABLE IS CONVERTED ROUND-TRIP, because a GPT cannot be written into a qcow2's own clusters
// without knowing which cluster a given LBA lands in. The disk is converted to raw, the table is
// written at its byte offsets, and it is converted back - the same sequence easydiskbake's
// lib/diskgen.py uses, for the same reason, and it is cheap because a fresh disk is sparse.

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"os/exec"
	"path/filepath"
)

const (
	// THE SECTOR SIZE A virtio-blk DISK OF THIS SHAPE REPORTS.
	gptSector = 512

	// THE ESP TYPE GUID, AS BYTES. GPT stores the first three fields little-endian, so the string
	// form does not appear in the disk and comparing against it matches nothing.
	gptESPType = "C12A7328-F81F-11D2-BA4B-00A0C93EC93B"
	gptLinux   = "0FC63DAF-8483-4772-8E79-3D69D8477DE4"

	// A FIXED DISK GUID, so two runs produce the same bytes. Nothing reads it.
	gptDiskGUID = "01234567-89ab-cdef-0123-456789abcdef"
	gptESPGUID  = "11111111-2222-3333-4444-555555555555"
	gptSysGUID  = "66666666-7777-8888-9999-aaaaaaaaaaaa"

	gptEntries      = 128
	gptEntrySize    = 128
	gptEntrySectors = gptEntries * gptEntrySize / gptSector // 32 sectors
	gptFirstUsable  = 34                                    // LBA 2..33 hold the primary array
	gptESPStart     = 2048                                  // 1 MiB, and the installer requires it
)

// gptGUIDBytes turns a GUID's text form into the sixteen bytes a GPT stores.
//
// THE FIRST THREE GROUPS ARE LITTLE-ENDIAN AND THE LAST TWO ARE NOT, which is the single fact that
// makes a hand-written GPT fail in a way nothing reports: an ESP with the GUID in text order is a
// valid partition of an UNKNOWN type, so the installer lists it and then refuses it with "This is
// not an EFI System Partition".
func gptGUIDBytes(text string) []byte {
	hex := func(i int) byte {
		v := 0
		for _, c := range text[i : i+2] {
			v <<= 4
			switch {
			case c >= '0' && c <= '9':
				v |= int(c - '0')
			case c >= 'a' && c <= 'f':
				v |= int(c-'a') + 10
			case c >= 'A' && c <= 'F':
				v |= int(c-'A') + 10
			}
		}
		return byte(v)
	}
	// The text form is 8-4-4-4-12 with hyphens; the byte offsets ignore them.
	var raw [16]byte
	// little-endian: the 4-byte field is reversed, then each 2-byte field
	raw[0], raw[1], raw[2], raw[3] = hex(6), hex(4), hex(2), hex(0)
	raw[4], raw[5] = hex(11), hex(9)
	raw[6], raw[7] = hex(16), hex(14)
	// the last two groups are written as-is
	for i, at := range []int{19, 21, 24, 26, 28, 30, 32, 34} {
		raw[8+i] = hex(at)
	}
	return raw[:]
}

// gptPartitionEntry is one 128-byte entry.
func gptPartitionEntry(first, last uint64, typeGUID, uniqueGUID, name string) []byte {
	e := make([]byte, gptEntrySize)
	copy(e[0:16], gptGUIDBytes(typeGUID))
	copy(e[16:32], gptGUIDBytes(uniqueGUID))
	binary.LittleEndian.PutUint64(e[32:40], first)
	binary.LittleEndian.PutUint64(e[40:48], last)
	// The name is UTF-16LE in 72 bytes, NUL-terminated.
	for i, r := range []rune(name) {
		if i >= 36 {
			break
		}
		binary.LittleEndian.PutUint16(e[56+i*2:58+i*2], uint16(r))
	}
	return e
}

// gptHeader builds the 92-byte header, CRCs included.
func gptHeader(myLBA, altLBA, entryLBA, firstUsable, lastUsable uint64, tableCRC uint32) []byte {
	h := make([]byte, 92)
	copy(h[0:8], "EFI PART")
	binary.LittleEndian.PutUint32(h[8:12], 0x00010000) // revision 1.0
	binary.LittleEndian.PutUint32(h[12:16], 92)        // header size
	// h[16:20] is the header CRC, filled last.
	binary.LittleEndian.PutUint64(h[24:32], myLBA)
	binary.LittleEndian.PutUint64(h[32:40], altLBA)
	binary.LittleEndian.PutUint64(h[40:48], firstUsable)
	binary.LittleEndian.PutUint64(h[48:56], lastUsable)
	copy(h[56:72], gptGUIDBytes(gptDiskGUID))
	binary.LittleEndian.PutUint64(h[72:80], entryLBA)
	binary.LittleEndian.PutUint32(h[80:84], gptEntries)
	binary.LittleEndian.PutUint32(h[84:88], gptEntrySize)
	binary.LittleEndian.PutUint32(h[88:92], tableCRC)
	binary.LittleEndian.PutUint32(h[16:20], crc32.ChecksumIEEE(h))
	return h
}

// writeGPT puts a GPT on `path`: a 260 MiB ESP typed EF00 at LBA 2048, Linux on the rest.
func writeGPT(path string, gigabytes, espMiB int) error {
	qemuImg, err := exec.LookPath("qemu-img")
	if err != nil {
		return fmt.Errorf("a `disk_gpt:` step needs qemu-img: %v", err)
	}
	raw := path + ".gpt-raw"
	defer os.Remove(raw)

	// THE TWO CONVERSIONS ARE THE PRICE OF A QCOW2. A GPT's bytes live at fixed offsets in the raw
	// image; a qcow2 has no such offsets to write to.
	if out, err := exec.Command(qemuImg, "convert", "-f", "qcow2", "-O", "raw", path, raw).
		CombinedOutput(); err != nil {
		return fmt.Errorf("unpacking %s to write a partition table: %v (%s)", filepath.Base(path),
			err, firstLineOf(string(out)))
	}

	fi, err := os.Stat(raw)
	if err != nil {
		return err
	}
	total := uint64(fi.Size()) / gptSector
	espLast := uint64(gptESPStart) + uint64(espMiB)*(1<<20)/gptSector - 1
	lastUsable := total - 1 - gptEntrySectors
	if lastUsable <= espLast {
		return fmt.Errorf("a %d GiB disk cannot hold a %d MiB ESP and a data partition", gigabytes, espMiB)
	}

	table := make([]byte, gptEntries*gptEntrySize)
	copy(table[0:], gptPartitionEntry(gptESPStart, espLast, gptESPType, gptESPGUID, "ESP"))
	copy(table[gptEntrySize:], gptPartitionEntry(espLast+1, lastUsable, gptLinux, gptSysGUID, "Android"))
	tableCRC := crc32.ChecksumIEEE(table)

	primary := gptHeader(1, total-1, 2, gptFirstUsable, lastUsable, tableCRC)
	backup := gptHeader(total-1, 1, total-1-gptEntrySectors, gptFirstUsable, lastUsable, tableCRC)

	mbr := make([]byte, gptSector)
	mbr[446+4] = 0xEE // protective partition
	binary.LittleEndian.PutUint32(mbr[446+8:446+12], 1)
	if total-1 > 0xFFFFFFFF {
		binary.LittleEndian.PutUint32(mbr[446+12:446+16], 0xFFFFFFFF)
	} else {
		binary.LittleEndian.PutUint32(mbr[446+12:446+16], uint32(total-1))
	}
	mbr[510], mbr[511] = 0x55, 0xAA

	fh, err := os.OpenFile(raw, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	write := func(at int64, payload []byte) error {
		_, err := fh.WriteAt(payload, at)
		return err
	}
	for _, w := range []struct {
		at      int64
		payload []byte
	}{
		{0, mbr},
		{gptSector, primary},
		{2 * gptSector, table},
		{int64(total-1-gptEntrySectors) * gptSector, table},
		{int64(total-1) * gptSector, backup},
	} {
		if err := write(w.at, w.payload); err != nil {
			fh.Close()
			return fmt.Errorf("writing the partition table: %v", err)
		}
	}
	if err := fh.Close(); err != nil {
		return err
	}

	if out, err := exec.Command(qemuImg, "convert", "-f", "raw", "-O", "qcow2", raw, path).
		CombinedOutput(); err != nil {
		return fmt.Errorf("re-encoding %s after writing its partition table: %v (%s)",
			filepath.Base(path), err, firstLineOf(string(out)))
	}
	return nil
}
