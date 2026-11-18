package step

import "testing"

// A KEY'S KIND IS CHECKED, AND THE CHECK KNOWS WHICH SHAPES ARE LEGAL.
//
// BEFORE THIS, `Validate` asked only whether the key existed and `Kind` drove nothing - so
// `click: "not a point"`, `click: [1,2,3]` and `click: [1.5, 2.5]` all passed `easyvm check` and
// reached a guest. The third would have been silently truncated to (1, 2).
func TestEveryKindAcceptsItsShapesAndRefusesTheOthers(t *testing.T) {
	cases := []struct {
		key   string
		ok    []any
		notOK []any
	}{
		{key: "click", ok: []any{[]any{0, 0}, []any{1919, 1079}},
			notOK: []any{"not a point", []any{1, 2, 3}, []any{1.5, 2.5}}},
		{key: "at_button", ok: []any{[]any{0, 0, 10, 10}},
			notOK: []any{[]any{1, 2, 3}}},
		{key: "at_color", ok: []any{[]any{40, 200, 64}},
			notOK: []any{[]any{1, 2}, []any{1, 2, 3, 4}}},
		// STARTING AND STOPPING A RECORDING ARE TWO STATEMENTS OF ONE KEY.
		{key: "record", ok: []any{"install.mp4", false, true},
			notOK: []any{12345, []any{"a"}}},
		// `desktop:` NAMES THE FAMILY, WHICH IS A STRING - the declaration said `mapping` and the
		// executor has always read a string.
		{key: "desktop", ok: []any{"linux", "linux/kde", "macos", "windows"},
			notOK: []any{[]any{"linux"}, map[string]any{"os": "linux"}}},
		{key: "disk", ok: []any{"disk.qcow2", "{{ work }}/disk.qcow2"},
			notOK: []any{[]any{"disk.qcow2"}, map[string]any{"from": "x"}}},
		// A TEMPLATE IS SUBSTITUTED AFTER THIS MAP IS READ, so it is legal here and resolved later.
		{key: "timeout", ok: []any{1800, 300.0, "{{ boot_timeout }}"},
			notOK: []any{"abc", []any{1}}},
		{key: "optional", ok: []any{true, false}, notOK: []any{"true", 1}},
		{key: "needs", ok: []any{[]any{"a.iso"}}, notOK: []any{"a.iso", true}},
	}

	for _, c := range cases {
		key, known := Keys[c.key]
		if !known {
			t.Fatalf("%q is not a key", c.key)
		}
		for _, v := range c.ok {
			if err := checkKind(key, v); err != nil {
				t.Errorf("%s: %#v was refused: %v", c.key, v, err)
			}
		}
		for _, v := range c.notOK {
			if err := checkKind(key, v); err == nil {
				t.Errorf("%s: %#v was ACCEPTED, and it is not one of its shapes", c.key, v)
			}
		}
	}
}

// AND ANY KEY WHOSE SHAPES ARE STRUCTURAL SAYS SO, RATHER THAN BEING SILENTLY UNCHECKED.
//
// `KindAny` is the weakest kind, so the list is pinned: a key added to it without a reason is a key
// nobody vetted. `boot`, `script` and `rect` are the three whose two forms differ in STRUCTURE rather
// than in type - a flag beside a mapping with options, a body beside a body with settings, and a name
// beside two kinds of list.
func TestTheStructuralKeysAreTheOnesWeThinkTheyAre(t *testing.T) {
	want := map[string]bool{"boot": true, "script": true, "rect": true}
	var found []string
	for name, key := range Keys {
		if key.Kind == KindAny {
			found = append(found, name)
		}
	}
	if len(found) != len(want) {
		t.Fatalf("`KindAny` is on %v; it should be on exactly %v - a key whose shapes differ only in "+
			"TYPE belongs on one of the precise kinds", found, want)
	}
	for _, name := range found {
		if !want[name] {
			t.Errorf("%q is `KindAny` and this test does not expect it", name)
		}
	}
}
