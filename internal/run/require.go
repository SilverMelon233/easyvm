package run

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/easydev-org/easyvm/internal/walk"
)

// doRequire refuses the walk unless every file it names is present, at a plausible size, in the work
// directory.
//
// WHY A STEP AND NOT A COMMENT. Every medium a walkthrough boots from is now pulled by the workflow
// BEFORE `easyvm run` is called - the installer ISOs, the macOS recovery image, OpenCore's three
// firmware files, the Xcode .xip, the dev package tarballs. That moved an 18-minute download out of
// the middle of a stage (measured on run 43847000071, where it ate the whole `wait for sshd` budget
// and reported a machine that never answered), and it also moved the DIAGNOSIS: a file that is
// missing now fails at the first `boot:` with
//
//	boot: no the installer medium at /work/ubuntu.iso: stat: no such file or directory
//
// which names the boot step and not the pull. This step is the one that names the pull.
//
// IT IS THE FIRST STEP OF EVERY WALKTHROUGH, AND IT FAILS FAST. A stage that discovers a missing ISO
// after ten minutes of clicking a live desktop has spent ten minutes to learn something available in
// the first second.
//
// THE SIZE FLOOR IS NOT DECORATION. `pull-artifact.sh` writes to `<dest>.part` and renames when the
// bytes are in, so a truncated file should not survive - but a `boot.img` that is 256 MiB of zeros
// and a `.xip` that is an HTML error page saved under the right name both look present to `-e`. The
// floors are deliberately far below the real sizes (200 MiB for a boot image that is 256) because the
// point is to catch a stub, not to police a republished artifact.
//
// THE KEYS ARE `require:` (a list) AND EVERY ENTRY IS `name` OR `name:min_mib`. A bare name means
// "present and not empty", which is what a 128 KiB NVRAM file needs.
func (e *Executor) doRequire(s walk.Step) error {
	raw, ok := s.Spec["require"]
	if !ok {
		return fail(s.Name, "`require:` needs a list of files")
	}
	items, ok := raw.([]any)
	if !ok {
		return fail(s.Name, "`require:` must be a list, not %T", raw)
	}
	if len(items) == 0 {
		return fail(s.Name, "`require:` names no files, so it would pass by checking nothing")
	}

	var missing, bad []string
	for _, item := range items {
		entry, _ := item.(string)
		entry = strings.TrimSpace(entry)
		if entry == "" {
			missing = append(missing, "(an empty entry)")
			continue
		}

		name := entry
		var floorMiB int64
		if i := strings.IndexByte(entry, ':'); i >= 0 {
			name = strings.TrimSpace(entry[:i])
			fmt.Sscanf(strings.TrimSpace(entry[i+1:]), "%d", &floorMiB)
		}

		path := name
		if !filepath.IsAbs(path) {
			path = filepath.Join(e.Work, path)
		}
		fi, err := os.Stat(path)
		if err != nil {
			missing = append(missing, name)
			continue
		}
		if fi.IsDir() {
			bad = append(bad, fmt.Sprintf("%s is a directory", name))
			continue
		}
		if floorMiB > 0 && fi.Size() < floorMiB<<20 {
			bad = append(bad, fmt.Sprintf("%s is %d bytes, below its %d MiB floor",
				name, fi.Size(), floorMiB))
		}
		e.Driver.Log(fmt.Sprintf("  required: %s (%d MiB)", name, fi.Size()>>20))
	}

	if len(missing) > 0 || len(bad) > 0 {
		var problems []string
		for _, m := range missing {
			problems = append(problems, m+" is not in "+e.Work)
		}
		problems = append(problems, bad...)
		// THE SENTENCE NAMES WHAT TO DO RATHER THAN WHAT HAPPENED, because the answer is always the
		// same one: the workflow's `assets:` list is what puts these files here, and a leg that
		// reaches this message has an `assets:` entry missing or a pull that failed.
		return fail(s.Name, "%s. Every medium a walk boots from is pulled BEFORE this process starts "+
			"- a leg's `assets:` in its workflow is what declares them, and `pull-artifact.sh` is what "+
			"places them. A missing file here means that pull did not run or did not finish, and the "+
			"run was stopped at its first step for that reason rather than forty minutes in",
			strings.Join(problems, "; "))
	}
	return nil
}
