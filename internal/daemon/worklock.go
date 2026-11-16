package daemon

// THE WORK DIRECTORY IS HELD BY THE KERNEL, NOT BY A PROMISE.
//
// A `--work` directory is not a scratch space: inside it are the qemu qmp socket, the tap name, the
// overlay, the frames and the disk this stage is publishing. Every one of those is a SINGLETON, and
// `internal/qmp.Registry` enforces that - but only INSIDE one process. Two `easyvm` processes pointed
// at the same directory agree on nothing, and the damage is silent:
//
//	MEASURED, on this repository's own lab pod, after an operator started a second machine without
//	destroying the first. The daemon refused the second machine correctly -
//
//		REFUSED: a machine is already running on this daemon: a `create` sandbox, up 3m49s
//
//	- and then, once the first process was killed without a `destroy`, the next boot reached
//
//		_ = os.Remove(qmpPath)
//
//	intact: it deleted the socket file of a qemu that was STILL RUNNING. That guest became
//	undrivable and unrepoatable, and the symptom was not an error at the point of damage:
//
//		could not read the screen: qmp /work/drive.sock.qmp: connect: no such file or directory
//		and `/api/click` answered {"ok":true} while the pointer on the screen never moved.
//
// WHY flock AND NOT A PIDFILE. A pidfile is a CLAIM: it records that some process intended to own the
// directory, and it cannot tell whether that process still exists. A killed daemon leaves one behind
// and every later start has to guess. A kernel `flock` is a FACT: the lock is released when the holder
// dies, for any reason, including SIGKILL, and the file left behind is harmless.
//
// WHY THIS IS THE OUTERMOST LAYER. `machineOwner` refuses a second machine in one daemon, and
// `qmp.Registry` refuses a second client on one socket. Both are correct and both are narrower than
// the resource they protect: the work directory outlives the process. This lock is what makes the
// other two sufficient instead of merely local.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// WorkLock is a held claim on a work directory. Release it when the daemon stops.
type WorkLock struct {
	path string
	file *os.File
}

// LockWork takes the exclusive claim on dir, or returns an error naming who holds it.
//
// IT DOES NOT WAIT. A second daemon on the same directory is a mistake, not a queue: the caller asked
// for a machine that cannot exist, and the useful answer is the pid of the process in the way rather
// than a block that looks like a slow start.
func LockWork(dir string) (*WorkLock, error) {
	path := filepath.Join(dir, "easyvm.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening the work lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		holder := readLockHolder(f)
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			who := ""
			if holder != "" {
				who = " (pid " + holder + ")"
			}
			return nil, fmt.Errorf("another easyvm is already using %s%s; one work directory holds "+
				"one machine. Stop it, or pass a different --work", dir, who)
		}
		return nil, fmt.Errorf("locking %s: %w", path, err)
	}
	// THE PID IS FOR THE ERROR MESSAGE, NOT FOR THE LOCK. The kernel already decided; this only makes
	// the refusal name somebody a person can look at.
	_ = f.Truncate(0)
	if _, err := f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0); err != nil {
		// A pid that cannot be written is not a reason to refuse a lock that is already held.
		_ = err
	}
	return &WorkLock{path: path, file: f}, nil
}

// Release gives the directory back. It is safe to call twice and safe on a nil lock.
func (l *WorkLock) Release() {
	if l == nil || l.file == nil {
		return
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
	l.file = nil
}

// Path is the lock file, for a log line.
func (l *WorkLock) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// readLockHolder returns the pid the current holder wrote, when it wrote one.
//
// IT IS READ BECAUSE THE REFUSAL IS ONLY USEFUL IF IT NAMES SOMEBODY. The lock does not depend on it:
// a lock file from a process that died is locked by nobody, and `flock` above is the fact that decides.
func readLockHolder(f *os.File) string {
	buf := make([]byte, 32)
	n, err := f.ReadAt(buf, 0)
	if err != nil && n == 0 {
		return ""
	}
	return strings.TrimSpace(string(buf[:n]))
}
