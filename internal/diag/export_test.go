package diag

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/store"
)

// sentinel is a piece of "prose" that should never appear in exports.
const sentinel = "on a snowy night the protagonist uncovers the antagonist's conspiracy this is secret prose"

// writeSession writes several messages in sessions/*.jsonl format to a temporary output directory.
func writeSession(t *testing.T, rel string, msgs []agentcore.Message) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "meta", "sessions", rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var b strings.Builder
	for _, m := range msgs {
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return dir
}

func commitCall(chapterRaw string) agentcore.Message {
	args := json.RawMessage(`{"chapter":` + chapterRaw + `,"content":"` + sentinel + sentinel + `"}`)
	return agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{Name: "commit_chapter", Args: args})},
	}
}

func errResult(msg string) agentcore.Message {
	return agentcore.Message{
		Role:     agentcore.RoleTool,
		Content:  []agentcore.ContentBlock{agentcore.TextBlock(msg)},
		Metadata: map[string]any{"is_error": true},
	}
}

// TestExport_DeathLoopShape end-to-end reproduces #34: the model stringifies the chapter argument
// in commit_chapter, causing a validation loop. Asserts that export can locate this and contains zero leaked prose.
func TestExport_DeathLoopShape(t *testing.T) {
	var msgs []agentcore.Message
	// A piece of raw coordinator planning text (<4KB, bypassing session_compact) must be redacted.
	msgs = append(msgs, agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(sentinel)},
	})
	// 14 rounds of commit_chapter(chapter:"7") + InputValidationError.
	for range 14 {
		msgs = append(msgs, commitCall(`"7"`))
		msgs = append(msgs, errResult("InputValidationError: chapter must be int"))
	}

	dir := writeSession(t, "coordinator.jsonl", msgs)
	s := store.NewStore(dir)
	rep, rc := Diagnose(s)
	out := string(RenderExport(rep, rc))

	if strings.Contains(out, sentinel) {
		t.Fatalf("Prose leaked! Export contains sentinel:\n%s", out)
	}
	if !strings.Contains(out, `chapter: "7"`) {
		t.Errorf("Missing type exception signal chapter: \"7\" (#34 root cause)\n%s", out)
	}
	if !strings.Contains(out, "InputValidationError") {
		t.Errorf("Error string not preserved\n%s", out)
	}
	if !strings.Contains(out, "×14") {
		t.Errorf("Repeat aggregation not listed x14\n%s", out)
	}
	// Phase 2: Runtime detection should determine this loop as a critical RepeatedToolError.
	if !strings.Contains(out, "Tool repeatedly reports same error") {
		t.Errorf("Runtime detection did not produce RepeatedToolError\n%s", out)
	}
	if !strings.Contains(out, "[critical]") {
		t.Errorf("14 repetitions should be upgraded to critical\n%s", out)
	}
}

// TestExport_NumberVsStringArg verifies that scalar and string projections differentiate types:
// chapter:7 (number) is kept as 7, while chapter:"7" (string) is kept as "7".
func TestExport_NumberVsStringArg(t *testing.T) {
	intDir := writeSession(t, "coordinator.jsonl", []agentcore.Message{commitCall(`7`)})
	si := store.NewStore(intDir)
	repInt, rcInt := Diagnose(si)
	outInt := string(RenderExport(repInt, rcInt))
	if !strings.Contains(outInt, "chapter: 7") || strings.Contains(outInt, `chapter: "7"`) {
		t.Errorf("Numeric parameter should be rendered as chapter: 7 (without quotes)\n%s", outInt)
	}
}

// TestProjectValue_ProseArgRedacted guards desensitization boundary: identifier-type short values are kept,
// while CJK/spaced short values (like dispatch task, chapter title) are always redacted.
func TestProjectValue_ProseArgRedacted(t *testing.T) {
	keep := map[string]string{
		`"7"`:       `"7"`,       // Stringified number (#34 signal)
		`"premise"`: `"premise"`, // Enum
		`"writer"`:  `"writer"`,  // Role name
		`7`:         `7`,         // Numeric scalar
		`true`:      `true`,      // Boolean scalar
	}
	for in, want := range keep {
		if got := projectValue([]byte(in)); got != want {
			t.Errorf("Should keep %s: got %q want %q", in, got, want)
		}
	}
	// Containing CJK / spaces -> must be redacted, and not contain the original prose.
	prose := []string{`"Chapter 7 truth of the snowy night"`, `"murderous intent on snowy night"`, `"protagonist uncovers conspiracy"`}
	for _, in := range prose {
		got := projectValue([]byte(in))
		if !strings.HasPrefix(got, "<redacted") {
			t.Errorf("CJK/spaced short values should be redacted: %s -> %q", in, got)
		}
		if strings.Contains(got, "snowy") || strings.Contains(got, "protagonist") {
			t.Errorf("Still contains prose after redacting: %s -> %q", in, got)
		}
	}
}

// TestWriteExport_WritesFile proves pure function path: does not depend on TUI, writes to fixed relative path.
func TestWriteExport_WritesFile(t *testing.T) {
	dir := writeSession(t, "coordinator.jsonl", []agentcore.Message{commitCall(`"7"`), errResult("boom")})
	s := store.NewStore(dir)

	rep, rc := Diagnose(s)
	path, err := WriteExport(s, rep, rc)
	if err != nil {
		t.Fatalf("WriteExport: %v", err)
	}
	if want := filepath.Join(dir, filepath.FromSlash(ExportRelPath)); path != want {
		t.Errorf("Incorrect path: got %s want %s", path, want)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(data), "diag-export") {
		t.Errorf("Abnormal file content\n%s", data)
	}
	if strings.Contains(string(data), sentinel) {
		t.Errorf("Written file contains prose")
	}
}

// TestRedactMessage_DupSha proves that the same text repeatedly appearing produces the same sha (loop signal).
func TestRedactMessage_DupSha(t *testing.T) {
	a := redactMessage("coordinator", agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(sentinel)},
	})
	b := redactMessage("coordinator", agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(sentinel)},
	})
	if a.TextSha == "" || a.TextSha != b.TextSha {
		t.Errorf("Same prose should yield same sha: %q vs %q", a.TextSha, b.TextSha)
	}
	if a.Redacted != 1 {
		t.Errorf("Expected 1 redacted text block, got %d", a.Redacted)
	}
}
