package net

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"
)

// THE TRANSPARENT PROXY IS FOR A LAB, AND THESE TESTS ARE ABOUT THE TWO THINGS THAT CAN GO WRONG
// SILENTLY: it starts when nobody asked for one, and it tunnels to the wrong place or in the wrong
// order.
//
// A GUEST THAT KNOWS NOTHING ABOUT PROXIES STILL HAS TO GO THROUGH ONE, which is why the redirection
// is in iptables rather than in an environment variable: `pkg`, `fetch`, `git` and anything a
// walkthrough downloads each decide for themselves whether to honour `http_proxy`, and a bake that
// works only for the tools that happened to read it measures those tools.
func TestNoProxyMeansNoListener(t *testing.T) {
	t.Setenv(proxyEnv, "")
	if up, ok := ProxyConfigured(); ok {
		t.Fatalf("ProxyConfigured() reported %q with %s unset", up, proxyEnv)
	}
}

func TestTheUpstreamIsTheEnvironmentAndNothingElse(t *testing.T) {
	const addr = "172.17.0.121:7890"
	t.Setenv(proxyEnv, addr)
	up, ok := ProxyConfigured()
	if !ok {
		t.Fatalf("ProxyConfigured() said there was no proxy with %s=%s", proxyEnv, addr)
	}
	if up != addr {
		t.Fatalf("ProxyConfigured() = %q, want %q", up, addr)
	}
}

// A MALFORMED UPSTREAM IS REFUSED AT STARTUP, NOT AT THE FIRST GUEST REQUEST.
//
// The difference matters: a daemon that accepted `mihomo` and failed later would leave every guest
// connection going nowhere, and the symptom would be a download that hangs inside `pkg` - three
// layers away from the typo.
func TestAMalformedUpstreamIsRefusedBeforeAnythingListens(t *testing.T) {
	for _, bad := range []string{"mihomo", "mihomo:proxy", ":7890"} {
		t.Run(bad, func(t *testing.T) {
			if p, err := StartTransparentProxy(bad); err == nil {
				p.Close()
				t.Fatalf("StartTransparentProxy(%q) succeeded; it should have been refused", bad)
			}
		})
	}
}

// THE TUNNEL ITSELF, AGAINST A FAKE PROXY.
//
// THIS IS THE PART THAT CANNOT BE TESTED BY READING THE CODE: the listener has to ask its upstream
// for `CONNECT host:port`, wait for the 200, DRAIN the proxy's headers, and only then move bytes.
// Getting that order wrong sends the guest's payload into a socket the proxy is still expecting a
// request line on - and the leftover headers arrive as garbage at the front of the guest's stream.
//
// `tunnel` TAKES THE DESTINATION AS AN ARGUMENT precisely so this can be tested: `handle` reads it
// from SO_ORIGINAL_DST, which only a genuinely redirected socket has.
func TestTheTunnelSpeaksConnectDrainsHeadersAndThenCopies(t *testing.T) {
	fake, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer fake.Close()

	type seen struct {
		request string
		payload string
	}
	got := make(chan seen, 1)
	go func() {
		conn, err := fake.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		// DRAIN THE CONNECT HEADERS THE WAY A REAL PROXY SENDS THEM. If the tunnel forwarded these
		// as payload they would be the first thing this reader saw after the blank line.
		for {
			l, err := br.ReadString('\n')
			if err != nil || l == "\r\n" || l == "\n" {
				break
			}
		}
		_, _ = conn.Write([]byte("HTTP/1.1 200 Connection established\r\nProxy-Agent: fake\r\n\r\n"))
		buf := make([]byte, 64)
		n, _ := br.Read(buf)
		got <- seen{request: strings.TrimSpace(line), payload: string(buf[:n])}
	}()

	p := &TransparentProxy{Upstream: fake.Addr().String()}
	client, server := net.Pipe()
	go func() {
		// THE PAYLOAD IS SENT ONLY AFTER THE TUNNEL IS UP, which is what a guest does: it writes to
		// what it believes is a plain socket.
		time.Sleep(50 * time.Millisecond)
		_, _ = client.Write([]byte("HELLO"))
	}()
	p.tunnel(server, "pkg.freebsd.org:443")

	select {
	case s := <-got:
		if !strings.HasPrefix(s.request, "CONNECT pkg.freebsd.org:443 ") {
			t.Fatalf("the proxy saw %q; it must ask for the ORIGINAL destination", s.request)
		}
		if !strings.HasSuffix(s.request, "HTTP/1.1") {
			t.Fatalf("the CONNECT line %q has no HTTP version", s.request)
		}
		if s.payload != "HELLO" {
			t.Fatalf("the proxy received %q after the handshake, want %q - the tunnel dropped, "+
				"reordered or prefixed the guest's bytes", s.payload, "HELLO")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the fake proxy never saw a request: the tunnel did not send its CONNECT line")
	}
}

// A REFUSED CONNECT MUST NOT LEAK THE PROXY'S ANSWER TO THE GUEST.
//
// A 403 or a 502 is the proxy's sentence about the request, not data for the program inside the
// guest. Forwarding it would put `HTTP/1.1 502` at the front of a TLS stream, and the guest would
// report a protocol error from a server it never reached.
func TestARefusedConnectDeliversNothingToTheGuest(t *testing.T) {
	fake, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer fake.Close()
	go func() {
		conn, err := fake.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		for {
			l, err := br.ReadString('\n')
			if err != nil || l == "\r\n" || l == "\n" {
				break
			}
		}
		_, _ = conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
	}()

	p := &TransparentProxy{Upstream: fake.Addr().String()}
	client, server := net.Pipe()
	buf := make([]byte, 64)
	n, _ := client.Read(buf)
	if n != 0 {
		t.Fatalf("the guest received %q from a refused CONNECT; it must receive nothing", buf[:n])
	}
	p.tunnel(server, "pkg.freebsd.org:443")
}
