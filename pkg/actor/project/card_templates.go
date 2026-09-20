package project

import (
	"strings"
	"time"
)

// projectInfoCardID is a well-known regular card ID. project_info is an
// ordinary wiki card — not builtin — fully editable and deletable, seeded from
// its template so a project has it out of the box.
const projectInfoCardID = "project_info"

// maxCardIDLen bounds the card id to a safe size for filesystem filenames.
// Most filesystems allow 255-byte filenames; the ".md" suffix and encoding
// expansion for special characters need headroom.
const maxCardIDLen = 200

// projectCardTemplate is the seed definition for a well-known regular card.
type projectCardTemplate struct {
	ID   string
	Body string
}

func templateFor(id string) (projectCardTemplate, bool) {
	for _, t := range projectCardTemplates {
		if t.ID == id {
			return t, true
		}
	}
	return projectCardTemplate{}, false
}

// formatWikiCard renders a regular wiki card's frontmatter + body.
func formatWikiCard(id, body, ts string) string {
	return "---\n" +
		"id: " + id + "\n" +
		"type: wiki\n" +
		"tags: []\n" +
		"created: \"" + ts + "\"\n" +
		"modified: \"" + ts + "\"\n" +
		"---\n\n" + body
}

func (t projectCardTemplate) toCardRecord(ts string) *CardRecord {
	body := t.Body
	return &CardRecord{
		Title:    t.ID,
		Type:     "wiki",
		Tags:     []string{},
		List:     []string{},
		Body:     body,
		Raw:      formatWikiCard(t.ID, body, ts),
		Created:  ts,
		Modified: ts,
	}
}

// ensureProjectInfoCard creates the project_info card from its template when
// missing. Called at project init so the card exists out of the box.
func (a *Actor) ensureProjectInfoCard() {
	if _, err := a.store.Get(projectInfoCardID); err == nil {
		return
	}
	if t, ok := templateFor(projectInfoCardID); ok {
		_ = a.store.Save(t.toCardRecord(time.Now().UTC().Format(time.RFC3339)))
	}
}

// migrateProjectInfoCards preserves user-authored content from the legacy
// __builtin_* project-info cards by copying it into regular cards. It must run
// before obsoleteBuiltinIDs cleanup deletes the old files. Legacy summary /
// constraints content lands in ordinary editable cards. It never clobbers an
// already-existing regular card and skips empty bodies.
func (a *Actor) migrateProjectInfoCards() {
	for oldID, newID := range legacyProjectInfoTargets {
		old, err := a.store.Get(oldID)
		if err != nil {
			continue
		}
		if _, err := a.store.Get(newID); err == nil {
			continue
		}
		body := strings.TrimSpace(old.Body)
		if body == "" {
			continue
		}
		ts := old.Modified
		if ts == "" {
			ts = old.Created
		}
		if ts == "" {
			ts = "1970-01-01T00:00:00Z"
		}
		card := &CardRecord{
			Title:    newID,
			Type:     "wiki",
			Tags:     []string{},
			List:     []string{},
			Body:     body,
			Raw:      formatWikiCard(newID, body, ts),
			Created:  ts,
			Modified: ts,
		}
		_ = a.store.Save(card)
	}
}
