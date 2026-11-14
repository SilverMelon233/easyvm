package net

import (
	"os"
	"syscall"
	"unsafe"
)

// THE FOUR CONSTANTS SO_ORIGINAL_DST NEEDS, DECLARED HERE RATHER THAN IMPORTED.
//
// `golang.org/x/sys/unix` has them, and so does every proxy implementation, but they are a hundred
// lines of dependency for four numbers that do not change: these are Linux's own values from
// <linux/netfilter_ipv4.h> and <linux/netfilter_ipv6/ip6_tables.h>.
const (
	// solIP is SOL_IP - the level SO_ORIGINAL_DST is asked at for an IPv4 socket.
	solIP = 0
	// solIPv6 is SOL_IPV6, the same question for an IPv6 socket.
	solIPv6 = 41
	// soOriginalDst is SO_ORIGINAL_DST, whose value is 80 for both families.
	soOriginalDst = 80
)

// osGetenv is `os.Getenv`, behind a name so proxy.go does not import os for one call and so a test
// can see that the only environment read in this file is named here.
func osGetenv(key string) string { return os.Getenv(key) }

// getsockopt is the raw syscall, because Go's net package exposes no way to ask for a socket option
// it does not know about and SO_ORIGINAL_DST is not in syscall's lowercase list.
//
// `syscall.Syscall6` RATHER THAN `unix.GetsockoptIPv6Mreq` OR SIMILAR: none of the typed helpers
// takes an arbitrary option number with an arbitrary buffer, which is exactly the shape this needs.
func getsockopt(fd, level, opt int, buf []byte, n *uint32) error {
	_, _, errno := syscall.Syscall6(
		syscall.SYS_GETSOCKOPT,
		uintptr(fd),
		uintptr(level),
		uintptr(opt),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(n)),
		0,
	)
	if errno != 0 {
		return errno
	}
	return nil
}
