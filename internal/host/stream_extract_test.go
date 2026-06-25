package host

import (
	"strings"
	"testing"
)

// feedAll feeds the input at once and returns the accumulated output.
func feedAll(t *testing.T, tool, input string) string {
	t.Helper()
	e := newToolExtractor(tool)
	if e == nil {
		t.Fatalf("no extractor for tool %q", tool)
	}
	return e.Feed(input)
}

// feedChunked feeds the input split by the specified number of bytes, verifying that the streaming result matches feedAll.
func feedChunked(t *testing.T, tool, input string, chunk int) string {
	t.Helper()
	e := newToolExtractor(tool)
	if e == nil {
		t.Fatalf("no extractor for tool %q", tool)
	}
	var b strings.Builder
	for i := 0; i < len(input); i += chunk {
		end := min(i+chunk, len(input))
		b.WriteString(e.Feed(input[i:end]))
	}
	return b.String()
}

func mustContain(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("expected substring %q in:\n---\n%s\n---", want, got)
	}
}

func mustNotContain(t *testing.T, got, want string) {
	t.Helper()
	if strings.Contains(got, want) {
		t.Errorf("unexpected substring %q in:\n---\n%s\n---", want, got)
	}
}

// ── Common mode: flat obj ──

func TestExtract_PlanChapter(t *testing.T) {
	in := `{"chapter":1,"title":"Indentures","goal":"Establish mine baseline","conflict":"Father's debt","hook":"Gray ore","emotion_arc":"Depressing"}`
	out := feedAll(t, "plan_chapter", in)
	mustContain(t, out, "✻ Plan")
	mustContain(t, out, "chapter: 1")
	mustContain(t, out, "title: Indentures")
	mustContain(t, out, "goal: Establish mine baseline")
	mustContain(t, out, "conflict: Father's debt")
	mustContain(t, out, "hook: Gray ore")
	mustContain(t, out, "emotion_arc: Depressing")
}

// ── Common mode: nested obj + array ──

func TestExtract_FoundationCharacters(t *testing.T) {
	in := `{"type":"characters","scale":"long","content":[` +
		`{"name":"Shen Li","role":"Protagonist","aliases":["Gray Pulse","Shen Qi"],"description":"Wilderness youth.","traits":["Restraint","Suspicious"]},` +
		`{"name":"Gu Xiaodeng","role":"Key supporting character","description":"Medicine workshop test girl."}` +
		`]}`
	out := feedAll(t, "save_foundation", in)
	mustContain(t, out, "✻ Setting")
	mustContain(t, out, "type: characters")
	mustContain(t, out, "scale: long")
	// Common rendering: show all fields, including aliases and traits which were previously skipped by whitelist
	mustContain(t, out, "name: Shen Li")
	mustContain(t, out, "role: Protagonist")
	mustContain(t, out, "aliases:")
	mustContain(t, out, "- Gray Pulse")
	mustContain(t, out, "- Shen Qi")
	mustContain(t, out, "description: Wilderness youth.")
	mustContain(t, out, "traits:")
	mustContain(t, out, "- Restraint")
	mustContain(t, out, "- Suspicious")
	mustContain(t, out, "name: Gu Xiaodeng")
	mustContain(t, out, "role: Key supporting character")
}

func TestExtract_FoundationLayeredOutline(t *testing.T) {
	in := `{"type":"layered_outline","content":[` +
		`{"index":1,"title":"Dim Mine Fire","arcs":[` +
		`{"index":1,"title":"Black Scale Mine Service","goal":"Survive","chapters":[` +
		`{"chapter":1,"title":"Indentures","core_event":"Sold to the mine."}` +
		`]}]}]}`
	out := feedAll(t, "save_foundation", in)
	mustContain(t, out, "type: layered_outline")
	// Volume
	mustContain(t, out, "index: 1")
	mustContain(t, out, "title: Dim Mine Fire")
	// Arc
	mustContain(t, out, "title: Black Scale Mine Service")
	mustContain(t, out, "goal: Survive")
	// Chapter
	mustContain(t, out, "chapter: 1")
	mustContain(t, out, "title: Indentures")
	mustContain(t, out, "core_event: Sold to the mine.")
	// Indentation reflects nesting structure
	mustContain(t, out, "arcs:\n")
	mustContain(t, out, "chapters:\n")
}

func TestExtract_FoundationUpdateCompass(t *testing.T) {
	in := `{"type":"update_compass","content":{"ending_direction":"Ascend alone vs cut blood sacrifice","open_threads":["Gray pulse key","Living bill ledger"],"estimated_scale":"5-6 volumes"}}`
	out := feedAll(t, "save_foundation", in)
	mustContain(t, out, "type: update_compass")
	mustContain(t, out, "ending_direction: Ascend alone vs cut blood sacrifice")
	mustContain(t, out, "estimated_scale: 5-6 volumes")
	mustContain(t, out, "open_threads:")
	mustContain(t, out, "- Gray pulse key")
	mustContain(t, out, "- Living bill ledger")
}

// ── save_review: contains array of objects + number array ──

func TestExtract_SaveReview(t *testing.T) {
	in := `{"chapter":3,"scope":"chapter","verdict":"polish","summary":"Slightly slow pace.","dimensions":[{"dimension":"hook","score":55,"verdict":"fail"}],"issues":[{"type":"hook","severity":"error","description":"Missing hook at chapter end."}],"affected_chapters":[3,4]}`
	out := feedAll(t, "save_review", in)
	mustContain(t, out, "✻ Review")
	mustContain(t, out, "verdict: polish")
	mustContain(t, out, "summary: Slightly slow pace.")
	mustContain(t, out, "dimension: hook")
	mustContain(t, out, "score: 55")
	mustContain(t, out, "verdict: fail")
	mustContain(t, out, "type: hook")
	mustContain(t, out, "severity: error")
	mustContain(t, out, "description: Missing hook at chapter end.")
	mustContain(t, out, "- 3")
	mustContain(t, out, "- 4")
}

// ── commit_chapter: complex nesting ──

func TestExtract_CommitChapter(t *testing.T) {
	in := `{"chapter":1,"summary":"Sold into the mine.","characters":["Shen Li","Mother"],"key_events":["Sign indentures"],"foreshadow_updates":[{"id":"f1","action":"plant","description":"Gray ore heats up."}],"state_changes":[{"entity":"Shen Li","field":"Identity","old_value":"Herb gatherer youth","new_value":"Mine handyman"}]}`
	out := feedAll(t, "commit_chapter", in)
	mustContain(t, out, "✻ Commit Chapter")
	mustContain(t, out, "summary: Sold into the mine.")
	mustContain(t, out, "- Shen Li")
	mustContain(t, out, "- Mother")
	mustContain(t, out, "- Sign indentures")
	mustContain(t, out, "id: f1")
	mustContain(t, out, "action: plant")
	mustContain(t, out, "description: Gray ore heats up.")
	mustContain(t, out, "entity: Shen Li")
	mustContain(t, out, "field: Identity")
	mustContain(t, out, "old_value: Herb gatherer youth")
	mustContain(t, out, "new_value: Mine handyman")
}

// ── edit_chapter: common mode + multiline string ──

func TestExtract_EditChapter(t *testing.T) {
	in := `{"chapter":24,"old_string":"Shen Li lowered his head and remained silent.\nHe clenched his fists.","new_string":"Shen Li did not look up, his Adam's apple rolled once.\nHis knuckles were clenched white.","replace_all":false}`
	out := feedAll(t, "edit_chapter", in)
	mustContain(t, out, "✻ Polish")
	mustContain(t, out, "chapter: 24")
	mustContain(t, out, "old_string: Shen Li lowered his head and remained silent.\nHe clenched his fists.")
	mustContain(t, out, "new_string: Shen Li did not look up, his Adam's apple rolled once.\nHis knuckles were clenched white.")
	mustContain(t, out, "replace_all: false")
}

// ── Read tools: args density is low but header + key fields should still be visible ──

func TestExtract_ReadChapter(t *testing.T) {
	in := `{"chapter":234,"source":"final"}`
	out := feedAll(t, "read_chapter", in)
	mustContain(t, out, "✻ Read Chapter")
	mustContain(t, out, "chapter: 234")
	mustContain(t, out, "source: final")
}

func TestExtract_CheckConsistency(t *testing.T) {
	out := feedAll(t, "check_consistency", `{"chapter":234}`)
	mustContain(t, out, "✻ Consistency Check")
	mustContain(t, out, "chapter: 234")
}

// Fallback for empty args: coordinator calls novel_context with no params (args is {}).
// It cannot be completely silent, must at least output header so user notices the call.
func TestExtract_NovelContextEmptyArgs(t *testing.T) {
	out := feedAll(t, "novel_context", `{}`)
	mustContain(t, out, "✻ Query Context")
}

func TestExtract_NovelContextWithChapter(t *testing.T) {
	out := feedAll(t, "novel_context", `{"chapter":234}`)
	mustContain(t, out, "✻ Query Context")
	mustContain(t, out, "chapter: 234")
}

// ── Naked flow mode ──

func TestExtract_DraftChapterRawMarkdown(t *testing.T) {
	in := `{"chapter":1,"content":"# Chapter 1\n\nShen Li stood at the mine entrance.\n"}`
	out := feedAll(t, "draft_chapter", in)
	// Naked flow: no decoration, no key prefix
	mustNotContain(t, out, "【")
	mustNotContain(t, out, "content:")
	mustNotContain(t, out, "chapter:")
	mustContain(t, out, "# Chapter 1")
	mustContain(t, out, "Shen Li stood at the mine entrance.")
}

func TestExtract_DraftChapterIgnoresOtherFields(t *testing.T) {
	// content outside fields should be silently skipped, not polluting output
	in := `{"chapter":7,"summary":"meta","content":"Body Text","extra_array":[1,2,3]}`
	out := feedAll(t, "draft_chapter", in)
	mustContain(t, out, "Body Text")
	mustNotContain(t, out, "meta")
	mustNotContain(t, out, "summary")
	mustNotContain(t, out, "7")
	mustNotContain(t, out, "1")
}

// ── Behavioral invariants ──

func TestExtract_UnknownTool(t *testing.T) {
	if e := newToolExtractor("nonexistent_tool"); e != nil {
		t.Errorf("expected nil for unknown tool")
	}
}

func TestExtract_DoneAfterClose(t *testing.T) {
	e := newToolExtractor("plan_chapter")
	e.Feed(`{"title":"x"}`)
	if !e.Done() {
		t.Error("expected Done after closing brace")
	}
}

// ── Streaming chunked invariance ──

// Same input chunked by 1/3/7/13 bytes, output should be exactly the same as feeding at once.
func TestExtract_ChunkedEqualsWhole(t *testing.T) {
	cases := []struct {
		tool  string
		input string
	}{
		{"plan_chapter", `{"title":"Indentures","goal":"Goal","conflict":"Father's debt","hook":"Gray ore","emotion_arc":"Depressing"}`},
		{"save_foundation", `{"type":"characters","content":[{"name":"Shen Li","role":"Protagonist","aliases":["Gray Pulse","Shen Qi"]}]}`},
		{"save_foundation", `{"type":"layered_outline","content":[{"index":1,"title":"Mine Fire","arcs":[{"index":1,"title":"Mine Service","goal":"Survive","chapters":[{"chapter":1,"title":"Indentures"}]}]}]}`},
		{"save_review", `{"verdict":"accept","summary":"good","dimensions":[{"dimension":"hook","score":85,"verdict":"pass"}],"issues":[]}`},
		{"draft_chapter", `{"chapter":1,"content":"# Chapter 1\n\nBody text.\n"}`},
	}
	for _, tc := range cases {
		whole := feedAll(t, tc.tool, tc.input)
		for _, chunk := range []int{1, 3, 7, 13} {
			got := feedChunked(t, tc.tool, tc.input, chunk)
			if got != whole {
				t.Errorf("tool=%s chunk=%d differs from whole\n--- whole ---\n%s\n--- chunked ---\n%s", tc.tool, chunk, whole, got)
			}
		}
	}
}

// ── Escape sequences and Unicode ──

func TestExtract_EscapeSequences(t *testing.T) {
	in := `{"goal":"Line 1\nLine 2 \"Quote\" \\Backslash Chinese"}`
	out := feedAll(t, "plan_chapter", in)
	mustContain(t, out, "Line 1\nLine 2")
	mustContain(t, out, `"Quote"`)
	mustContain(t, out, `\Backslash`)
	mustContain(t, out, "Chinese")
}

func TestExtract_UnicodeEscape(t *testing.T) {
	// Chinese = Chinese
	in := `{"goal":"Chinese"}`
	out := feedAll(t, "plan_chapter", in)
	mustContain(t, out, "Chinese")
}

// ── Empty container / simple structure ──

func TestExtract_EmptyArrays(t *testing.T) {
	in := `{"key_events":[],"characters":["Shen Li"]}`
	out := feedAll(t, "commit_chapter", in)
	mustContain(t, out, "key_events:")
	mustContain(t, out, "characters:")
	mustContain(t, out, "- Shen Li")
}

func TestExtract_BoolAndNull(t *testing.T) {
	in := `{"foreshadow_updates":[{"id":"f1","action":"plant","description":null}],"chapter":1,"summary":"x","characters":["a"],"key_events":["b"]}`
	out := feedAll(t, "commit_chapter", in)
	mustContain(t, out, "id: f1")
	mustContain(t, out, "action: plant")
	mustContain(t, out, "description: null")
}

// ── Edge cases: nested arrays, deeply nested ──

func TestExtract_NestedArrays(t *testing.T) {
	// affected_chapters is an int array; verify here with an array of arrays
	in := `{"summary":"x","key_events":[],"characters":["a"],"foreshadow_updates":[],"relationship_changes":[]}`
	out := feedAll(t, "commit_chapter", in)
	mustContain(t, out, "summary: x")
	mustContain(t, out, "key_events:")
	mustContain(t, out, "- a")
}

func TestExtract_DeeplyNested(t *testing.T) {
	in := `{"a":{"b":{"c":{"d":"deep"}}}}`
	e := newToolExtractor("plan_chapter")
	out := e.Feed(in)
	mustContain(t, out, "a:")
	mustContain(t, out, "b:")
	mustContain(t, out, "c:")
	mustContain(t, out, "d: deep")
	if !e.Done() {
		t.Error("expected Done after final closing brace")
	}
}

// ── chunk split in middle of utf-8 multi-byte char ──

func TestExtract_ChunkSplitInUTF8(t *testing.T) {
	// A 3-byte UTF-8 character (e.g., E4 B8 AD) is used. Set chunk size to 1 to ensure each byte is fed individually.
	in := `{"goal":"Chinese test"}`
	whole := feedAll(t, "plan_chapter", in)
	chunked := feedChunked(t, "plan_chapter", in, 1)
	if whole != chunked {
		t.Errorf("byte-by-byte chunked output differs from whole:\n--- whole ---\n%s\n--- chunked ---\n%s", whole, chunked)
	}
	mustContain(t, chunked, "Chinese test")
}

// ── Naked flow mode: same-name key inside nested obj should not be falsely hit ──

func TestExtract_NakedKeyOnlyTopLevel(t *testing.T) {
	// "content" appears in two places: nested obj + top level. Only the top level one should stream out.
	in := `{"meta":{"content":"nested should not output"},"content":"top level should output"}`
	out := feedAll(t, "draft_chapter", in)
	mustContain(t, out, "top level should output")
	mustNotContain(t, out, "nested should not output")
}

// ── Naked flow mode: skip all when content is non-string ──

func TestExtract_NakedKeyNonStringValue(t *testing.T) {
	// content mistyped as object (unlikely to happen but should tolerate)
	in := `{"content":{"unexpected":true}}`
	out := feedAll(t, "draft_chapter", in)
	if out != "" {
		t.Errorf("expected empty output, got: %q", out)
	}
}

// ── No more output when feeding after top level is closed ──

func TestExtract_FeedAfterDone(t *testing.T) {
	e := newToolExtractor("plan_chapter")
	e.Feed(`{"title":"x"}`)
	if !e.Done() {
		t.Fatal("expected Done")
	}
	if got := e.Feed(`junk`); got != "" {
		t.Errorf("expected empty output after Done, got: %q", got)
	}
}

// ── Empty chunk / empty input ──

func TestExtract_EmptyFeed(t *testing.T) {
	e := newToolExtractor("plan_chapter")
	if got := e.Feed(""); got != "" {
		t.Errorf("expected empty output for empty feed, got: %q", got)
	}
	if e.Done() {
		t.Error("Done should be false before any input")
	}
}

// ── Directly nested array in array (without obj) ──

func TestExtract_ArrayOfArrays(t *testing.T) {
	in := `{"matrix":[[1,2],[3,4]]}`
	out := feedAll(t, "plan_chapter", in)
	mustContain(t, out, "matrix:")
	mustContain(t, out, "- 1")
	mustContain(t, out, "- 2")
	mustContain(t, out, "- 3")
	mustContain(t, out, "- 4")
}

// ── Trailing space after number before separator ──

func TestExtract_NumberWithTrailingSpace(t *testing.T) {
	// "chapter": 1 ,  <- spaces around number
	in := `{ "chapter" : 1 , "title" : "x" }`
	out := feedAll(t, "plan_chapter", in)
	mustContain(t, out, "chapter: 1")
	mustContain(t, out, "title: x")
}
