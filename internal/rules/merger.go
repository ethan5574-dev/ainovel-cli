package rules

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Merge merges multiple sources returned by loader into a final Bundle.
//
// Merge rules:
//   - Regular structured fields: nearest priority (the latter overwrites the former), write field_conflict if multiple sources declare the same field and values mismatch
//   - fatigue_words: merged by word; when the same word is declared in multiple sources and thresholds mismatch, nearest priority is used and field_conflict is written
//   - Markdown body: concatenated in order of source, adding a source header for each block, without overwriting
//   - sources: all successfully loaded file paths
//   - conflicts: parsing conflicts + merge-time field_conflict
//
// Input parameter layers should already be sorted by SourceKind in ascending order (output format of loader.Load).
func Merge(layers []Parsed) Bundle {
	bundle := Bundle{
		Structured:  Structured{},
		Preferences: "",
		Sources:     make([]string, 0, len(layers)),
		Conflicts:   nil,
	}

	// Phase A: collect all declaration sources for each field, facilitating subsequent conflict decisions
	declarations := map[string][]Parsed{}
	declare := func(field string, p Parsed) {
		declarations[field] = append(declarations[field], p)
	}
	for _, p := range layers {
		if p.Structured.Genre != "" {
			declare("genre", p)
		}
		if p.Structured.ChapterWords != nil {
			declare("chapter_words", p)
		}
		if len(p.Structured.ForbiddenChars) > 0 {
			declare("forbidden_chars", p)
		}
		if len(p.Structured.ForbiddenPhrases) > 0 {
			declare("forbidden_phrases", p)
		}
		if len(p.Structured.FatigueWords) > 0 {
			declare("fatigue_words", p)
		}
	}

	// Phase B: merge structured fields to get the final structured fields.
	// Scalar/list fields are overwritten by proximity; fatigue_words is a map merged by word, making it easy for users to add only a small number of fatigue words.
	for _, p := range layers {
		if p.Structured.Genre != "" {
			bundle.Structured.Genre = p.Structured.Genre
		}
		if p.Structured.ChapterWords != nil {
			bundle.Structured.ChapterWords = p.Structured.ChapterWords
		}
		if len(p.Structured.ForbiddenChars) > 0 {
			bundle.Structured.ForbiddenChars = p.Structured.ForbiddenChars
		}
		if len(p.Structured.ForbiddenPhrases) > 0 {
			bundle.Structured.ForbiddenPhrases = p.Structured.ForbiddenPhrases
		}
		if len(p.Structured.FatigueWords) > 0 {
			bundle.Structured.FatigueWords = mergeFatigueWords(bundle.Structured.FatigueWords, p.Structured.FatigueWords)
		}
	}

	// Phase C: construct field_conflict (counts as conflict only when multiple sources exist and values mismatch)
	for field, sources := range declarations {
		if len(sources) < 2 {
			continue
		}
		if field == "fatigue_words" {
			bundle.Conflicts = append(bundle.Conflicts, fatigueWordConflicts(sources)...)
			continue
		}
		if allEqual(field, sources) {
			continue
		}
		bundle.Conflicts = append(bundle.Conflicts, Conflict{
			Source: sources[len(sources)-1].Source,
			Kind:   ConflictFieldConflict,
			Field:  field,
			Detail: describeFieldConflict(field, sources),
		})
	}

	// Phase D: merge Markdown preference body
	var sb strings.Builder
	for _, p := range layers {
		if strings.TrimSpace(p.Preference) == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		fmt.Fprintf(&sb, "## [%s] %s\n\n", p.Kind, p.Source)
		sb.WriteString(p.Preference)
	}
	bundle.Preferences = sb.String()

	// Phase E: aggregate sources and parsing conflicts
	for _, p := range layers {
		bundle.Sources = append(bundle.Sources, p.Source)
		bundle.Conflicts = append(bundle.Conflicts, p.Conflicts...)
	}

	return bundle
}

func mergeFatigueWords(dst, src map[string]int) map[string]int {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = make(map[string]int, len(src))
	}
	for word, limit := range src {
		dst[word] = limit
	}
	return dst
}

func fatigueWordConflicts(sources []Parsed) []Conflict {
	type declaration struct {
		source string
		limit  int
	}
	byWord := make(map[string][]declaration)
	for _, p := range sources {
		for word, limit := range p.Structured.FatigueWords {
			if word == "" {
				continue
			}
			byWord[word] = append(byWord[word], declaration{source: p.Source, limit: limit})
		}
	}

	words := make([]string, 0, len(byWord))
	for word := range byWord {
		words = append(words, word)
	}
	sort.Strings(words)

	var conflicts []Conflict
	for _, word := range words {
		ds := byWord[word]
		if len(ds) < 2 {
			continue
		}
		first := ds[0].limit
		allSame := true
		for _, d := range ds[1:] {
			if d.limit != first {
				allSame = false
				break
			}
		}
		if allSame {
			continue
		}
		parts := make([]string, 0, len(ds))
		for _, d := range ds {
			parts = append(parts, fmt.Sprintf("%s=%d", d.source, d.limit))
		}
		winner := ds[len(ds)-1]
		conflicts = append(conflicts, Conflict{
			Source: winner.source,
			Kind:   ConflictFieldConflict,
			Field:  "fatigue_words." + word,
			Detail: fmt.Sprintf("field fatigue_words[%q] is declared in multiple sources with inconsistent thresholds: %s; nearest priority applies: %s",
				word, strings.Join(parts, " | "), winner.source),
		})
	}
	return conflicts
}

// allEqual determines whether the value of the same field is completely consistent across multiple sources; if consistent, no conflict is reported.
//
// Semantically, list fields do not care about order, but implementation-wise yaml deserialization has preserved the declaration order.
// Two completely identical configurations return true for reflect.DeepEqual, which satisfies the "value consistency" decision.
// Treating cases where the elements are the same but order is different as "inconsistent" is acceptable (still just info, non-blocking).
func allEqual(field string, sources []Parsed) bool {
	if len(sources) < 2 {
		return true
	}
	first := extractField(field, sources[0].Structured)
	for _, p := range sources[1:] {
		if !reflect.DeepEqual(first, extractField(field, p.Structured)) {
			return false
		}
	}
	return true
}

func extractField(field string, s Structured) any {
	switch field {
	case "genre":
		return s.Genre
	case "chapter_words":
		if s.ChapterWords == nil {
			return nil
		}
		return *s.ChapterWords
	case "forbidden_chars":
		return s.ForbiddenChars
	case "forbidden_phrases":
		return s.ForbiddenPhrases
	case "fatigue_words":
		return s.FatigueWords
	default:
		return nil
	}
}

// describeFieldConflict describes the conflict in a human-readable way: lists all sources + value of each source.
// The end indicates the final active source (nearest priority).
func describeFieldConflict(field string, sources []Parsed) string {
	var parts []string
	for _, p := range sources {
		parts = append(parts, fmt.Sprintf("%s=%s", p.Source, formatFieldValue(field, p.Structured)))
	}
	winner := sources[len(sources)-1]
	return fmt.Sprintf(
		"field %s is declared in multiple sources with inconsistent values: %s; nearest priority applies: %s",
		field, strings.Join(parts, " | "), winner.Source,
	)
}

func formatFieldValue(field string, s Structured) string {
	switch field {
	case "genre":
		return s.Genre
	case "chapter_words":
		if s.ChapterWords == nil {
			return "<nil>"
		}
		return fmt.Sprintf("%d-%d", s.ChapterWords.Min, s.ChapterWords.Max)
	case "forbidden_chars":
		return fmt.Sprintf("%v", s.ForbiddenChars)
	case "forbidden_phrases":
		return fmt.Sprintf("%v", s.ForbiddenPhrases)
	case "fatigue_words":
		return fmt.Sprintf("%v", s.FatigueWords)
	default:
		return "<unknown>"
	}
}
