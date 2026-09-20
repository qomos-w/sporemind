package oracle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/diagcrash"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestIngestCrashReports_ReplaysAndMarksFiles seeds a crash-*.log file
// under <DataDir>/logs/, calls ingestCrashReports, and verifies:
//   - exactly one Diagnostic with Source="process.crash" is added;
//   - the file is moved to crash-replayed/;
//   - a second ingest pass does not double-report (Pending is empty).
func TestIngestCrashReports_ReplaysAndMarksFiles(t *testing.T) {
	dir := t.TempDir()
	config.SetDataDirForTest(dir)

	if _, err := diagcrash.Write("sporemind crash report\nTime: 2026-08-11\nPanic: boom\n\nStack:\nfake stack\n"); err != nil {
		t.Fatalf("seed crash file: %v", err)
	}

	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	a.ingestCrashReports(ctx)

	if len(a.diagnostics) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(a.diagnostics))
	}
	d := a.diagnostics[0]
	if d.Source != "process.crash" {
		t.Errorf("Source = %q, want process.crash", d.Source)
	}
	if !strings.Contains(d.Message, "sporemind crash report") {
		t.Errorf("Message = %q, want first-line header", d.Message)
	}
	if !strings.Contains(d.RawData, "boom") || !strings.Contains(d.RawData, "fake stack") {
		t.Errorf("RawData missing panic or stack: %q", d.RawData)
	}

	// File moved to replayed dir.
	pending, err := diagcrash.Pending()
	if err != nil {
		t.Fatalf("Pending after: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("Pending after ingest = %v, want empty", pending)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "logs", diagcrash.ReplayedDir))
	if err != nil {
		t.Fatalf("read replayed dir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("replayed dir entries = %d, want 1", len(entries))
	}

	// Second call should not double-report.
	a.ingestCrashReports(ctx)
	if len(a.diagnostics) != 1 {
		t.Errorf("after 2nd ingest: diagnostics = %d, want 1", len(a.diagnostics))
	}
}

// TestIngestCrashReports_EmptyIsNoop confirms no error path is taken when
// there are no crash files to replay.
func TestIngestCrashReports_EmptyIsNoop(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())

	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	a.ingestCrashReports(ctx)
	if len(a.diagnostics) != 0 {
		t.Errorf("expected 0 diagnostics, got %d", len(a.diagnostics))
	}
}
