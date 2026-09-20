package slashcmd

func init() {
	Register(&recompactCommand{})
}

type recompactCommand struct{}

func (c *recompactCommand) Name() string      { return "recompact" }
func (c *recompactCommand) ShortHelp() string { return "Discard previous summaries and re-compact from scratch" }

func (c *recompactCommand) Execute(_ interface{}, state AgentState, _ string) Result {
	if state.RawSession == nil || state.RawSession.MessageCount == 0 {
		return Result{Text: "No messages to recompact."}
	}
	if state.RawSession.SummarySegments == 0 {
		return Result{Text: "No existing summaries to recompact."}
	}
	// Actual reset and re-compaction is handled by the agent after dispatch.
	return Result{Continue: false}
}
