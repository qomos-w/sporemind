package pluginhost

import (
	"strings"
	"testing"
	"unicode/utf8"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// TestPluginLogRingCaptureAndQuery exercises the LogPluginEntry funnel → ring
// → handlePluginLogs path: backend entries land in order, frontend puts are
// tagged, and the process verdict defaults to running for unknown plugins.
// State transitions are reported via setProcessState (the explicit transport
// seam), not inferred from log strings.
func TestPluginLogRingCaptureAndQuery(t *testing.T) {
	a := &Actor{}

	a.LogPluginEntry("app.test", 0, 1, "handler says hi")
	a.LogPluginEntry("app.test", 0, 3, "plugin stderr: boom")

	if _, err := a.handlePluginLogPut(nil, gen.PluginLogPutReq{PluginID: "app.test", Level: "warn", Message: "frontend warn"}); err != nil {
		t.Fatalf("plugin_log_put: %v", err)
	}

	resp, err := a.handlePluginLogs(nil, gen.PluginLogsReq{PluginID: "app.test"})
	if err != nil {
		t.Fatalf("plugin_logs: %v", err)
	}
	if len(resp.Entries) != 3 {
		t.Fatalf("want 3 entries, got %d: %+v", len(resp.Entries), resp.Entries)
	}
	wantSources := []string{"backend", "backend", "frontend"}
	for i, e := range resp.Entries {
		if e.Source != wantSources[i] {
			t.Errorf("entry %d: source=%s want=%s", i, e.Source, wantSources[i])
		}
	}
	if resp.Entries[0].Level != "info" || resp.Entries[1].Level != "error" || resp.Entries[2].Level != "warn" {
		t.Errorf("level mapping wrong: %+v", resp.Entries)
	}
	if resp.ProcessState != "running" {
		t.Errorf("default process state = %q, want running", resp.ProcessState)
	}

	// Crash transition flips the verdict and records the cause.
	a.setProcessState("app.test", "crashed", "session read: EOF", "", 0)
	resp, err = a.handlePluginLogs(nil, gen.PluginLogsReq{PluginID: "app.test"})
	if err != nil {
		t.Fatalf("plugin_logs after crash: %v", err)
	}
	if resp.ProcessState != "crashed" {
		t.Errorf("process state = %q, want crashed", resp.ProcessState)
	}
	if resp.Crash == "" || !strings.Contains(resp.Crash, "EOF") {
		t.Errorf("crash cause missing: %q", resp.Crash)
	}

	// Respawn transition recovers the verdict.
	a.setProcessState("app.test", "running", "", "", 0)
	resp, _ = a.handlePluginLogs(nil, gen.PluginLogsReq{PluginID: "app.test"})
	if resp.ProcessState != "running" {
		t.Errorf("process state after respawn = %q, want running", resp.ProcessState)
	}
	if resp.Crash != "" {
		t.Errorf("crash cause must clear on respawn, got %q", resp.Crash)
	}

	// Limit returns the newest tail.
	resp, _ = a.handlePluginLogs(nil, gen.PluginLogsReq{PluginID: "app.test", Limit: 2})
	if len(resp.Entries) != 2 || resp.Entries[1].Message != "frontend warn" {
		t.Errorf("limit tail wrong: %+v", resp.Entries)
	}

	// Unknown plugin: empty ring, default verdict.
	resp, err = a.handlePluginLogs(nil, gen.PluginLogsReq{PluginID: "app.none"})
	if err != nil {
		t.Fatalf("plugin_logs unknown: %v", err)
	}
	if len(resp.Entries) != 0 || resp.ProcessState != "running" {
		t.Errorf("unknown plugin resp wrong: %+v", resp)
	}
}

// TestSetProcessStateOnTransition pins the injectable onTransition seam
// (the T2 appmanager hook; nil is a no-op): it fires only on an actual
// state change — first record, crashed, stopped — never on a repeat of the
// current state.
func TestSetProcessStateOnTransition(t *testing.T) {
	a := &Actor{}
	var calls []string
	a.processStateOnTransition = func(pluginID, state, crash, httpAddr string) {
		calls = append(calls, pluginID+" "+state+" "+crash)
	}

	a.setProcessState("app.t", "running", "", "", 0)
	a.setProcessState("app.t", "running", "", "", 0) // repeat: no call
	if len(calls) != 1 || calls[0] != "app.t running " {
		t.Fatalf("calls = %v, want exactly the initial running transition", calls)
	}

	a.setProcessState("app.t", "crashed", "boom", "", 0)
	a.setProcessState("app.t", "crashed", "boom again", "", 0) // repeat: no call
	if len(calls) != 2 || calls[1] != "app.t crashed boom" {
		t.Fatalf("calls = %v, want the crashed transition", calls)
	}
	// A repeated crashed report with a new cause still updates the stored
	// cause even though the transition callback stays silent.
	if _, crash := a.processState("app.t"); crash != "boom again" {
		t.Fatalf("stored crash = %q, want boom again", crash)
	}

	a.setProcessState("app.t", "stopped", "", "", 0)
	if len(calls) != 3 || calls[2] != "app.t stopped " {
		t.Fatalf("calls = %v, want the stopped transition", calls)
	}
	if _, crash := a.processState("app.t"); crash != "" {
		t.Fatalf("crash must clear on non-crashed state, got %q", crash)
	}

	// Nil callback (the default) is a no-op, not a panic.
	plain := &Actor{}
	plain.setProcessState("app.x", "crashed", "EOF", "", 0)
	if state, _ := plain.processState("app.x"); state != "crashed" {
		t.Fatalf("state = %q, want crashed", state)
	}
}

// TestProcessStateBoundToGeneration pins the reload-ordering rule: a state
// report from a superseded process generation must never overwrite the
// current verdict, and plugin_logs surfaces the CURRENT process generation —
// not the generation of whichever process happened to report last.
func TestProcessStateBoundToGeneration(t *testing.T) {
	a := &Actor{}

	// Gen 1 runs, then a hot reload spawns gen 2.
	gen1 := a.NextProcessGeneration("app.hot")
	a.setProcessState("app.hot", "running", "", "127.0.0.1:4001", gen1)
	gen2 := a.NextProcessGeneration("app.hot")
	a.setProcessState("app.hot", "running", "", "127.0.0.1:4002", gen2)
	if gen2 <= gen1 {
		t.Fatalf("generations must grow: %d then %d", gen1, gen2)
	}

	// The old process's late exit reports AFTER the replacement's spawn:
	// dropped — the current verdict stays running on the new listener.
	a.setProcessState("app.hot", "stopped", "", "", gen1)
	a.setProcessState("app.hot", "crashed", "stdout closed", "", gen1)
	state, crash := a.processState("app.hot")
	if state != "running" || crash != "" {
		t.Fatalf("stale gen-1 report leaked through: state=%q crash=%q", state, crash)
	}

	// plugin_logs reports the current generation and its verdict.
	resp, err := a.handlePluginLogs(nil, gen.PluginLogsReq{PluginID: "app.hot"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Generation != int32(gen2) || resp.ProcessState != "running" {
		t.Fatalf("plugin_logs = gen %d state %q, want gen %d running", resp.Generation, resp.ProcessState, gen2)
	}

	// The current generation's own crash still lands.
	a.setProcessState("app.hot", "crashed", "segfault", "", gen2)
	if state, crash := a.processState("app.hot"); state != "crashed" || crash != "segfault" {
		t.Fatalf("current-gen crash lost: %q/%q", state, crash)
	}

	// A plain unload (no replacement) keeps its stopped verdict.
	a.setProcessState("app.hot", "stopped", "", "", gen2)
	if state, _ := a.processState("app.hot"); state != "stopped" {
		t.Fatalf("current-gen stop lost: %q", state)
	}
}

// TestPluginLogRingOverflowAndTruncation pins the bounded-ring and message
// truncation rules (512 entries / 512 bytes).
func TestPluginLogRingOverflowAndTruncation(t *testing.T) {
	a := &Actor{}
	long := strings.Repeat("x", 2000)
	for i := 0; i < pluginLogRingCapacity+10; i++ {
		a.LogPluginEntry("app.big", 0, 1, long)
	}
	resp, err := a.handlePluginLogs(nil, gen.PluginLogsReq{PluginID: "app.big"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Entries) != pluginLogRingCapacity {
		t.Fatalf("ring must cap at %d, got %d", pluginLogRingCapacity, len(resp.Entries))
	}
	if resp.Dropped != 10 {
		t.Errorf("dropped = %d, want 10", resp.Dropped)
	}
	for _, e := range resp.Entries {
		if len(e.Message) > pluginLogMaxMessage+len("...(truncated)") {
			t.Fatalf("entry not truncated: %d bytes", len(e.Message))
		}
	}

	// Unload resets the capture surface.
	a.resetPluginLogs("app.big")
	resp, _ = a.handlePluginLogs(nil, gen.PluginLogsReq{PluginID: "app.big"})
	if len(resp.Entries) != 0 || resp.Dropped != 0 {
		t.Errorf("reset must clear ring: %+v", resp)
	}
}

// TestPluginLogRingUTF8Sanitization pins the strict-codec safety rule: the
// plugin_logs response codec rejects invalid UTF-8, so the ring must coerce
// invalid bytes (raw stderr) and truncate the 512-byte cap on a rune boundary
// instead of splitting a multi-byte character — one bad entry otherwise fails
// the whole query (gospore.handler.codec_encode).
func TestPluginLogRingUTF8Sanitization(t *testing.T) {
	a := &Actor{}
	a.LogPluginEntry("app.utf8", 0, 1, "bad \xff\xfe bytes")
	a.LogPluginEntry("app.utf8", 0, 1, strings.Repeat("\u4e2d", 300)) // 900 bytes, 3 bytes/rune

	resp, err := a.handlePluginLogs(nil, gen.PluginLogsReq{PluginID: "app.utf8"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(resp.Entries))
	}
	for i, e := range resp.Entries {
		if !utf8.ValidString(e.Message) {
			t.Fatalf("entry %d holds invalid UTF-8: %q", i, e.Message)
		}
	}
	if !strings.HasSuffix(resp.Entries[1].Message, "...(truncated)") {
		t.Errorf("long entry not marked truncated: %q", resp.Entries[1].Message)
	}

	// The stderr funnel must obey the same rule.
	if s := truncateForLog([]byte("panic \xff\xfe")); !utf8.ValidString(s) {
		t.Errorf("truncateForLog kept invalid UTF-8: %q", s)
	}
	s := truncateForLog([]byte(strings.Repeat("\u4e2d", 300)))
	if !utf8.ValidString(s) || !strings.HasSuffix(s, "...(truncated)") {
		t.Errorf("truncateForLog split a rune or lost the marker: %q", s)
	}
}
