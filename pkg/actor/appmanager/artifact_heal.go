package appmanager

// Restart self-heal for externally-wiped native artifacts (重启自愈):
// when the host restarts and a native app's recorded ArtifactPath file is
// gone (e.g. the inventory artifacts/ directory was deleted out-of-band)
// while its PackagePath inventory zip still exists, the artifact bytes are
// re-extracted from the zip (the same T1 parse/extract path install_local
// uses) and rewritten to the recorded content-addressed path — before the
// pluginhost restore or the appmanager spawn-loop verification consumes it.
//
// The pass runs in OnStart AFTER recoverPendingReloads/Cleanup and BEFORE
// the spawn loop. That ordering makes both start orders converge on running:
//
//   - Production order (appmanager starts before pluginhost): the heal
//     completes during appmanager OnStart, so pluginhost's own OnStart
//     restore (pluginhost.go, ArtifactLoads) finds the artifact present and
//     loads it — the record never enters the failure path.
//   - pluginhost-first order (e2e tests): pluginhost restore fails first and
//     registers an error descriptor; the heal then restores the file and the
//     appmanager spawn loop's idempotent artifact_load succeeds, pulling the
//     error-marked record back to running.
//
// Files that cannot be healed (package zip missing, unparseable zip, or an
// artifact hash that does not reproduce the recorded hash) are left for the
// normal restore/failure path — the record is kept and marked failed there —
// with a log that distinguishes "zip missing, no self-heal possible" from
// other causes. Healing is pure file restoration: it never touches app
// records, pluginhost state, or appstate.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/qomos-w/gospore/actor"

	"github.com/qomos-w/sporemind/pkg/persist"
)

// healMissingArtifacts restores every native app artifact that vanished
// while the host was down but whose package zip is still in the inventory.
// Candidates are native records that reference an artifact path and a
// package path and are not mid-cleanup; a record whose artifact file still
// exists needs no heal regardless of its state.
func (a *Actor) healMissingArtifacts(ctx actor.Context) {
	type candidate struct {
		id     string
		record appRecord
	}
	var candidates []candidate
	a.withMu(func() {
		for id, rec := range a.Records {
			if rec.Manifest.Runtime != "native" || rec.ArtifactPath == "" || rec.PackagePath == "" || rec.Abi == nil {
				continue
			}
			if isCleanupPendingState(rec.State) {
				continue
			}
			if _, err := os.Stat(rec.ArtifactPath); err == nil {
				continue // artifact present — nothing to heal
			} else if !os.IsNotExist(err) {
				continue // stat failed for a non-missing reason; leave to the load path
			}
			candidates = append(candidates, candidate{id: id, record: rec})
		}
	})

	for _, c := range candidates {
		if err := a.healArtifactFromPackage(c.record); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				ctx.Logger().Warn("appmanager: artifact self-heal skipped — package zip missing (no self-heal possible)",
					"app", c.id, "artifact", c.record.ArtifactPath, "package", c.record.PackagePath, "error", err)
			} else {
				ctx.Logger().Error("appmanager: artifact self-heal failed",
					"app", c.id, "artifact", c.record.ArtifactPath, "package", c.record.PackagePath, "error", err)
			}
			continue
		}
		ctx.Logger().Info("appmanager: restored missing artifact from package zip",
			"app", c.id, "artifact", c.record.ArtifactPath)
	}
}

// healArtifactFromPackage re-extracts a native app's artifact bytes from its
// inventory package zip and rewrites them to the recorded ArtifactPath,
// recreating the artifacts directory when the whole store was wiped. The
// extracted bytes must reproduce the recorded ArtifactHash; a mismatch means
// the inventory zip does not belong to this record and the heal is refused
// (the normal restore path then marks the app failed with a diagnostic).
func (a *Actor) healArtifactFromPackage(record appRecord) error {
	data, err := os.ReadFile(record.PackagePath)
	if err != nil {
		// The zip is gone together with the artifact (e.g. the whole
		// appmanager inventory was wiped): self-heal is impossible. The
		// error text says so explicitly for the failure log.
		return fmt.Errorf("package zip missing (no self-heal possible): %w", err)
	}
	pkg, err := a.parseLocalZipPackage(data)
	if err != nil {
		return fmt.Errorf("parse package zip: %w", err)
	}
	if len(pkg.Artifact) == 0 {
		return fmt.Errorf("package zip contains no native artifact binary")
	}
	if record.ArtifactHash != "" {
		if got := sha256Hex(pkg.Artifact); !strings.EqualFold(got, record.ArtifactHash) {
			return fmt.Errorf("package artifact hash %s does not reproduce recorded artifact hash %s; refusing self-heal", got, record.ArtifactHash)
		}
	}
	if err := os.MkdirAll(filepath.Dir(record.ArtifactPath), 0o755); err != nil {
		return fmt.Errorf("recreate artifact directory: %w", err)
	}
	if err := persist.WriteFileAtomic(record.ArtifactPath, pkg.Artifact, 0o644); err != nil {
		return fmt.Errorf("write restored artifact: %w", err)
	}
	return nil
}
