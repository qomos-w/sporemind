package slashcmd

func init() {
	Register(&compactCommand{})
}

type compactCommand struct{}

func (c *compactCommand) Name() string      { return "compact" }
func (c *compactCommand) ShortHelp() string { return "Manually compress context to free up context window space" }

func (c *compactCommand) Execute(_ interface{}, state AgentState, _ string) Result {
	if state.RawSession == nil || state.RawSession.MessageCount == 0 {
		return Result{Text: "No messages to compact."}
	}
	// Actual compaction is handled by the agent after dispatch.
	// This command signals intent; the agent intercepts it.
	return Result{Continue: false}
}
