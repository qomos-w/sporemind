package slashcmd

import (
	"fmt"
	"sort"
)

// Register adds a command to the global registry. Called from per-command
// file init() functions. Panics on duplicate name (fail-fast at startup).
func Register(cmd Command) {
	if _, exists := commands[cmd.Name()]; exists {
		panic(fmt.Sprintf("slashcmd: duplicate command %q", cmd.Name()))
	}
	commands[cmd.Name()] = cmd
}

// Lookup returns the command for the given name, or nil.
func Lookup(name string) Command {
	return commands[name]
}

// All returns all registered commands sorted by name (for /help).
func All() []Command {
	out := make([]Command, 0, len(commands))
	for _, cmd := range commands {
		out = append(out, cmd)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Dispatch parses the text and, if it is a slash command, looks it up and
// executes it. Returns (Result, true) if dispatched, or (Result{}, false)
// if the text is not a slash command.
func Dispatch(ctx interface{}, state AgentState, text string) (Result, bool) {
	parsed := Parse(text)
	if parsed == nil {
		return Result{}, false
	}
	cmd := Lookup(parsed.Name)
	if cmd == nil {
		return Result{
			Text:     fmt.Sprintf("Unknown command: /%s. Type /help for available commands.", parsed.Name),
			NotFound: true,
		}, true
	}
	return cmd.Execute(ctx, state, parsed.Args), true
}
