// Package crawl owns the durable task model and frontier queue for a browser
// crawl job. It is intentionally a skeleton: it tracks task state and
// persisted queue data, but does not drive the browser itself.
//
// Topology:
//
//	/crawl                         # this actor; holds crawl task state
//
// All state is persisted through persist.Persist, so a restart restores the
// running task and frontier regardless of whether the browser process is up.
package crawl

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/sporemind/pkg/actor/project"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
	browserusegen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/policy"
)

// State is the crawl task state machine.
type State string

const (
	StateRunning       State = "running"
	StateAwaitingLogin State = "awaiting_login"
	StateDone          State = "done"
	StateFailed        State = "failed"
	StateCancelled     State = "cancelled"
)

// terminal reports states that cannot be resumed.
func (s State) terminal() bool {
	return s == StateDone || s == StateFailed || s == StateCancelled
}

// Config is a point-in-time snapshot of the crawl definition card. It is stored
// with the task so that later edits to the card do not affect a running task.
//
// Fields are deliberately aligned with the type: crawl card validator in
// pkg/actor/project/card_validation.go: seeds, max_depth, max_pages,
// same_domain, rate_limit, extract_schema, profile, mode, instance_id.
//
// RateLimit is the per-request interval in seconds (0 = no limit). It is the
// canonical runtime form; the card validator accepts duration strings such as
// "500ms" which the executor normalizes to seconds before constructing a Config.
type Config struct {
	Seeds            []string       `json:"seeds"`
	MaxDepth         int            `json:"max_depth"`
	MaxPages         int            `json:"max_pages"`
	SameDomain       bool           `json:"same_domain"`
	RateLimit        int            `json:"rate_limit"`
	ExtractSchema    string         `json:"extract_schema,omitempty"`
	ExtractSelectors map[string]any `json:"extract_selectors,omitempty"`
	Profile          string         `json:"profile,omitempty"`
	Mode             string         `json:"mode"`
	InstanceID       string         `json:"instance_id,omitempty"`
	Extra            map[string]any `json:"extra,omitempty"`
}

// requireDeveloperOrManager returns nil if role is allowed to start a crawl.
// User-facing callers (admin/developer) are always allowed; trusted system
// actors (e.g. the workspace crawl executor) are also permitted so that
// workflow map nodes can dispatch crawl tasks without a human in the loop.
func requireDeveloperOrManager(role id.Role) error {
	return policy.RequireDeveloperOrManager(role)
}

// normalizeMode returns the validated crawl mode, defaulting to hidden_window.
func normalizeMode(mode string) string {
	switch mode {
	case "performance":
		return "performance"
	default:
		return "hidden_window"
	}
}

// Validate checks the configuration snapshot. It mirrors the runtime subset of
// the card validator without requiring a codegen struct registry.
func (c Config) Validate() error {
	if len(c.Seeds) == 0 {
		return fmt.Errorf("crawl: seeds must contain at least one URL")
	}
	for _, s := range c.Seeds {
		if s == "" {
			return fmt.Errorf("crawl: seeds must not contain empty URLs")
		}
	}
	if c.MaxDepth < 0 {
		return fmt.Errorf("crawl: max_depth must be >= 0")
	}
	if c.MaxPages <= 0 {
		return fmt.Errorf("crawl: max_pages must be > 0")
	}
	switch c.Mode {
	case "", "hidden_window", "performance":
	default:
		return fmt.Errorf("crawl: mode %q is not recognized (hidden_window, performance)", c.Mode)
	}
	if c.RateLimit < 0 {
		return fmt.Errorf("crawl: rate_limit must be >= 0")
	}
	if c.ExtractSchema != "" && c.ExtractSchema != "BrowserCrawlPageResult" {
		return fmt.Errorf("crawl: extract.schema %q is not supported in v1 (BrowserCrawlPageResult)", c.ExtractSchema)
	}
	return nil
}

// Task is one crawl job. It carries the configuration snapshot, a state
// machine, the set of already-crawled URLs, the frontier queue, a result
// cursor that tells consumers how many pages have produced results, and the
// extracted results themselves.
type Task struct {
	ID           string                        `json:"id"`
	Config       Config                        `json:"config"`
	State        State                         `json:"state"`
	CrawledURLs  []string                      `json:"crawled_urls"`
	Frontier     []string                      `json:"frontier"`
	Results      []gen.BrowserCrawlPageResult  `json:"results,omitempty"`
	ResultCursor int                           `json:"result_cursor"`
	LoginWallURL string                        `json:"login_wall_url,omitempty"`
	Error        string                        `json:"error,omitempty"`
	CreatedAt    time.Time                     `json:"created_at"`
	UpdatedAt    time.Time                     `json:"updated_at"`
}

// IsTerminal returns whether the task has reached a final state.
func (t *Task) IsTerminal() bool { return t.State.terminal() }

// NewTask creates a task from a config snapshot. The task always starts in
// the running state; it only enters awaiting_login when the runtime detects a
// login wall (the handoff path), never from a config flag.
func NewTask(id string, cfg Config) *Task {
	cfg.Mode = normalizeMode(cfg.Mode)
	now := time.Now().UTC()

	seen := make(map[string]struct{}, len(cfg.Seeds))
	frontier := make([]string, 0, len(cfg.Seeds))
	for _, s := range cfg.Seeds {
		if _, ok := seen[s]; ok || s == "" {
			continue
		}
		seen[s] = struct{}{}
		frontier = append(frontier, s)
	}

	return &Task{
		ID:           id,
		Config:       cfg,
		State:        StateRunning,
		Frontier:     frontier,
		ResultCursor: 0,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

// Transition moves the task to a new state if the move is legal.
func (t *Task) Transition(to State, reason string) error {
	if t.State.terminal() {
		return fmt.Errorf("crawl: cannot transition from terminal state %q", t.State)
	}
	if to == t.State {
		return nil
	}
	switch t.State {
	case StateRunning:
		if to != StateAwaitingLogin && to != StateDone && to != StateFailed && to != StateCancelled {
			return fmt.Errorf("crawl: invalid transition from %q to %q", t.State, to)
		}
	case StateAwaitingLogin:
		if to != StateRunning && to != StateFailed && to != StateCancelled {
			return fmt.Errorf("crawl: invalid transition from %q to %q", t.State, to)
		}
	default:
		return fmt.Errorf("crawl: invalid transition from %q to %q", t.State, to)
	}
	if (to == StateFailed || to == StateCancelled) && reason == "" && to == StateFailed {
		reason = "unknown error"
	}
	t.State = to
	if to == StateFailed || to == StateCancelled {
		t.Error = reason
	}
	t.UpdatedAt = time.Now().UTC()
	return nil
}

// LoginDone transitions a task from awaiting_login to running.
func (t *Task) LoginDone() error { return t.Transition(StateRunning, "") }

// Cancel marks the task as cancelled.
func (t *Task) Cancel(reason string) error { return t.Transition(StateCancelled, reason) }

// MarkFailed marks the task as failed with a reason.
func (t *Task) MarkFailed(reason string) error { return t.Transition(StateFailed, reason) }

// MarkDone marks the task as done when the frontier is exhausted.
func (t *Task) MarkDone() error { return t.Transition(StateDone, "") }

// RecordCrawlResult is the same as RecordCrawl but also stores the extracted
// result for the page. It is used by the execution engine.
func (t *Task) RecordCrawlResult(pageURL string, discovered []string, result *gen.BrowserCrawlPageResult) {
	t.RecordCrawl(pageURL, discovered)
	if result != nil {
		t.Results = append(t.Results, *result)
	}
}

// hostOf returns the lowercase host of a URL, or "" if parsing fails.
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Host)
}

// seedHosts returns the set of hosts derived from the seed URLs. Duplicates are
// collapsed; entries that cannot be parsed are dropped.
func (t *Task) seedHosts() map[string]struct{} {
	hosts := make(map[string]struct{}, len(t.Config.Seeds))
	for _, s := range t.Config.Seeds {
		if h := hostOf(s); h != "" {
			hosts[h] = struct{}{}
		}
	}
	return hosts
}

// isInDomain reports whether ref belongs to the seed-host set when same_domain is
// enabled. Relative URLs are resolved against base so they are treated as same
// domain. When same_domain is false every absolute URL is accepted.
func (t *Task) isInDomain(base, ref string) bool {
	if !t.Config.SameDomain {
		return true
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return false
	}
	u, err := baseURL.Parse(ref)
	if err != nil || u.Host == "" {
		return false
	}
	allowed := t.seedHosts()
	if len(allowed) == 0 {
		return false
	}
	_, ok := allowed[strings.ToLower(u.Host)]
	return ok
}

// seenSet returns the URLs that are either already crawled or already in the
// frontier, used to avoid duplicate queue entries.
func (t *Task) seenSet() map[string]struct{} {
	seen := make(map[string]struct{}, len(t.CrawledURLs)+len(t.Frontier))
	for _, u := range t.CrawledURLs {
		seen[u] = struct{}{}
	}
	for _, u := range t.Frontier {
		seen[u] = struct{}{}
	}
	return seen
}

// PopFrontier removes and returns the next URL from the frontier. If the queue
// is empty it returns an empty string.
func (t *Task) PopFrontier() string {
	if len(t.Frontier) == 0 {
		return ""
	}
	url := t.Frontier[0]
	t.Frontier = t.Frontier[1:]
	t.UpdatedAt = time.Now().UTC()
	return url
}

// RecordCrawl records that url was crawled, removes it from the frontier if it
// is still queued, and appends newly discovered URLs to the frontier while
// honoring same_domain and avoiding duplicates. Relative URLs are resolved
// against the crawled URL. It advances the result cursor.
func (t *Task) RecordCrawl(pageURL string, discovered []string) {
	base, err := url.Parse(pageURL)
	if err != nil {
		base = &url.URL{}
	}

	// Remove the crawled URL from the frontier if it is still queued.
	filtered := t.Frontier[:0]
	for _, u := range t.Frontier {
		if u != pageURL {
			filtered = append(filtered, u)
		}
	}
	t.Frontier = filtered

	crawledSet := make(map[string]struct{}, len(t.CrawledURLs))
	for _, u := range t.CrawledURLs {
		crawledSet[u] = struct{}{}
	}
	if _, ok := crawledSet[pageURL]; !ok {
		t.CrawledURLs = append(t.CrawledURLs, pageURL)
		crawledSet[pageURL] = struct{}{}
	}

	seen := t.seenSet()
	for _, raw := range discovered {
		ref, err := url.Parse(raw)
		if err != nil || ref.Scheme == "javascript" || ref.Scheme == "mailto" || ref.Scheme == "tel" {
			continue
		}
		resolved := base.ResolveReference(ref).String()
		if _, ok := seen[resolved]; ok {
			continue
		}
		if !t.isInDomain(pageURL, resolved) {
			continue
		}
		seen[resolved] = struct{}{}
		t.Frontier = append(t.Frontier, resolved)
	}
	t.ResultCursor = len(t.CrawledURLs)
	t.UpdatedAt = time.Now().UTC()
}

// Actor is the durable crawl task manager.
type Actor struct {
	actor.Host

	store persist.Persist

	mu      sync.Mutex
	actorID string
	tasks   []*Task
	nextID  int

	enginesMu sync.Mutex
	engines   map[string]*engine
}

// engine tracks one in-flight execution goroutine and its rate-limit state.
type engine struct {
	cancel        func()
	lastRequestAt time.Time
}

var _ persist.Persistent = (*Actor)(nil)

type snapshot struct {
	Tasks  []*Task `json:"tasks"`
	NextID int     `json:"next_id"`
}

// OnInit restores persisted state. It is called before the actor is registered.
func (a *Actor) OnInit(ctx actor.Context) error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("crawl"))
		if err != nil {
			return err
		}
	}
	a.actorID = ctx.Self().ID().String()
	a.tasks = make([]*Task, 0)
	a.engines = make(map[string]*engine)
	if err := a.Load(); err != nil {
		ctx.Logger().Error("crawl: load state failed", "error", err)
	}
	return nil
}

func (a *Actor) Type() string { return "crawl" }

// OnStart registers the crawl callables on the "crawl" domain so that the
// stable surface is exposed as crawl.*.
func (a *Actor) OnStart(ctx actor.Context) error {
	ctx.Logger().Info("crawl: starting", "id", a.actorID, "tasks", len(a.tasks))

	if err := ctx.Register("crawl.submit", a.handleSubmit, actor.AdminOnly()); err != nil {
		return fmt.Errorf("crawl: register submit: %w", err)
	}
	if err := ctx.Register("crawl.list", a.handleList, actor.Public()); err != nil {
		return fmt.Errorf("crawl: register list: %w", err)
	}
	if err := ctx.Register("crawl.get", a.handleGet, actor.Public()); err != nil {
		return fmt.Errorf("crawl: register get: %w", err)
	}
	if err := ctx.Register("crawl.cancel", a.handleCancel, actor.AdminOnly(),
		actor.WithDescription("Cancel a crawl task by TaskId. A running engine stops after the current page; a task suspended on a login wall (awaiting_login) also releases its hidden browser window."),
	); err != nil {
		return fmt.Errorf("crawl: register cancel: %w", err)
	}
	if err := ctx.Register("crawl.login_done", a.handleLoginDone, actor.AdminOnly()); err != nil {
		return fmt.Errorf("crawl: register login_done: %w", err)
	}
	if err := ctx.Register("crawl.update", a.handleUpdate, actor.Internal()); err != nil {
		return fmt.Errorf("crawl: register update: %w", err)
	}

	if err := ctx.Register("crawl.start", a.handleBrowserCrawlStart, actor.Public(),
		actor.WithDescription("Start a browser crawl task from an inline Config (seeds, mode, max pages, scope rules) — no card needed — or from a saved crawl card plus overrides. Returns the crawl TaskID; the crawl runs asynchronously — poll crawl.status and page through crawl.results."),
		// Field descriptions must be declared here (not just in the .spore
		// schema comments): host tool schemas are projected from reflection,
		// which carries no comments, so without WithParams the LLM sees a bare
		// {Card: string, Config: {...}} shape and asks the user for a crawl
		// card instead of filling Config inline.
		actor.WithParams(
			actor.ParamDesc{Name: "Config", Description: "Inline crawl configuration — sufficient on its own, no card required. Seeds (list of start URLs) is the only mandatory value; set InstanceID to reuse a mounted independent browser window. Others: MaxPages (default 100), MaxDepth (default 0, seed pages only), SameDomain, RateLimit (duration string e.g. \"500ms\"), Mode (hidden_window | performance), Profile, ExtractSchema (v1: BrowserCrawlPageResult), ExtractSelectors."},
			actor.ParamDesc{Name: "Card", Description: "Optional raw markdown of a type: crawl definition card (frontmatter + body). Only for workflow/persistent crawls; prefer Config for quick crawls."},
			actor.ParamDesc{Name: "Overrides", Description: "Optional highest-priority merge over Card/Config before snapshotting; card-data keys in snake_case (e.g. same_domain)."},
		),
	); err != nil {
		return fmt.Errorf("crawl: register start: %w", err)
	}
	if err := ctx.Register("crawl.status", a.handleBrowserCrawlStatus, actor.Public(),
		actor.WithDescription("Get a crawl task's state (running / awaiting_login / done / failed / cancelled), crawled and frontier page counts, max pages, and the error string when failed."),
	); err != nil {
		return fmt.Errorf("crawl: register status: %w", err)
	}
	if err := ctx.Register("crawl.results", a.handleBrowserCrawlResults, actor.Public(),
		actor.WithDescription("Fetch a crawl task's collected pages (URL, Title, Links, extracted Text), cursor-paginated (Cursor → NextCursor, default limit 50). Done=true when the crawl is finished and all pages have been read."),
	); err != nil {
		return fmt.Errorf("crawl: register results: %w", err)
	}
	if err := ctx.Register("crawl.handoff", a.handleBrowserCrawlHandoff, actor.AdminOnly(),
		actor.WithDescription("Open a visible, user-controllable browser window sharing the crawl task's hidden profile so the user can complete a login wall. Only valid while the task is in the awaiting_login state; after login the crawl resumes on the authenticated session."),
	); err != nil {
		return fmt.Errorf("crawl: register handoff: %w", err)
	}

	if err := ctx.RegisterDomain("crawl").Expose(); err != nil {
		return fmt.Errorf("crawl: expose crawl domain: %w", err)
	}

	// Resume any tasks that were running before the process stopped. The
	// browser may be off, but the frontier and progress survive in persist.
	for _, t := range a.tasks {
		if t.State == StateRunning {
			ctx.Logger().Info("crawl: resuming", "id", t.ID, "frontier", len(t.Frontier), "crawled", len(t.CrawledURLs))
			a.startEngine(ctx, t)
		}
	}
	return nil
}

// OnStop persists the current task state. The browser may be off, but the
// queue and state survive.
func (a *Actor) OnStop(_ actor.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.saveLocked()
}

// Save persists the task list and nextID.
func (a *Actor) Save() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.saveLocked()
}

func (a *Actor) saveLocked() error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("crawl"))
		if err != nil {
			return err
		}
	}
	copyTasks := append([]*Task(nil), a.tasks...)
	return a.store.Save(a.actorID, snapshot{Tasks: copyTasks, NextID: a.nextID})
}

// Load restores the task list and nextID from storage.
func (a *Actor) Load() error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("crawl"))
		if err != nil {
			return err
		}
	}
	var snap snapshot
	if err := persist.LoadOrZero(a.store, a.actorID, &snap); err != nil {
		return err
	}
	a.tasks = snap.Tasks
	if a.tasks == nil {
		a.tasks = make([]*Task, 0)
	}
	a.nextID = snap.NextID
	return nil
}

// SubmitReq starts a new crawl task. Hand-written placeholder until the crawl
// schema is code-generated.
type SubmitReq struct {
	Config Config `json:"config"`
}

// SubmitResp is the accepted crawl task. Hand-written placeholder until the
// crawl schema is code-generated.
type SubmitResp struct {
	Task Task `json:"task"`
}

func (a *Actor) handleSubmit(ctx actor.Context, req SubmitReq) (SubmitResp, error) {
	if err := policy.RequireDeveloper(ctx.Identity().Role); err != nil {
		return SubmitResp{}, err
	}
	if err := req.Config.Validate(); err != nil {
		return SubmitResp{}, err
	}

	a.mu.Lock()
	id := fmt.Sprintf("crawl-%d", a.nextID)
	a.nextID++
	task := NewTask(id, req.Config)
	a.tasks = append(a.tasks, task)
	if err := a.saveLocked(); err != nil {
		// Roll back the in-memory append so the actor stays consistent with the
		// persisted state.
		a.tasks = a.tasks[:len(a.tasks)-1]
		a.nextID--
		a.mu.Unlock()
		return SubmitResp{}, fmt.Errorf("crawl.submit: save failed: %w", err)
	}
	a.mu.Unlock()

	ctx.Logger().Info("crawl: submit", "id", id, "state", task.State, "mode", task.Config.Mode)
	a.startEngine(ctx, task)
	return SubmitResp{Task: *task}, nil
}

// GetReq requests a single task by ID. Hand-written placeholder until the crawl
// schema is code-generated.
type GetReq struct {
	ID string `json:"id"`
}

// handleGet is a stateless (PureContext) snapshot read: it returns a copy of
// the task under a.mu without mutating state.
func (a *Actor) handleGet(_ actor.PureContext, req GetReq) (Task, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, t := range a.tasks {
		if t.ID == req.ID {
			cp := *t
			return cp, nil
		}
	}
	return Task{}, fmt.Errorf("crawl.get: task %q not found", req.ID)
}

// ListResp returns all crawl tasks. Hand-written placeholder until the crawl
// schema is code-generated.
type ListResp struct {
	Tasks []*Task `json:"tasks"`
}

// handleList is a stateless (PureContext) snapshot read: it returns copies of
// every task under a.mu without mutating state.
func (a *Actor) handleList(_ actor.PureContext) (ListResp, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]*Task, len(a.tasks))
	for i, t := range a.tasks {
		cp := *t
		out[i] = &cp
	}
	return ListResp{Tasks: out}, nil
}

// CancelReq cancels a running or awaiting-login task. Hand-written placeholder
// until the crawl schema is code-generated.
type CancelReq struct {
	ID     string `json:"id"`
	Reason string `json:"reason,omitempty"`
}

func (a *Actor) handleCancel(ctx actor.Context, req CancelReq) (Task, error) {
	if err := policy.RequireDeveloper(ctx.Identity().Role); err != nil {
		return Task{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, t := range a.tasks {
		if t.ID != req.ID {
			continue
		}
		wasAwaiting := t.State == StateAwaitingLogin
		if err := t.Cancel(req.Reason); err != nil {
			return Task{}, err
		}
		if err := a.saveLocked(); err != nil {
			return Task{}, fmt.Errorf("crawl.cancel: save failed: %w", err)
		}
		a.stopEngine(req.ID)
		// When the task was suspended awaiting login the engine has already
		// exited, so the dedicated browser instance would otherwise leak.
		if wasAwaiting {
			a.removeBrowserInstance(ctx, req.ID)
		}
		ctx.Logger().Info("crawl: cancel", "id", req.ID)
		cp := *t
		return cp, nil
	}
	return Task{}, fmt.Errorf("crawl.cancel: task %q not found", req.ID)
}

// LoginDoneReq resumes a task that was waiting for login. Hand-written
// placeholder until the crawl schema is code-generated.
type LoginDoneReq struct {
	ID string `json:"id"`
}

func (a *Actor) handleLoginDone(ctx actor.Context, req LoginDoneReq) (Task, error) {
	if err := policy.RequireDeveloper(ctx.Identity().Role); err != nil {
		return Task{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, t := range a.tasks {
		if t.ID != req.ID {
			continue
		}
		if err := t.LoginDone(); err != nil {
			return Task{}, err
		}
		if err := a.saveLocked(); err != nil {
			return Task{}, fmt.Errorf("crawl.login_done: save failed: %w", err)
		}
		// Close the visible handoff window (if any) before resuming the hidden
		// crawl engine so the same profile can be reused.
		a.closeBrowserInstance(ctx, req.ID)
		a.startEngine(ctx, t)
		ctx.Logger().Info("crawl: login_done", "id", req.ID)
		cp := *t
		return cp, nil
	}
	return Task{}, fmt.Errorf("crawl.login_done: task %q not found", req.ID)
}

// --- crawl.* stable surface ---------------------------------------------------

// parseCardConfig parses a type: crawl definition card and optional overrides,
// snapshots the configuration, and returns it for validation.
func parseCardConfig(raw string, overrides map[string]any) (Config, error) {
	card := project.ParseCardRaw("crawl-card", raw)
	if card == nil {
		return Config{}, fmt.Errorf("parse card: empty card")
	}
	if card.Type != "crawl" {
		return Config{}, fmt.Errorf("crawl: card type is %q, expected crawl", card.Type)
	}
	data := mergeDataMaps(card.Data, overrides)
	return parseCrawlConfigFromData(data)
}

// resolveStartConfig merges configuration from up to three sources into a single
// Config snapshot. Priority (low → high): defaults < Card < Config non-zero
// fields < Overrides. At least one source must provide Seeds; otherwise the
// returned Config will fail Validate() with a clear message.
func resolveStartConfig(req gen.BrowserCrawlStartReq) (Config, error) {
	var base map[string]any
	if req.Card != "" {
		card := project.ParseCardRaw("crawl-card", req.Card)
		if card == nil {
			return Config{}, fmt.Errorf("parse card: empty card")
		}
		if card.Type != "crawl" {
			return Config{}, fmt.Errorf("crawl: card type is %q, expected crawl", card.Type)
		}
		base = card.Data
	} else {
		base = map[string]any{}
	}
	if req.Config != nil {
		base = mergeDataMaps(base, configToMap(req.Config))
	}
	if len(req.Overrides) > 0 {
		base = mergeDataMaps(base, req.Overrides)
	}
	return parseCrawlConfigFromData(base)
}

// configToMap converts the non-zero fields of a BrowserCrawlConfig into a
// card-data-style map so it can be merged into the same pipeline as card
// parsing. Keys match the type: crawl definition card field names (snake_case).
func configToMap(c *gen.BrowserCrawlConfig) map[string]any {
	m := map[string]any{}
	if len(c.Seeds) > 0 {
		m["seeds"] = c.Seeds
	}
	if c.MaxDepth != 0 {
		m["max_depth"] = c.MaxDepth
	}
	if c.MaxPages != 0 {
		m["max_pages"] = c.MaxPages
	}
	if c.SameDomain {
		m["same_domain"] = c.SameDomain
	}
	if c.RateLimit != "" {
		m["rate_limit"] = c.RateLimit
	}
	if c.Mode != "" {
		m["mode"] = c.Mode
	}
	if c.Profile != "" {
		m["profile"] = c.Profile
	}
	if c.ExtractSchema != "" || c.ExtractSelectors != nil {
		extract := map[string]any{}
		if c.ExtractSchema != "" {
			extract["schema"] = c.ExtractSchema
		}
		if c.ExtractSelectors != nil {
			extract["selectors"] = c.ExtractSelectors
		}
		m["extract"] = extract
	}
	if c.InstanceID != "" {
		m["instance_id"] = c.InstanceID
	}
	return m
}

// mergeDataMaps returns a shallow copy of base with overrides applied. A map
// override value fully replaces the corresponding base value.
func mergeDataMaps(base, overrides map[string]any) map[string]any {
	merged := make(map[string]any, len(base)+len(overrides))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range overrides {
		merged[k] = v
	}
	return merged
}

// parseCrawlConfigFromData converts the parsed card data block into a runtime
// Config snapshot. Values are string/int/bool/list as produced by the project
// card parser, so each field is coerced defensively.
func parseCrawlConfigFromData(data map[string]any) (Config, error) {
	cfg := Config{
		MaxDepth: 0,
		MaxPages: 100,
		Mode:     "hidden_window",
	}
	if seeds, ok := getStringSlice(data, "seeds"); ok {
		cfg.Seeds = seeds
	}
	if d, ok := getInt(data, "max_depth"); ok {
		cfg.MaxDepth = d
	}
	if d, ok := getInt(data, "max_pages"); ok {
		cfg.MaxPages = d
	}
	if b, ok := getBool(data, "same_domain"); ok {
		cfg.SameDomain = b
	}
	if s, ok := getString(data, "profile"); ok {
		cfg.Profile = s
	}
	if s, ok := getString(data, "mode"); ok {
		cfg.Mode = s
	}
	if s, ok := getString(data, "instance_id"); ok {
		cfg.InstanceID = s
	}
	if v, ok := data["rate_limit"]; ok {
		if s, ok := v.(string); ok && s != "" {
			d, err := time.ParseDuration(s)
			if err != nil {
				return Config{}, fmt.Errorf("crawl: invalid rate_limit %q: %w", s, err)
			}
			if d < 0 {
				return Config{}, fmt.Errorf("crawl: rate_limit must be >= 0")
			}
			cfg.RateLimit = int(d.Seconds())
		} else if n, ok := getIntValue(v); ok {
			cfg.RateLimit = n
		}
	}
	if ex, ok := getMap(data, "extract"); ok {
		if s, ok := getString(ex, "schema"); ok {
			cfg.ExtractSchema = s
		}
		if sel, ok := getMap(ex, "selectors"); ok {
			cfg.ExtractSelectors = sel
		}
	}
	cfg.Mode = normalizeMode(cfg.Mode)
	return cfg, nil
}

func getString(data map[string]any, key string) (string, bool) {
	v, ok := data[key]
	if !ok {
		return "", false
	}
	if s, ok := v.(string); ok {
		return s, true
	}
	return fmt.Sprintf("%v", v), true
}

func getStringSlice(data map[string]any, key string) ([]string, bool) {
	v, ok := data[key]
	if !ok {
		return nil, false
	}
	if ss, ok := v.([]string); ok {
		return ss, true
	}
	if vs, ok := v.([]any); ok {
		out := make([]string, 0, len(vs))
		for _, item := range vs {
			out = append(out, fmt.Sprintf("%v", item))
		}
		return out, true
	}
	return []string{fmt.Sprintf("%v", v)}, true
}

func getInt(data map[string]any, key string) (int, bool) {
	v, ok := data[key]
	if !ok {
		return 0, false
	}
	return getIntValue(v)
}

func getIntValue(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
			return i, true
		}
	}
	return 0, false
}

func getBool(data map[string]any, key string) (bool, bool) {
	v, ok := data[key]
	if !ok {
		return false, false
	}
	if b, ok := v.(bool); ok {
		return b, true
	}
	return false, false
}

func getMap(data map[string]any, key string) (map[string]any, bool) {
	v, ok := data[key]
	if !ok {
		return nil, false
	}
	if m, ok := v.(map[string]any); ok {
		return m, true
	}
	return nil, false
}

func (a *Actor) handleBrowserCrawlStart(ctx actor.Context, req gen.BrowserCrawlStartReq) (gen.BrowserCrawlStartResp, error) {
	if err := requireDeveloperOrManager(ctx.Identity().Role); err != nil {
		return gen.BrowserCrawlStartResp{}, err
	}
	cfg, err := resolveStartConfig(req)
	if err != nil {
		return gen.BrowserCrawlStartResp{}, fmt.Errorf("crawl.start: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return gen.BrowserCrawlStartResp{}, err
	}

	a.mu.Lock()
	id := fmt.Sprintf("crawl-%d", a.nextID)
	a.nextID++
	task := NewTask(id, cfg)
	a.tasks = append(a.tasks, task)
	if err := a.saveLocked(); err != nil {
		a.tasks = a.tasks[:len(a.tasks)-1]
		a.nextID--
		a.mu.Unlock()
		return gen.BrowserCrawlStartResp{}, fmt.Errorf("crawl.start: save failed: %w", err)
	}
	a.mu.Unlock()

	ctx.Logger().Info("crawl.start", "id", id, "mode", task.Config.Mode, "max_pages", task.Config.MaxPages)
	a.startEngine(ctx, task)
	return gen.BrowserCrawlStartResp{TaskID: id}, nil
}

// handleBrowserCrawlStatus is a stateless (PureContext) snapshot read: it
// reports task state/counts under a.mu without mutating state.
func (a *Actor) handleBrowserCrawlStatus(_ actor.PureContext, req gen.BrowserCrawlStatusReq) (gen.BrowserCrawlStatusResp, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.taskByIDLocked(req.TaskID)
	if t == nil {
		return gen.BrowserCrawlStatusResp{}, fmt.Errorf("crawl.status: task %q not found", req.TaskID)
	}
	return gen.BrowserCrawlStatusResp{
		State:    string(t.State),
		Crawled:  int32(len(t.CrawledURLs)),
		Frontier: int32(len(t.Frontier)),
		MaxPages: int32(t.Config.MaxPages),
		Error:    t.Error,
	}, nil
}

// handleBrowserCrawlResults is a stateless (PureContext) snapshot read: it
// pages through the collected results under a.mu without mutating state.
func (a *Actor) handleBrowserCrawlResults(_ actor.PureContext, req gen.BrowserCrawlResultsReq) (gen.BrowserCrawlResultsResp, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.taskByIDLocked(req.TaskID)
	if t == nil {
		return gen.BrowserCrawlResultsResp{}, fmt.Errorf("crawl.results: task %q not found", req.TaskID)
	}
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 50
	}
	cursor := int(req.Cursor)
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(t.Results) {
		cursor = len(t.Results)
	}
	end := cursor + limit
	if end > len(t.Results) {
		end = len(t.Results)
	}
	results := t.Results[cursor:end]
	done := t.State == StateDone || t.State == StateFailed || t.State == StateCancelled
	return gen.BrowserCrawlResultsResp{
		Results:    results,
		NextCursor: int32(end),
		Done:       done || end >= len(t.Results),
	}, nil
}

// handleBrowserCrawlHandoff opens a visible, user-controllable browser window
// on the same profile as the task's hidden crawl window. It is only valid when
// the task is in the awaiting_login state.
func (a *Actor) handleBrowserCrawlHandoff(ctx actor.Context, req gen.BrowserCrawlHandoffReq) (gen.BrowserCrawlHandoffResp, error) {
	if err := policy.RequireDeveloper(ctx.Identity().Role); err != nil {
		return gen.BrowserCrawlHandoffResp{}, err
	}
	a.mu.Lock()
	var task *Task
	for _, t := range a.tasks {
		if t.ID == req.TaskID {
			task = t
			break
		}
	}
	a.mu.Unlock()
	if task == nil {
		return gen.BrowserCrawlHandoffResp{}, fmt.Errorf("crawl.handoff: task %q not found", req.TaskID)
	}
	if task.State != StateAwaitingLogin {
		return gen.BrowserCrawlHandoffResp{}, fmt.Errorf("crawl.handoff: task %q is not awaiting_login (state=%s)", req.TaskID, task.State)
	}

	url := task.LoginWallURL
	if url == "" && len(task.Config.Seeds) > 0 {
		url = task.Config.Seeds[0]
	}
	instanceID := task.ID
	cfg := domain.BrowserInstanceConfig{
		ID:     instanceID,
		Name:   "crawl-handoff-" + task.ID,
		URL:    url,
		Open:   true,
		Hidden: false,
		Mode:   "window",
		State:  domain.BrowserWindowState{URL: url, Title: "crawl-handoff-" + task.ID, Width: 1024, Height: 768},
	}
	if _, err := a.createBrowserInstanceRaw(ctx, cfg); err != nil {
		return gen.BrowserCrawlHandoffResp{}, fmt.Errorf("crawl.handoff: %w", err)
	}
	ctx.Logger().Info("crawl: handoff", "id", task.ID, "url", url)
	return gen.BrowserCrawlHandoffResp{
		Accepted:   true,
		InstanceID: instanceID,
		URL:        url,
	}, nil
}



// createBrowserInstanceRaw is the low-level helper used by both the crawl engine
// and the handoff flow to talk to browsermanager.internal_create.
func (a *Actor) createBrowserInstanceRaw(ctx actor.Context, cfg domain.BrowserInstanceConfig) (domain.BrowserInstance, error) {
	mgrRef, ok := ctx.LookupService("browsermanager")
	if !ok {
		return domain.BrowserInstance{}, fmt.Errorf("browsermanager service not found")
	}
	stream := mgrRef.Invoke(ctx.Lifecycle(), "browsermanager.internal_create", cfg)
	if stream == nil {
		return domain.BrowserInstance{}, fmt.Errorf("browsermanager.internal_create returned nil stream")
	}
	defer stream.Close()
	raw, err := stream.Recv()
	if err != nil {
		return domain.BrowserInstance{}, fmt.Errorf("browsermanager.internal_create: %w", err)
	}
	inst, ok := raw.(domain.BrowserInstance)
	if !ok {
		return domain.BrowserInstance{}, fmt.Errorf("browsermanager.internal_create: unexpected response type %T", raw)
	}
	return inst, nil
}

// UpdateReq is the internal surface used by the browser driver to report a
// crawled page and push newly discovered URLs back into the frontier. Hand-written
// placeholder until the crawl schema is code-generated.
type UpdateReq struct {
	ID         string   `json:"id"`
	CrawledURL string   `json:"crawled_url"`
	Discovered []string `json:"discovered,omitempty"`
	State      State    `json:"state,omitempty"`
	Error      string   `json:"error,omitempty"`
}

func (a *Actor) handleUpdate(ctx actor.Context, req UpdateReq) (Task, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, t := range a.tasks {
		if t.ID != req.ID {
			continue
		}
		if req.CrawledURL != "" {
			t.RecordCrawl(req.CrawledURL, req.Discovered)
		}
		if req.State != "" && req.State != t.State {
			if err := t.Transition(req.State, req.Error); err != nil {
				return Task{}, err
			}
		}
		if err := a.saveLocked(); err != nil {
			return Task{}, fmt.Errorf("crawl.update: save failed: %w", err)
		}
		ctx.Logger().Info("crawl: update", "id", req.ID, "state", t.State, "cursor", t.ResultCursor)
		cp := *t
		return cp, nil
	}
	return Task{}, fmt.Errorf("crawl.update: task %q not found", req.ID)
}

// taskByIDLocked returns the task with the given ID, or nil. Caller must hold
// the actor mutex.
func (a *Actor) taskByIDLocked(id string) *Task {
	for _, t := range a.tasks {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// startEngine launches a background goroutine that drives the crawl task via
// browsermanager.use. It is idempotent: if an engine is already running for the
// task it does nothing.
func (a *Actor) startEngine(ctx actor.Context, task *Task) {
	if _, ok := ctx.LookupService("browsermanager"); !ok {
		ctx.Logger().Error("crawl: browsermanager not available, cannot start engine", "id", task.ID)
		return
	}
	a.enginesMu.Lock()
	if _, ok := a.engines[task.ID]; ok {
		a.enginesMu.Unlock()
		return
	}
	done := make(chan struct{})
	a.engines[task.ID] = &engine{cancel: func() { close(done) }}
	a.enginesMu.Unlock()

	go a.runTask(ctx, task, done)
}

// stopEngine cancels the background goroutine for the task, if any.
func (a *Actor) stopEngine(id string) {
	a.enginesMu.Lock()
	e, ok := a.engines[id]
	delete(a.engines, id)
	a.enginesMu.Unlock()
	if ok && e.cancel != nil {
		e.cancel()
	}
}

// runTask is the per-task execution engine. It creates a hidden browser
// instance, loops over the frontier, navigates to each URL, extracts data and
// discovers links, and persists progress after every page. It is resumed from
// persisted state after a process restart.
func (a *Actor) runTask(ctx actor.Context, task *Task, done <-chan struct{}) {
	defer func() {
		if r := recover(); r != nil {
			ctx.Logger().Error("crawl: runTask panic", "id", task.ID, "recover", r)
		}
		a.enginesMu.Lock()
		delete(a.engines, task.ID)
		a.enginesMu.Unlock()
	}()

	ctx.Logger().Info("crawl: runTask start", "id", task.ID)
	// A task may mount an existing browser instance (Config.InstanceID): the
	// engine then reuses that window instead of creating a task-dedicated
	// hidden one, and never removes or closes it — ownership stays with the
	// mount. An empty InstanceID keeps the create/remove lifecycle unchanged.
	reuseInstance := task.Config.InstanceID != ""
	instanceID := task.Config.InstanceID
	removeInstance := !reuseInstance
	defer func() {
		if removeInstance && instanceID != "" {
			a.removeBrowserInstance(ctx, instanceID)
		}
	}()

	if !reuseInstance {
		var err error
		instanceID, err = a.createBrowserInstance(ctx, task)
		if err != nil {
			a.markFailedLocked(task.ID, fmt.Sprintf("create browser instance: %v", err))
			return
		}
	}

	a.enginesMu.Lock()
	eng, ok := a.engines[task.ID]
	if !ok {
		a.enginesMu.Unlock()
		a.markFailedLocked(task.ID, "engine disappeared")
		return
	}
	a.enginesMu.Unlock()

	for {
		select {
		case <-done:
			return
		default:
		}

		if a.taskStateLocked(task.ID) != StateRunning {
			return
		}

		// Enforce the page cap before consuming another URL.
		if a.crawledCountLocked(task.ID) >= task.Config.MaxPages {
			a.markDoneLocked(task.ID)
			return
		}

		pageURL := a.popFrontierLocked(task.ID)
		if pageURL == "" {
			a.markDoneLocked(task.ID)
			return
		}

		// Rate-limit the interval between requests.
		a.rateLimit(eng, task.Config.RateLimit)

		// Navigate to the next URL.
		navResp, err := a.useBrowser(ctx, instanceID, browserusegen.BrowserUseReq{
			Action:       "navigate",
			URL:          pageURL,
			NavigateMode: "navigate",
		})
		if err != nil || !navResp.Success {
			if navResp.ErrorCode == "operator_not_bound" {
				a.markFailedLocked(task.ID, "operator_not_bound")
				return
			}
			a.markFailedLocked(task.ID, fmt.Sprintf("navigate %s: %s", pageURL, coalesceErr(err, navResp.Message)))
			return
		}
		eng.lastRequestAt = time.Now()

		// Extract the page state and links.
		obsResp, err := a.useBrowser(ctx, instanceID, browserusegen.BrowserUseReq{Action: "observe"})
		if err != nil || !obsResp.Success {
			a.markFailedLocked(task.ID, fmt.Sprintf("observe %s: %s", pageURL, coalesceErr(err, obsResp.Message)))
			return
		}
		obs := obsResp.Observation
		if obs == nil {
			a.markFailedLocked(task.ID, fmt.Sprintf("observe %s: nil observation", pageURL))
			return
		}

		// Login wall: suspend the task, release the hidden window, and wait for
		// the user to complete the handoff flow.
		if isLoginWall(obs) {
			if err := a.markAwaitingLoginLocked(task.ID, pageURL); err != nil {
				a.markFailedLocked(task.ID, fmt.Sprintf("awaiting_login transition: %v", err))
				return
			}
			removeInstance = false
			if !reuseInstance {
				// A mounted instance is user-owned; leave its window open.
				a.closeBrowserInstance(ctx, instanceID)
			}
			ctx.Logger().Info("crawl: login wall detected, awaiting handoff", "id", task.ID, "url", pageURL)
			return
		}

		discovered := extractLinks(obs)
		result := extractPageResult(obs, task.Config.ExtractSelectors)
		if err := a.recordCrawlResultLocked(task.ID, pageURL, discovered, result); err != nil {
			a.markFailedLocked(task.ID, fmt.Sprintf("record crawl: %v", err))
			return
		}
	}
}

// createBrowserInstance asks browsermanager for a hidden, task-dedicated
// browser instance. The instance ID is derived from the task ID so the profile
// directory (profileDir(instanceID)) is isolated per task/account.
func (a *Actor) createBrowserInstance(ctx actor.Context, task *Task) (string, error) {
	seedURL := ""
	if len(task.Config.Seeds) > 0 {
		seedURL = task.Config.Seeds[0]
	}
	instanceID := task.ID
	cfg := domain.BrowserInstanceConfig{
		ID:     instanceID,
		Name:   "crawl-" + task.ID,
		URL:    seedURL,
		Open:   true,
		Hidden: true,
		Mode:   "window",
		State:  domain.BrowserWindowState{URL: seedURL, Title: "crawl-" + task.ID, Width: 1024, Height: 768},
	}
	inst, err := a.createBrowserInstanceRaw(ctx, cfg)
	if err != nil {
		return "", err
	}
	return inst.Config.ID, nil
}

// removeBrowserInstance asks browsermanager to remove the task-dedicated
// browser instance and delete its profile directory.
func (a *Actor) removeBrowserInstance(ctx actor.Context, instanceID string) {
	mgrRef, ok := ctx.LookupService("browsermanager")
	if !ok {
		return
	}
	stream := mgrRef.Invoke(ctx.Lifecycle(), "browsermanager.internal_remove", domain.BrowserManagerRemoveReq{ID: instanceID})
	if stream == nil {
		return
	}
	defer stream.Close()
	_, _ = stream.Recv()
}

// closeBrowserInstance asks browsermanager to close the live window for an
// instance without deleting its profile. This releases the profile lock so the
// same profile can be reopened later (e.g. after login handoff).
func (a *Actor) closeBrowserInstance(ctx actor.Context, instanceID string) {
	mgrRef, ok := ctx.LookupService("browsermanager")
	if !ok {
		return
	}
	stream := mgrRef.Invoke(ctx.Lifecycle(), "browsermanager.internal_close", domain.BrowserManagerRemoveReq{ID: instanceID})
	if stream == nil {
		return
	}
	defer stream.Close()
	_, _ = stream.Recv()
}

// useBrowser calls browsermanager.use for a single action on the task instance.
func (a *Actor) useBrowser(ctx actor.Context, instanceID string, req browserusegen.BrowserUseReq) (browserusegen.BrowserUseResp, error) {
	mgrRef, ok := ctx.LookupService("browsermanager")
	if !ok {
		return browserusegen.BrowserUseResp{Success: false, ErrorCode: "browsermanager_not_found", Message: "browsermanager service not found"}, nil
	}
	req.InstanceID = instanceID
	stream := mgrRef.Invoke(ctx.Lifecycle(), "browsermanager.use", req)
	if stream == nil {
		return browserusegen.BrowserUseResp{Success: false, ErrorCode: "invoke_failed", Message: "browsermanager.use returned nil stream"}, nil
	}
	defer stream.Close()
	raw, err := stream.Recv()
	if err != nil {
		return browserusegen.BrowserUseResp{Success: false, ErrorCode: "invoke_failed", Message: err.Error()}, nil
	}
	resp, ok := raw.(browserusegen.BrowserUseResp)
	if !ok {
		return browserusegen.BrowserUseResp{Success: false, ErrorCode: "bad_response", Message: fmt.Sprintf("unexpected response type %T", raw)}, nil
	}
	return resp, nil
}

// taskStateLocked returns the current state of a task.
func (a *Actor) taskStateLocked(id string) State {
	a.mu.Lock()
	defer a.mu.Unlock()
	if t := a.taskByIDLocked(id); t != nil {
		return t.State
	}
	return StateFailed
}

// crawledCountLocked returns the number of already-crawled URLs for a task.
func (a *Actor) crawledCountLocked(id string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	if t := a.taskByIDLocked(id); t != nil {
		return len(t.CrawledURLs)
	}
	return 0
}

// popFrontierLocked removes and returns the next URL from the task frontier,
// persisting the updated state.
func (a *Actor) popFrontierLocked(id string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.taskByIDLocked(id)
	if t == nil {
		return ""
	}
	url := t.PopFrontier()
	if url != "" {
		_ = a.saveLocked()
	}
	return url
}

// recordCrawlResultLocked records a crawled page and its discovered links,
// extracts the result, and persists the task state.
func (a *Actor) recordCrawlResultLocked(id, pageURL string, discovered []string, result *gen.BrowserCrawlPageResult) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.taskByIDLocked(id)
	if t == nil {
		return fmt.Errorf("task %q not found", id)
	}
	t.RecordCrawlResult(pageURL, discovered, result)
	return a.saveLocked()
}

// markAwaitingLoginLocked transitions a task to awaiting_login and persists.
func (a *Actor) markAwaitingLoginLocked(id, pageURL string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.taskByIDLocked(id)
	if t == nil {
		return fmt.Errorf("task %q not found", id)
	}
	if err := t.Transition(StateAwaitingLogin, ""); err != nil {
		return err
	}
	t.LoginWallURL = pageURL
	return a.saveLocked()
}

// markDoneLocked transitions a task to done and persists.
func (a *Actor) markDoneLocked(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.taskByIDLocked(id)
	if t == nil {
		return fmt.Errorf("task %q not found", id)
	}
	if err := t.MarkDone(); err != nil {
		return err
	}
	return a.saveLocked()
}

// markFailedLocked transitions a task to failed with a reason and persists.
func (a *Actor) markFailedLocked(id, reason string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.taskByIDLocked(id)
	if t == nil {
		return fmt.Errorf("task %q not found", id)
	}
	if err := t.MarkFailed(reason); err != nil {
		return err
	}
	return a.saveLocked()
}

// rateLimit sleeps until the configured interval has elapsed since the last
// request. A rate_limit of 0 means no throttling.
func (a *Actor) rateLimit(eng *engine, seconds int) {
	if seconds <= 0 {
		return
	}
	interval := time.Duration(seconds) * time.Second
	if since := time.Since(eng.lastRequestAt); since < interval && !eng.lastRequestAt.IsZero() {
		time.Sleep(interval - since)
	}
}

// coalesceErr returns a message from an error or a response message.
func coalesceErr(err error, msg string) string {
	if err != nil {
		return err.Error()
	}
	return msg
}

// extractLinks collects href URLs from anchor elements in the observation.
func extractLinks(obs *browserusegen.BrowserPageObservation) []string {
	if obs == nil {
		return nil
	}
	var links []string
	for i := range obs.Elements {
		el := &obs.Elements[i]
		if el.TagName == "a" && el.Href != "" {
			links = append(links, el.Href)
		}
	}
	return links
}

// extractPageResult builds a BrowserCrawlPageResult from the page observation.
// If the crawl definition provides extract selectors (title/text) they are
// used; otherwise the page title and first h1 text are returned. The result
// shape is fixed to the v1 BrowserCrawlPageResult schema referenced by the
// crawl card's extract.schema.
func extractPageResult(obs *browserusegen.BrowserPageObservation, selectors map[string]any) *gen.BrowserCrawlPageResult {
	if obs == nil {
		return nil
	}
	res := &gen.BrowserCrawlPageResult{
		URL:   obs.URL,
		Title: obs.Title,
		Links: extractLinks(obs),
		Text:  firstElementText(obs, "h1"),
	}
	if len(selectors) == 0 {
		return res
	}
	if sel, ok := getString(selectors, "title"); ok && sel != "" {
		res.Title = firstElementText(obs, sel)
	}
	if sel, ok := getString(selectors, "text"); ok && sel != "" {
		res.Text = firstElementText(obs, sel)
	}
	return res
}

// isLoginWall uses a small deterministic heuristic to detect a page that is
// asking the user to authenticate. It is intentionally conservative: a page
// is only a login wall when it explicitly looks like one.
func isLoginWall(obs *browserusegen.BrowserPageObservation) bool {
	if obs == nil {
		return false
	}
	title := strings.ToLower(obs.Title)
	url := strings.ToLower(obs.URL)
	if strings.Contains(url, "/login") || strings.Contains(title, "login") || strings.Contains(title, "sign in") {
		return true
	}
	for _, el := range obs.Elements {
		if strings.ToLower(el.TagName) == "input" && strings.ToLower(el.InputType) == "password" {
			return true
		}
	}
	return false
}

func firstElementText(obs *browserusegen.BrowserPageObservation, selector string) string {
	el := findElement(obs, selector)
	if el == nil {
		return ""
	}
	return el.Text
}

func findElement(obs *browserusegen.BrowserPageObservation, selector string) *browserusegen.BrowserElement {
	if selector == "" {
		return nil
	}
	for i := range obs.Elements {
		el := &obs.Elements[i]
		if matchesSelector(el, selector) {
			return el
		}
	}
	return nil
}

func matchesSelector(el *browserusegen.BrowserElement, selector string) bool {
	if selector == "" {
		return false
	}
	if el.ID == selector || el.TagName == selector || el.Selector == selector {
		return true
	}
	return strings.Contains(el.Selector, selector)
}


