package diag

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/store"
)

// SkelEvent is a behavioral skeleton of a session message after redaction: structural signals
// (role / tool / error / repeated fingerprint) are preserved, while all free text (body text,
// prompt, thinking) is redacted. This is a stricter projection layer than store.compactMessage—
// the latter compresses by volume (>4KB), whereas here volume is ignored and no raw text is exposed.
type SkelEvent struct {
	Agent    string     // Source session: coordinator / writer-ch07 ...
	Role     string     // assistant / tool / user
	Tools    []SkelTool // Tool calls within this message
	ErrClass string     // role=tool and is_error: first line of error (framework error string, no raw text)
	TextSha  string     // Short hash of the redacted body text; identical sha = repeated generation of the same segment (loop signal)
	Redacted int        // Number of redacted text/thinking blocks in this message (used for self-check)
}

// SkelTool is a redacted projection of a tool call.
type SkelTool struct {
	Name     string            // Tool name (structural signal, no raw text)
	Args     map[string]string // key -> scalar raw value / short quoted string / "<redacted len sha>"
	Invalid  bool              // ArgsInvalid: Arguments sent by the model cannot be parsed (#34 signal)
	ParseErr string            // ArgsParseError: Reason for parsing failure
}

// redactMessage projects an agentcore.Message into a behavioral skeleton.
func redactMessage(agent string, m agentcore.Message) SkelEvent {
	ev := SkelEvent{Agent: agent, Role: string(m.Role)}
	isErr, _ := m.Metadata["is_error"].(bool)

	var text strings.Builder
	for _, b := range m.Content {
		switch b.Type {
		case agentcore.ContentText:
			// Keep the first line of the tool error result: this is our own error string
			// (e.g. InputValidationError), containing no raw text, and is key to locating loops.
			// All other text goes into the redaction pool.
			if m.Role == agentcore.RoleTool && isErr && ev.ErrClass == "" {
				ev.ErrClass = firstLine(b.Text, 160)
				continue
			}
			if strings.TrimSpace(b.Text) != "" {
				text.WriteString(b.Text)
				ev.Redacted++
			}
		case agentcore.ContentThinking:
			if strings.TrimSpace(b.Thinking) != "" {
				text.WriteString(b.Thinking)
				ev.Redacted++
			}
		case agentcore.ContentToolCall:
			if b.ToolCall != nil {
				ev.Tools = append(ev.Tools, redactToolCall(b.ToolCall))
			}
		}
	}
	if t := text.String(); t != "" {
		ev.TextSha = shortHash(t)
	}
	return ev
}

// redactToolCall projects a tool call: tool name + arguments (redacted value) + parsing anomaly flags.
func redactToolCall(tc *agentcore.ToolCall) SkelTool {
	return SkelTool{
		Name:     tc.Name,
		Args:     redactArgs(tc.Args),
		Invalid:  tc.ArgsInvalid,
		ParseErr: tc.ArgsParseError,
	}
}

// redactArgs projects a tool arguments object into key -> redacted value.
// Returns nil for non-object arguments (ArgsInvalid/ParseErr are recorded separately in SkelTool).
func redactArgs(raw json.RawMessage) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = projectValue(v)
	}
	return out
}

// projectValue projects a single parameter value by JSON type:
//   - Scalar (number / bool / null): Raw value is a structural signal and is kept (chapter: 7)
//   - Short identifier-style string: Kept with quotes to expose its type (chapter: "7" <- stringified number signal of #34)
//   - String containing CJK / space / long text, objects, arrays: Redacted to <redacted ...> (zero raw text exposed)
//   - Already a [session_compact: ...] placeholder: Safe and informative, kept as is
func projectValue(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return ""
	}
	switch s[0] {
	case '"':
		var str string
		if err := json.Unmarshal(raw, &str); err != nil {
			return redactPlaceholder(s)
		}
		if strings.HasPrefix(str, store.CompactTag) {
			return str
		}
		// Keep only short values that "look like an identifier/number/enum" (chapter:"7", type:"premise", agent:"writer");
		// Any string containing CJK, spaces, or other symbols is treated as body text and redacted.
		if utf8.RuneCountInString(str) <= 32 && isStructuralToken(str) {
			return strconv.Quote(str)
		}
		return redactPlaceholder(str)
	case '{':
		return fmt.Sprintf("<redacted object len=%d>", len(raw))
	case '[':
		return fmt.Sprintf("<redacted array len=%d>", len(raw))
	default:
		return s
	}
}

// isStructuralToken checks if a string "looks like an identifier"—pure ASCII alphanumeric or `_-.:/`,
// with no spaces or CJK characters. Used to distinguish structural signals (kept) from body text (redacted).
func isStructuralToken(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.' || r == ':' || r == '/':
		default:
			return false
		}
	}
	return true
}

func redactPlaceholder(s string) string {
	return fmt.Sprintf("<redacted len=%d sha=%s>", utf8.RuneCountInString(s), shortHash(s))
}

// shortHash takes a short hash of the text; used only to detect if the same text repeats, not for cryptographic purposes.
func shortHash(s string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return fmt.Sprintf("%08x", h.Sum32())
}

// firstLine takes the first line and truncates it by rune for error string summaries.
func firstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\n\r"); i >= 0 {
		s = s[:i]
	}
	if utf8.RuneCountInString(s) > max {
		r := []rune(s)
		s = string(r[:max]) + "…"
	}
	return s
}
