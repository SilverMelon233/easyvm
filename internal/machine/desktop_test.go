package machine

import (
	"strings"
	"testing"
)

// A MICROVM CANNOT HAVE A DESKTOP, AND THE REFUSAL IS THE FEATURE.
//
// MEASURED on 2026-11-18 against qemu 11.1.2 with Debian 6.12 and Alpine 3.21, on both adapters a
// `microvm` machine can create:
//
//	virtio-gpu-device   the device attaches to virtio-mmio-bus.23 and `virtio_gpu` loads, but the
//	                    probe fails: the virtio status comes back 0x83 (NEEDS_RESET), the driver
//	                    never binds (virtio1/driver does not exist), dmesg logs nothing about it,
//	                    and /sys/class/drm holds only `version` - no card0. X answers
//	                    `(EE) no screens found`.
//	isa-vga             a TEXT adapter. `modesetting` has no DRM node to open; `fbdev` needs
//	                    /dev/fb0 and there is none, because CONFIG_FB_VESA wants a VBE BIOS call
//	                    and a microvm HAS NO FIRMWARE. /sys/class/graphics lists only `fbcon`.
//
// Before this test the vocabulary told a reader the opposite: `keys.go` described `desktop` as
// "VGA with a window manager", and a walkthrough named `desktop.walk` recorded a text login prompt
// for weeks without anyone noticing, because a recording of a console is not obviously wrong until
// somebody plays it.
//
// SO THE ASSERTION IS A REFUSAL: a file that asks for a desktop on a microvm must FAIL BY NAME
// rather than spend two minutes producing a black video. The q35 case is checked beside it, because
// a desktop there is real and must keep working.
func TestAMicroVMCannotAskForADesktop(t *testing.T) {
	disk, _, _, _, _, _, _, _ := fixture(t)

	b := Boot{OS: Linux, Disk: disk, MicroVM: true, MicroVMDisplay: "desktop"}
	err := b.Validate()
	if err == nil {
		t.Fatal("a microvm with `microvm_display: desktop` was accepted. It cannot draw: measured, " +
			"`virtio-gpu-device` never binds (status 0x83) and `isa-vga` has no /dev/fb0 without " +
			"firmware. The run would record a black field and report success")
	}
	// THE MESSAGE HAS TO NAME THE TWO SHAPES THAT DO WORK, or a reader is left with a refusal and
	// no next step.
	for _, want := range []string{"serial", "terminal", "q35"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q, so it says what is wrong but not what to do: %v",
				want, err)
		}
	}
}

// AND THE TWO SHAPES A MICROVM CAN HAVE ARE ACCEPTED, so the check above is narrow rather than a
// blanket ban on the key.
func TestTheTwoAdapterShapesAMicroVMAccepts(t *testing.T) {
	disk, _, _, _, _, _, _, _ := fixture(t)
	for _, shape := range []string{"serial", "terminal", ""} {
		b := Boot{OS: Linux, Disk: disk, MicroVM: true, MicroVMDisplay: shape}
		if err := b.Validate(); err != nil {
			t.Errorf("a microvm with microvm_display=%q was refused: %v", shape, err)
		}
	}
}

// A FULL MACHINE KEEPS ITS DESKTOP. `desktop` on a q35 boot is the shape every GUI installer here
// uses, so the refusal must not leak out of the microvm branch.
func TestAQ35BootStillTakesADesktop(t *testing.T) {
	disk, _, _, _, _, _, _, _ := fixture(t)
	b := Boot{OS: Linux, Disk: disk, MicroVM: false, MicroVMDisplay: "desktop"}
	if err := b.Validate(); err != nil {
		t.Fatalf("a desktop on a full machine was refused, which breaks every installer walkthrough: %v", err)
	}
}
