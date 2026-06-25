package host

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/store"
)

// Cold start co-creation: clarify requirements from scratch, outputting writing instructions for the entire book.
const coCreateSystemPrompt = `You are a novel co-creation assistant. Your task is not to write the novel directly, but to help the user clarify writing requirements through multiple rounds of brief dialogue, and continuously compile a set of writing instructions that can be directly handed over to the writing engine.

Each round of reply must strictly follow the XML format below, containing four tags in order, and each tag must have correct open and close tags:

<reply>
Natural response to show the user: first respond to the user's input, then propose at most 1 to 2 of the most critical questions at present. If there is enough information to start writing, tell the user that they can press Ctrl+S to start.
</reply>

<draft>
The current complete draft of writing instructions using Markdown: start directly with level-2 headings, such as "## Theme", "## Key Elements", "## Information to Clarify"; use bullet points to list key details. Each round must be cumulatively updated on top of existing conclusions, absorbing the user's latest intentions; even if there are no new additions in this round, the complete draft must be rewritten as is—do not omit or use placeholders like "(keep previous round)".
</draft>
` + coCreateProtocolTail

// Stage co-creation: The novel has been partially written, planning the direction of "subsequent stages". The caller needs
// to append the current story status summary after this prompt ("## Current Story Status" section) so the model plans on top of what's written.
const stageCoCreateSystemPrompt = `You are a novel "stage co-creation" assistant. This novel has already been partially written (progress is shown in the "Current Story Status" below). The user has paused and wants to plan the direction of "subsequent stages" with you before continuing.

Your task is not to continue writing the text, but to help the user clarify the next segment (next few chapters / next arc / next volume) through multiple rounds of brief dialogue, and continuously compile a "subsequent direction brief" for the writing engine to proceed.

Ironclad rule: all suggestions must be consistent with the plot, characters, and foreshadowing that have already occurred in the "Current Story Status", and must never overturn or ignore already written content; only plan "how to go next", do not redesign the entire book.

Each round of reply must strictly follow the XML format below, containing four tags in order, and each tag must have correct open and close tags:

<reply>
Natural response to show the user: first respond to the user's input, then propose at most 1 to 2 of the most critical questions at present. If the subsequent direction is clear enough, tell the user they can press Ctrl+S to hand the direction to the writing engine and continue writing.
</reply>

<draft>
The current complete "subsequent direction brief" using Markdown: start directly with level-2 headings, such as "## Subsequent Direction", "## Key Turning Points", "## Foreshadowing to Resolve", "## Pace and Length"; use bullet points to list key details. Each round must be cumulatively updated on top of existing conclusions, absorbing the user's latest intentions; even if there are no new additions in this round, the complete brief must be rewritten as is—do not omit or use placeholders like "(keep previous round)".
</draft>
` + coCreateProtocolTail

// coCreateProtocolTail is the shared output protocol tail for both co-creation modes (<ready> / <suggestions> + output specifications).
// The two modes differ only in opening context and <draft> semantics; the protocol is identical.
const coCreateProtocolTail = `
<ready>false</ready>

<suggestions>
1-3 lines of "what the user might want to say next", each line starting with "- ". This is a guide when the user is stuck. The user can enter the number key to populate the input box, and edit before sending.

Requirements:
- Written from the user's perspective, like words the user would say to you, do not write them as questions from the assistant.
- Each suggestion must not exceed 25 words, use diverse sentence structures, and avoid being repetitive.
- Provide inclinations / choices / supplementary intentions, do not write the complete settings for the user in one sentence.
</suggestions>

Output specifications:
- Must use four XML tags: <reply> / <draft> / <ready> / <suggestions>, each with complete open and close tags.
- Tag names must be lowercase English, do not rewrite them to any variant like <REPLY>, <REWRITE>, <reply_msg>, etc.
- Do not add any explanation, thinking logs, or code fences outside the tags.
- <draft> allows multiple lines of Markdown, write with direct line breaks without any escaping.
- <ready> must only be true or false. Set to true when information is sufficient.
- When <ready> is true, <suggestions> can be empty (just keep the empty tag <suggestions></suggestions>).`

// CoCreateProgressKind identifies the content type of streaming callbacks.
const (
	CoCreateProgressThinking = "thinking"
	CoCreateProgressReply    = "reply"
)

// Four-part XML tag output. XML style is more robust than bracket markers—there is a large amount of format like
// <thinking>...</thinking> in Claude/GPT training data, making the model highly unlikely to rewrite <reply> to <REWRITE>
// or other variants; closing tags also make streaming truncation more precise (no need to find the next marker to cut the tail).
const (
	tagReply       = "reply"
	tagDraft       = "draft"
	tagReady       = "ready"
	tagSuggestions = "suggestions"
)

func coCreateStream(ctx context.Context, models *bootstrap.ModelSet, sessions *store.SessionStore, sysPrompt string, history []CoCreateMessage, onProgress func(kind, text string)) (reply CoCreateReply, err error) {
	if len(history) == 0 {
		return CoCreateReply{}, fmt.Errorf("cocreate history is empty")
	}

	model := models.ForRole("thinking")
	ctx, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()

	msgs := []agentcore.Message{agentcore.SystemMsg(sysPrompt)}
	for _, item := range history {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(item.Role)) {
		case "assistant":
			msgs = append(msgs, assistantMsg(content))
		default:
			msgs = append(msgs, agentcore.UserMsg(content))
		}
	}

	var raw, thinking strings.Builder

	// Troubleshooting occasional issues like "cocreate empty response" requires seeing what the model actually returned.
	// Each round is fully logged to <output>/meta/sessions/cocreate.jsonl, aligned with the formal writing session logs.
	start := time.Now()
	defer func() {
		if sessions == nil {
			return
		}
		_ = sessions.LogCoCreate(coCreateLogEntry{
			Time:         time.Now(),
			DurationMS:   time.Since(start).Milliseconds(),
			InputHistory: history,
			RawResponse:  raw.String(),
			RawLen:       len([]rune(raw.String())),
			Thinking:     thinking.String(),
			ParsedReply:  reply.Message,
			ParsedDraft:  reply.Prompt,
			ParsedReady:  reply.Ready,
			ParsedSugs:   reply.Suggestions,
			Error:        errString(err),
		})
	}()

	streamCh, err := model.GenerateStream(ctx, msgs, nil, agentcore.WithMaxTokens(2048))
	if err != nil {
		return CoCreateReply{}, fmt.Errorf("cocreate generate: %w", err)
	}

	var streamed bool
	for ev := range streamCh {
		switch ev.Type {
		case agentcore.StreamEventThinkingDelta:
			thinking.WriteString(ev.Delta)
			if onProgress != nil {
				onProgress(CoCreateProgressThinking, thinking.String())
			}
		case agentcore.StreamEventTextDelta:
			streamed = true
			raw.WriteString(ev.Delta)
			if onProgress != nil {
				onProgress(CoCreateProgressReply, extractReplyPreview(raw.String()))
			}
		case agentcore.StreamEventDone:
			if !streamed {
				raw.WriteString(ev.Message.TextContent())
			}
		case agentcore.StreamEventError:
			if ev.Err != nil {
				return CoCreateReply{}, fmt.Errorf("cocreate generate: %w", ev.Err)
			}
			return CoCreateReply{}, fmt.Errorf("cocreate generate failed")
		}
	}

	// Channel fallback: Reasoning models (like R1/GLM-Z1/QwQ) occasionally write the entire answer into
	// reasoning_content and fail to switch back to the final answer channel, leading to empty raw text but thinking containing
	// the complete four sections. As observed in meta/sessions/cocreate.jsonl—we directly parse thinking as raw text,
	// which is handled by the protocol fallback (treating the whole text as reply if no [REPLY] marker is found), ensuring no UI difference.
	rawText := raw.String()
	if strings.TrimSpace(rawText) == "" {
		if t := strings.TrimSpace(thinking.String()); t != "" {
			rawText = t
		}
	}
	reply, err = parseCoCreateResponse(rawText)
	return reply, err
}

// coCreateLogEntry is the structure of a line written to meta/sessions/cocreate.jsonl.
// Field naming aligns with jsonl direct querying habits (snake_case) to facilitate jq filtering.
type coCreateLogEntry struct {
	Time         time.Time         `json:"time"`
	DurationMS   int64             `json:"duration_ms"`
	InputHistory []CoCreateMessage `json:"input_history"`
	RawResponse  string            `json:"raw_response"`
	RawLen       int               `json:"raw_len"`
	Thinking     string            `json:"thinking,omitempty"`
	ParsedReply  string            `json:"parsed_reply"`
	ParsedDraft  string            `json:"parsed_draft"`
	ParsedReady  bool              `json:"parsed_ready"`
	ParsedSugs   []string          `json:"parsed_sugs,omitempty"`
	Error        string            `json:"error,omitempty"`
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func assistantMsg(text string) agentcore.Message {
	return agentcore.Message{
		Role:      agentcore.RoleAssistant,
		Content:   []agentcore.ContentBlock{agentcore.TextBlock(text)},
		Timestamp: time.Now(),
	}
}

// parseCoCreateResponse parses the XML tag output. If the model does not adhere to the protocol (speaks natural language directly),
// the entire block is displayed as reply, leaving draft empty so the session keeps the previous round.
func parseCoCreateResponse(raw string) (CoCreateReply, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return CoCreateReply{}, fmt.Errorf("cocreate empty response")
	}

	reply, draft, ready, suggestions := splitCoCreateMarkers(raw)
	if reply == "" {
		// The model did not follow the XML protocol: treat the entire text as reply.
		return CoCreateReply{Message: raw, Prompt: "", Ready: false, Raw: raw}, nil
	}
	return CoCreateReply{
		Message:     reply,
		Prompt:      draft,
		Ready:       ready,
		Suggestions: suggestions,
		Raw:         raw,
	}, nil
}

// splitCoCreateMarkers splits the text by four XML tags.
// Tags may be missing (mid-stream or model omission), and the missing parts correspond to empty/false/nil fields.
// If a closing tag is missing, extractTagContent will capture until the end of the string, still attempting to parse as much as possible.
func splitCoCreateMarkers(s string) (reply, draft string, ready bool, suggestions []string) {
	reply = extractTagContent(s, tagReply)
	draft = extractTagContent(s, tagDraft)
	readyStr := strings.ToLower(extractTagContent(s, tagReady))
	ready = readyStr == "true" || readyStr == "yes"
	suggestions = parseSuggestions(extractTagContent(s, tagSuggestions))
	return
}

// extractTagContent extracts the text between <tag>...</tag> from s.
// It handles three occasional failure scenarios to avoid dropping fields due to degradation:
//  1. Open without close (mid-stream) -> Cut before the next known open tag
//  2. Close without open (model typo, e.g., <suggestions> written as <uggestions>) -> Start from the end of the nearest known complete closing tag up to </tag>
//  3. reply has no open tag at all (model starts with natural language directly, and appends </reply> at the end) -> From start up to </reply>
func extractTagContent(s, tag string) string {
	open := "<" + tag + ">"
	closeTag := "</" + tag + ">"
	oIdx := strings.Index(s, open)
	if oIdx >= 0 {
		rest := s[oIdx+len(open):]
		if cIdx := strings.Index(rest, closeTag); cIdx >= 0 {
			return strings.TrimSpace(rest[:cIdx])
		}
		// Open without close -> Cut before the next known open tag
		for _, other := range []string{"<reply>", "<draft>", "<ready>", "<suggestions>"} {
			if other == open {
				continue
			}
			if idx := strings.Index(rest, other); idx >= 0 {
				rest = rest[:idx]
			}
		}
		return strings.TrimSpace(rest)
	}

	// Close without open -> start from the end of the nearest known complete closing tag to before </tag>.
	if cIdx := strings.Index(s, closeTag); cIdx >= 0 {
		prefix := s[:cIdx]
		start := 0
		for _, t := range []string{"</reply>", "</draft>", "</ready>", "</suggestions>"} {
			if t == closeTag {
				continue
			}
			if i := strings.LastIndex(prefix, t); i >= 0 {
				if end := i + len(t); end > start {
					start = end
				}
			}
		}
		return strings.TrimSpace(prefix[start:])
	}
	return ""
}

// parseSuggestions extracts each line of the <suggestions> section, stripping list prefixes like "- ", "* ", or "1. ".
// At most 3 suggestions are kept; empty lines, too short lines (<2 characters), or lines looking like XML tags (e.g., <uggestions>) are ignored.
func parseSuggestions(text string) []string {
	if text == "" {
		return nil
	}
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Line looks like XML tag -> skip (prevent typo open tag contamination)
		if strings.HasPrefix(line, "<") && strings.HasSuffix(line, ">") {
			continue
		}
		// Strip list prefix
		switch {
		case strings.HasPrefix(line, "- "):
			line = strings.TrimSpace(line[2:])
		case strings.HasPrefix(line, "* "):
			line = strings.TrimSpace(line[2:])
		case isOrderedSuggestion(line):
			line = stripOrderedPrefix(line)
		}
		if len([]rune(line)) < 2 {
			continue
		}
		out = append(out, line)
		if len(out) >= 3 {
			break
		}
	}
	return out
}

// isOrderedSuggestion checks if the line starts with something like "1. " or "12. " (number + dot + space).
func isOrderedSuggestion(line string) bool {
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	return i > 0 && i+1 < len(line) && line[i] == '.' && line[i+1] == ' '
}

func stripOrderedPrefix(line string) string {
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == 0 || i+1 >= len(line) {
		return line
	}
	return strings.TrimSpace(line[i+2:])
}

// extractReplyPreview provides a streaming preview of the text for the UI while raw is still growing.
// Finds the content after <reply>, cutting before </reply> or the next opening tag <draft>.
// If the model partially complies (omits <reply> opening tag), text from start to </reply> or <draft> is treated as reply.
func extractReplyPreview(raw string) string {
	trimmed := strings.TrimSpace(raw)
	open := "<" + tagReply + ">"
	closeTag := "</" + tagReply + ">"
	draftOpen := "<" + tagDraft + ">"

	rest := trimmed
	if rIdx := strings.Index(trimmed, open); rIdx >= 0 {
		rest = trimmed[rIdx+len(open):]
	}
	if cIdx := strings.Index(rest, closeTag); cIdx >= 0 {
		return strings.TrimSpace(rest[:cIdx])
	}
	if dIdx := strings.Index(rest, draftOpen); dIdx >= 0 {
		rest = rest[:dIdx]
	}
	return strings.TrimSpace(rest)
}
