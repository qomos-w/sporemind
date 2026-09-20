package project

import (
	"encoding/json"
	"sort"
	"time"
)

// wfDedupState is the per-map notification-dedup state stored inside the map
// card's data block ("wfDedup" key). It replaces the former in-memory maps
// (wfNotifyHist, wfEventNotified, wfWakeAt) so the updater has no persistent
// authoritative in-memory copy: reads go through the card store, writes
// persist via persist.Persist.
type wfDedupState struct {
	// LastMsg is the most recent non-nudge message text dispatched.
	LastMsg string `json:"lastMsg,omitempty"`
	// NudgeCount counts consecutive nudges sent after LastMsg. Reaches
	// maxContinueNotifications the owner is unbound.
	NudgeCount int `json:"nudgeCount,omitempty"`
	// LastRealSendAt is the RFC3339Nano timestamp of the last non-nudge send.
	// Escalation to nudge/unbind is gated on ownerConsumedSince.
	LastRealSendAt string `json:"lastRealSendAt,omitempty"`
	// Held throttles the "holding nudges" warning to one entry per stall.
	Held bool `json:"held,omitempty"`
	// NotifiedKeys holds sorted worker-event keys (actor|task|type) already
	// dispatched and still pending. Entries absent from the current event list
	// are pruned on pruneNotifiedKeys so a re-fired event notifies in full.
	NotifiedKeys []string `json:"notifiedKeys,omitempty"`
	// WakeAt is the RFC3339Nano timestamp of the last wake attempt.
	WakeAt string `json:"wakeAt,omitempty"`
}

const wfDedupDataKey = "wfDedup"

// isEmpty reports whether no dedup state has been recorded.
func (s *wfDedupState) isEmpty() bool {
	return s == nil ||
		(s.LastMsg == "" && s.NudgeCount == 0 && s.LastRealSendAt == "" &&
			!s.Held && len(s.NotifiedKeys) == 0 && s.WakeAt == "")
}

// readWfDedupState extracts the dedup state from a card's parsed Data block.
// A missing or malformed block returns a zero-value state — callers treat
// that as "no prior notifications". setCardDataFieldInRaw stores the value as a
// single-quoted JSON string, so Data[key] is a string that itself is JSON.
func readWfDedupState(card *CardRecord) *wfDedupState {
	st := &wfDedupState{}
	if jsonStr, ok := readWfDedupRaw(card); ok {
		_ = json.Unmarshal([]byte(jsonStr), st)
	}
	return st
}

// saveWfDedupState marshals st into the map card's frontmatter data block and
// saves through the card store (persist.Persist). Returns true on success.
// The empty state is not written: no state means no data line in the card.
// Dirty-checked: when the card already carries the exact same payload the
// write is skipped — the updater runs every 5s, and an unconditional
// temp-file+rename per tick floods the fsnotify watcher (backend re-reads,
// frontend card_changed refetch) with zero-information events.
func (a *Actor) saveWfDedupState(cardID string, st *wfDedupState) {
	if cardID == "" || a.store == nil || st.isEmpty() {
		return
	}
	card, err := a.store.Get(cardID)
	if err != nil || card == nil {
		return
	}
	b, err := json.Marshal(st)
	if err != nil {
		return
	}
	if prev, ok := readWfDedupRaw(card); ok && prev == string(b) {
		return // unchanged since the last write — no disk churn
	}
	card.Raw = setCardDataFieldInRaw(card.Raw, wfDedupDataKey, string(b))
	card.Data = nil // force re-parse on next Get
	_ = a.store.Save(card)
}

// readWfDedupRaw returns the raw JSON string currently stored in the card's
// wfDedup data field, and whether a field is present at all.
func readWfDedupRaw(card *CardRecord) (string, bool) {
	if card == nil || card.Data == nil {
		return "", false
	}
	raw, ok := card.Data[wfDedupDataKey]
	if !ok {
		return "", false
	}
	s, ok := raw.(string)
	return s, ok
}

// clearWfDedupState removes the wfDedup entry from the map card's data block.
func (a *Actor) clearWfDedupState(cardID string) {
	if cardID == "" || a.store == nil {
		return
	}
	card, err := a.store.Get(cardID)
	if err != nil || card == nil {
		return
	}
	newRaw := removeCardDataFieldInRaw(card.Raw, wfDedupDataKey)
	if newRaw == card.Raw {
		return // no entry present
	}
	card.Raw = newRaw
	card.Data = nil
	_ = a.store.Save(card)
}

// parseWfTime parses an optional RFC3339Nano timestamp; zero on empty/bad.
func parseWfTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// pruneNotifiedKeys drops keys no longer present in the current event list and
// reports whether any current key has not been notified yet.
func pruneNotifiedKeys(st *wfDedupState, events []wfWorkerEvent) bool {
	current := make(map[string]bool, len(events))
	for _, e := range events {
		current[wfEventKey(e)] = true
	}
	notified := make(map[string]bool, len(st.NotifiedKeys))
	for _, k := range st.NotifiedKeys {
		if current[k] {
			notified[k] = true
		}
	}
	fresh := false
	for k := range current {
		if !notified[k] {
			fresh = true
			break
		}
	}
	keys := make([]string, 0, len(notified))
	for k := range notified {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	st.NotifiedKeys = keys
	return fresh
}

// commitNotifiedKeys marks all current events as notified-pending.
func commitNotifiedKeys(st *wfDedupState, events []wfWorkerEvent) {
	set := make(map[string]bool, len(st.NotifiedKeys)+len(events))
	for _, k := range st.NotifiedKeys {
		set[k] = true
	}
	for _, e := range events {
		set[wfEventKey(e)] = true
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	st.NotifiedKeys = keys
}

// decideNotifyFromCard applies the pure decideNotify against a dedup state
// reconstructed history. The card state compresses the full message ring to
// (lastRealMsg, trailingNudgeCount), which decidesNotify can evaluate exactly:
// step 1 counts trailing nudges, step 2 compares the last entry, step 3 needs
// the same message within the window followed only by nudges — with a single
// last-message slot this only fires when the current message equals it, which
// step 2 already handles.
func decideNotifyFromCard(message string, st *wfDedupState, maxContinue int, nudgeHint string) (wfNotifyAction, string) {
	if st.NudgeCount >= maxContinue {
		return wfNotifyUnbind, ""
	}
	if message == st.LastMsg {
		return wfNotifyContinue, makeNudge(nudgeHint)
	}
	return wfNotifySend, message
}

// markSendApplied records a successful non-nudge dispatch.
func markSendApplied(st *wfDedupState, message string, at time.Time) {
	st.LastMsg = message
	st.NudgeCount = 0
	st.LastRealSendAt = at.UTC().Format(time.RFC3339Nano)
}