package assets

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"

	"github.com/voocel/ainovel-cli/internal/tools"
)

//go:embed prompts/*.md
var promptsFS embed.FS

//go:embed references
var referencesFS embed.FS

//go:embed styles/*.md
var stylesFS embed.FS

//go:embed rules
var rulesFS embed.FS

// Prompts represents the collection of embedded prompt templates.
type Prompts struct {
	Coordinator      string
	ArchitectShort   string
	ArchitectLong    string
	Writer           string
	Editor           string
	ImportFoundation string
	ImportAnalyzer   string
	SimulationSource string
	SimulationMerge  string
}

// Bundle represents the collection of static resources required for running.
type Bundle struct {
	References tools.References
	Prompts    Prompts
	Styles     map[string]string
	// RulesFS is the assets/rules subtree (the root directly contains default.md).
	// Passed to rules.Load by the caller as the built-in rules source.
	RulesFS fs.FS
}

// Load returns the resource bundle corresponding to the specified style.
func Load(style string) Bundle {
	return Bundle{
		References: loadReferences(style),
		Prompts:    loadPrompts(),
		Styles:     loadStyles(),
		RulesFS:    loadRulesFS(),
	}
}

// loadRulesFS returns the sub-filesystem of assets/rules; the root directly contains default.md.
// If fs.Sub fails (which theoretically should not happen) it returns nil, causing rules.Load to skip the built-in source.
func loadRulesFS() fs.FS {
	sub, err := fs.Sub(rulesFS, "rules")
	if err != nil {
		return nil
	}
	return sub
}

func loadReferences(style string) tools.References {
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
		AntiAITone:        mustRead(referencesFS, "references/anti-ai-tone.md"),
	}
	if style != "" && style != "default" {
		genreDir := "references/genres/" + style + "/"
		if data, err := referencesFS.ReadFile(genreDir + "style-references.md"); err == nil {
			refs.StyleReference = string(data)
		}
		if data, err := referencesFS.ReadFile(genreDir + "arc-templates.md"); err == nil {
			refs.ArcTemplates = string(data)
		}
	}
	return refs
}

func loadPrompts() Prompts {
	return Prompts{
		Coordinator:      withSimulationGuidance(mustRead(promptsFS, "prompts/coordinator.md"), "coordinator"),
		ArchitectShort:   withSimulationGuidance(mustRead(promptsFS, "prompts/architect-short.md"), "architect"),
		ArchitectLong:    withSimulationGuidance(mustRead(promptsFS, "prompts/architect-long.md"), "architect"),
		Writer:           withSimulationGuidance(mustRead(promptsFS, "prompts/writer.md"), "writer"),
		Editor:           withSimulationGuidance(mustRead(promptsFS, "prompts/editor.md"), "editor"),
		ImportFoundation: mustRead(promptsFS, "prompts/import-foundation.md"),
		ImportAnalyzer:   mustRead(promptsFS, "prompts/import-chapter-analyzer.md"),
		SimulationSource: mustRead(promptsFS, "prompts/simulation-source.md"),
		SimulationMerge:  mustRead(promptsFS, "prompts/simulation-merge.md"),
	}
}

func withSimulationGuidance(prompt, role string) string {
	return prompt + "\n\n" + strings.ReplaceAll(simulationGuidance, "{{role}}", role)
}

const simulationGuidance = `## Simulation Profile

When novel_context returns simulation_profile, it must be treated as the stylistic writing guidance for the current work. {{role}} should read style, lexicon, plot_design, hook_design, pacing_density, reader_engagement, and role_guidance from it.

Usage principles: Borrow structures, pacing, hooks, information release, and reader-engagement techniques; do not copy exact sentences, characters, locations, proprietary settings, or fixed tropes from the original. If simulation_profile conflicts with explicit user instructions, prioritize the user instructions.`

func loadStyles() map[string]string {
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
	return styles
}

func mustRead(fs embed.FS, path string) string {
	data, err := fs.ReadFile(path)
	if err != nil {
		panic(fmt.Sprintf("embed read %s: %v", path, err))
	}
	return string(data)
}
