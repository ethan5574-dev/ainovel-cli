package rules

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// findViolation finds the first violation by rule and target in the results.
func findViolation(vs []Violation, rule, target string) *Violation {
	for i := range vs {
		if vs[i].Rule == rule && vs[i].Target == target {
			return &vs[i]
		}
	}
	return nil
}

func TestCheck_EmptyStructured(t *testing.T) {
	vs := Check("any content", -1, Structured{})
	if vs != nil {
		t.Errorf("empty structured should return nil, got %+v", vs)
	}
}

func TestCheck_ForbiddenChars(t *testing.T) {
	text := "He smiled -- sighed again -- and left."
	vs := Check(text, -1, Structured{
		ForbiddenChars: []string{"--"},
	})
	v := findViolation(vs, "forbidden_chars", "--")
	if v == nil {
		t.Fatal("expected forbidden_chars violation")
	}
	if v.Severity != SeverityError {
		t.Errorf("severity=%s, want error", v.Severity)
	}
	if v.Actual != 2 {
		t.Errorf("actual=%v, want 2", v.Actual)
	}
}

func TestCheck_ForbiddenCharsNotPresent(t *testing.T) {
	vs := Check("ordinary text no violation", -1, Structured{
		ForbiddenChars: []string{"--"},
	})
	if len(vs) != 0 {
		t.Errorf("expected no violations, got %+v", vs)
	}
}

func TestCheck_ForbiddenPhrases(t *testing.T) {
	text := "Not... but rather the truth has been hidden. We discuss core motives here."
	vs := Check(text, -1, Structured{
		ForbiddenPhrases: []string{"Not... but rather", "core motives"},
	})
	if len(vs) != 2 {
		t.Errorf("expected 2 violations, got %d: %+v", len(vs), vs)
	}
	for _, v := range vs {
		if v.Severity != SeverityError {
			t.Errorf("severity=%s, want error", v.Severity)
		}
	}
}

func TestCheck_FatigueWordsUnderLimit(t *testing.T) {
	text := "He could not help but smile."
	vs := Check(text, -1, Structured{
		FatigueWords: map[string]int{"cannot help": 1},
	})
	if len(vs) != 0 {
		t.Errorf("under limit should not violate, got %+v", vs)
	}
}

func TestCheck_FatigueWordsAtLimit(t *testing.T) {
	// limit=1, actual=1 -> no violation
	text := "He could not help but smile."
	vs := Check(text, -1, Structured{
		FatigueWords: map[string]int{"cannot help": 1},
	})
	if len(vs) != 0 {
		t.Errorf("at limit should not violate (limit 1 actual 1), got %+v", vs)
	}
}

func TestCheck_FatigueWordsOverLimit(t *testing.T) {
	// limit=1, actual=3 -> warning
	text := "He cannot help but smile, cannot help but frown, and finally cannot help but leave."
	vs := Check(text, -1, Structured{
		FatigueWords: map[string]int{"cannot help": 1},
	})
	v := findViolation(vs, "fatigue_words", "cannot help")
	if v == nil {
		t.Fatal("expected fatigue_words violation")
	}
	if v.Severity != SeverityWarning {
		t.Errorf("severity=%s, want warning", v.Severity)
	}
	if v.Limit != 1 {
		t.Errorf("limit=%v, want 1", v.Limit)
	}
	if v.Actual != 3 {
		t.Errorf("actual=%v, want 3", v.Actual)
	}
}

// Word count boundary tests
// Range 3000-6000:
//   actual 3000 -> in range -> no violation
//   actual 2999 -> deviation ≈ 0.033% -> warning
//   actual 2401 -> deviation = 599/3000 ≈ 19.97% -> warning
//   actual 2400 -> deviation = 600/3000 = 20% -> error (>= threshold)
//   actual 6001 -> deviation ≈ 0.017% -> warning
//   actual 7199 -> deviation ≈ 19.98% -> warning
//   actual 7200 -> deviation = 1200/6000 = 20% -> error

func TestCheck_ChapterWordsInRange(t *testing.T) {
	rng := &WordRange{Min: 3000, Max: 6000}
	vs := Check("", 4000, Structured{ChapterWords: rng})
	if len(vs) != 0 {
		t.Errorf("in range should yield no violation, got %+v", vs)
	}
	// Boundary values
	vs = Check("", 3000, Structured{ChapterWords: rng})
	if len(vs) != 0 {
		t.Errorf("at min should be in range, got %+v", vs)
	}
	vs = Check("", 6000, Structured{ChapterWords: rng})
	if len(vs) != 0 {
		t.Errorf("at max should be in range, got %+v", vs)
	}
}

func TestCheck_ChapterWordsSlightlyBelow(t *testing.T) {
	// actual 2401 → deviation = 599/3000 = 0.1996... < 20% → warning
	rng := &WordRange{Min: 3000, Max: 6000}
	vs := Check("", 2401, Structured{ChapterWords: rng})
	if len(vs) != 1 || vs[0].Rule != "chapter_words" {
		t.Fatalf("expected 1 chapter_words violation, got %+v", vs)
	}
	if vs[0].Severity != SeverityWarning {
		t.Errorf("severity=%s, want warning at <20%%", vs[0].Severity)
	}
	if vs[0].Deviation >= ChapterWordsDeviationThreshold {
		t.Errorf("deviation=%f should be < %f", vs[0].Deviation, ChapterWordsDeviationThreshold)
	}
}

func TestCheck_ChapterWordsAtThreshold(t *testing.T) {
	// actual 2400 → deviation = 600/3000 = 0.2 == 20% → error（>= threshold）
	rng := &WordRange{Min: 3000, Max: 6000}
	vs := Check("", 2400, Structured{ChapterWords: rng})
	if len(vs) != 1 || vs[0].Severity != SeverityError {
		t.Errorf("expected error at 20%% threshold, got %+v", vs)
	}
}

func TestCheck_ChapterWordsAboveMax(t *testing.T) {
	// actual 7200 → deviation = 1200/6000 = 0.2 == 20% → error
	rng := &WordRange{Min: 3000, Max: 6000}
	vs := Check("", 7200, Structured{ChapterWords: rng})
	if len(vs) != 1 || vs[0].Severity != SeverityError {
		t.Errorf("expected error at 20%% above max, got %+v", vs)
	}
	if vs[0].Actual != 7200 {
		t.Errorf("actual=%v, want 7200", vs[0].Actual)
	}
}

func TestCheck_ChapterWordsSlightlyAbove(t *testing.T) {
	// actual 7199 → deviation = 1199/6000 ≈ 0.19983 < 20% → warning
	rng := &WordRange{Min: 3000, Max: 6000}
	vs := Check("", 7199, Structured{ChapterWords: rng})
	if len(vs) != 1 || vs[0].Severity != SeverityWarning {
		t.Errorf("expected warning slightly above max, got %+v", vs)
	}
}

func TestCheck_AutoWordCount(t *testing.T) {
	// When wordCount = -1, computed by checker itself
	text := strings.Repeat("A", 2500) // 2500 characters
	rng := &WordRange{Min: 3000, Max: 6000}
	vs := Check(text, -1, Structured{ChapterWords: rng})
	if len(vs) != 1 || vs[0].Rule != "chapter_words" {
		t.Fatalf("expected 1 chapter_words violation, got %+v", vs)
	}
	if vs[0].Actual != 2500 {
		t.Errorf("auto wordCount=%v, want 2500", vs[0].Actual)
	}
	if vs[0].Actual != utf8.RuneCountInString(text) {
		t.Errorf("auto count mismatch: %v vs rune count %d", vs[0].Actual, utf8.RuneCountInString(text))
	}
}

func TestCheck_MultipleRulesAtOnce(t *testing.T) {
	text := "He cannot help -- and cannot help -- leaving."
	rng := &WordRange{Min: 3000, Max: 6000}
	s := Structured{
		ChapterWords:   rng,
		ForbiddenChars: []string{"--"},
		FatigueWords:   map[string]int{"cannot help": 1},
	}
	vs := Check(text, 10, s)

	// Should trigger three categories simultaneously: forbidden_chars + fatigue_words + chapter_words
	rules := map[string]bool{}
	for _, v := range vs {
		rules[v.Rule] = true
	}
	if !rules["forbidden_chars"] || !rules["fatigue_words"] || !rules["chapter_words"] {
		t.Errorf("expected all three rules triggered, got %+v", rules)
	}
}

func TestCheck_FatigueZeroLimitSkipped(t *testing.T) {
	// limit=0 is an invalid value, should skip the entire rule (parser will filter as well, defensive check here)
	text := "cannot help cannot help cannot help"
	vs := Check(text, -1, Structured{
		FatigueWords: map[string]int{"cannot help": 0},
	})
	if len(vs) != 0 {
		t.Errorf("limit=0 should be skipped, got %+v", vs)
	}
}

func TestCheck_EmptyTargetsSkipped(t *testing.T) {
	// Empty string target should not lead to a false positive
	vs := Check("any text", -1, Structured{
		ForbiddenChars:   []string{""},
		ForbiddenPhrases: []string{""},
		FatigueWords:     map[string]int{"": 1},
	})
	if len(vs) != 0 {
		t.Errorf("empty targets should be skipped, got %+v", vs)
	}
}
