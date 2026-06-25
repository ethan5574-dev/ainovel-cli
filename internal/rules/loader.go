package rules

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LoadOptions is the input parameters for Load.
//
// Missing files are not considered errors, and loader silently skips them; parser failures do not block, and conflicts are written to Parsed.Conflicts by the parser.
type LoadOptions struct {
	// RulesFS is the assets/rules subtree. The root directory is expected to directly contain default.md.
	// Typically obtained via fs.Sub(embedFS, "rules"); nil means skipping built-in rules.
	RulesFS fs.FS

	// HomeRulesDir is the ~/.ainovel/rules/ directory; loader scans all top-level .md files under it (merged in lexicographical order). Empty means skip.
	HomeRulesDir string

	// ProjectRulesDir is the ./.ainovel/rules/ directory (mirrors global, also scans all top-level .md files under it). Empty means skip.
	ProjectRulesDir string
}

// Load reads in the order of Default -> Global -> Project, returning a list of Parsed in ascending order.
//
// After receiving the return value, merger only needs to merge in the order of the list, with the latter overwriting the former.
// Does not introduce a second-phase load - extension layers like Genre / Learned are not opened until they contain actual content.
func Load(opts LoadOptions) []Parsed {
	var layers []Parsed
	if p, ok := readFromFS(opts.RulesFS, "default.md", SourceDefault, "assets/rules/default.md"); ok {
		layers = append(layers, p)
	}
	layers = append(layers, readDirFromDisk(opts.HomeRulesDir, SourceGlobal)...)
	layers = append(layers, readDirFromDisk(opts.ProjectRulesDir, SourceProject)...)
	return layers
}

// readFromFS reads and parses from fs.FS; returns (Parsed{}, false) if the file does not exist.
// displayPath is used for Parsed.Source (so that it displays as "assets/rules/..." in sources/conflicts).
func readFromFS(fsys fs.FS, name string, kind SourceKind, displayPath string) (Parsed, bool) {
	if fsys == nil {
		return Parsed{}, false
	}
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		// Missing files are silently skipped; other errors do not block (loader does not return errors by design)
		if errors.Is(err, fs.ErrNotExist) || os.IsNotExist(err) {
			return Parsed{}, false
		}
		// A rare few IO errors: exposed as parse_errors to avoid silent failures
		return Parsed{
			Source: displayPath,
			Kind:   kind,
			Conflicts: []Conflict{{
				Source: displayPath,
				Kind:   ConflictParseError,
				Detail: "read failed: " + err.Error(),
			}},
		}, true
	}
	return Parse(displayPath, kind, data), true
}

// readFromDisk reads and parses from absolute path; returns (Parsed{}, false) if path is empty or file does not exist.
func readFromDisk(absPath string, kind SourceKind) (Parsed, bool) {
	if strings.TrimSpace(absPath) == "" {
		return Parsed{}, false
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return Parsed{}, false
		}
		return Parsed{
			Source: absPath,
			Kind:   kind,
			Conflicts: []Conflict{{
				Source: absPath,
				Kind:   ConflictParseError,
				Detail: "read failed: " + err.Error(),
			}},
		}, true
	}
	return Parse(absPath, kind, data), true
}

// readDirFromDisk scans all top-level .md files under the directory (in lexicographical order of filenames), parsing each into a Parsed.
// Lexicographical order ensures that the merge order of multiple files at the same level is stable and predictable (latter overrides former).
// Skips subdirectories and hidden/editor temporary files starting with . (like macOS ._x.md, emacs .#x.md)
// to prevent injecting binary contents of dirty files into the LLM as preference body.
// Empty path or non-existent directory returns nil (silent skip, consistent with single file missing);
// If directory exists but reading fails (permissions / path is actually a file), exposes ConflictParseError without silently swallowing the error -
// keeping consistency with the fault-tolerance contract of readFromFS / readFromDisk.
// Does not recurse subdirectories - keeps it flat to avoid introducing implicit hierarchies.
func readDirFromDisk(dir string, kind SourceKind) []Parsed {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return []Parsed{{
			Source: dir,
			Kind:   kind,
			Conflicts: []Conflict{{
				Source: dir,
				Kind:   ConflictParseError,
				Detail: "failed to read rules directory: " + err.Error(),
			}},
		}}
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || !strings.EqualFold(filepath.Ext(e.Name()), ".md") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	var out []Parsed
	for _, name := range names {
		if p, ok := readFromDisk(filepath.Join(dir, name), kind); ok {
			out = append(out, p)
		}
	}
	return out
}

// ainovelDirName is the dotdir name shared by ainovel at both user and project levels.
// Global ~/.ainovel/rules/ and project ./.ainovel/rules/ are symmetrical because of this.
const ainovelDirName = ".ainovel"

// DefaultProjectRulesDir constructs the absolute path of ./.ainovel/rules/ (based on the given project directory).
// Symmetrical with DefaultHomeRulesDir, the caller passes project root to avoid loader depending on cwd inside.
func DefaultProjectRulesDir(projectDir string) string {
	if projectDir == "" {
		return ""
	}
	return filepath.Join(projectDir, ainovelDirName, "rules")
}

// DefaultHomeRulesDir constructs the absolute path of the ~/.ainovel/rules/ directory.
// Returns empty string if home directory lookup fails (caller will skip this source).
func DefaultHomeRulesDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ainovelDirName, "rules")
}

// homeRulesReadme is the instructions written to ~/.ainovel/rules/README.txt during first-run guide.
// Deliberately uses .txt suffix instead of .md - loader only scans .md, so this instruction will not be injected into the LLM.
const homeRulesReadme = `Put global writing preferences here, active across all books.

Simplest way: Create a new .md file (e.g. my-style.md), and write preferences in plain English—
No formatting required, no YAML needed:

    # Characters
    - Do not write the protagonist Lin Chen as too saint-like, cold outside and warm inside is fine
    # Style
    - Use more physical perception (knuckles turning white) instead of emotional labels (nervous)
    - Dialogue should not be too formal

These will be handed over to the editor as is for semantic review. Multiple .md files are merged in lexicographical order of filenames;
hidden files starting with dots and non-.md files are ignored (so this README.txt will not be treated as a rule).

Advanced (optional): If you want rigid, deterministic mechanical checks like "word count / forbidden words",
you can add a YAML front matter section at the top of the file - commit_chapter will check word count and force errors:

    ---
    chapter_words: 3000-6000          # Chapter word count range
    forbidden_phrases: ["to some extent"] # Forbidden phrases, errors out if present
    fatigue_words: {cannot help: 1}   # Fatigue words, warns when exceeding threshold per chapter
    ---
    (Write plain English preferences below as usual)

It's fine if you don't write them: the mechanical baselines for common AI cliché phrases and fatigue words are already built-in and work out of the box.

Load priority (High -> Low): ./.ainovel/rules/*.md (this book) > ~/.ainovel/rules/*.md (here) > Built-in default
`

// EnsureHomeRulesDir attempts to create the ~/.ainovel/rules/ directory and write README.txt guide,
// helping users discover this global preference extension point and know how to write.
// nice-to-have, non-critical path: silently swallow home lookup failure or write errors, never blocking startup.
func EnsureHomeRulesDir() {
	if dir := DefaultHomeRulesDir(); dir != "" {
		_ = ensureRulesDirAt(dir)
	}
}

// ensureRulesDirAt creates the directory and writes README.txt as the current guide template, which is the testable core of EnsureHomeRulesDir.
// README.txt is a system-generated guide file (user preferences are written in *.md, it is not loaded by the loader), and it is overwritten each time
// with the latest template - keeping no old content, thus needing no version compatibility logic.
func ensureRulesDirAt(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "README.txt"), []byte(homeRulesReadme), 0o644)
}

// DefaultOptions constructs common LoadOptions based on the current working directory.
//
// Recommended to be called once during Host startup, allowing ContextTool / CommitChapterTool to reuse the same configuration.
// If parsing cwd fails, ProjectRulesDir is left empty (loader will skip that source).
//
// Path semantics: ProjectRulesDir binds to the **current working directory (cwd)** instead of outputDir.
// When users cd into different directories to launch and write different books, ./.ainovel/rules/ naturally follows cwd;
// If cross-book sharing is needed, simply put them in the ~/.ainovel/rules/ global directory (all .md under it will be loaded).
func DefaultOptions(rulesFS fs.FS) LoadOptions {
	cwd, _ := os.Getwd()
	return LoadOptions{
		RulesFS:         rulesFS,
		HomeRulesDir:    DefaultHomeRulesDir(),
		ProjectRulesDir: DefaultProjectRulesDir(cwd),
	}
}
