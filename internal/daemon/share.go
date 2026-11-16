package daemon

// --- the host share -------------------------------------------------------------------
//
// A `share_serve:` step names a directory on the host, and the daemon serves it to the guest over
// HTTP at `/shared/` - on the same listener as `/api/state`, `/screen.png` and the console.
//
// IT WAS SMB, AND SMB COST EVERY GUEST A DIFFERENT CLIENT. Linux needed `cifs-utils`, macOS used
// Apple's own mount_smbfs, Windows mapped it with `net use`, and FreeBSD COULD NOT CONNECT AT ALL:
// netsmb speaks SMB1 on NetBIOS 139 while smbd serves SMB3 on 445, so the kernel got a TCP RST that
// the library reported as `syserr = Connection refused`. Making that one guest work took a ports
// package, a base filesystem, a root-only mount option and a mount table that names the bind.
//
// See `sharehttp.go` for the transport and the measurement. What lives here is only the fact that a
// run has ONE shared directory, and the proof that a guest can read from it.

import (
	"fmt"
	"os"
)

// ServeShare records the directory this run serves, and starts nothing.
//
// IT USED TO START smbd ON THE BRIDGE AND WAIT FOR 445. That is gone with the SMB transport: the
// directory is now served over the daemon's own HTTP listener at `/shared/`, which needs no process
// of its own, no port of its own and no configuration file. What remains is the fact every other
// part of this package asks for - WHICH DIRECTORY IS SHARED - because the proof that a guest can
// read the host's file still has to look on the host side.
//
// A SECOND CALL FOR A DIFFERENT DIRECTORY IS STILL AN ERROR: one run has one share, and a step that
// silently repointed it would leave a guest fetching from a directory nobody meant to publish.
func (d *Driver) ServeShare(dir string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.shareDir != "" && d.shareDir != dir {
		return fmt.Errorf("this run is already serving %s; one guest has one share, so a second "+
			"`share_serve:` must name the same directory or run in a new stage", d.shareDir)
	}
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return fmt.Errorf("creating the shared directory %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		return fmt.Errorf("making %s writable: %w", dir, err)
	}
	d.shareDir = dir
	d.logf(fmt.Sprintf("  share: %s is served at %s", dir, shareURL(0)))
	return nil
}

func (d *Driver) ShareDir() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.shareDir
}

// ShareURL is where a guest fetches the shared directory, or "" when nothing is shared.
//
// IT IS A METHOD RATHER THAN A CONSTANT because the port is the daemon's own, and a test that
// listens elsewhere has to get the URL that matches.
func (d *Driver) ShareURL() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.shareDir == "" {
		return ""
	}
	return shareURL(0)
}
