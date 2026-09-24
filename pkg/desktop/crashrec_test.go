package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/gospore/gateway"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseCrashFileName(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
		kind string
		pid  int
	}{
		{"20260910-215500-1234-panic.log", true, CrashKindPanic, 1234},
		{"20260910-215500-1234-fatal.log", true, CrashKindFatal, 1234},
		{"20260910-215500-1234-native.json", true, CrashKindNative, 1234},
		{"20260910-215500-1234-native.dmp", false, "", 0},
		{"20260910-2155-1234-panic.log", false, "", 0},
		{"20260910-215500-x-panic.log", false, "", 0},
		{"20260910-215500-1234-unknown.log", false, "", 0},
		{"20260910-215500-1234-panic.json", false, "", 0},
		{"session.json", false, "", 0},
		{"ack.json", false, "", 0},
		{"errors.log", false, "", 0},
	}
	for _, c := range cases {
		meta, ok := parseCrashFileName(c.name)
		if ok != c.ok {
			t.Errorf("parseCrashFileName(%q) ok=%v, want %v", c.name, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if meta.kind != c.kind || meta.pid != c.pid {
			t.Errorf("parseCrashFileName(%q) = kind %q pid %d, want %q %d", c.name, meta.kind, meta.pid, c.kind, c.pid)
		}
	}
}

func TestCrashSessionLifecycle(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	// First boot: claim, clean finish, clean end.
	cs := claimCrashSession(dir, 111, now)
	if cs.abnormal {
		t.Fatal("first claim reported abnormal exit")
	}
	if _, err := os.Stat(filepath.Join(dir, crashSessionFile)); err != nil {
		t.Fatalf("marker not written: %v", err)
	}
	report := finishBootCrashReport(dir, cs, nil, now)
	if report.AbnormalExit || len(report.Records) != 0 {
		t.Fatalf("clean boot report: abnormal=%v records=%d", report.AbnormalExit, len(report.Records))
	}
	_ = os.Remove(filepath.Join(dir, crashSessionFile))

	// Next boot after clean end: not abnormal.
	cs = claimCrashSession(dir, 222, now)
	if cs.abnormal {
		t.Fatal("session after clean end still reported abnormal")
	}

	// Simulate a crash: marker stays, no EndCrashSession.
	claimCrashSession(dir, 333, now)
	cs = claimCrashSession(dir, 444, now)
	if !cs.abnormal {
		t.Fatal("crashed session not detected as abnormal exit")
	}
	if cs.prev == nil || cs.prev.PID != 333 {
		t.Fatalf("previous session = %+v, want pid 333", cs.prev)
	}
	report = finishBootCrashReport(dir, cs, nil, now)
	if len(report.Records) != 1 || report.Records[0].Kind != CrashKindAbnormalExit {
		t.Fatalf("records = %+v, want one abnormal-exit record", report.Records)
	}

	// Acknowledge the synthesized record: it must not reappear.
	ackCrashRecords(dir, []string{report.Records[0].ID})
	_ = os.Remove(filepath.Join(dir, crashSessionFile))
	cs = claimCrashSession(dir, 555, now)
	report = finishBootCrashReport(dir, cs, nil, now)
	if report.AbnormalExit || len(report.Records) != 0 {
		t.Fatalf("after ack: abnormal=%v records=%+v, want none", report.AbnormalExit, report.Records)
	}
}

// TestEarlyStartupCrashDetected covers a process that dies between claim and
// finish (e.g. a crash inside application.New): the marker it claimed must
// surface as an abnormal exit on the next boot.
func TestEarlyStartupCrashDetected(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	// Previous session ended cleanly.
	claimCrashSession(dir, 111, now)
	_ = os.Remove(filepath.Join(dir, crashSessionFile))

	// This session claims the marker but crashes before finishing startup.
	claimCrashSession(dir, 222, now)

	cs := claimCrashSession(dir, 333, now)
	if !cs.abnormal {
		t.Fatal("early-startup crash not detected")
	}
	report := finishBootCrashReport(dir, cs, nil, now)
	if len(report.Records) != 1 || report.Records[0].Kind != CrashKindAbnormalExit {
		t.Fatalf("records = %+v, want one abnormal-exit record", report.Records)
	}
}

func TestBootReportWithPanicRecord(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	claimCrashSession(dir, 111, now)
	writeTestFile(t, dir, "20260910-215500-111-panic.log",
		"sporemind crash report\nTime: 2026-09-10T21:55:00Z\nError: runtime error: index out of range\n\nStackTrace:\ngoroutine 1 [running]:\n")

	cs := claimCrashSession(dir, 222, now)
	report := finishBootCrashReport(dir, cs, nil, now)
	if !report.AbnormalExit {
		t.Fatal("abnormal exit not detected")
	}
	if len(report.Records) != 1 {
		t.Fatalf("records = %+v, want the panic record only", report.Records)
	}
	rec := report.Records[0]
	if rec.Kind != CrashKindPanic || rec.PID != 111 {
		t.Fatalf("record = %+v", rec)
	}
	if rec.Summary != "runtime error: index out of range" {
		t.Fatalf("summary = %q", rec.Summary)
	}
	if !strings.Contains(rec.Detail, "goroutine 1 [running]") {
		t.Fatalf("detail missing stack: %q", rec.Detail)
	}
}

func TestScanCrashRecordsNativeAndAck(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "20260910-215500-1234-native.json",
		`{"code":"0xC0000005","address":"0x7FF800000000","thread":42,"time":"2026-09-10T21:55:00+08:00","pid":1234,"version":"dev"}`)
	writeTestFile(t, dir, "20260910-215500-1234-native.dmp", "FAKEDUMP")
	writeTestFile(t, dir, "20260910-215501-1234-fatal.log", "sporemind fatal error\nTime: x\nError: webview2 exploded\n")
	writeTestFile(t, dir, "unrelated.txt", "ignore me")

	records := scanCrashRecords(dir, nil)
	if len(records) != 2 {
		t.Fatalf("records = %+v, want 2", records)
	}
	var native *CrashRecordInfo
	for i := range records {
		if records[i].Kind == CrashKindNative {
			native = &records[i]
		}
	}
	if native == nil {
		t.Fatalf("native record missing: %+v", records)
	}
	if !strings.Contains(native.Summary, "EXCEPTION_ACCESS_VIOLATION") || !strings.Contains(native.Summary, "0xC0000005") {
		t.Fatalf("native summary = %q", native.Summary)
	}
	if native.DumpFile == "" || !strings.HasSuffix(native.DumpFile, ".dmp") {
		t.Fatalf("dump file = %q", native.DumpFile)
	}

	acked := map[string]bool{records[0].ID: true}
	if got := scanCrashRecords(dir, acked); len(got) != 1 || got[0].ID == records[0].ID {
		t.Fatalf("acked filter failed: %+v", got)
	}
}

func TestAppendCrashErrorAndTail(t *testing.T) {
	dir := t.TempDir()
	appendCrashError(dir, fmt.Errorf("webview2 fatal: device lost"), time.Now())
	appendCrashError(dir, fmt.Errorf("second failure"), time.Now())

	lines := crashErrorsTailLines(dir, 10)
	if len(lines) != 2 {
		t.Fatalf("tail lines = %d, want 2", len(lines))
	}
	if !strings.Contains(lines[0], "webview2 fatal: device lost") {
		t.Fatalf("first line = %q", lines[0])
	}
	if !strings.Contains(lines[0], "CrashErrorHandler") && !strings.Contains(lines[0], "appendCrashError") && !strings.Contains(lines[0], "desktop.") {
		t.Fatalf("stack hint missing from line: %q", lines[0])
	}

	// FatalError instances cannot be constructed outside the wails package,
	// so the dedicated fatal record-file branch is exercised only via the
	// application error handler in production.
}

// TestErrorsTailAckAdvances covers the errors.log acknowledge offset: the
func TestAppendCrashErrorSkipsBenignWailsNoise(t *testing.T) {
	dir := t.TempDir()
	appendCrashError(dir, fmt.Errorf("Worker request cancellation setup: The parameter is incorrect."), time.Now())
	appendCrashError(dir, fmt.Errorf("Resuming attached WebView target: The parameter is incorrect."), time.Now())
	if lines := crashErrorsTailLines(dir, 10); len(lines) != 0 {
		t.Fatalf("benign wails noise recorded: %v", lines)
	}

	appendCrashError(dir, fmt.Errorf("real failure"), time.Now())
	lines := crashErrorsTailLines(dir, 10)
	if len(lines) != 1 || !strings.Contains(lines[0], "real failure") {
		t.Fatalf("real error lost: %v", lines)
	}
}

// surfaced tail must disappear after a dismiss and only freshly appended
// entries may reappear on a later fetch.
func TestErrorsTailAckAdvances(t *testing.T) {
	dir := t.TempDir()
	appendCrashError(dir, fmt.Errorf("first failure"), time.Now())
	appendCrashError(dir, fmt.Errorf("second failure"), time.Now())

	report := finishBootCrashReport(dir, ClaimedCrashSession{}, nil, time.Now())
	if len(report.ErrorsTail) != 2 {
		t.Fatalf("tail lines = %d, want 2", len(report.ErrorsTail))
	}

	ackCrashRecords(dir, []string{"some-record"})
	report = finishBootCrashReport(dir, ClaimedCrashSession{}, nil, time.Now())
	if len(report.ErrorsTail) != 0 {
		t.Fatalf("tail after ack = %v, want empty", report.ErrorsTail)
	}

	appendCrashError(dir, fmt.Errorf("third failure"), time.Now())
	report = finishBootCrashReport(dir, ClaimedCrashSession{}, nil, time.Now())
	if len(report.ErrorsTail) != 1 || !strings.Contains(report.ErrorsTail[0], "third failure") {
		t.Fatalf("tail after new append = %v, want only the third failure", report.ErrorsTail)
	}
}

// TestRefreshViewAfterAck covers the same-process refresh path: the boot
// report served after a webview reload must reflect acknowledgements made
// since boot instead of replaying the boot-time snapshot.
func TestRefreshViewAfterAck(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	claimCrashSession(dir, 111, now) // crashed previous session marker
	cs := claimCrashSession(dir, 222, now)
	if !cs.abnormal {
		t.Fatal("crashed session not detected")
	}

	base := finishBootCrashReport(dir, cs, nil, time.Now())
	if len(base.Records) != 1 || base.Records[0].Kind != CrashKindAbnormalExit {
		t.Fatalf("records = %+v, want one abnormal-exit record", base.Records)
	}

	// Refresh before dismiss: the view keeps serving the undismissed record.
	records, abnormal := crashRecordsView(dir, base.AbnormalExit, base.PreviousSession)
	if !abnormal || len(records) != 1 {
		t.Fatalf("refresh view before ack = (%v, %v), want one record still abnormal", records, abnormal)
	}

	// Dismiss, then refresh again: everything must be cleared.
	ackCrashRecords(dir, []string{base.Records[0].ID})
	records, abnormal = crashRecordsView(dir, base.AbnormalExit, base.PreviousSession)
	if abnormal || len(records) != 0 {
		t.Fatalf("refresh view after ack = (%v, %v), want empty and not abnormal", records, abnormal)
	}
}

func TestAppendCrashErrorTrimsOversizedLog(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("x", 300*1024)
	writeTestFile(t, dir, crashErrorsFile, big+"\n")
	appendCrashError(dir, fmt.Errorf("trigger"), time.Now())
	st, err := os.Stat(filepath.Join(dir, crashErrorsFile))
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() > crashErrorsMax+1024 {
		t.Fatalf("errors.log size = %d, exceeds cap %d", st.Size(), crashErrorsMax)
	}
	data, _ := os.ReadFile(filepath.Join(dir, crashErrorsFile))
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if !strings.Contains(lines[len(lines)-1], "trigger") {
		t.Fatalf("newest entry lost after trim: %q", lines[len(lines)-1])
	}
}

func TestPruneCrashRecords(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	old := now.Add(-45 * 24 * time.Hour)
	writeTestFile(t, dir, "20260101-000000-1-panic.log", "old crash")
	if err := os.Chtimes(filepath.Join(dir, "20260101-000000-1-panic.log"), old, old); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, dir, "20260910-000000-2-native.json", `{}`)
	writeTestFile(t, dir, "20260910-000000-2-native.dmp", "d1")
	for i := 0; i < 7; i++ {
		name := fmt.Sprintf("20260901-00000%d-3-native", i)
		writeTestFile(t, dir, name+".json", `{}`)
		writeTestFile(t, dir, name+".dmp", "d")
		mtime := now.Add(time.Duration(-i) * time.Hour)
		if err := os.Chtimes(filepath.Join(dir, name+".json"), mtime, mtime); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(filepath.Join(dir, name+".dmp"), mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, dir, "orphan.dmp", "orphan")

	pruneCrashRecords(dir, now)

	if _, err := os.Stat(filepath.Join(dir, "20260101-000000-1-panic.log")); !os.IsNotExist(err) {
		t.Fatal("expired record not pruned")
	}
	if _, err := os.Stat(filepath.Join(dir, "orphan.dmp")); !os.IsNotExist(err) {
		t.Fatal("orphan dump not pruned")
	}
	dumps := 0
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".dmp") {
			dumps++
		}
	}
	if dumps > crashRetainDumps {
		t.Fatalf("dumps kept = %d, want <= %d", dumps, crashRetainDumps)
	}
}

func TestFormatLogTail(t *testing.T) {
	entries := make([]gateway.LogEntry, 60)
	for i := range entries {
		entries[i] = gateway.LogEntry{Timestamp: "t", Level: "info", Message: fmt.Sprintf("m%d", i)}
	}
	tail := formatLogTail(entries, 10)
	if len(tail) != 10 {
		t.Fatalf("tail len = %d", len(tail))
	}
	if !strings.Contains(tail[len(tail)-1], "m59") {
		t.Fatalf("tail should keep newest, last = %q", tail[len(tail)-1])
	}
}

func TestPanicHandlerWritesCrashRecord(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())
	writeCrashReport(&application.PanicDetails{Error: fmt.Errorf("boom")})
	matches, _ := filepath.Glob(filepath.Join(config.DataDir(), "crashes", "*-panic.log"))
	if len(matches) != 1 {
		t.Fatalf("panic report not written under crashes/: %v", matches)
	}
}
