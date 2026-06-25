package diag

import (
	"fmt"
	"sort"

	"github.com/voocel/ainovel-cli/internal/store"
)

// ── Diagnostic Thresholds ─────────────────────────────────

const (
	ThresholdDimScoreLow      = 70  // ChronicLowDimension: Warn if average dimension score is below this value
	ThresholdContractMissRate = 0.3 // ContractMissPattern: Max rate of missed contracts
	ThresholdRewriteRate      = 0.5 // ExcessiveRewrites: Max rewrite rate
	ThresholdWordShortRatio   = 0.4 // WordCountAnomaly: Word count below this ratio of average is considered anomalous
	ThresholdWordLongRatio    = 2.5 // WordCountAnomaly: Word count above this ratio of average is considered anomalous
	ThresholdHookWeakScore    = 75  // HookWeakChain: Warn if hook score is below this value
	ThresholdHookWeakChain    = 3   // HookWeakChain: Threshold for consecutive weak chapters
	ThresholdPayoffMissRate   = 0.4 // PayoffMissPattern: Max rate of missed payoffs
	ThresholdCompassDrift     = 15  // CompassDrift: Max chapters before compass update is required
	ThresholdTimelineGapRate  = 0.3 // TimelineGaps: Max tolerable rate of missing timeline items
	ThresholdForeshadowMin    = 8   // StaleForeshadow: Min chapters for stale foreshadowing
)

// allRules ordered by flow -> quality -> planning -> context.
var allRules = []RuleFunc{
	// Flow
	InvalidPendingRewrites,
	RewritePendingPressure,
	OrphanedSteer,
	PhaseFlowMismatch,
	ChapterGaps,
	// Quality
	ChronicLowDimension,
	ContractMissPattern,
	HookWeakChain,
	PayoffMissPattern,
	ExcessiveRewrites,
	WordCountAnomaly,
	// Planning
	StaleForeshadow,
	CompassDrift,
	OutlineExhausted,
	MissingSummaries,
	// Context
	GhostCharacter,
	TimelineGaps,
	RelationshipStagnation,
}

// Analyze is the single entry point for the diagnostic system.
func Analyze(s *store.Store) Report {
	snap := Load(s)

	var findings []Finding
	for _, e := range snap.LoadErrors {
		findings = append(findings, Finding{
			Rule:       "LoadError",
			Category:   CatFlow,
			Severity:   SevWarning,
			Confidence: ConfHigh,
			AutoLevel:  AutoNone,
			Target:     "runtime.flow",
			Title:      fmt.Sprintf("Artifact load failed: %s", e),
			Suggestion: "The file may be corrupted or permission denied; results of relevant diagnostic rules might be incomplete.",
		})
	}
	for _, rule := range allRules {
		findings = append(findings, rule(&snap)...)
	}
	sortFindings(findings)

	return Report{
		Stats:    buildStats(&snap),
		Findings: findings,
		Actions:  PlanActions(findings),
	}
}

func buildStats(snap *Snapshot) Stats {
	st := Stats{}
	if snap.Progress == nil {
		return st
	}
	p := snap.Progress
	st.CompletedChapters = len(p.CompletedChapters)
	st.TotalChapters = p.TotalChapters
	st.TotalWords = p.TotalWordCount
	st.Phase = string(p.Phase)
	st.Flow = string(p.Flow)

	if st.CompletedChapters > 0 {
		st.AvgWordsPerCh = st.TotalWords / st.CompletedChapters
	}

	if snap.RunMeta != nil {
		st.PlanningTier = string(snap.RunMeta.PlanningTier)
	}

	// Review statistics
	st.ReviewCount = len(snap.Reviews)
	var totalScore float64
	var dimCount int
	for _, r := range snap.Reviews {
		if r.Verdict == "rewrite" {
			st.RewriteCount++
		}
		for _, d := range r.Dimensions {
			totalScore += float64(d.Score)
			dimCount++
		}
	}
	if dimCount > 0 {
		st.AvgReviewScore = totalScore / float64(dimCount)
	}

	// Foreshadow statistics
	latest := snap.LatestCompleted()
	for _, f := range snap.Foreshadow {
		if f.Status == "planted" || f.Status == "advanced" {
			st.ForeshadowOpen++
			if f.Status == "planted" && latest-f.PlantedAt > staleForeshadowThreshold(st.CompletedChapters) {
				st.ForeshadowStale++
			}
		}
	}
	return st
}

// sortFindings sorts findings by severity: critical > warning > info.
func sortFindings(findings []Finding) {
	order := map[Severity]int{SevCritical: 0, SevWarning: 1, SevInfo: 2}
	sort.SliceStable(findings, func(i, j int) bool {
		return order[findings[i].Severity] < order[findings[j].Severity]
	})
}

// staleForeshadowThreshold calculates the foreshadow stagnation threshold based on the total number of chapters.
func staleForeshadowThreshold(completedChapters int) int {
	t := completedChapters / 3
	if t < ThresholdForeshadowMin {
		return ThresholdForeshadowMin
	}
	return t
}
