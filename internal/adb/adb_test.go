package adb

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// THE HEADER IS SIX WORDS AND THE MAGIC IS THE COMMAND INVERTED. Both halves were wrong in the first
// version, and both failures look like "the device refused us" rather than "our packet was
// malformed" - which is why they are pinned here rather than left to the integration test.
// rwPair is one buffer both written and read, which is what `Client.rw` needs.
type rwPair struct {
	written strings.Builder
	toRead  *strings.Reader
}

func (r *rwPair) Write(p []byte) (int, error) { return r.written.Write(p) }
func (r *rwPair) Read(p []byte) (int, error) {
	if r.toRead == nil {
		return 0, io.EOF
	}
	return r.toRead.Read(p)
}

func TestTheHeaderIsSixWordsWithAnInvertedMagic(t *testing.T) {
	buf := &rwPair{}
	c := &Client{rw: buf}
	if err := c.send(cmdCNXN, AVersion, maxPayload, []byte("host::\x00")); err != nil {
		t.Fatal(err)
	}
	got := buf.written.String()
	if len(got) != 24+7 {
		t.Fatalf("wrote %d bytes, want 24 header + 7 payload", len(got))
	}
	get := func(at int) uint32 {
		return uint32(got[at]) | uint32(got[at+1])<<8 | uint32(got[at+2])<<16 | uint32(got[at+3])<<24
	}
	if get(0) != cmdCNXN {
		t.Errorf("command = %#x, want %#x", get(0), cmdCNXN)
	}
	if get(12) != 7 {
		t.Errorf("data_length = %d, want 7", get(12))
	}
	// THE SIXTH WORD. A header of five words puts the payload's first word here, adbd compares it
	// against the inverted command, disagrees, and closes the socket.
	if get(20) != cmdCNXN^0xFFFFFFFF {
		t.Errorf("magic = %#x, want %#x (the command inverted)", get(20), cmdCNXN^0xFFFFFFFF)
	}
}

func TestABadMagicIsReportedRatherThanRead(t *testing.T) {
	// A socket that is not adb - a web server, a wrong port - must not be parsed as one.
	header := make([]byte, 24)
	header[0] = 'H'
	header[1] = 'T'
	header[2] = 'T'
	header[3] = 'P'
	c := &Client{rw: &rwPair{toRead: strings.NewReader(string(header))}}
	if _, _, _, _, err := c.recv(); err == nil {
		t.Fatal("a reply with the wrong magic was accepted")
	} else if !strings.Contains(err.Error(), "not speaking adb") {
		t.Errorf("the error does not say what is wrong: %v", err)
	}
}

func TestBannerPicksTheDeviceName(t *testing.T) {
	payload := []byte("device::ro.product.name=Standard PC (Q35 + ICH9, 2009);" +
		"ro.product.model=Generic Android-x86_64;features=shell_v2,cmd\x00")
	got := banner(payload)
	// THE SECOND FIELD IS THE DESCRIPTION. The first is the literal `device::`, and taking it would
	// put the word "device" in every log line instead of the machine's own name.
	if !strings.Contains(got, "ro.product.model") {
		t.Errorf("banner = %q; want the second `;`-separated field, which is the device's own "+
			"description", got)
	}
}

// THE INTEGRATION TEST IS SKIPPED UNLESS A GUEST IS NAMED, because this package's whole point is a
// socket to a running machine and `go test ./...` runs where there is none.
//
//	EASYVM_ADB=172.30.0.2:5555 go test ./internal/adb/ -run TestAgainstAGuest -v
func TestAgainstAGuest(t *testing.T) {
	addr := getenv("EASYVM_ADB")
	if addr == "" {
		t.Skip("EASYVM_ADB is not set (host:port of a running Android guest's adbd)")
	}
	host, port, _ := strings.Cut(addr, ":")
	if port == "" {
		port = "5555"
	}
	c, err := Dial(host, atoi(port), 10*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	if c.Banner == "" {
		t.Error("the CNXN banner is empty; it is what proves the device answered rather than a proxy")
	}

	out, code, err := c.Run("id -un", 15*time.Second)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != 0 {
		t.Fatalf("`id -un` exited %d: %q", code, out)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("`id -un` printed nothing")
	}

	// AND A NON-ZERO EXIT HAS TO ARRIVE AS ONE. A stream that reported 0 for everything would make
	// every later `adb:` step pass whether or not its command worked.
	_, code, err = c.Run("exit 7", 15*time.Second)
	if err != nil {
		t.Fatalf("run exit 7: %v", err)
	}
	if code != 7 {
		t.Errorf("`exit 7` came back as %d; the exit code has to cross the wire", code)
	}
}

// getenv and atoi are here rather than imported so this file names no package the package under
// test does not already use.
func getenv(key string) string {
	return os.Getenv(key)
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
