package slashcmd

func init() {
	Register(&clearCommand{})
}

type clearCommand struct{}

func (c *clearCommand) Name() string      { return "clear" }
func (c *clearCommand) ShortHelp() string { return "Clear the agent conversation history" }

func (c *clearCommand) Execute(_ interface{}, _ AgentState, _ string) Result {
	// The actual reset is handled by the agent after Dispatch.
	// Continue=false signals the agent to intercept and clear the session.
	return Result{Continue: false}
}
