package workspace

import (
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func TestHandleListDebugCommands(t *testing.T) {
	a, ctx := freshActor(t)
	resp, err := a.handleListDebugCommands(ctx)
	if err != nil {
		t.Fatalf("handleListDebugCommands: %v", err)
	}
	byName := make(map[string]domain.SlashCommand, len(resp.Commands))
	for _, c := range resp.Commands {
		byName[c.Name] = c
	}
	// Every built-in debug command must be discoverable with non-empty ShortHelp.
	for _, name := range []string{"help", "version", "uptime", "loglevel"} {
		cmd, ok := byName[name]
		if !ok {
			t.Errorf("expected debug command %q, missing from response", name)
			continue
		}
		if cmd.ShortHelp == "" {
			t.Errorf("debug command %q has empty ShortHelp", name)
		}
	}
	if len(resp.Commands) == 0 {
		t.Error("expected at least one debug command, got empty list")
	}
}

func TestHandleExecDebugCommand(t *testing.T) {
	a, ctx := freshActor(t)

	// Unknown command must surface as an error.
	if _, err := a.handleExecDebugCommand(ctx, domain.DebugCommandExecReq{Name: "no_such_command"}); err == nil {
		t.Error("expected error for unknown debug command, got nil")
	}

	// help output lists every registered command.
	resp, err := a.handleExecDebugCommand(ctx, domain.DebugCommandExecReq{Name: "help"})
	if err != nil {
		t.Fatalf("exec help: %v", err)
	}
	if !strings.Contains(resp.Output, "Available debug commands:") {
		t.Errorf("help output missing header: %q", resp.Output)
	}
	for _, name := range []string{"help", "version", "uptime", "loglevel"} {
		if !strings.Contains(resp.Output, name) {
			t.Errorf("help output missing command %q: %q", name, resp.Output)
		}
	}

	// Pre-split args are dispatched as-is.
	resp, err = a.handleExecDebugCommand(ctx, domain.DebugCommandExecReq{Name: "version"})
	if err != nil {
		t.Fatalf("exec version: %v", err)
	}
	if strings.TrimSpace(resp.Output) == "" {
		t.Error("version output is empty")
	}
}
