package daemon

// ONE COMMAND IN THE GUEST, WITHOUT A PTY.
//
// `/api/shell` IS FOR PEOPLE and it says so: it requests a pty, which is what makes a terminal work -
// line editing, colours, `top`, an editor. A pty is exactly wrong for a program that wants the output
// of one command, because it echoes every byte that was typed and brackets lines with cursor reports.
//
// MEASURED, trying to write an end-to-end test against `/api/shell`: the echoed command line arrived in
// the stream, the shell's prompt arrived around it, and the marker used to find the end of the output
// was printed by the echo of the very line that was about to print it - so the reader matched before
// the command had run and returned the echo as the answer. Every fix was a guess about how much noise
// to strip, and a test that guesses about output can pass for the wrong reason.
//
// SO THE TEST GETS THE INTERFACE IT NEEDS RATHER THAN A TOLERATED ONE. No pty means the output is the
// command's output - stdout, stderr and an exit status - which is also what anybody automating this
// daemon would ask for, and is why this is a daemon feature rather than a test helper.
//
// THE SESSION IS THE ONE THE DAEMON ALREADY HOLDS: `DoGuest` hands out the connection
// `sshsession.go` caches and repairs, so a command here reuses the link a `require_ssh:` step opened.

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/easydev-org/easyvm/internal/guest"
)

type execRequest struct {
	// Command is passed to the guest's shell, so pipes and redirection work. It is NOT escaped: the
	// caller drives a VM this daemon owns, and quoting it here would make every useful command wrong.
	Command string `json:"command"`
	// TimeoutSeconds bounds the command. Zero means 60.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

type execResponse struct {
	OK bool `json:"ok"`
	// Stdout and Stderr are separate because a test must be able to tell a command that printed its
	// result from one that failed and said why.
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
	// ExitCode is the command's, and -1 when it could not be run at all.
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

func (d *Daemon) handleExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req execRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, execResponse{ExitCode: -1, Error: err.Error()})
		return
	}
	if req.Command == "" {
		writeJSON(w, http.StatusBadRequest, execResponse{ExitCode: -1, Error: "command is empty"})
		return
	}
	timeout := req.TimeoutSeconds
	if timeout <= 0 {
		timeout = 60
	}

	d.mu.Lock()
	slot := d.machine
	d.mu.Unlock()
	if slot == nil || slot.driver == nil {
		writeJSON(w, http.StatusConflict, execResponse{ExitCode: -1,
			Error: "no machine is running; start one with `easyvm create` or `easyvm run`"})
		return
	}
	drv, ok := slot.driver.(*Driver)
	if !ok {
		writeJSON(w, http.StatusConflict, execResponse{ExitCode: -1,
			Error: "this machine's driver has no guest channel"})
		return
	}

	var res guest.Result
	err := drv.DoGuest(func(c *guest.Client) error {
		var runErr error
		// `Run` RATHER THAN `Output`: `Output` folds stderr into stdout on a non-zero status, which is
		// right for a log and wrong for a test that wants to see which stream said what.
		res, runErr = c.Run(req.Command, time.Duration(timeout)*time.Second)
		return runErr
	})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, execResponse{ExitCode: -1, Error: err.Error()})
		return
	}
	// A NON-ZERO EXIT IS A SUCCESSFUL REQUEST. The command ran and said how it went; `ok` describes the
	// HTTP exchange, `exit_code` describes the command - folding them together would make a test unable
	// to distinguish "the rule blocked it" from "the daemon could not ask".
	writeJSON(w, http.StatusOK, execResponse{
		OK: true, Stdout: res.Stdout, Stderr: res.Stderr, ExitCode: res.Status,
	})
}
