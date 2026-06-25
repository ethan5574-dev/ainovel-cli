package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/voocel/agentcore/schema"
)

// AskUserResponse contains the results of the user's answers.
type AskUserResponse struct {
	Answers map[string]string // question text -> answers selected by the user
	Notes   map[string]string // question text -> custom input (when selecting "Other")
}

// AskUserHandler blocks waiting for the user's answers, with concrete implementations injected by CLI or TUI.
type AskUserHandler func(ctx context.Context, questions []Question) (*AskUserResponse, error)

// Question represents a single question.
type Question struct {
	Question    string   `json:"question"`
	Header      string   `json:"header"`
	Options     []Option `json:"options"`
	MultiSelect bool     `json:"multiSelect"`
}

// Option represents an available choice.
type Option struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// AskUserTool allows the LLM to present structured questions to the user.
type AskUserTool struct {
	mu      sync.RWMutex
	handler AskUserHandler
}

func NewAskUserTool() *AskUserTool {
	return &AskUserTool{}
}

// SetHandler injects UI callbacks, implemented separately by CLI and TUI.
func (t *AskUserTool) SetHandler(h AskUserHandler) {
	t.mu.Lock()
	t.handler = h
	t.mu.Unlock()
}

func (t *AskUserTool) Name() string  { return "ask_user" }
func (t *AskUserTool) Label() string { return "Ask User" }

// Interactive tool: blocks waiting for the user's answers, obviously cannot be scheduled concurrently.
func (t *AskUserTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *AskUserTool) ConcurrencySafe(_ json.RawMessage) bool { return false }
func (t *AskUserTool) Description() string {
	return "When requirement information is insufficient and the missing details would significantly affect the planning direction, present 1-4 structured questions to the user. Each question must contain header, question, and 2-4 options; the user can choose preset options or supplement freely. The returned result is a readable summary, formatted like: User response: [Length] Long-form; [Focus] Plot progression (Note: No harem). Use this only when it is impossible to stably judge the length, main storyline focus, key constraints, or explicit preferences; do not push questions that can be reasonably inferred by yourself to the user."
}

func (t *AskUserTool) Schema() map[string]any {
	option := schema.Object(
		schema.Property("label", schema.String("Option display text (1-5 words)")).Required(),
		schema.Property("description", schema.String("Description of the option's meaning")).Required(),
	)
	question := schema.Object(
		schema.Property("question", schema.String("Full question text")).Required(),
		schema.Property("header", schema.String("Short label (max 12 characters)")).Required(),
		schema.Property("options", schema.Array("2-4 options", option)).Required(),
		schema.Property("multiSelect", schema.Bool("Whether multi-select is allowed")),
	)
	return schema.Object(
		schema.Property("questions", schema.Array("1-4 questions", question)).Required(),
	)
}

type askUserArgs struct {
	Questions []Question `json:"questions"`
}

func (t *AskUserTool) Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a askUserArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if err := validateQuestions(a.Questions); err != nil {
		return json.Marshal(fmt.Sprintf("Parameter validation failed: %s", err))
	}

	t.mu.RLock()
	h := t.handler
	t.mu.RUnlock()

	if h == nil {
		return json.Marshal("Interactive inquiry is not supported in the current environment; please make a decision and proceed on your own judgment.")
	}

	resp, err := h(ctx, a.Questions)
	if err != nil {
		return json.Marshal(fmt.Sprintf("User interaction failed: %s. Please make a decision and proceed on your own judgment.", err))
	}

	return json.Marshal(formatAnswers(a.Questions, resp))
}

func validateQuestions(questions []Question) error {
	if len(questions) == 0 {
		return fmt.Errorf("at least one question is required")
	}
	if len(questions) > 4 {
		return fmt.Errorf("at most 4 questions, currently %d", len(questions))
	}
	for i, q := range questions {
		if q.Question == "" {
			return fmt.Errorf("question %d: question text cannot be empty", i+1)
		}
		if q.Header == "" {
			return fmt.Errorf("question %d: header cannot be empty", i+1)
		}
		if utf8.RuneCountInString(q.Header) > 12 {
			return fmt.Errorf("question %d: header %q exceeds 12 characters", i+1, q.Header)
		}
		if len(q.Options) < 2 || len(q.Options) > 4 {
			return fmt.Errorf("question %d: 2-4 options required, currently %d", i+1, len(q.Options))
		}
		for j, opt := range q.Options {
			if opt.Label == "" {
				return fmt.Errorf("question %d option %d: label cannot be empty", i+1, j+1)
			}
			if opt.Description == "" {
				return fmt.Errorf("question %d option %d: description cannot be empty", i+1, j+1)
			}
		}
	}
	return nil
}

func formatAnswers(questions []Question, resp *AskUserResponse) string {
	if resp == nil || len(resp.Answers) == 0 {
		return "User did not provide answers; please make a decision and proceed on your own judgment."
	}
	var parts []string
	for _, q := range questions {
		answer, ok := resp.Answers[q.Question]
		if !ok {
			continue
		}
		entry := fmt.Sprintf("[%s] %s", q.Header, answer)
		if note, hasNote := resp.Notes[q.Question]; hasNote {
			entry += " (Supplement: " + note + ")"
		}
		parts = append(parts, entry)
	}
	return fmt.Sprintf("User response: %s", strings.Join(parts, "; "))
}
