// Package debugcmd implements a fixed registration point for debug commands
// consumed by the frontend debug console (workspace.debug_commands_list /
// workspace.debug_command_exec).
//
// The registry mirrors the proven pkg/slashcmd pattern: a package-level map,
// per-command files that self-register in init(), duplicate registration
// panics at startup, and All() returning commands sorted by name. The
// go-ebitor gui/core_debug.go RegisterCommand model is the same shape.
//
// Arg splitting contract: the frontend console splits the raw input line into
// args BEFORE the wire, quote-aware (mirroring go-ebitor onSend's regex
// "([^"]*)"|(\S+)); DebugCommandExecReq carries the pre-split args and
// Dispatch never re-parses an input line. This keeps the tokenizer in exactly
// one place (the console producer) and Dispatch purely structural.
package debugcmd

import (
	"context"
	"fmt"
	"sort"
)

// Command is the interface each debug command implements.
type Command interface {
	Name() string
	ShortHelp() string
	Exec(ctx context.Context, args []string) (string, error)
}

// ErrUnknownCommand is returned by Dispatch when no command with the given
// name is registered. Callers can match with errors.Is.
var ErrUnknownCommand = fmt.Errorf("debugcmd: unknown command")

// commands is the fixed registration point. Every debug command registers
// itself here from its own file's init() — that is "registered in one fixed
// place" by construction.
var commands = map[string]Command{}

// Register adds a command to the global registry. Called from per-command
// file init() functions. Panics on duplicate name (fail-fast at startup).
func Register(cmd Command) {
	if _, exists := commands[cmd.Name()]; exists {
		panic(fmt.Sprintf("debugcmd: duplicate command %q", cmd.Name()))
	}
	commands[cmd.Name()] = cmd
}

// Lookup returns the command for the given name, or nil.
func Lookup(name string) Command {
	return commands[name]
}

// All returns all registered commands sorted by name (for help output and
// discovery callables).
func All() []Command {
	out := make([]Command, 0, len(commands))
	for _, cmd := range commands {
		out = append(out, cmd)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Dispatch executes the named command with pre-split args. It returns
// ErrUnknownCommand when the name is not registered.
func Dispatch(ctx context.Context, name string, args []string) (string, error) {
	cmd := Lookup(name)
	if cmd == nil {
		return "", fmt.Errorf("%w: %q", ErrUnknownCommand, name)
	}
	return cmd.Exec(ctx, args)
}
