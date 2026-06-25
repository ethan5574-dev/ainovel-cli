package imp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
	"github.com/voocel/ainovel-cli/internal/tools"
)

// Deps bundles pluggable dependencies needed by runner for easy test mock injection.
type Deps struct {
	Store      *store.Store
	CommitTool *tools.CommitChapterTool
	LLM        LLMChat // the same model is fine; both foundation and analyzer are structured reverse-engineering
	Prompts    Prompts
}

// Prompts are the two prompts used in the imp process.
type Prompts struct {
	Foundation string // reverse-engineer foundation
	Analyzer   string // reverse-engineer single chapter
}

// Run executes the complete import flow: split -> foundation -> chapter loop.
// It runs in its own goroutine; the Events channel is closed by this function.
//
// Design trade-offs:
//   - The complete flow is blocking execution (a long CLI task), and callers are responsible for spawning a goroutine to listen to the channel;
//   - Any step failure immediately ends the process and emits a StageError event;
//   - The chapter stage silently skips already completed chapters (commit_chapter's idempotency is a fallback, but skipping LLM saves tokens).
func Run(ctx context.Context, deps Deps, opts Options) (<-chan Event, error) {
	if deps.Store == nil || deps.CommitTool == nil || deps.LLM == nil {
		return nil, fmt.Errorf("deps incomplete")
	}
	if strings.TrimSpace(opts.SourcePath) == "" {
		return nil, fmt.Errorf("source path is required")
	}

	events := make(chan Event, 32)

	go func() {
		defer close(events)
		emit := func(stage Stage, current, total int, msg string, err error) {
			ev := Event{Time: time.Now(), Stage: stage, Current: current, Total: total, Message: msg, Err: err}
			select {
			case events <- ev:
			case <-ctx.Done():
			}
		}

		// ── 1. Split ──
		emit(StageSplitting, 0, 0, "Splitting chapters...", nil)
		chapters, err := SplitFile(opts.SourcePath)
		if err != nil {
			emit(StageError, 0, 0, "Failed to split chapters", err)
			return
		}
		total := len(chapters)
		if total == 0 {
			emit(StageError, 0, 0,
				"No chapters identified: supports headings like 'Chapter/Episode N', 'Volume N', 'Prologue/Epilogue', "+
					"compatible with Markdown #, full-width spaces, 【】 brackets, and GBK encoding. "+
					"Please confirm the file is indeed split-chapter novel text.",
				fmt.Errorf("no chapters matched"))
			return
		}
		emit(StageSplitting, 0, total, fmt.Sprintf("Splitting complete: %d chapters", total), nil)

		// ── 2. Foundation Reverse-engineering (skipped if already complete) ──
		if needsFoundation(deps.Store, opts) {
			emit(StageFoundation, 0, total, "Reverse-engineering Foundation (one LLM call)...", nil)
			fr, err := ReverseFoundation(ctx, deps.LLM, deps.Prompts.Foundation, chapters)
			if err != nil {
				emit(StageError, 0, total, "Failed to reverse-engineer Foundation", err)
				return
			}
			scale := pickScale(total)
			if err := PersistFoundation(ctx, deps.Store, scale, fr); err != nil {
				emit(StageError, 0, total, "Failed to persist Foundation", err)
				return
			}
			emit(StageFoundation, 0, total,
				fmt.Sprintf("Foundation ready: %d characters / %d rules / %d outline chapters (Volume 1)",
					len(fr.Characters), len(fr.WorldRules), len(domain.FlattenOutline(fr.Volumes))),
				nil)
		} else {
			emit(StageFoundation, 0, total, "Foundation already exists, skipping reverse-engineering", nil)
		}

		// ── 3. Chapter Loop ──
		premise, _ := deps.Store.Outline.LoadPremise()
		charactersBlock := loadCharactersBlock(deps.Store)

		startIdx := 0
		if opts.ResumeFrom > 1 {
			startIdx = opts.ResumeFrom - 1
		}
		for i := startIdx; i < total; i++ {
			if err := ctx.Err(); err != nil {
				emit(StageError, i+1, total, "User cancelled", err)
				return
			}
			chNum := i + 1
			ch := chapters[i]

			// Already completed -> skip LLM
			if deps.Store.Progress.IsChapterCompleted(chNum) {
				emit(StageChapter, chNum, total, fmt.Sprintf("Chapter %d completed, skipping", chNum), nil)
				continue
			}

			emit(StageChapter, chNum, total, fmt.Sprintf("Analyzing chapter %d/%d: %s", chNum, total, ch.Title), nil)

			activeHooks, _ := deps.Store.World.LoadActiveForeshadow()
			analysis, err := AnalyzeChapter(ctx, deps.LLM, deps.Prompts.Analyzer,
				chNum, ch.Title, ch.Content, premise, charactersBlock, activeHooks)
			if err != nil {
				emit(StageError, chNum, total, fmt.Sprintf("Failed to analyze chapter %d", chNum), err)
				return
			}

			if err := PersistChapter(ctx, deps.Store, deps.CommitTool, chNum, ch.Title, ch.Content, analysis); err != nil {
				emit(StageError, chNum, total, fmt.Sprintf("Failed to persist chapter %d", chNum), err)
				return
			}
			emit(StageChapter, chNum, total, fmt.Sprintf("Chapter %d import complete", chNum), nil)
		}

		emit(StageDone, total, total, fmt.Sprintf("Import complete: %d chapters", total), nil)
	}()

	return events, nil
}

// needsFoundation determines if foundation needs to be reverse-engineered again.
// Explicit ResumeFrom > 1 is treated as "resuming import", skipping reverse-engineering; otherwise determined by Store status.
func needsFoundation(st *store.Store, opts Options) bool {
	if opts.ResumeFrom > 1 {
		return false
	}
	return len(st.FoundationMissing()) > 0
}

// pickScale assigns a reasonable initial planning tier based on chapter count: short <= 25, mid <= 80, else long.
// Does not affect import itself, only affects Coordinator's choice of architect prompt during subsequent continuation.
func pickScale(total int) domain.PlanningTier {
	switch {
	case total <= 25:
		return domain.PlanningTierShort
	case total <= 80:
		return domain.PlanningTierMid
	default:
		return domain.PlanningTierLong
	}
}

// loadCharactersBlock renders the character profiles into a short text block (name/role + one-line description)
// only for LLM context reference; does not require strict structure.
func loadCharactersBlock(st *store.Store) string {
	chars, err := st.Characters.Load()
	if err != nil || len(chars) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, c := range chars {
		fmt.Fprintf(&sb, "- **%s**（%s）：%s\n", c.Name, c.Role, oneLine(c.Description))
	}
	return sb.String()
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
