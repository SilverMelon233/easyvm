package run

import "testing"

// THE THREE TEXT COMPARISONS ANSWER THREE DIFFERENT QUESTIONS, AND EVERY NUMBER HERE IS MEASURED.
//
// The pairs below are the misreads this repository's logs actually record, plus the pairs that must
// NEVER pass. The test exists because the unit is the whole design: an earlier version used a RATIO
// for `fuzzy:`, and a ratio refuses `dsk` for `disk` (0.750) - so a pane's own button label would
// not match itself after one dropped glyph.
func TestTheThreeTextComparisonsAnswerThreeQuestions(t *testing.T) {
	read := []string{"Erase", "disk", "and", "install", "Ubuntu"}

	// A. DOES THE REGION CONTAIN THESE WORDS? Exact, case and punctuation folded.
	if !allWords(read, []string{"erase", "disk"}) {
		t.Fatal("`contains: [erase, disk]` must pass on 'Erase disk and install Ubuntu' - that is " +
			"the question `contains:` asks, and it is the one the case in the design note is about")
	}
	if allWords(read, []string{"manual", "partitioning"}) {
		t.Fatal("`contains:` must fail when the words are absent")
	}

	// B. IS THE REGION THIS SENTENCE? A ratio over the whole region, which is right for a phrase.
	phrase := "root partition will be ext4"
	misread := "root partiton will be ext4"
	if r := levenshteinRatio(phrase, misread); r < 0.9 {
		t.Fatalf("a phrase with one dropped glyph must score >= 0.9, got %.3f", r)
	}
	if r := levenshteinRatio("erase disk", "Erase disk and install Ubuntu"); r > 0.5 {
		t.Fatalf("`like: \"erase disk\"` must NOT match a LONGER region - that is `contains:`, "+
			"not `like:`; got ratio %.3f", r)
	}

	// C. DOES THE REGION CONTAIN THESE WORDS, GIVEN OCR NOISE? Edit distance per word.
	one := []string{"dsk", "ext", "instal", "partiton", "Wpgrade", "insall"}
	want := []string{"disk", "ext4", "install", "partition", "upgrade", "install"}
	for i := range one {
		if !hasWordWithin(one, want[i], 1) {
			t.Fatalf("%q must match %q at edit distance 1 - it is ONE glyph, and a ratio would "+
				"refuse it because these words are short", want[i], one[i])
		}
	}
}

// AND THE PAIRS THAT MUST NEVER PASS, because a tolerance that swallows the opposite page is worse
// than no tolerance at all.
func TestFuzzyNeverSwallowsTheOppositeWord(t *testing.T) {
	// `uninstall` for `install` is distance 2. At `fuzzy: 1` it must be refused - and that is why
	// the cap on `fuzzy:` is what it is.
	if hasWordWithin([]string{"uninstall"}, "install", 1) {
		t.Fatal("`uninstall` must NOT match `install` at edit distance 1: it is a different page")
	}
	// `install` -> `uninstall` is 2 edits (drop nothing, add `un`), so distance 1 is safe and
	// distance 2 is where it starts to be ambiguous.
	if !hasWordWithin([]string{"uninstall"}, "install", 2) {
		t.Fatal("distance 2 is expected to accept this; the CAP is what keeps the default safe, " +
			"not the distance arithmetic")
	}
	// A short word against an unrelated one is a large distance, so the budget cannot rescue it.
	if hasWordWithin([]string{"uninstall"}, "disk", 2) {
		t.Fatal("`disk` must not match `uninstall` at any accepted distance")
	}
	// And a substring is not a word: this is why `hasWord` compares whole normalised words rather
	// than `strings.Contains`.
	if hasWord([]string{"uninstall"}, "install") {
		t.Fatal("`hasWord` must compare WHOLE words; `install` is not a word in `uninstall`")
	}
}

// THE DEFAULT IS EXACT, and a file gets tolerance only by asking for it with a number.
func TestFuzzyIsOffUnlessTheFileAsksForIt(t *testing.T) {
	if hasWordWithin([]string{"dsk"}, "disk", 0) {
		t.Fatal("with no `fuzzy:`, matching must be exact - `dsk` must not pass for `disk`")
	}
	if !hasWordWithin([]string{"disk"}, "disk", 0) {
		t.Fatal("exact matching must still match an exact word")
	}
}

// A RATIO'S BLIND SPOT, recorded so nobody re-derives it: at the whole-FRAME scale a ratio cannot
// separate "one glyph misread" from "the opposite word".
func TestARatioCannotBeAPageTest(t *testing.T) {
	// `welcome` came back from tesseract as `Wesn Home` on a real pane, so a CORRECT match scores
	// far BELOW a plausible floor.
	correct := levenshteinRatio("welcome", "Wesn Home")
	if correct > 0.7 {
		t.Fatalf("the recorded misread scores %.3f; if this rises above 0.7 the comment in "+
			"levenshteinRatio is stale", correct)
	}
	// AND THE DANGEROUS DIRECTION: the OPPOSITE word scores HIGH - 0.778, which is closer to 1 than
	// to 0 and would pass almost any floor a real misread needs. That is why a page-level ratio
	// cannot be used, and why `fuzzy:` is an edit distance per word.
	opposite := levenshteinRatio("install", "uninstall")
	if opposite < 0.7 {
		t.Fatalf("the recorded number says the opposite word scores %.3f; if this DROPS below "+
			"0.7 then a ratio would be safe and the design note is stale", opposite)
	}
	if correct >= opposite {
		t.Fatalf("a correct-but-mangled match (%.3f) must score BELOW the opposite word (%.3f) "+
			"for the recorded failure to make sense", correct, opposite)
	}
	// Normalisation makes case-only differences identical, which is why case never needs tolerance.
	if r := levenshteinRatio("next", "Next"); r != 1 {
		t.Fatalf("normalisation must make `next` and `Next` identical, got %.3f", r)
	}
}

// NORMALISATION IS THE FIRST HALF OF EVERY COMPARISON, so case and spacing never need a tolerance.
func TestNormalisationFoldsCasePunctuationAndSpace(t *testing.T) {
	pairs := [][2]string{
		{"Erase Disk", "erase disk"},
		{"/dev/vda2  ext4", "/dev/vda2 ext4"},
		{"Install Now.", "install now"},
	}
	for _, p := range pairs {
		if normaliseText(p[0]) != normaliseText(p[1]) {
			t.Fatalf("%q and %q must normalise identically, got %q and %q",
				p[0], p[1], normaliseText(p[0]), normaliseText(p[1]))
		}
		if r := levenshteinRatio(p[0], p[1]); r != 1 {
			t.Fatalf("%q vs %q must be ratio 1.0 after normalisation, got %.3f", p[0], p[1], r)
		}
	}
}
