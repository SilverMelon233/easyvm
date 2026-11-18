package run

import (
	"fmt"
	"image"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/easydev-org/easyvm/internal/screen"
	"github.com/easydev-org/easyvm/internal/walk"
)

// `see:` MEASURES, `check:` DECIDES, AND NEITHER KNOWS ABOUT THE OTHER.
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
	// ONLY WHAT `of:` ASKED FOR IS BOUND, AND THAT IS A CORRECTION.
	//
	// It used to bind all seven unconditionally, with the reasoning "a `want:` that names `spread`
	// should not depend on whether `of:` listed it". MEASURED what that cost: a `see: {reference:}`
	// binds one number and asks one question, and its reading carried EIGHT - so
	// `check: {from: shot}` was ambiguous and refused:
	//
	//	`check: from: shot` names a reading with 8 numbers (shot.min, shot.max, shot.similarity,
	//	shot.mean, shot.spread, shot.sat, shot.flat_rows, shot.rows)
	//
	// A file says `of: [mean, spread]` because those are the two it means. Binding the other five
	// answered questions nobody asked and made the name of the reading mean nothing in particular.
	set := func(k string, v float64) { e.numbers[name+"."+k] = v }
	wanted := map[string]bool{}
	for _, w := range ofRaw {
		wanted[fmt.Sprint(w)] = true
	}
	bind := func(k string, v float64) {
		if wanted[k] {
			set(k, v)
		}
	}
	bind("mean", r.Mean)
	bind("spread", r.Spread)
	bind("sat", r.Sat)
	bind("flat_rows", float64(r.FlatRows))
	bind("rows", float64(r.Rows))
	bind("min", r.Min)
	bind("max", r.Max)

	// `similarity` IS A MEASUREMENT, NOT A VERDICT, AND THAT IS WHERE IT BELONGS.
	//
	// A REFERENCE IMAGE IS A QUESTION ABOUT PIXELS, so it cannot go through `check:` - which compares
	// a reading that an earlier step already made and never touches the screen. Putting it there
	// would break the one rule that makes `check:` free to re-ask.
	//
	// INSTEAD THE COMPARISON PRODUCES A NUMBER, exactly like `mean` and `spread`: how much of the
	// screen is unchanged, 0..1. A `check: {mode: number_between, range: [0.97, 1.0]}` then decides
	// it, which is the same shape every other measurement in the vocabulary has.
	//
	// MEASURED over four real CI recordings from this repository - ubuntu, windows, macos, freebsd:
	//
	//	the same image against itself                  1.0000
	//	one second later, same screen              0.9910 - 0.9947
	//	five seconds later, same screen            0.9912 - 0.9931
	//	TWO DIFFERENT SCREENS                      0.0002 - 0.0166
	//
	// So 0.97 is a floor with room on both sides; the gap is three orders of magnitude wide.
	if ref, _ := raw["reference"].(string); ref != "" {
		sim, err := e.similarityTo(s, ref, img)
		if err != nil {
			return err
		}
		set("similarity", sim)
	}

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

// `want:` WAS HERE, AND IT WAS DELETED WITH NOBODY HAVING USED IT.
//
// It was the "better" decision primitive: `{name, all|any|not}` over the numbers `see:` bound, with
// nested groups and a relation per comparison. The documentation called it "**new**", which was
// honest - MEASURED: zero walkthroughs used it, and `check:` was used 29 times.
//
// IT WAS INTRODUCED BY THE SAME IMPULSE THAT PRODUCED `requires:`, `share_mount:` and `until_console:`
// - a generalisation written before anything needed it - and it cost 265 lines of recursive
// evaluation across five functions (evalGroup, evalNot, parseOp, evalOp, lookupNumber) that nothing
// called. A vocabulary is not made better by primitives nobody reaches for.
//
// A `check:` DOES THE JOB: it compares one reading and binds one boolean, and the three modes in use
// are says, says_any and says_without.

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

// `evalTextCriteria` WAS HERE, and `checkframe.go` with it: 703 lines plus this function, reachable
// only from `want:` - which no walkthrough ever wrote. A `check:` compares one reading and binds one
// boolean, and that is the whole of what the files use.

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
	// `reference:` BINDS `similarity`, SO IT IS AN `of:` THAT NAMES ITS OWN MEASUREMENT.
	//
	//	see: {name: shot, rect: [...], reference: reference/desktop.png}
	//
	// A file that writes `reference:` is asking for one number - how much of the screen matched - and
	// making it also write `of: [similarity]` would be two ways to say it. A file that writes NEITHER
	// is still refused: a `see:` with nothing to bind is a reading nobody asked for.
	if len(ofRaw) == 0 {
		if ref, _ := raw["reference"].(string); ref != "" {
			ofRaw = []any{"similarity"}
		}
	}
	if len(ofRaw) == 0 {
		return nil, "", nil, nil, fail(s.Name, "`see:` must say `of:` - which numbers to bind. "+
			"`of: [mean, spread]` binds %s.mean and %s.spread", name, name)
	}
	return raw, name, rectRaw, ofRaw, nil
}

// similarityTo compares a frame to a reference image and returns the share of pixels that MATCH.
//
// THE REFERENCE IS FOUND THE WAY A SCRIPT IS: beside the walkthrough. `disk:` and `scp:` both look in
// the work directory first and the file's own directory second, and a reference is the same kind of
// thing - a file that belongs to the walkthrough rather than to a run.
//
// A SIZE MISMATCH IS AN ERROR RATHER THAN A CROP: two screens of different sizes are not two views of
// one screen, and comparing the shared corner would report a wrong reference as a perfect match.
func (e *Executor) similarityTo(s walk.Step, ref string, frame image.Image) (float64, error) {
	path := ref
	if !filepath.IsAbs(path) {
		beside := filepath.Join(filepath.Dir(e.File.Path), ref)
		if fileExists(beside) {
			path = beside
		}
	}
	reference, err := screen.Load(path)
	if err != nil {
		return 0, fail(s.Name, "reading the reference %q: %v. A `reference:` is a path relative "+
			"to the walkthrough, as in `reference: reference/desktop.png`", ref, err)
	}
	diff, err := screen.Compare(frame, reference, 8)
	if err != nil {
		return 0, fail(s.Name, "%v", err)
	}
	// THE SHARE THAT MATCHED, SO A BIGGER NUMBER IS BETTER - the same direction as every other
	// measurement here, where `mean` and `spread` are what they are and a check says which way is
	// good.
	return 1 - diff.Changed, nil
}
