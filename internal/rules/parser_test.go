package rules

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// readFixture reads fixture files under testdata; calls t.Fatal if not found.
func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("testdata", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func TestParse_Basic(t *testing.T) {
	data := readFixture(t, "basic.rules.md")
	p := Parse("testdata/basic.rules.md", SourceProject, data)

	if p.Structured.Genre != "xianxia" {
		t.Errorf("genre=%q, want xianxia", p.Structured.Genre)
	}
	if p.Structured.ChapterWords == nil || p.Structured.ChapterWords.Min != 3000 || p.Structured.ChapterWords.Max != 6000 {
		t.Errorf("chapter_words=%+v, want {3000,6000}", p.Structured.ChapterWords)
	}
	wantChars := []string{"——", "（"}
	if !reflect.DeepEqual(p.Structured.ForbiddenChars, wantChars) {
		t.Errorf("forbidden_chars=%v, want %v", p.Structured.ForbiddenChars, wantChars)
	}
	wantPhrases := []string{"not...but", "core motivation"}
	if !reflect.DeepEqual(p.Structured.ForbiddenPhrases, wantPhrases) {
		t.Errorf("forbidden_phrases=%v, want %v", p.Structured.ForbiddenPhrases, wantPhrases)
	}
	wantFatigue := map[string]int{"suddenly": 1, "unexpectedly": 1, "seemingly": 2}
	if !reflect.DeepEqual(p.Structured.FatigueWords, wantFatigue) {
		t.Errorf("fatigue_words=%v, want %v", p.Structured.FatigueWords, wantFatigue)
	}
	if !strings.Contains(p.Preference, "# Style") {
		t.Errorf("preference missing markdown body, got %q", p.Preference)
	}
	if len(p.Conflicts) != 0 {
		t.Errorf("expected no conflicts, got %d: %+v", len(p.Conflicts), p.Conflicts)
	}
}

func TestParse_InvalidFrontMatter(t *testing.T) {
	data := readFixture(t, "invalid-frontmatter.rules.md")
	p := Parse("testdata/invalid-frontmatter.rules.md", SourceProject, data)

	// Fault tolerance: structured fields are empty, but the body is still used as preference
	if !p.Structured.IsEmpty() {
		t.Errorf("structured should be empty on parse_error, got %+v", p.Structured)
	}
	if !strings.Contains(p.Preference, "Although front matter parsing failed, the body should still be injected as preference.") {
		t.Errorf("preference should still be parsed despite front matter failure; got %q", p.Preference)
	}
	if len(p.Conflicts) == 0 {
		t.Fatal("expected at least one conflict for parse error")
	}
	found := false
	for _, c := range p.Conflicts {
		if c.Kind == ConflictParseError {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected ConflictParseError, got %+v", p.Conflicts)
	}
}

func TestParse_UnknownFields(t *testing.T) {
	data := readFixture(t, "unknown-fields.rules.md")
	p := Parse("testdata/unknown-fields.rules.md", SourceProject, data)

	// Known fields should be loaded normally
	if p.Structured.Genre != "xianxia" {
		t.Errorf("genre=%q, want xianxia", p.Structured.Genre)
	}
	if p.Structured.ChapterWords == nil || p.Structured.ChapterWords.Min != 2000 {
		t.Errorf("chapter_words=%+v, want {2000,4000}", p.Structured.ChapterWords)
	}
	// Unknown fields should go into conflicts
	unknowns := map[string]bool{}
	for _, c := range p.Conflicts {
		if c.Kind == ConflictUnknownField {
			unknowns[c.Field] = true
		}
	}
	if !unknowns["forbidden_emojis"] || !unknowns["secret_field"] {
		t.Errorf("expected forbidden_emojis & secret_field in conflicts, got %+v", p.Conflicts)
	}
}

func TestParse_TypeErrors(t *testing.T) {
	data := readFixture(t, "type-errors.rules.md")
	p := Parse("testdata/type-errors.rules.md", SourceProject, data)

	// Strict policy (Debug-First): all fields with incorrect types discard the field and write to conflicts, no "guessing for the user".

	// genre: 42 is an int, strictly judged as type_error -> discarded
	if p.Structured.Genre != "" {
		t.Errorf("genre should be discarded on type error (int instead of string), got %q", p.Structured.Genre)
	}

	// chapter_words: "not-a-range" -> invalid_value
	if p.Structured.ChapterWords != nil {
		t.Errorf("chapter_words should be nil on invalid_value, got %+v", p.Structured.ChapterWords)
	}

	// forbidden_chars: "should-be-list" -> type_error (top-level)
	if len(p.Structured.ForbiddenChars) != 0 {
		t.Errorf("forbidden_chars should be empty on type_error, got %v", p.Structured.ForbiddenChars)
	}

	// forbidden_phrases: [1, 2] -> each element is an int, strictly judged -> all discarded -> empty list
	if len(p.Structured.ForbiddenPhrases) != 0 {
		t.Errorf("forbidden_phrases should be empty on element type errors, got %v", p.Structured.ForbiddenPhrases)
	}

	// fatigue_words: true -> neither map nor list -> type_error whole discard
	if len(p.Structured.FatigueWords) != 0 {
		t.Errorf("fatigue_words should be empty on type_error, got %+v", p.Structured.FatigueWords)
	}

	// All error fields should go into conflicts
	fields := map[string]bool{}
	for _, c := range p.Conflicts {
		fields[c.Field] = true
	}
	expected := []string{"genre", "chapter_words", "forbidden_chars", "fatigue_words"}
	for _, f := range expected {
		if !fields[f] {
			t.Errorf("expected conflict for %s, got fields=%v conflicts=%+v", f, fields, p.Conflicts)
		}
	}
	// Element-level conflicts for forbidden_phrases, field names are forbidden_phrases[0]/[1]
	hasPhrasesElement := false
	for _, c := range p.Conflicts {
		if strings.HasPrefix(c.Field, "forbidden_phrases") {
			hasPhrasesElement = true
		}
	}
	if !hasPhrasesElement {
		t.Errorf("expected per-element conflict on forbidden_phrases, got %+v", p.Conflicts)
	}

	// Body should still be injected
	if !strings.Contains(p.Preference, "Type Errors") {
		t.Errorf("preference should be parsed despite type errors; got %q", p.Preference)
	}
}

// TestParse_FatigueWordsPartialInvalid verifies: when some key thresholds in fatigue_words map are invalid,
// each invalid key writes a conflict, while valid keys are preserved normally.
func TestParse_FatigueWordsPartialInvalid(t *testing.T) {
	content := []byte("---\nfatigue_words:\n" +
		"  normal: 2\n" +
		"  zero_threshold: 0\n" +
		"  negative_threshold: -1\n" +
		"  non_int: \"abc\"\n" +
		"---\n")
	p := Parse("inline", SourceProject, content)

	if v, ok := p.Structured.FatigueWords["normal"]; !ok || v != 2 {
		t.Errorf("legitimate key should be kept, got %v", p.Structured.FatigueWords)
	}
	if _, ok := p.Structured.FatigueWords["zero_threshold"]; ok {
		t.Errorf("zero threshold should be dropped")
	}
	if _, ok := p.Structured.FatigueWords["negative_threshold"]; ok {
		t.Errorf("negative threshold should be dropped")
	}
	if _, ok := p.Structured.FatigueWords["non_int"]; ok {
		t.Errorf("non-int threshold should be dropped")
	}

	// each invalid key should have a separate conflict
	keys := map[string]bool{}
	for _, c := range p.Conflicts {
		keys[c.Field] = true
	}
	for _, key := range []string{"fatigue_words.zero_threshold", "fatigue_words.negative_threshold", "fatigue_words.non_int"} {
		if !keys[key] {
			t.Errorf("expected conflict on %s, got fields=%v", key, keys)
		}
	}
}

func TestParse_Empty(t *testing.T) {
	p := Parse("testdata/empty.rules.md", SourceProject, []byte{})
	if !p.Structured.IsEmpty() {
		t.Errorf("empty file should yield empty structured, got %+v", p.Structured)
	}
	if p.Preference != "" {
		t.Errorf("empty file should yield empty preference, got %q", p.Preference)
	}
	if len(p.Conflicts) != 0 {
		t.Errorf("empty file should yield no conflicts, got %+v", p.Conflicts)
	}
}

func TestParse_FatigueAsList(t *testing.T) {
	data := readFixture(t, "fatigue-as-list.rules.md")
	p := Parse("testdata/fatigue-as-list.rules.md", SourceProject, data)

	want := map[string]int{"suddenly": 1, "unexpectedly": 1, "seemingly": 1}
	if !reflect.DeepEqual(p.Structured.FatigueWords, want) {
		t.Errorf("fatigue_words=%v, want %v", p.Structured.FatigueWords, want)
	}
	if len(p.Conflicts) != 0 {
		t.Errorf("list form should be accepted without conflict, got %+v", p.Conflicts)
	}
}

func TestParse_NoFrontMatter(t *testing.T) {
	data := readFixture(t, "no-frontmatter.rules.md")
	p := Parse("testdata/no-frontmatter.rules.md", SourceProject, data)

	if !p.Structured.IsEmpty() {
		t.Errorf("no front matter, structured should be empty, got %+v", p.Structured)
	}
	if !strings.Contains(p.Preference, "Body Only") {
		t.Errorf("preference should contain body, got %q", p.Preference)
	}
	if len(p.Conflicts) != 0 {
		t.Errorf("no front matter, no conflicts expected, got %+v", p.Conflicts)
	}
}

// TestParse_ChapterWordsObjectForm verifies chapter_words is compatible with map form {min, max} (except strings).
func TestParse_ChapterWordsObjectForm(t *testing.T) {
	content := []byte("---\nchapter_words:\n  min: 2500\n  max: 5500\n---\n")
	p := Parse("inline", SourceProject, content)
	if p.Structured.ChapterWords == nil {
		t.Fatal("expected ChapterWords to be set")
	}
	if p.Structured.ChapterWords.Min != 2500 || p.Structured.ChapterWords.Max != 5500 {
		t.Errorf("got %+v, want {2500, 5500}", p.Structured.ChapterWords)
	}
}

// TestParse_ChapterWordsSingleValue verifies single value notation (bare number / string) expands to a ±20% interval.
// Anti-regression issue #41: user writes a single value by intuition, which in the past was silently discarded and fell back to built-in default.
func TestParse_ChapterWordsSingleValue(t *testing.T) {
	cases := []struct {
		name             string
		content          string
		wantMin, wantMax int
	}{
		{"bare int", "---\nchapter_words: 2500\n---\n", 2000, 3000},
		{"quoted string", "---\nchapter_words: \"2500\"\n---\n", 2000, 3000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := Parse("inline", SourceProject, []byte(tc.content))
			if p.Structured.ChapterWords == nil {
				t.Fatalf("expected ChapterWords to be set, conflicts=%+v", p.Conflicts)
			}
			if p.Structured.ChapterWords.Min != tc.wantMin || p.Structured.ChapterWords.Max != tc.wantMax {
				t.Errorf("got %+v, want {%d, %d}", p.Structured.ChapterWords, tc.wantMin, tc.wantMax)
			}
		})
	}
}

// TestParse_ChapterWordsInvalidRange confirms min>max is treated as an invalid value.
func TestParse_ChapterWordsInvalidRange(t *testing.T) {
	content := []byte("---\nchapter_words: 6000-3000\n---\n")
	p := Parse("inline", SourceProject, content)
	if p.Structured.ChapterWords != nil {
		t.Errorf("min>max should be rejected, got %+v", p.Structured.ChapterWords)
	}
	found := false
	for _, c := range p.Conflicts {
		if c.Kind == ConflictInvalidValue && c.Field == "chapter_words" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected ConflictInvalidValue for chapter_words, got %+v", p.Conflicts)
	}
}

// TestParse_FrontMatterUnclosed confirms that when the starting --- is not closed, the entire document is treated as body.
func TestParse_FrontMatterUnclosed(t *testing.T) {
	content := []byte("---\ngenre: xianxia\n# unclosed --- \n\n# preference\n\n- content\n")
	p := Parse("inline", SourceProject, content)
	if !p.Structured.IsEmpty() {
		t.Errorf("unclosed front matter, structured should be empty, got %+v", p.Structured)
	}
	if !strings.Contains(p.Preference, "genre: xianxia") {
		t.Errorf("unclosed: whole content should be body, got %q", p.Preference)
	}
}
