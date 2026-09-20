// Package puppeteditor is the Inochi2D Puppet Editor backend actor. It owns a
// single actor-bound PuppetDocument (node tree + revision counter), the
// immutable revision history, and the staged-asset state container — all
// persisted via pkg/persist.
//
// It exposes:
//
//   - puppet.document.snapshot   (Public, read-only)
//   - puppet.revision.log        (Public, read-only)
//   - puppet.edit                (Public, whitelist-dispatched command that
//     mutates the document and appends a revision)
//   - puppet.document.revert_to  (Public, undo/redo: rebuild the document from
//     a revision snapshot as a new head revision; history is never deleted)
//   - puppet.viewport.capture    (Public, read-only: honest document-level
//     reprojection while the canvas lives in the frontend)
//   - puppet.asset.stage/list/commit/reject (Public staging lifecycle)
//
// plus an Internal agent surface (agent role + audited envelope required) for
// snapshot/revision/asset reads, explore queries, viewport capture, edits,
// asset staging/rejection, and revert.
//
// The author provenance for any revision is always derived from the
// authenticated caller context (ctx.Identity), never from a client-supplied
// field — see authorFromContext.
//
// All wire types are defined in schemas/puppet._2962.spore and generated into
// pkg/domain/gen/puppet.gen.go.
package puppeteditor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// Actor is the Puppet Editor backend actor. It embeds actor.Host and owns all
// editor durable state under a single persist.Persist key.
type Actor struct {
	actor.Host

	store   persist.Persist
	actorID string

	mu    sync.RWMutex
	state puppetState
	// projectRoots are the resolved project mount paths used to locate
	// generated files for content-addressed staging. Non-persistent: synced
	// from the workspace service on start. Guarded by mu.
	projectRoots []string
}

// Compile-time assertion that Actor implements persist.Persistent, required by
// the actorset's RequirePersistent flag so the runtime preserves the actor's
// canonical ID across restarts.
var _ persist.Persistent = (*Actor)(nil)

// puppetState is the single persisted document. It bundles the document core
// (with its node tree and monotonic revision counter), the full revision
// history chain, the staged-asset state container, and the per-revision
// document snapshots that back undo/redo (revert_to). All four are restored
// atomically on restart via one persist.LoadOrZero call.
type puppetState struct {
	Document  gen.PuppetDocument      `json:"document"`
	Revisions []gen.PuppetRevision    `json:"revisions"`
	Assets    []gen.PuppetStagedAsset `json:"assets"`
	// Snapshots maps a revision ID to a deep copy of the document as it was
	// immediately after that revision was appended. It is the rebuild source
	// for puppet.document.revert_to: restoring a snapshot is deterministic
	// where a command replay would depend on external pixel data. Revisions
	// that predate snapshot recording (legacy persisted state) have no entry
	// and revert_to rejects them. Guarded by mu; every writer clones before
	// storing so the live document never aliases a stored snapshot.
	Snapshots map[string]gen.PuppetDocument `json:"snapshots,omitempty"`
	// ProcessedRequests maps a client RequestId to its edit result for
	// idempotent dedup. Only non-empty RequestIds are tracked. A replay
	// returns the original revision without appending a duplicate. Agent
	// revert_to replays are cached under the "revert:" prefix of the same map.
	ProcessedRequests map[string]editResult `json:"processedRequests,omitempty"`
}

// Type identifies this actor in the gospore runtime.
func (a *Actor) Type() string { return "puppeteditor" }

// OnInit wires the persist store, restores durable state, and bootstraps a
// default document on first start (no prior state).
func (a *Actor) OnInit(ctx actor.Context) error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("puppeteditor"))
		if err != nil {
			return err
		}
	}
	a.actorID = ctx.Self().ID().String()

	if err := a.Load(); err != nil {
		ctx.Logger().Error("puppeteditor: load state failed", "error", err)
	}

	// First start: no persisted document exists. Initialise a default empty
	// document with a root group node and a single "init" revision so the
	// revision chain always has a root record (parent empty, sequence 0).
	// The init revision also gets a snapshot so the bootstrap state itself is
	// revertible.
	a.mu.Lock()
	if a.state.Document.ID == "" {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		docID := newID("doc")
		rootGUID := newID("node")
		initRev := gen.PuppetRevision{
			ID:               newID("rev"),
			DocumentID:       docID,
			Sequence:         0,
			ParentRevisionID: "",
			Author:           authorSystem,
			CommandKind:      "init",
			Timestamp:        now,
		}
		a.state = puppetState{
			Document: gen.PuppetDocument{
				ID:       docID,
				Revision: 0,
				Name:     "Untitled",
				Root: gen.PuppetNode{
					Guid:     rootGUID,
					Name:     "Root",
					Kind:     "group",
					Enabled:  true,
					Children: []gen.PuppetNode{},
				},
				CreatedAt: now,
				UpdatedAt: now,
			},
			Revisions: []gen.PuppetRevision{initRev},
			Assets:    []gen.PuppetStagedAsset{},
			Snapshots: map[string]gen.PuppetDocument{},
		}
		a.state.Snapshots[initRev.ID] = cloneDocument(a.state.Document)
		// Persist the freshly initialised state so a restart recovers it.
		if err := a.saveLocked(); err != nil {
			ctx.Logger().Error("puppeteditor: persist initial state failed", "error", err)
		}
	}
	a.mu.Unlock()
	return nil
}

// OnStart registers Puppet document, revision, edit, and staged-asset callables.
func (a *Actor) OnStart(ctx actor.Context) error {
	a.syncProjectRoots(ctx)
	if err := ctx.Register("puppet.document.snapshot", a.handleSnapshot,
		actor.Public(),
		actor.WithDescription("Read-only projection of the puppet document: the full node tree plus derived staging/revision counts"),
	); err != nil {
		return fmt.Errorf("puppeteditor: register document.snapshot: %w", err)
	}
	if err := ctx.Register("puppet.revision.log", a.handleRevisionLog,
		actor.Public(),
		actor.WithDescription("Immutable revision history (optionally limited to the most recent N)"),
	); err != nil {
		return fmt.Errorf("puppeteditor: register revision.log: %w", err)
	}
	if err := ctx.Register("puppet.edit", a.handleEdit,
		actor.Public(),
		actor.WithDescription("Apply a whitelisted edit command (set_node_name, set_node_enabled, set_node_z, set_node_mesh, generate_node_mesh, param_create, param_update, param_remove, set_param_value, set_param_binding) to the puppet document, appending an immutable revision"),
	); err != nil {
		return fmt.Errorf("puppeteditor: register edit: %w", err)
	}
	if err := ctx.Register("puppet.param.list", a.handleParamList,
		actor.Public(),
		actor.WithDescription("Read-only list of the document's animatable params and their node-property bindings"),
	); err != nil {
		return fmt.Errorf("puppeteditor: register param.list: %w", err)
	}
	if err := ctx.Register("puppet.asset.list", a.handleAssetList,
		actor.Public(),
		actor.WithDescription("List staged, committed, or rejected puppet assets"),
	); err != nil {
		return fmt.Errorf("puppeteditor: register asset.list: %w", err)
	}
	if err := ctx.Register("puppet.asset.stage", a.handleAssetStage,
		actor.Public(),
		actor.WithDescription("Stage a texture or reference asset for review"),
	); err != nil {
		return fmt.Errorf("puppeteditor: register asset.stage: %w", err)
	}
	if err := ctx.Register("puppet.asset.commit", a.handleAssetCommit,
		actor.Public(),
		actor.WithDescription("Commit a staged texture asset to an explicit target node"),
	); err != nil {
		return fmt.Errorf("puppeteditor: register asset.commit: %w", err)
	}
	if err := ctx.Register("puppet.asset.reject", a.handleAssetReject,
		actor.Public(),
		actor.WithDescription("Reject a staged asset without changing the document"),
	); err != nil {
		return fmt.Errorf("puppeteditor: register asset.reject: %w", err)
	}
	if err := ctx.Register("puppet.asset.read", a.handleAssetRead,
		actor.Public(),
		actor.WithDescription("Resolve a committed asset's DataRef to a loadable data URL (sha256-verified, no raw paths exposed)"),
	); err != nil {
		return fmt.Errorf("puppeteditor: register asset.read: %w", err)
	}
	if err := ctx.Register("puppet.document.revert_to", a.handleRevertTo,
		actor.Public(),
		actor.WithDescription("Rebuild the document to a historical revision as a new head revision (undo/redo); revision history is immutable and never deleted"),
	); err != nil {
		return fmt.Errorf("puppeteditor: register document.revert_to: %w", err)
	}
	if err := ctx.Register("puppet.viewport.capture", a.handleViewportCapture,
		actor.Public(),
		actor.WithDescription("Canvas snapshot; while the canvas lives in the frontend this returns an honest document-level reprojection (svg), never a fabricated raster image"),
	); err != nil {
		return fmt.Errorf("puppeteditor: register viewport.capture: %w", err)
	}
	// Agent surface is deliberately internal: only an authorized Agent tool
	// binding can route these schema-bound operations; anonymous/front-end callers
	// cannot discover or invoke them.
	if err := ctx.Register(agentSnapshot, a.handleAgentSnapshot, actor.Internal(), actor.WithDescription("Agent Explore: document snapshot; requires agent role and audited target envelope")); err != nil { return err }
	if err := ctx.Register(agentRevisionLog, a.handleAgentRevisionLog, actor.Internal(), actor.WithDescription("Agent Read: revision log; requires agent role and audited target envelope")); err != nil { return err }
	if err := ctx.Register(agentAssetList, a.handleAgentAssetList, actor.Internal(), actor.WithDescription("Agent Read: staged asset list; requires agent role and audited target envelope")); err != nil { return err }
	if err := ctx.Register(agentEdit, a.handleAgentEdit, actor.Internal(), actor.WithDescription("Agent Act: whitelisted draft edit only; requires agent role and audited target envelope")); err != nil { return err }
	if err := ctx.Register(agentAssetStage, a.handleAgentAssetStage, actor.Internal(), actor.WithDescription("Agent Act: stage asset for review; does not commit")); err != nil { return err }
	if err := ctx.Register(agentAssetReject, a.handleAgentAssetReject, actor.Internal(), actor.WithDescription("Agent Act: reject staged asset; requires agent role and audited target envelope")); err != nil { return err }
	if err := ctx.Register("puppet.agent.read.asset_read", a.handleAgentAssetRead, actor.Internal(), actor.WithDescription("Agent Read: resolve committed asset to data URL; requires agent role and audited target envelope")); err != nil { return err }
	if err := ctx.Register(agentExploreQuery, a.handleAgentExploreQuery, actor.Internal(), actor.WithDescription("Agent Explore: parameterized node query with whitelisted filters (kind, name pattern, texture, mesh); read-only projection")); err != nil { return err }
	if err := ctx.Register(agentRevertTo, a.handleAgentRevertTo, actor.Internal(), actor.WithDescription("Agent Act: revert document to a historical revision; requires agent role and audited target envelope")); err != nil { return err }
	if err := ctx.Register(agentExploreCapture, a.handleAgentExploreCapture, actor.Internal(), actor.WithDescription("Agent Explore: viewport capture as document reprojection; requires agent role and audited target envelope")); err != nil { return err }
	if err := ctx.RegisterDomain("puppet").Expose(); err != nil {
		return fmt.Errorf("puppeteditor: expose service: %w", err)
	}
	return nil
}

// OnStop persists state on shutdown.
func (a *Actor) OnStop(_ actor.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.saveLocked()
}

// syncProjectRoots queries the workspace service for the current project mount
// paths so the agent asset-stage bridge can locate generated files under a
// controlled root. Best-effort: if the workspace is unavailable the roots
// remain empty and agent staging returns a clear error.
func (a *Actor) syncProjectRoots(ctx actor.Context) {
	wsRef, ok := ctx.LookupService("workspace")
	if !ok {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	call := wsRef.Invoke(callCtx, "workspace.list_project", nil)
	defer call.Close()
	result, _ := call.Final(callCtx)
	roots := decodeProjectRoots(result)
	a.mu.Lock()
	a.projectRoots = roots
	a.mu.Unlock()
	if len(roots) > 0 {
		ctx.Logger().Info("puppeteditor: synced project roots", "count", len(roots))
	}
}

// ---------------------------------------------------------------------------
// Persistence — implements persist.Persistent
// ---------------------------------------------------------------------------

// Save persists the full editor state (document + revisions + assets) via
// pkg/persist.
func (a *Actor) Save() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.saveLocked()
}

func (a *Actor) saveLocked() error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("puppeteditor"))
		if err != nil {
			return err
		}
	}
	return a.store.Save(a.actorID, a.state)
}

// Load restores the full editor state. Missing state (first start) is a valid
// zero condition handled by OnInit's bootstrap.
func (a *Actor) Load() error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("puppeteditor"))
		if err != nil {
			return err
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return persist.LoadOrZero(a.store, a.actorID, &a.state)
}

// ---------------------------------------------------------------------------
// Callable handlers (read-only)
// ---------------------------------------------------------------------------

// handleSnapshot returns the full read-only document projection: the document
// with its node tree plus the derived staging/revision counts so a single read
// paints the editor shell. The returned document is a deep copy with the param
// binding projection applied — bound properties (e.g. opacity, z) carry their
// effective values, not just the authored ones — so callers cannot mutate the
// actor's internal state and always see the param-driven result.
func (a *Actor) handleSnapshot(_ actor.PureContext, _ gen.PuppetDocumentSnapshotReq) (gen.PuppetDocumentSnapshotResp, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	stagedCount := 0
	for _, asset := range a.state.Assets {
		if asset.State == "staged" {
			stagedCount++
		}
	}

	doc := cloneDocument(a.state.Document)
	applyParamProjection(&doc)

	return gen.PuppetDocumentSnapshotResp{
		Snapshot: gen.PuppetDocumentSnapshot{
			Document:         doc,
			StagedAssetCount: int32(stagedCount),
			RevisionCount:    int64(len(a.state.Revisions)),
		},
	}, nil
}

// handleParamList is the public puppet.param.list callable: the document's
// param definitions and binding table as a focused read-only projection
// (schema types PuppetParamListReq/Resp). Current values are read through the
// document snapshot (ParamValues with Default fallback via projection); this
// callable exposes the authored model, not derived values.
func (a *Actor) handleParamList(_ actor.PureContext, _ gen.PuppetParamListReq) (gen.PuppetParamListResp, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	params := make([]gen.PuppetParam, len(a.state.Document.Params))
	copy(params, a.state.Document.Params)
	bindings := make([]gen.PuppetParamBinding, len(a.state.Document.Bindings))
	copy(bindings, a.state.Document.Bindings)
	return gen.PuppetParamListResp{Params: params, Bindings: bindings}, nil
}

// handleRevisionLog returns the immutable revision history, optionally limited
// to the most recent N entries.
func (a *Actor) handleRevisionLog(_ actor.PureContext, req gen.PuppetRevisionLogReq) (gen.PuppetRevisionLogResp, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	revs := a.state.Revisions
	if req.Limit > 0 && int(req.Limit) < len(revs) {
		revs = revs[len(revs)-int(req.Limit):]
	}
	// Return a copy so the caller cannot mutate the actor's internal slice.
	out := make([]gen.PuppetRevision, len(revs))
	copy(out, revs)
	return gen.PuppetRevisionLogResp{Revisions: out}, nil
}

// ---------------------------------------------------------------------------
// Author provenance
// ---------------------------------------------------------------------------

const (
	authorSystem  = "system"
	authorUnknown = "unknown"
)

// authorFromContext derives the revision author identity from the authenticated
// caller context, never from a client-supplied request field. Internal
// actor-to-actor calls (zero identity) record "system"; authenticated external
// callers record their token subject. This is the single place author
// provenance is decided, so future edit/commit handlers reuse it and cannot be
// tricked by a forged Author field in PuppetEditCommand.
func authorFromContext(ctx actor.PureContext) string {
	if ctx == nil {
		return authorSystem
	}
	ident := ctx.Identity()
	if ident.IsZero() {
		return authorUnknown
	}
	if ident.Subject != "" {
		return ident.Subject
	}
	return authorUnknown
}

// ---------------------------------------------------------------------------
// ID helpers
// ---------------------------------------------------------------------------

// cloneDocument returns a value-deep copy of doc so the node tree's nested
// slices and maps are independent of the actor's internal state. This
// includes the param model (Params), explicit param values (ParamValues), and
// the binding table (Bindings) — all three participate in revision snapshots
// and must never alias the live document.
func cloneDocument(doc gen.PuppetDocument) gen.PuppetDocument {
	doc.Root = cloneNode(doc.Root)
	if doc.Params != nil {
		params := make([]gen.PuppetParam, len(doc.Params))
		copy(params, doc.Params)
		doc.Params = params
	}
	if doc.ParamValues != nil {
		values := make(map[string]string, len(doc.ParamValues))
		for k, v := range doc.ParamValues {
			values[k] = v
		}
		doc.ParamValues = values
	}
	if doc.Bindings != nil {
		bindings := make([]gen.PuppetParamBinding, len(doc.Bindings))
		copy(bindings, doc.Bindings)
		doc.Bindings = bindings
	}
	return doc
}

// cloneNode deep-copies a PuppetNode and its recursive children/params so the
// returned subtree shares no mutable references with the original. Mesh is
// cloned field-by-field (vertices/indices/uvs) because PuppetNode.Mesh is a
// pointer — without this, a stored revision snapshot would alias the live
// document's mesh geometry.
func cloneNode(n gen.PuppetNode) gen.PuppetNode {
	if n.Mesh != nil {
		m := *n.Mesh
		m.Vertices = append([]float32(nil), n.Mesh.Vertices...)
		m.Indices = append([]int32(nil), n.Mesh.Indices...)
		m.UVs = append([]float32(nil), n.Mesh.UVs...)
		n.Mesh = &m
	}
	if n.Params != nil {
		p := make(map[string]string, len(n.Params))
		for k, v := range n.Params {
			p[k] = v
		}
		n.Params = p
	}
	if n.Children != nil {
		kids := make([]gen.PuppetNode, len(n.Children))
		for i := range n.Children {
			kids[i] = cloneNode(n.Children[i])
		}
		n.Children = kids
	}
	return n
}

// newID returns a short content-independent id with the given prefix, e.g.
// "doc_a1b2c3d4". It uses crypto/rand so ids are unpredictable (not sequential).
func newID(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + hex.EncodeToString(b[:])
}
