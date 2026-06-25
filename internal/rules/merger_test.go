package rules

import (
	"reflect"
	"strings"
	"testing"
)

// makeParsed is a test helper: constructs a Parsed, omitting verbose fields.
func makeParsed(source string, kind SourceKind, s Structured, pref string) Parsed {
	return Parsed{Source: source, Kind: kind, Structured: s, Preference: pref}
}

func TestMerge_Empty(t *testing.T) {
	b := Merge(nil)
	if !b.IsEmpty() {
		t.Errorf("merge nil should be empty, got %+v", b)
	}
	if len(b.Sources) != 0 || len(b.Conflicts) != 0 {
		t.Errorf("merge nil should have no sources/conflicts, got %+v", b)
	}
}

func TestMerge_NearestWinsScalar(t *testing.T) {
	// default specifies chapter_words: 3000-6000, project specifies chapter_words: 4000-8000
	// Expectation: project takes precedence
	layers := []Parsed{
		makeParsed("default.md", SourceDefault, Structured{
			ChapterWords: &WordRange{Min: 3000, Max: 6000},
		}, ""),
		makeParsed("project.md", SourceProject, Structured{
			ChapterWords: &WordRange{Min: 4000, Max: 8000},
		}, ""),
	}
	b := Merge(layers)
	if b.Structured.ChapterWords == nil || b.Structured.ChapterWords.Min != 4000 || b.Structured.ChapterWords.Max != 8000 {
		t.Errorf("project should win, got %+v", b.Structured.ChapterWords)
	}
	// Consistency conflict should be identified
	if !hasConflict(b.Conflicts, ConflictFieldConflict, "chapter_words") {
		t.Errorf("expected field_conflict for chapter_words, got %+v", b.Conflicts)
	}
}

func TestMerge_NoConflictWhenEqual(t *testing.T) {
	// Both layers declare the same field, but with identical values -> not counted as a conflict
	layers := []Parsed{
		makeParsed("default.md", SourceDefault, Structured{
			ChapterWords:   &WordRange{Min: 3000, Max: 6000},
			ForbiddenChars: []string{"——"},
		}, ""),
		makeParsed("project.md", SourceProject, Structured{
			ChapterWords:   &WordRange{Min: 3000, Max: 6000},
			ForbiddenChars: []string{"——"},
		}, ""),
	}
	b := Merge(layers)
	for _, c := range b.Conflicts {
		if c.Kind == ConflictFieldConflict {
			t.Errorf("same value should not produce field_conflict, got %+v", c)
		}
	}
}

func TestMerge_NearestWinsList(t *testing.T) {
	// global ["——"], project ["（"], expect project to take effect, and report a conflict
	layers := []Parsed{
		makeParsed("global.md", SourceGlobal, Structured{
			ForbiddenChars: []string{"——"},
		}, ""),
		makeParsed("project.md", SourceProject, Structured{
			ForbiddenChars: []string{"（"},
		}, ""),
	}
	b := Merge(layers)
	if !reflect.DeepEqual(b.Structured.ForbiddenChars, []string{"（"}) {
		t.Errorf("expected project list, got %v", b.Structured.ForbiddenChars)
	}
	if !hasConflict(b.Conflicts, ConflictFieldConflict, "forbidden_chars") {
		t.Errorf("expected field_conflict for forbidden_chars, got %+v", b.Conflicts)
	}
}

func TestMerge_FatigueWordsMergeByKey(t *testing.T) {
	// genre fatigue {不禁:1}; project fatigue {竟然:2} -> merge by word, preventing user from losing default rules when only adding one word
	layers := []Parsed{
		makeParsed("default.md", SourceDefault, Structured{
			FatigueWords: map[string]int{"不禁": 1},
		}, ""),
		makeParsed("project.md", SourceProject, Structured{
			FatigueWords: map[string]int{"竟然": 2},
		}, ""),
	}
	b := Merge(layers)
	want := map[string]int{"不禁": 1, "竟然": 2}
	if !reflect.DeepEqual(b.Structured.FatigueWords, want) {
		t.Errorf("fatigue_words should merge by key, got %v want %v", b.Structured.FatigueWords, want)
	}
	if hasConflict(b.Conflicts, ConflictFieldConflict, "fatigue_words") {
		t.Errorf("different fatigue_words keys should not produce field-level conflict, got %+v", b.Conflicts)
	}
}

func TestMerge_FatigueWordsNearestWinsSameKey(t *testing.T) {
	// Same fatigue word declared with different thresholds in multiple sources -> nearest wins, and report conflict only for that word
	layers := []Parsed{
		makeParsed("default.md", SourceDefault, Structured{
			FatigueWords: map[string]int{"不禁": 1, "然而": 2},
		}, ""),
		makeParsed("project.md", SourceProject, Structured{
			FatigueWords: map[string]int{"不禁": 3, "其实": 1},
		}, ""),
	}
	b := Merge(layers)
	want := map[string]int{"不禁": 3, "然而": 2, "其实": 1}
	if !reflect.DeepEqual(b.Structured.FatigueWords, want) {
		t.Errorf("fatigue_words should merge with nearest value for same key, got %v want %v", b.Structured.FatigueWords, want)
	}
	if !hasConflict(b.Conflicts, ConflictFieldConflict, "fatigue_words.不禁") {
		t.Errorf("expected per-word field_conflict for 不禁, got %+v", b.Conflicts)
	}
}

func TestMerge_PreservesUntouchedFields(t *testing.T) {
	// Low priority declares field A; high priority only declares field B -> A should be preserved
	layers := []Parsed{
		makeParsed("default.md", SourceDefault, Structured{
			ForbiddenChars: []string{"——"},
		}, ""),
		makeParsed("project.md", SourceProject, Structured{
			Genre: "xianxia",
		}, ""),
	}
	b := Merge(layers)
	if b.Structured.Genre != "xianxia" {
		t.Errorf("genre missing, got %+v", b.Structured)
	}
	if !reflect.DeepEqual(b.Structured.ForbiddenChars, []string{"——"}) {
		t.Errorf("forbidden_chars from default should be preserved, got %v", b.Structured.ForbiddenChars)
	}
}

func TestMerge_MarkdownConcatenated(t *testing.T) {
	layers := []Parsed{
		makeParsed("default.md", SourceDefault, Structured{}, "default preference body"),
		makeParsed("project.md", SourceProject, Structured{}, "project preference body"),
	}
	b := Merge(layers)
	if !strings.Contains(b.Preferences, "default preference body") {
		t.Errorf("default body missing: %q", b.Preferences)
	}
	if !strings.Contains(b.Preferences, "project preference body") {
		t.Errorf("project body missing: %q", b.Preferences)
	}
	// Order: default first, project last
	di := strings.Index(b.Preferences, "default preference body")
	pi := strings.Index(b.Preferences, "project preference body")
	if di >= pi {
		t.Errorf("default body should appear before project body; default@%d project@%d", di, pi)
	}
	// Source header
	if !strings.Contains(b.Preferences, "[default] default.md") {
		t.Errorf("source header for default missing: %q", b.Preferences)
	}
	if !strings.Contains(b.Preferences, "[project] project.md") {
		t.Errorf("source header for project missing: %q", b.Preferences)
	}
}

func TestMerge_SkipsEmptyBody(t *testing.T) {
	layers := []Parsed{
		makeParsed("default.md", SourceDefault, Structured{}, "   "),
		makeParsed("project.md", SourceProject, Structured{}, "project body"),
	}
	b := Merge(layers)
	if strings.Contains(b.Preferences, "[default]") {
		t.Errorf("empty body should not emit source header, got %q", b.Preferences)
	}
	if !strings.Contains(b.Preferences, "project body") {
		t.Errorf("project body missing: %q", b.Preferences)
	}
}

func TestMerge_PropagatesParsedConflicts(t *testing.T) {
	// If a single file already has parse-time conflicts (e.g. unknown field), the merger should aggregate them as-is
	parsed := Parsed{
		Source: "project.md",
		Kind:   SourceProject,
		Conflicts: []Conflict{{
			Source: "project.md",
			Kind:   ConflictUnknownField,
			Field:  "secret_x",
			Detail: "Unknown",
		}},
	}
	b := Merge([]Parsed{parsed})
	if !hasConflict(b.Conflicts, ConflictUnknownField, "secret_x") {
		t.Errorf("expected ConflictUnknownField for secret_x, got %+v", b.Conflicts)
	}
}

func TestMerge_AllSourcesInList(t *testing.T) {
	layers := []Parsed{
		makeParsed("default.md", SourceDefault, Structured{}, ""),
		makeParsed("global.md", SourceGlobal, Structured{}, ""),
		makeParsed("project.md", SourceProject, Structured{}, ""),
	}
	b := Merge(layers)
	want := []string{"default.md", "global.md", "project.md"}
	if !reflect.DeepEqual(b.Sources, want) {
		t.Errorf("sources=%v, want %v", b.Sources, want)
	}
}

// hasConflict checks if there is an entry with the specified (Kind, Field) in conflicts.
func hasConflict(conflicts []Conflict, kind ConflictKind, field string) bool {
	for _, c := range conflicts {
		if c.Kind == kind && c.Field == field {
			return true
		}
	}
	return false
}
