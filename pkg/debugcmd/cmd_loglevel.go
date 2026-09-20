package debugcmd

import (
	"context"
	"fmt"
	"log/slog"
)

func init() {
	Register(&loglevelCommand{})
}

type loglevelCommand struct{}

func (c *loglevelCommand) Name() string { return "loglevel" }
func (c *loglevelCommand) ShortHelp() string {
	return "Show current log level (query-only; runtime level is not adjustable)"
}

// Exec is query-only: the runtime log level is fixed at startup (no global
// LevelVar is installed anywhere in the codebase), so there is nothing safe
// to mutate. Passing an argument is rejected; help text documents this.
func (c *loglevelCommand) Exec(ctx context.Context, args []string) (string, error) {
	if len(args) > 0 {
		return "", fmt.Errorf("loglevel is query-only: the runtime log level cannot be changed at runtime; run 'loglevel' with no arguments to query the current level")
	}
	return fmt.Sprintf("current log level: %s", currentLevel(ctx)), nil
}

// currentLevel probes the default slog handler to determine the coarsest
// enabled level.
func currentLevel(ctx context.Context) string {
	h := slog.Default().Handler()
	switch {
	case h.Enabled(ctx, slog.LevelDebug):
		return "debug"
	case h.Enabled(ctx, slog.LevelInfo):
		return "info"
	case h.Enabled(ctx, slog.LevelWarn):
		return "warn"
	default:
		return "error"
	}
}
