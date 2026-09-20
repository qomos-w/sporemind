package slashcmd

func init() {
	Register(&dreamCommand{})
}

type dreamCommand struct{}

func (c *dreamCommand) Name() string      { return "dream" }
func (c *dreamCommand) ShortHelp() string { return "Trigger memory consolidation (merge and promote memory nodes)" }

func (c *dreamCommand) Execute(_ interface{}, _ AgentState, _ string) Result {
	// Actual dreamer spawn is handled by the agent after dispatch.
	return Result{Continue: false}
}
