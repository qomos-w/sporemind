package puppeteditor

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

const (
	assetStateStaged    = "staged"
	assetStateCommitted = "committed"
	assetStateRejected  = "rejected"
	assetKindTexture    = "texture"
	assetKindReference  = "reference"
)

// handleAssetList returns copies of assets, optionally filtered by lifecycle
// state. An unknown filter is rejected to prevent clients treating arbitrary
// strings as valid states.
func (a *Actor) handleAssetList(_ actor.PureContext, req gen.PuppetAssetListReq) (gen.PuppetAssetListResp, error) {
	if req.State != "" && !isAssetState(req.State) {
		return gen.PuppetAssetListResp{}, fmt.Errorf("puppet.asset.list: invalid state %q", req.State)
	}

	a.mu.RLock()
	defer a.mu.RUnlock()
	assets := make([]gen.PuppetStagedAsset, 0, len(a.state.Assets))
	for _, asset := range a.state.Assets {
		if req.State == "" || asset.State == req.State {
			assets = append(assets, asset)
		}
	}
	return gen.PuppetAssetListResp{Assets: assets}, nil
}

// handleAssetStage records a newly available texture or reference asset in the
// staged state. Pixel data remains external and is referenced by DataRef.
func (a *Actor) handleAssetStage(_ actor.PureContext, req gen.PuppetAssetStageReq) (gen.PuppetAssetStageResp, error) {
	if !isAssetKind(req.Kind) {
		return gen.PuppetAssetStageResp{}, fmt.Errorf("puppet.asset.stage: invalid kind %q", req.Kind)
	}
	if req.DataRef == "" {
		return gen.PuppetAssetStageResp{}, fmt.Errorf("puppet.asset.stage: DataRef must not be empty")
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	asset := gen.PuppetStagedAsset{
		ID:               newID("asset"),
		DocumentID:       a.state.Document.ID,
		State:            assetStateStaged,
		Kind:             req.Kind,
		DataRef:          req.DataRef,
		Name:             req.Name,
		Width:            req.Width,
		Height:           req.Height,
		SourceRef:        req.SourceRef,
		GenParamsSummary: req.GenParamsSummary,
		CreatedAt:        time.Now().UTC().Format(time.RFC3339Nano),
	}
	a.state.Assets = append(a.state.Assets, asset)
	if err := a.saveLocked(); err != nil {
		return gen.PuppetAssetStageResp{}, fmt.Errorf("puppet.asset.stage: persist failed: %w", err)
	}
	return gen.PuppetAssetStageResp{Asset: asset}, nil
}

// handleAssetCommit transitions exactly one staged texture asset to committed,
// attaches it to the explicitly supplied target node, and records the document
// mutation as a revision. The lock serializes concurrent commits so only one
// request can transition a given asset.
func (a *Actor) handleAssetCommit(ctx actor.PureContext, req gen.PuppetAssetCommitReq) (gen.PuppetAssetCommitResp, error) {
	if req.AssetID == "" {
		return gen.PuppetAssetCommitResp{}, fmt.Errorf("puppet.asset.commit: AssetId must not be empty")
	}
	if req.NodeGuid == "" {
		return gen.PuppetAssetCommitResp{}, fmt.Errorf("puppet.asset.commit: NodeGuid must not be empty")
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	asset := a.findAssetLocked(req.AssetID)
	if asset == nil {
		return gen.PuppetAssetCommitResp{}, fmt.Errorf("puppet.asset.commit: asset %q not found", req.AssetID)
	}
	if asset.State != assetStateStaged {
		return gen.PuppetAssetCommitResp{}, fmt.Errorf("puppet.asset.commit: asset %q is %s, not staged", req.AssetID, asset.State)
	}
	if asset.Kind != assetKindTexture {
		return gen.PuppetAssetCommitResp{}, fmt.Errorf("puppet.asset.commit: asset %q kind %q cannot attach as a texture", req.AssetID, asset.Kind)
	}
	node := findNode(&a.state.Document.Root, req.NodeGuid)
	if node == nil {
		return gen.PuppetAssetCommitResp{}, fmt.Errorf("puppet.asset.commit: node %q not found", req.NodeGuid)
	}

	node.TextureAssetID = asset.ID
	asset.State = assetStateCommitted
	asset.NodeGuid = req.NodeGuid
	asset.ReviewedAt = time.Now().UTC().Format(time.RFC3339Nano)
	revision := a.appendRevisionLocked("commit_asset", authorFromContext(ctx), "", fmt.Sprintf("committed asset %s to node %s", asset.ID, req.NodeGuid))
	// Record the affected node guid on the revision so the frontend can
	// refresh only the touched subtree. appendRevisionLocked lives in
	// edit.go and does not yet accept affectedGuids, so we patch the last
	// revision in place under the lock we already hold.
	affected := []string{req.NodeGuid}
	revision.AffectedGuids = affected
	a.state.Revisions[len(a.state.Revisions)-1].AffectedGuids = affected
	if err := a.saveLocked(); err != nil {
		return gen.PuppetAssetCommitResp{}, fmt.Errorf("puppet.asset.commit: persist failed: %w", err)
	}
	return gen.PuppetAssetCommitResp{Asset: *asset, Revision: revision}, nil
}

// handleAssetReject transitions a staged asset to rejected and records audit
// metadata. It deliberately does not mutate the document node tree.
func (a *Actor) handleAssetReject(ctx actor.PureContext, req gen.PuppetAssetRejectReq) (gen.PuppetAssetRejectResp, error) {
	return a.handleAssetRejectWithAuditSource(ctx, req, "")
}

// handleAssetRejectWithAuditSource writes provenance only when creating the
// immutable rejection revision under the same lock as the asset transition.
func (a *Actor) handleAssetRejectWithAuditSource(ctx actor.PureContext, req gen.PuppetAssetRejectReq, auditSource string) (gen.PuppetAssetRejectResp, error) {
	if req.AssetID == "" {
		return gen.PuppetAssetRejectResp{}, fmt.Errorf("puppet.asset.reject: AssetId must not be empty")
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	asset := a.findAssetLocked(req.AssetID)
	if asset == nil {
		return gen.PuppetAssetRejectResp{}, fmt.Errorf("puppet.asset.reject: asset %q not found", req.AssetID)
	}
	if asset.State != assetStateStaged {
		return gen.PuppetAssetRejectResp{}, fmt.Errorf("puppet.asset.reject: asset %q is %s, not staged", req.AssetID, asset.State)
	}

	asset.State = assetStateRejected
	asset.ReviewedAt = time.Now().UTC().Format(time.RFC3339Nano)
	asset.ReviewReason = req.Reason
	a.appendRevisionLocked("reject_asset", authorFromContext(ctx), auditSource, fmt.Sprintf("rejected asset %s: %s", asset.ID, req.Reason))
	if err := a.saveLocked(); err != nil {
		return gen.PuppetAssetRejectResp{}, fmt.Errorf("puppet.asset.reject: persist failed: %w", err)
	}
	return gen.PuppetAssetRejectResp{Asset: *asset}, nil
}

// handleAssetRead resolves a committed asset's DataRef to a loadable data URL so
// the frontend renderer can fetch pixel data without a separate file-system
// path. The read path reuses the same controlled project-root resolution and
// sha256 verification used by agent staging: the actor locates the asset's
// SourceRef under one of the synced project roots, reads the file, and verifies
// its content hash matches the stored DataRef. Only committed assets are
// eligible; staged/rejected assets return an error. The actor never exposes a
// raw file-system path to the caller — the response carries only a base64 data
// URL and metadata.
func (a *Actor) handleAssetRead(_ actor.PureContext, req gen.PuppetAssetReadReq) (gen.PuppetAssetReadResp, error) {
	if req.AssetID == "" {
		return gen.PuppetAssetReadResp{}, fmt.Errorf("puppet.asset.read: AssetId must not be empty")
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	asset := a.findAssetLocked(req.AssetID)
	if asset == nil {
		return gen.PuppetAssetReadResp{}, fmt.Errorf("puppet.asset.read: asset %q not found", req.AssetID)
	}
	if asset.State != assetStateCommitted {
		return gen.PuppetAssetReadResp{}, fmt.Errorf("puppet.asset.read: asset %q is %s, not committed", req.AssetID, asset.State)
	}

	roots := a.projectRoots
	if len(roots) == 0 {
		return gen.PuppetAssetReadResp{}, fmt.Errorf("puppet.asset.read: no project roots available")
	}

	// Resolve the source file under the controlled project roots. This reuses
	// the same validation that agent staging applies: no absolute paths, no
	// ".." traversal, only files under the generated-assets directory.
	absPath, err := resolveGeneratedFilePath(roots, asset.SourceRef)
	if err != nil {
		return gen.PuppetAssetReadResp{}, fmt.Errorf("puppet.asset.read: %w", err)
	}

	// Read the file and verify its content hash matches the stored DataRef.
	// This prevents serving stale or tampered content: if the file on disk
	// has changed since staging, the sha256 will not match and the read is
	// rejected.
	data, err := os.ReadFile(absPath)
	if err != nil {
		return gen.PuppetAssetReadResp{}, fmt.Errorf("puppet.asset.read: read failed: %w", err)
	}
	actualRef, err := computeSHA256(absPath)
	if err != nil {
		return gen.PuppetAssetReadResp{}, fmt.Errorf("puppet.asset.read: hash failed: %w", err)
	}
	if actualRef != asset.DataRef {
		return gen.PuppetAssetReadResp{}, fmt.Errorf("puppet.asset.read: content hash mismatch (expected %s, got %s)", asset.DataRef, actualRef)
	}

	mimeType := guessMimeType(asset.SourceRef)
	dataURL := buildDataURL(mimeType, data)

	return gen.PuppetAssetReadResp{
		AssetID:  asset.ID,
		Uri:      dataURL,
		MimeType: mimeType,
		Width:    asset.Width,
		Height:   asset.Height,
	}, nil
}

// handleAgentAssetRead is the agent-surface bridge for puppet.asset.read. It
// enforces the same agent-role + audited-envelope gate as other agent
// callables, then delegates to handleAssetRead.
func (a *Actor) handleAgentAssetRead(ctx actor.PureContext, req gen.PuppetAgentAssetReadReq) (gen.PuppetAgentAssetReadResp, error) {
	if err := a.validateAgentRequest(ctx, req.Envelope); err != nil {
		return gen.PuppetAgentAssetReadResp{}, err
	}
	resp, err := a.handleAssetRead(ctx, gen.PuppetAssetReadReq{AssetID: req.AssetID})
	return gen.PuppetAgentAssetReadResp{Result: resp}, err
}

// guessMimeType infers a MIME type from the file extension in sourceRef. Falls
// back to application/octet-stream for unknown extensions.
func guessMimeType(sourceRef string) string {
	ext := strings.ToLower(filepath.Ext(sourceRef))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	default:
		return "application/octet-stream"
	}
}

// buildDataURL constructs a base64 data URL for the given MIME type and content.
func buildDataURL(mimeType string, data []byte) string {
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func (a *Actor) findAssetLocked(assetID string) *gen.PuppetStagedAsset {
	for i := range a.state.Assets {
		if a.state.Assets[i].ID == assetID {
			return &a.state.Assets[i]
		}
	}
	return nil
}

func isAssetState(state string) bool {
	return state == assetStateStaged || state == assetStateCommitted || state == assetStateRejected
}

func isAssetKind(kind string) bool {
	return kind == assetKindTexture || kind == assetKindReference
}
