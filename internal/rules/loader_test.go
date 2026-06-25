package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// TestLoad_ThreeLayers verifies that Default + Global + Project layers fall in place in ascending order.
func TestLoad_ThreeLayers(t *testing.T) {
	rulesFS := fstest.MapFS{
		"default.md": {Data: []byte("---\nchapter_words: 3000-6000\n---\n")},
	}
	tmp := t.TempDir()
	globalDir := filepath.Join(tmp, "rules")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projectDir := filepath.Join(tmp, "project-rules")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "global.md"), []byte("---\nforbidden_chars:\n  - \"--\"\n---\n# Global Preference\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "project.md"), []byte("---\nchapter_words: 4000-8000\n---\n# Project Preference\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	layers := Load(LoadOptions{
		RulesFS:         rulesFS,
		HomeRulesDir:    globalDir,
		ProjectRulesDir: projectDir,
	})

	if len(layers) != 3 {
		t.Fatalf("expected 3 layers, got %d: %+v", len(layers), layers)
	}
	expectKinds := []SourceKind{SourceDefault, SourceGlobal, SourceProject}
	for i, want := range expectKinds {
		if layers[i].Kind != want {
			t.Errorf("layer[%d].Kind=%v, want %v", i, layers[i].Kind, want)
		}
	}
	// After Merge, project's chapter_words should win
	b := Merge(layers)
	if b.Structured.ChapterWords == nil || b.Structured.ChapterWords.Min != 4000 {
		t.Errorf("project chapter_words should win, got %+v", b.Structured.ChapterWords)
	}
	// global-contributed forbidden_chars should be retained when not declared by project
	if len(b.Structured.ForbiddenChars) != 1 || b.Structured.ForbiddenChars[0] != "--" {
		t.Errorf("global forbidden_chars should propagate, got %v", b.Structured.ForbiddenChars)
	}
	if !strings.Contains(b.Preferences, "Global Preference") || !strings.Contains(b.Preferences, "Project Preference") {
		t.Errorf("merged preferences missing body: %q", b.Preferences)
	}
}

func TestLoad_GenreFieldIsPassThrough(t *testing.T) {
	// Phase 1.1: genre field is passed through, no longer triggering assets/rules/genres/ load.
	// Even if genres/xianxia.md is placed in fs, it will not be read.
	rulesFS := fstest.MapFS{
		"default.md":        {Data: []byte("")},
		"genres/xianxia.md": {Data: []byte("---\nforbidden_chars:\n  - \"--\"\n---\n")},
	}
	tmp := t.TempDir()
	projectDir := filepath.Join(tmp, "project-rules")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "project.md"), []byte("---\ngenre: xianxia\n---\n"), 0644); err != nil {
		t.Fatal(err)
	}

	layers := Load(LoadOptions{
		RulesFS:         rulesFS,
		ProjectRulesDir: projectDir,
	})

	// Expect only default + project, no genre layer
	if len(layers) != 2 {
		t.Fatalf("expected 2 layers (no genre loading), got %d: %+v", len(layers), layers)
	}
	b := Merge(layers)
	if b.Structured.Genre != "xianxia" {
		t.Errorf("genre field should be passed through, got %q", b.Structured.Genre)
	}
	// genre file not loaded -> should not have "--" from genre file
	if len(b.Structured.ForbiddenChars) != 0 {
		t.Errorf("genres/*.md must not be auto-loaded in Phase 1.1, got %v", b.Structured.ForbiddenChars)
	}
}

func TestLoad_NilFSDoesNotPanic(t *testing.T) {
	// Empty inputs: does not panic, returns empty layers
	layers := Load(LoadOptions{})
	if len(layers) != 0 {
		t.Errorf("expected 0 layers, got %d", len(layers))
	}
}

func TestLoad_OnlyDefault(t *testing.T) {
	// Only default project rules available, user lacks both files
	rulesFS := fstest.MapFS{
		"default.md": {Data: []byte("---\nchapter_words: 3000-6000\n---\n")},
	}
	layers := Load(LoadOptions{RulesFS: rulesFS})
	if len(layers) != 1 || layers[0].Kind != SourceDefault {
		t.Errorf("expected only default layer, got %+v", layers)
	}
}

// TestLoad_GlobalDirScansAllMarkdown verifies multiple .md under global directory are loaded,
// merged in lexicographical order (latter overrides former), and non-.md files are ignored.
func TestLoad_GlobalDirScansAllMarkdown(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rules")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("---\nchapter_words: 1000-2000\n---\n# A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.md"), []byte("---\nchapter_words: 3000-4000\n---\n# B\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Non-.md files should be ignored
	if err := os.WriteFile(filepath.Join(dir, "ignore.txt"), []byte("not a rule"), 0o644); err != nil {
		t.Fatal(err)
	}

	layers := Load(LoadOptions{HomeRulesDir: dir})
	if len(layers) != 2 {
		t.Fatalf("expected 2 global layers (a.md, b.md), got %d", len(layers))
	}
	for _, p := range layers {
		if p.Kind != SourceGlobal {
			t.Errorf("dir files should be SourceGlobal, got %v", p.Kind)
		}
	}
	// Lexicographical order: a is first and b is second, b overrides a after merge
	b := Merge(layers)
	if b.Structured.ChapterWords == nil || b.Structured.ChapterWords.Min != 3000 {
		t.Errorf("later file (b.md) should win on chapter_words, got %+v", b.Structured.ChapterWords)
	}
	if !strings.Contains(b.Preferences, "# A") || !strings.Contains(b.Preferences, "# B") {
		t.Errorf("both files' preferences should be merged, got %q", b.Preferences)
	}
}

// TestLoad_GlobalDirMissing verifies that non-existent global dir is silently skipped.
func TestLoad_GlobalDirMissing(t *testing.T) {
	layers := Load(LoadOptions{HomeRulesDir: filepath.Join(t.TempDir(), "does-not-exist")})
	if len(layers) != 0 {
		t.Errorf("missing global dir should yield 0 layers, got %d", len(layers))
	}
}

// TestLoad_GlobalDirIgnoresHiddenAndSubdirs locks: hidden/editor temporary files (. prefix) are ignored,
// subdirectories are not recursed -- preventing dirty file binary content from being injected into the LLM as preference body.
func TestLoad_GlobalDirIgnoresHiddenAndSubdirs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rules")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "real.md"), []byte("---\nchapter_words: 3000-6000\n---\n# real\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// macOS AppleDouble / emacs lock / common hidden files -- all should be ignored
	for _, dirty := range []string{"._real.md", ".#lock.md", ".hidden.md"} {
		if err := os.WriteFile(filepath.Join(dir, dirty), []byte("\x00binary garbage\x00"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// .md in subdirectories should not be recursed
	if err := os.WriteFile(filepath.Join(dir, "sub", "nested.md"), []byte("---\nchapter_words: 1-2\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	layers := Load(LoadOptions{HomeRulesDir: dir})
	if len(layers) != 1 {
		t.Fatalf("expected only real.md loaded (hidden/dirty/subdir ignored), got %d layers", len(layers))
	}
	if layers[0].Source != filepath.Join(dir, "real.md") {
		t.Errorf("loaded wrong file: %s", layers[0].Source)
	}
	if b := Merge(layers); strings.Contains(b.Preferences, "garbage") || strings.Contains(b.Preferences, "\x00") {
		t.Errorf("dirty file content leaked into preferences: %q", b.Preferences)
	}
}

// TestLoad_GlobalDirIsFileExposesConflict verifies that when the rules path is mistakenly created as a file (not directory),
// it exposes a conflict rather than silently swallowing the error -- consistent with single-file IO error tolerance contract.
func TestLoad_GlobalDirIsFileExposesConflict(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rules")
	if err := os.WriteFile(p, []byte("oops, should be a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	layers := Load(LoadOptions{HomeRulesDir: p})
	if len(layers) != 1 || len(layers[0].Conflicts) == 0 {
		t.Fatalf("expected 1 layer carrying a conflict, got %+v", layers)
	}
	if layers[0].Conflicts[0].Kind != ConflictParseError {
		t.Errorf("expected ConflictParseError, got %v", layers[0].Conflicts[0].Kind)
	}
}

// TestEnsureRulesDirAt verifies directory setup + README.txt: writes instructions, always overwrites with the latest template,
// and README.txt (non-.md) is not treated as a rule by the loader.
func TestEnsureRulesDirAt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rules")
	if err := ensureRulesDirAt(dir); err != nil {
		t.Fatal(err)
	}
	readme := filepath.Join(dir, "README.txt")
	data, err := os.ReadFile(readme)
	if err != nil {
		t.Fatalf("README.txt should be written: %v", err)
	}
	if !strings.Contains(string(data), "front matter") {
		t.Errorf("README.txt missing guidance, got %q", data)
	}

	// Always overwrite with the latest template: outdated copy written by old version is refreshed when ensuring again
	if err := os.WriteFile(readme, []byte("outdated copy from old version about ./rules.md"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureRulesDirAt(dir); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(readme); string(again) != homeRulesReadme {
		t.Errorf("README.txt should be refreshed to latest template, got %q", again)
	}

	// README.txt is not treated as a rule (loader only scans .md)
	if layers := Load(LoadOptions{HomeRulesDir: dir}); len(layers) != 0 {
		t.Errorf("README.txt must not be loaded as a rule, got %d layers", len(layers))
	}
}

// TestDefaultProjectRulesDir locks project-level rules directory mirroring global: ./.ainovel/rules/.
func TestDefaultProjectRulesDir(t *testing.T) {
	proj := filepath.Join("/tmp", "demo-book")
	want := filepath.Join(proj, ".ainovel", "rules")
	if got := DefaultProjectRulesDir(proj); got != want {
		t.Errorf("DefaultProjectRulesDir=%q, want %q", got, want)
	}
	if got := DefaultProjectRulesDir(""); got != "" {
		t.Errorf("Empty project root should return empty string, got %q", got)
	}
}

// TestDefaultOptions_LoadsProjectRulesFromDotAinovel end-to-end verification:
// DefaultOptions connects ./.ainovel/rules/ under cwd into SourceProject source.
func TestDefaultOptions_LoadsProjectRulesFromDotAinovel(t *testing.T) {
	proj := t.TempDir()
	t.Chdir(proj)
	rulesDir := filepath.Join(proj, ".ainovel", "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "book.md"), []byte("---\nchapter_words: 4000-8000\n---\n# Book Preference\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rulesFS := fstest.MapFS{"default.md": {Data: []byte("---\nchapter_words: 3000-6000\n---\n")}}
	layers := Load(DefaultOptions(rulesFS))

	var got *Parsed
	for i := range layers {
		if layers[i].Kind == SourceProject {
			got = &layers[i]
		}
	}
	if got == nil {
		t.Fatalf("expected project rules loaded from ./.ainovel/rules/, got %+v", layers)
	}
	if b := Merge(layers); b.Structured.ChapterWords == nil || b.Structured.ChapterWords.Min != 4000 {
		t.Errorf("project rules should override default chapter_words, got %+v", b.Structured.ChapterWords)
	}
}
