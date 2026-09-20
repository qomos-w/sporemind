package debugcmd

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestAll_ContainsBuiltins verifies every built-in command self-registered and
// All() returns them sorted by name.
func TestAll_ContainsBuiltins(t *testing.T) {
	cmds := All()
	if len(cmds) < 4 {
		t.Fatalf("expected at least 4 built-in commands, got %d", len(cmds))
	}
	byName := make(map[string]Command, len(cmds))
	prev := ""
	for _, c := range cmds {
		if c.Name() == "" || c.ShortHelp() == "" {
			t.Errorf("command has empty Name or ShortHelp: %+v", c)
		}
		if prev != "" && c.Name() <= prev {
			t.Errorf("commands not sorted: %q after %q", c.Name(), prev)
		}
		prev = c.Name()
		byName[c.Name()] = c
	}
	for _, name := range []string{"help", "version", "uptime", "loglevel"} {
		if _, ok := byName[name]; !ok {
			t.Errorf("expected built-in command %q, missing from registry", name)
		}
	}
}

// TestRegisterDuplicatePanics verifies the fail-fast duplicate guard.
func TestRegisterDuplicatePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	// helpCommand is already registered by its init().
	Register(&helpCommand{})
}

// TestDispatchUnknownCommand verifies Dispatch returns ErrUnknownCommand for
// unregistered names.
func TestDispatchUnknownCommand(t *testing.T) {
	_, err := Dispatch(context.Background(), "no_such_command", nil)
	if !errors.Is(err, ErrUnknownCommand) {
		t.Fatalf("expected ErrUnknownCommand, got %v", err)
	}
}

// TestDispatchHelp verifies the help output lists every registered command.
func TestDispatchHelp(t *testing.T) {
	out, err := Dispatch(context.Background(), "help", nil)
	if err != nil {
		t.Fatalf("help: %v", err)
	}
	if !strings.Contains(out, "Available debug commands:") {
		t.Errorf("help output missing header: %q", out)
	}
	for _, c := range All() {
		if !strings.Contains(out, c.Name()) {
			t.Errorf("help output missing command %q: %q", c.Name(), out)
		}
	}
}

// TestDispatchVersion verifies the version command reports the build version.
func TestDispatchVersion(t *testing.T) {
	out, err := Dispatch(context.Background(), "version", nil)
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("version output is empty")
	}
}

// TestDispatchUptime verifies the uptime command reports a duration.
func TestDispatchUptime(t *testing.T) {
	out, err := Dispatch(context.Background(), "uptime", nil)
	if err != nil {
		t.Fatalf("uptime: %v", err)
	}
	if !strings.HasPrefix(out, "up ") {
		t.Errorf("uptime output %q does not start with 'up '", out)
	}
}

// TestDispatchLogLevel verifies the query-only loglevel command: no args
// reports the current level, any args are rejected.
func TestDispatchLogLevel(t *testing.T) {
	out, err := Dispatch(context.Background(), "loglevel", nil)
	if err != nil {
		t.Fatalf("loglevel: %v", err)
	}
	if !strings.HasPrefix(out, "current log level:") {
		t.Errorf("loglevel output %q missing level prefix", out)
	}
	if _, err := Dispatch(context.Background(), "loglevel", []string{"debug"}); err == nil {
		t.Error("expected error when passing an argument to query-only loglevel")
	}
}
