package assets

import (
	"embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/voocel/ainovel-cli/internal/tools"
)

//go:embed prompts/*.md
var promptsFS embed.FS

//go:embed references
var referencesFS embed.FS

//go:embed styles/*.md
var stylesFS embed.FS

//go:embed voice.md
var voiceFS embed.FS

// Prompts contains the embedded prompt set.
type Prompts struct {
	ArchitectShort   string
	ArchitectLong    string
	Writer           string // Protocol template with {{VOICE}}; final prompt is assembled by BuildWriterPrompt.
	Editor           string
	ImportSegment    string // Semantic segmentation: identify chapter/volume/appendix boundaries.
	ImportAnalyze    string // Per-chapter fact extraction across consecutive batches.
	ImportSynthesize string // Hierarchical synthesis and volume-arc grouping (BookSynthesis).
	ImportRange      string // Consecutive range digest for the map phase of long books.
	SimulationSource string
	SimulationMerge  string
	RevisionAnalyze  string

	// Arbiter decision prompts (LLM-as-function, without simulation-guidance wrapping).
	ArbiterPlanStart    string
	ArbiterIntervention string
	ArbiterFailure      string
}

// Bundle contains the static resources required at runtime.
type Bundle struct {
	References tools.References
	Prompts    Prompts
	Styles     map[string]string
	Voice      string // Writing standard (voice layer), assembled with three-level overrides.
}

// LoadOptions declares voice-layer override sources. An empty directory skips that layer
// (eval passes zero values for a deterministic built-in baseline).
//
// Path semantics: BookStyleDir is tied to the book directory (outputDir), not cwd,
// so the same book restores the same voice. The rules layer remains cwd-based.
type LoadOptions struct {
	BookStyleDir string // <outputDir>/style
	HomeStyleDir string // ~/.ainovel/style
}

// DefaultLoadOptions builds production override sources from the book directory.
func DefaultLoadOptions(outputDir string) LoadOptions {
	var opts LoadOptions
	if outputDir != "" {
		opts.BookStyleDir = filepath.Join(outputDir, "style")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		opts.HomeStyleDir = filepath.Join(home, ".ainovel", "style")
	}
	return opts
}

// Load returns resources for the selected style. Voice assets (voice / anti-ai-tone / styles /
// genre style-references) use three-level precedence: built-in < home < book.
func Load(style string, opts LoadOptions) Bundle {
	return Bundle{
		References: loadReferences(style, opts),
		Prompts:    loadPrompts(),
		Styles:     loadStyles(opts),
		Voice:      resolveAppendable(mustRead(voiceFS, "voice.md"), "voice.md", opts),
	}
}

// voicePlaceholder is the insertion point for the voice section in the writer protocol template.
const voicePlaceholder = "{{VOICE}}"

// BuildWriterPrompt is the sole assembly entry point for the writer system prompt, shared by
// production, eval, and tests so both A/B arms use the same path.
// writerPrompt may already contain a simulation-guidance suffix; an empty style adds nothing.
func BuildWriterPrompt(writerPrompt, voice, style string) string {
	out := strings.Replace(writerPrompt, voicePlaceholder, strings.TrimSpace(voice), 1)
	if style != "" {
		out += "\n\n" + style
	}
	return out
}

// OverrideVoice replaces the assembled voice section with raw text for voice A/B evaluation.
// The variant and baseline still pass through BuildWriterPrompt.
func (b *Bundle) OverrideVoice(raw string) {
	b.Voice = raw
}

// resolveAppendable assembles three levels with append semantics: preserve built-in text,
// then append home and book sections. Without overrides it returns the built-in text byte-for-byte.
// "Later wins" is guidance for the LLM, not a mechanical guarantee; use rules for hard constraints.
func resolveAppendable(builtin, name string, opts LoadOptions) string {
	out := builtin
	if s := readOverride(opts.HomeStyleDir, name); s != "" {
		out += "\n\n## Global User Voice Override (the following takes priority over project defaults)\n\n" + s
	}
	if s := readOverride(opts.BookStyleDir, name); s != "" {
		out += "\n\n## Book Voice Override (the following takes priority over everything above)\n\n" + s
	}
	return out
}

// readOverride reads one file from an override directory; empty/missing directories or blank files return "".
func readOverride(dir, name string) string {
	if dir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// styleNameRe validates custom style filenames (without the extension) and rejects path characters.
var styleNameRe = regexp.MustCompile(`^[a-z0-9-]+$`)

func loadReferences(style string, opts LoadOptions) tools.References {
	if style == "" {
		style = "default"
	}
	refs := tools.References{
		ChapterGuide:      mustRead(referencesFS, "references/chapter-guide.md"),
		HookTechniques:    mustRead(referencesFS, "references/hook-techniques.md"),
		QualityChecklist:  mustRead(referencesFS, "references/quality-checklist.md"),
		OutlineTemplate:   mustRead(referencesFS, "references/outline-template.md"),
		CharacterTemplate: mustRead(referencesFS, "references/character-template.md"),
		ChapterTemplate:   mustRead(referencesFS, "references/chapter-template.md"),
		Consistency:       mustRead(referencesFS, "references/consistency.md"),
		ContentExpansion:  mustRead(referencesFS, "references/content-expansion.md"),
		DialogueWriting:   mustRead(referencesFS, "references/dialogue-writing.md"),
		LongformPlanning:  mustRead(referencesFS, "references/longform-planning.md"),
		Differentiation:   mustRead(referencesFS, "references/differentiation.md"),
		AntiAITone:        resolveAppendable(mustRead(referencesFS, "references/anti-ai-tone.md"), "anti-ai-tone.md", opts),
	}
	if style != "" && style != "default" {
		genreDir := "references/genres/" + style + "/"
		if data, err := referencesFS.ReadFile(genreDir + "style-references.md"); err == nil {
			refs.StyleReference = string(data)
		}
		if data, err := referencesFS.ReadFile(genreDir + "arc-templates.md"); err == nil {
			refs.ArcTemplates = string(data)
		}
		// Genre references are whole-file replacements (book > home). A custom style without a built-in
		// reference may be supplied only by an override; falling back to default would be misleading.
		relPath := filepath.Join("genres", style, "style-references.md")
		for _, dir := range []string{opts.HomeStyleDir, opts.BookStyleDir} {
			if s := readOverride(dir, relPath); s != "" {
				refs.StyleReference = s
			}
		}
	}
	return refs
}

func loadPrompts() Prompts {
	return Prompts{
		ArchitectShort:   WithSimulationGuidance(mustRead(promptsFS, "prompts/architect-short.md"), "architect"),
		ArchitectLong:    WithSimulationGuidance(mustRead(promptsFS, "prompts/architect-long.md"), "architect"),
		Writer:           WithSimulationGuidance(mustRead(promptsFS, "prompts/writer.md"), "writer"),
		Editor:           WithSimulationGuidance(mustRead(promptsFS, "prompts/editor.md"), "editor"),
		ImportSegment:    mustRead(promptsFS, "prompts/import-segment.md"),
		ImportAnalyze:    mustRead(promptsFS, "prompts/import-analyze.md"),
		ImportSynthesize: mustRead(promptsFS, "prompts/import-synthesize.md"),
		ImportRange:      mustRead(promptsFS, "prompts/import-range.md"),
		SimulationSource: mustRead(promptsFS, "prompts/simulation-source.md"),
		SimulationMerge:  mustRead(promptsFS, "prompts/simulation-merge.md"),
		RevisionAnalyze:  mustRead(promptsFS, "prompts/revision-analyze.md"),

		ArbiterPlanStart:    mustRead(promptsFS, "prompts/arbiter-plan-start.md"),
		ArbiterIntervention: mustRead(promptsFS, "prompts/arbiter-intervention.md"),
		ArbiterFailure:      mustRead(promptsFS, "prompts/arbiter-failure.md"),
	}
}

// WithSimulationGuidance appends simulation-profile guidance to core prompts. It is exported
// so eval and other external callers can wrap variants through the same path as Load's baseline.
func WithSimulationGuidance(prompt, role string) string {
	return prompt + "\n\n" + strings.ReplaceAll(simulationGuidance, "{{role}}", role)
}

// OverridePrompt replaces a role prompt in the bundle and applies the same WithSimulationGuidance
// wrapper as Load. This keeps eval A/B variants equivalent. For writer.md, raw must include
// {{VOICE}}; use OverrideVoice when only the voice should vary.
func (b *Bundle) OverridePrompt(file, raw string) error {
	role, ok := promptRole[file]
	if !ok {
		return fmt.Errorf("prompt file cannot be overridden: %s (only core prompts are supported)", file)
	}
	wrapped := WithSimulationGuidance(raw, role)
	switch file {
	case "architect-short.md":
		b.Prompts.ArchitectShort = wrapped
	case "architect-long.md":
		b.Prompts.ArchitectLong = wrapped
	case "writer.md":
		b.Prompts.Writer = wrapped
	case "editor.md":
		b.Prompts.Editor = wrapped
	}
	return nil
}

// promptRole maps core prompt filenames to simulation-guidance roles.
var promptRole = map[string]string{
	"architect-short.md": "architect",
	"architect-long.md":  "architect",
	"writer.md":          "writer",
	"editor.md":          "editor",
}

const simulationGuidance = `## Simulation Profile

When planning_memory or working_memory contains simulation_profile, treat it as the current book's stylistic imitation constraint. {{role}} must read its style, lexicon, plot_design, hook_design, pacing_density, reader_engagement, and role_guidance.

Principles: borrow structure, pacing, hooks, information release, and reader-engagement techniques; do not copy source sentences, characters, place names, named settings, or fixed scenes. If simulation_profile conflicts with an explicit user request, follow the user request.`

// loadStyles enumerates built-in styles, then applies styles/*.md overrides in home -> book order.
// Same-named files replace the whole style; new filenames add styles.
func loadStyles(opts LoadOptions) map[string]string {
	styles := make(map[string]string)
	entries, err := stylesFS.ReadDir("styles")
	if err != nil {
		return styles
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		data, err := stylesFS.ReadFile("styles/" + e.Name())
		if err != nil {
			continue
		}
		styles[name] = string(data)
	}
	for _, dir := range []string{opts.HomeStyleDir, opts.BookStyleDir} {
		overlayStyles(styles, dir)
	}
	return styles
}

// overlayStyles adds <dir>/styles/*.md to the style set; invalid filenames are skipped with a warning.
func overlayStyles(styles map[string]string, dir string) {
	if dir == "" {
		return
	}
	entries, err := os.ReadDir(filepath.Join(dir, "styles"))
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		if !styleNameRe.MatchString(name) {
			slog.Warn("Ignoring invalid style filename", "module", "assets", "dir", dir, "file", e.Name())
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, "styles", e.Name()))
		if err != nil {
			continue
		}
		styles[name] = string(data)
	}
}

func mustRead(fs embed.FS, path string) string {
	data, err := fs.ReadFile(path)
	if err != nil {
		panic(fmt.Sprintf("embed read %s: %v", path, err))
	}
	return string(data)
}
