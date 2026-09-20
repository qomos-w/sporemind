// Package interfacemanager is a stateful actor that lets agents drive the
// omnibox surfaces (content view, settings panel, active project, focused
// agent, interactive guides) via a single callable emitting
// interface_manager_event, which the frontend subscribes to and dispatches
// to the matching omnibox handlers. It also records UI interactions in a
// ring buffer and provides query capabilities.
//
// Topology:
//
//	/interfacemanager   # this actor; owns the interface_manager_event stream
package interfacemanager

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qomos-w/gospore/actor"

	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
)

const (
	eventKind             = "interface_manager_event"
	interactionBufferSize = 500
	defaultQueryLimit     = 50
	maxQueryLimit         = 200
	maxTutorials          = 32
	maxTutorialSteps      = 12
)

// Actor owns the interface_manager_event stream and interaction ring buffer.
// It implements persist.Persistent so the ring buffer survives process restarts.
type Actor struct {
	actor.Host
	store persist.Persist
	// stateLoaded is set once Load() has completed (first start included);
	// OnStop skips the save before that (gospore ForceCleanup may stop us
	// mid-OnInit, and saving an empty ring would wipe the record).
	stateLoaded     atomic.Bool
	actorID         string
	interactions    []domain.UiInteractionRecord
	interactionsMu  sync.Mutex
	interactionsIdx int // next write position in the ring buffer
	tutorials       map[string]domain.TutorialSpec
	tutorialsMu     sync.Mutex
}

var _ persist.Persistent = (*Actor)(nil)

// interactionSnapshot is the persisted state for the ring buffer and the
// dynamic tutorial library.
type interactionSnapshot struct {
	Interactions    []domain.UiInteractionRecord
	InteractionsIdx int                   `json:"interactionsIdx"`
	Tutorials       []domain.TutorialSpec `json:"Tutorials,omitempty"`
}

func (a *Actor) Type() string { return "interfacemanager" }

func (a *Actor) OnInit(ctx actor.Context) error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("interfacemanager"))
		if err != nil {
			return err
		}
	}
	a.actorID = ctx.Self().ID().String()
	if err := a.Load(); err != nil {
		ctx.Logger().Error("interfacemanager: load state failed", "error", err)
	}
	return nil
}

func (a *Actor) OnStart(ctx actor.Context) error {
	ctx.Logger().Info("interfacemanager: starting", "id", ctx.Self().ID().String())

	// interaction_ops is the dedicated stateful lane for report_interaction:
	// it appends to the persisted interaction ring buffer and writes the
	// snapshot to disk, so it cannot be stateless; pinning it to its own lane
	// keeps that write off the owner lane while query_interactions (PureContext)
	// serves reads from the forked pure loop.
	if err := ctx.RegisterLoop("interaction_ops", actor.ModeStateful); err != nil {
		return fmt.Errorf("interfacemanager: register interaction_ops loop: %w", err)
	}

	if err := ctx.Register("interfacemanager.control", a.handleControl, actor.Public(),
		actor.WithDescription("Drive the interface via an Action parameter: set_view (Mode: conversation|topology|files|problems|notes|ssh|multiconsole), open_settings (Category), switch_project (ProjectId), focus_agent (AgentId), anchor_catalog (list addressable UI anchors before composing a guide), show_guide (Steps with whitelist-validated TargetGuideId), hide_guide, interact (GuideId + Interaction: click|focus|input|scroll_into_view on data-guide-id annotated elements), create_tutorial (Title + Steps, optional TutorialId slug / Description / AutoPlay; upserts into the persisted tutorial library, max 32 tutorials of ≤12 steps each), delete_tutorial (TutorialId), tutorial_catalog (list persisted tutorials, no event). Fire-and-forget: verify effects via query_interactions."),
	); err != nil {
		return fmt.Errorf("interfacemanager: register control: %w", err)
	}

	if err := ctx.Register("interfacemanager.report_interaction", a.handleReportInteraction, actor.Public(),
		actor.WithLoop("interaction_ops"),
	); err != nil {
		return fmt.Errorf("interfacemanager: register report_interaction: %w", err)
	}

	if err := ctx.Register("interfacemanager.query_interactions", a.handleQueryInteractions, actor.Public(),
		actor.WithDescription("Read user (or remote-action) UI interactions from the 500-entry ring buffer. Optional filters: Since (RFC3339), GuideId, Kind (click|input|navigate|guide_error|remote_ack|guide_progress), Limit (default 50, max 200). Returns records newest-first. Use to verify the effect of interfacemanager.control actions."),
	); err != nil {
		return fmt.Errorf("interfacemanager: register query_interactions: %w", err)
	}

	if err := ctx.RegisterEventKind(eventKind, domain.InterfaceManagerEvent{}, actor.Public()); err != nil {
		return fmt.Errorf("interfacemanager: register event kind %s: %w", eventKind, err)
	}

	if err := ctx.RegisterDomain("interfacemanager").Expose(); err != nil {
		return fmt.Errorf("interfacemanager: expose: %w", err)
	}

	return nil
}

func (a *Actor) OnStop(ctx actor.Context) error {
	if !a.stateLoaded.Load() {
		// Stopped mid-OnInit (gospore ForceCleanup): skip the wipe-prone save.
		return nil
	}
	return a.Save()
}

// Save snapshots the ring buffer contents.
func (a *Actor) Save() error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("interfacemanager"))
		if err != nil {
			return err
		}
	}
	a.interactionsMu.Lock()
	snapshot := interactionSnapshot{
		Interactions:    append([]domain.UiInteractionRecord(nil), a.interactions...),
		InteractionsIdx: a.interactionsIdx,
	}
	a.interactionsMu.Unlock()
	a.tutorialsMu.Lock()
	snapshot.Tutorials = a.tutorialCatalogLocked()
	a.tutorialsMu.Unlock()
	return a.store.Save(a.actorID, snapshot)
}

// Load restores the ring buffer from persistent state.
func (a *Actor) Load() (err error) {
	// Mark state as load-complete only after a successful pass (first start
	// included) — the OnStop wipe guard depends on it.
	defer func() {
		if err == nil {
			a.stateLoaded.Store(true)
		}
	}()
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("interfacemanager"))
		if err != nil {
			return err
		}
	}
	var snapshot interactionSnapshot
	if err := persist.LoadOrZero(a.store, a.actorID, &snapshot); err != nil {
		return err
	}
	a.interactions = snapshot.Interactions
	a.interactionsIdx = snapshot.InteractionsIdx
	a.tutorialsMu.Lock()
	a.tutorials = make(map[string]domain.TutorialSpec, len(snapshot.Tutorials))
	for _, t := range snapshot.Tutorials {
		a.tutorials[t.TutorialID] = t
	}
	a.tutorialsMu.Unlock()
	return nil
}

func (a *Actor) saveOrLog(ctx actor.Context) {
	if err := a.Save(); err != nil {
		ctx.Logger().Error("interfacemanager: save state failed", "error", err)
	}
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func (a *Actor) handleControl(ctx actor.Context, req domain.InterfaceManagerControlReq) (domain.InterfaceManagerControlResp, error) {
	// Validation failures are business errors, not transport errors: return a
	// structured resp (Accepted=false) with nil error so the LLM receives the
	// JSON result rather than an unstructured IsError string.
	if err := validateControl(a, req); err != nil {
		return domain.InterfaceManagerControlResp{Accepted: false, Error: err.Error()}, nil
	}

	event := domain.InterfaceManagerEvent{
		Action:      req.Action,
		Mode:        req.Mode,
		Category:    req.Category,
		ProjectID:   req.ProjectID,
		AgentID:     req.AgentID,
		Steps:       req.Steps,
		Interaction: req.Interaction,
		Text:        req.Text,
		GuideID:     req.GuideID,
		AppID:       req.AppID,
		ViewID:      req.ViewID,
	}
	resp := domain.InterfaceManagerControlResp{Accepted: true}

	// Emit is fire-and-forget; a publish failure (e.g. no subscribers yet) is
	// not fatal — mirror browsermanager which ignores the emit error.
	switch req.Action {
	case "create_tutorial":
		spec := a.upsertTutorial(req)
		a.saveOrLog(ctx)
		event.Tutorial = &spec
		event.TutorialID = spec.TutorialID
		event.AutoPlay = req.AutoPlay
		resp.TutorialID = spec.TutorialID
		_ = ctx.EmitEvent(eventKind, event)
	case "delete_tutorial":
		a.deleteTutorial(req.TutorialID)
		a.saveOrLog(ctx)
		event.TutorialID = req.TutorialID
		_ = ctx.EmitEvent(eventKind, event)
	case "tutorial_catalog":
		// Synchronous response only (mirrors anchor_catalog's resp pattern);
		// the catalog is persisted state, not a UI effect, so no event.
		resp.Tutorials = a.tutorialCatalog()
	default:
		if req.Action == "anchor_catalog" {
			resp.Anchors = GuideAnchorCatalog()
		}
		_ = ctx.EmitEvent(eventKind, event)
	}
	return resp, nil
}

// tutorialCatalogLocked returns the persisted tutorials sorted by TutorialId.
// Caller must hold a.tutorialsMu.
func (a *Actor) tutorialCatalogLocked() []domain.TutorialSpec {
	out := make([]domain.TutorialSpec, 0, len(a.tutorials))
	for _, t := range a.tutorials {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TutorialID < out[j].TutorialID })
	return out
}

// tutorialCatalog returns a copy of the persisted tutorial library.
func (a *Actor) tutorialCatalog() []domain.TutorialSpec {
	a.tutorialsMu.Lock()
	defer a.tutorialsMu.Unlock()
	return a.tutorialCatalogLocked()
}

// upsertTutorial stores the tutorial from req under its TutorialId, generating
// a slug from Title when TutorialId is omitted, and returns the stored spec.
// Callers must have passed validateControl (Title/Steps/slug/library-cap).
func (a *Actor) upsertTutorial(req domain.InterfaceManagerControlReq) domain.TutorialSpec {
	a.tutorialsMu.Lock()
	defer a.tutorialsMu.Unlock()
	if a.tutorials == nil {
		a.tutorials = make(map[string]domain.TutorialSpec)
	}
	id := req.TutorialID
	if id == "" {
		id = generateTutorialID(req.Title, a.tutorials)
	}
	spec := domain.TutorialSpec{
		TutorialID:  id,
		Title:       req.Title,
		Description: req.Description,
		Steps:       append([]domain.GuideStep(nil), req.Steps...),
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	a.tutorials[id] = spec
	return spec
}

// deleteTutorial removes the tutorial by id. Callers must have passed
// validateControl (id required and present).
func (a *Actor) deleteTutorial(id string) {
	a.tutorialsMu.Lock()
	delete(a.tutorials, id)
	a.tutorialsMu.Unlock()
}

// tutorialSlugPattern is the allowed TutorialId grammar: lowercase slug of
// letters, digits and single hyphens (e.g. "my-tutorial-2").
var tutorialSlugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// reservedTutorialIDs are the builtin tutorial category ids; dynamic tutorials
// must not shadow them.
var reservedTutorialIDs = map[string]struct{}{
	"basics":   {},
	"workflow": {},
	"tools":    {},
	"settings": {},
}

// generateTutorialID derives a unique slug from the tutorial title.
func generateTutorialID(title string, existing map[string]domain.TutorialSpec) string {
	base := slugify(title)
	if base == "" {
		base = "tutorial"
	}
	id := base
	for i := 2; ; i++ {
		if _, ok := existing[id]; !ok {
			if _, reserved := reservedTutorialIDs[id]; !reserved {
				return id
			}
		}
		id = fmt.Sprintf("%s-%d", base, i)
	}
}

// slugify reduces a title to a lowercase hyphen slug.
func slugify(title string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case !lastDash && b.Len() > 0:
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// validateControl enforces the (action → required param) contract documented in
// the .spore enum comments, since the DSL has no first-class enum support.
// a may be nil (pure static checks); stateful checks (library cap, delete
// existence) are skipped when a is nil.
func validateControl(a *Actor, req domain.InterfaceManagerControlReq) error {
	switch req.Action {
	case "set_view":
		if req.Mode == "" {
			return fmt.Errorf("interfacemanager.control: action %q requires Mode", req.Action)
		}
		if _, ok := validViewModes[req.Mode]; !ok {
			return fmt.Errorf("interfacemanager.control: invalid Mode %q", req.Mode)
		}
	case "open_settings":
		if req.Category == "" {
			return fmt.Errorf("interfacemanager.control: action %q requires Category", req.Action)
		}
		if _, ok := validSettingCategories[req.Category]; !ok {
			return fmt.Errorf("interfacemanager.control: invalid Category %q", req.Category)
		}
	case "switch_project":
		if req.ProjectID == "" {
			return fmt.Errorf("interfacemanager.control: action %q requires ProjectId", req.Action)
		}
	case "focus_agent":
		if req.AgentID == "" {
			return fmt.Errorf("interfacemanager.control: action %q requires AgentId", req.Action)
		}
	case "request_plugin_dom_snapshot":
		if req.AppID == "" {
			return fmt.Errorf("interfacemanager.control: action %q requires AppId", req.Action)
		}
	case "show_guide":
		if len(req.Steps) == 0 {
			return fmt.Errorf("interfacemanager.control: action %q requires Steps", req.Action)
		}
		if err := validateGuideSteps(req.Action, req.Steps); err != nil {
			return err
		}
	case "hide_guide":
		// No additional parameters required.
	case "anchor_catalog":
		// No parameters required; the response carries Resp.Anchors.
	case "interact":
		if req.GuideID == "" {
			return fmt.Errorf("interfacemanager.control: action %q requires GuideId", req.Action)
		}
		if req.Interaction == "" {
			return fmt.Errorf("interfacemanager.control: action %q requires Interaction", req.Action)
		}
		if _, ok := validInteractionKinds[req.Interaction]; !ok {
			return fmt.Errorf("interfacemanager.control: invalid Interaction %q", req.Interaction)
		}
		// Text is required only when Interaction is "input".
		if req.Interaction == "input" && req.Text == "" {
			return fmt.Errorf("interfacemanager.control: action %q with Interaction %q requires Text", req.Action, req.Interaction)
		}
	case "open_app_view":
		// App existence and view resolution are validated by appmanager.open_view;
		// this layer only requires the target app id. ViewId is optional (first view).
		if req.AppID == "" {
			return fmt.Errorf("interfacemanager.control: action %q requires AppId", req.Action)
		}
	case "create_tutorial":
		if req.Title == "" {
			return fmt.Errorf("interfacemanager.control: action %q requires Title", req.Action)
		}
		if len(req.Steps) == 0 {
			return fmt.Errorf("interfacemanager.control: action %q requires Steps", req.Action)
		}
		if len(req.Steps) > maxTutorialSteps {
			return fmt.Errorf("interfacemanager.control: action %q allows at most %d steps, got %d", req.Action, maxTutorialSteps, len(req.Steps))
		}
		if err := validateGuideSteps(req.Action, req.Steps); err != nil {
			return err
		}
		if req.TutorialID != "" {
			if !tutorialSlugPattern.MatchString(req.TutorialID) {
				return fmt.Errorf("interfacemanager.control: action %q: invalid TutorialId %q (lowercase slug: letters, digits, single hyphens)", req.Action, req.TutorialID)
			}
			if _, reserved := reservedTutorialIDs[req.TutorialID]; reserved {
				return fmt.Errorf("interfacemanager.control: action %q: TutorialId %q is reserved for the builtin category", req.Action, req.TutorialID)
			}
		}
		if a != nil {
			a.tutorialsMu.Lock()
			_, exists := a.tutorials[req.TutorialID]
			count := len(a.tutorials)
			a.tutorialsMu.Unlock()
			if !exists && count >= maxTutorials {
				return fmt.Errorf("interfacemanager.control: action %q: tutorial library is full (%d max); delete a tutorial first", req.Action, maxTutorials)
			}
		}
	case "delete_tutorial":
		if req.TutorialID == "" {
			return fmt.Errorf("interfacemanager.control: action %q requires TutorialId", req.Action)
		}
		if a != nil {
			a.tutorialsMu.Lock()
			_, exists := a.tutorials[req.TutorialID]
			a.tutorialsMu.Unlock()
			if !exists {
				return fmt.Errorf("interfacemanager.control: action %q: no tutorial with TutorialId %q (call action %q to list existing tutorials)", req.Action, req.TutorialID, "tutorial_catalog")
			}
		}
	case "tutorial_catalog":
		// No parameters required; the response carries Resp.Tutorials and no
		// event is emitted (mirrors anchor_catalog's resp-only additions).
	default:
		return fmt.Errorf("interfacemanager.control: unknown Action %q", req.Action)
	}
	return nil
}

// validGuidePlacements mirrors the Placement vocabulary documented on
// GuideStep (top/bottom/left/right/auto); empty means auto-anchor.
var validGuidePlacements = map[string]struct{}{
	"top":    {},
	"bottom": {},
	"left":   {},
	"right":  {},
	"auto":   {},
}

// validateGuideSteps hardens the show_guide / create_tutorial step contract:
// every step must target a known anchor (whitelist from GuideAnchorCatalog),
// and the optional Placement / ExpectedInteraction fields must match their
// grammars. Errors carry the action and step index so the LLM can locate the
// offending step.
func validateGuideSteps(action string, steps []domain.GuideStep) error {
	anchors := guideAnchorSet()
	for i, step := range steps {
		if _, ok := anchors[step.TargetGuideID]; !ok {
			return fmt.Errorf("interfacemanager.control: %s step %d: unknown TargetGuideId %q (call action %q to list valid anchors)",
				action, i, step.TargetGuideID, "anchor_catalog")
		}
		if step.Placement != "" {
			if _, ok := validGuidePlacements[step.Placement]; !ok {
				return fmt.Errorf("interfacemanager.control: %s step %d: invalid Placement %q (one of top, bottom, left, right, auto)",
					action, i, step.Placement)
			}
		}
		if step.ExpectedInteraction != "" && !validExpectedInteraction(step.ExpectedInteraction) {
			return fmt.Errorf("interfacemanager.control: %s step %d: invalid ExpectedInteraction %q (use click:<guide-id> | text:<substring> | submit, comma-separated unions allowed)",
				action, i, step.ExpectedInteraction)
		}
	}
	return nil
}

// validExpectedInteraction checks the ExpectedInteraction grammar:
// "click:<guide-id>" | "text:<substring>" | "submit", or a comma-separated
// union of those forms (any alternative satisfies the gate).
func validExpectedInteraction(spec string) bool {
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		switch {
		case part == "submit":
		case strings.HasPrefix(part, "click:") && strings.TrimSpace(strings.TrimPrefix(part, "click:")) != "":
		case strings.HasPrefix(part, "text:") && strings.TrimSpace(strings.TrimPrefix(part, "text:")) != "":
		default:
			return false
		}
	}
	return true
}

// handleReportInteraction records a user interaction in the ring buffer and
// persists it. State-writing: it mutates a.interactions and writes the
// snapshot to disk, so it runs on the dedicated "interaction_ops" stateful
// lane (see OnStart) rather than the owner lane.
func (a *Actor) handleReportInteraction(ctx actor.Context, req domain.ReportInteractionReq) (domain.ReportInteractionResp, error) {
	record := domain.UiInteractionRecord{
		ID:      fmt.Sprintf("uir-%d", time.Now().UnixNano()),
		Ts:      time.Now().UTC().Format(time.RFC3339Nano),
		Kind:    req.Kind,
		GuideID: req.GuideID,
		Label:   req.Label,
		View:    req.View,
		Detail:  req.Detail,
	}

	a.interactionsMu.Lock()
	// Initialize ring buffer on first write.
	if a.interactions == nil {
		a.interactions = make([]domain.UiInteractionRecord, interactionBufferSize)
	}
	// Write to ring buffer and advance index.
	a.interactions[a.interactionsIdx%interactionBufferSize] = record
	a.interactionsIdx++
	a.interactionsMu.Unlock()

	a.saveOrLog(ctx)
	return domain.ReportInteractionResp{Accepted: true}, nil
}

// handleQueryInteractions returns interaction records matching the filter criteria.
// handleQueryInteractions returns interaction records matching the filter criteria.
// Stateless (PureContext): it reads the ring buffer under interactionsMu
// without mutating state, so it runs on the forked pure loop.
func (a *Actor) handleQueryInteractions(_ actor.PureContext, req domain.QueryInteractionsReq) (domain.QueryInteractionsResp, error) {
	limit := req.Limit
	if limit == 0 {
		limit = defaultQueryLimit
	}
	if limit < 0 {
		limit = 0
	}
	if limit > maxQueryLimit {
		limit = maxQueryLimit
	}

	a.interactionsMu.Lock()
	// Build a copy of all records in reverse chronological order.
	// Records are stored in ring buffer order (oldest to newest wrapping around).
	count := a.interactionsIdx
	if count > interactionBufferSize {
		count = interactionBufferSize
	}
	if count == 0 {
		a.interactionsMu.Unlock()
		return domain.QueryInteractionsResp{Items: []domain.UiInteractionRecord{}}, nil
	}

	// Collect records from newest to oldest.
	records := make([]domain.UiInteractionRecord, 0, count)
	for i := a.interactionsIdx - 1; i >= 0 && len(records) < interactionBufferSize; i-- {
		idx := i % interactionBufferSize
		if idx >= len(a.interactions) {
			continue
		}
		record := a.interactions[idx]
		// Skip zero-value records (never written).
		if record.ID == "" {
			continue
		}

		// Apply filters.
		if req.Since != "" && record.Ts < req.Since {
			continue
		}
		if req.GuideID != "" && record.GuideID != req.GuideID {
			continue
		}
		if req.Kind != "" && record.Kind != req.Kind {
			continue
		}

		records = append(records, record)
	}
	a.interactionsMu.Unlock()

	// Apply limit.
	if int(limit) < len(records) {
		records = records[:limit]
	}

	return domain.QueryInteractionsResp{Items: records}, nil
}
