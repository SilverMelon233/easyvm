package run

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGPTGUIDBytes(t *testing.T) {
	got := gptGUIDBytes("C12A7328-F81F-11D2-BA4B-00A0C93EC93B")
	want := []byte{0x28, 0x73, 0x2a, 0xc1, 0x1f, 0xf8, 0xd2, 0x11,
		0xba, 0x4b, 0x00, 0xa0, 0xc9, 0x3e, 0xc9, 0x3b}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("byte %d = %02x, want %02x (all: %x)", i, got[i], want[i], got)
		}
	}
}

// THE WHOLE LAYOUT, READ BACK FROM THE BYTES.
//
// A GUID that converts and a table that a real firmware accepts are different claims, and the second
// one is what the installer tests. This reads the written disk the same way a partition tool does.
func TestWriteGPTProducesATableTheInstallerAccepts(t *testing.T) {
	dir := t.TempDir()
	disk := filepath.Join(dir, "disk.qcow2")
	if out, err := exec.Command("qemu-img", "create", "-f", "qcow2", disk, "2G").CombinedOutput(); err != nil {
		t.Fatalf("qemu-img create: %v (%s)", err, out)
	}
	if err := writeGPT(disk, 2, 260); err != nil {
		t.Fatalf("writeGPT: %v", err)
	}

	// Convert back and read LBA 1 and the entry array with the same reader a tool would use.
	raw := filepath.Join(dir, "back.raw")
	if out, err := exec.Command("qemu-img", "convert", "-f", "qcow2", "-O", "raw", disk, raw).
		CombinedOutput(); err != nil {
		t.Fatalf("convert back: %v (%s)", err, out)
	}
	img, err := os.ReadFile(raw)
	if err != nil {
		t.Fatal(err)
	}

	if string(img[512:520]) != "EFI PART" {
		t.Fatalf("no GPT header at LBA 1: %q", img[512:520])
	}
	// The header's own CRC, computed the way the spec says.
	hdr := append([]byte(nil), img[512:512+92]...)
	gotCRC := binary.LittleEndian.Uint32(hdr[16:20])
	binary.LittleEndian.PutUint32(hdr[16:20], 0)
	if crc32.ChecksumIEEE(hdr) != gotCRC {
		t.Errorf("the header CRC is wrong: %08x", gotCRC)
	}

	entryLBA := binary.LittleEndian.Uint64(img[512+72 : 512+80])
	table := img[entryLBA*512:]
	if crc32.ChecksumIEEE(table[:gptEntries*gptEntrySize]) !=
		binary.LittleEndian.Uint32(img[512+88:512+92]) {
		t.Error("the partition array CRC is wrong")
	}

	first := binary.LittleEndian.Uint64(table[32:40])
	if first != gptESPStart {
		t.Errorf("the ESP starts at LBA %d, and the installer requires %d (1 MiB aligned)",
			first, gptESPStart)
	}
	wantESP := []byte{0x28, 0x73, 0x2a, 0xc1, 0x1f, 0xf8, 0xd2, 0x11,
		0xba, 0x4b, 0x00, 0xa0, 0xc9, 0x3e, 0xc9, 0x3b}
	for i := range wantESP {
		if table[i] != wantESP[i] {
			t.Fatalf("the ESP type GUID is wrong: %x\nwant %x", table[:16], wantESP)
		}
	}
	// And the data partition must be Linux, or the installer treats it as another ESP.
	wantLinux := []byte{0xaf, 0x3d, 0xc6, 0x0f, 0x83, 0x84, 0x72, 0x47,
		0x8e, 0x79, 0x3d, 0x69, 0xd8, 0x47, 0x7d, 0xe4}
	if !bytes.Equal(table[gptEntrySize:gptEntrySize+16], wantLinux) {
		t.Errorf("the second partition's type is %x, want the Linux GUID %x",
			table[gptEntrySize:gptEntrySize+16], wantLinux)
	}
}
