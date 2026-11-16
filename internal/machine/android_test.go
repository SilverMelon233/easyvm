package machine

import "testing"

func TestAndroidBootPutsTheKernelBeforeTheDrives(t *testing.T) {
	b := Boot{OS: Android, Disk: "/tmp/d.qcow2", ISO: "/tmp/i.iso",
		Kernel: "/tmp/kernel", Initrd: "/tmp/initrd.img", KernelAppend: "root=/dev/ram0 INSTALL=1"}
	args := b.StorageArgs()
	first := map[string]int{}
	for i, a := range args {
		if _, seen := first[a]; !seen {
			first[a] = i
		}
	}
	ki, di := first["-kernel"], first["-drive"]
	if ki == 0 && args[0] != "-kernel" {
		t.Fatalf("-kernel is not first: %v", args)
	}
	if ki > di {
		t.Errorf("-kernel at %d is AFTER the first -drive at %d; qemu accepts it and boots the firmware", ki, di)
	}
	for _, want := range []string{"-initrd", "-append"} {
		if _, ok := first[want]; !ok {
			t.Errorf("%s is missing from %v", want, args)
		}
	}
}

func TestAnInstalledAndroidDiskHasNoKernel(t *testing.T) {
	b := Boot{OS: Android, Disk: "/tmp/d.qcow2"}
	for _, a := range b.StorageArgs() {
		if a == "-kernel" || a == "-initrd" {
			t.Fatalf("an installed Android disk must not be handed a kernel: %v", b.StorageArgs())
		}
	}
}

func TestKernelIsRefusedForOtherFamilies(t *testing.T) {
	for _, os := range []OS{Windows, Linux, MacOS, BSD} {
		b := Boot{OS: os, Disk: "/tmp/d", Kernel: "/tmp/k", Initrd: "/tmp/i"}
		if b.Kernel != "" && os != Android {
			if err := b.Validate(); err == nil {
				t.Errorf("%s accepted a kernel; a guest handed a kernel cannot prove its disk boots", os)
			}
		}
	}
}
