package slashcmd

import (
	"strings"
)

// ParsedCommand is the result of parsing a slash-prefixed user message.
type ParsedCommand struct {
	Name string // e.g. "help", "compact", "clear"
	Args string // everything after the command name (trimmed)
	Raw  string // the original text
}

// Result is what a command returns. The agent inspects this to decide
// whether to continue to the normal turn flow.
type Result struct {
	// Text is the human-readable response. If non-empty and Continue is false,
	// the agent records it as a system message without spawning a turn.
	Text string

	// Continue indicates whether the normal chat.submit flow should proceed
	// after the command executes. Most commands set this to false.
	Continue bool

	// Error is set when the command failed. If non-nil, the agent returns
	// this error directly to the caller.
	Error error

	// NotFound is true when the name was parsed as a slash command but no
	// matching command was registered. Callers can use this to implement
	// fallback behavior (e.g. mounting a skill by name).
	NotFound bool
}

// AgentState is the read-only snapshot of agent state passed to commands.
// The agent fills this at the call site — slashcmd never imports pkg/actor/agent.
type AgentState struct {
	Session    *SessionSnapshot
	RawSession *RawSessionSnapshot
	Model      string
}

// SessionSnapshot carries the subset of domain.Session that commands need.
type SessionSnapshot struct {
	Turns      []TurnSnapshot
	ActiveHead int32
}

// TurnSnapshot is a read-only view of a single turn for command consumption.
type TurnSnapshot struct {
	ID        string
	Role      string
	UserInput string
	State     string
}

// RawSessionSnapshot carries the subset of domain.RawSession that commands need.
type RawSessionSnapshot struct {
	MessageCount    int
	EstimatedTokens int32
	SummarySegments int
}

// Command is the interface each slash command implements.
type Command interface {
	Name() string
	ShortHelp() string
	Execute(ctx interface{}, state AgentState, args string) Result
}

// Parse checks whether text starts with '/'. If so, it splits into a ParsedCommand.
// Returns nil if the text is not a slash command (including "//" which is a comment).
func Parse(text string) *ParsedCommand {
	if !strings.HasPrefix(text, "/") {
		return nil
	}
	if strings.HasPrefix(text, "//") {
		return nil
	}
	body := strings.TrimPrefix(text, "/")
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}
	parts := strings.SplitN(body, " ", 2)
	name := parts[0]
	args := ""
	if len(parts) > 1 {
		args = strings.TrimSpace(parts[1])
	}
	return &ParsedCommand{Name: name, Args: args, Raw: text}
}
