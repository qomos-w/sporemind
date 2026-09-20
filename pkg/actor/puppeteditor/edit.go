package puppeteditor

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/actor/puppeteditor/automesh"
)

// Whitelisted command kinds. The puppet.edit handler dispatches only on these
// values plus the param commands (param_create / param_update / param_remove /
// set_param_value / set_param_binding) defined alongside the param model in
// params.go; any other Kind is rejected with a structured failure (Applied=false)
// rather than an opaque error. This mirrors nijigenerate's commands/base.d
// Command model where each command is a concrete type, not an arbitrary string.
const (
	cmdSetNodeName      = "set_node_name"
	cmdSetNodeEnabled   = "set_node_enabled"
	cmdSetNodeZ         = "set_node_z"
	cmdSetNodeMesh      = "set_node_mesh"
	cmdGenerateNodeMesh = "generate_node_mesh"
)

// Mesh command param names (carried in PuppetEditCommand.Params).
const (
	meshParamMethod = "method" // generate_node_mesh: "" | "contour" (default)
	meshParamPreset = "preset" // generate_node_mesh sampling preset: "" | "detailed" | "thin"
	meshParamMesh   = "mesh"   // set_node_mesh: JSON-encoded PuppetMesh
)

// meshAlphaThreshold is the binarization threshold applied to the texture
// alpha before contour tracing: any pixel with alpha >= 1 is solid. This makes
// anti-aliased edges deterministic instead of leaving partial alpha values in
// the traced mask.
const meshAlphaThreshold = 1

// editResult is the idempotency cache entry stored per RequestId. It carries
// enough data to reconstruct the full PuppetEditResp on a replay without
// re-applying the command or appending a duplicate revision.
type editResult struct {
	RevisionID    string   `json:"revisionId"`
	Detail        string   `json:"detail,omitempty"`
	AffectedGuids []string `json:"affectedGuids,omitempty"`
}

// ---------------------------------------------------------------------------
// Command model (nijigenerate Command analogue)
// ---------------------------------------------------------------------------

// editCommand is the nijigenerate Command interface analogue. canExecute
// validates the command against the document context without mutating; execute
// applies the mutation and returns the touched node guids plus a human-readable
// detail string for the revision summary and the structured response.
type editCommand interface {
	kind() string
	canExecute(doc gen.PuppetDocument) error
	execute(doc *gen.PuppetDocument) (affectedGuids []string, detail string)
}

// --- set_node_name ---------------------------------------------------------

type setNodeNameCmd struct {
	targetGuid string
	name       string
}

func (c setNodeNameCmd) kind() string { return cmdSetNodeName }

func (c setNodeNameCmd) canExecute(doc gen.PuppetDocument) error {
	if findNode(&doc.Root, c.targetGuid) == nil {
		return fmt.Errorf("set_node_name: node %q not found", c.targetGuid)
	}
	return nil
}

func (c setNodeNameCmd) execute(doc *gen.PuppetDocument) ([]string, string) {
	node := findNode(&doc.Root, c.targetGuid)
	old := node.Name
	node.Name = c.name
	return []string{c.targetGuid},
		fmt.Sprintf("set_node_name %s: %q -> %q", c.targetGuid, old, c.name)
}

// --- set_node_enabled ------------------------------------------------------

type setNodeEnabledCmd struct {
	targetGuid string
	enabled    bool
}

func (c setNodeEnabledCmd) kind() string { return cmdSetNodeEnabled }

func (c setNodeEnabledCmd) canExecute(doc gen.PuppetDocument) error {
	if findNode(&doc.Root, c.targetGuid) == nil {
		return fmt.Errorf("set_node_enabled: node %q not found", c.targetGuid)
	}
	return nil
}

func (c setNodeEnabledCmd) execute(doc *gen.PuppetDocument) ([]string, string) {
	node := findNode(&doc.Root, c.targetGuid)
	old := node.Enabled
	node.Enabled = c.enabled
	return []string{c.targetGuid},
		fmt.Sprintf("set_node_enabled %s: %v -> %v", c.targetGuid, old, c.enabled)
}

// --- set_node_z ------------------------------------------------------------

type setNodeZCmd struct {
	targetGuid string
	z          int32
}

func (c setNodeZCmd) kind() string { return cmdSetNodeZ }

func (c setNodeZCmd) canExecute(doc gen.PuppetDocument) error {
	if findNode(&doc.Root, c.targetGuid) == nil {
		return fmt.Errorf("set_node_z: node %q not found", c.targetGuid)
	}
	return nil
}

func (c setNodeZCmd) execute(doc *gen.PuppetDocument) ([]string, string) {
	node := findNode(&doc.Root, c.targetGuid)
	old := node.Z
	node.Z = c.z
	return []string{c.targetGuid},
		fmt.Sprintf("set_node_z %s: %d -> %d", c.targetGuid, old, c.z)
}

// --- set_node_mesh -----------------------------------------------------------

// setNodeMeshCmd attaches explicit mesh geometry to a node. The mesh arrives
// as a JSON-encoded PuppetMesh in Params["mesh"]; an all-empty mesh is valid
// and clears the node's mesh (node.Mesh = nil).
type setNodeMeshCmd struct {
	targetGuid string
	mesh       gen.PuppetMesh // validated explicit geometry; zero = clear
	clear      bool           // all-empty mesh payload → node.Mesh = nil
}

func (c setNodeMeshCmd) kind() string { return cmdSetNodeMesh }

func (c setNodeMeshCmd) canExecute(doc gen.PuppetDocument) error {
	if findNode(&doc.Root, c.targetGuid) == nil {
		return fmt.Errorf("set_node_mesh: node %q not found", c.targetGuid)
	}
	return nil
}

func (c setNodeMeshCmd) execute(doc *gen.PuppetDocument) ([]string, string) {
	node := findNode(&doc.Root, c.targetGuid)
	if c.clear {
		node.Mesh = nil
		return []string{c.targetGuid}, fmt.Sprintf("set_node_mesh %s: cleared mesh", c.targetGuid)
	}
	// Copy into a fresh struct so the document never aliases the command's
	// parse-time slices.
	mesh := c.mesh
	node.Mesh = &mesh
	return []string{c.targetGuid},
		fmt.Sprintf("set_node_mesh %s: %d vertices, %d triangles",
			c.targetGuid, len(mesh.Vertices)/2, len(mesh.Indices)/3)
}

// --- generate_node_mesh ------------------------------------------------------

// generateNodeMeshCmd stores a mesh generated from the node's attached texture
// alpha via the automesh contour pipeline. The pipeline itself is pure and
// deterministic, so it runs during command construction (see
// Actor.buildEditCommand / generateNodeMeshLocked) while the actor state is
// consistent; canExecute then validates the document context and execute only
// mutates. A nil mesh never reaches execute — degenerate pipeline output is
// rejected as a construction error.
type generateNodeMeshCmd struct {
	targetGuid string
	method     string // Params["method"], "" or "contour"
	texW, texH int
	mesh       *gen.PuppetMesh
}

func (c generateNodeMeshCmd) kind() string { return cmdGenerateNodeMesh }

func (c generateNodeMeshCmd) canExecute(doc gen.PuppetDocument) error {
	if findNode(&doc.Root, c.targetGuid) == nil {
		return fmt.Errorf("generate_node_mesh: node %q not found", c.targetGuid)
	}
	return nil
}

func (c generateNodeMeshCmd) execute(doc *gen.PuppetDocument) ([]string, string) {
	node := findNode(&doc.Root, c.targetGuid)
	node.Mesh = c.mesh
	return []string{c.targetGuid},
		fmt.Sprintf("generate_node_mesh %s: %d vertices, %d triangles from %dx%d texture (method=%s)",
			c.targetGuid, len(c.mesh.Vertices)/2, len(c.mesh.Indices)/3, c.texW, c.texH, c.method)
}

// ---------------------------------------------------------------------------
// Command factory (whitelist gate + param validation)
// ---------------------------------------------------------------------------

// buildEditCommand parses a PuppetEditCommand envelope into a concrete
// editCommand. Unknown kinds and malformed params are rejected here — this is
// the whitelist gate. Context-dependent validation (node existence) is deferred
// to canExecute. The mesh commands need actor state: set_node_mesh only for
// pure parsing, generate_node_mesh because its geometry is computed from the
// node's committed texture (see generateNodeMeshLocked). The caller must hold
// a.mu so document, assets, and project roots are read consistently.
func (a *Actor) buildEditCommand(env gen.PuppetEditCommand) (editCommand, error) {
	if env.TargetGuid == "" {
		return nil, fmt.Errorf("puppet.edit: TargetGuid must not be empty")
	}
	if env.Params == nil {
		env.Params = map[string]string{}
	}
	switch env.Kind {
	case cmdSetNodeName:
		name := env.Params["name"]
		if name == "" {
			return nil, fmt.Errorf("set_node_name: missing or empty param \"name\"")
		}
		return setNodeNameCmd{targetGuid: env.TargetGuid, name: name}, nil

	case cmdSetNodeEnabled:
		raw, ok := env.Params["enabled"]
		if !ok {
			return nil, fmt.Errorf("set_node_enabled: missing param \"enabled\"")
		}
		enabled, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("set_node_enabled: invalid param \"enabled\"=%q: %w", raw, err)
		}
		return setNodeEnabledCmd{targetGuid: env.TargetGuid, enabled: enabled}, nil

	case cmdSetNodeZ:
		raw, ok := env.Params["z"]
		if !ok {
			return nil, fmt.Errorf("set_node_z: missing param \"z\"")
		}
		z, err := strconv.ParseInt(raw, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("set_node_z: invalid param \"z\"=%q: %w", raw, err)
		}
		return setNodeZCmd{targetGuid: env.TargetGuid, z: int32(z)}, nil

	case cmdSetNodeMesh:
		raw, ok := env.Params[meshParamMesh]
		if !ok || strings.TrimSpace(raw) == "" {
			return nil, fmt.Errorf("set_node_mesh: missing param \"mesh\"")
		}
		mesh, clear, err := parseMeshParam(raw)
		if err != nil {
			return nil, err
		}
		return setNodeMeshCmd{targetGuid: env.TargetGuid, mesh: mesh, clear: clear}, nil

	case cmdGenerateNodeMesh:
		method := env.Params[meshParamMethod]
		if method != "" && method != "contour" {
			return nil, fmt.Errorf("generate_node_mesh: unknown method %q (only \"contour\" is implemented)", method)
		}
		preset, err := meshPresetFor(env.Params[meshParamPreset])
		if err != nil {
			return nil, err
		}
		mesh, w, h, err := a.generateNodeMeshLocked(env.TargetGuid, preset)
		if err != nil {
			return nil, err
		}
		return generateNodeMeshCmd{targetGuid: env.TargetGuid, method: method, texW: w, texH: h, mesh: mesh}, nil

	case cmdParamCreate, cmdParamUpdate, cmdParamRemove, cmdSetParamValue, cmdSetParamBinding:
		// Param commands need no actor state (unlike generate_node_mesh), so
		// the param whitelist gate lives in params.go next to the model.
		return buildParamEditCommand(env)

	default:
		return nil, fmt.Errorf("puppet.edit: unknown command kind %q", env.Kind)
	}
}

// parseMeshParam decodes and validates the JSON-encoded PuppetMesh carried in
// Params["mesh"]. Structural rules follow the PuppetMesh schema contract: flat
// (x,y) vertex pairs, triangle index triples referencing valid vertex
// positions, and UVs of the same length as Vertices. An all-empty mesh is
// valid and reports clear=true (the command clears node.Mesh).
func parseMeshParam(raw string) (mesh gen.PuppetMesh, clear bool, err error) {
	var m gen.PuppetMesh
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return gen.PuppetMesh{}, false, fmt.Errorf("set_node_mesh: param \"mesh\" is not valid PuppetMesh JSON: %w", err)
	}
	if len(m.Vertices)%2 != 0 {
		return gen.PuppetMesh{}, false, fmt.Errorf("set_node_mesh: Vertices length %d is not an even (x,y) pair list", len(m.Vertices))
	}
	if len(m.UVs)%2 != 0 {
		return gen.PuppetMesh{}, false, fmt.Errorf("set_node_mesh: UVs length %d is not an even (u,v) pair list", len(m.UVs))
	}
	points := len(m.Vertices) / 2
	if points == 0 {
		if len(m.UVs) != 0 || len(m.Indices) != 0 {
			return gen.PuppetMesh{}, false, fmt.Errorf("set_node_mesh: mesh has UVs/Indices but no Vertices")
		}
		return gen.PuppetMesh{}, true, nil
	}
	if points < 3 {
		return gen.PuppetMesh{}, false, fmt.Errorf("set_node_mesh: mesh needs at least 3 vertices, got %d", points)
	}
	if len(m.UVs) != 0 && len(m.UVs) != len(m.Vertices) {
		return gen.PuppetMesh{}, false, fmt.Errorf("set_node_mesh: UVs length %d does not match Vertices length %d", len(m.UVs), len(m.Vertices))
	}
	if len(m.Indices)%3 != 0 {
		return gen.PuppetMesh{}, false, fmt.Errorf("set_node_mesh: Indices length %d is not a triangle triple list", len(m.Indices))
	}
	for _, idx := range m.Indices {
		if idx < 0 || int(idx) >= points {
			return gen.PuppetMesh{}, false, fmt.Errorf("set_node_mesh: index %d out of range (0..%d)", idx, points-1)
		}
	}
	return m, false, nil
}

// meshPresetFor maps the generate_node_mesh "preset" param onto an automesh
// sampling configuration. The empty string selects the nijigenerate "Normal
// parts" default; "detailed" and "thin" select the other shipped presets.
func meshPresetFor(preset string) (automesh.ContourParams, error) {
	switch preset {
	case "":
		return automesh.DefaultContourParams(), nil
	case "detailed":
		return automesh.DetailedContourParams(), nil
	case "thin":
		return automesh.ThinContourParams(), nil
	default:
		return automesh.ContourParams{}, fmt.Errorf("generate_node_mesh: unknown preset %q (want default, detailed, or thin)", preset)
	}
}

// generateNodeMeshLocked resolves the node's committed texture, decodes its
// alpha channel, and runs the automesh contour pipeline, returning the mesh in
// schema form plus the texture dimensions. It mirrors the controlled read path
// of handleAssetRead: the SourceRef must resolve under a synced project root
// and the file's sha256 must match the stored DataRef, so generation never
// consumes tampered or stale pixels. Degenerate input — a texture whose alpha
// yields no usable contours or fewer than one triangle — is rejected with an
// error rather than stored as an empty mesh. The caller must hold a.mu.
func (a *Actor) generateNodeMeshLocked(targetGuid string, preset automesh.ContourParams) (*gen.PuppetMesh, int, int, error) {
	node := findNode(&a.state.Document.Root, targetGuid)
	if node == nil {
		return nil, 0, 0, fmt.Errorf("generate_node_mesh: node %q not found", targetGuid)
	}
	if node.TextureAssetID == "" {
		return nil, 0, 0, fmt.Errorf("generate_node_mesh: node %q has no attached texture", targetGuid)
	}
	asset := a.findAssetLocked(node.TextureAssetID)
	if asset == nil {
		return nil, 0, 0, fmt.Errorf("generate_node_mesh: texture asset %q not found", node.TextureAssetID)
	}
	if asset.State != assetStateCommitted {
		return nil, 0, 0, fmt.Errorf("generate_node_mesh: texture asset %q is %s, not committed", node.TextureAssetID, asset.State)
	}
	roots := a.projectRoots
	if len(roots) == 0 {
		return nil, 0, 0, fmt.Errorf("generate_node_mesh: no project roots available")
	}
	absPath, err := resolveGeneratedFilePath(roots, asset.SourceRef)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("generate_node_mesh: %w", err)
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("generate_node_mesh: read texture failed: %w", err)
	}
	actualRef, err := computeSHA256(absPath)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("generate_node_mesh: hash failed: %w", err)
	}
	if actualRef != asset.DataRef {
		return nil, 0, 0, fmt.Errorf("generate_node_mesh: texture content hash mismatch (expected %s, got %s)", asset.DataRef, actualRef)
	}
	alpha, err := automesh.DecodeAlphaPNGStrict(data)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("generate_node_mesh: %w", err)
	}
	res := automesh.Run(automesh.PipelineParams{
		Alpha:          alpha,
		AlphaThreshold: meshAlphaThreshold,
		Retrieval:      automesh.RetrievalExternal,
		Approx:         automesh.ApproxSimple,
		Sample:         preset,
		Triangulate:    true,
		Target:         automesh.TextureTarget([2]float64{0, 0}),
	})
	if len(res.Mesh.Vertices) < 3 || len(res.Mesh.Tris) == 0 {
		return nil, 0, 0, fmt.Errorf("generate_node_mesh: automesh degenerate input rejected: %d vertices, %d triangles from %dx%d texture",
			len(res.Mesh.Vertices), len(res.Mesh.Tris), alpha.W, alpha.H)
	}

	mesh := &gen.PuppetMesh{
		Vertices: make([]float32, 0, len(res.Mesh.Vertices)*2),
		Indices:  make([]int32, 0, len(res.Mesh.Tris)*3),
		UVs:      make([]float32, 0, len(res.Mesh.Vertices)*2),
	}
	for _, v := range res.Mesh.Vertices {
		mesh.Vertices = append(mesh.Vertices, float32(v.X), float32(v.Y))
		mesh.UVs = append(mesh.UVs,
			float32(clamp01(v.X/float64(alpha.W))),
			float32(clamp01(v.Y/float64(alpha.H))),
		)
	}
	for _, t := range res.Mesh.Tris {
		mesh.Indices = append(mesh.Indices, int32(t[0]), int32(t[1]), int32(t[2]))
	}
	return mesh, alpha.W, alpha.H, nil
}

// clamp01 constrains v to the PuppetMesh UV range documented in the schema.
// Scaled contour layers can place vertices slightly outside the texture, so
// the UV derivation clamps instead of emitting out-of-range coordinates.
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// ---------------------------------------------------------------------------
// Node tree lookup
// ---------------------------------------------------------------------------

// findNode returns a pointer to the node with the given guid in the subtree
// rooted at n, or nil if not found.
func findNode(n *gen.PuppetNode, guid string) *gen.PuppetNode {
	if n == nil {
		return nil
	}
	if n.Guid == guid {
		return n
	}
	for i := range n.Children {
		if found := findNode(&n.Children[i], guid); found != nil {
			return found
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// puppet.edit handler
// ---------------------------------------------------------------------------

// handleEdit is the puppet.edit callable. It accepts a PuppetEditCommand
// envelope, dispatches to a whitelisted concrete command, and returns a
// structured PuppetEditResp for both success and failure. The author is always
// derived from the authenticated caller context, ignoring any client-supplied
// Author field. A non-empty RequestId provides idempotency: a replay returns
// the original revision without appending a duplicate.
func (a *Actor) handleEdit(ctx actor.PureContext, req gen.PuppetEditReq) (gen.PuppetEditResp, error) {
	return a.handleEditWithAuditSource(ctx, req, "")
}

// handleEditWithAuditSource appends provenance as part of the immutable revision
// creation while holding the document lock. Public edits intentionally pass an
// empty source; the restricted Agent surface supplies its audited provenance.
func (a *Actor) handleEditWithAuditSource(ctx actor.PureContext, req gen.PuppetEditReq, auditSource string) (gen.PuppetEditResp, error) {
	author := authorFromContext(ctx)
	env := req.Command

	a.mu.Lock()
	defer a.mu.Unlock()

	// Idempotency: a seen RequestId returns the cached result without
	// re-applying, re-resolving texture pixel data, or appending a revision.
	if env.RequestID != "" {
		if pr, ok := a.state.ProcessedRequests[env.RequestID]; ok {
			return a.idempotentRespLocked(pr), nil
		}
	}

	// Whitelist gate + param validation.
	cmd, err := a.buildEditCommand(env)
	if err != nil {
		return gen.PuppetEditResp{Applied: false, Detail: err.Error()}, nil
	}

	// Context validation (node existence etc.).
	if err := cmd.canExecute(a.state.Document); err != nil {
		return gen.PuppetEditResp{Applied: false, Detail: err.Error()}, nil
	}

	// Apply the mutation.
	affected, detail := cmd.execute(&a.state.Document)

	// Append an immutable revision and bump the document counter. The
	// revision itself records the touched guids so the persisted audit log is
	// self-describing — the same contract commit_asset and revert_to follow.
	rev := a.appendRevisionLocked(cmd.kind(), author, auditSource, detail)
	rev.AffectedGuids = affected
	a.state.Revisions[len(a.state.Revisions)-1].AffectedGuids = affected

	// Record the RequestId for future idempotent replays.
	if env.RequestID != "" {
		if a.state.ProcessedRequests == nil {
			a.state.ProcessedRequests = make(map[string]editResult)
		}
		a.state.ProcessedRequests[env.RequestID] = editResult{
			RevisionID:    rev.ID,
			Detail:        detail,
			AffectedGuids: affected,
		}
	}

	if err := a.saveLocked(); err != nil {
		return gen.PuppetEditResp{}, fmt.Errorf("puppet.edit: persist failed: %w", err)
	}

	return gen.PuppetEditResp{
		Applied:       true,
		Revision:      rev,
		Detail:        detail,
		AffectedGuids: affected,
	}, nil
}

// idempotentRespLocked reconstructs the structured response for a replayed
// RequestId. It looks up the original revision by ID and returns it with
// Applied=true. The caller must hold a.mu.
func (a *Actor) idempotentRespLocked(pr editResult) gen.PuppetEditResp {
	var rev gen.PuppetRevision
	for _, r := range a.state.Revisions {
		if r.ID == pr.RevisionID {
			rev = r
			break
		}
	}
	return gen.PuppetEditResp{
		Applied:       true,
		Revision:      rev,
		Detail:        pr.Detail,
		AffectedGuids: pr.AffectedGuids,
	}
}

// appendRevisionLocked creates a new immutable PuppetRevision, links it to the
// previous head via ParentRevisionId, increments the document revision counter,
// and appends it to the history. The sequence starts at 1 for the first
// applied edit (the bootstrap "init" revision occupies sequence 0). It also
// records a deep-copy snapshot of the post-state document under the new
// revision ID — the rebuild source for puppet.document.revert_to. The caller
// must hold a.mu and must have finished every document mutation for this
// revision before calling (all current mutation paths do).
func (a *Actor) appendRevisionLocked(commandKind, author, auditSource, summary string) gen.PuppetRevision {
	now := time.Now().UTC().Format(time.RFC3339Nano)

	a.state.Document.Revision++
	a.state.Document.UpdatedAt = now

	var parentID string
	if n := len(a.state.Revisions); n > 0 {
		parentID = a.state.Revisions[n-1].ID
	}

	rev := gen.PuppetRevision{
		ID:               newID("rev"),
		DocumentID:       a.state.Document.ID,
		Sequence:         a.state.Document.Revision,
		ParentRevisionID: parentID,
		Author:           author,
		CommandKind:      commandKind,
		AuditSource:      auditSource,
		Summary:          summary,
		Timestamp:        now,
	}
	a.state.Revisions = append(a.state.Revisions, rev)
	if a.state.Snapshots == nil {
		a.state.Snapshots = make(map[string]gen.PuppetDocument)
	}
	a.state.Snapshots[rev.ID] = cloneDocument(a.state.Document)
	return rev
}
