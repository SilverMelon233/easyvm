package walk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A RESUME FILE THAT MAKES A DISK IS NOT A RESUME FILE, AND THE COST OF NOT SAYING SO WAS MEASURED.
//
// `create -file <setup.yaml>` runs every `disk:` step before it boots, and `createDisk` DELETES a
// previous file rather than reusing it - deliberately, so a failed run cannot be half-overwritten by
// the next one. The consequence for a RESUME is the opposite of what it is for an install:
//
//	removing the disk a previous run left at /work/disk.qcow2
//	disk.qcow2: 221 MiB
//
// and a 37 GiB macOS recovery, formatted disk and partly-written system were gone. The question "is
// it really using the disk from last time" arrived after the answer had already been destroyed.
//
// SO THE RULE IS MECHANICAL: a file under `steps/` whose name says resume may not contain the step
// that creates a disk. The check is on the YAML rather than on a comment because the next person to
// write one of these will read the file, not this test.
func TestAResumeFileDoesNotCreateItsDisk(t *testing.T) {
	root := filepath.Join("..", "..")
	var checked int
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".yaml") ||
			!strings.Contains(path, string(filepath.Separator)+"steps"+string(filepath.Separator)) {
			return nil
		}
		base := strings.ToLower(filepath.Base(path))
		if !strings.Contains(base, "resume") {
			return nil
		}
		f, loadErr := Load(path, nil)
		if loadErr != nil {
			t.Errorf("%s does not load: %v", path, loadErr)
			return nil
		}
		checked++
		for _, s := range f.Steps {
			if s.Has("disk_gb") || s.Has("disk_gpt") {
				t.Errorf("%s has a step %q that CREATES a disk. A resume file must only `boot:` one "+
					"that exists: `createDisk` deletes the previous file, so this step destroys the "+
					"very disk the file was written to resume - measured, at the cost of a 37 GiB "+
					"macOS install", path, s.Name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		// NOT A VACUOUS PASS. If nobody names a file resume, the rule is untested and this test
		// should say so rather than pass having looked at nothing.
		t.Log("no resume file in this checkout; the rule was not exercised")
	}
}
