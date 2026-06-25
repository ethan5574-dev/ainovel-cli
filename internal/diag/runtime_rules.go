package diag

import (
	"fmt"
	"strings"

	"github.com/voocel/ainovel-cli/internal/store"
)

// Runtime detection thresholds.
const (
	repeatCritical = 8 // Upgraded to critical if near-end repetition reaches this count
	streamIdleWarn = 3 // Threshold for accumulated stream_idle warnings
)

// RuntimeRuleFunc is the uniform signature of runtime diagnostic rules (corresponding to RuleFunc on the creative side).
// Input is the anonymized and aggregated RuntimeCapture, producing report-type Findings—all AutoNone,
// only diagnosing, no Actions produced (observer discipline, see architecture.md §2.3).
type RuntimeRuleFunc func(rc *RuntimeCapture) []Finding

var runtimeRules = []RuntimeRuleFunc{
	repeatedErrors,
	stuckStep,
	streamIdleStorm,
}

// runtimeFindings runs all runtime rules.
func runtimeFindings(rc *RuntimeCapture) []Finding {
	var out []Finding
	for _, rule := range runtimeRules {
		out = append(out, rule(rc)...)
	}
	return out
}

// Diagnose is the complete diagnostic entry for /diag: creative diagnosis + runtime signals + runtime checks,
// returning the merged Report and the original RuntimeCapture (reused for export to avoid duplicate capture).
// Runtime Findings are only merged into Findings for display, without modifying Actions—keeping pure observation.
func Diagnose(s *store.Store) (Report, RuntimeCapture) {
	rep := Analyze(s)
	rc := CaptureRuntime(s)
	rep.Findings = append(rep.Findings, runtimeFindings(&rc)...)
	sortFindings(rep.Findings)
	return rep, rc
}

// repeatedErrors only treats "near-end repeated errors / invalid arguments" as Findings.
// Does not touch normal tool repetitions—subagent/novel_context/read_chapter etc. are naturally
// high frequency in long runs, and their accumulated counts are not loop signals; real "repetition without progression" is caught by stuckStep.
func repeatedErrors(rc *RuntimeCapture) []Finding {
	var out []Finding
	for _, r := range rc.Repeats {
		var rule, title, sugg string
		switch {
		case strings.Contains(r.Sig, " · err: "):
			rule = "RepeatedToolError"
			title = "Tool repeatedly reports same error"
			sugg = "Near-end same tool repeatedly returns the same error, mostly due to invalid model parameters or tool contract mismatch; check agentcore tool validation / prompt parameter conventions (see #34)."
		case strings.Contains(r.Sig, "(args invalid)"):
			rule = "ArgsInvalidLoop"
			title = "Parameters repeatedly fail to parse"
			sugg = "Parameters sent by the model fail to parse but retries continue; check if agentcore does loose coercion for this type (see #34)."
		default:
			continue // Normal tool repetitions do not produce Finding
		}
		sev := SevWarning
		if r.Count >= repeatCritical {
			sev = SevCritical
		}
		out = append(out, Finding{
			Rule:       rule,
			Category:   CatFlow,
			Severity:   sev,
			Confidence: ConfHigh,
			AutoLevel:  AutoNone,
			Target:     "runtime.flow",
			Title:      title,
			Evidence:   fmt.Sprintf("`%s` ×%d", r.Sig, r.Count),
			Suggestion: sugg,
		})
	}
	return out
}

// stuckStep detects checkpoint consecutively stuck at the same step.
func stuckStep(rc *RuntimeCapture) []Finding {
	if rc.StuckStep == "" {
		return nil
	}
	sev := SevWarning
	if rc.StuckCount >= repeatCritical {
		sev = SevCritical
	}
	return []Finding{{
		Rule:       "StuckStep",
		Category:   CatFlow,
		Severity:   sev,
		Confidence: ConfHigh,
		AutoLevel:  AutoNone,
		Target:     "runtime.flow",
		Title:      "checkpoint stuck at the same step",
		Evidence:   fmt.Sprintf("consecutively stuck at `%s` ×%d", rc.StuckStep, rc.StuckCount),
		Suggestion: "Same step repeatedly written without progression; combine with repeated signatures above to locate which subagent is stuck.",
	}}
}

// streamIdleStorm detects frequent stream timeouts (stream_idle) (#32).
func streamIdleStorm(rc *RuntimeCapture) []Finding {
	n := rc.LogKinds["stream_idle"]
	if n < streamIdleWarn {
		return nil
	}
	return []Finding{{
		Rule:       "StreamIdleStorm",
		Category:   CatFlow,
		Severity:   SevWarning,
		Confidence: ConfHigh,
		AutoLevel:  AutoNone,
		Target:     "runtime.provider",
		Title:      "Frequent stream timeouts (stream_idle)",
		Evidence:   fmt.Sprintf("stream_idle ×%d", n),
		Suggestion: "Upstream takes too long to emit token and gets killed by watchdog; increase streamIdleTimeout for slow thinking models, or troubleshoot provider connection stability (see #32).",
	}}
}
