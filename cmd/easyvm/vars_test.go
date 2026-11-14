package main

import (
	"reflect"
	"testing"
)

// A FLAG OF THE COMMAND AND A VARIABLE OF THE FILE ARE THE SAME SHAPE ON THE COMMAND LINE, and
// getting that wrong is not a cosmetic bug: `fs.Parse` refuses the variable with
//
//	flag provided but not defined: -shutdown
//
// and exits 2 before a single step runs. That was MEASURED on this build - the help text advertised
// `--name=value` while every use of it was a usage error - so the split is worth a test of its own
// rather than a comment claiming it works.
func TestAVariablesFlagIsNotTheCommandsFlag(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		flags []string
		vars  map[string]string
	}{
		{
			name:  "a variable is lifted out and the file stays a flag argument",
			args:  []string{"f.yaml", "--work", "/tmp/x", "--shutdown=kill"},
			flags: []string{"f.yaml", "--work", "/tmp/x"},
			vars:  map[string]string{"shutdown": "kill"},
		},
		{
			name:  "the equals form of a declared flag stays a flag",
			args:  []string{"f.yaml", "--work=/tmp/x", "--out=/tmp/o.qcow2"},
			flags: []string{"f.yaml", "--work=/tmp/x", "--out=/tmp/o.qcow2"},
			vars:  map[string]string{},
		},
		{
			name:  "a url with equals signs in its value survives whole",
			args:  []string{"f.yaml", "--mirror=https://m.example/FreeBSD:15:amd64/quarterly"},
			flags: []string{"f.yaml"},
			vars:  map[string]string{"mirror": "https://m.example/FreeBSD:15:amd64/quarterly"},
		},
		{
			name:  "several variables, one of them empty",
			args:  []string{"f.yaml", "--mirror=", "--shutdown=ssh"},
			flags: []string{"f.yaml"},
			vars:  map[string]string{"mirror": "", "shutdown": "ssh"},
		},
		{
			// A BARE NAME HAS NO VALUE, SO IT IS NOT A VARIABLE. Leaving it to `fs.Parse` is what
			// keeps a typo loud instead of silently doing nothing.
			name:  "a bare name is left for the flag package to refuse",
			args:  []string{"f.yaml", "--shutdown"},
			flags: []string{"f.yaml", "--shutdown"},
			vars:  map[string]string{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			flags, vars := splitVars(c.args)
			if !reflect.DeepEqual(flags, c.flags) {
				t.Fatalf("flags = %q, want %q", flags, c.flags)
			}
			if !reflect.DeepEqual(vars, c.vars) {
				t.Fatalf("vars = %v, want %v", vars, c.vars)
			}
		})
	}
}
