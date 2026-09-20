package slashcmd

import (
	"fmt"
	"strings"
)

func init() {
	Register(&helpCommand{})
}

type helpCommand struct{}

func (c *helpCommand) Name() string      { return "help" }
func (c *helpCommand) ShortHelp() string { return "Show available slash commands" }

func (c *helpCommand) Execute(_ interface{}, _ AgentState, _ string) Result {
	var b strings.Builder
	b.WriteString("Available commands:\n\n")
	for _, cmd := range All() {
		fmt.Fprintf(&b, "  /%-12s %s\n", cmd.Name(), cmd.ShortHelp())
	}
	return Result{Text: b.String()}
}
