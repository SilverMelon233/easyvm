package machine

import (
	"strings"
	"testing"
)

// A MICROVM'S DESKTOP RUNS ON virtio-gpu OVER PCIe. THE TRANSPORT IS THE WHOLE STORY.
//
// MEASURED on 2026-11-18 against qemu 11.1.2 with Debian 6.12 and Alpine 3.21, on the two adapters
// a microvm could then create:
//
//	virtio-gpu-device   on virtio-mmio-bus.23 the device attaches and `virtio_gpu` loads, but the
//	                    probe fails: virtio status 0x83 (NEEDS_RESET), virtio1/driver does not
//	                    exist, dmesg logs nothing, /sys/class/drm holds only `version`. X answers
//	                    `(EE) no screens found`.
//	isa-vga             a TEXT adapter, and no /dev/fb0 either, because CONFIG_FB_VESA wants a
//	                    VBE BIOS call and a microvm HAS NO FIRMWARE.
//
// THE FAULT WAS THE MMIO TRANSPORT, NOT THE MACHINE, AND THAT WAS MEASURED TOO. The same image
// booted with `-M microvm,pcie=on` and `-device virtio-gpu-pci` reports
//
//	virtio-gpu-pci detected at 0000:00:01.0
//	[drm] Initialized virtio_gpu 0.1.0 for 0000:00:01.0 on minor 0
//	virtio_gpudrmfb frame buffer device
//	/sys/class/drm: card0 card0-Virtual-1 renderD128
//
// - one device carrying BOTH the KMS output and the render node, which is what Mutter, KWin and
// wlroots' EGL path need. So `desktop` is ACCEPTED on a microvm, and this test holds the three
// facts that make it work: the boot is accepted, the kernel line keeps the PCI bus, and the
// machine type asks for PCIe only for a desktop.
func TestAMicroVMDesktopIsAccepted(t *testing.T) {
	disk, _, _, _, _, _, _, _ := fixture(t)

	for _, shape := range []string{"desktop", "serial", "terminal", ""} {
		b := Boot{OS: Linux, Disk: disk, MicroVM: true, MicroVMDisplay: shape}
		if err := b.Validate(); err != nil {
			t.Errorf("a microvm with microvm_display=%q was refused: %v", shape, err)
		}
	}
}

// THE PCI BUS MUST SURVIVE TO THE KERNEL. `pci=off` is prepended to every microvm line that has no
// PCI bus, because some guests stop at `PCI: Fatal: No config space access function found` without
// it. A desktop microvm has the bus ON PURPOSE, and hiding it would hide the GPU with it.
func TestADesktopMicroVMKeepsItsPCIBus(t *testing.T) {
	disk, _, _, _, _, _, _, _ := fixture(t)

	desktop := Boot{
		OS: Linux, Disk: disk, MicroVM: true, MicroVMDisplay: "desktop",
		Kernel: "/vmlinuz", KernelAppend: "console=ttyS0 root=/dev/vda rw",
	}
	for _, arg := range desktop.KernelArgs() {
		if strings.Contains(arg, "pci=off") {
			t.Fatalf("a desktop microvm's kernel line carries pci=off, which hides the PCIe bus "+
				"virtio-gpu-pci is attached to: %q", arg)
		}
	}

	// AND A GUEST WITH NO PCI BUS STILL GETS THE STATEMENT, including one that wrote its own line.
	for _, shape := range []string{"serial", "terminal"} {
		b := Boot{
			OS: Linux, Disk: disk, MicroVM: true, MicroVMDisplay: shape,
			Kernel: "/vmlinuz", KernelAppend: "console=ttyS0 root=/dev/vda rw",
		}
		joined := strings.Join(b.KernelArgs(), " ")
		if !strings.Contains(joined, "pci=off") {
			t.Errorf("a %q microvm lost pci=off, which is what keeps a guest with no PCI bus from "+
				"stopping on a fatal PCI probe: %v", shape, b.KernelArgs())
		}
	}
}

// A FULL MACHINE KEEPS ITS DESKTOP. `desktop` on a q35 boot is the shape every GUI installer here
// uses, so the microvm branch must not change it.
func TestAQ35BootStillTakesADesktop(t *testing.T) {
	disk, _, _, _, _, _, _, _ := fixture(t)
	b := Boot{OS: Linux, Disk: disk, MicroVM: false, MicroVMDisplay: "desktop"}
	if err := b.Validate(); err != nil {
		t.Fatalf("a desktop on a full machine was refused, which breaks every installer walkthrough: %v", err)
	}
}
