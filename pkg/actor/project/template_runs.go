package project

import (
	"fmt"
	"sort"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// templateRunLog is the persisted shape of all template instantiation runs for
// a project. It is saved under the name "template-runs" via the project's
// persistStore (persist.Persist interface), satisfying the actor-state rule
// that durable state must go through the persist layer.
type templateRunLog struct {
	Runs []templateRunEntry `json:"runs"`
}

type templateRunEntry struct {
	TemplateMapID   string `json:"templateMapId"`
	InstanceMapID   string `json:"instanceMapId"`
	SchedulerCardID string `json:"schedulerCardId,omitempty"`
	Source          string `json:"source"`
	StartedAt       string `json:"startedAt"`
}

func (e templateRunEntry) toGen() gen.TemplateRunRecord {
	return gen.TemplateRunRecord{
		InstanceMapID:   e.InstanceMapID,
		SchedulerCardID: e.SchedulerCardID,
		Source:          e.Source,
		StartedAt:       e.StartedAt,
	}
}

const templateRunsPersistName = "template-runs"

// loadTemplateRuns reads the persisted run log through persist.Persist and
// groups runs by template map id, newest first.
func (a *Actor) loadTemplateRuns() (map[string][]templateRunEntry, error) {
	var log templateRunLog
	if err := persist.LoadOrZero(a.persistStore, templateRunsPersistName, &log); err != nil {
		return nil, fmt.Errorf("load template runs: %w", err)
	}
	out := make(map[string][]templateRunEntry, len(log.Runs))
	for _, e := range log.Runs {
		out[e.TemplateMapID] = append(out[e.TemplateMapID], e)
	}
	// Keep newest-first per template so callers don't have to sort.
	for k := range out {
		sort.Slice(out[k], func(i, j int) bool {
			return out[k][i].StartedAt > out[k][j].StartedAt
		})
	}
	return out, nil
}

// saveTemplateRuns writes the in-memory run log back through persist.Persist.
func (a *Actor) saveTemplateRuns(runs map[string][]templateRunEntry) error {
	log := templateRunLog{Runs: make([]templateRunEntry, 0)}
	for _, list := range runs {
		log.Runs = append(log.Runs, list...)
	}
	// Persist in chronological order to keep the file stable and readable.
	sort.Slice(log.Runs, func(i, j int) bool {
		return log.Runs[i].StartedAt < log.Runs[j].StartedAt
	})
	if err := a.persistStore.Save(templateRunsPersistName, log); err != nil {
		return fmt.Errorf("save template runs: %w", err)
	}
	return nil
}

// recordTemplateRun appends a run entry for the given template. It is safe
// to call with an empty source or scheduler id (manual instantiations leave
// those fields empty). Serialized by templateRunsMu because the load-modify-
// save cycle is a read-modify-write over the persisted log.
func (a *Actor) recordTemplateRun(templateMapID, instanceMapID, schedulerCardID, source string) error {
	if templateMapID == "" || instanceMapID == "" {
		return nil
	}
	if source == "" {
		source = "manual"
	}
	a.templateRunsMu.Lock()
	defer a.templateRunsMu.Unlock()
	runs, err := a.loadTemplateRuns()
	if err != nil {
		return err
	}
	entry := templateRunEntry{
		TemplateMapID:   templateMapID,
		InstanceMapID:   instanceMapID,
		SchedulerCardID: schedulerCardID,
		Source:          source,
		StartedAt:       time.Now().UTC().Format(time.RFC3339),
	}
	runs[templateMapID] = append([]templateRunEntry{entry}, runs[templateMapID]...)
	return a.saveTemplateRuns(runs)
}

// removeTemplateRunsByInstance removes all run log entries whose InstanceMapID
// is in the given set. Called from cascadeDeleteSchedulerInstances to clean up
// run log entries alongside instance map deletion. Caller must NOT hold
// templateRunsMu (this method acquires it).
func (a *Actor) removeTemplateRunsByInstance(instanceMapIDs []string) error {
	if len(instanceMapIDs) == 0 {
		return nil
	}
	kill := make(map[string]bool, len(instanceMapIDs))
	for _, id := range instanceMapIDs {
		kill[id] = true
	}
	a.templateRunsMu.Lock()
	defer a.templateRunsMu.Unlock()
	runs, err := a.loadTemplateRuns()
	if err != nil {
		return fmt.Errorf("load template runs for removal: %w", err)
	}
	changed := false
	for tplID, entries := range runs {
		filtered := make([]templateRunEntry, 0, len(entries))
		for _, e := range entries {
			if !kill[e.InstanceMapID] {
				filtered = append(filtered, e)
			}
		}
		if len(filtered) == 0 {
			delete(runs, tplID)
			changed = true
		} else if len(filtered) < len(entries) {
			runs[tplID] = filtered
			changed = true
		}
	}
	if changed {
		return a.saveTemplateRuns(runs)
	}
	return nil
}

// handleWikiListTemplateRuns returns the most recent instantiation runs of a
// template map, newest first, capped at Limit.
func (a *Actor) handleWikiListTemplateRuns(_ actor.PureContext, req domain.WikiListTemplateRunsReq) (domain.WikiListTemplateRunsResp, error) {
	if req.TemplateMapID == "" {
		return domain.WikiListTemplateRunsResp{}, fmt.Errorf("project.wiki.list_template_runs: TemplateMapId is required")
	}
	a.templateRunsMu.Lock()
	runs, err := a.loadTemplateRuns()
	a.templateRunsMu.Unlock()
	if err != nil {
		return domain.WikiListTemplateRunsResp{}, fmt.Errorf("project.wiki.list_template_runs: load runs: %w", err)
	}
	list := runs[req.TemplateMapID]
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 10
	}
	if len(list) > limit {
		list = list[:limit]
	}
	resp := domain.WikiListTemplateRunsResp{Runs: make([]gen.TemplateRunRecord, 0, len(list))}
	for _, e := range list {
		resp.Runs = append(resp.Runs, e.toGen())
	}
	return resp, nil
}
