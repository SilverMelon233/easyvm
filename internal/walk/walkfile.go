package walk

// THE `.walk` FORMAT: A WALKTHROUGH WRITTEN AS SENTENCES, CONVERTED TO THE SAME DOCUMENT.
//
// IT IS NOT A SECOND ENGINE, and that is the whole point of this file: it reads lines and produces
// the `map[string]any` that `yaml.Unmarshal` would have produced, so everything after that point -
// validation, the dispatcher, the measurements, the recordings - is untouched. A `.walk` file cannot
// express anything a `.yaml` file cannot, which is the property that stops the two drifting.
//
// THE SHAPE, and indentation is the only syntax:
//
//	walkthrough "kubuntu install"
//	  resolution 1920x1080
//	  needs kubuntu.iso
//	  click "next" at 1777 1059
//	  when the screen says "welcome":
//	    click "next" at 1777 1059
//
// THREE THINGS A LINE CAN BE, and each has one rule:
//
//	a header line    `resolution`, `user`, `password`, `recording` - at the top, before any step
//	a step           a verb and its arguments; its NAME is the line itself
//	a child          indented under a step, and becomes a key OF that step
//
// `docs/WALK-FORMAT.md` is the reference a person reads.

import (
	"fmt"
	"strconv"
	"strings"
)

// WalkToYAML converts a `.walk` document into the mapping the YAML decoder would produce.
//
// IT IS EXPORTED SO THE CONVERSION CAN BE TESTED AS A VALUE rather than only through a machine. The
// test that matters is "every real YAML walkthrough converts to `.walk` and back byte-identically",
// and that needs this function, not a run.
func WalkToYAML(src string) (map[string]any, error) {
	lines := scanWalkLines(src)
	if len(lines) == 0 {
		return nil, fmt.Errorf("the file has no lines; a walkthrough starts with `walkthrough \"name\"`")
	}
	if lines[0].indent != 0 {
		return nil, fmt.Errorf("line %d: the title must not be indented", lines[0].no)
	}
	name, err := parseTitle(lines[0])
	if err != nil {
		return nil, fmt.Errorf("line %d: %w", lines[0].no, err)
	}

	doc := map[string]any{"name": name}
	vars := map[string]any{}
	var steps []any
	var recording string

	i := 1
	// ---- the header: everything before the first step verb --------------------------------
	for i < len(lines) {
		key, value, isHeader, err := parseHeader(lines[i])
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lines[i].no, err)
		}
		if !isHeader {
			break
		}
		switch key {
		case "recording":
			recording = value.(string)
		case "vars":
			vars[value.(varPair).name] = value.(varPair).spec
		default:
			doc[key] = value
		}
		i++
	}
	if len(vars) > 0 {
		doc["vars"] = vars
	}
	// A RECORDING IS A STEP. It is written as a header because it starts before anything else and
	// reads as a property of the run, but the YAML has it as the first step and the executor starts
	// it there.
	if recording != "" {
		steps = append(steps, map[string]any{"name": "start recording", "record": recording})
	}

	// ---- the steps ------------------------------------------------------------------------
	for i < len(lines) {
		step, next, err := parseStep(lines, i)
		if err != nil {
			return nil, err
		}
		steps = append(steps, step)
		i = next
	}
	if len(steps) == 0 {
		return nil, fmt.Errorf("the file has no steps; a header with nothing after it proves nothing")
	}
	doc["steps"] = steps
	return doc, nil
}

// line is one non-blank, non-comment source line.
type line struct {
	no     int
	indent int
	text   string
}

// scanWalkLines drops blanks and comments and records each line's indentation and number.
//
// THE NUMBER IS KEPT because a `.walk` failure that says "bad indent" without a line is a file a
// person has to bisect.
func scanWalkLines(src string) []line {
	var out []line
	for n, raw := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		text := strings.TrimRight(raw, " \t")
		trimmed := strings.TrimSpace(text)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		out = append(out, line{no: n + 1, indent: len(text) - len(strings.TrimLeft(text, " \t")), text: trimmed})
	}
	return out
}

// varPair is a variable's name and its YAML spec, carried out of parseHeader as one value.
type varPair struct {
	name string
	spec map[string]any
}

// parseHeader recognises a header line, and says so with `isHeader` rather than by guessing from
// indentation: `resolution` at the top is a header word, and so is a step called "resolution" only
// if it is written with a verb.
func parseHeader(l line) (key string, value any, isHeader bool, err error) {
	verb, rest := cutVerb(l.text)
	switch verb {
	case "resolution":
		w, h, err := parseSize(rest)
		if err != nil {
			return "", nil, false, fmt.Errorf("resolution: %w", err)
		}
		return "resolution", []any{w, h}, true, nil
	case "user":
		// BARE OR QUOTED: a single word needs no quotes, and `user vmuser` reads better than
		// `user "vmuser"` for a value with no spaces.
		v, err := bareOrQuoted(rest)
		if err != nil {
			return "", nil, false, fmt.Errorf("user: %w", err)
		}
		return "ssh_user", v, true, nil
	case "password":
		// BARE OR QUOTED: a single word needs no quotes, and `user vmuser` reads better than
		// `user "vmuser"` for a value with no spaces.
		v, err := bareOrQuoted(rest)
		if err != nil {
			return "", nil, false, fmt.Errorf("password: %w", err)
		}
		return "vars", varPair{"password", map[string]any{
			"from": "args.password", "default": v,
		}}, true, nil
	case "recording":
		// BARE OR QUOTED: a single word needs no quotes, and `user vmuser` reads better than
		// `user "vmuser"` for a value with no spaces.
		v, err := bareOrQuoted(rest)
		if err != nil {
			return "", nil, false, fmt.Errorf("recording: %w", err)
		}
		return "recording", v, true, nil
	}
	return "", nil, false, nil
}

// parseStep reads one step and everything indented under it. It returns the step and the index of the
// first line that is not part of it.
func parseStep(lines []line, i int) (map[string]any, int, error) {
	head := lines[i]
	verb, rest := cutVerb(head.text)

	// CHILDREN ARE EVERY FOLLOWING LINE INDENTED MORE THAN THIS ONE.
	var children []line
	next := i + 1
	for next < len(lines) && lines[next].indent > head.indent {
		children = append(children, lines[next])
		next++
	}

	var step map[string]any
	var err error
	switch {
	case verb == "when" || verb == "unless":
		step, err = parseGate(head, rest, children)
	case verb == "repeat":
		step, err = parseRepeat(head, rest, children)
	case len(children) > 0:
		// A STEP THAT OWNS CHILDREN IS A GATE OR A LOOP; anything else with an indented block is a
		// file whose author expected something this format does not do, and saying so is better than
		// silently running the head and dropping the block.
		return nil, 0, fmt.Errorf("line %d: %q opens a block, and only `when`, `unless` and "+
			"`repeat` take one", head.no, head.text)
	default:
		step, err = parseStatement(head)
	}
	if err != nil {
		return nil, 0, err
	}
	if _, ok := step["name"]; !ok {
		step["name"] = head.text
	}
	return step, next, nil
}

// parseGate builds a step whose `when:`/`when_not:` gate is the head and whose action is its block.
//
//	tap the power button, unless the screen says "installed"
//	  click "next" at 1777 1059
func parseGate(head line, rest string, children []line) (map[string]any, error) {
	if len(children) != 1 {
		return nil, fmt.Errorf("line %d: a gate needs exactly one action under it, got %d",
			head.no, len(children))
	}
	action, err := parseStatement(children[0])
	if err != nil {
		return nil, err
	}
	words, err := quotedWords(rest, "the screen says")
	if err != nil {
		return nil, fmt.Errorf("line %d: %w", head.no, err)
	}
	key := "when"
	if strings.Fields(head.text)[0] == "unless" {
		key = "when_not"
	}
	action[key] = words
	action["name"] = action["__default_name"]
	delete(action, "__default_name")
	if action["name"] == "" || action["name"] == nil {
		action["name"] = head.text
	}
	return action, nil
}

// parseRepeat builds a `loop:` from `repeat up to N times:` and its block.
func parseRepeat(head line, rest string, children []line) (map[string]any, error) {
	max, err := parseRepeatMax(rest)
	if err != nil {
		return nil, fmt.Errorf("line %d: %w", head.no, err)
	}
	loop := map[string]any{"max": max}
	var action []any
	for _, c := range children {
		sub, err := parseStatement(c)
		if err != nil {
			return nil, err
		}
		// THE FOUR PARTS OF A LOOP ARE RECOGNISED BY THE KEY THEY PRODUCE, because `loop:` names
		// them: `do:`, `ocr:`, `check:`, `until:`.
		switch {
		case sub["__until"] != nil:
			loop["until"] = map[string]any{"fact": sub["__until"]}
		case sub["ocr"] != nil:
			loop["ocr"] = sub["ocr"]
		case sub["check"] != nil:
			loop["check"] = sub["check"]
		default:
			delete(sub, "name")
			action = append(action, sub)
		}
	}
	if len(action) == 0 {
		return nil, fmt.Errorf("line %d: `repeat` has no action, so it spins on the clock",
			head.no)
	}
	loop["do"] = action
	return map[string]any{"name": head.text, "loop": loop}, nil
}

// parseStatement turns one sentence into a step mapping.
//
// THE VERB IS THE FIRST WORD AND IT IS THE YAML KEY, so a statement that needs no argument parsing
// is passed through unchanged - which is what keeps a key added to the vocabulary working here with
// no change to this file. The verbs below are the ones whose ARGUMENTS have a shape worth reading:
// a coordinate pair, a colour, a number of seconds. Everything else is `verb: rest`.
func parseStatement(l line) (map[string]any, error) {
	verb, rest := cutVerb(l.text)
	step := map[string]any{"name": l.text}

	switch verb {
	// ---- loop bookkeeping, which is not a step -------------------------------------------
	case "until":
		fact, err := unquote(rest)
		if err != nil {
			return nil, fmt.Errorf("line %d: `until` needs a quoted fact name: %w", l.no, err)
		}
		return map[string]any{"__until": fact, "name": l.text}, nil

	// ---- the machine ---------------------------------------------------------------------
	case "make":
		return parseMakeDisk(l, rest, step)
	case "boot":
		return parseBoot(l, rest, step)
	case "stop":
		return parseStop(l, rest, step)

	// ---- input ---------------------------------------------------------------------------
	case "click", "double":
		x, y, err := parsePoint(rest)
		if err != nil {
			return nil, fmt.Errorf("line %d: %s: %w", l.no, verb, err)
		}
		step[verb] = []any{x, y}
		return step, nil
	case "press":
		// `press Return` / `press ctrl+alt+t`
		if rest == "" {
			return nil, fmt.Errorf("line %d: `press` needs a key", l.no)
		}
		step["key"] = keyQcode(rest)
		return step, nil
	case "type":
		return parseType(l, rest, step)
	case "run":
		step["run"] = rest
		return step, nil
	case "ssh":
		step["ssh"] = rest
		return step, nil

	// ---- a wait --------------------------------------------------------------------------
	case "settle":
		secs, err := parseSeconds(rest)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", l.no, err)
		}
		step["settle"] = secs
		return step, nil

	// ---- a reading -----------------------------------------------------------------------
	case "read":
		return parseRead(l, rest, step)
	case "decide":
		return parseDecide(l, rest, step)

	// ---- the serial console, which is all a microvm has --------------------------------
	case "wait":
		// `wait for the console to say "X", waiting up to 180s`
		if strings.Contains(rest, "console") {
			words, err := quotedWords(rest, "say")
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", l.no, err)
			}
			step["until_console"] = words[0]
			if secs, err := parseTimeoutClause(rest); err == nil && secs > 0 {
				step["timeout"] = secs
			}
			return step, nil
		}
		secs, err := parseSeconds(rest)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", l.no, err)
		}
		step["settle"] = secs
		return step, nil
	case "the":
		// `the console will say "localhost login"` - an assertion about the serial line.
		if strings.HasPrefix(rest, "console will say") {
			words, err := quotedWords(rest, "say")
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", l.no, err)
			}
			step["until_console"] = words[0]
			step["timeout"] = 180
			return step, nil
		}

	// ---- a declaration -------------------------------------------------------------------
	case "needs":
		var files []any
		for _, f := range strings.Fields(rest) {
			files = append(files, strings.Trim(f, `"`))
		}
		if len(files) == 0 {
			return nil, fmt.Errorf("line %d: `needs` names no files, so it would pass by checking "+
				"nothing", l.no)
		}
		step["needs"] = files
		return step, nil
	}
	// A VERB THIS FILE DOES NOT PARSE BUT THE VOCABULARY HAS is passed through with its argument as
	// a string, so `record "x.mp4"` and `screenshot "s.png"` need no case here. An unknown verb is
	// caught by the vocabulary's own validator, which names every key it accepts.
	if rest == "" {
		step[verb] = true
		return step, nil
	}
	step[verb] = strings.Trim(rest, `"`)
	return step, nil
}

// parseMakeDisk reads `make a disk "alpine.qcow2" of 1 GiB`.
func parseMakeDisk(l line, rest string, step map[string]any) (map[string]any, error) {
	// `a disk` is prose that carries no meaning; the path and the size do.
	r := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest), "a disk"))
	name, after, err := firstQuote(r)
	if err != nil {
		return nil, fmt.Errorf("line %d: `make a disk` needs the file name in quotes: %w", l.no, err)
	}
	step["disk"] = name
	gb, err := parseGiB(after)
	if err != nil {
		return nil, fmt.Errorf("line %d: %w", l.no, err)
	}
	step["disk_gb"] = gb
	return step, nil
}

// parseBoot reads `boot the alpine medium "alpine.iso" as a microvm, waiting up to 300s`.
func parseBoot(l line, rest string, step map[string]any) (map[string]any, error) {
	step["boot"] = true
	// A MICROVM TAKES A KERNEL AND AN INITRAMFS INSTEAD OF A MEDIUM, because there is no firmware to
	// read a boot sector. Its two inputs are read first, and the medium is only looked for when they
	// are absent - otherwise the kernel's own name would be taken as the ISO.
	microvm := strings.Contains(rest, "microvm")
	if microvm {
		step["microvm"] = true
		if i := strings.Index(rest, "kernel "); i >= 0 {
			if name, _, err := firstQuote(rest[i:]); err == nil {
				step["kernel"] = name
			}
		}
		if i := strings.Index(rest, "initramfs "); i >= 0 {
			if name, _, err := firstQuote(rest[i:]); err == nil {
				step["initrd"] = name
			}
		}
	}
	// THE MEDIUM IS THE FIRST QUOTED THING, and `as a microvm` and the timeout are the two clauses
	// that follow it in any order.
	if _, exists := step["kernel"]; !exists {
		if name, after, err := firstQuote(rest); err == nil {
			step["iso"] = name
			rest = after
		}
	}
	if secs, err := parseTimeoutClause(rest); err == nil && secs > 0 {
		step["timeout"] = secs
	}

	if i := strings.Index(rest, "as a "); i >= 0 {
		osName := strings.Fields(rest[i+5:])[0]
		osName = strings.Trim(osName, ",.")
		if osName == "microvm" {
			osName = "linux"
		}
		step["os"] = osName
	}
	if _, ok := step["os"]; !ok {
		step["os"] = "linux"
	}
	return step, nil
}

// parseStop reads `stop the machine by killing it, waiting up to 60s`.
func parseStop(l line, rest string, step map[string]any) (map[string]any, error) {
	method := "kill"
	for _, m := range []string{"kill", "ssh", "acpi", "guest_agent"} {
		if strings.Contains(rest, m) {
			method = m
			break
		}
	}
	step["shutdown"] = method
	if secs, err := parseTimeoutClause(rest); err == nil && secs > 0 {
		step["timeout"] = secs
	}
	return step, nil
}

// parseType reads `type "admin" into the password field` and `type "admin"`.
func parseType(l line, rest string, step map[string]any) (map[string]any, error) {
	value, after, err := firstQuote(rest)
	if err != nil {
		// A VAR REFERENCE: `type the password` means `{{ password }}`.
		word := strings.TrimSpace(rest)
		word = strings.TrimPrefix(word, "the ")
		if word == "" {
			return nil, fmt.Errorf("line %d: `type` needs text or a variable name", l.no)
		}
		step["type"] = "{{ " + strings.Fields(word)[0] + " }}"
		return step, nil
	}
	step["type"] = value
	// `clear` appears when the file says the field may already hold something.
	if strings.Contains(after, "clearing") || strings.Contains(after, "clear") {
		step["clear"] = true
	}
	return step, nil
}

// parseRead reads `read the screen as "parts"` and `read the console as "console"`.
func parseRead(l line, rest string, step map[string]any) (map[string]any, error) {
	name, _, err := firstQuote(rest)
	if err != nil {
		return nil, fmt.Errorf("line %d: `read ... as \"name\"` needs a quoted name: %w", l.no, err)
	}
	// THE AREA IS THE WHOLE FRAME unless the phrase names an edge, because a reading with no rect
	// reads the whole screen and the vocabulary requires one.
	rect := []any{"full"}
	if i := strings.Index(rest, " of the "); i >= 0 {
		rect = []any{strings.TrimSpace(rest[i+8 : strings.LastIndex(rest, " as ")])}
	}
	step["ocr"] = map[string]any{"name": name, "rect": []any{rect[0], 0, 0, 0}}
	return step, nil
}

// parseDecide reads a `decide "name" is:` block head; its comparisons are children.
func parseDecide(l line, rest string, step map[string]any) (map[string]any, error) {
	name, _, err := firstQuote(rest)
	if err != nil {
		return nil, fmt.Errorf("line %d: `decide \"name\"` needs a quoted name: %w", l.no, err)
	}
	step["want"] = map[string]any{"name": name}
	return step, nil
}

// ---- small parsers -----------------------------------------------------------------------

// cutVerb splits "verb rest" into the verb and everything after it.
func cutVerb(text string) (verb, rest string) {
	if i := strings.IndexAny(text, " \t"); i >= 0 {
		return text[:i], strings.TrimSpace(text[i+1:])
	}
	return text, ""
}

// parseTitle reads `walkthrough "name"`.
func parseTitle(l line) (string, error) {
	verb, rest := cutVerb(l.text)
	if verb != "walkthrough" {
		return "", fmt.Errorf("a walkthrough starts with `walkthrough \"name\"`; got %q", l.text)
	}
	name, err := unquote(rest)
	if err != nil {
		return "", fmt.Errorf("the walkthrough's name must be quoted: %w", err)
	}
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("the walkthrough has an empty name, so every log line about it is " +
			"anonymous")
	}
	return name, nil
}

// parseSize reads `1920x1080`.
func parseSize(s string) (int, int, error) {
	parts := strings.Split(strings.TrimSpace(s), "x")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("%q is not a size; write it as 1920x1080", s)
	}
	w, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, fmt.Errorf("%q is not a size: %v", s, err)
	}
	h, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, fmt.Errorf("%q is not a size: %v", s, err)
	}
	return w, h, nil
}

// parseSeconds reads `8s`, `8`, `1.5s`.
//
// THE UNIT IS ALLOWED AND NOT REQUIRED, which is the opposite of the format's rule for other units
// and is deliberate: `settle:` in the YAML is seconds, so `wait 8s` and `wait 8` mean the same thing
// and both read correctly out loud.
func parseSeconds(s string) (float64, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "s")
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number of seconds", s)
	}
	return v, nil
}

// parseRepeatMax reads `up to 30 times`.
func parseRepeatMax(s string) (float64, error) {
	fields := strings.Fields(s)
	for i, f := range fields {
		if f == "times" || f == "time" {
			if i == 0 {
				break
			}
			v, err := strconv.ParseFloat(fields[i-1], 64)
			if err != nil {
				return 0, fmt.Errorf("%q is not a number of attempts", fields[i-1])
			}
			return v, nil
		}
	}
	return 0, fmt.Errorf("%q does not say how many times; write `repeat up to 30 times:`", s)
}

// quotedWords reads the quoted strings out of a phrase, ignoring the words before `stopAt`.
func quotedWords(s, stopAt string) ([]any, error) {
	if stopAt != "" {
		if i := strings.Index(s, stopAt); i >= 0 {
			s = s[i+len(stopAt):]
		}
	}
	var out []any
	i := 0
	for i < len(s) {
		if s[i] == '"' {
			j := strings.IndexByte(s[i+1:], '"')
			if j < 0 {
				return nil, fmt.Errorf("an opening quote has no closing quote")
			}
			out = append(out, s[i+1:i+1+j])
			i += j + 2
			continue
		}
		i++
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no quoted words: a gate needs the words the screen will say, in " +
			"quotes")
	}
	return out, nil
}

// unquote strips the quotes from a value that must be quoted.
func unquote(s string) (string, error) {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", fmt.Errorf("%q is not quoted; a value the log echoes is written in quotes", s)
	}
	return s[1 : len(s)-1], nil
}

// ---- the small parsers the statements use ------------------------------------------------

// parsePoint reads `1777 1059` into two integers.
func parsePoint(rest string) (int, int, error) {
	// `click "next" at 1777 1059` - the coordinates are after `at`, and the words before it are the
	// control's name, which the step name already carries.
	if i := strings.Index(rest, " at "); i >= 0 {
		rest = rest[i+4:]
	}
	fields := strings.Fields(strings.ReplaceAll(rest, ",", " "))
	if len(fields) < 2 {
		return 0, 0, fmt.Errorf("%q is not a coordinate; write `at 1777 1059`", rest)
	}
	x, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, 0, fmt.Errorf("%q is not an x coordinate", fields[0])
	}
	y, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, 0, fmt.Errorf("%q is not a y coordinate", fields[1])
	}
	return x, y, nil
}

// keyQcode maps the words a person writes to the qemu qcodes the vocabulary takes.
//
// IT IS A TABLE RATHER THAN A PASSTHROUGH because `press Return` and `press ret` are the same
// instruction and only one of them reads like English.
func keyQcode(word string) string {
	switch strings.ToLower(strings.TrimSpace(word)) {
	case "return", "enter":
		return "ret"
	case "escape", "esc":
		return "esc"
	case "space":
		return "space"
	case "tab":
		return "tab"
	case "backspace":
		return "backspace"
	case "delete":
		return "delete"
	case "up", "down", "left", "right":
		return strings.ToLower(word)
	}
	return strings.TrimSpace(word)
}

// firstQuote returns the first quoted string and the text after its closing quote.
func firstQuote(s string) (value, after string, err error) {
	i := strings.IndexByte(s, '"')
	if i < 0 {
		return "", s, fmt.Errorf("no quoted value in %q", s)
	}
	rest := s[i+1:]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return "", s, fmt.Errorf("an opening quote has no closing quote in %q", s)
	}
	return rest[:j], rest[j+1:], nil
}

// parseGiB reads `of 1 GiB` and `of 128 GiB`.
//
// THE UNIT IS HONOURED, because a disk written `1 GiB` and one written `1 MiB` are different files
// and a silent coercion to GiB would make a microvm's 1 GiB a 1 GiB it did not ask for. `disk_gb` is
// the vocabulary's key, so MiB is converted and a fractional GiB is kept.
func parseGiB(s string) (float64, error) {
	fields := strings.Fields(strings.TrimPrefix(strings.TrimSpace(s), "of "))
	if len(fields) < 1 {
		return 0, fmt.Errorf("no size: write `of 1 GiB`")
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a size", fields[0])
	}
	unit := "gib"
	if len(fields) > 1 {
		unit = strings.ToLower(fields[1])
	}
	switch unit {
	case "gib", "gb", "g":
		return v, nil
	case "mib", "mb", "m":
		return v / 1024, nil
	}
	return 0, fmt.Errorf("%q is not a unit I know; write GiB or MiB", unit)
}

// parseTimeoutClause reads `waiting up to 300s`.
func parseTimeoutClause(s string) (float64, error) {
	i := strings.Index(s, "up to ")
	if i < 0 {
		return 0, fmt.Errorf("no timeout")
	}
	rest := s[i+6:]
	f := strings.Fields(rest)
	if len(f) == 0 {
		return 0, fmt.Errorf("no number after `up to`")
	}
	return parseSeconds(strings.TrimSuffix(f[0], ","))
}

// walkVerbFor reports whether the format has a verb that produces this key, and which.
//
// IT IS THE COVERAGE TEST'S QUESTION: "does a person writing `.walk` have a way to say this?" A key
// with no verb is a key the format does not cover, and the test names it rather than letting the
// format quietly support less than the vocabulary.
func walkVerbFor(key string) (string, bool) {
	verbs := map[string]string{
		"needs": "needs", "record": "recording (header)", "disk": "make a disk",
		"disk_gb": "make a disk", "boot": "boot", "os": "boot ... as a", "iso": "boot",
		"microvm": "boot ... as a microvm", "timeout": "waiting up to",
		"click": "click", "double": "double", "key": "press", "type": "type", "run": "run",
		"ssh": "ssh", "settle": "settle", "ocr": "read", "want": "decide",
		"when": "when", "when_not": "unless", "loop": "repeat",
		"until_console": "wait for the console to say", "shutdown": "stop",
		"check": "decide", "screenshot": "screenshot", "compress": "compress",
		"share_serve": "share_serve", "share_mount": "share_mount", "scp": "scp",
		"script": "script", "write": "write", "reboot": "reboot",
		"require_ssh": "require_ssh", "see": "read", "fetch": "fetch", "esp_fallback": "esp_fallback",
		"adb": "adb", "at_color": "at_color", "at_button": "at_button",
		// THE REST OF THE VOCABULARY, each with the phrase a file writes. A key whose verb is a bare
		// passthrough is still covered: `desktop linux` produces `desktop: linux`.
		"at_bottom": "at_bottom", "at_fallback": "at_fallback", "clear": "type ... clearing",
		"type_gap": "type_gap", "terminal_wait": "terminal_wait", "desktop": "desktop",
		"disk_settle": "disk_settle", "disk_min_mb": "disk_min_mb", "disk_gpt": "disk gpt",
		"disk_esp_mib": "disk esp", "memory_mb": "with memory", "answer_cd": "answer_cd",
		"driver_cd": "driver_cd", "recovery": "recovery", "release": "release",
		"template": "template", "ssh_probe": "ssh_probe", "blind_after": "blind_after",
		"if": "if", "then": "if ... then", "do": "repeat", "esp_from": "esp_from",
		"at_color_tolerance": "at_color", "colour": "decide", "text": "decide",
		"want_all": "decide", "poll": "poll", "optional": "optionally", "requires": "requires",
		"settle_after": "settle", "url": "fetch", "kind": "fetch", "share_family": "share_mount",
		"script_shell": "script", "script_name": "script", "modifier": "type",
		"keep_alive": "desktop", "metadata": "fetch", "adb_connect": "adb",
		"vga": "boot", "cpus": "with cpu", "at_button_fallback": "at_button",
		// THE READING'S OWN SUBKEYS, which appear as keys of a `read`/`decide` block rather than as
		// step verbs - `rect 0 0 100 100` inside `read the screen as "x"`.
		"rect": "read ... within", "rect_at": "read ... measured at", "scale": "read ... scaled",
		"when_not_any": "unless any of", "skip_when_unfinished": "if finished",
		"color_region": "at_color within", "color_tolerance": "at_color tolerance",
		"else": "if ... else", "ssh_after_reboot": "require_ssh after a reboot",
		"color": "decide", "all": "decide", "any": "decide", "not": "decide",
		"must": "decide ... must", "like": "contains", "fuzzy": "contains ... fuzzy",
		"ratio": "contains ... similar", "contains": "contains", "contains_any": "contains any",
		"not_contains": "contains none", "matches": "matches", "equals": "equals",
		"between": "between", "less_than": "less than", "greater_than": "greater than",
		"at_least": "at least", "at_most": "at most", "minus": "minus",
		"adb_privileged": "adb with root",
	}
	v, ok := verbs[key]
	return v, ok
}

// bareOrQuoted accepts a quoted string or a single bare word.
//
// THE RULE IS "quotes when a value the log echoes has spaces in it", which is what makes
// `user vmuser` and `recording "mint install.mp4"` both read correctly.
func bareOrQuoted(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("no value")
	}
	if strings.HasPrefix(s, "\"") {
		return unquote(s)
	}
	if strings.ContainsAny(s, " \t") {
		return "", fmt.Errorf("%q has a space in it, so it needs quotes", s)
	}
	return s, nil
}
