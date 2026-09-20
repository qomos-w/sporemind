package project

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
)

const externalSkillCardPrefix = "ext-skill:"

// externalSkillSource maps a project-relative directory to its source label.
type externalSkillSource struct {
	Dir    string // project-relative directory, e.g. ".claude/skills"
	Source string // source label for card ID and metadata, e.g. "claude"
}

// externalSkillSources is the authoritative list of external skill directories
// scanned on demand. Adding a new source here is sufficient to surface its
// skills as read-only ext-skill: cards.
// ExternalSkillCardProvider surfaces skills imported from external agent
// ecosystems (Claude, Codex, OpenCode, Pi) as read-only external cards.
// Skills are discovered by scanning project-relative skill directories on
// demand (no caching, no persistence). Cards are never persisted and
// Save/Delete are always rejected.
type ExternalSkillCardProvider struct {
	projectRoot string
}

func newExternalSkillCardProvider(projectRoot string) *ExternalSkillCardProvider {
	return &ExternalSkillCardProvider{projectRoot: projectRoot}
}

func (p *ExternalSkillCardProvider) Prefix() string { return externalSkillCardPrefix }

// externalSkillCardID builds a stable, deterministic card ID:
// ext-skill:<source>:<sanitized-name>.
func externalSkillCardID(source, name string) string {
	return externalSkillCardPrefix + source + ":" + sanitizePathSegment(name)
}

// parseExternalSkillID extracts the source and name from an ext-skill: ID.
// Returns ok=false if the ID is malformed.
func parseExternalSkillID(id string) (source, name string, ok bool) {
	if !strings.HasPrefix(id, externalSkillCardPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(id, externalSkillCardPrefix)
	if rest == "" {
		return "", "", false
	}
	idx := strings.Index(rest, ":")
	if idx <= 0 {
		return "", "", false
	}
	source = rest[:idx]
	name = rest[idx+1:]
	if source == "" || name == "" {
		return "", "", false
	}
	return source, name, true
}

// skillNameFromFile returns the skill name for a .md file: the frontmatter
// "name" field if present, otherwise the filename stem (relative to srcDir).
func skillNameFromFile(path, srcDir string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if name := parseFrontmatterString(string(data), "name"); name != "" {
		return name
	}
	rel, err := filepath.Rel(srcDir, path)
	if err != nil {
		rel = filepath.Base(path)
	}
	return strings.TrimSuffix(rel, filepath.Ext(rel))
}

// externalSkillListItem builds a read-only MonoCardListItem for an external
// skill file. stamp is the skill file's mtime (empty when unavailable); it is
// used for both Created and Modified so recency ordering reflects the file,
// not the moment of the scan.
func externalSkillListItem(filePath, source, name, stamp string) domain.MonoCardListItem {
	id := externalSkillCardID(source, name)
	return domain.MonoCardListItem{
		ID:         id,
		Type:       "skill",
		Source:     source,
		Storage:    "external",
		Visibility: "component",
		Tags:       []string{"skill", "component", "external"},
		List:       []string{},
		Created:    stamp,
		Modified:   stamp,
		Protected:  true,
		Editable:   false,
		Deletable:  false,
		Data: map[string]any{
			"componentKind": "skill",
			"source":        source,
			"storage":       "external",
			"visibility":    "component",
			"protected":     true,
			"editable":      false,
			"deletable":     false,
			"filePath":      filePath,
			"name":          name,
		},
	}
}

// walkExternalSkillDir scans a single source directory recursively and appends
// items for each .md file found.
func (p *ExternalSkillCardProvider) walkExternalSkillDir(src externalSkillSource, items *[]domain.MonoCardListItem) {
	srcDir := filepath.Join(p.projectRoot, filepath.FromSlash(src.Dir))
	_ = filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable directories
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		name := skillNameFromFile(path, srcDir)
		if name == "" {
			return nil
		}
		stamp := ""
		if info, infoErr := d.Info(); infoErr == nil {
			stamp = info.ModTime().UTC().Format(time.RFC3339)
		}
		*items = append(*items, externalSkillListItem(path, src.Source, name, stamp))
		return nil
	})
}

func (p *ExternalSkillCardProvider) List(_ actor.PureContext) ([]domain.MonoCardListItem, error) {
	items := make([]domain.MonoCardListItem, 0)
	for _, src := range externalSkillSources {
		p.walkExternalSkillDir(src, &items)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].ID < items[j].ID
	})
	return items, nil
}

func (p *ExternalSkillCardProvider) Get(_ actor.PureContext, id string) (string, error) {
	if _, _, ok := parseExternalSkillID(id); !ok {
		return "", fmt.Errorf("external skill card: invalid id %q", id)
	}
	// Re-scan to find the matching file by card ID.
	for _, src := range externalSkillSources {
		srcDir := filepath.Join(p.projectRoot, filepath.FromSlash(src.Dir))
		var found string
		_ = filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
				return nil
			}
			name := skillNameFromFile(path, srcDir)
			if name != "" && externalSkillCardID(src.Source, name) == id {
				found = path
			}
			return nil
		})
		if found != "" {
			data, err := os.ReadFile(found)
			if err != nil {
				return "", fmt.Errorf("external skill card: read %s: %w", found, err)
			}
			return string(data), nil
		}
	}
	return "", fmt.Errorf("external skill card: %q not found", id)
}

// Save is always rejected: external skill cards are read-only.
func (p *ExternalSkillCardProvider) Save(_ actor.PureContext, _ string, _ string) error {
	return fmt.Errorf("external skill cards are read-only")
}

// Delete is always rejected: external skill cards are read-only.
func (p *ExternalSkillCardProvider) Delete(_ actor.PureContext, _ string) error {
	return fmt.Errorf("external skill cards are read-only")
}
