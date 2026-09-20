package appmanager

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/qomos-w/sporemind/pkg/persist"
)

// Asset blobs are persisted OUTSIDE the single appmanager state document:
// the document keeps only name→content-hash refs (appRecord.AssetRefs /
// pendingReload.CandidateAssetRefs), the bytes go content-addressed to
// <persistBase>/<actorID>/assets/<appID>/<sha256>. Without this, every
// Save() re-serialized every app's full bundle as base64 inside the one
// state document (2026-09-09: a dev app that swept frontend/node_modules
// grew the document to 139MB; each state transition rewrote all of it and
// starved the owner lane into invoke timeouts).
//
// Backends without BasePather (non-filesystem persist) degrade to the
// legacy inline form — Assets stay in the document — per the persist
// optional-capability contract.

func assetContentHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// assetDirName maps an appID to its side-store directory name. Register-time
// validation keeps appIDs path-safe, but persisted state is untrusted input:
// anything that could escape the assets root (empty, dot-only, separators)
// is mapped to a hash-derived name instead.
func assetDirName(appID string) string {
	if appID == "" || appID == "." || appID == ".." || strings.ContainsAny(appID, `/\`) {
		sum := sha256.Sum256([]byte(appID))
		return "x-" + hex.EncodeToString(sum[:8])
	}
	return appID
}

// assetStoreBase returns <persistBase>/<actorID>/assets when the store
// backend exposes a base path; ok=false means the backend cannot host the
// side store and callers must keep assets inline.
func (a *Actor) assetStoreBase() (string, bool) {
	if a.store == nil || a.actorID == "" {
		return "", false
	}
	bp, ok := a.store.(persist.BasePather)
	if !ok {
		return "", false
	}
	return filepath.Join(bp.BasePath(), a.actorID, "assets"), true
}

// writeAssetBlobs durably stores every blob of one app and returns its
// name→hash refs. Content addressing makes repeated and concurrent saves
// idempotent: an existing file is never rewritten.
func (a *Actor) writeAssetBlobs(appID string, assets map[string][]byte) (map[string]string, error) {
	base, ok := a.assetStoreBase()
	if !ok {
		return nil, nil
	}
	dir := filepath.Join(base, assetDirName(appID))
	refs := make(map[string]string, len(assets))
	for name, content := range assets {
		h := assetContentHash(content)
		path := filepath.Join(dir, h)
		if _, err := os.Stat(path); err == nil {
			refs[name] = h
			continue
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("appmanager: asset store stat %s: %w", name, err)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("appmanager: asset store mkdir: %w", err)
		}
		if err := persist.WriteFileAtomic(path, content, 0o644); err != nil {
			return nil, fmt.Errorf("appmanager: asset store write %s: %w", name, err)
		}
		refs[name] = h
	}
	return refs, nil
}

// readAssetBlobs rehydrates one app's name→bytes map from its refs. A
// missing or hash-mismatched blob is skipped with a warning — the record
// stays usable and an explicit asset push surfaces the gap — never fatal,
// because one lost blob must not prevent the whole actor from starting.
func (a *Actor) readAssetBlobs(appID string, refs map[string]string, warn func(msg string, kv ...any)) map[string][]byte {
	base, ok := a.assetStoreBase()
	if !ok {
		return nil
	}
	dir := filepath.Join(base, assetDirName(appID))
	assets := make(map[string][]byte, len(refs))
	for name, h := range refs {
		content, err := os.ReadFile(filepath.Join(dir, h))
		if err != nil {
			if warn != nil {
				warn("appmanager: asset blob missing from side store", "app", appID, "asset", name, "error", err)
			}
			continue
		}
		if assetContentHash(content) != h {
			if warn != nil {
				warn("appmanager: asset blob corrupt in side store (hash mismatch)", "app", appID, "asset", name)
			}
			continue
		}
		assets[name] = content
	}
	return assets
}

// removeAppAssetDir reclaims one app's whole side-store directory on
// unregister. Best-effort, mirroring removeOwnedInventoryFile: a missing
// directory is ignored, a failure only wastes disk.
func (a *Actor) removeAppAssetDir(appID string) {
	base, ok := a.assetStoreBase()
	if !ok {
		return
	}
	_ = os.RemoveAll(filepath.Join(base, assetDirName(appID)))
}

// offloadAssetsSnapshot moves the snapshot's asset bytes to the side store,
// replacing them with refs so the persisted document carries only hashes.
// The snapshot records are private copies (saveState builds them under
// a.mu), so nil-ing the Assets field never touches the live records. A blob
// write failure aborts the save — callers already roll back on Save errors.
func (a *Actor) offloadAssetsSnapshot(records map[string]appRecord, pending map[string]pendingReload) error {
	if _, ok := a.assetStoreBase(); !ok {
		return nil
	}
	for appID, rec := range records {
		if len(rec.Assets) == 0 {
			continue
		}
		refs, err := a.writeAssetBlobs(appID, rec.Assets)
		if err != nil {
			return err
		}
		rec.AssetRefs = refs
		rec.Assets = nil
		records[appID] = rec
	}
	for appID, pr := range pending {
		if len(pr.CandidateAssets) == 0 {
			continue
		}
		refs, err := a.writeAssetBlobs(appID, pr.CandidateAssets)
		if err != nil {
			return err
		}
		pr.CandidateAssetRefs = refs
		pr.CandidateAssets = nil
		pending[appID] = pr
	}
	return nil
}

// rehydrateAssetRefs restores in-memory asset bytes from the persisted refs
// after a Load/OnInit. Records that still carry inline Assets (legacy state,
// or a non-BasePather backend) are left untouched; their next save migrates
// them to the side store.
func (a *Actor) rehydrateAssetRefs(warn func(msg string, kv ...any)) {
	for appID, record := range a.Records {
		if len(record.AssetRefs) == 0 || len(record.Assets) > 0 {
			continue
		}
		record.Assets = a.readAssetBlobs(appID, record.AssetRefs, warn)
		a.Records[appID] = record
	}
	for appID, pr := range a.PendingReloads {
		if len(pr.CandidateAssetRefs) == 0 || len(pr.CandidateAssets) > 0 {
			continue
		}
		pr.CandidateAssets = a.readAssetBlobs(appID, pr.CandidateAssetRefs, warn)
		a.PendingReloads[appID] = pr
	}
}
