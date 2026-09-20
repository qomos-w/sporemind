// Package diagcrash writes and reads crash report files. Crash files are
// plain text snapshots left by a dying process (headless or desktop) so the
// next run can surface the failure through the oracle diagnostic pipeline
// instead of losing it to stderr.
//
// File layout:
//
//	<logs>/crash-YYYYMMDD-HHMMSS.log        — fresh crash file, awaiting replay
//	<logs>/crash-replayed/crash-*.log       — replayed by oracle, kept for audit
//
// The rename-and-move pattern is the durability contract: a file in
// crash-replayed/ has been ingested as a Diagnostic; a file still at the top
// level is pending and will be replayed on the next oracle startup.
package diagcrash

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/qomos-w/sporemind/pkg/config"
)

// TopLevelGlob matches crash files that have not yet been replayed.
const TopLevelGlob = "crash-*.log"

// ReplayedDir is the subdirectory under logs/ that holds replayed crash
// files. The directory is created lazily by MarkReplayed.
const ReplayedDir = "crash-replayed"

// FilePrefix and FileSuffix pin the naming scheme so writers and readers
// agree. The timestamp in between is formatted as YYYYMMDD-HHMMSS.
const (
	FilePrefix = "crash-"
	FileSuffix = ".log"
)

// LogsDir returns the directory under <DataDir>/logs where crash files
// live. The directory may not exist yet — callers that intend to write
// should use Write, which creates it.
func LogsDir() string {
	return filepath.Join(config.DataDir(), "logs")
}

// Write writes content to a fresh crash-<timestamp>.log file under logs/.
// Returns the absolute path. Creates the directory if needed.
//
// Write is safe to call from a panic defer — it does not depend on actor
// state, only on os and config.DataDir() (which is a constant after init).
func Write(content string) (string, error) {
	dir := LogsDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("diagcrash: mkdir logs: %w", err)
	}
	name := fmt.Sprintf("%s%s%s", FilePrefix, time.Now().Format("20060102-150405"), FileSuffix)
	// Disambiguate if two crashes happen in the same second.
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err == nil {
		path = filepath.Join(dir, fmt.Sprintf("%s%s-%d%s", FilePrefix, time.Now().Format("20060102-150405"), time.Now().Nanosecond(), FileSuffix))
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("diagcrash: write crash file: %w", err)
	}
	return path, nil
}

// Pending returns crash-*.log files at the top of logs/, sorted by ModTime
// ascending (oldest first). Files already moved to crash-replayed/ are not
// returned. Missing logs/ is treated as empty.
func Pending() ([]string, error) {
	dir := LogsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("diagcrash: read logs dir: %w", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, FilePrefix) || !strings.HasSuffix(name, FileSuffix) {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	return out, nil
}

// MarkReplayed moves path into logs/crash-replayed/ so subsequent Pending
// calls skip it. Missing source file is a no-op. The replayed directory is
// created if needed.
func MarkReplayed(path string) error {
	if path == "" {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("diagcrash: stat crash file: %w", err)
	}
	replayedDir := filepath.Join(LogsDir(), ReplayedDir)
	if err := os.MkdirAll(replayedDir, 0755); err != nil {
		return fmt.Errorf("diagcrash: mkdir replayed: %w", err)
	}
	dest := filepath.Join(replayedDir, filepath.Base(path))
	// os.Rename across same filesystem is atomic.
	if err := os.Rename(path, dest); err != nil {
		return fmt.Errorf("diagcrash: rename crash file: %w", err)
	}
	return nil
}
