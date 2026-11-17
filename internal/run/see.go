package run

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/easydev-org/easyvm/internal/screen"
	"github.com/easydev-org/easyvm/internal/walk"
)

// `see:` MEASURES, `want:` DECIDES, AND NEITHER KNOWS ABOUT THE OTHER.
//
// THIS FILE REPLACES THE PATTERN THAT MADE EVERY NEW SCREEN SHAPE A REBUILD. Today a question about a
// picture is a Go function reached through a dispatch table - `desktop: linux/cinnamon` and eight
// siblings in `internal/screen` - because the MEASUREMENT and the COMPARISON live in the same
// function. So changing a threshold recompiles the binary and rebuilds the image, which is the thing
// this design exists to stop.
//
// THE SPLIT IS THE WHOLE DESIGN:
//
//	see:  reads the frame and binds NUMBERS   -> internal/screen.Measure, which decides nothing
//	want: compares those numbers, binds a BOOL -> this file, which never reads the frame
//
// MEASURED ON THE FRAMES ALREADY IN internal/screen/testdata - which is why this is a design rather
// than a hope: one rule written in `want:` form answers mint, zorin and a BIOS menu, and reaches the
// same three verdicts the Go reaches today.
//
//	mint   bottom spread=8.5  flat_rows=11/49  -> true   (its panel IS its wallpaper's colour)
//	zorin  top    spread=25.7 diff=61.8        -> true   (a panel that differs in brightness)
//	mx     bottom spread=40.7 flat_rows=0/49   -> false  (a BIOS menu is not a desktop)
//
// THE `IsCinnamonDesktop` THIS MAKES UNNECESSARY was 80 lines of Go and a new image.

// doSee measures one rect and binds its numbers as named readings.
//
// THE NAMES IT BINDS ARE `name.mean`, `name.spread`, `name.sat`, `name.flat_rows`, `name.rows`, and
// for a text rect, `name.words`. A `want:` step refers to them by those names, so a file can say
// `bottom.flat_rows` and a reader can find where it came from.
func (e *Executor) doSee(s walk.Step) error {
	raw, name, rectRaw, ofRaw, err := parseSeeStep(s)
	if err != nil {
		return err
	}
	_ = raw

	// `rect_at:` IS READ HERE, ONCE, AND HANDED DOWN. The step is `see: {name, of, rect, rect_at}` -
	// the size is INSIDE the mapping, not beside the key - so reading it from `s.Spec` finds nothing
	// and leaves the rect scaled by the frame. That mistake survived one dispatch: the macos recovery
	// frame is 640x480 and its rect was written at 1920x1080, and the failure was identical to the one
	// this whole change exists to fix.
	at, _ := pairOfMapping(raw, "rect_at")
	if at == ([2]int{}) {
		at, _ = pairOfMapping(raw, "region_at")
	}

	// A TEXT rect is read with the OCR path rather than measured, because its result is words.
	for _, want := range ofRaw {
		if fmt.Sprint(want) == "text" {
			return e.seeText(s, name, rectRaw, at)
		}
	}

	rect, err := parseRectAt(rectRaw, at)
	if err != nil {
		return fail(s.Name, "%v", err)
	}
	// A PIXEL RECT FROM A `.walk` IS `[]any` OF `int`, AND A YAML ONE IS `[]any` OF `int` TOO - but
	// the converter can also produce `[]int` when it builds the full-frame default. Both are
	// normalised by `parseRect`; this line is here so a future third shape is caught rather than
	// silently read as a fraction.
	f, err := e.Driver.Frame()
	if err != nil {
		return fail(s.Name, "reading the frame: %v", err)
	}
	img, err := screen.Load(f.Path())
	if err != nil {
		return fail(s.Name, "loading the frame: %v", err)
	}
	r := screen.Measure(img, rect)

	if e.numbers == nil {
		e.numbers = map[string]float64{}
	}
	set := func(k string, v float64) { e.numbers[name+"."+k] = v }
	// EVERY NUMBER IS ALWAYS BOUND, so a `want:` that names `spread` does not depend on whether the
	// `of:` list happened to include it. `of:` says which numbers the READER cares about; binding all
	// of them costs nothing and removes a way for two steps to disagree about what is measurable.
	set("mean", r.Mean)
	set("spread", r.Spread)
	set("sat", r.Sat)
	set("flat_rows", float64(r.FlatRows))
	set("rows", float64(r.Rows))
	set("min", r.Min)
	set("max", r.Max)

	e.Driver.Log(fmt.Sprintf("  measured %s: %s", name, r.Explain()))
	return nil
}

// seeText reads a rect with OCR. It exists so a `see:` step can produce WORDS as well as numbers,
// which is what makes `see:` a generalisation of `ocr:` rather than a second mechanism beside it.
func (e *Executor) seeText(s walk.Step, name string, rectRaw any, at [2]int) error {
	rect, err := parseRectAt(rectRaw, at)
	if err != nil {
		return fail(s.Name, "%v", err)
	}
	// A PIXEL RECT FROM A `.walk` IS `[]any` OF `int`, AND A YAML ONE IS `[]any` OF `int` TOO - but
	// the converter can also produce `[]int` when it builds the full-frame default. Both are
	// normalised by `parseRect`; this line is here so a future third shape is caught rather than
	// silently read as a fraction.
	f, err := e.Driver.Frame()
	if err != nil {
		return fail(s.Name, "reading the frame: %v", err)
	}
	img, err := screen.Load(f.Path())
	if err != nil {
		return fail(s.Name, "loading the frame: %v", err)
	}
	// THE ENLARGEMENT COMES OFF THE STEP, because a reading of small text that finds nothing is
	// indistinguishable from a pane that is not up.
	scale := 3
	if v, ok := toFloat(s.Spec["scale"]); ok && v > 0 {
		scale = int(v)
	}
	// THE RECT ALREADY CARRIES `rect_at:`, so the crop is placed on the screen the file measured it
	// on rather than on whatever size this frame arrived at - a macos recovery frame is 640x480 and
	// its rects were written at 1920x1080.
	words, err := e.readWords(img, rect, scale)
	if err != nil {
		return fail(s.Name, "%v", err)
	}
	if e.seen == nil {
		e.seen = map[string][]string{}
	}
	e.seen[name+".words"] = words
	// AND A `words:` ALIAS, so a `want:` written against the OLD `ocr:` name still resolves while
	// both vocabularies are in the tree.
	e.seen[name] = words
	e.Driver.Log(fmt.Sprintf("  read %s: %d word(s): %s", name, len(words),
		truncateWords(words, 10)))
	return nil
}

// wantOp is one comparison in a `want:` step.
type wantOp struct {
	// left is a reading name or a literal.
	left string
	// minus is another reading, subtracted from left before the comparison, or "".
	minus string
	// rel is the relation: less_than, greater_than, at_least, at_most, equals.
	rel string
	// right is a literal number (or a reading name).
	right string
}

// doWant evaluates comparisons over the numbers `see:` bound and binds a boolean.
//
// IT NEVER READS THE SCREEN. That is what makes a `want:` free to re-ask, and it is why a `loop:`
// can test the same boolean on every attempt without paying for a frame each time.
func (e *Executor) doWant(s walk.Step) error {
	raw, ok := s.Spec["want"].(map[string]any)
	if !ok {
		return fail(s.Name, "`want:` must be a mapping: {name, all|any|not}")
	}
	name, _ := raw["name"].(string)
	if name == "" {
		return fail(s.Name, "`want:` must say `name:` - the boolean is bound under it and a "+
			"decision nobody named cannot be tested by `until:`")
	}

	var okNow bool
	var detail string
	var err error

	switch {
	case raw["all"] != nil:
		okNow, detail, err = e.evalGroup(raw["all"], true)
	case raw["any"] != nil:
		okNow, detail, err = e.evalGroup(raw["any"], false)
	case raw["not"] != nil:
		okNow, detail, err = e.evalNot(raw["not"])
	default:
		return fail(s.Name, "`want:` must say one of `all:`, `any:` or `not:` - a decision with "+
			"no comparisons is a decision that is always true")
	}
	if err != nil {
		return fail(s.Name, "%v", err)
	}

	if e.facts == nil {
		e.facts = map[string]bool{}
	}
	e.facts[name] = okNow
	e.Driver.Log(fmt.Sprintf("  %s = %v (%s)", name, okNow, detail))
	return nil
}

// evalGroup evaluates a list of comparisons, requiring all of them or any of them.
func (e *Executor) evalGroup(list any, needAll bool) (bool, string, error) {
	entries, ok := list.([]any)
	if !ok || len(entries) == 0 {
		return false, "", fmt.Errorf("`want: all:`/`any:` needs a list of comparisons")
	}
	var parts []string
	result := needAll
	for _, entry := range entries {
		// A NESTED `all:`/`any:` IS ALLOWED, because the rule for the two edges is the same three
		// comparisons repeated - and a reader should be able to see that shape.
		if m, ok := entry.(map[string]any); ok {
			if m["all"] != nil || m["any"] != nil || m["not"] != nil {
				var v bool
				var d string
				var err error
				switch {
				case m["all"] != nil:
					v, d, err = e.evalGroup(m["all"], true)
				case m["any"] != nil:
					v, d, err = e.evalGroup(m["any"], false)
				default:
					v, d, err = e.evalNot(m["not"])
				}
				if err != nil {
					return false, "", err
				}
				parts = append(parts, d)
				if needAll && !v {
					result = false
				}
				if !needAll && v {
					result = true
				}
				continue
			}
		}
		// A `text:` COMPARISON IS A LIST OF CRITERIA over a named reading, and it is evaluated by the
		// same code `check:` uses - because `want:` and `check:` answer the same question about the
		// words on a screen, and two implementations would disagree eventually.
		if m, ok := entry.(map[string]any); ok {
			// `text:` IS A LIST, AND A ONE-ITEM LIST MAY ARRIVE AS THE MAP ITSELF - a YAML decoder
			// and the `.walk` converter do not have to agree on that, so both shapes are taken.
			var list []any
			switch t := m["text"].(type) {
			case []any:
				list = t
			case map[string]any:
				list = []any{t}
			}
			if list != nil {
				okNow, note, err := e.evalTextCriteria(list)
				if err != nil {
					return false, "", err
				}
				parts = append(parts, note)
				if needAll && !okNow {
					result = false
				}
				if !needAll && okNow {
					result = true
				}
				continue
			}
		}
		op, err := e.parseOp(entry)
		if err != nil {
			return false, "", err
		}
		v, d, err := e.evalOp(op)
		if err != nil {
			return false, "", err
		}
		parts = append(parts, d)
		if needAll && !v {
			result = false
		}
		if !needAll && v {
			result = true
		}
	}
	word := "all"
	if !needAll {
		word = "any"
	}
	return result, fmt.Sprintf("%s of [%s]", word, strings.Join(parts, "; ")), nil
}

func (e *Executor) evalNot(v any) (bool, string, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return false, "", fmt.Errorf("`want: not:` needs a comparison")
	}
	if m["all"] != nil || m["any"] != nil {
		var inner bool
		var d string
		var err error
		if m["all"] != nil {
			inner, d, err = e.evalGroup(m["all"], true)
		} else {
			inner, d, err = e.evalGroup(m["any"], false)
		}
		if err != nil {
			return false, "", err
		}
		return !inner, "not(" + d + ")", nil
	}
	op, err := e.parseOp(v)
	if err != nil {
		return false, "", err
	}
	got, d, err := e.evalOp(op)
	if err != nil {
		return false, "", err
	}
	return !got, "not(" + d + ")", nil
}

// comparisonKey matches `reading.name: {less_than: 42}`.
var comparisonKey = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_.]*)$`)

// parseOp turns one comparison entry into an operation.
//
// THE SHAPE IS `<reading>: {<relation>: <value>}`, which is what a person says out loud:
//
//	bottom.spread: {less_than: 42}
//	bottom.mean:   {minus: body.mean, greater_than: 40}
func (e *Executor) parseOp(entry any) (wantOp, error) {
	m, ok := entry.(map[string]any)
	if !ok {
		return wantOp{}, fmt.Errorf("a `want:` comparison must be a mapping like " +
			"`bottom.spread: {less_than: 42}`")
	}
	if len(m) != 1 {
		return wantOp{}, fmt.Errorf("a `want:` comparison names exactly one reading, got %d", len(m))
	}
	var left string
	var body any
	for k, v := range m {
		if !comparisonKey.MatchString(k) {
			return wantOp{}, fmt.Errorf("%q is not a reading name; a comparison is "+
				"`<reading>: {<relation>: <value>}`", k)
		}
		left, body = k, v
	}
	mapping, ok := body.(map[string]any)
	if !ok {
		return wantOp{}, fmt.Errorf("`%s:` needs a relation, like {less_than: 42}", left)
	}
	// `minus` IS A SECOND READING, used as `{minus: body.mean, greater_than: 40}`. It is the only
	// arithmetic this vocabulary needs, and it is written as a word rather than an operator: the
	// comparison is still "greater_than 40", and *what* is greater than 40 is the difference.
	var minus, rel string
	var relValue any
	for k, v := range mapping {
		if k == "minus" {
			minus = fmt.Sprint(v)
			continue
		}
		switch k {
		case "less_than", "greater_than", "at_least", "at_most", "equals":
			rel, relValue = k, v
		}
	}
	if rel == "" {
		return wantOp{}, fmt.Errorf("`%s:` has no relation I know; use less_than, greater_than, "+
			"at_least, at_most, equals, or `minus:` beside one of them", left)
	}
	return wantOp{left: left, minus: minus, rel: rel, right: fmt.Sprint(relValue)}, nil
}

// evalOp computes one comparison and says what it found, so a failure names the number.
func (e *Executor) evalOp(op wantOp) (bool, string, error) {
	lv, err := e.lookupNumber(op.left)
	if err != nil {
		return false, "", err
	}
	shownLeft := op.left
	if op.minus != "" {
		mv, err := e.lookupNumber(op.minus)
		if err != nil {
			return false, "", err
		}
		lv -= mv
		shownLeft = fmt.Sprintf("%s-%s", op.left, op.minus)
	}
	rv, err := e.lookupNumber(op.right)
	if err != nil {
		return false, "", err
	}
	shown := fmt.Sprintf("%s=%g %s %g", shownLeft, lv, op.rel, rv)
	switch op.rel {
	case "less_than":
		return lv < rv, shown, nil
	case "greater_than":
		return lv > rv, shown, nil
	case "at_least":
		return lv >= rv, shown, nil
	case "at_most":
		return lv <= rv, shown, nil
	case "equals":
		return lv == rv, shown, nil
	}
	return false, "", fmt.Errorf("unknown relation %q", op.rel)
}

// lookupNumber resolves a reading name, or a literal number.
func (e *Executor) lookupNumber(name string) (float64, error) {
	if v, err := strconv.ParseFloat(name, 64); err == nil {
		return v, nil
	}
	if e.numbers != nil {
		if v, ok := e.numbers[name]; ok {
			return v, nil
		}
	}
	// A `see:` step binds every number, so a missing name is a typo or a step that never ran - and
	// both want to be named rather than compared as zero.
	return 0, fmt.Errorf("`%s` is not a number any `see:` step bound; a `want:` comparison "+
		"names a reading such as `bottom.spread`", name)
}

// parseRect turns a `rect:` value into a screen.Rect.
//
// FRACTIONS, NOT PIXELS, WHICH IS THE POINT. A pixel rect carries an implicit claim about the frame's
// size, and the files record what that costs: `click (936, 628) is a coordinate measured at 1920x1080
// but the framebuffer is 640x480`. A fraction means the same place at every size.
func parseRect(raw any) (screen.Rect, error) {
	return parseRectAt(raw, [2]int{})
}

// parseRectAt is `parseRect` for a step that also says `rect_at:` - the screen its pixels were
// written for. Without it a pixel rect is scaled by the frame, which is wrong whenever the two differ.
func parseRectAt(raw any, at [2]int) (screen.Rect, error) {
	switch v := raw.(type) {
	case string:
		return parseRectWords(v)
	case []any:
		if len(v) != 4 {
			return screen.Rect{}, fmt.Errorf("`rect:` as a list needs [x0, y0, x1, y1] as " +
				"fractions of the frame")
		}
		var f [4]float64
		for i := range v {
			n, ok := toFloat(v[i])
			if !ok {
				return screen.Rect{}, fmt.Errorf("`rect:` entry %d is not a number", i+1)
			}
			f[i] = n
		}
		// A LIST IS PIXELS OR FRACTIONS, AND THE TWO ARE TOLD APART BY MAGNITUDE.
		//
		// Pixels are what the older files carry - `rect: [760, 200, 1400, 560]` on a 1920x1080 frame -
		// and a fraction is what the newer form prefers, because it cannot be measured at the wrong
		// size. A fraction is at most 1.0 and a pixel coordinate is at least 1 in any real frame, so
		// values above 1 in any position mean PIXELS.
		//
		// IT IS NOT A GUESS: a rect of [0, 0, 1, 1] is the single top-left pixel and no file means
		// that, while a rect whose last two numbers are 1920 and 1080 is a frame this repository
		// measures at on every page. The alternative - a second key - is what `rect_at:` already is,
		// except `rect_at:` says the SIZE and this says the UNIT.
		pixels := f[0] > 1 || f[1] > 1 || f[2] > 1 || f[3] > 1
		if !pixels {
			return screen.Rect{From: f[1], To: f[3], XFrom: f[0], XTo: f[2]}, nil
		}
		// A PIXEL RECT IS CONVERTED AGAINST THE FRAME THE READING ARRIVED FROM, which the caller
		// knows and this function does not - so it is returned unconverted and the caller scales it.
		return screen.Rect{
			From: f[1], To: f[3], XFrom: f[0], XTo: f[2], Pixels: true,
			AtW: at[0], AtH: at[1],
		}, nil
	}
	return screen.Rect{}, fmt.Errorf("`rect:` must be a name like `bottom 4.5%%` or a list of " +
		"four numbers")
}

var rectWords = regexp.MustCompile(`^(middle|top|bottom)(?:\s+(\d+(?:\.\d+)?)\s*(%|px))?$`)

// parseRectWords reads the named forms: `middle`, `top`, `top 4.5%`, `bottom 40px`.
//
// `px` IS ACCEPTED AND CONVERTED, because a panel's height is a pixel measurement everywhere in the
// files (`panel 48 rows`, `panel 8 rows`) and pretending otherwise would make those numbers
// unquotable. The conversion divides by the frame height, which the caller passes.
func parseRectWords(s string) (screen.Rect, error) {
	m := rectWords.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return screen.Rect{}, fmt.Errorf("`rect: %q` is not a region; use `middle`, `top`, "+
			"`top 4.5%%`, `bottom 40px`, or four fractions", s)
	}
	where, amount, unit := m[1], m[2], m[3]
	if amount == "" {
		switch where {
		case "middle":
			return screen.Rect{From: 0.35, To: 0.65, XTo: 1}, nil
		case "top":
			return screen.Rect{From: 0, To: 0.045, XTo: 1}, nil
		default:
			return screen.Rect{From: 0.955, To: 1, XTo: 1}, nil
		}
	}
	n, err := strconv.ParseFloat(amount, 64)
	if err != nil {
		return screen.Rect{}, fmt.Errorf("`rect: %q`: %v", s, err)
	}
	var frac float64
	switch unit {
	case "%":
		frac = n / 100
	case "px":
		// 1080 is the only height any file in this repository measures at; the guard is that every
		// file says `resolution:` and this is a reading, not a coordinate.
		frac = n / 1080
	default:
		return screen.Rect{}, fmt.Errorf("`rect: %q`: the unit is %% or px", s)
	}
	if where == "top" {
		return screen.Rect{From: 0, To: frac, XTo: 1}, nil
	}
	if where == "bottom" {
		return screen.Rect{From: 1 - frac, To: 1, XTo: 1}, nil
	}
	return screen.Rect{From: 0.5 - frac/2, To: 0.5 + frac/2, XTo: 1}, nil
}

// evalTextCriteria applies `check:`'s text criteria to the readings a `see:` step bound.
//
// A `want:` MAY COMPARE WORDS AS WELL AS NUMBERS, because "the screen says this" and "this number is
// in range" are both decisions about the same frame. Rather than a second implementation, the words
// are handed to the same function `check:` uses.
func (e *Executor) evalTextCriteria(list []any) (bool, string, error) {
	var notes []string
	result := true
	for _, raw := range list {
		m, ok := raw.(map[string]any)
		if !ok {
			return false, "", fmt.Errorf("`text:` takes the same criteria `check:` does")
		}
		reading, _ := m["from"].(string)
		words := e.seen[reading]
		if words == nil {
			words = e.seen[reading+".words"]
		}
		if words == nil {
			return false, "", fmt.Errorf("`text: {from: %s}` names a reading no `see:` step made. "+
				"A `see: {of: [text]}` has to come first, in file order, with that `name:`", reading)
		}
		fr := &frame{ex: e}
		okNow, note, err := fr.textOn(words, m)
		if err != nil {
			return false, "", err
		}
		notes = append(notes, note)
		if !okNow {
			result = false
		}
	}
	return result, strings.Join(notes, "; "), nil
}

// parseSeeStep validates a `see:` mapping and returns the four things the work needs.
//
// IT IS SPLIT OUT SO A LOOP CAN ASK "IS THIS A READING I CAN TAKE" WITHOUT TAKING ONE. `doSee` used to
// own both the validation and the capture, so the only way to check a loop's `see:` at assembly time
// was to capture a frame - which a loop cannot do before its first attempt.
func parseSeeStep(s walk.Step) (raw map[string]any, name string, rectRaw any, ofRaw []any, err error) {
	raw, ok := s.Spec["see"].(map[string]any)
	if !ok {
		return nil, "", nil, nil, fail(s.Name, "`see:` must be a mapping: {name, rect, of}")
	}
	name, _ = raw["name"].(string)
	if name == "" {
		return nil, "", nil, nil, fail(s.Name, "`see:` must say `name:` - the readings are bound "+
			"under it, and a measurement nobody named cannot be compared")
	}
	rectRaw, ok = raw["rect"]
	if !ok {
		return nil, "", nil, nil, fail(s.Name, "`see:` must say `rect:` - the region to measure. It "+
			"is a fraction of the frame (`top 4.5%%`, `bottom 40px`, `middle`) so it cannot be "+
			"measured at the wrong size")
	}
	ofRaw, _ = raw["of"].([]any)
	if len(ofRaw) == 0 {
		return nil, "", nil, nil, fail(s.Name, "`see:` must say `of:` - which numbers to bind. "+
			"`of: [mean, spread]` binds %s.mean and %s.spread", name, name)
	}
	return raw, name, rectRaw, ofRaw, nil
}
