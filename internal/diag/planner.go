package diag

import "fmt"

// PlanActions generates executable actions based on high-confidence Findings.
// Only Findings with Confidence == ConfHigh and AutoLevel == AutoSafe will produce Actions.
func PlanActions(findings []Finding) []Action {
	seen := make(map[string]struct{})

	var actions []Action
	for _, f := range findings {
		if f.Confidence != ConfHigh || f.AutoLevel != AutoSafe {
			continue
		}
		if _, ok := seen[f.Rule]; ok {
			continue
		}
		seen[f.Rule] = struct{}{}

		actions = append(actions, planRule(f)...)
	}
	return actions
}

func planRule(f Finding) []Action {
	key := findingFingerprint(f)

	switch f.Rule {
	case "PhaseFlowMismatch":
		return []Action{
			{SourceRule: f.Rule, Kind: ActionEmitNotice, Severity: f.Severity, Summary: f.Title, Message: f.Title, Fingerprint: key},
			{SourceRule: f.Rule, Kind: ActionEnqueueFollowUp, Severity: f.Severity, Summary: "Fix State Machine Anomaly", Message: "State machine anomaly: " + f.Evidence + ". Please check and correct the progress phase/flow status before continuing.", Fingerprint: key},
		}
	case "OutlineExhausted":
		return []Action{
			{SourceRule: f.Rule, Kind: ActionEnqueueFollowUp, Severity: f.Severity, Summary: "Handle Exhausted Outline", Message: "Completed chapters have reached the planned limit. Please invoke the Architect to expand the next arc or append a new volume before writing.", Fingerprint: key},
		}
	case "OrphanedSteer":
		return []Action{
			{SourceRule: f.Rule, Kind: ActionEnqueueFollowUp, Severity: f.Severity, Summary: "Process Unresolved User Steering", Message: "Unresolved user steering instructions found. Please resolve pending steer before continuing the current task.", Fingerprint: key},
		}
	default:
		return nil
	}
}

func findingFingerprint(f Finding) string {
	return fmt.Sprintf("%s|%s|%s|%s", f.Rule, f.Target, f.Title, f.Evidence)
}
