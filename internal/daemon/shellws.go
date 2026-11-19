package daemon

import (
	"encoding/json"
	"net/http"

	"github.com/coder/websocket"
	"golang.org/x/crypto/ssh"

	"github.com/easydev-org/easyvm/internal/guest"
)

// handleShellWS is a SHELL IN THE GUEST, over SSH, with a real PTY.
//
// WHY SSH AND NOT A LOCAL PTY. The convention for "a terminal in the browser" is a daemon that
// `forkpty()`s a shell - ttyd and gotty both do exactly that. It is not available here and the
// reason is the machine model: easyvm drives a GUEST VM, which is a separate kernel, so there is no
// process to fork. `kubectl exec` has the same problem and solves it by asking the runtime to call
// execve inside the container's namespaces; a VM has no such door, and the one it does have is
// sshd.
//
// THE PTY IS REQUESTED FROM THE SERVER, NOT FAKED. `RequestPty` is the SSH channel request that
// makes sshd allocate a pty(4) for the session, which is what gives a real prompt, job control,
// line editing, colours and `Ctrl-C`. A session without it runs a shell with no tty and every
// interactive program degrades: no prompt, no readline, `vim` refuses to start.
//
// THE PROTOCOL IS NEWLINE-DELIMITED JSON IN BOTH DIRECTIONS, and that is chosen for the page rather
// than for purity: `internal/daemon/ui/app.js` is hand-written with no framework and no build step,
// so `JSON.stringify({line}) + "\n"` is the whole client.
type shellMessage struct {
	// Line is one command. The daemon writes it to the pty and the shell echoes and runs it.
	Line string `json:"line,omitempty"`
	// Cols and Rows are the terminal size, sent whenever the browser resizes.
	Cols int `json:"cols,omitempty"`
	Rows int `json:"rows,omitempty"`
}

func (d *Daemon) handleShellWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusInternalError, "closing")

	d.mu.Lock()
	slot := d.machine
	d.mu.Unlock()
	if slot == nil || slot.driver == nil {
		_ = conn.Write(r.Context(), websocket.MessageText,
			[]byte("\r\n[no machine: start one with `easyvm create` or `easyvm run`]\r\n"))
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
	write := func(s string) bool {
		return conn.Write(ctx, websocket.MessageText, []byte(s)) == nil
	}

	// THE SSH SESSION IS THE ONE THE DAEMON ALREADY HOLDS. It is cached and repaired by
	// `sshsession.go`, so a shell opened after a `require_ssh:` step reuses the connection rather
	// than dialling again - and a connection that died is redialled by the same code path.
	var raw *ssh.Client
	err = drv.DoGuest(func(c *guest.Client) error {
		raw = c.Raw()
		return nil
	})
	if err != nil || raw == nil {
		write("\r\n[cannot reach the guest over ssh: " + errText(err) + "]\r\n" +
			"[the guest must be running and sshd must answer; `/api/state` says which]\r\n")
		return
	}

	sess, err := raw.NewSession()
	if err != nil {
		write("\r\n[ssh refused a session: " + err.Error() + "]\r\n")
		return
	}
	defer sess.Close()

	// 120x32 IS A STARTING SIZE, NOT A DEFAULT TO KEEP: the page sends its own on open and on every
	// resize, and a pty stuck at the wrong width wraps every line.
	if err := sess.RequestPty("xterm-256color", 32, 120, ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 38400,
		ssh.TTY_OP_OSPEED: 38400,
	}); err != nil {
		write("\r\n[the guest refused a pty: " + err.Error() + "]\r\n")
		return
	}

	stdin, err := sess.StdinPipe()
	if err != nil {
		write("\r\n[no stdin: " + err.Error() + "]\r\n")
		return
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		write("\r\n[no stdout: " + err.Error() + "]\r\n")
		return
	}
	sess.Stderr = sess.Stdout // one stream: a pty already merges them, and two would interleave badly.

	if err := sess.Shell(); err != nil {
		write("\r\n[no shell: " + err.Error() + "]\r\n")
		return
	}

	// GUEST -> BROWSER.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, rerr := stdout.Read(buf)
			if n > 0 {
				if conn.Write(ctx, websocket.MessageBinary, buf[:n]) != nil {
					return
				}
			}
			if rerr != nil {
				_ = conn.Write(ctx, websocket.MessageText, []byte("\r\n[session ended]\r\n"))
				return
			}
		}
	}()

	// BROWSER -> GUEST.
	for {
		typ, data, rerr := conn.Read(ctx)
		if rerr != nil {
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		var m shellMessage
		if err := json.Unmarshal(data, &m); err != nil {
			// NOT JSON IS TYPED TEXT. A person pressing a key one at a time in a raw terminal would
			// send single characters; treating the payload as a literal keystroke is the behaviour
			// that makes both shapes work from one socket.
			if _, werr := stdin.Write(data); werr != nil {
				return
			}
			continue
		}
		if m.Cols > 0 && m.Rows > 0 {
			_ = sess.WindowChange(m.Rows, m.Cols)
			continue
		}
		if m.Line != "" {
			if _, werr := stdin.Write([]byte(m.Line + "\n")); werr != nil {
				return
			}
		}
	}
}

// errText renders a nil error as a sentence rather than the word "<nil>".
func errText(err error) string {
	if err == nil {
		return "no ssh session (a `require_ssh:` step has not run)"
	}
	return err.Error()
}
