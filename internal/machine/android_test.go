package machine

import (
	"os"
	"path/filepath"
	"testing"
)

// THE ADAPTER IS A PLATFORM FACT AND IT IS THE ONE THAT DECIDES WHETHER ANDROID DRAWS AT ALL.
func TestAndroidGetsVirtioVGA(t *testing.T) {
	b := Boot{OS: Android}
	if got := b.VGA("std"); got != "virtio" {
		t.Errorf("android got %q; HWC=drm_minigbm needs /dev/dri/card0 and a std VGA has no DRM "+
			"device, so the guest draws nothing and the screen stops on initrd's last line", got)
	}
	for _, os := range []OS{Windows, Linux, BSD} {
		if got := (Boot{OS: os}).VGA("std"); got != "std" {
			t.Errorf("%s got %q, want the default", os, got)
		}
	}
	if got := (Boot{OS: MacOS}).VGA("std"); got != "none" {
		t.Errorf("macOS got %q, want none: OpenCore supplies vmware-svga", got)
	}
}

// AND THE ISO IS THE MEDIUM, NOT A KERNEL. A boot with `iso:` and no kernel is the shape that runs
// the installer's own boot chain, which is the only one that proves anything about the disk.
func TestAnAndroidInstallBootsItsISO(t *testing.T) {
	dir := t.TempDir()
	disk := filepath.Join(dir, "d.qcow2")
	iso := filepath.Join(dir, "i.iso")
	for _, f := range []string{disk, iso} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b := Boot{OS: Android, Disk: disk, ISO: iso}
	if err := b.Validate(); err != nil {
		t.Fatalf("an android ISO boot was refused: %v", err)
	}
	for _, a := range b.StorageArgs() {
		if a == "-kernel" || a == "-initrd" || a == "-append" {
			t.Fatalf("an android install must boot its own ISO, not a host-supplied kernel: %v",
				b.StorageArgs())
		}
	}
}

// THE ADAPTER IS NOT A PREFERENCE, AND THE REFUSAL LIVES BESIDE THE CHOICE.
//
// A std VGA on an Android guest gives it a framebuffer with no DRM node; HWC=drm_minigbm opens
// /dev/dri/card0, finds nothing, and Android draws nothing at all. The screen stops on initrd's last
// line and the console says nothing, which is why this was chased at 240 s, 600 s and 600 s again
// before the cause was found.
func TestAndroidRefusesAnyOtherAdapter(t *testing.T) {
	for _, asked := range []string{"std", "cirrus", "qxl", "vmware"} {
		if got := (Boot{OS: Android}).VGA(asked); got != AndroidVGA {
			t.Errorf("VGA(%q) = %q on android; it must be %q whatever the caller asked for",
				asked, got, AndroidVGA)
		}
	}
	// And the value the daemon turns into `-device virtio-vga,xres=...` is the short name.
	if AndroidVGA != "virtio" {
		t.Errorf("AndroidVGA = %q; the daemon's switch keys on `virtio`", AndroidVGA)
	}
}
