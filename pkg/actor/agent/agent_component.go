package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/agentkit"
	"github.com/qomos-w/sporemind/pkg/buildinfo"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// agentChatCardPrefix is the virtual component card prefix for @-mention
// conversable tags: card IDs look like agent-chat:<AgentRef.ID>. This does NOT
// collide with the external agent card prefix "agent:" (pkg/actor/project/
// agent_external_card.go), which surfaces agents as project cards: agent-chat:
// cards are per-target virtual components with no backing card entity, resolved
// against live workspace agent metadata and staying resolvable only while the
// bound target agent exists and is loaded.
const agentChatCardPrefix = "agent-chat:"

// browserChatCardPrefix is the virtual component card prefix for composer
// browser badges: card IDs look like browser-chat:<instanceId>, where
// instanceId is the browsermanager independent-window instance the user
// picked in the composer's % menu. Like agent-chat: these are per-target
// virtual components with no backing card entity; unlike agent-chat: the
// descriptor needs no live workspace lookup — the mount itself is the
// binding, the frontend owns the mount lifecycle (closing the window
// unmounts the card), and the crawl runtime validates the instance when a
// crawl actually starts.
const browserChatCardPrefix = "browser-chat:"

func (a *Actor) cardRefEnabled(cardID string) bool {
	refs := a.cardRefs
	if refs == nil {
		refs = canonicalCardRefsFromMounts(a.ComponentMounts)
	}
	for _, ref := range refs {
		if ref.ID == cardID {
			return !ref.Disabled
		}
	}
	return false
}

func (a *Actor) syncCanonicalCardRefs() {
	a.cardRefs = canonicalCardRefsFromMounts(a.ComponentMounts)
}

func canonicalCardRefsFromMounts(mounts []domain.AgentComponentMount) []gen.CardRef {
	refs := make([]gen.CardRef, 0, len(mounts))
	seen := make(map[string]bool, len(mounts))
	for _, mount := range mounts {
		if mount.CardID == "" || mount.Scope == skillScopeKindConfig || seen[mount.CardID] {
			continue
		}
		seen[mount.CardID] = true
		refs = append(refs, gen.CardRef{ID: mount.CardID, Source: mount.Kind, Scope: mount.Scope, Order: mount.Order, Disabled: !mount.Enabled})
	}
	return refs
}

func componentMountsFromCardRefs(refs []gen.CardRef) []domain.AgentComponentMount {
	mounts := make([]domain.AgentComponentMount, 0, len(refs))
	for _, ref := range refs {
		if ref.ID == "" {
			continue
		}
		scope := ref.Scope
		if scope == "" {
			scope = "user"
		}
		mounts = append(mounts, domain.AgentComponentMount{
			MountID: ref.ID, CardID: ref.ID, Kind: ref.Source,
			Enabled: !ref.Disabled, Order: ref.Order, Scope: scope, Title: ref.ID,
		})
	}
	return mounts
}

func (a *Actor) handleComponentMount(ctx actor.Context, req domain.AgentComponentMountReq) (domain.AgentComponentMountResp, error) {
	return a.handleComponentMountInternal(ctx, req, map[string]struct{}{})
}

func (a *Actor) handleComponentMountInternal(ctx actor.Context, req domain.AgentComponentMountReq, seen map[string]struct{}) (domain.AgentComponentMountResp, error) {
	if req.CardID == "" {
		return domain.AgentComponentMountResp{}, fmt.Errorf("agent.component.mount: cardId is required")
	}
	if req.CardID == "builtin:bundle:coordinator-wearable" && (a.agentKind != domain.AgentKindCoordinator || !buildinfo.IsDev()) {
		return domain.AgentComponentMountResp{}, fmt.Errorf("agent.component.mount: coordinator wearable bundle is reserved for coordinator agents in dev builds")
	}
	if conflicting := a.conflictingFlowMode(req.CardID); conflicting != "" {
		return domain.AgentComponentMountResp{}, fmt.Errorf("agent.component.mount: cannot mount %q while %q is active (mutually exclusive flow)", req.CardID, conflicting)
	}
	if err := a.validateComponentCard(ctx, req.CardID); err != nil {
		return domain.AgentComponentMountResp{}, err
	}
	if _, ok := seen[req.CardID]; ok {
		for _, m := range a.ComponentMounts {
			if m.CardID == req.CardID {
				return domain.AgentComponentMountResp{Mount: m}, nil
			}
		}
		return domain.AgentComponentMountResp{}, nil
	}
	seen[req.CardID] = struct{}{}

	// agent.component.mount is the UI's entry point for worktree mode (mode
	// panel / omnibox), while the /worktree slash command enters via
	// handleChatSubmit. Both must bind the worktree BEFORE the card is
	// mounted so card presence always implies an active binding; a card
	// mounted without one is unmounted by the turn-start
	// refreshWorktreeStatus on the next message.
	if req.CardID == "builtin:mode:worktree" {
		if err := a.enterWorktreeMode(ctx); err != nil {
			return domain.AgentComponentMountResp{}, fmt.Errorf("agent.component.mount: %w", err)
		}
	}

	descriptor, _ := a.componentDescriptor(ctx, req.CardID)
	mount := a.applyComponentMount(ctx, req, descriptor)

	if err := a.mountComponentDependencies(ctx, req.CardID, seen); err != nil {
		return domain.AgentComponentMountResp{Mount: mount}, err
	}

	a.ComponentRevision++
	a.invalidateComponentSnapshot(ctx)
	a.stageToolsRefresh()
	a.syncMemoryMountState(ctx)
	if a.cardRefs == nil {
		a.syncCanonicalCardRefs()
	}
	a.saveMailbox(ctx)
	a.notifyWorkspaceStatus(ctx)
	return domain.AgentComponentMountResp{Mount: mount}, nil
}

func (a *Actor) mountComponentDependencies(ctx actor.Context, cardID string, seen map[string]struct{}) error {
	descriptor, ok := a.componentDescriptor(ctx, cardID)
	if !ok {
		return fmt.Errorf("component %q could not be resolved", cardID)
	}
	for _, dep := range descriptor.Dependencies {
		if !dep.Required {
			continue
		}
		// Skip dependencies that are already enabled: re-mounting would
		// overwrite their existing scope (e.g. a "user" mount downgraded to
		// "dependency") and needlessly bump the revision.
		if a.cardRefEnabled(dep.CardID) {
			continue
		}
		depReq := domain.AgentComponentMountReq{
			CardID:  dep.CardID,
			Enabled: true,
			Scope:   "dependency",
		}
		if _, err := a.handleComponentMountInternal(ctx, depReq, seen); err != nil {
			return fmt.Errorf("mount dependency %q of %q: %w", dep.CardID, cardID, err)
		}
	}
	return nil
}

func (a *Actor) applyComponentMount(ctx actor.Context, req domain.AgentComponentMountReq, descriptor domain.ComponentDescriptor) domain.AgentComponentMount {
	now := time.Now().UTC().Format(time.RFC3339)
	scope := req.Scope
	if scope == "" {
		scope = "user"
	}
	var mount domain.AgentComponentMount
	if a.cardRefs != nil {
		found := false
		for i := range a.cardRefs {
			if a.cardRefs[i].ID != req.CardID {
				continue
			}
			found = true
			if req.Enabled {
				a.cardRefs[i].Disabled = false
			}
			a.cardRefs[i].Order = req.Order
			a.cardRefs[i].Scope = scope
			if descriptor.Ref.Kind != "" {
				a.cardRefs[i].Source = descriptor.Ref.Kind
			}
			break
		}
		if !found {
			a.cardRefs = append(a.cardRefs, gen.CardRef{
				ID: req.CardID, Source: descriptor.Ref.Kind, Scope: scope, Order: req.Order,
			})
		}
		a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)
		for i := range a.ComponentMounts {
			if a.ComponentMounts[i].CardID == req.CardID {
				a.ComponentMounts[i].Title = descriptor.Title
				a.ComponentMounts[i].Icon = descriptor.Icon
				a.ComponentMounts[i].Visual = descriptor.Visual
				a.ComponentMounts[i].Kind = descriptor.Ref.Kind
				a.ComponentMounts[i].UpdatedAt = now
				mount = a.ComponentMounts[i]
			}
		}
		return mount
	}
	for i := range a.ComponentMounts {
		if a.ComponentMounts[i].CardID != req.CardID {
			continue
		}
		a.ComponentMounts[i].Enabled = req.Enabled || a.ComponentMounts[i].Enabled
		a.ComponentMounts[i].Order = req.Order
		a.ComponentMounts[i].Scope = scope
		a.ComponentMounts[i].Title = descriptor.Title
		a.ComponentMounts[i].Icon = descriptor.Icon
		a.ComponentMounts[i].Visual = descriptor.Visual
		a.ComponentMounts[i].Kind = descriptor.Ref.Kind
		a.ComponentMounts[i].UpdatedAt = now
		return a.ComponentMounts[i]
	}
	mount = domain.AgentComponentMount{
		MountID:   ctx.NewID().String(),
		CardID:    req.CardID,
		Enabled:   true,
		Order:     req.Order,
		Scope:     scope,
		Title:     descriptor.Title,
		Icon:      descriptor.Icon,
		Visual:    descriptor.Visual,
		Kind:      descriptor.Ref.Kind,
		MountedAt: now,
		UpdatedAt: now,
	}
	a.ComponentMounts = append(a.ComponentMounts, mount)
	return mount
}

// onModeUnmounted handles mode-specific cleanup when a mode card is
// explicitly unmounted by the user. It clears the session state associated
// with the mode (goal, workflow, memory) so unmounting a mode is equivalent
// to deactivating it. Required bundles that were mounted as mode dependencies
// (scope "user" or "dependency") are removed via removeModeMountedCards;
// bundles mounted with scope "builtin" (kind config / permission proxy) are
// preserved.
func (a *Actor) onModeUnmounted(ctx actor.Context, cardID string) {
	switch cardID {
	case "builtin:mode:goal":
		a.RawSession.Goal = nil
	case "builtin:mode:workflow":
		a.RawSession.ActiveWorkflow = nil
	case "builtin:mode:worktree":
		// System-managed mode: worktree binding state (worktreeID/Name/Status)
		// is tracked separately by refreshWorktreeStatus. No session state or
		// required bundles to clean up.
		return
	case "builtin:mode:scheduler":
		// Scheduler mode is bound to the scheduler card(s) recorded in
		// RawSession.ActiveScheduler. Unmounting the mode clears that binding
		// so the agent no longer carries scheduler state.
		a.RawSession.ActiveScheduler = nil
	case "builtin:mode:memory":
		a.unmountMemory(ctx)
		return // unmountMemory handles its own state
	}
	// Remove required bundles that were mounted as mode dependencies
	// (scope "user" or "dependency"), preserving "builtin" scope bundles.
	descriptor, ok := a.componentDescriptor(ctx, cardID)
	if !ok {
		return
	}
	for _, dep := range descriptor.Dependencies {
		if dep.Required {
			a.removeModeMountedCards([]string{dep.CardID})
		}
	}
	// Snapshot was already invalidated by the caller after removing the mode
	// card itself; removing the dependency bundles above may change the mount
	// set again, so invalidate once more to keep the cached snapshot fresh.
	a.ComponentRevision++
	a.invalidateComponentSnapshot(ctx)
}

func (a *Actor) handleComponentUnmount(ctx actor.Context, req domain.AgentComponentUnmountReq) (domain.AgentComponentUnmountResp, error) {
	if req.CardID == "" {
		return domain.AgentComponentUnmountResp{}, fmt.Errorf("agent.component.unmount: cardId is required")
	}
	if err := a.requireWorktreeUnmountConfirm(req); err != nil {
		return domain.AgentComponentUnmountResp{}, err
	}
	if req.CardID == "builtin:mode:worktree" && req.Confirm {
		if err := a.exitWorktreeMode(ctx); err != nil {
			return domain.AgentComponentUnmountResp{}, err
		}
	}
	if a.cardRefs != nil {
		for i := range a.cardRefs {
			if a.cardRefs[i].ID != req.CardID {
				continue
			}
			if descriptor, ok := a.componentDescriptor(ctx, req.CardID); ok && descriptor.Protected && a.cardRefs[i].Scope == "builtin" {
				return domain.AgentComponentUnmountResp{}, fmt.Errorf("agent.component.unmount: protected card %q cannot be unmounted", req.CardID)
			}
			a.cardRefs = append(a.cardRefs[:i], a.cardRefs[i+1:]...)
			a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)
			a.ComponentRevision++
			a.invalidateComponentSnapshot(ctx)
			a.stageToolsRefresh()
			a.onModeUnmounted(ctx, req.CardID)
			a.saveMailbox(ctx)
			a.notifyWorkspaceStatus(ctx)
			return domain.AgentComponentUnmountResp{CardID: req.CardID}, nil
		}
		return domain.AgentComponentUnmountResp{}, fmt.Errorf("agent.component.unmount: card %q is not mounted", req.CardID)
	}
	for i := range a.ComponentMounts {
		if a.ComponentMounts[i].CardID == req.CardID {
			if descriptor, ok := a.componentDescriptor(ctx, req.CardID); ok && descriptor.Protected && a.ComponentMounts[i].Scope == "builtin" {
				return domain.AgentComponentUnmountResp{}, fmt.Errorf("agent.component.unmount: protected card %q cannot be unmounted", req.CardID)
			}
			a.ComponentMounts = append(a.ComponentMounts[:i], a.ComponentMounts[i+1:]...)
			a.ComponentRevision++
			a.invalidateComponentSnapshot(ctx)
			a.stageToolsRefresh()
			a.onModeUnmounted(ctx, req.CardID)
			a.syncCanonicalCardRefs()
			a.saveMailbox(ctx)
			a.notifyWorkspaceStatus(ctx)
			return domain.AgentComponentUnmountResp{CardID: req.CardID}, nil
		}
	}
	return domain.AgentComponentUnmountResp{}, fmt.Errorf("agent.component.unmount: card %q is not mounted", req.CardID)
}

func (a *Actor) handleComponentSetEnabled(ctx actor.Context, req domain.AgentComponentSetEnabledReq) (domain.AgentComponentSetEnabledResp, error) {
	if a.cardRefs != nil {
		for i := range a.cardRefs {
			if a.cardRefs[i].ID != req.CardID {
				continue
			}
			if !req.Enabled {
				if descriptor, ok := a.componentDescriptor(ctx, req.CardID); ok && descriptor.Protected && a.cardRefs[i].Scope == "builtin" {
					return domain.AgentComponentSetEnabledResp{}, fmt.Errorf("agent.component.set_enabled: protected card %q cannot be disabled", req.CardID)
				}
			}
			a.cardRefs[i].Disabled = !req.Enabled
			a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)
			a.ComponentRevision++
			a.invalidateComponentSnapshot(ctx)
			a.stageToolsRefresh()
			a.syncMemoryMountState(ctx)
			a.saveMailbox(ctx)
			a.notifyWorkspaceStatus(ctx)
			for _, mount := range a.ComponentMounts {
				if mount.CardID == req.CardID {
					return domain.AgentComponentSetEnabledResp{Mount: mount}, nil
				}
			}
		}
		return domain.AgentComponentSetEnabledResp{}, fmt.Errorf("agent.component.set_enabled: card %q is not mounted", req.CardID)
	}
	for i := range a.ComponentMounts {
		if a.ComponentMounts[i].CardID == req.CardID {
			if !req.Enabled {
				if descriptor, ok := a.componentDescriptor(ctx, req.CardID); ok && descriptor.Protected && a.ComponentMounts[i].Scope == "builtin" {
					return domain.AgentComponentSetEnabledResp{}, fmt.Errorf("agent.component.set_enabled: protected card %q cannot be disabled", req.CardID)
				}
			}
			a.ComponentMounts[i].Enabled = req.Enabled
			a.ComponentMounts[i].UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			a.ComponentRevision++
			a.invalidateComponentSnapshot(ctx)
			a.stageToolsRefresh()
			a.syncCanonicalCardRefs()
			a.syncMemoryMountState(ctx)
			a.saveMailbox(ctx)
			a.notifyWorkspaceStatus(ctx)
			return domain.AgentComponentSetEnabledResp{Mount: a.ComponentMounts[i]}, nil
		}
	}
	return domain.AgentComponentSetEnabledResp{}, fmt.Errorf("agent.component.set_enabled: card %q is not mounted", req.CardID)
}

func (a *Actor) handleComponentList(ctx actor.PureContext, _ domain.AgentComponentListReq) (domain.AgentComponentListResp, error) {
	if a.cardRefs == nil {
		return domain.AgentComponentListResp{Items: append([]domain.AgentComponentMount(nil), a.ComponentMounts...)}, nil
	}
	legacy := make(map[string]domain.AgentComponentMount, len(a.ComponentMounts))
	for _, mount := range a.ComponentMounts {
		legacy[mount.CardID] = mount
	}
	items := make([]domain.AgentComponentMount, 0, len(a.cardRefs))
	for _, ref := range a.cardRefs {
		mount := legacy[ref.ID]
		mount.CardID = ref.ID
		mount.Enabled = !ref.Disabled
		mount.Order = ref.Order
		mount.Scope = ref.Scope
		if mount.MountID == "" {
			mount.MountID = ref.ID
		}
		if descriptor, ok := a.componentDescriptor(ctx, ref.ID); ok {
			mount.Kind = descriptor.Ref.Kind
			mount.Title = descriptor.Title
			mount.Icon = descriptor.Icon
			mount.Visual = descriptor.Visual
		}
		items = append(items, mount)
	}
	return domain.AgentComponentListResp{Items: items}, nil
}

func (a *Actor) handleComponentSnapshot(_ actor.PureContext, _ domain.AgentComponentSnapshotReq) (domain.AgentComponentSnapshotResp, error) {
	snap := a.componentSnapshot.Load()
	if snap == nil {
		return domain.AgentComponentSnapshotResp{}, nil
	}
	return domain.AgentComponentSnapshotResp{Snapshot: cloneComponentSnapshot(*snap)}, nil
}

func (a *Actor) componentDescriptor(ctx actor.PureContext, cardID string) (domain.ComponentDescriptor, bool) {
	// builtin:* cards live in the system meta project, not the parent
	// project. Resolve them via workspace so every agent — regardless of
	// which project it belongs to — can access builtin components.
	if strings.HasPrefix(cardID, "builtin:") {
		return a.builtinComponentDescriptor(ctx, cardID)
	}
	// app-bundle:* components are virtual: projected from the appmanager
	// registry (bundle definitions embedded in the loaded artifact's
	// manifest), host-wide for every project, no card entity anywhere.
	if strings.HasPrefix(cardID, "app-bundle:") {
		return a.appBundleComponentDescriptor(ctx, cardID)
	}
	// agent-chat:* cards are virtual, per-target conversable tags: the
	// descriptor is projected from live workspace agent metadata and resolves
	// only while the bound target agent exists and is loaded.
	if strings.HasPrefix(cardID, agentChatCardPrefix) {
		return a.agentChatComponentDescriptor(ctx, cardID)
	}
	// browser-chat:* cards are virtual, per-browser-instance tags: the
	// descriptor projects the crawl.* tool surface pre-bound to the mounted
	// independent browser window (browser-chat:<instanceId>). Resolution is
	// static — no live lookup — so mount validation and snapshot caching
	// treat it like any materialized card.
	if strings.HasPrefix(cardID, browserChatCardPrefix) {
		return browserChatComponentDescriptor(cardID)
	}
	parent := ctx.Parent()
	planner := ctx.Planner()
	if parent == nil || planner == nil {
		return domain.ComponentDescriptor{}, false
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	result, err := planner.Call(callCtx, parent, "project.component_get", domain.ProjectComponentGetReq{CardID: cardID}).Await()
	cancel()
	if err != nil || result == nil {
		return domain.ComponentDescriptor{}, false
	}
	return decodeComponentGet(result)
}

func (a *Actor) appBundleComponentDescriptor(ctx actor.PureContext, cardID string) (domain.ComponentDescriptor, bool) {
	planner := ctx.Planner()
	if planner == nil {
		return domain.ComponentDescriptor{}, false
	}
	appmanagerRef, ok := ctx.LookupService("appmanager")
	if !ok {
		return domain.ComponentDescriptor{}, false
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	result, err := planner.Call(callCtx, appmanagerRef, "appmanager.component_get", gen.AppManagerComponentGetReq{CardID: cardID}).Await()
	cancel()
	if err != nil || result == nil {
		return domain.ComponentDescriptor{}, false
	}
	return decodeAppBundleComponentGet(result)
}

// decodeAppBundleComponentGet decodes an appmanager.component_get response.
// Unlike the project/workspace component_get callables (which share the
// ProjectComponentGetResp type), appmanager returns its own response type with
// the same Component field, so typed, raw-JSON, and map forms all decode here.
func decodeAppBundleComponentGet(result any) (domain.ComponentDescriptor, bool) {
	switch value := result.(type) {
	case gen.AppManagerComponentGetResp:
		return value.Component, true
	default:
		return decodeComponentGet(result)
	}
}

func (a *Actor) builtinComponentDescriptor(ctx actor.PureContext, cardID string) (domain.ComponentDescriptor, bool) {
	planner := ctx.Planner()
	if planner == nil {
		return builtinDescriptorFromAsset(cardID)
	}
	workspaceRef, ok := ctx.LookupService("workspace")
	if !ok {
		return builtinDescriptorFromAsset(cardID)
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	result, err := planner.Call(callCtx, workspaceRef, "workspace.component_get", domain.ProjectComponentGetReq{CardID: cardID}).Await()
	cancel()
	if err != nil || result == nil {
		return builtinDescriptorFromAsset(cardID)
	}
	descriptor, ok := decodeComponentGet(result)
	if !ok {
		return builtinDescriptorFromAsset(cardID)
	}
	// The catalog path falls back to the raw card id as the descriptor title
	// ("builtin:bundle:file-tools"); overlay the friendly name declared by the
	// embedded asset so composer badges and menus show "File Tools" instead.
	if descriptor.Title == "" || descriptor.Title == cardID {
		if title, found := builtinAssetTitle(cardID); found {
			descriptor.Title = title
		}
	}
	return descriptor, true
}

// agentChatComponentDescriptor resolves a virtual agent-chat:<AgentRef.ID>
// component card by querying the workspace for live agent metadata. The lookup
// is workspace-wide (no ProjectID filter): the @ menu offers agents from every
// project and the contributed callables address targets by actor ID, so
// scoping to the mounting agent's parent project would reject valid
// cross-project mounts. Only a target found with LoadState "loaded" resolves;
// an unknown or unloaded target returns not-ok so mount validation rejects the
// card with an error naming the target, and a target unloading after mount
// makes the tools vanish with a snapshot diagnostic (mirrors worktree mode).
func (a *Actor) agentChatComponentDescriptor(ctx actor.PureContext, cardID string) (domain.ComponentDescriptor, bool) {
	targetID := strings.TrimPrefix(cardID, agentChatCardPrefix)
	if targetID == "" {
		return domain.ComponentDescriptor{}, false
	}
	planner := ctx.Planner()
	if planner == nil {
		return domain.ComponentDescriptor{}, false
	}
	workspaceRef, ok := ctx.LookupService("workspace")
	if !ok {
		return domain.ComponentDescriptor{}, false
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	result, err := planner.Call(callCtx, workspaceRef, "workspace.list_agents",
		domain.WorkspaceListAgentsReq{}).Await()
	cancel()
	if err != nil || result == nil {
		return domain.ComponentDescriptor{}, false
	}
	var items []domain.AgentRef
	switch v := result.(type) {
	case domain.AgentRefListResp:
		items = v.Items
	case *domain.AgentRefListResp:
		items = v.Items
	default:
		return domain.ComponentDescriptor{}, false
	}
	var target *domain.AgentRef
	for i := range items {
		if items[i].ID == targetID || items[i].ActorID == targetID {
			target = &items[i]
			break
		}
	}
	if target == nil || target.LoadState != "loaded" {
		return domain.ComponentDescriptor{}, false
	}
	return agentChatDescriptorFromRef(cardID, *target), true
}

// agentChatDescriptorFromRef builds the tools-only descriptor for a
// conversable agent-chat card bound to a loaded target agent: exactly the four
// inter-agent ops, each description naming the bound target. NO Prompts
// contribution — the hot-context block owns the prompt surface.
func agentChatDescriptorFromRef(cardID string, target domain.AgentRef) domain.ComponentDescriptor {
	displayName := target.DisplayName
	if displayName == "" {
		displayName = target.ID
	}
	specs := []struct {
		callable    string
		description string
	}{
		{"workspace.agent_send_message", "Send a message to " + displayName + "."},
		{"workspace.agent_read_message", "Read messages from " + displayName + "."},
		{"workspace.agent_pause", "Pause " + displayName + "."},
		{"workspace.agent_resume", "Resume " + displayName + "."},
	}
	descriptor := domain.ComponentDescriptor{
		Ref:   domain.ComponentRef{CardID: cardID, Kind: "agent-chat", Source: "workspace"},
		Title: "Chat: " + displayName,
		// The composer badge row only renders mounts that carry both Title
		// and Icon (AIConversationComposer mountBadges filter), so the icon
		// is what makes the conversable tag visible as a persistent chip.
		Icon: "message-circle",
	}
	for i, spec := range specs {
		descriptor.Tools = append(descriptor.Tools, domain.ComponentToolContribution{
			ID:          fmt.Sprintf("%s-tool-%d", cardID, i),
			CardID:      cardID,
			CallableID:  spec.callable,
			Description: spec.description,
		})
	}
	return descriptor
}

// browserChatComponentDescriptor resolves a virtual browser-chat:<instanceId>
// component card. Unlike agent-chat: the suffix alone is the whole binding —
// no workspace query is needed to build the descriptor — so an empty suffix
// is the only rejection. The mounted window's liveness is intentionally NOT
// gated here: the frontend owns the mount lifecycle (closing the window
// unmounts the card) and the crawl runtime validates the instance when a
// crawl actually starts, mirroring how agent-chat keeps its tools live via
// withLiveAgentChatTools while browser-chat needs no live patch pass.
func browserChatComponentDescriptor(cardID string) (domain.ComponentDescriptor, bool) {
	instanceID := strings.TrimPrefix(cardID, browserChatCardPrefix)
	if instanceID == "" {
		return domain.ComponentDescriptor{}, false
	}
	return browserChatDescriptorFromID(cardID, instanceID), true
}

// browserChatDescriptorFromID builds the tools-only descriptor for a
// browser-chat card bound to an independent browser window instance: the
// five crawl.* callables from the crawl actor's registration surface
// (pkg/actor/crawl/crawl.go), each pre-bound to the mounted window via
// Config.InstanceID=<instanceId> on crawl.start and naming the bound window
// in its description. NO Prompts contribution — the hot-context block owns
// the prompt surface (mirrors agentChatDescriptorFromRef).
func browserChatDescriptorFromID(cardID, instanceID string) domain.ComponentDescriptor {
	specs := []struct {
		callable    string
		description string
		usage       string
	}{
		{
			"crawl.start",
			"Start a browser crawl task that drives the mounted independent browser window " + instanceID + " (visible and reusable, not a fresh hidden window).",
			`Pre-bound: pass Config.InstanceID = "` + instanceID + `" on every call so the crawl reuses the mounted window.`,
		},
		{
			"crawl.status",
			"Get the state of a crawl task running on the mounted independent browser window " + instanceID + ".",
			`Poll with the TaskID returned by crawl.start (started with Config.InstanceID = "` + instanceID + `").`,
		},
		{
			"crawl.results",
			"Fetch the pages collected by a crawl task running on the mounted independent browser window " + instanceID + ".",
			`Page through with Cursor/NextCursor using the TaskID from crawl.start (Config.InstanceID = "` + instanceID + `").`,
		},
		{
			"crawl.cancel",
			"Cancel a crawl task running on the mounted independent browser window " + instanceID + ".",
			`Pass the TaskID from crawl.start (Config.InstanceID = "` + instanceID + `").`,
		},
		{
			"crawl.handoff",
			"Hand a login-walled crawl task on the mounted independent browser window " + instanceID + " to the user.",
			`Only while the task is awaiting_login; pass the TaskID from crawl.start (Config.InstanceID = "` + instanceID + `").`,
		},
	}
	descriptor := domain.ComponentDescriptor{
		Ref:   domain.ComponentRef{CardID: cardID, Kind: "browser-chat", Source: "browser"},
		Title: "Browser: " + instanceID,
		// The composer badge row only renders mounts that carry both Title
		// and Icon (same filter as agent-chat), so the icon is what makes
		// the browser chip visible.
		Icon: "globe",
	}
	for i, spec := range specs {
		descriptor.Tools = append(descriptor.Tools, domain.ComponentToolContribution{
			ID:          fmt.Sprintf("%s-tool-%d", cardID, i),
			CardID:      cardID,
			CallableID:  spec.callable,
			Description: spec.description,
			Usage:       spec.usage,
		})
	}
	return descriptor
}

// agentChatLiveTarget finds the workspace agent a conversable card is bound
// to, matching by AgentRef.ID or ActorID (the same keys mount validation
// accepts). Returns nil when the target is unknown or not loaded.
func agentChatLiveTarget(index map[string]domain.AgentRef, targetID string) *domain.AgentRef {
	target, ok := index[targetID]
	if !ok || target.LoadState != "loaded" {
		return nil
	}
	return &target
}

// indexAgentRefs keys workspace agent refs by both AgentRef.ID and ActorID so
// agent-chat card suffixes resolve either form.
func indexAgentRefs(items []domain.AgentRef) map[string]domain.AgentRef {
	index := make(map[string]domain.AgentRef, len(items)*2)
	for _, item := range items {
		if item.ActorID != "" {
			index[item.ActorID] = item
		}
		index[item.ID] = item
	}
	return index
}

// withLiveAgentChatTools patches a cloned component snapshot so agent-chat
// mounts contribute tools according to the targets' CURRENT load state. The
// cached snapshot deliberately omits agent-chat contributions: after a
// process restart every agent restores LoadState="unloaded", so a snapshot
// built at mount time would freeze that state and permanently drop the
// send-message tools even after the target loads. Mirroring
// buildConversableBlock, ONE workspace.list_agents lookup resolves every
// mounted target per call; lookup failure fails closed (no agent-chat tools
// rather than stale ones). Snapshots without enabled agent-chat mounts are
// returned untouched (zero extra IO).
func (a *Actor) withLiveAgentChatTools(ctx actor.Context, snapshot domain.AgentComponentSnapshot) domain.AgentComponentSnapshot {
	hasAgentChatMount := false
	for _, mount := range a.ComponentMounts {
		if mount.Enabled && strings.HasPrefix(mount.CardID, agentChatCardPrefix) {
			hasAgentChatMount = true
			break
		}
	}
	if !hasAgentChatMount {
		return snapshot
	}
	planner := ctx.Planner()
	workspaceRef, workspaceOK := ctx.LookupService("workspace")
	if planner == nil || !workspaceOK {
		return snapshot
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	result, err := planner.Call(callCtx, workspaceRef, "workspace.list_agents", domain.WorkspaceListAgentsReq{}).Await()
	cancel()
	if err != nil || result == nil {
		return snapshot
	}
	var items []domain.AgentRef
	switch v := result.(type) {
	case domain.AgentRefListResp:
		items = v.Items
	case *domain.AgentRefListResp:
		items = v.Items
	default:
		return snapshot
	}
	index := indexAgentRefs(items)
	tools := make([]domain.ComponentToolContribution, 0, len(snapshot.Tools)+4)
	for _, tool := range snapshot.Tools {
		if strings.HasPrefix(tool.CardID, agentChatCardPrefix) {
			continue
		}
		tools = append(tools, tool)
	}
	seen := make(map[string]struct{})
	for _, mount := range a.ComponentMounts {
		if !mount.Enabled || !strings.HasPrefix(mount.CardID, agentChatCardPrefix) {
			continue
		}
		targetID := strings.TrimPrefix(mount.CardID, agentChatCardPrefix)
		if _, dup := seen[targetID]; dup {
			continue
		}
		seen[targetID] = struct{}{}
		target := agentChatLiveTarget(index, targetID)
		if target == nil {
			continue
		}
		tools = append(tools, agentChatDescriptorFromRef(mount.CardID, *target).Tools...)
	}
	snapshot.Tools = tools
	return snapshot
}

// builtinAssetTitle returns the human-readable title declared by the embedded
// asset for a builtin card, if any.
func builtinAssetTitle(cardID string) (string, bool) {
	assets, err := agentkit.LoadBuiltinAssets()
	if err != nil {
		return "", false
	}
	for _, asset := range assets {
		if asset.Title == cardID && asset.BuiltinTitle != "" {
			return asset.BuiltinTitle, true
		}
	}
	return "", false
}

// builtinModeFlow returns the flow group declared by a builtin mode card.
// Modes sharing the same flow value are mutually exclusive: only one can be
// mounted at a time. Returns ("", false) for cards without a flow declaration.
func builtinModeFlow(cardID string) (string, bool) {
	assets, err := agentkit.LoadBuiltinAssets()
	if err != nil {
		return "", false
	}
	for _, asset := range assets {
		if asset.Title == cardID && asset.Flow != "" {
			return asset.Flow, true
		}
	}
	return "", false
}

// conflictingFlowMode returns the card ID of an already-mounted mode that
// shares the same flow group as cardID, or "" if there is no conflict.
func (a *Actor) conflictingFlowMode(cardID string) string {
	flow, ok := builtinModeFlow(cardID)
	if !ok {
		return ""
	}
	for _, ref := range a.cardRefs {
		if ref.ID == cardID || ref.Disabled {
			continue
		}
		if otherFlow, ok := builtinModeFlow(ref.ID); ok && otherFlow == flow {
			return ref.ID
		}
	}
	return ""
}

// builtinDescriptorFromAsset resolves a builtin card descriptor from the
// embedded agentkit assets. It is used as a local fallback when the workspace
// service is not yet available or cannot resolve the builtin card, so that
// builtin components (especially builtin:mode:goal) remain functional across
// agent restarts.
func builtinDescriptorFromAsset(cardID string) (domain.ComponentDescriptor, bool) {
	assets, err := agentkit.LoadBuiltinAssets()
	if err != nil {
		return domain.ComponentDescriptor{}, false
	}
	for _, asset := range assets {
		if asset.Title != cardID {
			continue
		}
		descriptor := domain.ComponentDescriptor{
			Ref:       domain.ComponentRef{CardID: cardID, Kind: asset.Type, Source: asset.Source},
			Title:     asset.BuiltinTitle,
			Icon:      asset.Icon,
			Visual:    componentVisualFromAsset(asset.Visual),
			Protected: asset.Protected,
		}
		if asset.Body != "" {
			placement, priority := resolveAssetPromptMeta(cardID, asset.Data)
			descriptor.Prompts = append(descriptor.Prompts, domain.ComponentPromptContribution{
				ID:        cardID + "-body",
				CardID:    cardID,
				Title:     asset.BuiltinTitle,
				Text:      asset.Body,
				Placement: placement,
				Priority:  priority,
			})
		}
		for i, tool := range asset.Tools {
			descriptor.Tools = append(descriptor.Tools, domain.ComponentToolContribution{
				ID:         fmt.Sprintf("%s-tool-%d", cardID, i),
				CardID:     cardID,
				CallableID: tool,
			})
		}
		for _, dep := range asset.Dependencies {
			descriptor.Dependencies = append(descriptor.Dependencies, domain.ComponentDependency{
				CardID:   dep,
				Required: true,
				Reason:   "mode dependency",
			})
		}
		return descriptor, true
	}
	return domain.ComponentDescriptor{}, false
}

// resolveAssetPromptMeta extracts the placement and priority for a builtin
// card body contribution from its parsed frontmatter data, mirroring the
// workspace projection path (card_projection.go). prompt:profile: cards are
// forced to "role"; an empty or "prompt" placement falls back to "policy";
// any other declared placement (e.g. "tool_guidance") is preserved so bundle
// bodies land in their intended section instead of being hard-dropped to
// "system" (which the compiler maps to "policy").
func resolveAssetPromptMeta(cardID string, data map[string]any) (placement string, priority int32) {
	if s, ok := data["placement"].(string); ok {
		placement = s
	}
	if strings.HasPrefix(cardID, "prompt:profile:") {
		placement = "role"
	}
	if placement == "" || placement == "prompt" {
		placement = "policy"
	}
	switch v := data["priority"].(type) {
	case int:
		priority = int32(v)
	case int32:
		priority = v
	case int64:
		priority = int32(v)
	case float64:
		priority = int32(v)
	}
	return placement, priority
}

// componentVisualFromAsset converts an agentkit card visual into the wire
// ComponentVisual used by descriptors and mounts, so badges and the omnibox can
// render colored lucide icons. Returns nil when the asset declares no visual.
func componentVisualFromAsset(v *agentkit.CardVisual) *domain.ComponentVisual {
	if v == nil {
		return nil
	}
	cv := &domain.ComponentVisual{}
	set := false
	if v.Icon != "" {
		cv.Icon, set = v.Icon, true
	}
	if v.Accent != "" {
		cv.Accent, set = v.Accent, true
	}
	if v.Emphasis != "" {
		cv.Emphasis, set = v.Emphasis, true
	}
	if v.Color != "" {
		cv.Color, set = v.Color, true
	}
	if v.Background != "" {
		cv.Background, set = v.Background, true
	}
	if v.Border != "" {
		cv.Border, set = v.Border, true
	}
	if v.Size != 0 {
		cv.Size, set = int32(v.Size), true
	}
	if !set {
		return nil
	}
	return cv
}

func (a *Actor) validateComponentCard(ctx actor.Context, cardID string) error {
	if _, ok := a.componentDescriptor(ctx, cardID); !ok {
		return fmt.Errorf("agent.component.mount: component card %q is unavailable", cardID)
	}
	return nil
}

// ensureComponentCardMounted ensures that a component card is present and
// enabled in the mount list when active is true. It returns true if any mount
// state was changed. When active is false the function is a no-op; callers that
// need to unmount should do so explicitly (see clearGoal). This lets OnStart
// repair missing mode mounts (e.g. builtin:mode:goal) without disturbing other
// lifecycle paths.
func (a *Actor) ensureComponentCardMounted(ctx actor.Context, cardID, scope string, active bool) bool {
	if !active {
		return false
	}
	if a.cardRefs != nil {
		for i := range a.cardRefs {
			if a.cardRefs[i].ID != cardID {
				continue
			}
			if a.cardRefs[i].Disabled {
				a.cardRefs[i].Disabled = false
				a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)
				a.ComponentRevision++
				a.invalidateComponentSnapshot(ctx)
				return true
			}
			return false
		}
		a.cardRefs = append(a.cardRefs, gen.CardRef{ID: cardID, Scope: scope})
		a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)
		a.ComponentRevision++
		a.invalidateComponentSnapshot(ctx)
		return true
	}
	for i := range a.ComponentMounts {
		if a.ComponentMounts[i].CardID != cardID {
			continue
		}
		if !a.ComponentMounts[i].Enabled {
			a.ComponentMounts[i].Enabled = true
			a.ComponentRevision++
			a.invalidateComponentSnapshot(ctx)
			return true
		}
		return false
	}
	now := time.Now().UTC().Format(time.RFC3339)
	a.ComponentMounts = append(a.ComponentMounts, domain.AgentComponentMount{
		MountID:   cardID,
		CardID:    cardID,
		Enabled:   true,
		Scope:     scope,
		MountedAt: now,
		UpdatedAt: now,
	})
	a.ComponentRevision++
	a.invalidateComponentSnapshot(ctx)
	a.syncCanonicalCardRefs()
	return true
}

// removeMountedCards removes the given card IDs from cardRefs (or from
// ComponentMounts when cardRefs is nil), keeping both in sync. It is the shared
// removal path for lifecycle cleanup (clearGoal, goal-mode unmount) and returns
// true when any mount state changed.
func (a *Actor) removeMountedCards(ids []string) bool {
	changed := false
	if a.cardRefs != nil {
		for _, id := range ids {
			for i := range a.cardRefs {
				if a.cardRefs[i].ID != id {
					continue
				}
				a.cardRefs = append(a.cardRefs[:i], a.cardRefs[i+1:]...)
				a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)
				changed = true
				break
			}
		}
	} else {
		for _, id := range ids {
			for i := range a.ComponentMounts {
				if a.ComponentMounts[i].CardID != id {
					continue
				}
				a.ComponentMounts = append(a.ComponentMounts[:i], a.ComponentMounts[i+1:]...)
				a.syncCanonicalCardRefs()
				changed = true
				break
			}
		}
	}
	return changed
}

// removeModeMountedCards removes the given card IDs from cardRefs (or
// ComponentMounts when cardRefs is nil) but preserves entries with scope
// "builtin" (kind-config seeded bundles that serve as permission proxies).
// Used by deactivateMode to tear down a mode's required bundles without
// stripping permission granted via the agent kind config.
func (a *Actor) removeModeMountedCards(ids []string) bool {
	changed := false
	if a.cardRefs != nil {
		for _, id := range ids {
			for i := range a.cardRefs {
				if a.cardRefs[i].ID != id {
					continue
				}
				if a.cardRefs[i].Scope == "builtin" {
					continue
				}
				a.cardRefs = append(a.cardRefs[:i], a.cardRefs[i+1:]...)
				a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)
				changed = true
				break
			}
		}
	} else {
		for _, id := range ids {
			for i := range a.ComponentMounts {
				if a.ComponentMounts[i].CardID != id {
					continue
				}
				if a.ComponentMounts[i].Scope == "builtin" {
					continue
				}
				a.ComponentMounts = append(a.ComponentMounts[:i], a.ComponentMounts[i+1:]...)
				a.syncCanonicalCardRefs()
				changed = true
				break
			}
		}
	}
	return changed
}

// ensureModeActive mounts a mode card and all its required bundles. It reads
// the mode descriptor's Dependencies (requires list) and mounts each required
// bundle with scope "user" unless it is already mounted (e.g. via kind config
// as a permission proxy with scope "builtin"). The mode card itself is mounted
// with scope "user". If a conflicting flow mode is already active, it is
// deactivated first. Returns true if any mount state changed.
func (a *Actor) ensureModeActive(ctx actor.Context, modeCardID string) bool {
	descriptor, ok := a.componentDescriptor(ctx, modeCardID)
	if !ok {
		return false
	}
	changed := false
	if conflicting := a.conflictingFlowMode(modeCardID); conflicting != "" {
		if a.deactivateMode(ctx, conflicting) {
			changed = true
		}
	}
	for _, dep := range descriptor.Dependencies {
		if !dep.Required {
			continue
		}
		if a.ensureComponentCardMounted(ctx, dep.CardID, "user", true) {
			changed = true
		}
	}
	if a.ensureComponentCardMounted(ctx, modeCardID, "user", true) {
		changed = true
	}
	return changed
}

// deactivateMode unmounts a mode and its required bundles that were mounted as
// mode dependencies (scope "user" or "dependency"), but preserves bundles
// mounted with scope "builtin" (kind config / permission proxy). Returns true
// if any mount state changed.
func (a *Actor) deactivateMode(ctx actor.Context, modeCardID string) bool {
	descriptor, ok := a.componentDescriptor(ctx, modeCardID)
	ids := []string{modeCardID}
	if ok {
		for _, dep := range descriptor.Dependencies {
			if dep.Required {
				ids = append(ids, dep.CardID)
			}
		}
	}
	changed := a.removeModeMountedCards(ids)
	if changed {
		a.ComponentRevision++
		a.invalidateComponentSnapshot(ctx)
	}
	return changed
}

// cardIsMode reports whether a mounted card is a mode component. Modes are
// identified by their resolved descriptor kind ("mode"); builtin mode cards
// fall back to the builtin:mode: prefix when the descriptor cannot be resolved.
func (a *Actor) cardIsMode(ctx actor.Context, cardID string) bool {
	descriptor, ok := a.componentDescriptor(ctx, cardID)
	if !ok {
		return strings.HasPrefix(cardID, "builtin:mode:")
	}
	return descriptor.Ref.Kind == "mode"
}

// handleModesUnloadAll unmounts every currently-mounted mode card and clears
// the mode-derived session state, leaving the agent with no active mode. It is
// the scheduler execution path's entry point: the caller stops the current
// turn (turn_cancel) first, then this call tears down goal/workflow/memory/
// scheduler modes so a fresh mode can be mounted for the scheduled task.
//
// Each mode is removed via deactivateMode (mode card + its required non-builtin
// bundles) followed by onModeUnmounted (RawSession cleanup). Bundles mounted
// with scope "builtin" (kind config / permission proxy) are preserved, matching
// the deactivateMode contract. Returns the list of unmounted mode card IDs.
func (a *Actor) handleModesUnloadAll(ctx actor.Context, _ domain.AgentModesUnloadAllReq) (domain.AgentModesUnloadAllResp, error) {
	// Collect the mounted mode IDs first so we don't mutate while ranging.
	var modeIDs []string
	if a.cardRefs != nil {
		for _, ref := range a.cardRefs {
			if !ref.Disabled && a.cardIsMode(ctx, ref.ID) {
				modeIDs = append(modeIDs, ref.ID)
			}
		}
	} else {
		for _, mount := range a.ComponentMounts {
			if mount.Enabled && a.cardIsMode(ctx, mount.CardID) {
				modeIDs = append(modeIDs, mount.CardID)
			}
		}
	}
	for _, id := range modeIDs {
		a.deactivateMode(ctx, id)
		a.onModeUnmounted(ctx, id)
	}
	if len(modeIDs) > 0 {
		a.ComponentRevision++
		a.invalidateComponentSnapshot(ctx)
		a.stageToolsRefresh()
		a.saveMailbox(ctx)
		a.notifyWorkspaceStatus(ctx)
	}
	return domain.AgentModesUnloadAllResp{Unloaded: modeIDs}, nil
}

func (a *Actor) restoreComponentMountMetadata(ctx actor.Context) bool {
	changed := false
	now := time.Now().UTC().Format(time.RFC3339)
	if a.cardRefs != nil {
		legacy := make(map[string]domain.AgentComponentMount, len(a.ComponentMounts))
		for _, mount := range a.ComponentMounts {
			legacy[mount.CardID] = mount
		}
		a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)
		for i := range a.ComponentMounts {
			mount := &a.ComponentMounts[i]
			old := legacy[mount.CardID]
			mount.MountID = old.MountID
			mount.MountedAt = old.MountedAt
			mount.UpdatedAt = old.UpdatedAt
			descriptor, ok := a.componentDescriptor(ctx, mount.CardID)
			if ok {
				mount.Kind = descriptor.Ref.Kind
				mount.Title = descriptor.Title
				mount.Icon = descriptor.Icon
				mount.Visual = descriptor.Visual
			} else if isSkillCardID(mount.CardID) {
				mount.Kind = "skill"
				mount.Title = skillMountTitle(mount.CardID)
			}
			if mount.MountID == "" {
				mount.MountID = mount.CardID
			}
			if mount.MountedAt == "" {
				mount.MountedAt = now
			}
			if mount.UpdatedAt == "" {
				mount.UpdatedAt = now
			}
			if mount.Kind != old.Kind || mount.Title != old.Title || mount.Icon != old.Icon || mount.MountID != old.MountID || mount.MountedAt != old.MountedAt || mount.UpdatedAt != old.UpdatedAt {
				changed = true
			}
		}
		if changed {
			a.ComponentRevision++
			a.invalidateComponentSnapshot(ctx)
			a.saveMailbox(ctx)
		}
		return changed
	}

	for i := range a.ComponentMounts {
		mount := &a.ComponentMounts[i]
		if mount.Scope == "" {
			mount.Scope = "user"
			mount.UpdatedAt = now
			changed = true
		}
		descriptor, ok := a.componentDescriptor(ctx, mount.CardID)
		if !ok {
			// Skill mounts are not project components; ensure they still have
			// display metadata so the UI badge drawer shows them.
			if isSkillCardID(mount.CardID) {
				mountChanged := false
				if mount.Kind == "" {
					mount.Kind = "skill"
					mountChanged = true
				}
				if mount.Title == "" {
					mount.Title = skillMountTitle(mount.CardID)
					mountChanged = true
				}
				if mountChanged {
					mount.UpdatedAt = now
					changed = true
				}
			}
			continue
		}
		mountChanged := false
		if mount.Kind == "" && descriptor.Ref.Kind != "" {
			mount.Kind = descriptor.Ref.Kind
			mountChanged = true
		}
		if mount.Title == "" && descriptor.Title != "" {
			mount.Title = descriptor.Title
			mountChanged = true
		}
		if mount.Icon == "" && descriptor.Icon != "" {
			mount.Icon = descriptor.Icon
			mount.Visual = descriptor.Visual
			mountChanged = true
		}
		if mountChanged {
			mount.UpdatedAt = now
			changed = true
		}
	}
	if changed {
		a.ComponentRevision++
		a.invalidateComponentSnapshot(ctx)
	}
	return changed
}

// builtinCardsToSeed returns the builtin card IDs to mount for this agent.
// Tool bundles (including the debug/introspection bundle) are sourced from the
// agent kind config's DefaultBundleIDs plus the spawn-time extraBundleIDs
// (e.g. plugin-dev for agents in a dev-app project) — config is the single
// source of truth for which bundles an agent mounts. Read-only kinds
// (explorer/scout/dreamer/reviewer) never receive extra bundles: their turns
// run with DefaultBehavior "allow", so a mutating callable that reached their
// surface would execute without user confirmation. A spawn_assign child also
// seeds one conversable agent-chat:<parent> card so the parent-messaging tools
// reach its tool surface (the workspace's direct-child allowance authorizes
// the calls themselves); read-only kinds are excluded for the same
// mutation-safety reason as extra bundles.
func (a *Actor) builtinCardsToSeed(ctx actor.Context) []string {
	cfg := a.fetchAgentKindConfig(ctx)
	if cfg.Kind == "" {
		for _, defaultCfg := range agentkit.BaseKindConfigs() {
			if defaultCfg.Kind == a.agentKind {
				cfg = defaultCfg
				break
			}
		}
	}
	ids := make([]string, 0, len(cfg.DefaultBundleIDs)+len(a.extraBundleIDs))
	for _, id := range cfg.DefaultBundleIDs {
		// Dev-only bundles (coordinator-wearable) must not reach agents outside
		// dev builds even when a persisted kind config still lists them.
		if !buildinfo.IsDev() && agentkit.IsDevOnlyBundle(id) {
			continue
		}
		ids = append(ids, id)
	}
	if a.hasParentAgent() && !agentkit.IsReadOnlyAgentKind(a.agentKind) {
		ids = append(ids, agentChatCardPrefix+a.parentAgentID)
	}
	if !agentkit.IsReadOnlyAgentKind(a.agentKind) {
		for _, id := range a.extraBundleIDs {
			if !buildinfo.IsDev() && agentkit.IsDevOnlyBundle(id) {
				continue
			}
			dup := false
			for _, existing := range ids {
				if existing == id {
					dup = true
					break
				}
			}
			if !dup {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// seedScopeForCard returns the mount scope for a card being seeded:
// "project" for spawn-time extra bundles (attached because the agent lives in
// a dev-app project — they are not kind-config-driven and must survive
// reconcileBuiltinBundleMounts, including restores via timer-local spawn paths
// where ExtraBundleIDs is not re-sent), "builtin" for kind-config defaults.
func (a *Actor) seedScopeForCard(cardID string) string {
	for _, id := range a.extraBundleIDs {
		if id == cardID {
			return "project"
		}
	}
	return "builtin"
}

// canonicalCardRefs drops empty-ID refs and dedups by ID, preserving order.
// It cleans historical corruption (stale empty-ID refs, accidental duplicates)
// so cardRefs are always a clean, unique set.
func canonicalCardRefs(refs []gen.CardRef) []gen.CardRef {
	out := make([]gen.CardRef, 0, len(refs))
	seen := make(map[string]bool, len(refs))
	for _, ref := range refs {
		if ref.ID == "" || seen[ref.ID] {
			continue
		}
		seen[ref.ID] = true
		out = append(out, ref)
	}
	return out
}

// migrateRenamedBuiltinCards swaps mounts of retired builtin card IDs for
// their replacements (agentkit.BuiltinCardRenames) so agents persisted before
// a builtin card rename don't keep dangling mounts. If the replacement is
// already mounted, the stale entry is simply dropped.
func (a *Actor) migrateRenamedBuiltinCards(ctx actor.Context, now string) {
	if a.cardRefs != nil {
		refs, changed := migrateRenamedCardRefs(a.cardRefs)
		if !changed {
			return
		}
		a.cardRefs = refs
		a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)
		for i := range a.ComponentMounts {
			descriptor, ok := a.componentDescriptor(ctx, a.ComponentMounts[i].CardID)
			if ok {
				a.ComponentMounts[i].Kind = descriptor.Ref.Kind
				a.ComponentMounts[i].Title = descriptor.Title
				a.ComponentMounts[i].Icon = descriptor.Icon
				a.ComponentMounts[i].Visual = descriptor.Visual
			}
			a.ComponentMounts[i].MountedAt = now
			a.ComponentMounts[i].UpdatedAt = now
		}
		a.ComponentRevision++
		a.invalidateComponentSnapshot(ctx)
		return
	}
	mounts, changed := migrateRenamedComponentMounts(a.ComponentMounts, now)
	if !changed {
		return
	}
	a.ComponentMounts = mounts
	a.ComponentRevision++
	a.invalidateComponentSnapshot(ctx)
}

// migrateRenamedCardRefs rewrites refs whose card ID was renamed. A renamed
// ref whose replacement is already mounted — or whose replacement does not
// resolve to a builtin asset — is dropped. Pure; the caller rebuilds mount
// metadata for the surviving refs.
func migrateRenamedCardRefs(refs []gen.CardRef) ([]gen.CardRef, bool) {
	if len(agentkit.BuiltinCardRenames) == 0 {
		return refs, false
	}
	mounted := make(map[string]bool, len(refs))
	for _, ref := range refs {
		mounted[ref.ID] = true
	}
	changed := false
	kept := make([]gen.CardRef, 0, len(refs))
	for _, ref := range refs {
		newID, renamed := agentkit.BuiltinCardRenames[ref.ID]
		if !renamed {
			kept = append(kept, ref)
			continue
		}
		changed = true
		if mounted[newID] {
			continue
		}
		descriptor, ok := builtinDescriptorFromAsset(newID)
		if !ok {
			continue
		}
		kept = append(kept, gen.CardRef{ID: newID, Source: descriptor.Ref.Kind, Scope: ref.Scope})
		mounted[newID] = true
	}
	return kept, changed
}

// migrateRenamedComponentMounts is the legacy (ComponentMounts-only) variant
// of migrateRenamedCardRefs.
func migrateRenamedComponentMounts(mounts []domain.AgentComponentMount, now string) ([]domain.AgentComponentMount, bool) {
	if len(agentkit.BuiltinCardRenames) == 0 {
		return mounts, false
	}
	mounted := make(map[string]bool, len(mounts))
	for _, m := range mounts {
		mounted[m.CardID] = true
	}
	changed := false
	kept := make([]domain.AgentComponentMount, 0, len(mounts))
	for _, m := range mounts {
		newID, renamed := agentkit.BuiltinCardRenames[m.CardID]
		if !renamed {
			kept = append(kept, m)
			continue
		}
		changed = true
		if mounted[newID] {
			continue
		}
		descriptor, ok := builtinDescriptorFromAsset(newID)
		if !ok {
			continue
		}
		m.CardID = newID
		m.Kind = descriptor.Ref.Kind
		m.Title = descriptor.Title
		m.Icon = descriptor.Icon
		m.Visual = descriptor.Visual
		m.UpdatedAt = now
		kept = append(kept, m)
		mounted[newID] = true
	}
	return kept, changed
}

// seedBuiltinComponentMounts ensures all builtin protected component cards
// are mounted on agent startup. This makes the builtin card the single source
// of truth — resolveComponentSnapshot reads contributions from mounted cards
// rather than unconditionally injecting them.
func (a *Actor) seedBuiltinComponentMounts(ctx actor.Context) {
	now := time.Now().UTC().Format(time.RFC3339)
	a.migrateRenamedBuiltinCards(ctx, now)
	cardIDs := a.builtinCardsToSeed(ctx)
	if a.cardRefs != nil {
		mounted := make(map[string]bool, len(a.cardRefs))
		for _, ref := range a.cardRefs {
			mounted[ref.ID] = true
		}
		seeded := false
		for _, cardID := range cardIDs {
			if mounted[cardID] {
				continue
			}
			descriptor, ok := a.componentDescriptor(ctx, cardID)
			if !ok {
				continue
			}
			a.cardRefs = append(a.cardRefs, gen.CardRef{ID: cardID, Source: descriptor.Ref.Kind, Scope: a.seedScopeForCard(cardID)})
			mounted[cardID] = true
			seeded = true
		}
		if seeded {
			a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)
			for i := range a.ComponentMounts {
				descriptor, ok := a.componentDescriptor(ctx, a.ComponentMounts[i].CardID)
				if ok {
					a.ComponentMounts[i].Kind = descriptor.Ref.Kind
					a.ComponentMounts[i].Title = descriptor.Title
					a.ComponentMounts[i].Icon = descriptor.Icon
					a.ComponentMounts[i].Visual = descriptor.Visual
				}
				a.ComponentMounts[i].MountedAt = now
				a.ComponentMounts[i].UpdatedAt = now
			}
			a.ComponentRevision++
			a.invalidateComponentSnapshot(ctx)
		}
		return
	}
	mounted := make(map[string]bool, len(a.ComponentMounts))
	for _, m := range a.ComponentMounts {
		mounted[m.CardID] = true
	}
	seeded := false
	for _, cardID := range cardIDs {
		if mounted[cardID] {
			continue
		}
		descriptor, ok := a.componentDescriptor(ctx, cardID)
		if !ok {
			continue
		}
		a.ComponentMounts = append(a.ComponentMounts, domain.AgentComponentMount{
			MountID:   ctx.NewID().String(),
			CardID:    cardID,
			Enabled:   true,
			Scope:     a.seedScopeForCard(cardID),
			Kind:      descriptor.Ref.Kind,
			Title:     descriptor.Title,
			Icon:      descriptor.Icon,
			Visual:    descriptor.Visual,
			MountedAt: now,
			UpdatedAt: now,
		})
		seeded = true
	}
	if seeded {
		a.ComponentRevision++
		a.invalidateComponentSnapshot(ctx)
	}
}

// reconcileBuiltinBundleMounts drops builtin-seeded bundle mounts that are no
// longer listed in the kind config's DefaultBundleIDs. seedBuiltinComponentMounts
// only unions new defaults in, so without this prune a bundle the user removed
// in kind settings stays mounted on every existing agent forever and its tools
// keep reaching the runtime tool set (settings save appears ineffective).
// Mounts seeded by other paths are never touched: runtime user mounts
// (scope "user"), mode dependencies (scope "dependency"), kind-config card refs
// (skillScopeKindConfig), spawn-time app-tier bundles (scope "project"), and
// any card whose descriptor is unavailable.
func (a *Actor) reconcileBuiltinBundleMounts(ctx actor.Context) bool {
	cfg := a.fetchAgentKindConfig(ctx)
	if cfg.Kind == "" {
		return false
	}
	allowed := make(map[string]bool, len(cfg.DefaultBundleIDs))
	for _, id := range cfg.DefaultBundleIDs {
		if !buildinfo.IsDev() && agentkit.IsDevOnlyBundle(id) {
			continue
		}
		allowed[id] = true
	}
	stale := func(cardID string) bool {
		if allowed[cardID] {
			return false
		}
		descriptor, ok := a.componentDescriptor(ctx, cardID)
		if !ok {
			return false
		}
		return descriptor.Ref.Kind == "bundle"
	}
	changed := false
	if a.cardRefs != nil {
		kept := make([]gen.CardRef, 0, len(a.cardRefs))
		for _, ref := range a.cardRefs {
			if ref.Scope == "builtin" && stale(ref.ID) {
				changed = true
				continue
			}
			kept = append(kept, ref)
		}
		if !changed {
			return false
		}
		a.cardRefs = kept
		a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)
		for i := range a.ComponentMounts {
			descriptor, ok := a.componentDescriptor(ctx, a.ComponentMounts[i].CardID)
			if ok {
				a.ComponentMounts[i].Kind = descriptor.Ref.Kind
				a.ComponentMounts[i].Title = descriptor.Title
				a.ComponentMounts[i].Icon = descriptor.Icon
				a.ComponentMounts[i].Visual = descriptor.Visual
			}
		}
	} else {
		kept := make([]domain.AgentComponentMount, 0, len(a.ComponentMounts))
		for _, m := range a.ComponentMounts {
			if m.Scope == "builtin" && stale(m.CardID) {
				changed = true
				continue
			}
			kept = append(kept, m)
		}
		if !changed {
			return false
		}
		a.ComponentMounts = kept
	}
	a.ComponentRevision++
	a.invalidateComponentSnapshot(ctx)
	return true
}

func (a *Actor) resolveComponentSnapshot(ctx actor.Context) domain.AgentComponentSnapshot {
	if cached := a.componentSnapshot.Load(); cached != nil {
		// agent-chat tool contributions are never cached (liveness would
		// freeze at build time); patch them live on every read.
		return a.withLiveAgentChatTools(ctx, cloneComponentSnapshot(*cached))
	}
	snapshot := domain.AgentComponentSnapshot{
		Revision: a.ComponentRevision,
		TurnID:   a.status.TurnID,
		Mounts:   append([]domain.AgentComponentMount(nil), a.ComponentMounts...),
		Prompts:  []domain.ComponentPromptContribution{},
		Tools:    []domain.ComponentToolContribution{},
	}
	cfg := a.fetchAgentKindConfig(ctx)
	// Determine which fragment refs are already covered by mounted cards.
	// Profile refs (system/role) are always resolved via RPC — they are
	// identity config, not mountable components. Only fragments need
	// dedup against mounted cards.
	mountedPromptCards := make(map[string]bool)
	for _, m := range a.ComponentMounts {
		if m.Enabled && strings.HasPrefix(m.CardID, "prompt:") {
			mountedPromptCards[m.CardID] = true
		}
	}
	snapshot.Prompts = append(snapshot.Prompts, a.configuredPromptComponentsFiltered(ctx, cfg, mountedPromptCards)...)
	// Bound plugin/app system prompt overlays the builtin plugin-agent profile:
	// profile defines the generic contract, the app prompt specializes it. It is
	// persisted on the actor (agent_bind_app), not a kind config ref, because it
	// is per-app, not per-kind.
	if a.BoundAppID() != "" && a.BoundAppPrompt() != "" {
		snapshot.Prompts = append(snapshot.Prompts, domain.ComponentPromptContribution{
			ID:        "prompt:bound-app:" + a.BoundAppID(),
			Text:      a.BoundAppPrompt(),
			Title:     "Bound App Prompt",
			Placement: "system",
			Priority:  -900,
		})
	}
	mounts := append([]domain.AgentComponentMount(nil), a.ComponentMounts...)
	sort.SliceStable(mounts, func(i, j int) bool {
		if mounts[i].Order != mounts[j].Order {
			return mounts[i].Order < mounts[j].Order
		}
		return mounts[i].CardID < mounts[j].CardID
	})
	snapshot.Mounts = mounts
	seen := make(map[string]struct{}, len(mounts))
	for _, mount := range mounts {
		if !mount.Enabled {
			continue
		}
		if mount.Kind == "skill" || isSkillCardID(mount.CardID) {
			continue
		}
		// agent-chat cards skip the cached descriptor path: their tools are
		// live-resolved per read (withLiveAgentChatTools) so restart-time
		// LoadState="unloaded" targets never freeze the send-message tools
		// out of the snapshot.
		if strings.HasPrefix(mount.CardID, agentChatCardPrefix) {
			continue
		}
		a.appendComponentDescriptor(ctx, &snapshot, mount.CardID, seen)
	}
	// Builtin cards contribute their body directly from card frontmatter.
	a.annotateToolContributions(ctx, &snapshot)
	a.componentSnapshot.Store(&snapshot)
	return a.withLiveAgentChatTools(ctx, cloneComponentSnapshot(snapshot))
}

// annotateToolContributions verifies that every tool contribution in the
// snapshot can actually materialize as a ToolSpec on the agent's tool
// surface. A contribution whose CallableID matches no callable known to the
// topology or the agent-local fallbacks is silently dropped by
// ToolSpecsFromCallables; AdminOnly-registered callables are surfaced with a
// warning because their visibility is admin-scoped. Both cases append a
// ComponentDiagnostic so a "healthy-looking" snapshot cannot hide a broken
// tool surface.
func (a *Actor) annotateToolContributions(ctx actor.Context, snapshot *domain.AgentComponentSnapshot) {
	if len(snapshot.Tools) == 0 {
		return
	}
	universe := a.componentCallableUniverse()
	reported := make(map[string]struct{}, len(snapshot.Tools))
	for _, contribution := range snapshot.Tools {
		if contribution.CallableID == "" {
			continue
		}
		if _, dup := reported[contribution.CallableID]; dup {
			continue
		}
		ci, ok := universe[contribution.CallableID]
		if !ok {
			reported[contribution.CallableID] = struct{}{}
			snapshot.Diagnostics = append(snapshot.Diagnostics, domain.ComponentDiagnostic{
				CardID:  contribution.CardID,
				Level:   "error",
				Message: fmt.Sprintf("tool %q contributed by %q has no callable registration in the topology or agent-local fallbacks; it will not appear in the tool surface", contribution.CallableID, contribution.CardID),
				Reason:  "callable_not_found",
			})
			continue
		}
		if ci.Permission == "admin" {
			reported[contribution.CallableID] = struct{}{}
			snapshot.Diagnostics = append(snapshot.Diagnostics, domain.ComponentDiagnostic{
				CardID:  contribution.CardID,
				Level:   "warning",
				Message: fmt.Sprintf("tool %q contributed by %q is registered AdminOnly; it materializes but its callable is admin-scoped (frontend visibility admin)", contribution.CallableID, contribution.CardID),
				Reason:  "admin_visibility",
			})
		}
	}
}

// componentCallableUniverse returns every callable the agent's tool surface
// can resolve tool contributions against: topology callables plus the
// agent-local interaction and worktree fallbacks that resolveTools injects.
func (a *Actor) componentCallableUniverse() map[string]domain.CallableInterface {
	universe := a.callablesMap()
	if universe == nil {
		universe = make(map[string]domain.CallableInterface)
	}
	universe = ensureLocalInteractionCallables(universe)
	universe = ensureWorktreeCallables(universe)
	return universe
}

// invalidateComponentSnapshot clears the cached component snapshot and
// resolved instructions, then rebuilds the snapshot immediately so the pure
// (stateless) component.snapshot handler always returns fresh data without
// entering the owner queue. It also refreshes the prompt-artifact /
// compiled-prompt caches, since those embed tool and instruction projections
// derived from the component snapshot — without this, mounting a mode would
// leave the Prompt Context Snapshot stale until the next turn compile.
// Must be called on the cell goroutine (stateful mutation paths that already
// hold a valid actor.Context).
func (a *Actor) invalidateComponentSnapshot(ctx actor.Context) {
	a.componentSnapshot.Store(nil)
	a.resolvedInstructions.Store(nil)
	_ = a.resolveComponentSnapshot(ctx)
	a.refreshPromptCaches(ctx)
}

// stageToolsRefresh flags the active turn engine to re-resolve its tool
// surface at the next safe judgment window. Called from the component
// mount/unmount/set_enabled handlers so bundles mounted mid-turn contribute
// their tools to the remaining LLM dispatches of the same turn.
func (a *Actor) stageToolsRefresh() {
	a.pendingToolsRefresh.Store(true)
}

// AgentToolsRefreshNotifyReq is the request type for the tools_refresh_notify
// callable. Called by appmanager when plugins are registered, unregistered,
// or reloaded so the agent's tool surface stays in sync.
type AgentToolsRefreshNotifyReq struct {
	AppID  string `json:"AppId"`
	Reason string `json:"Reason,omitempty"`
}

// AgentToolsRefreshNotifyResp is the response type for tools_refresh_notify.
type AgentToolsRefreshNotifyResp struct{}

// handleToolsRefreshNotify is called by appmanager when the registered app
// list changes (register, unregister, reload). It flags the active turn engine
// to re-resolve its tool surface at the next safe judgment window.
func (a *Actor) handleToolsRefreshNotify(ctx actor.Context, req AgentToolsRefreshNotifyReq) (AgentToolsRefreshNotifyResp, error) {
	a.stageToolsRefresh()
	return AgentToolsRefreshNotifyResp{}, nil
}

func (a *Actor) appendComponentDescriptor(ctx actor.Context, snapshot *domain.AgentComponentSnapshot, cardID string, seen map[string]struct{}) {
	a.appendComponentDescriptorWithParent(ctx, snapshot, cardID, seen, "")
}

func (a *Actor) appendComponentDescriptorWithParent(ctx actor.Context, snapshot *domain.AgentComponentSnapshot, cardID string, seen map[string]struct{}, parentCardID string) {
	if _, ok := seen[cardID]; ok {
		return
	}
	seen[cardID] = struct{}{}
	component, ok := a.componentDescriptor(ctx, cardID)
	if !ok {
		snapshot.Diagnostics = append(snapshot.Diagnostics, domain.ComponentDiagnostic{
			CardID:  cardID,
			Level:   "error",
			Message: fmt.Sprintf("component %q could not be resolved", cardID),
		})
		return
	}
	// If this is a dependency (not directly mounted), add a virtual mount
	// entry so the frontend can display it. Virtual mounts have scope
	// "dependency" and are read-only.
	isDependency := parentCardID != ""
	if isDependency {
		// Check if it's already in snapshot.Mounts.
		alreadyInMounts := false
		for _, m := range snapshot.Mounts {
			if m.CardID == cardID {
				alreadyInMounts = true
				break
			}
		}
		if !alreadyInMounts {
			snapshot.Mounts = append(snapshot.Mounts, domain.AgentComponentMount{
				CardID:    cardID,
				Enabled:   true,
				Scope:     "dependency",
				MountedAt: "virtual",
			})
		}
	}
	for _, dependency := range component.Dependencies {
		if dependency.Required {
			a.appendComponentDescriptorWithParent(ctx, snapshot, dependency.CardID, seen, cardID)
		}
	}
	for i := range snapshot.Mounts {
		if snapshot.Mounts[i].CardID == cardID {
			snapshot.Mounts[i].Kind = component.Ref.Kind
			snapshot.Mounts[i].Version = component.Ref.Version
			snapshot.Mounts[i].Title = component.Title
			snapshot.Mounts[i].Icon = component.Icon
		}
	}
	snapshot.Prompts = append(snapshot.Prompts, component.Prompts...)
	snapshot.Tools = append(snapshot.Tools, component.Tools...)
}

func decodeComponentGet(result any) (domain.ComponentDescriptor, bool) {
	var response domain.ProjectComponentGetResp
	switch value := result.(type) {
	case domain.ProjectComponentGetResp:
		response = value
	case []byte:
		if json.Unmarshal(value, &response) != nil {
			return domain.ComponentDescriptor{}, false
		}
	case map[string]interface{}:
		payload, err := json.Marshal(value)
		if err != nil || json.Unmarshal(payload, &response) != nil {
			return domain.ComponentDescriptor{}, false
		}
	default:
		return domain.ComponentDescriptor{}, false
	}
	return response.Component, response.Component.Ref.CardID != ""
}

func cloneComponentSnapshot(snapshot domain.AgentComponentSnapshot) domain.AgentComponentSnapshot {
	snapshot.Mounts = append([]domain.AgentComponentMount(nil), snapshot.Mounts...)
	snapshot.Prompts = append([]domain.ComponentPromptContribution(nil), snapshot.Prompts...)
	snapshot.Tools = append([]domain.ComponentToolContribution(nil), snapshot.Tools...)
	return snapshot
}

func componentToolIDs(snapshot domain.AgentComponentSnapshot) []string {
	ids := make([]string, 0, len(snapshot.Tools))
	seen := make(map[string]struct{}, len(snapshot.Tools))
	for _, contribution := range snapshot.Tools {
		if contribution.CallableID == "" {
			continue
		}
		if _, ok := seen[contribution.CallableID]; ok {
			continue
		}
		seen[contribution.CallableID] = struct{}{}
		ids = append(ids, contribution.CallableID)
	}
	return ids
}

// componentStatusSection renders the live component status injected as the
// agent:component-status synth card (section "component_status", between
// skills and tool_guidance). Rendered only when the bundle-use capability is
// live on this agent — the snapshot must contribute both component_mount and
// component_list — so kinds that do not mount the bundle carry no section.
//
// It shows the two things the LLM cannot see without probing:
//   - every currently mounted component with enabled state, scope, and a
//     LOCKED marker for mounts that cannot be unmounted at runtime:
//     kind-config scope (pinned by the agent kind config file) or builtin
//     scope with a protected descriptor (system bundle).
//   - every catalog bundle not currently mounted, with its contributed tool
//     IDs, so the bundle-use retrieval step starts from live data instead of
//     a component_list call.
//
// The section is compiled at turn start; mounts performed mid-turn appear in
// it on the next turn (the tool surface itself refreshes mid-turn via
// pendingToolsRefresh).
func (a *Actor) componentStatusSection(ctx actor.Context, snapshot domain.AgentComponentSnapshot) string {
	var hasMount, hasList bool
	for _, t := range snapshot.Tools {
		switch t.CallableID {
		case "component_mount":
			hasMount = true
		case "component_list":
			hasList = true
		}
	}
	if !hasMount || !hasList || len(snapshot.Mounts) == 0 {
		return ""
	}

	protectedBuiltin, builtinBundles := builtinAssetIndex()

	mounted := make(map[string]bool, len(snapshot.Mounts))
	mountLines := make([]string, 0, len(snapshot.Mounts))
	for _, m := range snapshot.Mounts {
		if m.CardID == "" {
			continue
		}
		mounted[m.CardID] = true
		kind := m.Kind
		if kind == "" {
			kind = "component"
		}
		line := fmt.Sprintf("- %s [%s] scope=%s", m.CardID, kind, m.Scope)
		if !m.Enabled {
			line += " — disabled (silenced: contributes no tools or prompts)"
		}
		switch {
		case m.Scope == skillScopeKindConfig:
			line += " — LOCKED (kind-config: mounted via the agent kind config file; cannot be unmounted at runtime)"
		case m.Scope == "builtin" && protectedBuiltin[m.CardID]:
			line += " — LOCKED (protected builtin; cannot be unmounted)"
		}
		mountLines = append(mountLines, line)
	}

	var b strings.Builder
	b.WriteString("Currently mounted components (live snapshot):\n")
	b.WriteString(strings.Join(mountLines, "\n"))
	b.WriteString("\n\nLOCKED mounts cannot be removed with component_unmount; all others can. Use component_set_enabled to silence a mount without removing it.")

	available := a.availableBundleCatalog(ctx, builtinBundles, mounted)
	if len(available) > 0 {
		b.WriteString("\n\nAvailable bundles not currently mounted (inspect with project.component_get, mount with component_mount):\n")
		for _, d := range available {
			// agent-chat:* cards are per-target virtuals, not catalog items;
			// never offer them as mountable suggestions.
			if strings.HasPrefix(d.Ref.CardID, agentChatCardPrefix) {
				continue
			}
			line := "- " + d.Ref.CardID
			if d.Title != "" && d.Title != d.Ref.CardID {
				line += " — " + d.Title
			}
			if summary := bundleToolSummary(d); summary != "" {
				line += " (" + summary + ")"
			}
			b.WriteString(line + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// builtinAssetIndex returns the protected-flag set for builtin card IDs and
// bundle descriptors built from the embedded builtin card assets. One parse
// pass serves both the LOCKED determination for builtin-scope mounts and the
// local fallback catalog when the project catalog callable is unavailable.
func builtinAssetIndex() (protected map[string]bool, bundles []domain.ComponentDescriptor) {
	protected = make(map[string]bool)
	assets, err := agentkit.LoadBuiltinAssets()
	if err != nil {
		return protected, nil
	}
	for _, asset := range assets {
		if asset.Protected {
			protected[asset.Title] = true
		}
		if asset.Type != "bundle" {
			continue
		}
		descriptor := domain.ComponentDescriptor{
			Ref:    domain.ComponentRef{CardID: asset.Title, Kind: "bundle", Source: "builtin"},
			Title:  asset.BuiltinTitle,
			Icon:   asset.Icon,
			Visual: componentVisualFromAsset(asset.Visual),
		}
		for _, id := range asset.Tools {
			descriptor.Tools = append(descriptor.Tools, domain.ComponentToolContribution{
				ID: id, CardID: asset.Title, CallableID: id,
			})
		}
		for _, dep := range asset.Dependencies {
			descriptor.Dependencies = append(descriptor.Dependencies, domain.ComponentDependency{CardID: dep, Required: true})
		}
		bundles = append(bundles, descriptor)
	}
	return protected, bundles
}

// availableBundleCatalog returns kind=bundle catalog descriptors that are not
// in the mounted set, sorted by cardId. Primary source is the project's
// component_list callable (builtin + persisted project + external cards);
// when no parent/planner is reachable the embedded builtin bundles serve as
// a local fallback.
func (a *Actor) availableBundleCatalog(ctx actor.Context, builtinBundles []domain.ComponentDescriptor, mounted map[string]bool) []domain.ComponentDescriptor {
	var catalog []domain.ComponentDescriptor
	parent := ctx.Parent()
	planner := ctx.Planner()
	if parent != nil && planner != nil {
		callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
		result, err := planner.Call(callCtx, parent, "project.component_list", domain.ProjectComponentListReq{}).Await()
		cancel()
		if err == nil && result != nil {
			catalog = decodeComponentList(result)
		}
	}
	if len(catalog) == 0 {
		catalog = builtinBundles
	}
	// App bundles are virtual components served by appmanager (registry
	// projection). Merge them host-wide; virtual entries win over stale
	// materialized project cards sharing the same id.
	if descriptors := a.appBundleCatalog(ctx); len(descriptors) > 0 {
		byID := make(map[string]bool, len(descriptors))
		for _, d := range descriptors {
			byID[d.Ref.CardID] = true
		}
		merged := make([]domain.ComponentDescriptor, 0, len(catalog)+len(descriptors))
		for _, d := range catalog {
			if !byID[d.Ref.CardID] {
				merged = append(merged, d)
			}
		}
		catalog = append(merged, descriptors...)
	}

	available := make([]domain.ComponentDescriptor, 0, len(catalog))
	for _, d := range catalog {
		if d.Ref.Kind != "bundle" || d.Ref.CardID == "" || mounted[d.Ref.CardID] {
			continue
		}
		available = append(available, d)
	}
	sort.Slice(available, func(i, j int) bool { return available[i].Ref.CardID < available[j].Ref.CardID })
	return available
}

// appBundleCatalog fetches the host-wide virtual app-bundle catalog from
// appmanager. Empty on any failure: the catalog is additive, never fatal.
func (a *Actor) appBundleCatalog(ctx actor.Context) []domain.ComponentDescriptor {
	planner := ctx.Planner()
	if planner == nil {
		return nil
	}
	appmanagerRef, ok := ctx.LookupService("appmanager")
	if !ok {
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	result, err := planner.Call(callCtx, appmanagerRef, "appmanager.component_list", gen.AppManagerComponentListReq{}).Await()
	cancel()
	if err != nil || result == nil {
		return nil
	}
	if response, ok := result.(gen.AppManagerComponentListResp); ok {
		return response.Items
	}
	return decodeComponentList(result)
}

func decodeComponentList(result any) []domain.ComponentDescriptor {
	var response domain.ProjectComponentListResp
	switch value := result.(type) {
	case domain.ProjectComponentListResp:
		response = value
	case []byte:
		if json.Unmarshal(value, &response) != nil {
			return nil
		}
	case map[string]interface{}:
		payload, err := json.Marshal(value)
		if err != nil || json.Unmarshal(payload, &response) != nil {
			return nil
		}
	default:
		return nil
	}
	return response.Items
}

// bundleToolSummary renders a compact tool-ID preview for one bundle
// descriptor: at most five IDs plus an ellipsis marker.
func bundleToolSummary(d domain.ComponentDescriptor) string {
	if len(d.Tools) == 0 {
		return ""
	}
	const max = 5
	ids := make([]string, 0, max+1)
	for i, t := range d.Tools {
		if i >= max {
			ids = append(ids, "...")
			break
		}
		if t.CallableID != "" {
			ids = append(ids, t.CallableID)
		}
	}
	return strings.Join(ids, ", ")
}

func componentToolGuidance(snapshot domain.AgentComponentSnapshot) string {
	var sections []string
	for _, contribution := range snapshot.Tools {
		parts := make([]string, 0, 3)
		if contribution.Description != "" {
			parts = append(parts, "Description: "+contribution.Description)
		}
		if contribution.Usage != "" {
			parts = append(parts, "Usage: "+contribution.Usage)
		}
		if contribution.Constraints != "" {
			parts = append(parts, "Constraints: "+contribution.Constraints)
		}
		if len(parts) > 0 {
			sections = append(sections, fmt.Sprintf("Tool %s\n%s", contribution.CallableID, strings.Join(parts, "\n")))
		}
	}
	return strings.Join(sections, "\n\n")
}

func applyComponentToolMetadata(tools []domain.ToolSpec, snapshot domain.AgentComponentSnapshot) []domain.ToolSpec {
	byCallable := make(map[string]domain.ComponentToolContribution, len(snapshot.Tools))
	for _, contribution := range snapshot.Tools {
		if _, exists := byCallable[contribution.CallableID]; !exists {
			byCallable[contribution.CallableID] = contribution
		}
	}
	for i := range tools {
		contribution, ok := byCallable[tools[i].CallableID]
		if !ok {
			continue
		}
		if contribution.Name != "" {
			tools[i].Name = contribution.Name
		}
		if contribution.Description != "" {
			tools[i].Description = contribution.Description
		}
	}
	return tools
}
