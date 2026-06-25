package rules

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Collection of known front matter fields, used to identify unknown fields and write them to conflicts.
var knownFrontMatterFields = map[string]struct{}{
	"genre":             {},
	"chapter_words":     {},
	"forbidden_chars":   {},
	"forbidden_phrases": {},
	"fatigue_words":     {},
}

// Parse parses a single rules.md content (front matter + Markdown).
//
// Tolerance Strategy:
//   - front matter parsing failure as a whole: does not block, body is still used as preferences, conflicts records parse_error
//   - unknown fields: discarded, conflicts records unknown_field
//   - field type error: discards the field, conflicts records type_error
//   - invalid field value (e.g. chapter_words cannot be parsed as a range): discarded, conflicts records invalid_value
//
// source is the file path, only used for conflicts.source; kind determines priority.
func Parse(source string, kind SourceKind, content []byte) Parsed {
	parsed := Parsed{Source: source, Kind: kind}

	fmText, bodyText := splitFrontMatter(content)
	parsed.Preference = strings.TrimSpace(bodyText)

	if strings.TrimSpace(fmText) == "" {
		return parsed
	}

	// Unmarshal to map[string]any first, then parse field-by-field with strong types.
	// This helps differentiate between "field does not exist" and "field type error", and identify unknown fields.
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(fmText), &raw); err != nil {
		parsed.Conflicts = append(parsed.Conflicts, Conflict{
			Source: source,
			Kind:   ConflictParseError,
			Detail: fmt.Sprintf("front matter YAML parsing failed: %v", err),
		})
		return parsed
	}

	for key, val := range raw {
		if _, ok := knownFrontMatterFields[key]; !ok {
			parsed.Conflicts = append(parsed.Conflicts, Conflict{
				Source: source,
				Kind:   ConflictUnknownField,
				Field:  key,
				Detail: fmt.Sprintf("unknown field %q, not supported in Phase 1; ignored", key),
			})
			continue
		}
		applyField(&parsed, key, val)
	}

	return parsed
}

// splitFrontMatter splits front matter wrapped in '---' from the remaining body.
//
// Convention:
//   - File must start with '---' (toterates BOM / empty lines) to be considered having front matter
//   - Text after the second '---' is the body
//   - No front matter: the entire text is body
//   - Symmetrical '---' start but no terminating '---': treated as no front matter (prevent swallowing entire body)
func splitFrontMatter(content []byte) (fm, body string) {
	text := string(bytes.TrimPrefix(content, []byte{0xEF, 0xBB, 0xBF})) // Remove UTF-8 BOM
	lines := strings.Split(text, "\n")

	// Find first non-empty line; if it's not '---', then all is body
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.TrimSpace(line) == "---" {
			start = i
		}
		break
	}
	if start < 0 {
		return "", text
	}

	// Find second '---'
	end := -1
	for i := start + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		// Starting with '---' but not closed: conservatively treated as no front matter
		return "", text
	}

	fm = strings.Join(lines[start+1:end], "\n")
	body = strings.Join(lines[end+1:], "\n")
	return fm, body
}

// applyField puts a single raw field into Parsed.Structured, writes to conflicts on type mismatch.
func applyField(p *Parsed, key string, val any) {
	switch key {
	case "genre":
		s, ok := asString(val)
		if !ok {
			p.Conflicts = append(p.Conflicts, typeErr(p.Source, key, "string", val))
			return
		}
		p.Structured.Genre = strings.TrimSpace(s)

	case "chapter_words":
		rng, ok := parseChapterWords(val)
		if !ok {
			p.Conflicts = append(p.Conflicts, Conflict{
				Source: p.Source,
				Kind:   ConflictInvalidValue,
				Field:  key,
				Detail: fmt.Sprintf("chapter_words expects range \"min-max\" (e.g. 3000-6000) or a single target value (e.g. 2500), got %v", val),
			})
			return
		}
		p.Structured.ChapterWords = rng

	case "forbidden_chars":
		list, ok := asStringList(p, key, val)
		if !ok {
			p.Conflicts = append(p.Conflicts, typeErr(p.Source, key, "[]string", val))
			return
		}
		p.Structured.ForbiddenChars = list

	case "forbidden_phrases":
		list, ok := asStringList(p, key, val)
		if !ok {
			p.Conflicts = append(p.Conflicts, typeErr(p.Source, key, "[]string", val))
			return
		}
		p.Structured.ForbiddenPhrases = list

	case "fatigue_words":
		m, ok := parseFatigueWords(p, val)
		if !ok {
			p.Conflicts = append(p.Conflicts, typeErr(p.Source, key, "map[string]int or []string", val))
			return
		}
		p.Structured.FatigueWords = m
	}
}

// parseChapterWords parses the chapter word range as a *WordRange, accepting three formats:
//   - "min-max" range string (e.g. "3000-6000")
//   - {min, max} map
//   - A single positive integer N (naked number 2500 or string "2500") - understood as "target N words/chapter",
//     automatically expanded into an N±20% range. Otherwise, writing a single value by intuition will be silently discarded and fallback to built-in default (issue #41).
func parseChapterWords(val any) (*WordRange, bool) {
	switch v := val.(type) {
	case string:
		s := strings.TrimSpace(v)
		if !strings.Contains(s, "-") { // Single value format, e.g. "2500"
			if n, err := strconv.Atoi(s); err == nil && n > 0 {
				return wordBandAround(n), true
			}
			return nil, false
		}
		parts := strings.Split(s, "-")
		if len(parts) != 2 {
			return nil, false
		}
		minV, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
		maxV, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err1 != nil || err2 != nil || minV < 0 || maxV < 0 || minV > maxV {
			return nil, false
		}
		return &WordRange{Min: minV, Max: maxV}, true
	case map[string]any:
		minV, ok1 := asInt(v["min"])
		maxV, ok2 := asInt(v["max"])
		if !ok1 || !ok2 || minV < 0 || maxV < 0 || minV > maxV {
			return nil, false
		}
		return &WordRange{Min: minV, Max: maxV}, true
	default: // Raw number, parsed as int / float64 by YAML
		if n, ok := asInt(v); ok && n > 0 {
			return wordBandAround(n), true
		}
		return nil, false
	}
}

// wordBandAround expands "target N words/chapter" into a comfortable ±20% range (e.g. 2500 -> 2000-3000),
// making the single value format equivalent to a reasonable range instead of an N-N hard wall (tight range forces compression infinite loop).
func wordBandAround(n int) *WordRange {
	return &WordRange{Min: n * 4 / 5, Max: n * 6 / 5}
}

// parseFatigueWords accepts both map[string]int (with threshold) and []string (default threshold 1).
//
// Any key type error or invalid threshold will write a conflict to p.Conflicts, never silently swallowed.
// Returns (map, true) if any valid items exist; (nil, false) if the overall type is wrong or all items are invalid.
func parseFatigueWords(p *Parsed, val any) (map[string]int, bool) {
	switch v := val.(type) {
	case map[string]any:
		out := make(map[string]int, len(v))
		for k, raw := range v {
			trimmed := strings.TrimSpace(k)
			if trimmed == "" {
				p.Conflicts = append(p.Conflicts, Conflict{
					Source: p.Source,
					Kind:   ConflictInvalidValue,
					Field:  "fatigue_words",
					Detail: "blank key found in fatigue_words, skipped",
				})
				continue
			}
			n, ok := asInt(raw)
			if !ok {
				p.Conflicts = append(p.Conflicts, Conflict{
					Source: p.Source,
					Kind:   ConflictTypeError,
					Field:  "fatigue_words." + trimmed,
					Detail: fmt.Sprintf("fatigue_words[%q] expects int threshold, got %T (%v); key discarded", trimmed, raw, raw),
				})
				continue
			}
			if n <= 0 {
				p.Conflicts = append(p.Conflicts, Conflict{
					Source: p.Source,
					Kind:   ConflictInvalidValue,
					Field:  "fatigue_words." + trimmed,
					Detail: fmt.Sprintf("fatigue_words[%q] threshold must be > 0, got %d; key discarded", trimmed, n),
				})
				continue
			}
			out[trimmed] = n
		}
		if len(out) == 0 {
			return nil, false
		}
		return out, true
	case []any:
		out := make(map[string]int, len(v))
		for i, raw := range v {
			s, ok := raw.(string)
			if !ok {
				p.Conflicts = append(p.Conflicts, Conflict{
					Source: p.Source,
					Kind:   ConflictTypeError,
					Field:  fmt.Sprintf("fatigue_words[%d]", i),
					Detail: fmt.Sprintf("fatigue_words list elements expect string, got %T (%v); element discarded", raw, raw),
				})
				continue
			}
			s = strings.TrimSpace(s)
			if s == "" {
				p.Conflicts = append(p.Conflicts, Conflict{
					Source: p.Source,
					Kind:   ConflictInvalidValue,
					Field:  fmt.Sprintf("fatigue_words[%d]", i),
					Detail: "fatigue_words list element is blank; discarded",
				})
				continue
			}
			out[s] = 1
		}
		if len(out) == 0 {
			return nil, false
		}
		return out, true
	default:
		return nil, false
	}
}

// asString / asInt / asStringList are small utilities for type normalization after yaml.v3 deserialization.
//
// Strict Policy (Debug-First): Only accepts the target type, no auto-conversions of other types.
// Type errors are written to conflicts by the caller, not silently fixed inside.

// asString only accepts string scalar.
// Note: In YAML, 'genre: 42' (without quotes) will be deserialized as int, which is determined as a type error.
// The user should write 'genre: "42"' to explicitly declare it as string.
func asString(v any) (string, bool) {
	if s, ok := v.(string); ok {
		return s, true
	}
	return "", false
}

// asInt accepts all integer types; float64 is only accepted when it is exactly an integer (YAML numbers default to float64).
// String numbers are no longer auto-converted - avoiding confusion with mistaken placement of fields as strings.
func asInt(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int64:
		return int(x), true
	case float64:
		// Only accepted when float is exactly an integer (e.g. yaml parsed '5' -> float64(5.0))
		if x == float64(int(x)) {
			return int(x), true
		}
		return 0, false
	default:
		return 0, false
	}
}

// Elements in asStringList must be string; other types skip the element and write to conflicts.
// Returns (list, true) if valid elements exist; (nil, false) if overall type is wrong or all elements are invalid.
func asStringList(p *Parsed, field string, v any) ([]string, bool) {
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(arr))
	for i, raw := range arr {
		s, ok := raw.(string)
		if !ok {
			p.Conflicts = append(p.Conflicts, Conflict{
				Source: p.Source,
				Kind:   ConflictTypeError,
				Field:  fmt.Sprintf("%s[%d]", field, i),
				Detail: fmt.Sprintf("%s list elements expect string, got %T (%v); element discarded", field, raw, raw),
			})
			continue
		}
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

func typeErr(source, field, expected string, got any) Conflict {
	return Conflict{
		Source: source,
		Kind:   ConflictTypeError,
		Field:  field,
		Detail: fmt.Sprintf("field %s type error, expected %s, got %T (%v); discarded", field, expected, got, got),
	}
}
