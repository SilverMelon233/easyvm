// EGRESS PROBE: the fake upstreams an end-to-end test needs, in one binary.
//
// WHY A PROGRAM RATHER THAN A REAL PROXY. The question CI has to answer is "did this connection take
// the path the rules chose", and a real proxy answers that only by being inspected. These do it by
// REPLYING with their own name: whatever the guest receives says which hop carried it.
//
// THREE MODES, AND EACH ONE IS A SEPARATE LISTENER:
//
//	http      a plain HTTP server that returns a marker. Reached directly, it proves the `direct`
//	          path; it is the destination a proxied connection is tunnelled TO.
//	connect   a minimal HTTP CONNECT proxy. It does NOT forward: it answers the CONNECT with 200 and
//	          then writes an HTTP response carrying its own marker, so the guest sees which proxy was
//	          used without any backend being involved.
//	udp-echo  a UDP server that replies with a marker plus the payload length.
//
// Every listener prints one line per request, so the job's log is also evidence.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
)

func main() {
	mode := flag.String("mode", "http", "http | connect | udp-echo")
	addr := flag.String("addr", ":8080", "listen address")
	name := flag.String("name", "probe", "the marker this listener returns")
	flag.Parse()

	switch *mode {
	case "http":
		runHTTP(*addr, *name)
	case "connect":
		runConnect(*addr, *name)
	case "udp-echo":
		runUDPEcho(*addr, *name)
	default:
		log.Fatalf("unknown mode %q", *mode)
	}
}

func runHTTP(addr, name string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("http[%s]: %s %s from %s\n", name, r.Method, r.URL.Path, r.RemoteAddr)
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintln(w, name)
	})
	log.Printf("http %s listening on %s", name, addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

// runConnect is an HTTP CONNECT proxy that answers with its own name instead of forwarding.
//
// THE 200 IS SENT FIRST because that is the protocol: a client writes its request line and waits for
// the status before sending anything. A proxy that replied with a body and no status would deadlock a
// guest that is waiting to write.
func runConnect(addr, name string) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("connect %s listening on %s", name, addr)
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			br := bufio.NewReader(c)
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			// DRAIN THE CONNECT HEADERS, or they arrive at the client as garbage in front of the body.
			for {
				l, err := br.ReadString('\n')
				if err != nil || l == "\r\n" || l == "\n" {
					break
				}
			}
			target := strings.TrimPrefix(strings.TrimSpace(line), "CONNECT ")
			target = strings.TrimSuffix(target, " HTTP/1.1")
			fmt.Printf("connect[%s]: %s\n", name, target)
			if _, err := io.WriteString(c, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
				return
			}
			// THE BODY IS A COMPLETE HTTP RESPONSE, because the guest is speaking HTTP through the
			// tunnel and will parse it. Anything else would be a truncated response.
			body := name + "\n"
			fmt.Fprintf(c, "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: %d\r\n"+
				"Connection: close\r\n\r\n%s", len(body), body)
		}(c)
	}
}

func runUDPEcho(addr, name string) {
	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		log.Fatal(err)
	}
	c, err := net.ListenUDP("udp", ua)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("udp-echo %s listening on %s", name, addr)
	buf := make([]byte, 65535)
	for {
		n, from, err := c.ReadFromUDP(buf)
		if err != nil {
			return
		}
		fmt.Printf("udp-echo[%s]: %d bytes from %s: %q\n", name, n, from,
			string(buf[:min(n, 40)]))
		// THE MARKER PLUS THE PAYLOAD LENGTH, so a test can tell a reply from the echo it sent from a
		// reply that some other listener produced.
		if _, err := c.WriteToUDP([]byte(fmt.Sprintf("%s %d\n", name, n)), from); err != nil {
			return
		}
	}
}

