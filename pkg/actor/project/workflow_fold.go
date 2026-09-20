package project

import (
	"time"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// Server-side port of the frontend workflow epic-tree fold semantics
// (web/src/ui/ai/components/workflowLayout.ts). The frontend computes virtual
// category bands and archived time buckets purely for layout; this port lets
// wiki.list_all_cards filter hidden maps and their task cards out of the
// response itself, so folded subtrees are never shipped to the client.

const (
	archiveAfter          = 7 * 24 * time.Hour
	archiveDateBucketDays = 30
	archiveMonthBucketDay = 365
	epicBucketPrefix      = "__epic_bucket__:"
)

type epicCategory string

const (
	catInProgress epicCategory = "in-progress"
	catToStart    epicCategory = "to-start"
	catTemplate   epicCategory = "template"
	catPlanned    epicCategory = "planned"
	catCompleted  epicCategory = "completed"
	catArchived   epicCategory = "archived"
)

// workflowTaskIDs mirrors workflowTaskIds: data.scope.include, data.include,
// plus task cards parented to the map.
func workflowTaskIDs(mapCard domain.MonoCardListItem, cards []domain.MonoCardListItem) []string {
	var ids []string
	seen := map[string]bool{}
	push := func(v any) {
		arr, ok := v.([]any)
		if !ok {
			return
		}
		for _, item := range arr {
			id, _ := item.(string)
			if id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	if scope, ok := mapCard.Data["scope"].(map[string]any); ok {
		push(scope["include"])
	}
	push(mapCard.Data["include"])
	for _, c := range cards {
		if c.Type == "task" && c.Parent == mapCard.ID && !seen[c.ID] {
			seen[c.ID] = true
			ids = append(ids, c.ID)
		}
	}
	return ids
}

// workflowOwnerAgentID mirrors workflowOwnerAgentId.
func workflowOwnerAgentID(card domain.MonoCardListItem) string {
	id, _ := card.Data["ownerAgentId"].(string)
	return id
}

// workflowLastActivity mirrors workflowLastActivity: latest Modified across
// the map and its task cards (string compare — timestamps share one format).
func workflowLastActivity(mapCard domain.MonoCardListItem, cards []domain.MonoCardListItem) string {
	latest := mapCard.Modified
	byID := make(map[string]string, len(cards))
	for _, c := range cards {
		byID[c.ID] = c.Modified
	}
	for _, taskID := range workflowTaskIDs(mapCard, cards) {
		if m := byID[taskID]; m > latest {
			latest = m
		}
	}
	return latest
}

// parseCardTime parses a card timestamp the way Date.parse does for the
// formats this project writes (RFC3339 / RFC3339 nano with offset).
func parseCardTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// archivedBucketID computes the virtual time-bucket id for an archived map.
// Bucket boundaries follow the display layer: local calendar day/month/year.
// The server's local zone is used — in the desktop form factor server and
// client share one machine, matching the frontend exactly.
func archivedBucketID(lastActivity time.Time, now time.Time) string {
	ageDays := now.Sub(lastActivity).Hours() / 24
	local := lastActivity.Local()
	switch {
	case ageDays < archiveDateBucketDays:
		return epicBucketPrefix + "date:" + local.Format("2006-01-02")
	case ageDays < archiveMonthBucketDay:
		return epicBucketPrefix + "month:" + local.Format("2006-01")
	default:
		return epicBucketPrefix + "year:" + local.Format("2006")
	}
}

// classifyWorkflowCategory mirrors classifyWorkflowCategory
// (web/src/ui/ai/components/workflowLayout.ts:317-347). The scheduler→planned
// branch is kept for mirror completeness even though filterWorkflowFoldVisible
// only classifies workflow cards; the Go filter never sees scheduler cards
// passed to classification directly.
func classifyWorkflowCategory(
	card domain.MonoCardListItem,
	agentIDs map[string]bool,
	all []domain.MonoCardListItem,
	now time.Time,
) epicCategory {
	var base epicCategory
	switch {
	case card.Type == "scheduler":
		base = catPlanned
	case card.Standalone || card.Data["template"] == true:
		// data.template:true is what project.wiki.template_save stamps on
		// saved template maps (id tpl::<mapId>, status doing); without this
		// check they would land in to-start/in-progress instead of the
		// template band.
		base = catTemplate
	default:
		// Mirrors the TS truth source (workflowLayout.ts classifyWorkflowCategory):
		// a live owner makes the map in-progress REGARDLESS of card status —
		// workflow_start/activateWorkflow binds ownerAgentId without stamping
		// status=doing (only scheduler instances are created doing,
		// wiki_template.go), so a running map may legitimately carry
		// todo/empty status. Status-gating in-progress on doing/
		// pending_review/blocked made this port classify such maps to-start
		// and drop their task cards from list responses whenever the to-start
		// band was folded, while the client kept them expanded — the workflow
		// graph collapsed on the next reload. Only "done" outranks the owner.
		switch card.Status {
		case "done":
			base = catCompleted
		default:
			owner := workflowOwnerAgentID(card)
			ownerLive := owner != "" && (agentIDs == nil || agentIDs[owner])
			if ownerLive {
				base = catInProgress
			} else if _, ok := card.Data["instance_of"].(string); ok {
				// A template instance (data.instance_of) that no live owner
				// has picked up is planned: workflow_start binds ownerAgentId
				// when the instance actually starts, so an ownerless instance
				// is queued, not to-start.
				base = catPlanned
			} else {
				base = catToStart
			}
		}
	}
	if base == catCompleted {
		if last, ok := parseCardTime(workflowLastActivity(card, all)); ok && now.Sub(last) > archiveAfter {
			return catArchived
		}
	}
	return base
}

// filterWorkflowFoldVisible applies the fold-visibility filter. Hidden maps
// (individually folded, folded category band, or archived in a collapsed
// bucket) are KEPT as lightweight rows — the epic tree renders them as
// collapsed nodes and derives band/bucket headers plus expand affordances
// from them; dropping the rows would make folded workflows unreachable and
// blank topology. Only the hidden maps' scoped task cards (the bulk) are
// dropped from the response. Returns the input slice unchanged when the
// filter is nil.
func filterWorkflowFoldVisible(items []domain.MonoCardListItem, filter *domain.WikiWorkflowFilter, now time.Time) []domain.MonoCardListItem {
	if filter == nil {
		return items
	}
	foldedWorkflows := toSet(filter.FoldedWorkflows)
	foldedCategories := toSet(filter.FoldedCategories)
	expandedBuckets := toSet(filter.ExpandedBuckets)
	// Always build the agent set (empty slice → empty map, non-nil): the
	// frontend runtime always sends a Set — possibly empty
	// (TopologyModeView.currentAgentIds / mono-store load) — so an empty set
	// must take the owner-guarded branch (no live owner → instance_of ?
	// planned : to-start), never the unguarded nil → in-progress branch.
	// A nil here would disagree with an empty client Set on cold start
	// (agent list not loaded yet / project has no agents).
	agentIDs := toSet(filter.AgentIds)

	hiddenTasks := map[string]bool{}
	// Precompute scheduler card IDs for the planned-bucket existence check
	// (mirrors buildPlannedSchedulerGroups: a planned workflow's bucket id is
	// its data.scheduler_card_id, but only when that scheduler card exists).
	schedulerIDs := map[string]bool{}
	for _, c := range items {
		if c.Type == "scheduler" {
			schedulerIDs[c.ID] = true
		}
	}
	out := items[:0:0]
	for _, c := range items {
		if c.Type == "workflow" {
			category := classifyWorkflowCategory(c, agentIDs, items, now)
			hidden := foldedWorkflows[c.ID] || foldedCategories[string(category)]
			if !hidden && category == catArchived {
				if last, ok := parseCardTime(workflowLastActivity(c, items)); ok {
					if bucketID := archivedBucketID(last, now); bucketID != "" && !expandedBuckets[bucketID] {
						hidden = true
					}
				}
			}
			if !hidden && category == catPlanned {
				// Planned workflows with a valid scheduler_card_id form a
				// planned bucket. When the bucket is collapsed, the workflow's
				// tasks are hidden but the map row remains.
				if schedID, _ := c.Data["scheduler_card_id"].(string); schedID != "" && schedulerIDs[schedID] && !expandedBuckets[schedID] {
					hidden = true
				}
			}
			if hidden {
				for _, taskID := range workflowTaskIDs(c, items) {
					hiddenTasks[taskID] = true
				}
			}
		}
		out = append(out, c)
	}
	filtered := make([]domain.MonoCardListItem, 0, len(out))
	for _, c := range out {
		if hiddenTasks[c.ID] {
			continue
		}
		filtered = append(filtered, c)
	}
	return filtered
}

func toSet(items []string) map[string]bool {
	set := make(map[string]bool, len(items))
	for _, s := range items {
		set[s] = true
	}
	return set
}
