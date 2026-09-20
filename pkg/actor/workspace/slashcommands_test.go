package workspace

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func TestHandleListSlashCommands(t *testing.T) {
	a, ctx := freshActor(t)
	resp, err := a.handleListSlashCommands(ctx)
	if err != nil {
		t.Fatalf("handleListSlashCommands: %v", err)
	}
	byName := make(map[string]domain.SlashCommand, len(resp.Commands))
	for _, c := range resp.Commands {
		byName[c.Name] = c
	}
	// Every built-in command must be discoverable with a non-empty ShortHelp.
	for _, name := range []string{"clear", "compact", "help", "recompact"} {
		cmd, ok := byName[name]
		if !ok {
			t.Errorf("expected slash command %q, missing from response", name)
			continue
		}
		if cmd.ShortHelp == "" {
			t.Errorf("slash command %q has empty ShortHelp", name)
		}
	}
	if len(resp.Commands) == 0 {
		t.Error("expected at least one slash command, got empty list")
	}
}

func TestHandleListBuiltinModes(t *testing.T) {
	a, ctx := freshActor(t)
	resp, err := a.handleListBuiltinModes(ctx)
	if err != nil {
		t.Fatalf("handleListBuiltinModes: %v", err)
	}
	byName := make(map[string]domain.BuiltinMode, len(resp.Modes))
	for _, m := range resp.Modes {
		byName[m.Name] = m
	}
	// Every builtin:mode:* card must be discoverable with a card id, title and icon.
	for _, name := range []string{"goal", "memory", "worktree"} {
		mode, ok := byName[name]
		if !ok {
			t.Errorf("expected builtin mode %q, missing from response", name)
			continue
		}
		if mode.CardID != "builtin:mode:"+name {
			t.Errorf("mode %q has unexpected CardID %q", name, mode.CardID)
		}
		if mode.Title == "" || mode.Icon == "" {
			t.Errorf("mode %q missing Title/Icon: %+v", name, mode)
		}
	}
}
