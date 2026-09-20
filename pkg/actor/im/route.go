package im

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// newRouteID returns a short random id, e.g. "ir_1a2b3c4d".
func newRouteID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "ir_" + hex.EncodeToString(b[:])
}

// findRoute returns the index of the account's exclusive route mount, or -1.
// Mount semantics: each account carries at most one route (account→agent).
func findRoute(st imStore, accountID string) int {
	for i := range st.Routes {
		if st.Routes[i].AccountID == accountID {
			return i
		}
	}
	return -1
}

// accountMounted reports whether the account currently has an exclusive
// route mount.
func (a *Actor) accountMounted(accountID string) bool {
	st, err := a.loadStore()
	if err != nil {
		return false
	}
	return findRoute(st, accountID) >= 0
}

// ---------------------------------------------------------------------------
// Route callables (admin-gated: mounts decide which agent an account reaches)
// ---------------------------------------------------------------------------

func (a *Actor) handleRouteList(_ actor.PureContext, _ gen.ImRouteListReq) (gen.ImRouteListResp, error) {
	st, err := a.loadStore()
	if err != nil {
		return gen.ImRouteListResp{}, fmt.Errorf("im.route.list: load routes: %w", err)
	}
	items := make([]gen.ImRoute, 0, len(st.Routes))
	items = append(items, st.Routes...)
	return gen.ImRouteListResp{Items: items}, nil
}

// handleRouteSet mounts an account onto an agent exclusively. Conflicting
// mounts are rejected: an account already mounted on a different agent must
// unmount first, and an agent already occupied by another account cannot be
// taken. Remounting the same (account, agent) pair is idempotent — the route
// id is kept and the server re-stamps MountedAt (UTC ISO).
func (a *Actor) handleRouteSet(_ actor.PureContext, req gen.ImRouteSetReq) (gen.ImRouteSetResp, error) {
	if req.AccountID == "" || req.AgentActorID == "" {
		return gen.ImRouteSetResp{}, fmt.Errorf("im.route.set: account id and agent actor id are required")
	}
	st, err := a.loadStore()
	if err != nil {
		return gen.ImRouteSetResp{}, fmt.Errorf("im.route.set: load routes: %w", err)
	}
	if findAccount(st, req.AccountID) < 0 {
		return gen.ImRouteSetResp{}, fmt.Errorf("im.route.set: account %q not found", req.AccountID)
	}
	route := gen.ImRoute{
		ID:           newRouteID(),
		AccountID:    req.AccountID,
		AgentActorID: req.AgentActorID,
		MountedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	if idx := findRoute(st, req.AccountID); idx >= 0 {
		if st.Routes[idx].AgentActorID != req.AgentActorID {
			return gen.ImRouteSetResp{}, fmt.Errorf(
				"im.route.set: 账户已挂载 agent %s，请先卸载（im.route.delete）",
				st.Routes[idx].AgentActorID)
		}
		route.ID = st.Routes[idx].ID
		st.Routes[idx] = route
	} else {
		for i := range st.Routes {
			if st.Routes[i].AgentActorID == req.AgentActorID {
				return gen.ImRouteSetResp{}, fmt.Errorf(
					"im.route.set: agent 已被占用（账户 %s），请先卸载该账户",
					st.Routes[i].AccountID)
			}
		}
		st.Routes = append(st.Routes, route)
	}
	if err := a.saveStore(st); err != nil {
		return gen.ImRouteSetResp{}, fmt.Errorf("im.route.set: save routes: %w", err)
	}
	return gen.ImRouteSetResp{Route: route}, nil
}

func (a *Actor) handleRouteDelete(_ actor.PureContext, req gen.ImRouteDeleteReq) (gen.ImRouteDeleteResp, error) {
	if req.ID == "" {
		return gen.ImRouteDeleteResp{}, fmt.Errorf("im.route.delete: route id is required")
	}
	st, err := a.loadStore()
	if err != nil {
		return gen.ImRouteDeleteResp{}, fmt.Errorf("im.route.delete: load routes: %w", err)
	}
	idx := -1
	for i := range st.Routes {
		if st.Routes[i].ID == req.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return gen.ImRouteDeleteResp{}, fmt.Errorf("im.route.delete: route %q not found", req.ID)
	}
	st.Routes = append(st.Routes[:idx], st.Routes[idx+1:]...)
	if err := a.saveStore(st); err != nil {
		return gen.ImRouteDeleteResp{}, fmt.Errorf("im.route.delete: save routes: %w", err)
	}
	return gen.ImRouteDeleteResp{}, nil
}

// ---------------------------------------------------------------------------
// Mount reconciliation (cascade release)
// ---------------------------------------------------------------------------

// hasMounts reports whether any account→agent mount exists. The reply tick
// stays armed for mount reconciliation while true. A nil store (actor built
// without OnInit, e.g. registration-surface tests) means no mounts.
func (a *Actor) hasMounts() bool {
	if a.store == nil {
		return false
	}
	st, err := a.loadStore()
	if err != nil {
		return false
	}
	return len(st.Routes) > 0
}

// reconcileMounts is the tick-side cascade release: every mount whose agent
// no longer appears in the workspace agent list is deleted, so a re-created
// agent can be mounted again. Reconciliation failures are logged (long
// fields truncated) and never block the reply pipeline.
func (a *Actor) reconcileMounts(ctx actor.Context, state gen.WorkspaceAgentListState, stateOK bool) {
	st, err := a.loadStore()
	if err != nil {
		ctx.Logger().Warn("im: mount reconcile could not load store", "error", truncate(err.Error(), replyLogLimit))
		return
	}
	if len(st.Routes) == 0 {
		return
	}
	if !stateOK {
		// Fetch failure already logged at the call site; never block.
		return
	}
	alive := make(map[string]struct{}, len(state.Items))
	for i := range state.Items {
		if state.Items[i].ActorID != "" {
			alive[state.Items[i].ActorID] = struct{}{}
		}
	}
	kept := make([]gen.ImRoute, 0, len(st.Routes))
	for i := range st.Routes {
		route := st.Routes[i]
		if _, ok := alive[route.AgentActorID]; !ok {
			ctx.Logger().Info("im: mount released (agent gone)",
				"account", route.AccountID, "agent", truncate(route.AgentActorID, 64))
			continue
		}
		kept = append(kept, route)
	}
	if len(kept) == len(st.Routes) {
		return
	}
	st.Routes = kept
	if err := a.saveStore(st); err != nil {
		ctx.Logger().Warn("im: mount reconcile could not save store", "error", truncate(err.Error(), replyLogLimit))
	}
}

// releaseGoneAccountMount is the inbound-side cascade release: when the
// account is mounted and the mounted agent no longer exists in the workspace,
// the mount is deleted first so the message falls back to the coordinator
// default. A failed workspace lookup is logged (truncated) and never blocks
// the pipeline. Returns true when a stale mount was released.
func (a *Actor) releaseGoneAccountMount(ctx actor.Context, accountID string) bool {
	st, err := a.loadStore()
	if err != nil {
		ctx.Logger().Warn("im.inbound: mount check could not load store", "account", accountID,
			"error", truncate(err.Error(), replyLogLimit))
		return false
	}
	idx := findRoute(st, accountID)
	if idx < 0 {
		return false
	}
	mounted := st.Routes[idx].AgentActorID
	state, ok := fetchWorkspaceAgentState(ctx)
	if !ok {
		ctx.Logger().Warn("im.inbound: mount check could not fetch agent state", "account", accountID)
		return false
	}
	for i := range state.Items {
		if state.Items[i].ActorID == mounted {
			return false // agent still alive
		}
	}
	if _, err := a.handleRouteDelete(ctx, gen.ImRouteDeleteReq{ID: st.Routes[idx].ID}); err != nil {
		ctx.Logger().Warn("im.inbound: stale mount release failed", "account", accountID,
			"agent", truncate(mounted, 64), "error", truncate(err.Error(), replyLogLimit))
		return false
	}
	ctx.Logger().Info("im.inbound: released stale mount (agent gone)",
		"account", accountID, "agent", truncate(mounted, 64))
	return true
}

// ---------------------------------------------------------------------------
// Inbound routing
// ---------------------------------------------------------------------------

// resolveAgentActorID returns the agent actor id the account is mounted on.
// Accounts without a mount fall back to the workspace Coordinator so an
// allowlisted message still reaches an agent (the reply pipeline nudges the
// sender to /bind in that case).
func (a *Actor) resolveAgentActorID(ctx actor.Context, accountID string) (string, error) {
	st, err := a.loadStore()
	if err != nil {
		return "", fmt.Errorf("im: resolve route: load routes: %w", err)
	}
	if idx := findRoute(st, accountID); idx >= 0 {
		return st.Routes[idx].AgentActorID, nil
	}
	return lookupCoordinatorActorID(ctx)
}

// lookupCoordinatorActorID asks the workspace service for the live Coordinator
// actor id (workspace.coordinator_lookup loads — spawns — it when necessary).
func lookupCoordinatorActorID(ctx actor.Context) (string, error) {
	wsRef, ok := ctx.LookupService("workspace")
	if !ok {
		return "", fmt.Errorf("im: default route: workspace service not found")
	}
	planner := ctx.Planner()
	if planner == nil {
		return "", fmt.Errorf("im: default route: no planner capability")
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	result, err := planner.Call(callCtx, wsRef, "workspace.coordinator_lookup", nil).Await()
	if err != nil {
		return "", fmt.Errorf("im: default route: coordinator lookup: %w", err)
	}
	lookup, ok := decodeCoordinatorLookup(result)
	if !ok || !lookup.Found || lookup.ActorID == "" {
		return "", fmt.Errorf("im: default route: coordinator not found")
	}
	return lookup.ActorID, nil
}

// decodeCoordinatorLookup decodes a workspace.coordinator_lookup result that
// may arrive as the typed struct, raw JSON bytes, or a generic map.
func decodeCoordinatorLookup(result any) (gen.WorkspaceCoordinatorLookupResp, bool) {
	switch v := result.(type) {
	case gen.WorkspaceCoordinatorLookupResp:
		return v, true
	case []byte:
		var resp gen.WorkspaceCoordinatorLookupResp
		if err := json.Unmarshal(v, &resp); err != nil {
			return gen.WorkspaceCoordinatorLookupResp{}, false
		}
		return resp, true
	case map[string]interface{}:
		b, _ := json.Marshal(v)
		var resp gen.WorkspaceCoordinatorLookupResp
		if err := json.Unmarshal(b, &resp); err != nil {
			return gen.WorkspaceCoordinatorLookupResp{}, false
		}
		return resp, true
	default:
		return gen.WorkspaceCoordinatorLookupResp{}, false
	}
}

// ---------------------------------------------------------------------------
// Inbound slash-command parsing
// ---------------------------------------------------------------------------

// parseCommand extracts a leading slash command from an inbound message:
// "/bind agent-1" -> ("bind", "agent-1", true); "/status" -> ("status", "",
// true). The command token is lowercased and must directly follow the slash;
// the argument is the trimmed remainder with inner spacing preserved. Plain
// text returns ok=false; dispatching (and rejecting unknown commands) belongs
// to the caller.
func parseCommand(text string) (cmd string, arg string, ok bool) {
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, "/") {
		return "", "", false
	}
	rest := t[1:]
	if rest == "" || rest[0] == ' ' || rest[0] == '\t' {
		return "", "", false
	}
	fields := strings.Fields(rest)
	cmd = strings.ToLower(fields[0])
	if len(fields) > 1 {
		arg = strings.TrimSpace(rest[len(fields[0]):])
	}
	return cmd, arg, true
}
