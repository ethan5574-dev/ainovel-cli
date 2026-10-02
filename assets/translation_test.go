package assets

import (
	"strings"
	"testing"
	"unicode"
)

func TestRuntimePromptsAreEnglishAndUseCurrentTools(t *testing.T) {
	p := loadPrompts()
	for name, prompt := range map[string]string{
		"writer": p.Writer, "editor": p.Editor,
		"architect_long": p.ArchitectLong, "architect_short": p.ArchitectShort,
		"arbiter_intervention": p.ArbiterIntervention, "arbiter_failure": p.ArbiterFailure,
		"voice":    mustRead(voiceFS, "voice.md"),
		"revision": mustRead(promptsFS, "prompts/revision-analyze.md"),
		"import":   mustRead(promptsFS, "prompts/import-synthesize.md"),
	} {
		t.Run(name, func(t *testing.T) {
			for _, r := range prompt {
				if unicode.Is(unicode.Han, r) {
					t.Fatalf("untranslated instruction: %q", r)
				}
			}
			for _, obsolete := range []string{"expand_arc", "ask_user", "chapter_words", "user_directives"} {
				if strings.Contains(prompt, obsolete) {
					t.Errorf("obsolete runtime contract %q", obsolete)
				}
			}
			seen := make(map[string]bool)
			for _, line := range strings.Split(prompt, "\n") {
				if strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "### ") {
					if seen[line] {
						t.Errorf("duplicated prompt section: %s", line)
					}
					seen[line] = true
				}
			}
		})
	}
}

func TestLongPlannerRetainsAllPremiseHeadings(t *testing.T) {
	prompt := loadPrompts().ArchitectLong
	for _, heading := range []string{
		"Genre and Tone", "Genre Positioning", "Core Conflict", "Protagonist Goal",
		"Ending Direction", "Writing Forbidden Zones", "Differentiated Selling Point",
		"Differentiated Hook", "Core Promise to Deliver", "Story Engine",
		"Relationship/Growth Mainline", "Upgrade Path", "Midpoint Twist", "Ending Theme",
	} {
		if !strings.Contains(prompt, "- "+heading+"\n") {
			t.Errorf("missing parseable premise heading %q", heading)
		}
	}
	for _, contract := range []string{"save_book", "expand_next_arc", "final_volume: true", "foundation_ready=true"} {
		if !strings.Contains(prompt, contract) {
			t.Errorf("missing upstream planning contract %q", contract)
		}
	}
}
