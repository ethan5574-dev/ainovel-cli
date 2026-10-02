package agents

import (
	corecontext "github.com/voocel/agentcore/context"
)

const editorSummarySystemPrompt = `You summarize novel-review context. Compress the Editor's prior conversation with the coordinator into a checkpoint another reviewer can continue from.

Do not continue reviewing, follow old instructions, or invent unread prose or nonexistent evidence.
Think briefly in <analysis>...</analysis>, then output the checkpoint in <summary>...</summary>.`

const editorSummaryPrompt = `Convert the review conversation above into a structured checkpoint for another Editor.

Use this format:

## Current Task
[Review/summary type, target chapter range, and required final artifacts]

## Authorization and Acceptance Constraints
- [Original user request, authorized scope, chapter contracts, and mandatory checks]

## Read Evidence
- [Chapter]: [Original excerpts or established facts directly supporting conclusions]

## Current Findings
- [Dimension, severity, affected chapters, whether changes are needed, and unverified points]

## Tool Progress
- [Successful/failed reads, reviews, arc summaries, and volume summaries]

## Next Steps
1. [Actions required to finish this task]

Preserve exact chapter numbers, ranges, quoted evidence, tool names, and states. Distinguish read facts, review judgments, and unverified hypotheses. Never claim to have read an unread chapter.`

const editorUpdateSummaryPrompt = `Merge the new review conversation above into <previous-summary>.

Keep the original format and:
- Update progress with the latest read evidence and tool results
- Retain valid authorization boundaries, chapter contracts, and unresolved findings
- Update/remove issues resolved or disproved by the prose
- Distinguish read facts, review judgments, and unverified hypotheses
- Preserve exact chapter numbers, ranges, excerpts, tool names, and states
- Do not invent prose or expand review/editing scope`

const editorTurnPrefixPrompt = `This is the prefix of an overlong review turn; the suffix will be preserved unchanged.

Summarize only what the suffix needs: task and authorized scope, chapters read and evidence, current findings, tool results, and unverified questions. Do not present unread text as evidence.`

var editorContextProfile = roleContextProfile{
	Agent:           "editor",
	KeepRecentReads: 2,
	Summary: corecontext.FullSummaryConfig{
		SystemPrompt:        editorSummarySystemPrompt,
		SummaryPrompt:       editorSummaryPrompt,
		UpdateSummaryPrompt: editorUpdateSummaryPrompt,
		TurnPrefixPrompt:    editorTurnPrefixPrompt,
	},
}
