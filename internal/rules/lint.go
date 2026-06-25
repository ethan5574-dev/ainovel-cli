package rules

import (
	"regexp"
	"strings"
)

// Lint performs built-in product baseline checks: scanning the text for mechanical residue, independent of user rules, and is always executed during commit.
// Shares the same contract as Check - only returns facts (Iron Rule #1), does not block flow, and is resolved by review/user.
//
// Current categories (all empirical defects from real long-running generation):
//   - markdown_residue: bold markdown residues ** in body, # heading lines other than the first line (exposing symbols when exporting txt)
//   - non_cjk_fragments: consecutive Latin letter fragments (model language mixing, e.g. "pattern" mixed directly in Chinese body)
func Lint(text string) []Violation {
	var vs []Violation
	vs = appendMarkdownResidue(vs, text)
	vs = appendNonCJKFragments(vs, text)
	return vs
}

func appendMarkdownResidue(vs []Violation, text string) []Violation {
	if n := strings.Count(text, "**"); n > 0 {
		vs = append(vs, Violation{
			Rule:     "markdown_residue",
			Target:   "**",
			Actual:   n,
			Severity: SeverityWarning,
		})
	}
	headings := 0
	seenContent := false
	for line := range strings.SplitSeq(text, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		// The first non-empty line's # heading is a valid format for chapter files (not hardcoded to line number, tolerates leading empty lines)
		first := !seenContent
		seenContent = true
		if !first && strings.HasPrefix(t, "#") {
			headings++
		}
	}
	if headings > 0 {
		vs = append(vs, Violation{
			Rule:     "markdown_residue",
			Target:   "#",
			Actual:   headings,
			Severity: SeverityWarning,
		})
	}
	return vs
}

var latinFragmentRe = regexp.MustCompile(`[A-Za-z]{2,}`)

// appendNonCJKFragments reports the total count of Latin letter fragments and deduplicated examples.
// Legitimate English in modern genres (brand names / abbreviations) will also hit - warning-level fact, decided by review based on genre.
func appendNonCJKFragments(vs []Violation, text string) []Violation {
	matches := latinFragmentRe.FindAllString(text, -1)
	if len(matches) == 0 {
		return vs
	}
	seen := make(map[string]struct{})
	var examples []string
	for _, m := range matches {
		if _, ok := seen[m]; ok {
			continue
		}
		seen[m] = struct{}{}
		if len(examples) < 3 {
			examples = append(examples, m)
		}
	}
	return append(vs, Violation{
		Rule:     "non_cjk_fragments",
		Target:   strings.Join(examples, ", "),
		Actual:   len(matches),
		Severity: SeverityWarning,
	})
}
