package debugcmd

import (
	"context"
	"fmt"
	"time"
)

func init() {
	Register(&uptimeCommand{})
}

type uptimeCommand struct{}

// start is captured once at package init (effectively process start) and is
// never written again — an immutable snapshot, not mutable service state.
var start = time.Now()

func (c *uptimeCommand) Name() string      { return "uptime" }
func (c *uptimeCommand) ShortHelp() string { return "Show process uptime" }

func (c *uptimeCommand) Exec(_ context.Context, _ []string) (string, error) {
	return fmt.Sprintf("up %s", time.Since(start).Round(time.Second)), nil
}
