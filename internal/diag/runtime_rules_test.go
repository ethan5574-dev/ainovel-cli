package diag

import "testing"

// TestRuntimeFindings_Classify verifies that repeated signatures are classified correctly,
// threshold upgrades/downgrades work, and all runtime Findings are AutoNone (observer rule: diagnose only, no action).
func TestRuntimeFindings_Classify(t *testing.T) {
	rc := RuntimeCapture{
		Repeats: []RepeatStat{
			{Sig: "coordinator · err: InputValidationError", Count: 14}, // Error loop critical
			{Sig: "coordinator · subagent", Count: 45},                  // Normal high-frequency tool -> no Finding produced
			{Sig: "writer · save_plan (args invalid)", Count: 4},        // Invalid arguments warning
		},
		StuckStep:  "writing.commit_ch07",
		StuckCount: 9, // Stuck critical
		LogKinds:   map[string]int{"stream_idle": 4},
		LogErrors:  270, // Long run accumulation, should not produce Finding alone
	}

	fs := runtimeFindings(&rc)
	sev := map[string]Severity{}
	for _, f := range fs {
		sev[f.Rule] = f.Severity
		if f.AutoLevel != AutoNone {
			t.Errorf("%s should be AutoNone (observer rule), got %s", f.Rule, f.AutoLevel)
		}
	}

	want := map[string]Severity{
		"RepeatedToolError": SevCritical,
		"ArgsInvalidLoop":   SevWarning,
		"StuckStep":         SevCritical,
		"StreamIdleStorm":   SevWarning,
	}
	for rule, w := range want {
		if sev[rule] != w {
			t.Errorf("%s: got %q want %q", rule, sev[rule], w)
		}
	}
	// Normal high-frequency tool / cumulative log errors should not produce Finding (to avoid false positives in long runs).
	if _, ok := sev["RepeatedToolCall"]; ok {
		t.Error("Normal tool repetition should not produce Finding")
	}
	if _, ok := sev["LogErrorBurst"]; ok {
		t.Error("Cumulative log errors should not produce Finding alone")
	}
}

// TestRuntimeFindings_Quiet verifies that no runtime Finding is produced when there are no anomaly signals (zero false positives).
func TestRuntimeFindings_Quiet(t *testing.T) {
	rc := RuntimeCapture{
		LogKinds:  map[string]int{"stream_idle": 1}, // Below threshold
		LogErrors: 2,
	}
	if fs := runtimeFindings(&rc); len(fs) != 0 {
		t.Errorf("Quiet state should not produce Finding, got %d: %+v", len(fs), fs)
	}
}
