package run

import (
	"fmt"
	"image"
	"regexp"
	"strings"

	"github.com/easydev-org/easyvm/internal/screen"
	"github.com/easydev-org/easyvm/internal/walk"
)

// `check:` CAPTURES THE SCREEN ONCE AND ASKS ANY NUMBER OF QUESTIONS ABOUT THAT ONE FRAME.
//
// THE SINGLE CAPTURE IS A CORRECTNESS REQUIREMENT, NOT AN OPTIMISATION. `Driver.Frame()` takes a real
// screendump on every call - `internal/daemon/driver.go` locks, calls `Screendump`, and returns a NEW
// file, nothing is cached - so a file that writes
//
//	- see: {name: bottom, rect: bottom 4.5%, ...}
//	- see: {name: body,   rect: middle, ...}
//	- want: {name: up, ...}
//
// measures THREE DIFFERENT MOMENTS. On a pane that is still repainting, `body` can be read while the
// wizard fades and `bottom` after it settles, and the two numbers then describe no frame that ever
// existed. Every criterion below is evaluated against ONE frame for that reason.
//
//	- check:
//	    name: page                    # REQUIRED: the boolean this step binds
//	    region: middle                # the default rect for text criteria that name none
//	    text:                         # OCR criteria - any number, all on this frame
//	      - contains: [erase, disk]
//	      - like: "install now"
//	        fuzzy: 0.85
//	    colour:                       # pixel criteria - any number, each may name its own rect
//	      - rect: bottom 4.5%
//	        mean:      {between: [20, 60]}
//	        spread:    {less_than: 42}
//	        flat_rows: {at_least: 3}
//	        vs: {rect: middle, mean: {greater_than: 40}}
//	    any_of:                       # ONE of these groups must pass
//	      - colour: [{rect: top 4.5%, flat_rows: {at_least: 3}}]
//	    unless: [welcome, setup]      # every word here must be ABSENT
//
// IT NEVER CLICKS. A step that captures, decides AND clicks cannot say which of the three failed, and
// this repository has already paid for that lesson: a `until:` inside a click step reported "did not
// advance" when the click had missed. A click is the `do:` beside it, gated by the fact this binds.

// frame is one capture, shared by every criterion of one `check:` step.
type frame struct {
	img image.Image
	w   int
	h   int
	// words caches each rect's OCR, because two criteria may read the same region and a capture
	// that is read twice should not be OCR'd twice.
	words map[string][]string
	ex    *Executor
}

// checkFrame runs the merged `check:`.
func (e *Executor) checkFrame(s walk.Step, raw map[string]any) error {
	name, _ := raw["name"].(string)
	if name == "" {
		return fail(s.Name, "`check:` must say `name:` - the boolean it binds, which a `loop:`, "+
			"`until:` or `if:` tests. A decision nobody named cannot be tested")
	}
	// AT LEAST ONE CRITERION, because a `check:` with none is a step that always passes.
	_, hasText := raw["text"]
	_, hasColour := raw["colour"]
	_, hasAnyOf := raw["any_of"]
	_, hasUnless := raw["unless"]
	if !hasText && !hasColour && !hasAnyOf && !hasUnless {
		return fail(s.Name, "`check:` has no criteria; it must say `text:`, `colour:`, "+
			"`unless:` or `any_of:` - or it decides nothing and always passes")
	}

	f, err := e.Driver.Frame()
	if err != nil {
		return fail(s.Name, "capturing the frame: %v", err)
	}
	img, err := screen.Load(f.Path())
	if err != nil {
		return fail(s.Name, "loading the frame: %v", err)
	}
	fw, fh := f.Size()
	fr := &frame{img: img, w: fw, h: fh, words: map[string][]string{}, ex: e}

	defaultRect := "middle"
	if r, ok := raw["region"].(string); ok && r != "" {
		defaultRect = r
	}

	var passes []bool
	var notes []string

	if list, ok := raw["text"].([]any); ok {
		for i, entry := range list {
			okNow, note, err := fr.text(defaultRect, entry)
			if err != nil {
				return fail(s.Name, "text criterion %d: %v", i+1, err)
			}
			passes = append(passes, okNow)
			notes = append(notes, note)
		}
	}

	if list, ok := raw["colour"].([]any); ok {
		for i, entry := range list {
			okNow, note, err := fr.colour(entry)
			if err != nil {
				return fail(s.Name, "colour criterion %d: %v", i+1, err)
			}
			passes = append(passes, okNow)
			notes = append(notes, note)
		}
	}

	if words := stringList(raw["unless"]); len(words) > 0 {
		read, err := fr.read(defaultRect)
		if err != nil {
			return fail(s.Name, "reading for `unless:`: %v", err)
		}
		var found []string
		for _, w := range words {
			if hasWord(read, w) {
				found = append(found, w)
			}
		}
		absent := len(found) == 0
		passes = append(passes, absent)
		notes = append(notes, fmt.Sprintf("unless[%s]=%v", strings.Join(words, ","), absent))
	}

	anyOK := true
	if groups, ok := raw["any_of"].([]any); ok && len(groups) > 0 {
		anyOK = false
		for i, group := range groups {
			okNow, note, err := fr.group(defaultRect, group)
			if err != nil {
				return fail(s.Name, "any_of group %d: %v", i+1, err)
			}
			if okNow {
				anyOK = true
			}
			notes = append(notes, fmt.Sprintf("any_of[%d]=%v(%s)", i+1, okNow, note))
		}
	}

	// EVERY CRITERION MUST PASS, EXCEPT THE `any_of:` GROUP, WHICH NEEDS ONE INSIDE IT.
	result := anyOK
	for _, p := range passes {
		if !p {
			result = false
		}
	}

	if e.facts == nil {
		e.facts = map[string]bool{}
	}
	e.facts[name] = result
	e.Driver.Log(fmt.Sprintf("  %s = %v (%s)", name, result, strings.Join(notes, "; ")))
	return nil
}

// text evaluates ONE `text:` criterion.
func (fr *frame) text(defaultRect string, entry any) (bool, string, error) {
	m, ok := entry.(map[string]any)
	if !ok {
		return false, "", fmt.Errorf("a text criterion is a mapping, like `{contains: [erase, disk]}`")
	}
	rect := defaultRect
	if r, ok := m["rect"].(string); ok && r != "" {
		rect = r
	}
	read, err := fr.read(rect)
	if err != nil {
		return false, "", err
	}
	return textOnWords(read, m)
}

// textOnWords is `text`'s comparison half, taking the words instead of fetching them.
//
// IT IS SPLIT OUT SO `want:` CAN USE IT: a `decide` over a reading a `see:` step made needs the same
// six comparisons over words that are already in hand, and a second implementation of "contains these
// words, like this sentence" is the kind of duplication that drifts.
func textOnWords(read []string, m map[string]any) (bool, string, error) {
	var parts []string
	result := true
	check := func(label string, okNow bool) {
		parts = append(parts, fmt.Sprintf("%s=%v", label, okNow))
		if !okNow {
			result = false
		}
	}

	if sets := wordSets(m["contains_any"]); len(sets) > 0 {
		okAny := false
		for _, set := range sets {
			if allWords(read, set) {
				okAny = true
			}
		}
		check("contains_any", okAny)
	}
	if words := stringList(m["not_contains"]); len(words) > 0 {
		var found []string
		for _, w := range words {
			if hasWord(read, w) {
				found = append(found, w)
			}
		}
		check("not_contains["+strings.Join(words, ",")+"]", len(found) == 0)
	}
	if want, ok := m["equals"].(string); ok {
		check("equals", normaliseText(strings.Join(read, " ")) == normaliseText(want))
	}
	if pat, ok := m["matches"].(string); ok {
		re, err := regexp.Compile("(?i)" + pat)
		if err != nil {
			return false, "", fmt.Errorf("`matches: %q` is not a regular expression: %v", pat, err)
		}
		check("matches", re.MatchString(strings.Join(read, " ")))
	}
	// `fuzzy:` IS AN EDIT DISTANCE ON EACH WORD - the unit that works for short words. See
	// `wordWithin` for why a ratio is the wrong unit here: `disk`->`dsk` is one glyph and scores
	// 0.750 as a ratio, so a ratio threshold refuses exactly the words a pane uses for buttons.
	if words := stringList(m["contains"]); len(words) > 0 {
		fuzzy := 0
		if v, ok := toFloat(m["fuzzy"]); ok {
			if v < 0 || v > maxFuzzy {
				return false, "", fmt.Errorf("`fuzzy: %g` is outside the accepted range; it is an "+
					"EDIT DISTANCE per word, at most %d, because beyond that `install` and "+
					"`uninstall` are one edit apart from real words", v, maxFuzzy)
			}
			fuzzy = int(v)
		}
		check("contains["+strings.Join(words, ",")+"]", allWordsWithin(read, words, fuzzy))
	}
	// `like:` IS THE ONLY RATIO, and it is right only for a whole region that is ONE SENTENCE -
	// `root partition will be ext4`. A ratio over a long region is reliable; over a three-letter
	// word it is not, which is why `fuzzy:` above is a distance rather than this.
	if want, ok := m["like"].(string); ok {
		floor := 0.85
		if v, ok := toFloat(m["ratio"]); ok {
			if v <= 0 || v > 1 {
				return false, "", fmt.Errorf("`ratio: %g` must be between 0 and 1", v)
			}
			floor = v
		}
		got := strings.Join(read, " ")
		ratio := levenshteinRatio(want, got)
		parts = append(parts, fmt.Sprintf("like=%q ratio=%.3f>=%.2f", want, ratio, floor))
		if ratio < floor {
			result = false
		}
	}
	if len(parts) == 0 {
		return false, "", fmt.Errorf("a text criterion needs `contains:`, `contains_any:`, " +
			"`not_contains:`, `equals:`, `matches:` or `like:`")
	}
	return result, "text[" + strings.Join(parts, " ") + "]", nil
}

// colour evaluates ONE `colour:` criterion, which may name its own rect and several numbers.
func (fr *frame) colour(entry any) (bool, string, error) {
	m, ok := entry.(map[string]any)
	if !ok {
		return false, "", fmt.Errorf("a colour criterion is a mapping, like " +
			"`{rect: bottom 4.5%%, spread: {less_than: 42}}`")
	}
	rectName, _ := m["rect"].(string)
	if rectName == "" {
		return false, "", fmt.Errorf("a colour criterion must say `rect:` - where on the frame to " +
			"measure. It is a fraction, so it cannot be measured at the wrong size")
	}
	rect, err := parseRectWords(rectName)
	if err != nil {
		return false, "", err
	}
	got := screen.Measure(fr.img, rect)

	var parts []string
	result := true

	// THE NUMBERS, each with its own comparison. `between` is spelled out rather than written as two
	// relations, because `mean: {between: [20, 60]}` is what a person says.
	apply := func(label string, value float64, spec any) error {
		cmp, ok := spec.(map[string]any)
		if !ok {
			return fmt.Errorf("`%s:` needs a comparison, like {less_than: 42} or {between: [20, 60]}",
				label)
		}
		okNow, err := compareNumber(value, cmp)
		if err != nil {
			return fmt.Errorf("`%s:` %v", label, err)
		}
		parts = append(parts, fmt.Sprintf("%s=%g", label, value))
		if !okNow {
			result = false
		}
		return nil
	}

	if spec, ok := m["mean"]; ok {
		if err := apply("mean", got.Mean, spec); err != nil {
			return false, "", err
		}
	}
	if spec, ok := m["spread"]; ok {
		if err := apply("spread", got.Spread, spec); err != nil {
			return false, "", err
		}
	}
	if spec, ok := m["sat"]; ok {
		if err := apply("sat", got.Sat, spec); err != nil {
			return false, "", err
		}
	}
	if spec, ok := m["flat_rows"]; ok {
		if err := apply("flat_rows", float64(got.FlatRows), spec); err != nil {
			return false, "", err
		}
	}
	if spec, ok := m["contrast"]; ok {
		if err := apply("contrast", got.Max-got.Min, spec); err != nil {
			return false, "", err
		}
	}
	if spec, ok := m["min"]; ok {
		if err := apply("min", got.Min, spec); err != nil {
			return false, "", err
		}
	}
	if spec, ok := m["max"]; ok {
		if err := apply("max", got.Max, spec); err != nil {
			return false, "", err
		}
	}

	// `vs:` IS THE ONLY COMPOSITION: this rect's number compared against ANOTHER rect's. It is the
	// panel rule's `bottom.mean - body.mean > 40`, and it is why one rect alone cannot answer "is
	// there a panel".
	if vs, ok := m["vs"].(map[string]any); ok {
		other, _ := vs["rect"].(string)
		if other == "" {
			return false, "", fmt.Errorf("`vs:` must say `rect:` - the region to compare against")
		}
		otherRect, err := parseRectWords(other)
		if err != nil {
			return false, "", err
		}
		them := screen.Measure(fr.img, otherRect)
		for label, spec := range vs {
			if label == "rect" {
				continue
			}
			var mine, theirs float64
			switch label {
			case "mean":
				mine, theirs = got.Mean, them.Mean
			case "spread":
				mine, theirs = got.Spread, them.Spread
			case "sat":
				mine, theirs = got.Sat, them.Sat
			default:
				return false, "", fmt.Errorf("`vs:` can compare mean, spread or sat; not %q", label)
			}
			cmp, ok := spec.(map[string]any)
			if !ok {
				return false, "", fmt.Errorf("`vs: {%s: ...}` needs a comparison", label)
			}
			// THE DIFFERENCE IS SIGNED, so `greater_than: 40` means this one is brighter by 40. A
			// file that wants either direction says `between:` or compares twice.
			diff := mine - theirs
			okNow, err := compareNumber(diff, cmp)
			if err != nil {
				return false, "", fmt.Errorf("`vs: {%s: ...}` %v", label, err)
			}
			parts = append(parts, fmt.Sprintf("%s-vs-%s=%+.1f", label, other, diff))
			if !okNow {
				result = false
			}
		}
	}

	if len(parts) == 0 {
		return false, "", fmt.Errorf("a colour criterion needs at least one of mean, spread, sat, " +
			"flat_rows, contrast, min, max or vs")
	}
	return result, "colour[" + rectName + " " + strings.Join(parts, " ") + "]", nil
}

// group evaluates one `any_of:` entry, which is a mapping of criterion lists.
func (fr *frame) group(defaultRect string, entry any) (bool, string, error) {
	m, ok := entry.(map[string]any)
	if !ok {
		return false, "", fmt.Errorf("an `any_of:` group is a mapping of criterion lists")
	}
	result := true
	var notes []string
	if list, ok := m["text"].([]any); ok {
		for _, e := range list {
			okNow, note, err := fr.text(defaultRect, e)
			if err != nil {
				return false, "", err
			}
			notes = append(notes, note)
			if !okNow {
				result = false
			}
		}
	}
	if list, ok := m["colour"].([]any); ok {
		for _, e := range list {
			okNow, note, err := fr.colour(e)
			if err != nil {
				return false, "", err
			}
			notes = append(notes, note)
			if !okNow {
				result = false
			}
		}
	}
	if len(notes) == 0 {
		return false, "", fmt.Errorf("an `any_of:` group needs `text:` or `colour:`")
	}
	return result, strings.Join(notes, " "), nil
}

// read OCRs a rect, caching per rect because two criteria may name the same one.
func (fr *frame) read(rectName string) ([]string, error) {
	if w, ok := fr.words[rectName]; ok {
		return w, nil
	}
	rect, err := parseRectWords(rectName)
	if err != nil {
		return nil, err
	}
	b := fr.img.Bounds()
	w, h := b.Dx(), b.Dy()
	region := [4]int{
		int(float64(w) * rect.XFrom), int(float64(h) * rect.From),
		int(float64(w) * rect.XTo), int(float64(h) * rect.To),
	}
	// A DEGENERATE RECT IS A MISTAKE IN THE FILE, not a reading of nothing.
	if region[2]-region[0] < 2 || region[3]-region[1] < 2 {
		return nil, fmt.Errorf("`rect: %q` is %dx%d pixels on this frame - too small to read",
			rectName, region[2]-region[0], region[3]-region[1])
	}
	cropped, err := screen.Crop(fr.img, region, 3)
	if err != nil {
		return nil, err
	}
	words, err := fr.ex.readCrop(cropped, "check")
	if err != nil {
		return nil, err
	}
	fr.words[rectName] = words
	return words, nil
}

// compareNumber applies a comparison mapping to one number.
func compareNumber(v float64, cmp map[string]any) (bool, error) {
	if r, ok := cmp["between"].([]any); ok && len(r) == 2 {
		lo, ok1 := toFloat(r[0])
		hi, ok2 := toFloat(r[1])
		if !ok1 || !ok2 {
			return false, fmt.Errorf("`between:` takes two numbers")
		}
		return v >= lo && v <= hi, nil
	}
	for k, raw := range cmp {
		n, ok := toFloat(raw)
		if !ok {
			return false, fmt.Errorf("`%s:` takes a number", k)
		}
		switch k {
		case "less_than":
			return v < n, nil
		case "greater_than":
			return v > n, nil
		case "at_least":
			return v >= n, nil
		case "at_most":
			return v <= n, nil
		case "equals":
			return v == n, nil
		}
		return false, fmt.Errorf("`%s:` is not a comparison; use less_than, greater_than, "+
			"at_least, at_most, equals or between", k)
	}
	return false, fmt.Errorf("needs a comparison, like {less_than: 42}")
}

// --- small helpers -----------------------------------------------------------------------

// stringList reads a list of strings, WHATEVER GO SLICE IT ARRIVED AS.
//
// A YAML DECODER GIVES `[]interface{}` AND THE `.walk` CONVERTER GIVES `[]string`, and the difference
// is invisible until a criterion silently matches nothing:
//
//	[debug] contains=[]string []string{"Alpine"} stringList=[]
//
// - which is a `want:` that always answers false on a screen that plainly says the word. The type
// switch is the fix, and it is written out rather than reflected because there are three shapes and
// reflection would be slower and no clearer.
func stringList(v any) []string {
	switch list := v.(type) {
	case []string:
		return list
	case []any:
		out := make([]string, 0, len(list))
		for _, x := range list {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, x := range list {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// wordSets reads [[a,b],[c]] into slices, the form `contains_any:` takes.
func wordSets(v any) [][]string {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	var out [][]string
	for _, x := range list {
		out = append(out, stringList(x))
	}
	return out
}

func allWords(read []string, want []string) bool {
	for _, w := range want {
		if !hasWord(read, w) {
			return false
		}
	}
	return true
}

// allWordsWithin is `allWords` with an EDIT DISTANCE allowed per word.
//
// THE UNIT IS THE WHOLE POINT. A ratio divides by length, so it refuses a short word with one glyph
// dropped - and a pane's buttons and field labels ARE short words. Measured:
//
//	'dsk'   for 'disk'      distance 1   ratio 0.750   <- a ratio threshold of 0.85 REFUSES this
//	'ext'   for 'ext4'      distance 1   ratio 0.750   <- REFUSED
//	'ys'    for 'yes'       distance 1   ratio 0.667   <- REFUSED
//	'instal' for 'install'  distance 1   ratio 0.857
//
// so `fuzzy: 1` accepts every one of those, and still refuses the pairs that matter:
//
//	'uninstall' for 'install'  distance 2   <- the opposite page
//	'uninstall' for 'disk'     distance 7
func allWordsWithin(read []string, want []string, distance int) bool {
	for _, w := range want {
		if !hasWordWithin(read, w, distance) {
			return false
		}
	}
	return true
}

// hasWordWithin finds a read word within `distance` edits of an expected one.
func hasWordWithin(read []string, want string, distance int) bool {
	w := normaliseText(want)
	if w == "" {
		return false
	}
	for _, r := range read {
		got := normaliseText(r)
		if got == "" {
			continue
		}
		if distance == 0 {
			if got == w {
				return true
			}
			continue
		}
		// A LENGTH GATE FIRST, because it is free and it rejects most candidates: two strings whose
		// lengths differ by more than the budget cannot be within it.
		ra, rb := []rune(w), []rune(got)
		if d := len(ra) - len(rb); d > distance || d < -distance {
			continue
		}
		if levenshtein(ra, rb) <= distance {
			return true
		}
	}
	return false
}

// hasWord is a WORD comparison on normalised text, so punctuation does not matter and a substring
// does not match: `install` must not be found inside `uninstall`.
func hasWord(read []string, want string) bool {
	w := normaliseText(want)
	for _, r := range read {
		if normaliseText(r) == w {
			return true
		}
	}
	return false
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// normaliseText lowercases and folds punctuation and runs of space.
//
// IT IS THE FIRST HALF OF EVERY COMPARISON, which is why `erase disk` and `Erase Disk` are equal at
// ratio 1.0 and do not need `fuzzy:` - case and spacing are not OCR errors, they are the same text.
func normaliseText(s string) string {
	return strings.Join(strings.Fields(nonAlnum.ReplaceAllString(strings.ToLower(s), " ")), " ")
}

// levenshteinRatio is 1 - distance/maxLen over NORMALISED text.
//
// MEASURED ON THE REGION-SIZED READINGS THIS REPOSITORY'S PANES PRODUCE, because the unit matters and
// getting it wrong is what makes this feature dangerous:
//
//	want 'install now'                  read 'insall now'                 0.909  <- one glyph lost
//	want 'installation summary'         read 'installaton summary'        0.950
//	want 'root partition will be ext4'  read 'root partiton will be ext4' 0.963
//	want 'erase disk'                   read 'Erase Disk'                 1.000  <- case only
//	want '/dev/vda2  ext4'              read '/dev/vda2 ext4'             1.000  <- spacing only
//	want 'erase disk'                   read 'Erase disk and install ...' 0.345  <- a different pane
//
// SO 0.85 SEPARATES A LOST GLYPH FROM A WRONG CROP. THE FULL-FRAME NUMBER DOES NOT: `welcome` came
// back from tesseract as `Wesn Home`, a ratio of 0.556, and `install` against `uninstall` scores
// 0.778 - higher than `next` against `Next` before normalisation. A PAGE-level ratio cannot tell
// "one glyph misread" from "the opposite page"; a REGION-level one can, because a cropped button is
// three words rather than forty.
func levenshteinRatio(want, got string) float64 {
	a, b := normaliseText(want), normaliseText(got)
	if a == "" && b == "" {
		return 1
	}
	if a == "" || b == "" {
		return 0
	}
	ra, rb := []rune(a), []rune(b)
	d := levenshtein(ra, rb)
	max := len(ra)
	if len(rb) > max {
		max = len(rb)
	}
	return 1 - float64(d)/float64(max)
}

// levenshtein is the edit distance, over runes so a multi-byte character is one edit.
func levenshtein(a, b []rune) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

// maxFuzzy is the largest edit distance per word the validator accepts. Beyond 2, `install` and
// `uninstall` (distance 2) start to be within budget of words that mean something else.
const maxFuzzy = 2

// textOn applies a text criterion to words already read, for `want:`.
func (fr *frame) textOn(words []string, m map[string]any) (bool, string, error) {
	return textOnWords(words, m)
}
