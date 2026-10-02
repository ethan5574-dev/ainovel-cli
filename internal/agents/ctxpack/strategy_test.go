package ctxpack

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/voocel/agentcore"
	corecontext "github.com/voocel/agentcore/context"
	"github.com/voocel/ainovel-cli/internal/domain"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

func TestWriterRestoreIncludesOptionalDataWarnings(t *testing.T) {
	s := seededWriterStore(t)
	if err := os.WriteFile(filepath.Join(s.Dir(), "meta", "style_rules.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}

	text, ok, err := buildWriterRestoreText(s, restoreBudgetTokens)
	if err != nil {
		t.Fatalf("辅助数据损坏不应阻止恢复上下文: %v", err)
	}
	if !ok || !strings.Contains(text, "Data Warnings") || !strings.Contains(text, "style_rules") {
		t.Fatalf("恢复上下文应向模型暴露读取告警: %q", text)
	}
}

func TestStoreSummaryCompactApplyFallsBackWhenStoreDataInsufficient(t *testing.T) {
	dir := t.TempDir()
	s := storepkg.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Save(&domain.Progress{
		Phase:             domain.PhaseWriting,
		CurrentChapter:    1,
		TotalChapters:     3,
		CompletedChapters: nil,
	}); err != nil {
		t.Fatalf("Save progress: %v", err)
	}

	strategy := NewStoreSummaryCompact(StoreSummaryCompactConfig{Store: s, KeepRecentTokens: 20})
	msgs := []agentcore.AgentMessage{
		agentcore.UserMsg(strings.Repeat("old context", 40)),
		agentcore.Message{
			Role:    agentcore.RoleAssistant,
			Content: []agentcore.ContentBlock{agentcore.TextBlock(strings.Repeat("old reply", 40))},
		},
	}

	out, result, err := strategy.Apply(context.Background(), msgs, msgs, corecontext.Budget{
		Tokens:    corecontext.EstimateTotal(msgs),
		Window:    64,
		Threshold: 16,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Applied {
		t.Fatal("expected no-op when persistent memory is insufficient")
	}
	if len(out) != len(msgs) {
		t.Fatalf("expected messages unchanged, got %d", len(out))
	}
}

func TestWriterRestorePackRefreshReusesStoreBuilder(t *testing.T) {
	s := seededWriterStore(t)
	pack := &WriterRestorePack{}
	pack.Refresh(s)

	msg, ok, err := pack.buildMessage(restoreBudgetTokens)
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	if !ok {
		t.Fatal("expected restore pack message")
	}
	text := msg.TextContent()
	if !strings.Contains(text, "<post-compact-context>") {
		t.Fatalf("expected wrapped restore context, got %q", text)
	}
	if !strings.Contains(text, "Pending Reviews") {
		t.Fatalf("expected pending review section, got %q", text)
	}
	if !strings.Contains(text, "Current Chapter Plan") {
		t.Fatalf("expected chapter plan section, got %q", text)
	}

	if _, _, err := pack.buildMessage(0); err == nil {
		t.Fatal("expected an explicit error when the restore pack does not fit")
	}
}

func seededWriterStore(t *testing.T) *storepkg.Store {
	t.Helper()

	s := storepkg.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Save(&domain.Progress{
		Phase:             domain.PhaseWriting,
		CurrentChapter:    3,
		TotalChapters:     6,
		CompletedChapters: []int{1, 2},
		Flow:              domain.FlowWriting,
	}); err != nil {
		t.Fatalf("Save progress: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "Chapter 1", CoreEvent: "Opening"},
		{Chapter: 2, Title: "Chapter 2", CoreEvent: "Conflict escalates"},
		{Chapter: 3, Title: "Chapter 3", CoreEvent: "Investigate clues", Scenes: []string{"Protagonist investigates missing person case", "Find old warehouse clues"}},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{
		Chapter:    3,
		Title:      "Chapter 3",
		Goal:       "Advance investigation of the missing case",
		Conflict:   "Protagonist and partner disagree on direction",
		Hook:       "Suspicious recording found in the warehouse",
		EmotionArc: "Suspicion to tension",
	}); err != nil {
		t.Fatalf("SaveChapterPlan: %v", err)
	}
	if err := s.Summaries.SaveSummary(domain.ChapterSummary{
		Chapter:    1,
		Summary:    "Protagonist takes on the commission and finds the missing person case is not simple.",
		Characters: []string{"Lin Lan", "Zhou Ce"},
		KeyEvents:  []string{"Commission established"},
	}); err != nil {
		t.Fatalf("SaveSummary 1: %v", err)
	}
	if err := s.Summaries.SaveSummary(domain.ChapterSummary{
		Chapter:    2,
		Summary:    "The two investigate the old pier, clues point to the abandoned warehouse.",
		Characters: []string{"Lin Lan", "Zhou Ce", "Uncle Shen"},
		KeyEvents:  []string{"Old pier conflict", "Warehouse clues appear"},
	}); err != nil {
		t.Fatalf("SaveSummary 2: %v", err)
	}
	if err := s.World.SaveForeshadowLedger([]domain.ForeshadowEntry{
		{ID: "tape", Description: "Audiotape left by the missing person", PlantedAt: 2, Status: "planted"},
	}); err != nil {
		t.Fatalf("SaveForeshadowLedger: %v", err)
	}
	if err := s.World.SaveTimeline([]domain.TimelineEvent{
		{Chapter: 2, Time: "Night", Event: "Old pier clash", Characters: []string{"Lin Lan", "Zhou Ce"}},
	}); err != nil {
		t.Fatalf("SaveTimeline: %v", err)
	}
	if err := s.World.SaveStyleRules(domain.WritingStyleRules{
		Prose:  []string{"Sentences tend to be short, keeping tension"},
		Taboos: []string{"Avoid explaining the mystery directly"},
	}); err != nil {
		t.Fatalf("SaveStyleRules: %v", err)
	}
	if err := s.World.SaveReview(domain.ReviewEntry{
		Chapter: 2,
		Scope:   "chapter",
		Verdict: "polish",
		Summary: "Chapter 2 ending setup is a bit rushed, need to add a beat of tension before the warehouse.",
		Issues: []domain.ConsistencyIssue{
			{
				Type:        "pacing",
				Severity:    "warning",
				Description: "Warehouse clues appear too fast, suspense building is insufficient.",
				Suggestion:  "Add a description of hesitation and environmental oppression before entering the warehouse.",
			},
		},
		ContractMisses: []string{"End hook is not strong enough"},
	}); err != nil {
		t.Fatalf("Save chapter review: %v", err)
	}
	if err := s.World.SaveReview(domain.ReviewEntry{
		Chapter: 2,
		Scope:   "global",
		Verdict: "polish",
		Summary: "Chapter 2 ending pacing is a bit fast, warehouse clues need another beat of building tension.",
	}); err != nil {
		t.Fatalf("SaveReview: %v", err)
	}
	return s
}

func toolGroup(id, name, result string) []agentcore.AgentMessage {
	return []agentcore.AgentMessage{
		agentcore.Message{
			Role:    agentcore.RoleAssistant,
			Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{ID: id, Name: name, Args: []byte(`{}`)})},
		},
		agentcore.ToolResultMsg(id, []byte(strconv.Quote(result)), false),
	}
}

// Writer 一次运行只有一条任务消息，其余全是工具组：压缩必须落在工具循环内部，
// 摘要来自 store 事实，任务文本跟着摘要走，再次压缩也不能丢。
func TestStoreSummaryCompactCompactsToolLoop(t *testing.T) {
	const task = "返工第 3 章：结尾要承接第二章的仓库线索\n\n补充：结尾留一个钩子"
	strategy := NewStoreSummaryCompact(StoreSummaryCompactConfig{
		Store:              seededWriterStore(t),
		KeepRecentTokens:   600,
		SummaryTokenBudget: 2000,
	})
	msgs := []agentcore.AgentMessage{agentcore.UserMsg(task)}
	for i := 1; i <= 6; i++ {
		msgs = append(msgs, toolGroup("t"+strconv.Itoa(i), "read_chapter", strings.Repeat("a", 1600))...)
	}

	compact := func(in []agentcore.AgentMessage) []agentcore.AgentMessage {
		out, result, err := strategy.Apply(context.Background(), in, in, corecontext.Budget{
			Tokens: corecontext.EstimateTotal(in), Window: 4000, Threshold: 1000,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !result.Applied || result.Info == nil || result.Info.CompactedCount <= 0 {
			t.Fatalf("store summary must apply inside a tool loop, got %+v", result)
		}
		summary, ok := out[0].(corecontext.ContextSummary)
		if !ok || !strings.Contains(summary.Summary, taskHeading+task+"\n\n## ") {
			t.Fatalf("summary must carry the task section verbatim, got %T", out[0])
		}
		for _, want := range []string{"Recent Chapter Summaries", "Current Chapter Plan", "Active Foreshadowing", "Pending Reviews", "warehouse clues need another beat of building tension"} {
			if !strings.Contains(summary.Summary, want) {
				t.Fatalf("expected %q in store checkpoint", want)
			}
		}
		if next, ok := out[1].(agentcore.Message); !ok || !next.HasToolCalls() {
			t.Fatalf("kept suffix must start with a tool call, got %T", out[1])
		}
		return out
	}

	first := compact(msgs)
	if len(first) >= len(msgs) {
		t.Fatalf("expected compaction, %d -> %d", len(msgs), len(first))
	}
	compact(append(first, toolGroup("t7", "read_chapter", strings.Repeat("a", 1600))...))

	// 中间经过 FullSummary：LLM 摘要按 WriterSummaryPrompt 格式重写，任务仍在固定一节
	llmSummary := corecontext.ContextSummary{Summary: taskHeading + task + "\n\n## Current Progress\n第 3 章进行中"}
	compact(append([]agentcore.AgentMessage{llmSummary}, msgs[1:]...))
}
