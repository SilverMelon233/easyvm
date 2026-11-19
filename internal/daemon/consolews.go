package daemon

import (
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/coder/websocket"
)

// consoleSocket is where qemu's `mux` chardev puts the second copy of the serial line.
//
// THE FILE IS STILL THE RECORD. `console.log` keeps being written because `ConsoleState` measures it
// - its size is "how much has the guest said", its idle time is "has it stopped" - and a failed run
// still has its evidence on disk. This socket is the same bytes for a live reader, which a file can
// never be.
func (d *Driver) consoleSocket() string {
	return filepath.Join(d.work, "console.sock")
}

// handleConsoleWS follows the serial line for a browser.
//
// IT REPLAYS WHAT ALREADY HAPPENED, THEN FOLLOWS. A viewer that joined at second 40 would otherwise
// see the middle of a boot with no beginning, and the first thing a person wants when a machine will
// not come up is the lines from before they opened the page. qemu's socket only carries NEW bytes -
// it is not a ring buffer - so the tail of `console.log` is sent first and the socket is followed
// from there.
func (d *Daemon) handleConsoleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// THE PAGE IS SERVED BY THIS SAME SERVER. The daemon cannot know the name a Service publishes
		// it under, so the origin check is off; the socket only carries the guest's own output.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return // Accept has already written the failure.
	}
	defer conn.Close(websocket.StatusInternalError, "closing")

	// THE DRIVER IS TAKEN FROM THE SLOT AND HELD, not looked up per read: `d.machine` is nil
	// between a shutdown and the next boot, and a console that dereferenced it on every poll would
	// have to answer what a viewer is watching when there is no machine.
	d.mu.Lock()
	slot := d.machine
	d.mu.Unlock()
	if slot == nil || slot.driver == nil {
		_ = conn.Write(r.Context(), websocket.MessageText,
			[]byte("[no machine: start one with `easyvm create` or `easyvm run`]\n"))
		return
	}
	// THE SLOT HOLDS AN INTERFACE; THE CONSOLE AND THE SHELL NEED THE CONCRETE DRIVER, because
	// `run.Driver` is the surface a STEP uses and these two need things a step never does - the
	// serial socket's path and the raw ssh client. The assertion cannot fail for a driver this
	// package built, and the failure is reported rather than panicking.
	drv, ok := slot.driver.(*Driver)
	if !ok {
		_ = conn.Write(r.Context(), websocket.MessageText,
			[]byte("[this machine's driver has no console]\n"))
		return
	}

	ctx := r.Context()

	// THE BACKLOG FIRST, BOUNDED. A 40-minute install writes tens of megabytes and a browser does not
	// want all of it; 64 KiB is a few hundred lines and is what a person scrolls back through.
	if tail := drv.consoleTail(64 << 10); tail != "" {
		if err := conn.Write(ctx, websocket.MessageText, []byte(tail)); err != nil {
			return
		}
	}

	// THEN THE LIVE SOCKET, RETRIED WHILE THE MACHINE IS COMING UP. qemu creates the socket as it
	// starts, so a page opened the instant a machine is asked for would find no path yet.
	var rc net.Conn
	for attempt := 0; attempt < 100; attempt++ {
		if rc, err = net.Dial("unix", drv.consoleSocket()); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
	if rc == nil {
		_ = conn.Write(ctx, websocket.MessageText,
			[]byte("\n[the serial socket never appeared; the log above is all there is]\n"))
		return
	}
	defer rc.Close()

	// A READER GOROUTINE AND THIS WRITER, rather than one loop: a WebSocket write blocks on the
	// browser and the serial socket would fill up behind it, which stalls the guest's own console.
	lines := make(chan []byte, 64)
	go func() {
		defer close(lines)
		buf := make([]byte, 4096)
		for {
			n, rerr := rc.Read(buf)
			if n > 0 {
				b := make([]byte, n)
				copy(b, buf[:n])
				select {
				case lines <- b:
				case <-ctx.Done():
					return
				}
			}
			if rerr != nil {
				if !errors.Is(rerr, io.EOF) && !errors.Is(rerr, os.ErrClosed) {
					// A closed socket is how qemu says the machine stopped; anything else is worth
					// saying out loud in the stream itself.
					select {
					case lines <- []byte("\n[serial: " + rerr.Error() + "]\n"):
					default:
					}
				}
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case b, ok := <-lines:
			if !ok {
				_ = conn.Write(ctx, websocket.MessageText, []byte("\n[the machine stopped]\n"))
				return
			}
			if err := conn.Write(ctx, websocket.MessageBinary, b); err != nil {
				return
			}
		}
	}
}

// consoleTail reads the last n bytes of the serial log.
//
// IT STARTS AT A LINE BOUNDARY when it can: sending half a line makes the first thing a reader sees
// look like a corrupted log rather than a truncated one.
func (d *Driver) consoleTail(n int64) string {
	path := filepath.Join(d.work, "console.log")
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.Size() == 0 {
		return ""
	}
	start := fi.Size() - n
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return ""
	}
	b, err := io.ReadAll(io.LimitReader(f, n))
	if err != nil {
		return ""
	}
	// DROP THE FIRST PARTIAL LINE unless the tail starts the file.
	if start > 0 {
		if i := indexByte(b, '\n'); i >= 0 && i+1 < len(b) {
			b = b[i+1:]
		}
	}
	return string(b)
}

func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}
