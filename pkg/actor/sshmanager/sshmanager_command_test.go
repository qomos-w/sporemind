package sshmanager

import (
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// ---------------------------------------------------------------------------
// Test stub context
// ---------------------------------------------------------------------------

// sshTestCtx implements actor.PureContext for unit tests. Only Identity is
// overridden; the nil-embedded interface satisfies the remaining methods that
// the command/history handlers never call.
type sshTestCtx struct {
	actor.PureContext
	ident id.Identity
}

func (c sshTestCtx) Identity() id.Identity { return c.ident }

func adminCtx() sshTestCtx {
	return sshTestCtx{ident: id.Identity{Role: "admin", Subject: "admin-1"}}
}

func anonCtx() sshTestCtx {
	return sshTestCtx{ident: id.Identity{Role: "anonymous"}}
}

// ---------------------------------------------------------------------------
// Pure-function tests
// ---------------------------------------------------------------------------

func TestPushHistoryCmdDedup(t *testing.T) {
	hist := pushHistoryCmd(nil, "ls -la", 200)
	hist = pushHistoryCmd(hist, "cd /tmp", 200)
	hist = pushHistoryCmd(hist, "ls -la", 200) // duplicate — must move to front

	if len(hist) != 2 {
		t.Fatalf("expected 2 entries after dedup, got %d: %v", len(hist), hist)
	}
	if hist[0] != "ls -la" {
		t.Fatalf("most recent command should be first, got %q", hist[0])
	}
	if hist[1] != "cd /tmp" {
		t.Fatalf("second entry should be cd /tmp, got %q", hist[1])
	}
}

func TestPushHistoryCmdEmptyIgnored(t *testing.T) {
	hist := pushHistoryCmd(nil, "uptime", 200)
	before := len(hist)
	hist = pushHistoryCmd(hist, "   ", 200) // whitespace-only
	if len(hist) != before {
		t.Fatalf("empty command should not be recorded, got %v", hist)
	}
}

func TestPushHistoryCmdMaxCap(t *testing.T) {
	hist := []string{"a", "b", "c"}
	hist = pushHistoryCmd(hist, "d", 3) // exceeds max
	if len(hist) != 3 {
		t.Fatalf("expected cap at 3, got %d: %v", len(hist), hist)
	}
	if hist[0] != "d" {
		t.Fatalf("newest should be first, got %q", hist[0])
	}
	// "c" is the oldest and should have been dropped by the tail truncation.
	for _, h := range hist {
		if h == "c" {
			t.Fatalf("oldest entry should have been dropped: %v", hist)
		}
	}
}

func TestExtractLinesPartial(t *testing.T) {
	lines, rest := extractLines("ls ", "-la\r")
	if len(lines) != 1 || lines[0] != "ls -la" || rest != "" {
		t.Fatalf("unexpected result: lines=%v rest=%q", lines, rest)
	}

	// Partial line carries over as rest.
	lines, rest = extractLines("", "git sta")
	if len(lines) != 0 || rest != "git sta" {
		t.Fatalf("partial should carry over: lines=%v rest=%q", lines, rest)
	}

	// Multiple terminators in one chunk.
	lines, rest = extractLines("", "a\nb\rc\r")
	if len(lines) != 3 || lines[0] != "a" || lines[1] != "b" || lines[2] != "c" || rest != "" {
		t.Fatalf("multi-line parse failed: lines=%v rest=%q", lines, rest)
	}
}

func TestSanitizeHistoryCmd(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"ls -la", "ls -la"},
		{"  uptime  ", "uptime"},
		{"\x1b[32mgreen\x1b[0m ls", "green ls"},         // ANSI color escape
		{"echo\x7fhello", "echohello"},                  // DEL control char stripped
		{"\x1b[Ahistory", "history"},                    // arrow-up escape stripped
		{"", ""},
		{"   ", ""},
	}
	for _, c := range cases {
		got := sanitizeHistoryCmd(c.in)
		if got != c.want {
			t.Errorf("sanitizeHistoryCmd(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Command CRUD handler tests
// ---------------------------------------------------------------------------

func newTestActor(t *testing.T) *Actor {
	t.Helper()
	a := &Actor{
		store:       persist.NewFSPersist(t.TempDir()),
		Credentials: make(map[string]sshCredential),
		Commands:    []domain.SshCommandSnippet{},
		History:     []string{},
	}
	return a
}

func TestCommandCRUD(t *testing.T) {
	a := newTestActor(t)

	// Create
	resp, err := a.handleCommandCreate(adminCtx(), domain.SshCommandCreateReq{
		Name: "Disk usage", Content: "df -h", Category: "Disk",
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if resp.Item.ID == "" || resp.Item.Name != "Disk usage" {
		t.Fatalf("unexpected created item: %+v", resp.Item)
	}
	cmdID := resp.Item.ID

	// List
	listResp, err := a.handleCommandList(adminCtx(), domain.SshCommandListReq{})
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(listResp.Items) != 1 || listResp.Items[0].ID != cmdID {
		t.Fatalf("list mismatch: %+v", listResp.Items)
	}

	// Update
	if _, err := a.handleCommandUpdate(adminCtx(), domain.SshCommandUpdateReq{
		ID: cmdID, Name: "Free space", Content: "df -h --total", Category: "Disk",
	}); err != nil {
		t.Fatalf("update failed: %v", err)
	}
	listResp, _ = a.handleCommandList(adminCtx(), domain.SshCommandListReq{})
	if listResp.Items[0].Name != "Free space" || listResp.Items[0].Content != "df -h --total" {
		t.Fatalf("update not reflected: %+v", listResp.Items[0])
	}

	// Remove
	if _, err := a.handleCommandRemove(adminCtx(), domain.SshCommandRemoveReq{ID: cmdID}); err != nil {
		t.Fatalf("remove failed: %v", err)
	}
	listResp, _ = a.handleCommandList(adminCtx(), domain.SshCommandListReq{})
	if len(listResp.Items) != 0 {
		t.Fatalf("expected empty list after remove, got %+v", listResp.Items)
	}
}

func TestCommandUpdateNotFound(t *testing.T) {
	a := newTestActor(t)
	_, err := a.handleCommandUpdate(adminCtx(), domain.SshCommandUpdateReq{ID: "nope"})
	if err == nil {
		t.Fatal("update of missing command should error")
	}
}

func TestCommandRemoveNotFound(t *testing.T) {
	a := newTestActor(t)
	_, err := a.handleCommandRemove(adminCtx(), domain.SshCommandRemoveReq{ID: "nope"})
	if err == nil {
		t.Fatal("remove of missing command should error")
	}
}

func TestCommandCreateRejectsAnonymous(t *testing.T) {
	a := newTestActor(t)
	_, err := a.handleCommandCreate(anonCtx(), domain.SshCommandCreateReq{Name: "x"})
	if err == nil {
		t.Fatal("anonymous caller must be rejected")
	}
}

func TestCommandCreateRejectsBlankName(t *testing.T) {
	a := newTestActor(t)
	_, err := a.handleCommandCreate(adminCtx(), domain.SshCommandCreateReq{Name: "  "})
	if err == nil {
		t.Fatal("blank command name must be rejected")
	}
}

// ---------------------------------------------------------------------------
// History + persistence tests
// ---------------------------------------------------------------------------

func TestRecordHistoryDedupAndPersist(t *testing.T) {
	a := newTestActor(t)
	sess := &session{id: "s1", owner: "admin-1"}

	// Simulate keystrokes: "ls" then Enter, then "cd /tmp" then Enter.
	a.recordHistory(sess, "ls")
	a.recordHistory(sess, "\r")
	a.recordHistory(sess, "cd /tmp\n")

	resp, err := a.handleHistoryList(adminCtx(), domain.SshHistoryListReq{})
	if err != nil {
		t.Fatalf("history list failed: %v", err)
	}
	if len(resp.Items) != 2 || resp.Items[0] != "cd /tmp" || resp.Items[1] != "ls" {
		t.Fatalf("history order/dedup mismatch: %v", resp.Items)
	}

	// Re-run "ls" — dedup moves it to the front.
	a.recordHistory(sess, "ls\r")
	resp, _ = a.handleHistoryList(adminCtx(), domain.SshHistoryListReq{})
	if len(resp.Items) != 2 || resp.Items[0] != "ls" {
		t.Fatalf("dedup should move repeated command to front: %v", resp.Items)
	}
}

func TestRecordHistorySanitizesANSI(t *testing.T) {
	a := newTestActor(t)
	sess := &session{id: "s1", owner: "admin-1"}

	a.recordHistory(sess, "\x1b[32mfree\x1b[0m -m\r")
	resp, _ := a.handleHistoryList(adminCtx(), domain.SshHistoryListReq{})
	if len(resp.Items) != 1 || resp.Items[0] != "free -m" {
		t.Fatalf("ANSI escapes should be stripped: %v", resp.Items)
	}
}

func TestHistoryListLimit(t *testing.T) {
	a := newTestActor(t)
	a.History = []string{"c", "b", "a"}

	resp, err := a.handleHistoryList(adminCtx(), domain.SshHistoryListReq{Limit: 2})
	if err != nil {
		t.Fatalf("history list failed: %v", err)
	}
	if len(resp.Items) != 2 || resp.Items[0] != "c" || resp.Items[1] != "b" {
		t.Fatalf("limit should return most-recent 2: %v", resp.Items)
	}
}

func TestHistoryRejectsAnonymous(t *testing.T) {
	a := newTestActor(t)
	_, err := a.handleHistoryList(anonCtx(), domain.SshHistoryListReq{})
	if err == nil {
		t.Fatal("anonymous caller must be rejected")
	}
}

func TestCommandsAndHistoryPersistRoundTrip(t *testing.T) {
	a := newTestActor(t)

	// Populate state through handlers.
	a.handleCommandCreate(adminCtx(), domain.SshCommandCreateReq{
		Name: "ping", Content: "ping -c 3 8.8.8.8", Category: "Network",
	})
	sess := &session{id: "s1", owner: "admin-1"}
	a.recordHistory(sess, "uptime\r")

	// Reload from the same store.
	loaded := &Actor{store: a.store}
	if err := loaded.Load(); err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if len(loaded.Commands) != 1 || loaded.Commands[0].Name != "ping" {
		t.Fatalf("commands not persisted: %+v", loaded.Commands)
	}
	if len(loaded.History) != 1 || loaded.History[0] != "uptime" {
		t.Fatalf("history not persisted: %v", loaded.History)
	}
}
