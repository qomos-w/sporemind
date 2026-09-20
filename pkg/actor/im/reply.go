package im

// Reply pipeline: one inbound InboundMessage flows through command
// interception (/bind mount menu, /unmount, /status), chat→agent routing
// (route.go), submission to the routed agent's chat_submit, and a polling
// turn-completion watcher that finally pushes the assistant text back through
// the provider adapter.
//
// Turn-completion sensing is poll-based (no event subscription from the im
// actor): while replies are pending, a self-rearming ctx.After tick calls
// workspace.agent_list_state and watches the agent Runtime.State transition
// running→completed/failed. Once terminal, the agent's session_summary is
// pulled and the last assistant text step of the submitted turn becomes the
// outbound reply (markdown simplified, hard-capped at 4096 runes with a
// visible truncation marker). The same tick reconciliates the mount table:
// mounts whose agent vanished from the workspace are released (cascade
// release), and the inbound path double-checks a mounted agent before
// submitting so a stale mount falls back to the coordinator.
//
// Log hygiene: every upstream response fragment or long text that reaches a
// log line goes through truncate(…, replyLogLimit) (the respSnippet 512-char
// convention) before crossing the actor boundary.

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/spore/identity"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// callableInbound / callableReplyPollTick are internal self-invocation ids.
const (
	callableInbound       = "im.inbound"
	callableReplyPollTick = "im.reply_poll_tick"
)

// imPollLoop is the dedicated stateful lane the self-rearming reply poll tick
// runs on (declared in OnStart). It keeps the cross-actor workspace /
// session_summary Await off the owner lane.
const imPollLoop = "im_poll"

const (
	// replyPollInterval spaces consecutive workspace.agent_list_state polls
	// while at least one reply is pending.
	replyPollInterval = 3 * time.Second
	// replyWaitDeadline bounds how long a pending reply keeps polling before
	// giving up with a timeout notice (long agent turns must not poll
	// forever).
	replyWaitDeadline = 30 * time.Minute
	// replyInvokeTimeout caps one cross-actor invoke (chat_submit /
	// session_summary / agent_list_state).
	replyInvokeTimeout = 30 * time.Second
	// replyLogLimit is the max length of any upstream fragment carried into
	// a log line (respSnippet convention).
	replyLogLimit = 512

	// maxReplyRunes is the outbound reply cap (Telegram sendMessage rejects
	// bodies over 4096 characters). The marker must fit inside the budget.
	maxReplyRunes  = 4096
	replyTruncMark = "…(truncated)"

	// Agent runtime/step vocabulary (mirrors agent status strings).
	runtimeRunning = "running"
	runtimeWaiting = "waiting"
	runtimePaused  = "paused"
	runtimeFailed  = "failed"
	runtimeCancel  = "cancelled"

	roleAssistant = "assistant"
	typeText      = "text"
)

// pendingReply tracks one in-flight inbound message whose agent turn has been
// submitted but not yet answered. Keyed by the submitted TurnActorId in
// Actor.pendingReplies. handleInbound (owner lane) inserts, handleReplyPollTick
// (the "im_poll" lane) removes; access is replyMu-guarded and the value is
// immutable once inserted.
type pendingReply struct {
	AccountID    string
	ChatID       string
	AgentActorID string
	TurnID       string
	SubmittedAt  time.Time
}

// handleInbound is the entry point of the reply pipeline for one allowed
// inbound message (the provider adapter already applied the account
// allowlist and rate limit). Command texts are intercepted here; plain text
// is routed to the chat's agent (bound route or coordinator default) and
// submitted as a chat_submit turn whose completion is then polled.
func (a *Actor) handleInbound(ctx actor.Context, msg InboundMessage) error {
	if msg.AccountID == "" || msg.ChatID == "" || strings.TrimSpace(msg.Text) == "" {
		return nil
	}
	st, err := a.loadStore()
	if err != nil {
		return fmt.Errorf("im.inbound: load store: %w", err)
	}
	idx := findAccount(st, msg.AccountID)
	if idx < 0 {
		ctx.Logger().Warn("im.inbound: unknown account, dropping message", "account", msg.AccountID)
		return nil
	}
	acc := st.Accounts[idx]
	if !acc.Enabled {
		return nil
	}

	if cmd, arg, ok := parseCommand(msg.Text); ok {
		return a.handleInboundCommand(ctx, acc, msg, cmd, arg)
	}
	return a.submitAgentTurn(ctx, acc, msg)
}

// handleInboundCommand dispatches the supported slash commands. /bind without
// arguments answers the numbered mount menu (workspace agents minus occupied
// ones); "/bind <编号或 id>" mounts (set's mutual-exclusion errors are echoed
// verbatim); /unmount releases the account's mount; /status answers an
// account + routing summary; anything else gets a usage notice. All answers
// leave via provider.Send.
func (a *Actor) handleInboundCommand(ctx actor.Context, acc gen.ImAccount, msg InboundMessage, cmd, arg string) error {
	switch cmd {
	case "bind":
		if arg == "" {
			a.answerBindMenu(ctx, acc, msg.ChatID)
			return nil
		}
		target := arg
		if _, err := identity.ParseCanonicalID(arg); err != nil {
			// Not a canonical agent id — maybe a number from the last menu.
			menu := a.menuFor(acc.ID)
			n, nerr := strconv.Atoi(arg)
			if nerr != nil {
				a.replyRaw(ctx, acc, msg.ChatID, "绑定失败：agent actor id 无效")
				return nil
			}
			if n < 1 || n > len(menu) {
				a.replyRaw(ctx, acc, msg.ChatID, "编号无效，请先发送 /bind 查看挂载列表")
				return nil
			}
			target = menu[n-1]
		}
		if _, err := a.handleRouteSet(ctx, gen.ImRouteSetReq{
			AccountID:    acc.ID,
			AgentActorID: target,
		}); err != nil {
			ctx.Logger().Warn("im.inbound: /bind failed", "chat", msg.ChatID, "error", err)
			a.replyRaw(ctx, acc, msg.ChatID, "绑定失败："+truncate(err.Error(), 200))
			return nil
		}
		a.replyRaw(ctx, acc, msg.ChatID, "✅ 已挂载 agent："+target)
		return nil
	case "unmount":
		a.answerUnmount(ctx, acc, msg.ChatID)
		return nil
	case "status":
		a.replyRaw(ctx, acc, msg.ChatID, a.statusSummary(acc))
		return nil
	default:
		a.replyRaw(ctx, acc, msg.ChatID, "未知命令。可用命令：/bind <编号或 id>、/unmount、/status")
		return nil
	}
}

// answerBindMenu renders the /bind mount menu: the workspace agents not
// occupied by any mount, numbered, plus the usage hint. The rendered order is
// cached per account so a follow-up "/bind <n>" resolves the number the
// sender saw (mount changes are still enforced by route.set's mutexes).
func (a *Actor) answerBindMenu(ctx actor.Context, acc gen.ImAccount, chatID string) {
	st, err := a.loadStore()
	if err != nil {
		ctx.Logger().Warn("im.inbound: /bind menu could not load store", "error", truncate(err.Error(), replyLogLimit))
		a.replyRaw(ctx, acc, chatID, "⚠️ 挂载列表读取失败，请稍后再试")
		return
	}
	state, ok := fetchWorkspaceAgentState(ctx)
	if !ok {
		a.replyRaw(ctx, acc, chatID, "⚠️ 挂载列表获取失败，请稍后再试")
		return
	}
	occupied := make(map[string]struct{}, len(st.Routes))
	for i := range st.Routes {
		occupied[st.Routes[i].AgentActorID] = struct{}{}
	}
	var menu []string
	var b strings.Builder
	b.WriteString("🔌 可挂载 agent：\n")
	for i := range state.Items {
		item := &state.Items[i]
		if item.ActorID == "" {
			continue
		}
		if _, busy := occupied[item.ActorID]; busy {
			continue
		}
		menu = append(menu, item.ActorID)
		label := item.DisplayName
		if label == "" {
			label = item.AgentKind
		}
		if label == "" {
			label = "agent"
		}
		b.WriteString(fmt.Sprintf("%d. %s｜%s\n", len(menu), label, item.ActorID))
	}
	if len(menu) == 0 {
		a.forgetMenu(acc.ID)
		a.replyRaw(ctx, acc, chatID, "当前没有可挂载的 agent。用法：/bind <编号或 id>")
		return
	}
	a.rememberMenu(acc.ID, menu)
	b.WriteString("用法：/bind <编号或 id>")
	a.replyRaw(ctx, acc, chatID, truncateReply(b.String()))
}

// answerUnmount releases the account's exclusive mount (/unmount). An
// unmounted account gets a plain notice.
func (a *Actor) answerUnmount(ctx actor.Context, acc gen.ImAccount, chatID string) {
	st, err := a.loadStore()
	if err != nil {
		ctx.Logger().Warn("im.inbound: /unmount could not load store", "error", truncate(err.Error(), replyLogLimit))
		a.replyRaw(ctx, acc, chatID, "⚠️ 卸载失败：状态读取失败")
		return
	}
	idx := findRoute(st, acc.ID)
	if idx < 0 {
		a.replyRaw(ctx, acc, chatID, "当前账户未挂载")
		return
	}
	route := st.Routes[idx]
	if _, err := a.handleRouteDelete(ctx, gen.ImRouteDeleteReq{ID: route.ID}); err != nil {
		ctx.Logger().Warn("im.inbound: /unmount failed", "chat", chatID, "error", err)
		a.replyRaw(ctx, acc, chatID, "卸载失败："+truncate(err.Error(), 200))
		return
	}
	a.replyRaw(ctx, acc, chatID, "✅ 已卸载 agent："+route.AgentActorID)
}

// ---------------------------------------------------------------------------
// /bind menu cache (owner-loop only)
// ---------------------------------------------------------------------------

// rememberMenu caches the numbered agent order rendered by the last /bind
// menu for one account.
func (a *Actor) rememberMenu(accountID string, agentIDs []string) {
	if a.mountMenus == nil {
		a.mountMenus = map[string][]string{}
	}
	a.mountMenus[accountID] = append([]string(nil), agentIDs...)
}

// menuFor returns the cached menu order for the account (nil when none).
func (a *Actor) menuFor(accountID string) []string {
	return a.mountMenus[accountID]
}

// forgetMenu drops the cached menu (e.g. when no agent is mountable).
func (a *Actor) forgetMenu(accountID string) {
	delete(a.mountMenus, accountID)
}

// statusSummary renders the /status answer: the receiving account plus its
// mount state (mounted agent id + MountedAt shown in local time).
func (a *Actor) statusSummary(acc gen.ImAccount) string {
	var b strings.Builder
	b.WriteString("📊 IM 状态\n")
	enabled := "已停用"
	if acc.Enabled {
		enabled = "已启用"
	}
	b.WriteString(fmt.Sprintf("账户：%s（%s，%s）\n", acc.Name, acc.Provider, enabled))
	st, err := a.loadStore()
	if err != nil {
		b.WriteString("挂载：状态读取失败")
		return b.String()
	}
	if idx := findRoute(st, acc.ID); idx >= 0 {
		route := st.Routes[idx]
		b.WriteString(fmt.Sprintf("挂载 agent：%s\n挂载时间：%s（共 %d 条挂载）",
			route.AgentActorID, localTimeText(route.MountedAt), len(st.Routes)))
	} else {
		b.WriteString(fmt.Sprintf("挂载 agent：未挂载（默认 coordinator，共 %d 条挂载）", len(st.Routes)))
	}
	return b.String()
}

// localTimeText renders a stored UTC ISO timestamp (MountedAt) in the local
// timezone for chat display; unparseable values pass through unchanged.
func localTimeText(utcISO string) string {
	t, err := time.Parse(time.RFC3339, utcISO)
	if err != nil {
		return utcISO
	}
	return t.Local().Format("2006-01-02 15:04:05 MST")
}

// submitAgentTurn resolves the account's mounted agent (bound route first,
// coordinator default otherwise), then submits the message text to the
// agent's chat_submit as an IM-tagged turn (Meta "im:<provider>"). The
// returned TurnActorId is parked in pendingReplies and the poll tick is
// armed. Before resolving, a mounted account whose agent no longer exists is
// released first (inbound cascade release) so the message falls back to the
// coordinator default; an unmounted account still reaches the coordinator
// but the sender is nudged to /bind.
func (a *Actor) submitAgentTurn(ctx actor.Context, acc gen.ImAccount, msg InboundMessage) error {
	released := a.releaseGoneAccountMount(ctx, acc.ID)
	mounted := a.accountMounted(acc.ID)
	if released {
		a.replyRaw(ctx, acc, msg.ChatID, "ℹ️ 挂载的 agent 已不存在，已自动卸载；本条默认转交 coordinator，可 /bind 重新挂载。")
	} else if !mounted {
		a.replyRaw(ctx, acc, msg.ChatID, "ℹ️ 本账户未挂载，请 /bind <编号或 agent actor id>；当前默认转交 coordinator。")
	}
	agentActorID, err := a.resolveAgentActorID(ctx, acc.ID)
	if err != nil {
		ctx.Logger().Warn("im.inbound: route resolution failed", "chat", msg.ChatID, "error", err)
		a.replyRaw(ctx, acc, msg.ChatID, "⚠️ 路由失败："+truncate(err.Error(), 200))
		return nil
	}
	cid, err := identity.ParseCanonicalID(agentActorID)
	if err != nil {
		a.replyRaw(ctx, acc, msg.ChatID, "⚠️ 路由失败：agent actor id 无效")
		return nil
	}
	agentRef, ok := ctx.LookupID(id.From(cid))
	if !ok || agentRef == nil {
		a.replyRaw(ctx, acc, msg.ChatID, "⚠️ 路由失败：agent 未加载")
		return nil
	}

	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), replyInvokeTimeout)
	defer cancel()
	call := agentRef.Invoke(callCtx, "chat_submit", gen.AgentChatSubmitReq{
		Text: msg.Text,
		Meta: "im:" + acc.Provider,
	})
	if call == nil {
		a.replyRaw(ctx, acc, msg.ChatID, "⚠️ 提交失败：agent 不可达")
		return nil
	}
	defer call.Close()
	v, err := call.Final(callCtx)
	if err != nil {
		ctx.Logger().Warn("im.inbound: chat_submit failed", "chat", msg.ChatID, "error", err)
		a.replyRaw(ctx, acc, msg.ChatID, "⚠️ 提交失败："+truncate(err.Error(), 200))
		return nil
	}
	resp, ok := decodeAs[gen.AgentChatSubmitResp](v)
	if !ok || resp.TurnActorID == "" {
		ctx.Logger().Warn("im.inbound: chat_submit returned no turn id", "chat", msg.ChatID, "value_type", fmt.Sprintf("%T", v))
		a.replyRaw(ctx, acc, msg.ChatID, "⚠️ 提交失败：未返回 turn id")
		return nil
	}

	a.addPendingReply(resp.TurnActorID, &pendingReply{
		AccountID:    acc.ID,
		ChatID:       msg.ChatID,
		AgentActorID: agentActorID,
		TurnID:       resp.TurnActorID,
		SubmittedAt:  time.Now(),
	})
	ctx.Logger().Info("im.inbound: turn submitted", "chat", msg.ChatID, "turn", resp.TurnActorID, "text_len", len(msg.Text))
	a.scheduleReplyPollTick(ctx)
	return nil
}

// ---------------------------------------------------------------------------
// pendingReplies access (cross-lane: owner lane inserts, im_poll removes)
// ---------------------------------------------------------------------------

// addPendingReply parks one in-flight turn. Safe from the owner lane while the
// im_poll lane reads/removes concurrently.
func (a *Actor) addPendingReply(turnID string, p *pendingReply) {
	a.replyMu.Lock()
	if a.pendingReplies == nil {
		a.pendingReplies = map[string]*pendingReply{}
	}
	a.pendingReplies[turnID] = p
	a.replyMu.Unlock()
}

// dropPendingReply removes one in-flight turn (idempotent).
func (a *Actor) dropPendingReply(turnID string) {
	a.replyMu.Lock()
	delete(a.pendingReplies, turnID)
	a.replyMu.Unlock()
}

// pendingReplyCount reports how many turns are still awaiting a reply.
func (a *Actor) pendingReplyCount() int {
	a.replyMu.Lock()
	defer a.replyMu.Unlock()
	return len(a.pendingReplies)
}

// snapshotPendingReplies returns a shallow copy of the pending map. The
// *pendingReply values are immutable once inserted, so the caller can process
// the snapshot without holding replyMu and drop entries via dropPendingReply.
func (a *Actor) snapshotPendingReplies() map[string]*pendingReply {
	a.replyMu.Lock()
	defer a.replyMu.Unlock()
	out := make(map[string]*pendingReply, len(a.pendingReplies))
	for k, v := range a.pendingReplies {
		out[k] = v
	}
	return out
}

// scheduleReplyPollTick arms the next poll tick while there is anything to
// watch: pending replies and/or live mounts (mount reconciliation).
func (a *Actor) scheduleReplyPollTick(ctx actor.Context) {
	if a.pendingReplyCount() == 0 && !a.hasMounts() {
		return
	}
	if err := ctx.After(replyPollInterval, callableReplyPollTick, nil); err != nil {
		ctx.Logger().Error("im: schedule reply poll tick failed", "err", err)
	}
}

// handleReplyPollTick is the turn-completion watcher and mount reconciler. It
// runs on the dedicated "im_poll" lane (see OnStart), so its cross-actor
// workspace / session_summary waits never occupy the owner lane; it re-arms
// its own next tick.
//
// It fetches the workspace agent list once per tick, releases every mount
// whose agent is gone (cascade release: a deleted agent frees its slot for
// remounting), then advances every pending reply whose agent has reached a
// terminal Runtime.State (completed / failed / cancelled) by pulling the
// agent's session_summary and extracting the submitted turn's last assistant
// text step. Replies whose agent is still busy (or whose turn's step has not
// landed yet) keep waiting until replyWaitDeadline. A failed state fetch is
// logged and never blocks either concern.
func (a *Actor) handleReplyPollTick(ctx actor.Context) error {
	hasMounts := a.hasMounts()
	if a.pendingReplyCount() == 0 && !hasMounts {
		return nil
	}
	now := time.Now()

	state, stateOK := fetchWorkspaceAgentState(ctx)
	if !stateOK {
		ctx.Logger().Warn("im: reply poll could not fetch agent state")
	}
	if hasMounts {
		a.reconcileMounts(ctx, state, stateOK)
	}
	byActor := map[string]*gen.AgentListItem{}
	if stateOK {
		for i := range state.Items {
			byActor[state.Items[i].ActorID] = &state.Items[i]
		}
	}

	// Process a snapshot: handleInbound (owner lane) may insert a new pending
	// reply mid-tick, which the next tick picks up.
	for turnID, p := range a.snapshotPendingReplies() {
		if now.Sub(p.SubmittedAt) > replyWaitDeadline {
			a.dropPendingReply(turnID)
			ctx.Logger().Warn("im: reply wait deadline exceeded", "turn", turnID, "chat", p.ChatID)
			a.sendReplyNotice(ctx, p, "⏱️ 等待回复超时，请稍后再试。")
			continue
		}
		item := byActor[p.AgentActorID]
		if item == nil {
			// Agent not visible in the workspace list (unloaded while the
			// turn was in flight) — keep polling until the deadline.
			continue
		}
		runtimeState := ""
		if item.Runtime != nil {
			runtimeState = item.Runtime.State
		}
		switch runtimeState {
		case runtimeRunning, runtimeWaiting, runtimePaused:
			continue // still busy
		case runtimeFailed:
			a.dropPendingReply(turnID)
			detail := "agent 执行失败"
			if item.Runtime != nil && item.Runtime.Error != "" {
				detail += "：" + truncate(item.Runtime.Error, 200)
			}
			ctx.Logger().Warn("im: agent turn failed", "turn", turnID, "chat", p.ChatID, "error", truncate(runtimeError(item), replyLogLimit))
			a.sendReplyNotice(ctx, p, "⚠️ "+detail)
			continue
		case runtimeCancel:
			a.dropPendingReply(turnID)
			ctx.Logger().Info("im: agent turn cancelled", "turn", turnID, "chat", p.ChatID)
			a.sendReplyNotice(ctx, p, "⛔ 回复已取消。")
			continue
		}
		// completed (or idle "" leftover): the turn's assistant step decides.
		text, found, err := a.fetchTurnReply(ctx, p)
		if err != nil {
			ctx.Logger().Warn("im: session_summary fetch failed", "turn", turnID, "error", err)
			continue // retry on the next tick
		}
		if !found || text == "" {
			continue // step not landed yet; keep waiting
		}
		a.dropPendingReply(turnID)
		ctx.Logger().Info("im: reply delivered", "turn", turnID, "chat", p.ChatID, "reply_len", len(text))
		a.deliverReply(ctx, p, text)
	}

	a.scheduleReplyPollTick(ctx)
	return nil
}

// runtimeError extracts the agent error string for logging.
func runtimeError(item *gen.AgentListItem) string {
	if item.Runtime == nil {
		return ""
	}
	return item.Runtime.Error
}

// fetchWorkspaceAgentState calls workspace.agent_list_state and decodes the
// agent registry snapshot.
func fetchWorkspaceAgentState(ctx actor.Context) (gen.WorkspaceAgentListState, bool) {
	wsRef, ok := ctx.LookupService("workspace")
	if !ok || wsRef == nil {
		return gen.WorkspaceAgentListState{}, false
	}
	planner := ctx.Planner()
	if planner == nil {
		return gen.WorkspaceAgentListState{}, false
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), replyInvokeTimeout)
	defer cancel()
	result, err := planner.Call(callCtx, wsRef, "workspace.agent_list_state", nil).Await()
	if err != nil {
		return gen.WorkspaceAgentListState{}, false
	}
	state, ok := decodeAs[gen.WorkspaceAgentListState](result)
	if !ok {
		return gen.WorkspaceAgentListState{}, false
	}
	return state, true
}

// fetchTurnReply pulls the agent's session_summary and returns the submitted
// turn's final assistant text. found=false (nil error) means the turn's
// assistant step has not landed yet.
func (a *Actor) fetchTurnReply(ctx actor.Context, p *pendingReply) (text string, found bool, err error) {
	cid, cerr := identity.ParseCanonicalID(p.AgentActorID)
	if cerr != nil {
		return "", false, fmt.Errorf("invalid agent actor id: %w", cerr)
	}
	agentRef, ok := ctx.LookupID(id.From(cid))
	if !ok || agentRef == nil {
		return "", false, fmt.Errorf("agent %q not addressable", truncate(p.AgentActorID, 64))
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), replyInvokeTimeout)
	defer cancel()
	call := agentRef.Invoke(callCtx, "session_summary", gen.AgentSessionSummaryReq{MaxTurns: 10})
	if call == nil {
		return "", false, fmt.Errorf("agent %q not reachable", truncate(p.AgentActorID, 64))
	}
	defer call.Close()
	v, ferr := call.Final(callCtx)
	if ferr != nil {
		return "", false, ferr
	}
	resp, ok := decodeAs[gen.AgentSessionSummaryResp](v)
	if !ok {
		return "", false, fmt.Errorf("session_summary returned undecodable payload %T", v)
	}
	return lastAssistantText(resp.Steps, p.TurnID), true, nil
}

// lastAssistantText returns the text of the LAST assistant step belonging to
// turnID whose type is "text", joining its text Content blocks. Discarded
// steps never win.
func lastAssistantText(steps []gen.Step, turnID string) string {
	var text string
	for i := range steps {
		s := &steps[i]
		if s.TurnID != turnID || s.Role != roleAssistant || s.Type != typeText || s.Discarded {
			continue
		}
		var parts []string
		for _, b := range s.Content {
			if b.Type == typeText && b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		if len(parts) > 0 {
			text = strings.Join(parts, "\n\n")
		}
	}
	return text
}

// deliverReply simplifies the markdown, caps the length and pushes the reply
// into the chat.
func (a *Actor) deliverReply(ctx actor.Context, p *pendingReply, text string) {
	out := truncateReply(simplifyMarkdown(text))
	st, err := a.loadStore()
	if err != nil {
		ctx.Logger().Error("im: reply delivery could not load store", "error", err)
		return
	}
	idx := findAccount(st, p.AccountID)
	if idx < 0 {
		ctx.Logger().Warn("im: reply delivery account gone", "account", p.AccountID)
		return
	}
	a.replyRaw(ctx, st.Accounts[idx], p.ChatID, out)
}

// sendReplyNotice pushes a short system notice (timeout / failure) for a
// pending reply without a markdown pass — notices are already plain text but
// still capped.
func (a *Actor) sendReplyNotice(ctx actor.Context, p *pendingReply, notice string) {
	st, err := a.loadStore()
	if err != nil {
		ctx.Logger().Error("im: notice delivery could not load store", "error", err)
		return
	}
	idx := findAccount(st, p.AccountID)
	if idx < 0 {
		return
	}
	a.replyRaw(ctx, st.Accounts[idx], p.ChatID, truncateReply(notice))
}

// replyRaw sends text into one chat through the account's provider adapter.
// Errors are logged (truncated) but never fail the pipeline turn.
func (a *Actor) replyRaw(ctx actor.Context, acc gen.ImAccount, chatID string, text string) {
	if text == "" {
		return
	}
	provider, ok := LookupProvider(acc.Provider)
	if !ok {
		ctx.Logger().Warn("im: no provider adapter for account", "account", acc.ID, "provider", acc.Provider)
		return
	}
	if err := provider.Send(ctx.Lifecycle(), acc, chatID, text); err != nil {
		ctx.Logger().Warn("im: provider send failed", "chat", chatID, "provider", acc.Provider,
			"error", truncate(err.Error(), replyLogLimit), "text", truncate(text, replyLogLimit))
	}
}

// ---------------------------------------------------------------------------
// Reply assembly helpers
// ---------------------------------------------------------------------------

// truncateReply caps s at maxReplyRunes runes, appending a visible truncation
// marker inside the budget when cutting. Rune-safe so multi-byte text is
// never split mid-character.
func truncateReply(s string) string {
	if runeLenWithin(s, maxReplyRunes) {
		return s
	}
	keep := maxReplyRunes - len([]rune(replyTruncMark))
	out := make([]rune, 0, keep)
	for _, r := range s {
		if len(out) >= keep {
			break
		}
		out = append(out, r)
	}
	return string(out) + replyTruncMark
}

// runeLenWithin reports whether s has at most max runes.
func runeLenWithin(s string, max int) bool {
	n := 0
	for range s {
		n++
		if n > max {
			return false
		}
	}
	return true
}

// Markdown simplification regexes. Deliberately conservative: emphasis via a
// single underscore is NOT stripped (it would eat snake_case identifiers),
// fenced code content is preserved verbatim.
var (
	mdHeadingRe = regexp.MustCompile(`^\s{0,3}#{1,6}\s+`)
	mdLinkRe    = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
	mdBoldRe    = regexp.MustCompile(`\*\*(.+?)\*\*|__(.+?)__`)
	mdItalicRe  = regexp.MustCompile(`\*(.+?)\*`)
	mdCodeRe    = regexp.MustCompile("`([^`]+)`")
)

// simplifyMarkdown degrades assistant markdown to chat-friendly plain text:
// fenced code blocks keep their content but lose the fences, headings lose
// their leading #'s, links collapse to "text (url)", and emphasis / inline
// code markers are dropped.
func simplifyMarkdown(src string) string {
	var b strings.Builder
	inFence := false
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue // drop the fence marker line, keep content verbatim
		}
		if !inFence {
			line = stripInlineMarkdown(line)
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// stripInlineMarkdown removes inline markdown decorations from one line
// outside code fences.
func stripInlineMarkdown(line string) string {
	line = mdHeadingRe.ReplaceAllString(line, "")
	line = mdLinkRe.ReplaceAllString(line, "$1 ($2)")
	line = mdBoldRe.ReplaceAllString(line, "${1}${2}")
	line = mdItalicRe.ReplaceAllString(line, "$1")
	line = mdCodeRe.ReplaceAllString(line, "$1")
	return line
}

// decodeAs coerces a cross-actor invoke result into T, accepting the typed
// struct, raw JSON bytes, or a generic map (mirrors decodeCoordinatorLookup).
func decodeAs[T any](v any) (T, bool) {
	var zero T
	switch t := v.(type) {
	case T:
		return t, true
	case []byte:
		var out T
		if err := json.Unmarshal(t, &out); err == nil {
			return out, true
		}
		return zero, false
	case string:
		var out T
		if err := json.Unmarshal([]byte(t), &out); err == nil {
			return out, true
		}
		return zero, false
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return zero, false
		}
		var out T
		if err := json.Unmarshal(b, &out); err == nil {
			return out, true
		}
		return zero, false
	}
}
