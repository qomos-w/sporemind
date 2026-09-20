// Package im implements the instant-messaging gateway actor. It stores IM
// bot accounts (token persisted server-side, redacted in list/view
// projections), exclusive account→agent mount routes and the provider
// adapter registry; concrete provider adapters (telegram first) land in
// follow-up cards.
// Contract: schemas/im._3043.spore.
package im

import (
	"fmt"
	"sync"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// Actor manages IM gateway accounts, chat→agent routes and (in follow-up
// cards) provider connections.
type Actor struct {
	actor.Host
	store   persist.Persist
	actorID string

	// pendingReplies tracks in-flight inbound turns awaiting their agent
	// reply, keyed by TurnActorId. It is written by the owner-lane inbound
	// path (submitAgentTurn) and read/mutated by the dedicated "im_poll"
	// lane (handleReplyPollTick), so every access is guarded by replyMu.
	pendingReplies map[string]*pendingReply
	// replyMu guards pendingReplies across the owner and im_poll lanes. The
	// *pendingReply values are immutable once inserted, so a snapshot of the
	// map can be processed without holding the lock.
	replyMu sync.Mutex

	// mountMenus caches the numbered agent order rendered by the last /bind
	// menu per account, so "/bind <n>" resolves the number the sender saw.
	// Mutated only on the actor owner loop (Internal handlers); chat menus
	// are ephemeral and deliberately not persisted.
	mountMenus map[string][]string
}

// Type returns the actor type identifier.
func (a *Actor) Type() string { return "im" }

// OnInit opens the persistence store.
func (a *Actor) OnInit(ctx actor.Context) error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("im"))
		if err != nil {
			return err
		}
	}
	a.actorID = ctx.Self().ID().String()
	if a.pendingReplies == nil {
		a.pendingReplies = map[string]*pendingReply{}
	}
	if a.mountMenus == nil {
		a.mountMenus = map[string][]string{}
	}
	return nil
}

// OnStart registers callables. Every im.* callable derives its ServiceName
// "im" at Register time from the callID's first segment matching the
// registered domain (RegisterDomain("im")), so no per-callable WithService
// is needed. Account configuration and route callables are admin-gated (bot
// tokens are sensitive configuration; routing decides which agent sees a
// chat).
func (a *Actor) OnStart(ctx actor.Context) error {
	// Reply-poll lane: im.reply_poll_tick awaits workspace.agent_list_state
	// (and per-agent session_summary) every tick, so it must not occupy the
	// owner lane (Owner Lane 禁阻塞 red line); the self-rearming tick is
	// routed to a dedicated stateful lane. Shared state with the owner lane
	// (pendingReplies) is replyMu-guarded. Mirrors agent_exec (agent.go:737).
	if err := ctx.RegisterLoop(imPollLoop, actor.ModeStateful); err != nil {
		return fmt.Errorf("im: register reply poll loop: %w", err)
	}
	if err := ctx.Register("im.account.list", a.handleAccountList, actor.AdminOnly()); err != nil {
		return fmt.Errorf("im: register account.list: %w", err)
	}
	if err := ctx.Register("im.account.create", a.handleAccountCreate, actor.AdminOnly()); err != nil {
		return fmt.Errorf("im: register account.create: %w", err)
	}
	if err := ctx.Register("im.account.update", a.handleAccountUpdate, actor.AdminOnly()); err != nil {
		return fmt.Errorf("im: register account.update: %w", err)
	}
	if err := ctx.Register("im.account.delete", a.handleAccountDelete, actor.AdminOnly()); err != nil {
		return fmt.Errorf("im: register account.delete: %w", err)
	}
	if err := ctx.Register("im.route.list", a.handleRouteList, actor.AdminOnly()); err != nil {
		return fmt.Errorf("im: register route.list: %w", err)
	}
	if err := ctx.Register("im.route.set", a.handleRouteSet, actor.AdminOnly()); err != nil {
		return fmt.Errorf("im: register route.set: %w", err)
	}
	if err := ctx.Register("im.route.delete", a.handleRouteDelete, actor.AdminOnly()); err != nil {
		return fmt.Errorf("im: register route.delete: %w", err)
	}
	if err := ctx.Register("im.status", a.handleStatus, actor.Public()); err != nil {
		return fmt.Errorf("im: register status: %w", err)
	}
	if err := ctx.Register("im.send", a.handleSend, actor.Public()); err != nil {
		return fmt.Errorf("im: register send: %w", err)
	}
	// Internal reply-pipeline callables: im.inbound is the entry point the
	// provider receive loop delivers allowed messages to; im.reply_poll_tick
	// is the self-rearming turn-completion watcher (armed on demand while
	// replies are pending) and runs on the "im_poll" lane.
	if err := ctx.Register(callableInbound, a.handleInbound, actor.Internal()); err != nil {
		return fmt.Errorf("im: register inbound: %w", err)
	}
	if err := ctx.Register(callableReplyPollTick, a.handleReplyPollTick, actor.Internal(), actor.WithLoop(imPollLoop)); err != nil {
		return fmt.Errorf("im: register reply poll tick: %w", err)
	}

	if err := ctx.RegisterDomain(ServiceName).Expose(); err != nil {
		return fmt.Errorf("im: expose service: %w", err)
	}
	// Mounts restored from persistence keep the tick armed after a restart:
	// the reply-pipeline tick doubles as the mount reconciler (cascade
	// release) even with no pending replies.
	if a.hasMounts() {
		a.scheduleReplyPollTick(ctx)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Persistence
// ---------------------------------------------------------------------------

// imStore is the on-disk shape: the list of IM bot accounts plus the
// account→agent mount table (routes live alongside the accounts they
// reference). Serialized via json.Marshal (PascalCase tags).
type imStore struct {
	Accounts []gen.ImAccount `json:"Accounts"`
	Routes   []gen.ImRoute   `json:"Routes"`
}

func (a *Actor) loadStore() (imStore, error) {
	var st imStore
	if err := persist.LoadOrZero(a.store, a.actorID, &st); err != nil {
		return imStore{}, err
	}
	// Converge legacy multi-chat routing data to the exclusive mount shape
	// (at most one route per account) so stale duplicates never trip the
	// mount mutexes; the converged form persists on the next save.
	st.Routes = convergeRoutes(st.Routes)
	return st, nil
}

// convergeRoutes keeps only the newest route per account, dropping the rest
// silently (legacy stores may predate exclusive mounts). "Newest" is by
// MountedAt; when MountedAt is absent or unparseable the insertion order
// decides — the later entry wins.
func convergeRoutes(routes []gen.ImRoute) []gen.ImRoute {
	out := make([]gen.ImRoute, 0, len(routes))
	pos := make(map[string]int, len(routes)) // AccountID → index in out
	for i := range routes {
		account := routes[i].AccountID
		if j, ok := pos[account]; ok {
			if routeNewer(routes[i], out[j]) {
				out[j] = routes[i]
			}
			continue
		}
		pos[account] = len(out)
		out = append(out, routes[i])
	}
	return out
}

// routeNewer reports whether candidate supersedes cur as the account's mount.
func routeNewer(candidate, cur gen.ImRoute) bool {
	c, cerr := parseMountedAt(candidate.MountedAt)
	k, kerr := parseMountedAt(cur.MountedAt)
	if cerr != nil || kerr != nil {
		return true // no comparable timestamps: insertion order decides
	}
	return c.After(k)
}

// parseMountedAt parses a server-stamped UTC ISO mount time.
func parseMountedAt(s string) (time.Time, error) {
	return time.Parse(time.RFC3339, s)
}

func (a *Actor) saveStore(st imStore) error {
	return a.store.Save(a.actorID, st)
}

// ---------------------------------------------------------------------------
// Callable handlers (non-account)
// ---------------------------------------------------------------------------

// handleStatus reports live connection state for all accounts. Until provider
// adapters land, every enabled account reports "disconnected".
func (a *Actor) handleStatus(_ actor.PureContext, _ gen.ImStatusReq) (gen.ImStatusResp, error) {
	st, err := a.loadStore()
	if err != nil {
		return gen.ImStatusResp{}, fmt.Errorf("im.status: load accounts: %w", err)
	}
	items := make([]gen.ImAccountView, 0, len(st.Accounts))
	for i := range st.Accounts {
		view := accountView(st.Accounts[i])
		if st.Accounts[i].Enabled {
			view.Status = statusDisconnected
		}
		items = append(items, view)
	}
	return gen.ImStatusResp{Items: items}, nil
}

// handleSend is the outbound push stub; real delivery arrives with the
// provider adapters.
func (a *Actor) handleSend(_ actor.PureContext, req gen.ImSendReq) (gen.ImSendResp, error) {
	if req.AccountID == "" || req.ChatID == "" || req.Text == "" {
		return gen.ImSendResp{}, fmt.Errorf("im.send: account id, chat id and text are required")
	}
	return gen.ImSendResp{}, errSendNotImplemented
}
