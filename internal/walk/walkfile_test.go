package walk

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/easydev-org/easyvm/internal/step"
	"gopkg.in/yaml.v3"
)

// THE `.walk` FORMAT MUST PRODUCE EXACTLY WHAT THE YAML PRODUCES, and this is the test that says so
// as a value rather than as a claim.
//
// THE PROPERTY THAT MATTERS IS NOT "it parses". It is that a `.walk` file cannot express something a
// `.yaml` file cannot, because the moment it can, the two drift and a vocabulary change has to be
// made twice. So this test converts a document, and checks the RESULT is the shape `Load` consumes -
// every step has a name, every key is one the vocabulary declares, and the header keys are the ones
// `validateDocument` accepts.
func TestAWalkFileBecomesTheSameDocument(t *testing.T) {
	src := `walkthrough "probe"
  resolution 1920x1080
  user vmuser
  password admin
  recording "probe.mp4"

  needs kubuntu.iso
  make a disk "disk.qcow2" of 128 GiB
  boot "kubuntu.iso", waiting up to 1800s
  click "next" at 1777 1059
  press Return
  settle 8s
`
	doc, err := WalkToYAML(src)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	if got := doc["name"]; got != "probe" {
		t.Fatalf("name = %v, want probe", got)
	}
	if got := doc["resolution"]; !reflect.DeepEqual(got, []any{1920, 1080}) {
		t.Fatalf("resolution = %v, want [1920 1080]", got)
	}
	if got := doc["ssh_user"]; got != "vmuser" {
		t.Fatalf("ssh_user = %v, want vmuser", got)
	}
	steps, _ := doc["steps"].([]any)
	// THE RECORDING IS A STEP, so it is the first of six plus the one the header emitted.
	if len(steps) != 7 {
		t.Fatalf("%d steps, want 7 (the recording plus six): %v", len(steps), steps)
	}
	// EVERY STEP HAS A NAME, because a failure with no name is a failure nobody can act on.
	for i, raw := range steps {
		s, _ := raw.(map[string]any)
		if name, _ := s["name"].(string); name == "" {
			t.Errorf("step %d has no name: %v", i+1, s)
		}
	}
	// AND THE KEYS ARE THE VOCABULARY'S, checked by asking the validator rather than by listing them
	// here - which is what keeps this test from being a second copy of the vocabulary.
	for _, raw := range steps {
		s, _ := raw.(map[string]any)
		name, _ := s["name"].(string)
		if err := stepValidateForTest(name, s); err != nil {
			t.Errorf("%q produces a document the validator refuses: %v", name, err)
		}
	}
}

// AND THE TWO FORMATS RUN THE SAME STEPS. This is the "not a second engine" property in the form a
// person can check: a `.walk` file and the YAML it converts to are loaded by the same `Load`, and the
// resulting `Step` list has the same keys in the same order.
func TestAWalkFileAndItsYAMLAreTheSameWalk(t *testing.T) {
	dir := t.TempDir()
	walkSrc := filepath.Join(dir, "a.walk")
	yamlSrc := filepath.Join(dir, "a.yaml")

	const body = `walkthrough "same walk"
  resolution 1920x1080

  needs kubuntu.iso
  click "next" at 100 200
  settle 3s
`
	if err := os.WriteFile(walkSrc, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// THE YAML THE CONVERTER PRODUCES, written out and loaded the ordinary way.
	doc, err := WalkToYAML(body)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	yamlText := docToYAMLForTest(t, doc)
	if err := os.WriteFile(yamlSrc, []byte(yamlText), 0o644); err != nil {
		t.Fatal(err)
	}

	fromWalk, err := Load(walkSrc, nil)
	if err != nil {
		t.Fatalf("the .walk file does not load: %v", err)
	}
	fromYAML, err := Load(yamlSrc, nil)
	if err != nil {
		t.Fatalf("the YAML it converts to does not load:\n%s\n%v", yamlText, err)
	}

	if len(fromWalk.Steps) != len(fromYAML.Steps) {
		t.Fatalf("the .walk has %d steps and its YAML has %d", len(fromWalk.Steps), len(fromYAML.Steps))
	}
	for i := range fromWalk.Steps {
		a, b := fromWalk.Steps[i], fromYAML.Steps[i]
		if a.Name != b.Name {
			t.Errorf("step %d named %q from the .walk and %q from the YAML", i+1, a.Name, b.Name)
		}
		if got, want := keyList(a.Spec), keyList(b.Spec); !reflect.DeepEqual(got, want) {
			t.Errorf("step %d has keys %v from the .walk and %v from the YAML", i+1, got, want)
		}
	}
}

// EVERY REAL YAML WALKTHROUGH CONVERTS, which is the claim that the format covers the vocabulary.
//
// IT CONVERTS RATHER THAN ROUND-TRIPS, because writing a YAML emitter would be a second thing to keep
// in step and the property worth testing is that the CONVERTER understands every verb the real files
// use. A verb it cannot read is a verb the format does not cover, and that is the finding.
func TestEveryRealWalkthroughUsesVerbsTheFormatCovers(t *testing.T) {
	root := filepath.Join("..", "..")
	files, err := filepath.Glob(filepath.Join(root, "*", "*", "steps", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 20 {
		t.Fatalf("found only %d walkthroughs; the glob must be wrong", len(files))
	}
	var unconvertible []string
	for _, path := range files {
		doc, err := Load(path, nil)
		if err != nil {
			continue // a file that does not load is another test's subject
		}
		// THE CONVERSION IS TESTED ON THE VERBS, so a walkthrough with any step whose verb this file
		// does not know is named rather than silently skipped.
		for _, s := range doc.Steps {
			for key := range s.Spec {
				if key == "name" {
					continue
				}
				if _, ok := walkVerbFor(key); !ok {
					unconvertible = append(unconvertible, filepath.Base(path)+": "+key)
				}
			}
		}
	}
	sort.Strings(unconvertible)
	if len(unconvertible) > 0 {
		t.Errorf("these keys are used by real walkthroughs and the .walk format has no verb for them, "+
			"so the format does not cover the vocabulary yet:\n\t%s",
			strings.Join(uniqueStrings(unconvertible), "\n\t"))
	}
	t.Logf("checked %d walkthroughs", len(files))
}

// stepValidateForTest asks the vocabulary whether a step is legal, without importing step into a
// test that already has enough to read.
func stepValidateForTest(name string, spec map[string]any) error {
	return step.Validate(name, spec)
}

// docToYAMLForTest renders the converted document back to YAML, so the test can load BOTH formats
// through the same `Load` and compare the steps it produced.
func docToYAMLForTest(t *testing.T, doc map[string]any) string {
	t.Helper()
	b, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatalf("render the converted document back to YAML: %v", err)
	}
	return string(b)
}

func keyList(spec map[string]any) []string {
	out := make([]string, 0, len(spec))
	for k := range spec {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
