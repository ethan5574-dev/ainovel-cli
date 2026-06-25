package rules

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Check performs a mechanical check on the chapter text based on structured rules, returning a list of violations.
//
// Design Contract:
//   - Only return facts, do not issue commands (Iron Rule #1)
//   - Do not block any caller flow
//   - severity maps fixedly by rule type (see the comment table in types.go)
//
// Parameters:
//   - text: chapter text (can be final draft or draft)
//   - wordCount: chapter word count (rune count). If < 0, computed by checker itself to avoid repetitive O(n) scans by the caller.
//   - s: merged structured rules; returns nil immediately if IsEmpty.
func Check(text string, wordCount int, s Structured) []Violation {
	if s.IsEmpty() {
		return nil
	}
	if wordCount < 0 {
		wordCount = utf8.RuneCountInString(text)
	}

	var violations []Violation
	violations = appendForbiddenChars(violations, text, s.ForbiddenChars)
	violations = appendForbiddenPhrases(violations, text, s.ForbiddenPhrases)
	violations = appendFatigueWords(violations, text, s.FatigueWords)
	violations = appendChapterWords(violations, wordCount, s.ChapterWords)
	return violations
}

// forbidden_chars: error if appears >= 1 time.
// A single rule produces only one violation, and actual is the occurrence count.
func appendForbiddenChars(vs []Violation, text string, list []string) []Violation {
	for _, ch := range list {
		if ch == "" {
			continue
		}
		n := strings.Count(text, ch)
		if n == 0 {
			continue
		}
		vs = append(vs, Violation{
			Rule:     "forbidden_chars",
			Target:   ch,
			Actual:   n,
			Severity: SeverityError,
		})
	}
	return vs
}

// forbidden_phrases: error if appears >= 1 time; behavior is identical to forbidden_chars, only differentiated by rule name.
func appendForbiddenPhrases(vs []Violation, text string, list []string) []Violation {
	for _, ph := range list {
		if ph == "" {
			continue
		}
		n := strings.Count(text, ph)
		if n == 0 {
			continue
		}
		vs = append(vs, Violation{
			Rule:     "forbidden_phrases",
			Target:   ph,
			Actual:   n,
			Severity: SeverityError,
		})
	}
	return vs
}

// fatigue_words: violation occurs only when occurrences in this chapter exceed the threshold, warning level.
// No cross-chapter accumulation - cross-chapter issues are delegated to diagnostics later.
func appendFatigueWords(vs []Violation, text string, m map[string]int) []Violation {
	for word, limit := range m {
		if word == "" || limit <= 0 {
			continue
		}
		n := strings.Count(text, word)
		if n <= limit {
			continue
		}
		vs = append(vs, Violation{
			Rule:     "fatigue_words",
			Target:   word,
			Limit:    limit,
			Actual:   n,
			Severity: SeverityWarning,
		})
	}
	return vs
}

// chapter_words: word count deviation.
// deviation < 20%: warning; deviation >= 20%: error.
// Deviation formula: below min uses (min-actual)/min; above max uses (actual-max)/max.
func appendChapterWords(vs []Violation, wordCount int, rng *WordRange) []Violation {
	if rng == nil {
		return vs
	}
	var deviation float64
	switch {
	case wordCount < rng.Min:
		if rng.Min == 0 {
			return vs
		}
		deviation = float64(rng.Min-wordCount) / float64(rng.Min)
	case wordCount > rng.Max:
		if rng.Max == 0 {
			return vs
		}
		deviation = float64(wordCount-rng.Max) / float64(rng.Max)
	default:
		return vs // within range
	}

	severity := SeverityWarning
	if deviation >= ChapterWordsDeviationThreshold {
		severity = SeverityError
	}
	vs = append(vs, Violation{
		Rule:      "chapter_words",
		Limit:     fmt.Sprintf("%d-%d", rng.Min, rng.Max),
		Actual:    wordCount,
		Deviation: deviation,
		Severity:  severity,
	})
	return vs
}
