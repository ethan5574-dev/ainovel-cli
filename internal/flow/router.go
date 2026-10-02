// Package flow 实现垂类路由：Host 根据事实决定下一个调哪个子代理做什么。
//
// 设计原则：
//   - Route 是纯函数：输入 State，输出 *Instruction。无 IO、无 Store 调用，可单测。
//   - State 由 LoadState（非纯）从 Store 构造，一次性把路由需要的事实读齐。
//   - 返回 nil 是合法的：表示当前没有可由确定性事实推出的 Worker 指令；
//     Engine 再按终态、启动补裁或等待用户干预处理。
//
// Router 覆盖的是"查表型"决策（每章下一步、弧末后处理、队列驱动），
// 不覆盖"语义理解型"决策（选规划师、处理用户 Steer、输出总结）。
package flow

import (
	"fmt"
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// plannerForTier 从已落盘的规划级别推导规划师身份:short 归短篇规划师,
// mid/long 归长篇规划师(与启动 Arbiter 的选型口径一致)。
func plannerForTier(tier domain.PlanningTier) string {
	if tier == domain.PlanningTierShort {
		return "architect_short"
	}
	return "architect_long"
}

// Instruction 指示 Engine 下一步直接运行的 Worker 与任务。
type Instruction struct {
	Agent   string // architect_long / architect_short / writer / editor
	Task    string // 给子代理的任务描述
	Reason  string // 路由理由（用于事件、日志与失败裁定）
	Chapter int    // writer 任务涉及的章节号（续写/重写/打磨）；0 表示不涉及（editor/architect 任务）
}

type AggregateKind string

const (
	AggregateArcReview     AggregateKind = "arc_review"
	AggregateArcSummary    AggregateKind = "arc_summary"
	AggregateVolumeSummary AggregateKind = "volume_summary"
	AggregateGlobalReview  AggregateKind = "global_review"
)

type AggregateRefresh struct {
	Kind         AggregateKind
	Volume       int
	Arc          int
	StartChapter int
	EndChapter   int
}

// State 是 Route 的输入：所有事实必须在此显式声明，禁止 Route 内部读 Store。
type State struct {
	Progress *domain.Progress

	// Maximum completed chapter number; zero means writing has not started.
	LastCompleted int

	// 上一章的弧边界信息；IsArcEnd=false 时其他字段无意义。
	// 当 LastCompleted=0 或非 Layered 模式时应为 nil。
	ArcBoundary *storepkg.ArcBoundary

	// 弧末后处理的三个事实：评审 / 弧摘要 / 卷摘要是否已完成。
	HasArcReview     bool
	HasArcSummary    bool
	HasVolumeSummary bool

	// 基础设定缺项（规划阶段的补齐信号）。
	FoundationMissing []string

	// 已落盘的规划级别（save_foundation 落 scale 时写入 RunMeta）。
	// 空 = 首次规划尚未产出任何设定，规划师身份不可判定。
	PlanningTier domain.PlanningTier

	// 非分层书：最近完成章是否已有 scope=global 的全局审阅
	//（仅在 ShouldReview 触发点有意义；分层书恒 false）。
	HasGlobalReview bool

	// External revision impacts that Architect must handle before continuation. Ordinary Writer feedback waits for
	// the next natural structural operation rather than causing an extra planner dispatch per chapter.
	ImmediateFeedbackCount int

	// Earliest arc/volume artifact requiring Editor regeneration after an external revision.
	AggregateRefresh *AggregateRefresh
}

// Route 根据事实返回下一步确定性指令；返回 nil 由 Engine 按调用上下文处理。
//
// 决策优先级（互斥，自上而下匹配第一个）：
//  1. Phase=Complete        → nil（Host 确定性输出总结）
//  2. 规划期设定缺项且规划师可判定 → 同一规划师补齐；否则 nil（Engine 启动补裁）
//  3. PendingRewrites 非空  → writer 按队列重写/打磨
//  4. Flow=Reviewing        → nil（dormant：当前无写入者，评审期 Flow 实为 writing）
//  5. Flow=Steering         → nil（用户干预处理中）
//  6. External revision invalidates aggregates -> editor rebuilds
//  7. External revision affects future plans -> architect handles it
//  8. Layered arc end -> review, summaries, expansion, or next volume
//  9. Flat global review due -> editor(global review)
//
// 10. Flat outline exhausted -> architect decides completion or extension
// 11. Otherwise -> writer(next_chapter)
func Route(s State) *Instruction {
	p := s.Progress
	if p == nil {
		return nil
	}

	// 1. 终态：Host 根据 store 事实生成确定性总结
	if p.Phase == domain.PhaseComplete {
		return nil
	}

	// 2. 规划期补齐：查表型决策——缺什么在 store，规划师身份从已落盘的 scale 推导
	//    （short → architect_short，其余 → architect_long）。tier 为空说明首次规划
	//    尚未落盘任何设定（选型是语义判断），由 Engine 的 planStartFallback 补裁。
	if p.Phase != domain.PhaseWriting {
		if len(s.FoundationMissing) > 0 && s.PlanningTier != "" {
			task := fmt.Sprintf("Fill missing foundation and book metadata: %s; use save_book for book metadata and save_foundation for other foundation artifacts", strings.Join(s.FoundationMissing, "、"))
			if len(s.FoundationMissing) == 1 && s.FoundationMissing[0] == "foundation_audit" {
				task = "Foundation artifacts are complete: call novel_context again to read all saved artifacts and foundation_status.fingerprint, check cross-file semantic consistency, then call audit_foundation; fix issues and repeat the audit first if necessary"
			}
			return &Instruction{
				Agent:  plannerForTier(s.PlanningTier),
				Task:   task,
				Reason: "Foundation artifacts are missing; dispatch the same planner to fill them",
			}
		}
		return nil
	}

	// 3. 重写/打磨队列优先（事实已在工具层落盘，Router 只照单派发）
	if len(p.PendingRewrites) > 0 {
		ch := p.PendingRewrites[0]
		verb := "Rewrite"
		if p.Flow == domain.FlowPolishing {
			verb = "Polish"
		}
		return &Instruction{
			Agent:   "writer",
			Task:    fmt.Sprintf("%s chapter %d", verb, ch),
			Reason:  fmt.Sprintf("PendingRewrites has %d chapters remaining", len(p.PendingRewrites)),
			Chapter: ch,
		}
	}

	// 4. 审阅中 → 交回 LLM。当前为 dormant 分支：save_review 只把 Flow 置为
	//    writing/rewriting/polishing，无任何生产路径置 reviewing（评审期 Flow 实为 writing，
	//    "评审先于续写"由 agentcore steering 优先级保证，不靠此分支）。保留以与 Steering
	//    对称，并在未来 editor 评审期显式置 reviewing 时使路由让位于 LLM。
	if p.Flow == domain.FlowReviewing {
		return nil
	}

	// 5. 用户干预处理中：Arbiter 正在裁定，Engine 不抢占
	if p.Flow == domain.FlowSteering {
		return nil
	}
	if refresh := s.AggregateRefresh; refresh != nil {
		switch refresh.Kind {
		case AggregateArcReview:
			return &Instruction{
				Agent: "editor",
				Task: fmt.Sprintf(
					"Review volume %d arc %d (chapters %d-%d): call novel_context(chapter=%d), then save_review with scope=arc and chapter=%d",
					refresh.Volume, refresh.Arc, refresh.StartChapter, refresh.EndChapter, refresh.EndChapter, refresh.EndChapter,
				),
				Reason: "Arc review missing",
			}
		case AggregateArcSummary:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("Generate a summary, character snapshot, and writing rules for volume %d arc %d (save_arc_summary)", refresh.Volume, refresh.Arc),
				Reason: "Arc summary missing",
			}
		case AggregateVolumeSummary:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("Generate a summary for volume %d (save_volume_summary)", refresh.Volume),
				Reason: "Volume summary missing",
			}
		case AggregateGlobalReview:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("Review the first %d chapters: call novel_context(chapter=%d), then save_review with scope=global and chapter=%d", refresh.EndChapter, refresh.EndChapter, refresh.EndChapter),
				Reason: "Global review missing",
			}
		}
	}

	if s.ImmediateFeedbackCount > 0 {
		return &Instruction{
			Agent:  plannerForTier(s.PlanningTier),
			Task:   "Handle only external-revision writer_feedback in novel_context: compare established events with future plans, call revise_outline or the appropriate structural tool when changes are needed, or resolve_outline_feedback when no changes are needed; do not handle foundation_status or other planning, and end with one sentence after saving",
			Reason: fmt.Sprintf("%d external-revision impacts have not been propagated to future plans", s.ImmediateFeedbackCount),
		}
	}

	// 8. End-of-arc processing in layered mode.
	if p.Layered && s.ArcBoundary != nil && s.ArcBoundary.IsArcEnd {
		b := s.ArcBoundary
		switch {
		case !s.HasArcReview:
			return &Instruction{
				Agent: "editor",
				Task: fmt.Sprintf(
					"Perform an arc review for volume %d arc %d (chapters %d-%d): call novel_context(chapter=%d), then save_review with scope=arc and chapter=%d; issues[].chapters must stay within this range",
					b.Volume, b.Arc, b.StartChapter, b.EndChapter, b.EndChapter, b.EndChapter,
				),
				Reason: "End-of-arc review incomplete",
			}
		case !s.HasArcSummary:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("Generate a summary, character snapshot, and writing rules for volume %d arc %d (save_arc_summary)", b.Volume, b.Arc),
				Reason: "Arc summary incomplete",
			}
		case b.IsVolumeEnd && !s.HasVolumeSummary:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("Generate a summary for volume %d (save_volume_summary)", b.Volume),
				Reason: "Volume summary incomplete",
			}
		case b.NeedsExpansion && b.NextArc > 0:
			return &Instruction{
				Agent:  "architect_long",
				Task:   fmt.Sprintf("Expand volume %d arc %d (expand_next_arc)", b.NextVolume, b.NextArc),
				Reason: "Next arc skeleton needs expansion",
			}
		case b.NeedsNewVolume:
			return &Instruction{
				Agent:  "architect_long",
				Task:   "Create the next volume: evaluate the completion checklist, then call save_foundation. If the story continues, use type=append_volume; if it is nearing its end, use type=append_volume with \"final\": true at the volume JSON top level (finale volume: resolve remaining threads throughout it, with automatic completion afterward); if all completion conditions are already met, use type=complete_book. All three choices require a reason explaining the decision",
				Reason: "End of volume: decide whether to append another volume, append a finale volume, or complete the book",
			}
		}
	}

	// 11. 非分层全局审阅：每 ReviewInterval 章一次(事实:该章的 global review 未落盘)。
	//     原为 commit_chapter 返回值里的 review_required 信号,现按事实推导——
	//     返回值只是事实的镜像,Route 从 store 直接看同一事实。
	if !p.Layered && s.LastCompleted > 0 {
		if due, reason := domain.ShouldReview(len(p.CompletedChapters)); due && !s.HasGlobalReview {
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("Review the first %d chapters globally (save_review scope=global, chapter=%d)", s.LastCompleted, s.LastCompleted),
				Reason: reason,
			}
		}
	}

	// 12. 非分层大纲耗尽时不能继续派发越界章节。让 Architect 基于当前故事事实
	// 决定完结，或用 revise_outline 从 next 章续接计划。
	next := p.NextChapter()
	if next <= 0 {
		return nil
	}
	if !p.Layered && p.TotalChapters > 0 && next > p.TotalChapters {
		return &Instruction{
			Agent: plannerForTier(s.PlanningTier),
			Task: fmt.Sprintf(
				"The flat outline is exhausted (%d chapters completed out of %d): if the story has concluded, call save_foundation(type=complete_book); otherwise use revise_outline to extend the plan from chapter %d",
				len(p.CompletedChapters), p.TotalChapters, next,
			),
			Reason: "Flat outline exhausted: decide whether to complete or extend the story",
		}
	}

	// 13. 正常续写
	return &Instruction{
		Agent:   "writer",
		Task:    fmt.Sprintf("Write chapter %d", next),
		Reason:  "Write the next chapter",
		Chapter: next,
	}
}
