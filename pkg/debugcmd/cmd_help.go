package debugcmd

import (
	"context"
	"fmt"
	"strings"
)

func init() {
	Register(&helpCommand{})
}

type helpCommand struct{}

func (c *helpCommand) Name() string      { return "help" }
func (c *helpCommand) ShortHelp() string { return "List all debug commands" }

func (c *helpCommand) Exec(_ context.Context, _ []string) (string, error) {
	var b strings.Builder
	b.WriteString("Available debug commands:\n\n")
	for _, cmd := range All() {
		fmt.Fprintf(&b, "  %-12s %s\n", cmd.Name(), cmd.ShortHelp())
	}
	return b.String(), nil
}
