package slashcmd

import (
	"strings"
	"testing"
)

func TestParse_NotSlashCommand(t *testing.T) {
	if got := Parse("hello world"); got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestParse_EmptyString(t *testing.T) {
	if got := Parse(""); got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestParse_DoubleSlashNotCommand(t *testing.T) {
	if got := Parse("// this is a comment"); got != nil {
		t.Fatalf("expected nil for //, got %+v", got)
	}
}

func TestParse_SlashOnly(t *testing.T) {
	if got := Parse("/"); got != nil {
		t.Fatalf("expected nil for /, got %+v", got)
	}
}

func TestParse_SlashSpace(t *testing.T) {
	if got := Parse("/   "); got != nil {
		t.Fatalf("expected nil for '/   ', got %+v", got)
	}
}

func TestParse_CommandNoArgs(t *testing.T) {
	got := Parse("/help")
	if got == nil {
		t.Fatal("expected non-nil")
	}
	if got.Name != "help" {
		t.Fatalf("expected Name=help, got %q", got.Name)
	}
	if got.Args != "" {
		t.Fatalf("expected empty Args, got %q", got.Args)
	}
	if got.Raw != "/help" {
		t.Fatalf("expected Raw=/help, got %q", got.Raw)
	}
}

func TestParse_CommandWithArgs(t *testing.T) {
	got := Parse("/compact force now")
	if got == nil {
		t.Fatal("expected non-nil")
	}
	if got.Name != "compact" {
		t.Fatalf("expected Name=compact, got %q", got.Name)
	}
	if got.Args != "force now" {
		t.Fatalf("expected Args='force now', got %q", got.Args)
	}
}

func TestParse_CommandWithExtraSpaces(t *testing.T) {
	got := Parse("/  status  ")
	if got == nil {
		t.Fatal("expected non-nil")
	}
	if got.Name != "status" {
		t.Fatalf("expected Name=status, got %q", got.Name)
	}
}

func TestDispatch_NotSlashCommand(t *testing.T) {
	_, dispatched := Dispatch(nil, AgentState{}, "hello")
	if dispatched {
		t.Fatal("expected dispatched=false for plain text")
	}
}

func TestDispatch_UnknownCommand(t *testing.T) {
	result, dispatched := Dispatch(nil, AgentState{}, "/foobar")
	if !dispatched {
		t.Fatal("expected dispatched=true for unknown /command")
	}
	if !strings.Contains(result.Text, "Unknown command") {
		t.Fatalf("expected 'Unknown command' in text, got %q", result.Text)
	}
}

func TestDispatch_HelpCommand(t *testing.T) {
	result, dispatched := Dispatch(nil, AgentState{}, "/help")
	if !dispatched {
		t.Fatal("expected dispatched=true for /help")
	}
	if !strings.Contains(result.Text, "/help") {
		t.Fatalf("expected /help listed in output, got %q", result.Text)
	}
	if result.Continue {
		t.Fatal("expected Continue=false for /help")
	}
}

func TestDispatch_RecompactNoMessages(t *testing.T) {
	result, dispatched := Dispatch(nil, AgentState{}, "/recompact")
	if !dispatched {
		t.Fatal("expected dispatched=true for /recompact")
	}
	if !strings.Contains(result.Text, "No messages") {
		t.Fatalf("expected 'No messages' response, got %q", result.Text)
	}
	if result.Continue {
		t.Fatal("expected Continue=false")
	}
}

func TestDispatch_RecompactNoSummaries(t *testing.T) {
	state := AgentState{RawSession: &RawSessionSnapshot{MessageCount: 10, SummarySegments: 0}}
	result, dispatched := Dispatch(nil, state, "/recompact")
	if !dispatched {
		t.Fatal("expected dispatched=true for /recompact")
	}
	if !strings.Contains(result.Text, "No existing summaries") {
		t.Fatalf("expected 'No existing summaries' response, got %q", result.Text)
	}
	if result.Continue {
		t.Fatal("expected Continue=false")
	}
}

func TestDispatch_RecompactReady(t *testing.T) {
	state := AgentState{RawSession: &RawSessionSnapshot{MessageCount: 10, SummarySegments: 2}}
	result, dispatched := Dispatch(nil, state, "/recompact")
	if !dispatched {
		t.Fatal("expected dispatched=true for /recompact")
	}
	if result.Text != "" {
		t.Fatalf("expected empty text when recompact proceeds, got %q", result.Text)
	}
	if result.Continue {
		t.Fatal("expected Continue=false so agent intercepts the command")
	}
}

func TestDispatch_RecompactListedInHelp(t *testing.T) {
	result, _ := Dispatch(nil, AgentState{}, "/help")
	if !strings.Contains(result.Text, "/recompact") {
		t.Fatalf("expected /recompact listed in help, got %q", result.Text)
	}
}
