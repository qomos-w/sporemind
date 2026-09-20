package project

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// reviewChangesetCardData is the summary projected into the task card's
// CardRecord.Data["reviewChangeset"]. It contains only file-level metadata
// (paths, statuses, markers, diff line counts) — never full diffs or
// untracked file content. The card data is the sole authoritative copy;
// handlers read it back on every request.
type reviewChangesetCardData struct {
	Status   string `json:"status"`
	ErrorMsg string `json:"errorMsg,omitempty"`
	FrozenAt string `json:"frozenAt"`
	Baseline string `json:"baseline"`
	Head     string `json:"head"`
	Branch   string `json:"branch,omitempty"`
	IsDirty  bool   `json:"isDirty,omitempty"`
	AgentActorID string `json:"agentActorId,omitempty"`
	Stats    struct {
		TrackedChanged int32 `json:"trackedChanged"`
		Untracked      int32 `json:"untracked"`
		AddedLines     int32 `json:"addedLines"`
		DeletedLines   int32 `json:"deletedLines"`
		TotalDiffLines int32 `json:"totalDiffLines"`
	} `json:"stats"`
	Files          []reviewChangesetCardFile          `json:"files"`
	Commits        []gen.ProjectReviewCommitInfo      `json:"commits,omitempty"`
	UntrackedFiles []gen.ProjectReviewUntrackedFile  `json:"untrackedFiles,omitempty"`
	TestResult     gen.ProjectReviewTestResult        `json:"testResult,omitempty"`
	Generation     int32                              `json:"gen,omitempty"`
}

type reviewChangesetCardFile struct {
	Path        string `json:"path"`
	Status      string `json:"status"`
	OldPath     string `json:"oldPath"`
	IsBinary    bool   `json:"binary"`
	IsGenerated bool   `json:"generated"`
	DiffLines   int32  `json:"diffLines"`
}

// writeReviewChangesetCardData writes the review changeset summary into the
// task card's CardRecord.Data via the data: frontmatter block. The data is
// stored as a single-line JSON string under the "reviewChangeset" key inside
// the data: block, which decodeCard → parseDataBlock will parse back into
// Data["reviewChangeset"] on read.
//
// This function is called from the project actor owner loop (via the finalize
// callable) — never from the async goroutine.
func (a *Actor) writeReviewChangesetCardData(cardID string, data reviewChangesetCardData) {
	if cardID == "" || a.store == nil {
		return
	}
	card, err := a.store.Get(cardID)
	if err != nil || card == nil {
		return
	}
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return
	}
	card.Raw = setCardDataFieldInRaw(card.Raw, "reviewChangeset", string(jsonBytes))
	card.Data = nil // force re-parse on next Get
	_ = a.store.Save(card)
}

// clearReviewChangesetCardData removes the reviewChangeset entry from the
// task card's data: frontmatter block. Called on clear/teardown.
func (a *Actor) clearReviewChangesetCardData(cardID string) {
	if cardID == "" || a.store == nil {
		return
	}
	card, err := a.store.Get(cardID)
	if err != nil || card == nil {
		return
	}
	newRaw := removeCardDataFieldInRaw(card.Raw, "reviewChangeset")
	if newRaw == card.Raw {
		return // no change
	}
	card.Raw = newRaw
	card.Data = nil
	_ = a.store.Save(card)
}

// readReviewChangesetCardData reads the reviewChangeset entry back from a task
// card's Data block. Returns ok=false when absent or malformed.
func readReviewChangesetCardData(card *CardRecord) (reviewChangesetCardData, bool) {
	var d reviewChangesetCardData
	if card == nil || card.Data == nil {
		return d, false
	}
	raw, ok := card.Data["reviewChangeset"]
	if !ok {
		return d, false
	}
	jsonStr, ok := raw.(string)
	if !ok {
		return d, false
	}
	if err := json.Unmarshal([]byte(jsonStr), &d); err != nil {
		return d, false
	}
	return d, true
}

// readReviewChangesetByTask reads the changeset card data for a task card ID.
func (a *Actor) readReviewChangesetByTask(taskCardID string) (reviewChangesetCardData, bool) {
	if taskCardID == "" || a.store == nil {
		return reviewChangesetCardData{}, false
	}
	card, err := a.store.Get(taskCardID)
	if err != nil || card == nil {
		return reviewChangesetCardData{}, false
	}
	return readReviewChangesetCardData(card)
}

// summaryToCardData projects a full summary response into card data for
// persistence. Called by the finalize path.
func summaryToCardData(s *gen.ProjectReviewChangesetSummaryResp, generation int32) reviewChangesetCardData {
	d := reviewChangesetCardData{
		Status:        s.Status,
		ErrorMsg:      s.ErrorMsg,
		FrozenAt:      s.GeneratedAt,
		Baseline:      s.Baseline,
		Head:          s.Head,
		Branch:        s.Branch,
		IsDirty:       s.IsDirty,
		AgentActorID:  s.AgentActorID,
		Commits:       s.Commits,
		TestResult:    s.TestResult,
		Generation:    generation,
	}
	d.Stats.TrackedChanged = s.Stats.TrackedChanged
	d.Stats.Untracked = s.Stats.Untracked
	d.Stats.AddedLines = s.Stats.AddedLines
	d.Stats.DeletedLines = s.Stats.DeletedLines
	d.Stats.TotalDiffLines = s.Stats.TotalDiffLines
	for _, f := range s.Files {
		d.Files = append(d.Files, reviewChangesetCardFile{
			Path:        f.Path,
			Status:      f.Status,
			OldPath:     f.OldPath,
			IsBinary:    f.IsBinary,
			IsGenerated: f.IsGenerated,
			DiffLines:   f.DiffLines,
		})
	}
	d.UntrackedFiles = s.UntrackedFiles
	return d
}

// cardDataToSummaryResp reconstructs a summary response from card data.
func cardDataToSummaryResp(d reviewChangesetCardData, agentActorID string) gen.ProjectReviewChangesetSummaryResp {
	files := make([]gen.ProjectReviewFileEntry, 0, len(d.Files))
	for _, f := range d.Files {
		files = append(files, gen.ProjectReviewFileEntry{
			Path:        f.Path,
			Status:      f.Status,
			OldPath:     f.OldPath,
			IsBinary:    f.IsBinary,
			IsGenerated: f.IsGenerated,
			DiffLines:   f.DiffLines,
		})
	}
	untracked := d.UntrackedFiles
	if untracked == nil {
		untracked = []gen.ProjectReviewUntrackedFile{}
	}
	commits := d.Commits
	if commits == nil {
		commits = []gen.ProjectReviewCommitInfo{}
	}
	return gen.ProjectReviewChangesetSummaryResp{
		AgentActorID:   agentActorID,
		GeneratedAt:    d.FrozenAt,
		Status:         d.Status,
		ErrorMsg:       d.ErrorMsg,
		Baseline:       d.Baseline,
		Head:           d.Head,
		Branch:         d.Branch,
		IsDirty:        d.IsDirty,
		Commits:        commits,
		Files:          files,
		UntrackedFiles: untracked,
		TestResult:     d.TestResult,
		Stats: gen.ProjectReviewChangesetStats{
			TrackedChanged: d.Stats.TrackedChanged,
			Untracked:      d.Stats.Untracked,
			AddedLines:     d.Stats.AddedLines,
			DeletedLines:   d.Stats.DeletedLines,
			TotalDiffLines: d.Stats.TotalDiffLines,
		},
	}
}

// joinFrontLines reassembles frontmatter lines, guaranteeing a trailing
// newline so the closing --- delimiter always starts its own line. Without
// the guard, inserting or replacing the last data field glues the closer onto
// the value ("key: value---"), which the strict frontend parser rejects.
func joinFrontLines(lines []string) string {
	joined := strings.Join(lines, "\n")
	if !strings.HasSuffix(joined, "\n") {
		joined += "\n"
	}
	return joined
}

// setCardDataFieldInRaw inserts or replaces a key inside the data: frontmatter
// block. The value is stored as a single-quoted JSON string. If no data: block
// exists, one is created.
func setCardDataFieldInRaw(raw, key, jsonValue string) string {
	quotedValue := "'" + jsonValue + "'"
	dataLine := "  " + key + ": " + quotedValue

	// No frontmatter at all — create minimal block.
	if !strings.HasPrefix(raw, "---") {
		return "---\ndata:\n" + dataLine + "\n---\n\n" + raw
	}

	end := strings.Index(raw[3:], "---")
	if end < 0 {
		return "---\ndata:\n" + dataLine + "\n---\n\n" + raw
	}

	front := raw[3 : 3+end]
	rest := raw[3+end:]
	lines := strings.Split(front, "\n")

	// Find data: block.
	dataIdx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "data:" {
			dataIdx = i
			break
		}
	}

	if dataIdx < 0 {
		// No data: block — append one.
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
		lines = append(lines, "data:")
		lines = append(lines, dataLine)
		return "---" + joinFrontLines(lines) + rest
	}

	// Find existing key within data: block.
	keyPrefix := "  " + key + ":"
	for i := dataIdx + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		// Non-indented line → end of data: block.
		if lines[i] != "" && lines[i][0] != ' ' && lines[i][0] != '\t' {
			break
		}
		if strings.HasPrefix(lines[i], keyPrefix) {
			lines[i] = dataLine
			return "---" + joinFrontLines(lines) + rest
		}
	}

	// Key not found — insert after data: line.
	insertAt := dataIdx + 1
	for insertAt < len(lines) {
		trimmed := strings.TrimSpace(lines[insertAt])
		if trimmed == "" {
			insertAt++
			continue
		}
		if lines[insertAt] != "" && lines[insertAt][0] != ' ' && lines[insertAt][0] != '\t' {
			break
		}
		insertAt++
	}
	newLines := make([]string, 0, len(lines)+1)
	newLines = append(newLines, lines[:insertAt]...)
	newLines = append(newLines, dataLine)
	newLines = append(newLines, lines[insertAt:]...)
	return "---" + joinFrontLines(newLines) + rest
}

// setCardDataStringInRaw inserts or replaces a plain string scalar inside the
// data: frontmatter block, written as a double-quoted YAML value so it
// round-trips through parseDataBlock → unquote into CardRecord.Data. This is
// the encoding used for executor/reviewer/workflow_template scheduler fields.
// If no data: block exists, one is created.
func setCardDataStringInRaw(raw, key, value string) string {
	quoted := quoteValue(value)
	dataLine := "  " + key + ": " + quoted

	// No frontmatter at all — create minimal block.
	if !strings.HasPrefix(raw, "---") {
		return "---\ndata:\n" + dataLine + "\n---\n\n" + raw
	}

	end := strings.Index(raw[3:], "---")
	if end < 0 {
		return "---\ndata:\n" + dataLine + "\n---\n\n" + raw
	}

	front := raw[3 : 3+end]
	rest := raw[3+end:]
	lines := strings.Split(front, "\n")

	// Find data: block.
	dataIdx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "data:" {
			dataIdx = i
			break
		}
	}

	if dataIdx < 0 {
		// No data: block — append one.
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
		lines = append(lines, "data:")
		lines = append(lines, dataLine)
		return "---" + joinFrontLines(lines) + rest
	}

	// Find existing key within data: block.
	keyPrefix := "  " + key + ":"
	for i := dataIdx + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		// Non-indented line → end of data: block.
		if lines[i] != "" && lines[i][0] != ' ' && lines[i][0] != '\t' {
			break
		}
		if strings.HasPrefix(lines[i], keyPrefix) {
			lines[i] = dataLine
			return "---" + joinFrontLines(lines) + rest
		}
	}

	// Key not found — insert after data: line.
	insertAt := dataIdx + 1
	for insertAt < len(lines) {
		trimmed := strings.TrimSpace(lines[insertAt])
		if trimmed == "" {
			insertAt++
			continue
		}
		if lines[insertAt] != "" && lines[insertAt][0] != ' ' && lines[insertAt][0] != '\t' {
			break
		}
		insertAt++
	}
	newLines := make([]string, 0, len(lines)+1)
	newLines = append(newLines, lines[:insertAt]...)
	newLines = append(newLines, dataLine)
	newLines = append(newLines, lines[insertAt:]...)
	return "---" + joinFrontLines(newLines) + rest
}

// setCardDataIntInRaw inserts or replaces an integer scalar inside the data:
// frontmatter block, written as an unquoted YAML value so parseDataBlock yields
// a numeric type readable by cardDataInt. If no data: block exists, one is
// created.
func setCardDataIntInRaw(raw, key string, value int) string {
	dataLine := "  " + key + ": " + strconv.Itoa(value)

	// No frontmatter at all — create minimal block.
	if !strings.HasPrefix(raw, "---") {
		return "---\ndata:\n" + dataLine + "\n---\n\n" + raw
	}

	end := strings.Index(raw[3:], "---")
	if end < 0 {
		return "---\ndata:\n" + dataLine + "\n---\n\n" + raw
	}

	front := raw[3 : 3+end]
	rest := raw[3+end:]
	lines := strings.Split(front, "\n")

	// Find data: block.
	dataIdx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "data:" {
			dataIdx = i
			break
		}
	}

	if dataIdx < 0 {
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
		lines = append(lines, "data:")
		lines = append(lines, dataLine)
		return "---" + joinFrontLines(lines) + rest
	}

	keyPrefix := "  " + key + ":"
	for i := dataIdx + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		if lines[i] != "" && lines[i][0] != ' ' && lines[i][0] != '\t' {
			break
		}
		if strings.HasPrefix(lines[i], keyPrefix) {
			lines[i] = dataLine
			return "---" + joinFrontLines(lines) + rest
		}
	}

	insertAt := dataIdx + 1
	for insertAt < len(lines) {
		trimmed := strings.TrimSpace(lines[insertAt])
		if trimmed == "" {
			insertAt++
			continue
		}
		if lines[insertAt] != "" && lines[insertAt][0] != ' ' && lines[insertAt][0] != '\t' {
			break
		}
		insertAt++
	}
	newLines := make([]string, 0, len(lines)+1)
	newLines = append(newLines, lines[:insertAt]...)
	newLines = append(newLines, dataLine)
	newLines = append(newLines, lines[insertAt:]...)
	return "---" + joinFrontLines(newLines) + rest
}

// removeCardDataFieldInRaw removes a key from the data: frontmatter block.
// Returns the raw unchanged if the key doesn't exist.
func removeCardDataFieldInRaw(raw, key string) string {
	if !strings.HasPrefix(raw, "---") {
		return raw
	}
	end := strings.Index(raw[3:], "---")
	if end < 0 {
		return raw
	}

	front := raw[3 : 3+end]
	rest := raw[3+end:]
	lines := strings.Split(front, "\n")

	keyPrefix := "  " + key + ":"
	found := false
	for i, line := range lines {
		if strings.HasPrefix(line, keyPrefix) {
			lines = append(lines[:i], lines[i+1:]...)
			found = true
			break
		}
	}
	if !found {
		return raw
	}
	return "---" + joinFrontLines(lines) + rest
}

// preparingCardData creates a minimal preparing-state card data entry.
func preparingCardData(generatedAt string, generation int32, agentActorID string) reviewChangesetCardData {
	return reviewChangesetCardData{
		Status:        "preparing",
		FrozenAt:      generatedAt,
		Generation:    generation,
		AgentActorID:  agentActorID,
	}
}

// failedCardData creates a failed-state card data entry.
func failedCardData(generatedAt, errMsg string) reviewChangesetCardData {
	return reviewChangesetCardData{
		Status:   "failed",
		ErrorMsg: errMsg,
		FrozenAt: generatedAt,
	}
}

// cardDataToJSON marshals card data for frontmatter storage.
func cardDataToJSON(d reviewChangesetCardData) string {
	b, err := json.Marshal(d)
	if err != nil {
		return fmt.Sprintf(`{"status":"failed","frozenAt":"%s"}`, time.Now().UTC().Format(time.RFC3339))
	}
	return string(b)
}

// ── Persistent review binding (agent → task card + generation counter) ──

// reviewBindingRecord is persisted via persist.Persist under
// "review-binding-<agentActorID>". It carries the task card the agent's
// changeset is projected onto and the freeze generation counter used for
// stale-finalize rejection. The card data (on the task card) is the
// authoritative snapshot; this record is the durable pointer/counter.
type reviewBindingRecord struct {
	TaskCardID string `json:"taskCardId"`
	Gen        int32  `json:"gen"`
}

func reviewBindingKey(agentActorID string) string {
	return "review-binding-" + agentActorID
}

// loadReviewBinding returns the persisted binding record, or the zero record
// when none exists (first freeze, or persistence unavailable).
func (a *Actor) loadReviewBinding(agentActorID string) reviewBindingRecord {
	var rec reviewBindingRecord
	if a.persistStore == nil {
		return rec
	}
	_ = persist.LoadOrZero(a.persistStore, reviewBindingKey(agentActorID), &rec)
	return rec
}

// saveReviewBinding persists the binding record via persist.Persist.
func (a *Actor) saveReviewBinding(agentActorID string, rec reviewBindingRecord) {
	if a.persistStore == nil {
		return
	}
	_ = a.persistStore.Save(reviewBindingKey(agentActorID), rec)
}

// deleteReviewBinding removes the persisted binding record.
func (a *Actor) deleteReviewBinding(agentActorID string) {
	if a.persistStore == nil {
		return
	}
	_ = a.persistStore.Delete(reviewBindingKey(agentActorID))
}
