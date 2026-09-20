package project

import (
	"fmt"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// automationTemplatePrefix is the id convention for templates created by
// automation_bind. Using "tpl::<MapId>" makes the binding idempotent: binding
// the same map twice reuses the existing template rather than failing.
const automationTemplatePrefix = "tpl::"

// templateIDForMap derives the default template id for a source workflow map.
func templateIDForMap(mapID string) string {
	return automationTemplatePrefix + mapID
}

// workflowInstanceID derives a unique instance map id for a timer fire. The
// timestamp (second + nanosecond) guarantees that repeated fires of the same
// template — including two fires landing in the same second (manual trigger
// racing a scheduled fire) — produce distinct instance maps (kept as execution
// history). Second-only precision silently dropped the second fire with
// "card already exists".
func workflowInstanceID(tplID string) string {
	now := time.Now().UTC()
	return "inst::" + now.Format("20060102150405") + "-" + now.Format(".000000000")[1:] + "::" + tplID
}

// handleWikiAutomationBind snapshots a workflow map into a reusable template
// (via the template_save path) and binds that template to a scheduler card by
// stamping the template id into the scheduler card's frontmatter
// (workflow_template field). Subsequent timer fires then instantiate fresh
// workflow instances from the bound template instead of submitting the card
// body text. The scheduler card is the only place a workflow template can be
// bound.
//
// The binding is idempotent for a given source map: the template id is
// "tpl::<MapId>", so re-binding the same map reuses the existing template.
func (a *Actor) handleWikiAutomationBind(ctx actor.PureContext, req domain.WikiAutomationBindReq) (domain.WikiAutomationBindResp, error) {
	if req.MapID == "" {
		return domain.WikiAutomationBindResp{}, fmt.Errorf("project.wiki.automation_bind: MapId is required")
	}
	if req.SchedulerCardID == "" {
		return domain.WikiAutomationBindResp{}, fmt.Errorf("project.wiki.automation_bind: SchedulerCardId is required")
	}

	// Validate the scheduler card exists and is a scheduler card.
	scheduler, err := a.store.Get(req.SchedulerCardID)
	if err != nil {
		return domain.WikiAutomationBindResp{}, fmt.Errorf("project.wiki.automation_bind: scheduler card %q: %w", req.SchedulerCardID, err)
	}
	if !strings.HasPrefix(scheduler.Title, schedulerCardPrefix) {
		return domain.WikiAutomationBindResp{}, fmt.Errorf("project.wiki.automation_bind: %q is not a scheduler card (must start with %q)", req.SchedulerCardID, schedulerCardPrefix)
	}

	templateID := templateIDForMap(req.MapID)

	// Snapshot the map as a template. If the template already exists (re-bind
	// of the same map), reuse it instead of erroring.
	var templateMap domain.MonoCardListItem
	if existing, getErr := a.store.Get(templateID); getErr == nil {
		templateMap = cardToListItem(existing)
	} else {
		saveResp, saveErr := a.handleWikiTemplateSave(ctx, domain.WikiTemplateSaveReq{
			MapID:      req.MapID,
			TemplateID: templateID,
		})
		if saveErr != nil {
			return domain.WikiAutomationBindResp{}, fmt.Errorf("project.wiki.automation_bind: %w", saveErr)
		}
		templateMap = saveResp.TemplateMap
	}

	// Write the template id into the scheduler card's data: block, alongside
	// executor/reviewer. Stored in the data block (not frontmatter top-level)
	// so it round-trips through decodeCard → parseDataBlock into
	// CardRecord.Data, which is what the frontend scheduler editor reads (the
	// list view strips Raw, and top-level keys never reach Data). Re-read to
	// get the latest raw in case template_save emitted changes.
	scheduler, _ = a.store.Get(req.SchedulerCardID)
	now := time.Now().UTC().Format(time.RFC3339)
	updated := &CardRecord{Title: req.SchedulerCardID, Raw: setCardDataStringInRaw(scheduler.Raw, "workflow_template", templateID)}
	updated.Raw = ensureCardMeta(req.SchedulerCardID, updated.Raw, now)
	if err := validateCard(req.SchedulerCardID, updated.Raw); err != nil {
		return domain.WikiAutomationBindResp{}, fmt.Errorf("project.wiki.automation_bind: %w", err)
	}
	if err := a.store.Save(updated); err != nil {
		return domain.WikiAutomationBindResp{}, fmt.Errorf("project.wiki.automation_bind: save scheduler card: %w", err)
	}

	savedScheduler, _ := a.store.Get(req.SchedulerCardID)
	a.emitCardChanged(ctx, savedScheduler)

	return domain.WikiAutomationBindResp{
		TemplateID:    templateID,
		TemplateMap:   templateMap,
		SchedulerCard: cardToListItem(savedScheduler),
	}, nil
}

// handleWikiListTemplates returns every template map card in the project —
// workflow maps carrying data.template:true. Used by the scheduler editor's
// template selector to populate the dropdown of bindable templates.
func (a *Actor) handleWikiListTemplates(_ actor.PureContext, _ domain.WikiListTemplatesReq) (domain.WikiListTemplatesResp, error) {
	cards, err := a.store.List()
	if err != nil {
		return domain.WikiListTemplatesResp{}, fmt.Errorf("project.wiki.list_templates: %w", err)
	}
	var templates []domain.MonoCardListItem
	for _, card := range cards {
		if card.Type == "workflow" && cardIsTemplate(card) {
			templates = append(templates, cardToListItem(card))
		}
	}
	return domain.WikiListTemplatesResp{Templates: templates}, nil
}
