package project

import (
	"strings"
)

// CardRepairer normalizes a decoded card before validation. Repairers are
// intentionally conservative: they only replace well-known misspellings and
// synonyms with canonical values, never invent data.
type CardRepairer interface {
	Repair(card *CardRecord) *CardRecord
}

// cardTypeRepairers maps canonical types to their repairers. Unknown or
// unregistered types are left untouched.
// repairCardRecord returns a shallow copy of card with common mistakes fixed.
// It is called by validateCardStructured before type validators run so that
// validators see canonical values.
func repairCardRecord(card *CardRecord) *CardRecord {
	if card == nil {
		return nil
	}
	repaired := *card
	cardType := normalizeCardType(repaired.Type)
	if r, ok := cardTypeRepairers[cardType]; ok {
		r.Repair(&repaired)
	}
	return &repaired
}

// --- type-specific repairers -------------------------------------------------

type taskRepairer struct{}

func (taskRepairer) Repair(card *CardRecord) *CardRecord {
	if card == nil {
		return nil
	}
	card.Status = normalizeTaskStatus(card.Status)
	card.Priority = repairTaskPriority(card.Priority)
	return card
}

// repairTaskStatus maps common synonyms and misspellings to the canonical
// status set: backlog, todo, doing, done, blocked, cancelled.
//
// Deprecated: use normalizeTaskStatus directly. This wrapper is kept for
// callers in the repair path until they can be updated.
func repairTaskStatus(status string) string {
	return normalizeTaskStatus(status)
}

// repairTaskPriority maps common synonyms to the canonical priority set:
// low, medium, high, urgent.
func repairTaskPriority(priority string) string {
	switch strings.ToLower(strings.TrimSpace(priority)) {
	case "mid", "normal", "moderate":
		return "medium"
	case "hi":
		return "high"
	case "critical", "crit", "asap", "blocking", "blocker":
		return "urgent"
	}
	return priority
}

// normalizeCardStatusInRaw rewrites the top-level status frontmatter line to
// its canonical value. It is a conservative, in-place transformation: only the
// status value changes, all other formatting is preserved.
func normalizeCardStatusInRaw(raw string) string {
	if !strings.HasPrefix(raw, "---") {
		return raw
	}
	end := strings.Index(raw[3:], "---")
	if end < 0 {
		return raw
	}
	front := raw[3 : 3+end]
	lines := strings.Split(front, "\n")
	changed := false
	for i, line := range lines {
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			continue
		}
		trimmed := strings.TrimSpace(line)
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok || strings.TrimSpace(key) != "status" {
			continue
		}
		value = strings.TrimSpace(value)
		canonical := normalizeTaskStatus(value)
		if canonical != value {
			lines[i] = "status: " + canonical
			changed = true
		}
	}
	if !changed {
		return raw
	}
	return raw[:3] + strings.Join(lines, "\n") + raw[3+end:]
}
