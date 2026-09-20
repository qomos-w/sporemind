package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// systemWikiForwardTimeout caps the wait for a forwarded system.wiki RPC.
// Kept modest because every forward is simply a sequential cross-actor call the
// caller waits on: a long cap here directly extends how long one stuck call
// delays the whole response (the handlers take `actor.PureContext`, so they run
// on a forked goroutine and never serialize on the workspace owner lane —
// Owner Lane 禁阻塞; see gospore ARCHITECTURE.md on PureContext execution).
// Normal wiki ops are millisecond-scale; 5s tolerates transient project-loop
// contention without letting one slow forward dominate a multi-mount fan-out.
const systemWikiForwardTimeout = 5 * time.Second

// systemWikiForwardRetries tolerates the system meta project's spawn window:
// LookupID can resolve the ref before the actor's OnStart finished registering
// its project.wiki_* callables, surfacing as gospore.cell "call ID not
// registered". gospore's batch-start wait (app.Run) caps at 10s — after that
// the gateway opens even if some cell is still mid-OnStart — so the retry
// budget covers the same 10s: 21 attempts (1 + 20 retries) × 500ms. Each
// attempt runs on a PureContext fork (never the owner lane), so the wait
// never blocks the workspace.
const (
	systemWikiForwardRetries    = 20
	systemWikiForwardRetryDelay = 500 * time.Millisecond
)

func isCallNotRegisteredErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not registered")
}

// forwardSystemWiki invokes a project.wiki.* callable on the system meta
// project actor (the workspace's built-in project that holds global/builtin
// cards such as skill-creator) and returns its raw response.
func (a *Actor) forwardSystemWiki(ctx actor.PureContext, callable string, req any) (any, error) {
	sysID := a.systemMetaProjectActorID()
	if sysID == "" {
		return nil, fmt.Errorf("system.wiki: system meta project not available")
	}
	cid, err := identity.ParseCanonicalID(sysID)
	if err != nil {
		return nil, fmt.Errorf("system.wiki: invalid system meta project id: %w", err)
	}
	projRef, ok := ctx.LookupID(id.From(cid))
	if !ok {
		return nil, fmt.Errorf("system.wiki: system meta project actor not running")
	}
	var lastErr error
	for attempt := 0; attempt <= systemWikiForwardRetries; attempt++ {
		forwardCtx, cancel := context.WithTimeout(ctx.Lifecycle(), systemWikiForwardTimeout)
		call := projRef.Invoke(forwardCtx, callable, req)
		if call == nil {
			cancel()
			return nil, fmt.Errorf("system.wiki: forward %s returned nil call", callable)
		}
		raw, err := call.Final(forwardCtx)
		call.Close()
		cancel()
		if err == nil {
			return raw, nil
		}
		lastErr = err
		if attempt == systemWikiForwardRetries || !isCallNotRegisteredErr(err) {
			break
		}
		select {
		case <-ctx.Lifecycle().Done():
			return nil, fmt.Errorf("system.wiki: forward %s: %w", callable, lastErr)
		case <-time.After(systemWikiForwardRetryDelay):
		}
	}
	return nil, fmt.Errorf("system.wiki: forward %s: %w", callable, lastErr)
}

func (a *Actor) handleSystemWikiListCards(ctx actor.PureContext, req domain.WikiListCardsReq) (domain.WikiListCardsResp, error) {
	raw, err := a.forwardSystemWiki(ctx, "project.wiki_list_cards", req)
	if err != nil {
		return domain.WikiListCardsResp{}, err
	}
	resp, ok := raw.(domain.WikiListCardsResp)
	if !ok {
		return domain.WikiListCardsResp{}, fmt.Errorf("system.wiki.list_cards: unexpected response type")
	}
	return resp, nil
}

// projectWikiForwardWhitelist enumerates the read-only project.wiki_*
// callables workspace.project_wiki_forward may carry (the wiki.read host-call
// set). The forward never accepts mutating project callables.
var projectWikiForwardWhitelist = map[string]bool{
	"project.wiki_get_card":             true,
	"project.wiki_get_card_hierarchy":   true,
	"project.wiki_get_cards_batch":      true,
	"project.wiki_get_concept_tree":     true,
	"project.wiki_list_cards":           true,
	"project.wiki_search_card_content":  true,
}

// projectActorIDStrict resolves a project by exact actor ID or mount name —
// no first-mount fallback. The plugin wiki forward must never silently land
// on a different project than the one the plugin is bound to.
func (a *Actor) projectActorIDStrict(projectID string) (string, error) {
	if projectID == "" {
		return "", fmt.Errorf("project id is empty")
	}
	a.mountMu.RLock()
	defer a.mountMu.RUnlock()
	for _, m := range a.Mounts {
		if m.ActorID == projectID {
			return m.ActorID, nil
		}
	}
	for _, m := range a.Mounts {
		if strings.EqualFold(m.Name, projectID) {
			return m.ActorID, nil
		}
	}
	return "", fmt.Errorf("project %q not found", projectID)
}

// handleProjectWikiForward implements workspace.project_wiki_forward: it
// routes one whitelisted read-only project.wiki_* callable to the named
// project's actor and returns its raw JSON response. Called by the
// pluginhost host bridge for a plugin's wiki.read host calls — the plugin's
// project comes from its OnLoad config (appmanager), and the project actor
// itself validates the request exactly as it would for an agent tool call.
// Role-gated like workspace.create_app_agent: only the appmanager/pluginhost
// system surface may invoke it.
func (a *Actor) handleProjectWikiForward(ctx actor.PureContext, req domain.ProjectWikiForwardReq) (domain.ProjectWikiForwardResp, error) {
	if role := ctx.Identity().Role; role != "system" && role != "admin" {
		return domain.ProjectWikiForwardResp{}, fmt.Errorf("project.wiki.forward: forbidden: requires system or admin role, got %q", role)
	}
	if !projectWikiForwardWhitelist[req.Callable] {
		return domain.ProjectWikiForwardResp{}, fmt.Errorf("project.wiki.forward: callable %q is not a whitelisted read-only project.wiki_* call", req.Callable)
	}
	actorID, err := a.projectActorIDStrict(req.ProjectID)
	if err != nil {
		return domain.ProjectWikiForwardResp{}, fmt.Errorf("project.wiki.forward: %w", err)
	}
	cid, err := identity.ParseCanonicalID(actorID)
	if err != nil {
		return domain.ProjectWikiForwardResp{}, fmt.Errorf("project.wiki.forward: invalid project actor id: %w", err)
	}
	projRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projRef == nil {
		return domain.ProjectWikiForwardResp{}, fmt.Errorf("project.wiki.forward: project actor for %q not running", req.ProjectID)
	}
	forwardCtx, cancel := context.WithTimeout(ctx.Lifecycle(), systemWikiForwardTimeout)
	defer cancel()
	call := projRef.Invoke(forwardCtx, req.Callable, req.Payload)
	if call == nil {
		return domain.ProjectWikiForwardResp{}, fmt.Errorf("project.wiki.forward: forward %s returned nil call", req.Callable)
	}
	defer call.Close()
	raw, err := call.Final(forwardCtx)
	if err != nil {
		return domain.ProjectWikiForwardResp{}, fmt.Errorf("project.wiki.forward: forward %s: %w", req.Callable, err)
	}
	switch v := raw.(type) {
	case []byte:
		return domain.ProjectWikiForwardResp{Result: json.RawMessage(v)}, nil
	case json.RawMessage:
		return domain.ProjectWikiForwardResp{Result: v}, nil
	default:
		encoded, merr := json.Marshal(raw)
		if merr != nil {
			return domain.ProjectWikiForwardResp{}, fmt.Errorf("project.wiki.forward: encode %s response: %w", req.Callable, merr)
		}
		return domain.ProjectWikiForwardResp{Result: encoded}, nil
	}
}

func (a *Actor) handleSystemWikiSearchCardContent(ctx actor.PureContext, req domain.WikiSearchCardContentReq) (domain.WikiSearchCardContentResp, error) {
	raw, err := a.forwardSystemWiki(ctx, "project.wiki_search_card_content", req)
	if err != nil {
		return domain.WikiSearchCardContentResp{}, err
	}
	resp, ok := raw.(domain.WikiSearchCardContentResp)
	if !ok {
		return domain.WikiSearchCardContentResp{}, fmt.Errorf("system.wiki.search_card_content: unexpected response type")
	}
	return resp, nil
}

func (a *Actor) handleSystemWikiGetCard(ctx actor.PureContext, req domain.WikiGetCardReq) (domain.WikiGetCardResp, error) {
	raw, err := a.forwardSystemWiki(ctx, "project.wiki_get_card", req)
	if err != nil {
		return domain.WikiGetCardResp{}, err
	}
	resp, ok := raw.(domain.WikiGetCardResp)
	if !ok {
		return domain.WikiGetCardResp{}, fmt.Errorf("system.wiki.get_card: unexpected response type")
	}
	return resp, nil
}

func (a *Actor) handleSystemWikiCreateCard(ctx actor.PureContext, req domain.WikiCreateCardReq) (domain.WikiCreateCardResp, error) {
	raw, err := a.forwardSystemWiki(ctx, "project.wiki_create_card", req)
	if err != nil {
		return domain.WikiCreateCardResp{}, err
	}
	resp, ok := raw.(domain.WikiCreateCardResp)
	if !ok {
		return domain.WikiCreateCardResp{}, fmt.Errorf("system.wiki.create_card: unexpected response type")
	}
	return resp, nil
}

func (a *Actor) handleSystemWikiEditCard(ctx actor.PureContext, req domain.WikiEditCardReq) (domain.WikiEditCardResp, error) {
	raw, err := a.forwardSystemWiki(ctx, "project.wiki_edit_card", req)
	if err != nil {
		return domain.WikiEditCardResp{}, err
	}
	resp, ok := raw.(domain.WikiEditCardResp)
	if !ok {
		return domain.WikiEditCardResp{}, fmt.Errorf("system.wiki.edit_card: unexpected response type")
	}
	return resp, nil
}

func (a *Actor) handleSystemWikiDeleteCard(ctx actor.PureContext, req domain.WikiDeleteCardReq) (domain.WikiDeleteCardResp, error) {
	raw, err := a.forwardSystemWiki(ctx, "project.wiki_delete_card", req)
	if err != nil {
		return domain.WikiDeleteCardResp{}, err
	}
	resp, ok := raw.(domain.WikiDeleteCardResp)
	if !ok {
		return domain.WikiDeleteCardResp{}, fmt.Errorf("system.wiki.delete_card: unexpected response type")
	}
	return resp, nil
}

func (a *Actor) handleSystemComponentGet(ctx actor.PureContext, req domain.ProjectComponentGetReq) (domain.ProjectComponentGetResp, error) {
	raw, err := a.forwardSystemWiki(ctx, "project.component_get", req)
	if err != nil {
		return domain.ProjectComponentGetResp{}, err
	}
	resp, ok := raw.(domain.ProjectComponentGetResp)
	if !ok {
		return domain.ProjectComponentGetResp{}, fmt.Errorf("system.component.get: unexpected response type")
	}
	return resp, nil
}
