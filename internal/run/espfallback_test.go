package run

// THE WRITE-BACK IS THE ONLY PART OF `esp_fallback:` THAT TOUCHES THE DISK, AND IT WAS THE PART
// THAT DID NOTHING.
//
// `newFat32` stored the caller's slice, so `fat.img` and `image` were the same memory: the
// comparison that decides which blocks changed compared a buffer with ITSELF, every block looked
// unchanged, and the step wrote nothing while reporting success. Measured on a real run - the disk
// was published with `/EFI/BLISSOS` intact and `/EFI/BOOT` absent, and firmware answered
// "No bootable option or device was found".
//
// These tests are about that aliasing and about the FAT structures, not about qemu: the parts that
// need a qcow2 are covered by the walks that use them.

import "testing"

// anESP builds a minimal FAT32 image the size of the installer's own: 260 MiB, 512-byte sectors,
// 8 per cluster, 6 reserved, 2 FATs of 519 sectors, root cluster 2 - the geometry mkdosfs chose on
// the disk every measurement in this file came from.
func anESP(t *testing.T) []byte {
	t.Helper()
	const (
		bps, spc, reserved, nfats, fatSectors = 512, 8, 6, 2, 519
		clusters                              = (260 << 20) / (bps * spc)
	)
	img := make([]byte, 260<<20)
	img[0], img[1], img[2] = 0xEB, 0x58, 0x90
	copy(img[3:11], "mkdosfs\x00")
	put16 := func(at int, v uint16) { img[at], img[at+1] = byte(v), byte(v>>8) }
	put32 := func(at int, v uint32) {
		img[at], img[at+1], img[at+2], img[at+3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
	}
	put16(11, bps)
	img[13] = spc
	put16(14, reserved)
	img[16] = nfats
	img[21] = 0xF8
	put16(22, 0) // FATSz16 must be zero on a FAT32
	put32(32, uint32(clusters*spc))
	put32(36, fatSectors)
	put32(44, 2)
	img[510], img[511] = 0x55, 0xAA
	for n := 0; n < nfats; n++ {
		off := (reserved + n*fatSectors) * bps
		put32(off, 0x0FFFFFF8)
		put32(off+4, 0x0FFFFFFF)
		put32(off+8, 0x0FFFFFFF) // cluster 2, the root, is last
	}
	return img
}

func TestNewFat32DoesNotAliasItsInput(t *testing.T) {
	img := anESP(t)
	before := append([]byte(nil), img...)
	fat, err := newFat32(img)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fat.mkdir(fat.root, "EFI"); err != nil {
		t.Fatal(err)
	}
	// THE CALLER'S SLICE MUST STILL BE UNTOUCHED. It is the "what the disk looked like" side of the
	// write-back's comparison, so a shared buffer makes every block look unchanged.
	for i := range img {
		if img[i] != before[i] {
			t.Fatalf("newFat32's input changed at byte %d: the write-back compares against this "+
				"slice and would conclude nothing changed", i)
		}
	}
	if len(fat.img) != len(img) {
		t.Fatalf("the copy is %d bytes, the image %d", len(fat.img), len(img))
	}
}

func TestFallbackCreatesBootAndCopiesTheLoader(t *testing.T) {
	fat, err := newFat32(anESP(t))
	if err != nil {
		t.Fatal(err)
	}
	efi, err := fat.mkdir(fat.root, "EFI")
	if err != nil {
		t.Fatal(err)
	}
	blissos, err := fat.mkdir(efi, "BLISSOS")
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("MZ fake loader")
	if err := fat.writeFile(blissos, "BOOTX64.EFI", payload); err != nil {
		t.Fatal(err)
	}
	if err := fat.writeFile(blissos, "GRUB.CFG",
		[]byte("search.fs_uuid 1234-5678 root\nset kdir=/android\n")); err != nil {
		t.Fatal(err)
	}
	if err := fat.writeFile(blissos, "ANDROID.CFG", []byte("menuentry x\n")); err != nil {
		t.Fatal(err)
	}

	// The fallback, as `espFallback` does it once it has the partition.
	boot, err := fat.mkdir(efi, "BOOT")
	if err != nil {
		t.Fatal(err)
	}
	if err := fat.writeFile(boot, "BOOTX64.EFI", payload); err != nil {
		t.Fatal(err)
	}

	got := fat.find(boot, "BOOTX64.EFI")
	if got == nil {
		t.Fatal("/EFI/BOOT/BOOTX64.EFI is not in the tree after the copy")
	}
	back := fat.readChain(got.cluster)[:got.size]
	if string(back) != string(payload) {
		t.Errorf("the copied loader reads back as %q, want %q", back, payload)
	}
	// AND THE SOURCE IS STILL THERE: the installer's own directory is left alone, so a later boot
	// that DOES have an NVRAM entry naming /EFI/BLISSOS still works.
	if fat.find(blissos, "BOOTX64.EFI") == nil {
		t.Error("the source loader was moved rather than copied")
	}
}

// THE PARSE IS THE OTHER HALF OF THE WRITE-BACK, AND IT IS EASY TO GET WRONG IN SILENCE.
func TestParseHexDumpReadsSixteenBytesALine(t *testing.T) {
	// The exact shape `qemu-io read -v` prints: an offset, a colon, TWO spaces, sixteen pairs, two
	// spaces, then the ASCII column. Splitting the remainder on "  " yields the EMPTY string, which
	// is how an earlier version parsed zero bytes a line and reported "no GPT header" about a disk
	// that has one.
	dump := "00000200:  45 46 49 20 50 41 52 54 00 00 01 00 5c 00 00 00  EFI.PART........\n"
	got := parseHexDump(dump)
	want := []byte{0x45, 0x46, 0x49, 0x20, 0x50, 0x41, 0x52, 0x54, 0x00, 0x00, 0x01, 0x00, 0x5c,
		0x00, 0x00, 0x00}
	if len(got) != len(want) {
		t.Fatalf("parsed %d bytes, want %d: % x", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("byte %d = %02x, want %02x", i, got[i], want[i])
		}
	}
}
