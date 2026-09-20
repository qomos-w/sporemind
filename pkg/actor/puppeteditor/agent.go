package puppeteditor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

const (
	agentSnapshot       = "puppet.agent.explore.snapshot"
	agentRevisionLog    = "puppet.agent.read.revision_log"
	agentAssetList      = "puppet.agent.read.asset_list"
	agentEdit           = "puppet.agent.act.edit"
	agentAssetStage     = "puppet.agent.act.asset_stage"
	agentAssetReject    = "puppet.agent.act.asset_reject"
	agentExploreQuery   = "puppet.agent.explore.query"
	agentRevertTo       = "puppet.agent.act.revert_to"
	agentExploreCapture = "puppet.agent.explore.capture"
)

// validateAgentRequest is the single policy gate for the Agent surface. The
// callable visibility blocks anonymous transport callers; this check also
// protects direct handler invocation and requires auditable provenance.
func (a *Actor) validateAgentRequest(ctx actor.PureContext, req gen.PuppetAgentRequest) error {
	if ctx == nil || ctx.Identity().Subject == "" || ctx.Identity().Role != "agent" {
		return fmt.Errorf("puppet agent access denied: requires agent role")
	}
	if req.TargetGuid == "" || req.RequestID == "" || req.AuditSource == "" {
		return fmt.Errorf("puppet agent request requires TargetGuid, RequestId, and AuditSource")
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if req.TargetGuid != a.state.Document.ID {
		return fmt.Errorf("puppet agent target document %q not found", req.TargetGuid)
	}
	return nil
}

func (a *Actor) handleAgentSnapshot(ctx actor.PureContext, req gen.PuppetAgentSnapshotReq) (gen.PuppetAgentSnapshotResp, error) {
	if err := a.validateAgentRequest(ctx, req.Envelope); err != nil {
		return gen.PuppetAgentSnapshotResp{}, err
	}
	resp, err := a.handleSnapshot(ctx, gen.PuppetDocumentSnapshotReq{})
	return gen.PuppetAgentSnapshotResp{Snapshot: resp.Snapshot}, err
}

func (a *Actor) handleAgentRevisionLog(ctx actor.PureContext, req gen.PuppetAgentRevisionLogReq) (gen.PuppetAgentRevisionLogResp, error) {
	if err := a.validateAgentRequest(ctx, req.Envelope); err != nil {
		return gen.PuppetAgentRevisionLogResp{}, err
	}
	resp, err := a.handleRevisionLog(ctx, gen.PuppetRevisionLogReq{Limit: req.Limit})
	return gen.PuppetAgentRevisionLogResp{Revisions: resp.Revisions}, err
}

func (a *Actor) handleAgentAssetList(ctx actor.PureContext, req gen.PuppetAgentAssetListReq) (gen.PuppetAgentAssetListResp, error) {
	if err := a.validateAgentRequest(ctx, req.Envelope); err != nil {
		return gen.PuppetAgentAssetListResp{}, err
	}
	resp, err := a.handleAssetList(ctx, gen.PuppetAssetListReq{State: req.State})
	return gen.PuppetAgentAssetListResp{Assets: resp.Assets}, err
}

func (a *Actor) handleAgentEdit(ctx actor.PureContext, req gen.PuppetAgentEditReq) (gen.PuppetAgentEditResp, error) {
	if err := a.validateAgentRequest(ctx, req.Envelope); err != nil {
		return gen.PuppetAgentEditResp{}, err
	}
	if req.Command.TargetGuid == "" || req.Command.RequestID != req.Envelope.RequestID {
		return gen.PuppetAgentEditResp{}, fmt.Errorf("puppet agent edit command node target and RequestId must be present and RequestId must match envelope")
	}
	resp, err := a.handleEditWithAuditSource(ctx, gen.PuppetEditReq{Command: req.Command}, req.Envelope.AuditSource)
	return gen.PuppetAgentEditResp{Result: resp}, err
}

// generatedAssetDir is the project-relative directory that generate_image
// writes to (domain.GeneratedAssetsDir). The agent surface only accepts assets
// whose SourceRef lies inside it, so an agent cannot stage arbitrary project
// files or escape the project.
const generatedAssetDir = domain.GeneratedAssetsDir

// handleAgentAssetStage is the restricted bridge callable an image-gen
// Agent invokes after a successful generate_image. It enforces:
//   - agent role + audited envelope (validateAgentRequest)
//   - SourceRef is a project-relative path inside assets/generated/ (no
//     absolute paths, no ".." traversal, no arbitrary project files)
//   - GenParamsSummary is present (auditable generation provenance)
//
// Data integrity is established by the actor itself: after accepting the
// SourceRef the actor reads the generated file under a controlled project
// root and computes a content-addressed sha256 DataRef. The agent never
// supplies (and cannot forge) the hash. Media account credentials never
// enter the request or response. The agent has no asset_commit callable,
// so a staged asset cannot attach to the document without an explicit
// human review.
func (a *Actor) handleAgentAssetStage(ctx actor.PureContext, req gen.PuppetAgentAssetStageReq) (gen.PuppetAgentAssetStageResp, error) {
	if err := a.validateAgentRequest(ctx, req.Envelope); err != nil {
		return gen.PuppetAgentAssetStageResp{}, err
	}
	if strings.TrimSpace(req.Operation.GenParamsSummary) == "" {
		return gen.PuppetAgentAssetStageResp{}, fmt.Errorf("puppet agent asset stage requires GenParamsSummary for generation provenance")
	}
	a.mu.RLock()
	roots := a.projectRoots
	a.mu.RUnlock()
	absPath, err := resolveGeneratedFilePath(roots, req.Operation.SourceRef)
	if err != nil {
		return gen.PuppetAgentAssetStageResp{}, err
	}
	dataRef, err := computeSHA256(absPath)
	if err != nil {
		return gen.PuppetAgentAssetStageResp{}, fmt.Errorf("puppet agent asset stage: hash failed: %w", err)
	}
	req.Operation.DataRef = dataRef
	resp, err := a.handleAssetStage(ctx, req.Operation)
	return gen.PuppetAgentAssetStageResp{Result: resp}, err
}

// resolveGeneratedFilePath validates the SourceRef and locates the generated
// file under one of the controlled project roots. It returns the absolute path
// of the first existing match, or an error if the path is malformed or the file
// is absent from every root. It never reads file contents.
func resolveGeneratedFilePath(roots []string, sourceRef string) (string, error) {
	if err := validateGeneratedSourceRef(sourceRef); err != nil {
		return "", err
	}
	rel := filepath.ToSlash(filepath.Clean(sourceRef))
	for _, root := range roots {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if info, err := os.Stat(abs); err == nil && !info.IsDir() {
			return abs, nil
		}
	}
	return "", fmt.Errorf("puppet agent asset stage: generated file %q not found under any project root", sourceRef)
}

// computeSHA256 reads the file at path and returns its content-addressed
// DataRef in the canonical "sha256:<hex>" form.
func computeSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// decodeProjectRoots extracts the primary mount paths from a workspace
// list_project response encoded in any of the common invoke shapes.
func decodeProjectRoots(result any) []string {
	if result == nil {
		return nil
	}
	var resp struct {
		Items []struct {
			Path   string `json:"Path"`
			Mounts []struct {
				Path string `json:"Path"`
			} `json:"Mounts"`
		} `json:"Items"`
	}
	switch v := result.(type) {
	case []byte:
		_ = json.Unmarshal(v, &resp)
	case map[string]any:
		b, _ := json.Marshal(v)
		_ = json.Unmarshal(b, &resp)
	default:
		b, _ := json.Marshal(v)
		_ = json.Unmarshal(b, &resp)
	}
	var out []string
	for _, item := range resp.Items {
		if item.Path != "" {
			out = append(out, filepath.Clean(item.Path))
		}
		for _, m := range item.Mounts {
			if m.Path != "" {
				out = append(out, filepath.Clean(m.Path))
			}
		}
	}
	return out
}

// validateGeneratedSourceRef rejects any SourceRef that is not a clean
// project-relative path under the generated-assets directory. This prevents
// absolute paths, parent-directory traversal, and arbitrary project files
// from entering the staging stream via the agent surface.
func validateGeneratedSourceRef(sourceRef string) error {
	if sourceRef == "" {
		return fmt.Errorf("puppet agent asset stage requires SourceRef")
	}
	if filepath.IsAbs(sourceRef) {
		return fmt.Errorf("puppet agent asset stage SourceRef must be a project-relative path, not absolute")
	}
	if strings.Contains(sourceRef, "..") {
		return fmt.Errorf("puppet agent asset stage SourceRef must not contain parent-directory traversal")
	}
	cleaned := filepath.ToSlash(filepath.Clean(sourceRef))
	if !strings.HasPrefix(cleaned, generatedAssetDir) {
		return fmt.Errorf("puppet agent asset stage SourceRef must be under %s", generatedAssetDir)
	}
	return nil
}

func (a *Actor) handleAgentAssetReject(ctx actor.PureContext, req gen.PuppetAgentAssetRejectReq) (gen.PuppetAgentAssetRejectResp, error) {
	if err := a.validateAgentRequest(ctx, req.Envelope); err != nil {
		return gen.PuppetAgentAssetRejectResp{}, err
	}
	resp, err := a.handleAssetRejectWithAuditSource(ctx, gen.PuppetAssetRejectReq{AssetID: req.AssetID, Reason: req.Reason}, req.Envelope.AuditSource)
	return gen.PuppetAgentAssetRejectResp{Result: resp}, err
}

// handleAgentExploreQuery is the agent surface's parameterized node query —
// the minimal subset of nijigenerate's selector capability: instead of
// requiring a fully explicit TargetGuid, an agent filters by whitelisted
// fields (Kind, NamePattern, HasTexture, HasMesh) and receives flat
// PuppetNodeInfo projections. It is strictly read-only: it takes only a read
// lock, mutates nothing, and returns values that share no mutable references
// with actor state.
func (a *Actor) handleAgentExploreQuery(ctx actor.PureContext, req gen.PuppetAgentExploreQueryReq) (gen.PuppetAgentExploreQueryResp, error) {
	if err := a.validateAgentRequest(ctx, req.Envelope); err != nil {
		return gen.PuppetAgentExploreQueryResp{}, err
	}
	if req.Kind != "" && !nodeKindWhitelist[req.Kind] {
		return gen.PuppetAgentExploreQueryResp{}, fmt.Errorf("puppet agent explore: unknown Kind filter %q (whitelist: part, composite, mask, bone, group, node)", req.Kind)
	}
	pattern := strings.ToLower(req.NamePattern)

	a.mu.RLock()
	defer a.mu.RUnlock()

	nodes := make([]gen.PuppetNodeInfo, 0)
	walkNodeInfos(&a.state.Document.Root, "", 0, func(info gen.PuppetNodeInfo) {
		if req.Kind != "" && info.Kind != req.Kind {
			return
		}
		if pattern != "" && !strings.Contains(strings.ToLower(info.Name), pattern) {
			return
		}
		if req.HasTexture && info.TextureAssetID == "" {
			return
		}
		if req.HasMesh && !info.HasMesh {
			return
		}
		nodes = append(nodes, info)
	})
	return gen.PuppetAgentExploreQueryResp{Nodes: nodes}, nil
}

// handleAgentExploreCapture is the agent bridge for viewport capture. Same
// policy gate as every agent callable; the result carries the same honest
// Source=document_reprojection marker as the public callable.
func (a *Actor) handleAgentExploreCapture(ctx actor.PureContext, req gen.PuppetAgentExploreCaptureReq) (gen.PuppetAgentExploreCaptureResp, error) {
	if err := a.validateAgentRequest(ctx, req.Envelope); err != nil {
		return gen.PuppetAgentExploreCaptureResp{}, err
	}
	resp, err := a.handleViewportCapture(ctx, gen.PuppetViewportCaptureReq{
		Width:  req.Width,
		Height: req.Height,
		Format: req.Format,
	})
	return gen.PuppetAgentExploreCaptureResp{Result: resp}, err
}

// walkNodeInfos flattens the node tree into PuppetNodeInfo projections in
// deterministic pre-order, computing Depth and ParentGuid along the way. The
// visit callback receives value copies only.
func walkNodeInfos(n *gen.PuppetNode, parentGuid string, depth int32, visit func(gen.PuppetNodeInfo)) {
	if n == nil {
		return
	}
	visit(gen.PuppetNodeInfo{
		Guid:           n.Guid,
		Name:           n.Name,
		Kind:           n.Kind,
		Enabled:        n.Enabled,
		Z:              n.Z,
		TextureAssetID: n.TextureAssetID,
		HasMesh:        n.Mesh != nil,
		Depth:          depth,
		ParentGuid:     parentGuid,
	})
	for i := range n.Children {
		walkNodeInfos(&n.Children[i], n.Guid, depth+1, visit)
	}
}
