package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime/pprof"
	"strconv"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/debug"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/util"
)

// maxProfileTextBytes caps the profile text returned to the LLM. Goroutine and
// heap profiles can exceed several MB on a busy runtime; truncation keeps the
// response within tool-result limits while Debug=1 output stays complete for
// typical workloads.
const maxProfileTextBytes = 256 * 1024

// internalRefreshWorktreeStatus is the fire-and-forget self-call used by the
// stateless invoke_callable handler to refresh agent-local worktree binding
// state (mode card mounts, component snapshot, mailbox persistence). It runs
// on the dedicated coord_event stateful lane (agent.go WithLoop), so it no
// longer occupies the owner lane's business ingress while still serializing
// with the other inbound event callbacks. invoke_callable itself cannot touch
// that state: it runs on the stateless pool, and any RawSession/steps/status
// or component write from there would race the engine.
const internalRefreshWorktreeStatus = "internal_refresh_worktree_status"

// handleInternalRefreshWorktreeStatus refreshes the agent's worktree binding
// state and re-notifies the workspace after a project.worktree_* invocation.
// It runs on the coord_event stateful lane: refreshWorktreeStatus mounts/
// unmounts the builtin:mode:worktree card and persists the mailbox, so it must
// be treated as a state writer (never a stateless handler).
func (a *Actor) handleInternalRefreshWorktreeStatus(ctx actor.Context) error {
	a.refreshWorktreeStatus(ctx)
	a.notifyWorkspaceStatus(ctx)
	return nil
}

// isValidProfileName reports whether name is a pprof profile kind this
// callable can capture.
func isValidProfileName(name string) bool {
	switch name {
	case "cpu", "heap", "goroutine", "mutex", "block", "threadcreate":
		return true
	default:
		return false
	}
}

// handleFrontendDebug lets a debugging agent execute JavaScript in the current
// frontend webview (desktop runtime only) or read basic page info.
func (a *Actor) handleFrontendDebug(ctx actor.PureContext, req domain.AgentFrontendDebugReq) (domain.AgentFrontendDebugResp, error) {
	if debug.EvalJS == nil {
		return domain.AgentFrontendDebugResp{}, fmt.Errorf("agent.frontend_debug: frontend runtime not available (not desktop mode)")
	}

	op := req.Operation
	if op == "" {
		op = "eval"
	}

	var script string
	switch op {
	case "eval":
		script = req.Script
		if script == "" {
			return domain.AgentFrontendDebugResp{}, fmt.Errorf("agent.frontend_debug: eval operation requires Script")
		}
	case "info":
		script = `({url: window.location.href, title: document.title, userAgent: navigator.userAgent, viewport: {width: window.innerWidth, height: window.innerHeight}})`
	default:
		return domain.AgentFrontendDebugResp{}, fmt.Errorf("agent.frontend_debug: unknown operation %q", op)
	}

	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()

	result, err := debug.EvalJS(callCtx, script)
	if err != nil {
		return domain.AgentFrontendDebugResp{Error: err.Error()}, nil
	}
	return domain.AgentFrontendDebugResp{Result: result}, nil
}

// handleListCallables returns all callable interfaces visible to this agent,
// optionally filtered by a query substring.
func (a *Actor) handleListCallables(_ actor.PureContext, req domain.AgentListCallablesReq) (domain.AgentListCallablesResp, error) {
	callables := a.callablesMap()
	query := strings.ToLower(req.Query)

	limit := req.Limit
	if limit <= 0 {
		limit = 200
	}

	items := make([]domain.CallableInterface, 0, len(callables))
	for _, ci := range callables {
		if query != "" {
			if !strings.Contains(strings.ToLower(ci.Name), query) && !strings.Contains(strings.ToLower(ci.Description), query) {
				continue
			}
		}
		items = append(items, ci)
		if len(items) >= int(limit) {
			break
		}
	}
	return domain.AgentListCallablesResp{Items: items}, nil
}

// freeAgentActionForCallID maps a workspace agent-management callable ID to
// its free-agent action name. Returns "" for callables that are not agent
// management operations — those continue through the capability binding path.
func freeAgentActionForCallID(callID string) string {
	switch callID {
	case "workspace.create_agent":
		return "create"
	case "workspace.load_agent":
		return "switch"
	case "workspace.agent_send_message":
		return "message"
	default:
		return ""
	}
}

// handleInvokeCallable lets a debugging agent invoke any other callable by
// service name and call ID. This is gated by the agent's tool resolution in
// production; it is only exposed as an autonomous tool when introspection
// debugging is enabled.
func (a *Actor) handleInvokeCallable(ctx actor.PureContext, req domain.AgentInvokeCallableReq) (domain.AgentInvokeCallableResp, error) {
	planner := ctx.Planner()
	if planner == nil {
		return domain.AgentInvokeCallableResp{}, fmt.Errorf("agent.invoke_callable: planner not available")
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()

	if req.AppID != "" {
		appRef, ok := ctx.LookupService("appmanager")
		if !ok {
			return domain.AgentInvokeCallableResp{}, fmt.Errorf("agent.invoke_callable: appmanager service not found")
		}

		// Free-agent action routing: when the target callable is a workspace
		// agent management operation (create/switch/message), route through
		// appmanager.agent.action which enforces FreeAgentPolicy, instead of
		// appmanager.invoke which enforces capability bindings.
		if action := freeAgentActionForCallID(req.CallID); action != "" {
			actionReq := gen.AppManagerAgentActionReq{
				ID: req.AppID, Action: action, Payload: req.Payload,
				AgentID: req.AgentID, Role: req.Role,
				ProjectID: req.ProjectID, RequestID: req.RequestID,
			}
			// Extract Kind and TargetAgentID from payload for policy check.
			if kind, _ := req.Payload["Kind"].(string); kind != "" {
				actionReq.Kind = kind
			}
			if tid, _ := req.Payload["TargetAgentId"].(string); tid != "" {
				actionReq.TargetAgentID = tid
			} else if tid, _ := req.Payload["AgentId"].(string); tid != "" {
				actionReq.TargetAgentID = tid
			}
			actionResp, err := planner.Call(callCtx, appRef, "appmanager.agent_action", actionReq).Await()
			if err != nil {
				return domain.AgentInvokeCallableResp{Error: err.Error()}, nil
			}
			return domain.AgentInvokeCallableResp{Result: actionResp}, nil
		}

		// Capability binding path (existing).
		payload, err := json.Marshal(req.Payload)
		if err != nil {
			return domain.AgentInvokeCallableResp{}, err
		}
		appResp, err := planner.Call(callCtx, appRef, "appmanager.invoke", gen.AppManagerInvokeReq{ID: req.AppID, Callable: req.CallID, Payload: payload, AgentID: req.AgentID, Role: req.Role, ProjectID: req.ProjectID, RequestID: req.RequestID}).Await()
		if err != nil {
			return domain.AgentInvokeCallableResp{Error: err.Error()}, nil
		}
		return domain.AgentInvokeCallableResp{Result: appResp}, nil
	}

	ref, ok := ctx.LookupService(req.Service)
	if !ok && req.Service == "project" {
		ref = ctx.Parent()
		ok = ref != nil
	}
	if !ok {
		return domain.AgentInvokeCallableResp{}, fmt.Errorf("invoke_callable: service %q not found", req.Service)
	}

	result, err := planner.Call(callCtx, ref, req.CallID, req.Payload).Await()
	// Worktree operations mutate agent-local worktree binding state (mode card
	// mounts, component snapshot) and are persisted via saveMailbox — stateful
	// writes that must run on the owner lane, serialized with the turn engine
	// and chat control-plane. From this stateless handler we only fire a
	// fire-and-forget self-call so the refresh lands back on the owner lane
	// instead of racing the engine's RawSession/steps writes here.
	if strings.HasPrefix(req.CallID, "project.worktree_") {
		if aerr := ctx.After(0, internalRefreshWorktreeStatus, nil); aerr != nil {
			ctx.Logger().Warn("agent.invoke_callable: schedule worktree status refresh failed", "error", aerr)
		}
	}
	if err != nil {
		return domain.AgentInvokeCallableResp{Error: err.Error()}, nil
	}
	return domain.AgentInvokeCallableResp{Result: result}, nil
}

// cellStatsResult mirrors the gospore gospore.cell.stats callable response.
// Defined locally (not codegen) because the callable returns `any`.
type cellStatsResult struct {
	ActorID        string            `json:"actorId"`
	ActorType      string            `json:"actorType"`
	State          string            `json:"state"`
	OwnerQueue     queueStat         `json:"ownerQueue"`
	SystemQueue    queueStat         `json:"systemQueue"`
	ReplyQueue     queueStat         `json:"replyQueue"`
	PendingInvokes int               `json:"pendingInvokes"`
	Invoke         invokeStatsResult `json:"invoke"`
}

// invokeStatsResult mirrors the app-level invoke diagnostics block of
// gospore.cell.stats. The pending table is shared process-wide, so this
// block is identical regardless of which actor the stats were read from.
type invokeStatsResult struct {
	InFlight        int                    `json:"inFlight"`
	InFlightByMode  map[string]int         `json:"inFlightByMode"`
	InFlightByCall  map[string]int         `json:"inFlightByCall"`
	Registered      uint64                 `json:"registered"`
	Completed       uint64                 `json:"completed"`
	Errored         uint64                 `json:"errored"`
	SendFailed      uint64                 `json:"sendFailed"`
	ClosedEarly     uint64                 `json:"closedEarly"`
	EvictedStalled  uint64                 `json:"evictedStalled"`
	EvictedCapacity uint64                 `json:"evictedCapacity"`
	DroppedFrames   uint64                 `json:"droppedFrames"`
	RecentEvictions []invokeEvictionRecord `json:"recentEvictions"`
}

// invokeEvictionRecord mirrors one gospore invoke.EvictionRecord as it rides
// through the gospore.cell.stats JSON envelope.
type invokeEvictionRecord struct {
	CallID string `json:"callId"`
	Mode   string `json:"mode"`
	CorID  uint64 `json:"corId"`
	Reason string `json:"reason"`
	Drops  int32  `json:"drops"`
	At     string `json:"at"`
}

type queueStat struct {
	Depth    int `json:"depth"`
	Capacity int `json:"capacity"`
}

// handleInspectActor returns a non-blocking runtime snapshot of a target
// actor: lifecycle state, pipeline water level (owner/system/reply queue
// depth vs capacity), and the count of outbound invocations awaiting
// response ("stuck" callables). For agent targets it also surfaces the
// business state from agent_status.
func (a *Actor) handleInspectActor(ctx actor.Context, req domain.AgentInspectActorReq) (domain.AgentInspectActorResp, error) {
	if strings.TrimSpace(req.ActorPath) == "" {
		return domain.AgentInspectActorResp{}, fmt.Errorf("agent.inspect_actor: ActorPath must not be empty")
	}

	planner := ctx.Planner()
	if planner == nil {
		return domain.AgentInspectActorResp{Error: "planner not available"}, nil
	}
	root := ctx.Root()
	if root == nil {
		return domain.AgentInspectActorResp{Error: "root actor not available"}, nil
	}

	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()

	// 1. Fetch cell runtime stats via the gospore root callable.
	raw, err := planner.Call(callCtx, root, "gospore.cell.stats", map[string]any{"actorPath": req.ActorPath}).Await()
	if err != nil {
		return domain.AgentInspectActorResp{Error: err.Error()}, nil
	}
	stats, err := decodeCellStatsResult(raw)
	if err != nil {
		return domain.AgentInspectActorResp{Error: fmt.Sprintf("decode cell stats: %v", err)}, nil
	}

	resp := domain.AgentInspectActorResp{
		ActorID:             stats.ActorID,
		ActorType:           stats.ActorType,
		CellState:           stats.State,
		OwnerQueueDepth:     int32(stats.OwnerQueue.Depth),
		OwnerQueueCapacity:  int32(stats.OwnerQueue.Capacity),
		SystemQueueDepth:    int32(stats.SystemQueue.Depth),
		SystemQueueCapacity: int32(stats.SystemQueue.Capacity),
		ReplyQueueDepth:     int32(stats.ReplyQueue.Depth),
		ReplyQueueCapacity:  int32(stats.ReplyQueue.Capacity),
		PendingInvokes:      int32(stats.PendingInvokes),
		Invoke:              toInvokeDiagnostics(stats.Invoke),
	}

	// 2. If the target is an agent, fetch its business state via agent_status.
	if stats.ActorType == "agent" && stats.ActorID != "" {
		if cid, perr := identity.ParseCanonicalID(stats.ActorID); perr == nil {
			if targetRef, ok := ctx.LookupID(id.From(cid)); ok && targetRef != nil {
				statusRaw, serr := planner.Call(callCtx, targetRef, "agent_status", map[string]any{}).Await()
				if serr == nil {
					if b, merr := json.Marshal(statusRaw); merr == nil {
						var status gen.AgentStatusResp
						if json.Unmarshal(b, &status) == nil {
							resp.BusinessState = status.State
						}
					}
				}
			}
		}
	}

	resp.Health = computeActorHealth(stats)
	return resp, nil
}

// toInvokeDiagnostics converts the decoded cell.stats invoke block into the
// codegen response type (uint64 counters narrow to int32).
func toInvokeDiagnostics(s invokeStatsResult) *gen.InvokeDiagnostics {
	byMode := make(map[string]int32, len(s.InFlightByMode))
	for k, v := range s.InFlightByMode {
		byMode[k] = int32(v)
	}
	var byCall map[string]int32
	if n := len(s.InFlightByCall); n > 0 {
		byCall = make(map[string]int32, n)
		for k, v := range s.InFlightByCall {
			byCall[k] = int32(v)
		}
	}
	var evictions []gen.InvokeEvictionRecord
	for _, r := range s.RecentEvictions {
		evictions = append(evictions, gen.InvokeEvictionRecord{
			CallID: r.CallID,
			Mode:   r.Mode,
			CorID:  strconv.FormatUint(r.CorID, 10),
			Reason: r.Reason,
			Drops:  r.Drops,
			At:     r.At,
		})
	}
	return &gen.InvokeDiagnostics{
		InFlight:        int32(s.InFlight),
		InFlightByMode:  byMode,
		InFlightByCall:  byCall,
		Registered:      int32(s.Registered),
		Completed:       int32(s.Completed),
		Errored:         int32(s.Errored),
		SendFailed:      int32(s.SendFailed),
		ClosedEarly:     int32(s.ClosedEarly),
		EvictedStalled:  int32(s.EvictedStalled),
		EvictedCapacity: int32(s.EvictedCapacity),
		DroppedFrames:   int32(s.DroppedFrames),
		RecentEvictions: evictions,
	}
}

// transientInvokePrefixes lists invoke call-ID prefixes whose in-flight
// entries never represent actor workload: event-bus subscriptions live for
// the whole process lifetime, and diagnostic probes (this tool's own
// gospore.cell.stats / inspect_actor calls) complete within milliseconds.
// Counting either as pending work would mislabel every actor.
var transientInvokePrefixes = []string{
	"gospore.events.subscribe",
	"gospore.cell.stats",
	"inspect_actor",
}

// countTransientInvocations sums in-flight invocations that are steady-state
// or self-observation rather than awaited work.
func countTransientInvocations(s cellStatsResult) int {
	n := 0
	for call, c := range s.Invoke.InFlightByCall {
		for _, prefix := range transientInvokePrefixes {
			if strings.HasPrefix(call, prefix) {
				n += c
				break
			}
		}
	}
	return n
}

// computeActorHealth derives a summary label from queue saturation and pending
// invocation count. Subscriptions and diagnostic probes are discounted from
// the pending count: they are steady-state or self-observation, not queued
// work.
func computeActorHealth(s cellStatsResult) string {
	if s.State == "stopped" {
		return "stopped"
	}
	// Owner queue near capacity means the actor can barely accept new stateful work.
	if s.OwnerQueue.Capacity > 0 && s.OwnerQueue.Depth*100 >= s.OwnerQueue.Capacity*80 {
		return "blocked"
	}
	pending := s.PendingInvokes - countTransientInvocations(s)
	if pending > 0 || s.OwnerQueue.Depth > 0 {
		return "backlogged"
	}
	return "ok"
}

// decodeCellStatsResult robustly decodes the planner.Call result into
// cellStatsResult. The result may arrive as:
//   - []byte — production path: gospore.cell.stats returns type `any`
//     (schema BuiltinAny), so the codec returns raw JSON bytes.
//   - map[string]any — when the caller-side codec decodes to a generic map.
//   - string — defensive fallback for unexpected transport shapes.
func decodeCellStatsResult(raw any) (cellStatsResult, error) {
	var b []byte
	switch v := raw.(type) {
	case nil:
		return cellStatsResult{}, nil
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		var err error
		b, err = json.Marshal(v)
		if err != nil {
			return cellStatsResult{}, err
		}
	}
	var s cellStatsResult
	if err := json.Unmarshal(b, &s); err != nil {
		return cellStatsResult{}, err
	}
	return s, nil
}

// handleCaptureProfile captures a Go runtime pprof profile of the running
// process and returns it as readable text. Three modes:
//
//  1. Summary (default, Debug=1): parse entries, sort by count descending,
//     show top TopN with aggregated function frames.
//  2. Raw stacks (Debug=2): full WriteTo output, byte-truncated.
//  3. Diff (Diff=true): two snapshots Seconds apart, only changed entries.
//
// cpu profiles always go through `go tool pprof -text -cum` (TopN → -nodecount).
func (a *Actor) handleCaptureProfile(ctx actor.PureContext, req domain.AgentCaptureProfileReq) (domain.AgentCaptureProfileResp, error) {
	profile := req.Profile
	if profile == "" {
		profile = "goroutine"
	}
	if !isValidProfileName(profile) {
		return domain.AgentCaptureProfileResp{}, fmt.Errorf("agent.capture_profile: unknown profile %q (want cpu|heap|goroutine|mutex|block|threadcreate)", profile)
	}

	if profile == "cpu" {
		if req.Diff {
			return domain.AgentCaptureProfileResp{}, fmt.Errorf("agent.capture_profile: Diff is not supported for cpu profiles (cpu is already time-windowed; capture two cpu profiles manually to compare)")
		}
		return a.captureCPUProfile(ctx, req)
	}

	p := pprof.Lookup(profile)
	if p == nil {
		return domain.AgentCaptureProfileResp{}, fmt.Errorf("agent.capture_profile: profile %q not available (mutex/block require runtime.SetMutexProfileFraction / runtime.SetBlockProfileRate > 0)", profile)
	}

	topN := int(req.TopN)
	if topN <= 0 {
		topN = 20
	}
	if topN > 500 {
		topN = 500
	}

	// Diff mode: two snapshots, delta only.
	if req.Diff {
		return a.captureNamedProfileDiff(ctx, p, profile, req, topN)
	}

	// Raw stacks mode (Debug=2): full WriteTo output.
	if req.Debug >= 2 {
		var buf bytes.Buffer
		if err := p.WriteTo(&buf, 2); err != nil {
			return domain.AgentCaptureProfileResp{Error: err.Error()}, nil
		}
		text, truncated := truncateProfileText(buf.String())
		return domain.AgentCaptureProfileResp{
			Profile:   profile,
			Text:      text,
			Truncated: truncated,
		}, nil
	}

	// Summary mode (Debug=1, default): parse, aggregate, top-N.
	var buf bytes.Buffer
	if err := p.WriteTo(&buf, 1); err != nil {
		return domain.AgentCaptureProfileResp{Error: err.Error()}, nil
	}
	header, entries := parseProfileEntries(buf.String())
	text := formatSummary(header, entries, topN)
	if t, truncated := truncateProfileText(text); truncated {
		return domain.AgentCaptureProfileResp{Profile: profile, Text: t, Truncated: true}, nil
	}
	return domain.AgentCaptureProfileResp{Profile: profile, Text: text}, nil
}

// captureNamedProfileDiff captures two snapshots of a named profile Seconds
// apart and returns only entries that changed between the two. Useful for
// detecting goroutine leaks (new stacks), allocation growth, or contention
// changes that a single point-in-time snapshot cannot reveal.
func (a *Actor) captureNamedProfileDiff(ctx actor.PureContext, p *pprof.Profile, profile string, req domain.AgentCaptureProfileReq, topN int) (domain.AgentCaptureProfileResp, error) {
	seconds := int(req.Seconds)
	if seconds <= 0 {
		seconds = 10
	}
	if seconds > 60 {
		seconds = 60
	}

	var buf1 bytes.Buffer
	if err := p.WriteTo(&buf1, 1); err != nil {
		return domain.AgentCaptureProfileResp{Error: fmt.Sprintf("snapshot 1: %v", err)}, nil
	}

	timer := time.NewTimer(time.Duration(seconds) * time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Lifecycle().Done():
		return domain.AgentCaptureProfileResp{Error: "context cancelled between diff snapshots"}, nil
	}

	var buf2 bytes.Buffer
	if err := p.WriteTo(&buf2, 1); err != nil {
		return domain.AgentCaptureProfileResp{Error: fmt.Sprintf("snapshot 2: %v", err)}, nil
	}

	_, beforeEntries := parseProfileEntries(buf1.String())
	_, afterEntries := parseProfileEntries(buf2.String())
	added, grown, shrunk := diffProfileEntries(beforeEntries, afterEntries)
	text := formatDiff(profile, beforeEntries, afterEntries, added, grown, shrunk, topN)

	if t, truncated := truncateProfileText(text); truncated {
		return domain.AgentCaptureProfileResp{Profile: profile, Text: t, Truncated: true}, nil
	}
	return domain.AgentCaptureProfileResp{Profile: profile, Text: text}, nil
}

// captureCPUProfile captures a CPU profile for the requested duration and
// renders it as text via `go tool pprof -text -cum`. The binary profile is
// kept in a temp file when the go tool is unavailable so the agent can analyze
// it manually with shell_exec.
func (a *Actor) captureCPUProfile(ctx actor.PureContext, req domain.AgentCaptureProfileReq) (domain.AgentCaptureProfileResp, error) {
	seconds := req.Seconds
	if seconds <= 0 {
		seconds = 30
	}
	if seconds > 120 {
		seconds = 120
	}

	var buf bytes.Buffer
	if err := pprof.StartCPUProfile(&buf); err != nil {
		return domain.AgentCaptureProfileResp{Error: fmt.Sprintf("start cpu profile: %v (another profile may be running)", err)}, nil
	}

	timer := time.NewTimer(time.Duration(seconds) * time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Lifecycle().Done():
		pprof.StopCPUProfile()
		return domain.AgentCaptureProfileResp{Error: "context cancelled during cpu profiling"}, nil
	}
	pprof.StopCPUProfile()

	tmp, err := os.CreateTemp("", "sporemind-cpu-*.pprof")
	if err != nil {
		return domain.AgentCaptureProfileResp{
			Profile: "cpu",
			Error:   fmt.Sprintf("cpu profile captured (%d bytes) but temp file failed: %v", buf.Len(), err),
		}, nil
	}
	if _, werr := tmp.Write(buf.Bytes()); werr != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return domain.AgentCaptureProfileResp{Profile: "cpu", Error: fmt.Sprintf("write temp profile: %v", werr)}, nil
	}
	tmp.Close()

	cmdCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 30*time.Second)
	defer cancel()
	args := []string{"tool", "pprof", "-text", "-cum"}
	if req.TopN > 0 {
		args = append(args, fmt.Sprintf("-nodecount=%d", req.TopN))
	}
	args = append(args, tmp.Name())
	cmd := util.CommandContext(cmdCtx, "go", args...)
	out, cerr := cmd.Output()
	if cerr != nil {
		// Keep the temp file so the agent can analyze it manually.
		return domain.AgentCaptureProfileResp{
			Profile: "cpu",
			Text:    fmt.Sprintf("cpu profile captured for %ds (%d bytes binary) at %s — go tool pprof unavailable (%v); analyze the file with shell_exec or copy it out", seconds, buf.Len(), tmp.Name(), cerr),
			Error:   cerr.Error(),
		}, nil
	}
	os.Remove(tmp.Name())

	text, truncated := truncateProfileText(string(out))
	return domain.AgentCaptureProfileResp{
		Profile:   "cpu",
		Text:      text,
		Truncated: truncated,
	}, nil
}

// truncateProfileText caps s at maxProfileTextBytes, cutting at the last
// complete line before the limit and appending a truncation marker.
func truncateProfileText(s string) (string, bool) {
	if len(s) <= maxProfileTextBytes {
		return s, false
	}
	cut := s[:maxProfileTextBytes]
	if idx := strings.LastIndexByte(cut, '\n'); idx >= 0 {
		cut = cut[:idx]
	}
	return cut + "\n... [truncated: profile exceeds 256KB; use Debug=1 for a more compact summary]", true
}
