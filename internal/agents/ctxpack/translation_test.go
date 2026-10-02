package ctxpack

import (
	"testing"

	"github.com/voocel/agentcore"
	corecontext "github.com/voocel/agentcore/context"
)

func TestLeadingTaskAcceptsEnglishAndLegacyChineseCheckpoints(t *testing.T) {
	for _, heading := range []string{taskHeading, "## 当前任务\n"} {
		messages := []agentcore.AgentMessage{
			corecontext.ContextSummary{Summary: "Checkpoint\n\n" + heading + "Revise chapter 3\n\n## Progress\nDone"},
		}
		if got := leadingTask(messages); got != "Revise chapter 3" {
			t.Errorf("heading %q: task = %q", heading, got)
		}
	}
}
