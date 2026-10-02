package tools

import (
	"fmt"
	"slices"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/store"
)

// ReopenBook returns a completed book to rework, called by Engine at intervention boundaries.
// 完本后 completePhaseGate 硬拦一切 subagent 派发，用户无法返工已写章节。
// This bypasses subagent and is allowed during complete: atomically return phase to writing and enqueue target chapters,
// PendingRewrites、flow=rewriting，随后 Flow Router 照既有返工队列派 writer 逐章重写，
// 队列跑完 commit_chapter 自动重新收尾完结。Gate / Router / edit / commit 重逻辑均无需改动。
func ReopenBook(s *store.Store, chapters []int, reason string) error {
	if len(chapters) == 0 {
		return fmt.Errorf("chapters must not be empty; specify the chapters to rework: %w", errs.ErrToolArgs)
	}

	progress, err := s.Progress.Load()
	if err != nil {
		return fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
	}
	if progress == nil {
		return fmt.Errorf("progress is not initialized: %w", errs.ErrToolPrecondition)
	}
	// 只能返工已写章；不在已完成集合的章号属续写/越界，明确拒绝引导用户走篇幅调整。
	var invalid []int
	for _, ch := range chapters {
		if !slices.Contains(progress.CompletedChapters, ch) {
			invalid = append(invalid, ch)
		}
	}
	if len(invalid) > 0 {
		return fmt.Errorf("chapters %v are unfinished; reopen reworks completed chapters only (use length adjustment for new/expanded plot): %w", invalid, errs.ErrToolPrecondition)
	}

	// phase 前置校验在 store.Reopen 内兜底（仅 complete 可调）。
	if err := s.Progress.Reopen(chapters, reason); err != nil {
		return fmt.Errorf("reopen: %w: %w", errs.ErrStoreWrite, err)
	}

	// checkpoint：与 complete_book 对称（GlobalScope + meta/progress.json）。
	if _, err := s.Checkpoints.AppendArtifact(domain.GlobalScope(), "reopen", "meta/progress.json"); err != nil {
		return fmt.Errorf("checkpoint reopen: %w: %w", errs.ErrStoreWrite, err)
	}
	return nil
}
