package aistats

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// migrateLegacyWorkspaces moves per-workspace record files and checkpoints from
// the legacy layout (aistats/<workspaceID>/...) into the single global
// namespace (aistats/...). It is best-effort: a failed legacy dir is skipped,
// not fatal. It is a no-op when no legacy workspace directories exist.
//
// This is a raw filesystem operation that runs before the persist store is
// initialized. Record files keep their UUID names so collisions across
// workspaces are virtually impossible; when a target already exists (re-run)
// the file is skipped. The merged checkpoint is written only after records are
// moved so a crash leaves data recoverable from disk.
//
// Returns the number of record files migrated and the count of legacy dirs
// that failed (logged by the caller).
func migrateLegacyWorkspaces(globalRoot string) (moved, failed int, err error) {
	parent := filepath.Dir(globalRoot)
	base := filepath.Base(globalRoot) // "aistats"

	entries, err := os.ReadDir(parent)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}

	var merged checkpoint
	merged.Session = map[string]gen.AIStatsCounters{}
	merged.Project = map[string]gen.AIStatsCounters{}
	merged.Provider = map[string]gen.AIStatsCounters{}
	merged.Model = map[string]gen.AIStatsCounters{}
	merged.WorkspaceMap = map[string]gen.AIStatsCounters{}
	haveMerged := false

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if name == base {
			continue
		}
		legacyRoot := filepath.Join(parent, name)
		// Only treat as a legacy aistats workspace if it has a records/ or
		// checkpoint.json inside; otherwise leave it alone.
		if !isLegacyAistatsDir(legacyRoot) {
			continue
		}

		n, moveErr := moveRecords(legacyRoot, globalRoot)
		if moveErr != nil {
			failed++
			continue
		}
		moved += n

		if cp, cpErr := loadCheckpointFS(legacyRoot); cpErr == nil && cp != nil {
			mergeCheckpoint(&merged, cp.snapshot())
			haveMerged = true
		}
	}

	if haveMerged {
		// Fold the merged legacy aggregates into any existing global checkpoint.
		if existing, err := loadCheckpointFS(globalRoot); err == nil && existing != nil {
			mergeCheckpoint(&merged, existing.snapshot())
		}
		if err := writeCheckpointFS(globalRoot, &merged); err != nil {
			return moved, failed, err
		}
	}

	return moved, failed, nil
}

// isLegacyAistatsDir reports whether dir looks like a legacy per-workspace
// aistats root (contains records/ or checkpoint.json).
func isLegacyAistatsDir(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "checkpoint.json")); err == nil {
		return true
	}
	if info, err := os.Stat(filepath.Join(dir, "records")); err == nil && info.IsDir() {
		return true
	}
	return false
}

// moveRecords copies every record JSON file from legacyRoot/records/ into
// globalRoot/records/, preserving the month subdirectories. Files that already
// exist at the destination are skipped. Returns the number of files moved.
func moveRecords(legacyRoot, globalRoot string) (int, error) {
	legacyRecords := filepath.Join(legacyRoot, "records")
	moved := 0
	err := filepath.WalkDir(legacyRecords, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(legacyRecords, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(globalRoot, "records", rel)
		if _, err := os.Stat(dst); err == nil {
			return nil // already present (idempotent re-run)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := persist.WriteFileAtomic(dst, data, 0o644); err != nil {
			return err
		}
		moved++
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return moved, err
	}
	return moved, nil
}

// mergeCheckpoint adds src's aggregates into dst in place.
func mergeCheckpoint(dst, src *checkpoint) {
	dst.Workspace = addCounters(dst.Workspace, src.Workspace)
	for k, v := range src.WorkspaceMap {
		dst.WorkspaceMap[k] = addCounters(dst.WorkspaceMap[k], v)
	}
	for k, v := range src.Session {
		dst.Session[k] = addCounters(dst.Session[k], v)
	}
	for k, v := range src.Project {
		dst.Project[k] = addCounters(dst.Project[k], v)
	}
	for k, v := range src.Provider {
		dst.Provider[k] = addCounters(dst.Provider[k], v)
	}
	for k, v := range src.Model {
		dst.Model[k] = addCounters(dst.Model[k], v)
	}
}

// migrateFSToDB copies all record files, checkpoint, and cost rates from the
// legacy filesystem layout (<fsRoot>/records/, <fsRoot>/checkpoint.json,
// <fsRoot>/costs/) into the persist store. It is idempotent and interruptible:
//
//   - Idempotent: a "migrated" marker document gates the migration. Once
//     written, subsequent startups skip the migration entirely. If interrupted
//     before the marker is written, re-running overwrites the same documents
//     (persist.Save is idempotent for the same name+value).
//   - Interruptible: the migration writes each record, month marker, and cost
//     rate individually via store.Save. A crash leaves a partial DB that is
//     safe to re-run from scratch. The checkpoint is written after records so
//     a crash leaves the DB with records but possibly a stale or missing
//     checkpoint — on restart the migration re-runs and writes the correct
//     checkpoint.
//   - No source deletion: the legacy filesystem tree is left intact. Rollback
//     is achieved by changing the config backend back to "fs".
//
// Returns the number of record documents migrated.
func migrateFSToDB(fsRoot string, store persist.Persist) (int, error) {
	// Gate: skip if already migrated.
	var migrated bool
	if err := store.Load("migrated", &migrated); err != nil {
		if !errors.Is(err, persist.ErrNotExist) {
			return 0, fmt.Errorf("aistats: check migration marker: %w", err)
		}
	} else if migrated {
		return 0, nil
	}

	recordsDir := filepath.Join(fsRoot, "records")
	moved := 0

	// Walk the legacy records tree and save each record + month marker to DB.
	walkErr := filepath.WalkDir(recordsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil // no records directory
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil // skip unreadable files
		}
		var r gen.AIStatsRecord
		if err := json.Unmarshal(data, &r); err != nil {
			return nil // skip corrupt records
		}
		if err := store.Save(recordName(r), r); err != nil {
			return fmt.Errorf("aistats: migrate record %s: %w", r.ID, err)
		}
		// Save month marker (idempotent overwrite).
		if err := store.Save(monthMarkerName(recordMonth(r)), true); err != nil {
			return fmt.Errorf("aistats: migrate month marker: %w", err)
		}
		moved++
		return nil
	})
	if walkErr != nil {
		return moved, fmt.Errorf("aistats: migrate records: %w", walkErr)
	}

	// Migrate checkpoint from fs. Overwrite any partial DB checkpoint from
	// an interrupted prior run — the fs checkpoint is the authoritative
	// pre-migration state.
	if cp, cpErr := loadCheckpointFS(fsRoot); cpErr == nil && cp != nil {
		if err := store.Save("checkpoint", cp.snapshot()); err != nil {
			return moved, fmt.Errorf("aistats: migrate checkpoint: %w", err)
		}
	}

	// Migrate cost rates from fs.
	costsDir := filepath.Join(fsRoot, "costs")
	entries, readErr := os.ReadDir(costsDir)
	if readErr == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(costsDir, e.Name()))
			if err != nil {
				continue
			}
			var rate gen.AIStatsCostRate
			if err := json.Unmarshal(data, &rate); err != nil {
				continue
			}
			name := "costs/" + safeFilename(rate.Provider+"--"+rate.Model)
			_ = store.Save(name, rate)
		}
	}

	// Write migration marker last — only after all data is migrated.
	if err := store.Save("migrated", true); err != nil {
		return moved, fmt.Errorf("aistats: write migration marker: %w", err)
	}

	return moved, nil
}
