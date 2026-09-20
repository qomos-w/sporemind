package project

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/actor/pluginhost"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
)

const pluginExternalPrefix = "plugin:"

// ExternalCardProvider generates cards that live outside .sporecode/wiki.
// External cards are merged into project.wiki.listCards and support get/save/delete
// through the same callables as persisted cards.
type ExternalCardProvider interface {
	Prefix() string
	List(ctx actor.PureContext) ([]domain.MonoCardListItem, error)
	Get(ctx actor.PureContext, id string) (string, error)
	Save(ctx actor.PureContext, id string, raw string) error
	Delete(ctx actor.PureContext, id string) error
}

// PluginExternalCardProvider surfaces installed and dev plugins as external cards.
// Each plugin maps to a card with ID "plugin:<name>". The backing markdown file is
// "<pluginDir>/plugin-<sanitized-name>.md".
//
// Dev-app plugins are discovered from workspace mounts (AppKind == "dev-app")
// rather than by scanning the fixed <dataDir>/data/dev-app directory.
type PluginExternalCardProvider struct{}

func newPluginExternalCardProvider() *PluginExternalCardProvider {
	return &PluginExternalCardProvider{}
}

func (p *PluginExternalCardProvider) Prefix() string {
	return pluginExternalPrefix
}

// sanitizePathSegment makes a string safe for use as both a directory name and a
// filename base, replacing filesystem-unsafe and whitespace characters with '-'.
func sanitizePathSegment(name string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9._~\-]+`)
	return re.ReplaceAllString(name, "-")
}

func pluginCardID(name string) string {
	return pluginExternalPrefix + sanitizePathSegment(name)
}

func pluginArchiveFile(dir string, name string) string {
	return filepath.Join(dir, "plugin-"+sanitizePathSegment(name)+".md")
}

func pluginNameFromID(id string) (string, bool) {
	if !strings.HasPrefix(id, pluginExternalPrefix) {
		return "", false
	}
	name := strings.TrimPrefix(id, pluginExternalPrefix)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\\`) {
		return "", false
	}
	if sanitizePathSegment(name) != name {
		return "", false
	}
	return name, true
}

func (p *PluginExternalCardProvider) List(ctx actor.PureContext) ([]domain.MonoCardListItem, error) {
	items := make(map[string]domain.MonoCardListItem)
	now := time.Now().Format(time.RFC3339)

	// Installed plugins from pluginhost.
	for _, d := range p.listInstalledPlugins(ctx) {
		name := sanitizePathSegment(d.ID)
		id := pluginCardID(name)
		created := now
		if !d.LoadedAt.IsZero() {
			created = d.LoadedAt.Local().Format(time.RFC3339)
		}
		items[id] = domain.MonoCardListItem{
			ID: id, Type: "capability", Source: "pluginhost", Storage: "external", Visibility: "component",
			Tags: []string{"plugin", "component", "capability", "installed"}, List: []string{}, Created: created, Modified: now,
			Protected: true, Editable: false, Deletable: false, Raw: "",
			Data: map[string]any{"componentKind": "capability", "source": "pluginhost", "storage": "external", "visibility": "component", "protected": true, "pluginId": d.ID},
		}
	}

	// Dev apps from the workspace's mounted dev-app projects.
	for _, m := range p.devAppMounts(ctx) {
		name := sanitizePathSegment(m.Name)
		if name == "" || strings.HasPrefix(name, ".") {
			continue
		}
		id := pluginCardID(name)
		if existing, ok := items[id]; ok {
			existing.Tags = appendUniqueTag(existing.Tags, "dev")
			items[id] = existing
			continue
		}
		items[id] = domain.MonoCardListItem{
			ID: id, Type: "capability", Source: "pluginhost", Storage: "external", Visibility: "component",
			Tags: []string{"plugin", "component", "capability", "dev"}, List: []string{}, Created: now, Modified: now,
			Protected: true, Editable: false, Deletable: false, Raw: "",
			Data: map[string]any{"componentKind": "capability", "source": "pluginhost", "storage": "external", "visibility": "component", "protected": true, "pluginId": name},
		}
	}

	out := make([]domain.MonoCardListItem, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (p *PluginExternalCardProvider) Get(ctx actor.PureContext, id string) (string, error) {
	name, ok := pluginNameFromID(id)
	if !ok {
		return "", fmt.Errorf("plugin external card: invalid id %s", id)
	}
	// Try dev directory first, then installed directory.
	if dir := p.findDevDir(ctx, name); dir != "" {
		archive := pluginArchiveFile(dir, name)
		data, err := os.ReadFile(archive)
		if err == nil {
			return string(data), nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("plugin external card: read archive %s: %w", archive, err)
		}
	}
	installedDir := filepath.Join(config.DataDir(), "data", "plugins", name)
	archive := pluginArchiveFile(installedDir, name)
	data, err := os.ReadFile(archive)
	if err == nil {
		return string(data), nil
	}
	if !os.IsNotExist(err) {
		return "", fmt.Errorf("plugin external card: read archive %s: %w", archive, err)
	}
	if !p.hasPlugin(ctx, name) {
		return "", fmt.Errorf("plugin external card: plugin not found")
	}

	return p.renderDefault(ctx, name)
}

func (p *PluginExternalCardProvider) Save(ctx actor.PureContext, id string, raw string) error {
	name, ok := pluginNameFromID(id)
	if !ok {
		return fmt.Errorf("plugin external card: invalid id %s", id)
	}
	if !p.hasPlugin(ctx, name) {
		return fmt.Errorf("plugin external card: plugin not found")
	}

	// Prefer dev directory if it exists; otherwise create installed directory.
	dir := p.findDevDir(ctx, name)
	if dir == "" {
		dir = filepath.Join(config.DataDir(), "data", "plugins", name)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("plugin external card: create dir %s: %w", dir, err)
	}
	archive := pluginArchiveFile(dir, name)
	if err := os.WriteFile(archive, []byte(raw), 0o644); err != nil {
		return fmt.Errorf("plugin external card: write archive %s: %w", archive, err)
	}
	return nil
}

func (p *PluginExternalCardProvider) Delete(ctx actor.PureContext, id string) error {
	name, ok := pluginNameFromID(id)
	if !ok {
		return fmt.Errorf("plugin external card: invalid id %s", id)
	}

	for _, dir := range []string{p.findDevDir(ctx, name), filepath.Join(config.DataDir(), "data", "plugins", name)} {
		if dir == "" {
			continue
		}
		archive := pluginArchiveFile(dir, name)
		if err := os.Remove(archive); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("plugin external card: delete archive %s: %w", archive, err)
		}
	}
	return nil
}

// listInstalledPlugins queries pluginhost for installed plugin descriptors.
func (p *PluginExternalCardProvider) listInstalledPlugins(ctx actor.PureContext) []pluginhost.PluginDescriptor {
	ref, ok := ctx.LookupService("pluginhost")
	if !ok || ref == nil {
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	call := ref.Invoke(callCtx, "pluginhost.list_plugins", nil)
	if call == nil {
		return nil
	}
	defer call.Close()
	v, _ := call.Final(callCtx)
	if v == nil {
		return nil
	}
	var descs []pluginhost.PluginDescriptor
	switch d := v.(type) {
	case []pluginhost.PluginDescriptor:
		descs = d
	default:
		body, _ := json.Marshal(v)
		_ = json.Unmarshal(body, &descs)
	}
	return descs
}

func (p *PluginExternalCardProvider) hasPlugin(ctx actor.PureContext, name string) bool {
	if p.findDevDir(ctx, name) != "" {
		return true
	}
	for _, d := range p.listInstalledPlugins(ctx) {
		if sanitizePathSegment(d.ID) == name {
			return true
		}
	}
	return false
}

// renderDefault renders a markdown body when no archive exists.
func (p *PluginExternalCardProvider) renderDefault(ctx actor.PureContext, name string) (string, error) {
	for _, d := range p.listInstalledPlugins(ctx) {
		if sanitizePathSegment(d.ID) == name {
			return fmt.Sprintf(
				"---\nid: %s\ntags: [__builtin_plugin__, plugin, component, capability, installed]\ndata:\n  componentKind: capability\n  source: pluginhost\n  pluginId: %s\n---\n\n# %s\n\n- ID: %s\n- Version: %s\n- Namespace: %s\n- Status: %s\n",
				d.Name, d.ID, d.Name, d.ID, d.Version, d.Namespace, d.Status,
			), nil
		}
	}
	devDir := p.findDevDir(ctx, name)
	if devDir == "" {
		devDir = filepath.Join(config.DataDir(), "data", "plugins", name)
	}
	return fmt.Sprintf(
		"---\nid: %s\ntags: [__builtin_plugin__, plugin, component, capability, dev]\ndata:\n  componentKind: capability\n  source: pluginhost\n  pluginId: %s\n---\n\n# %s\n\nDev plugin at %s\n",
		name, name, name, devDir,
	), nil
}

// devAppMounts queries the workspace actor for mounted dev-app projects and
// returns them sorted by sanitized mount name for stable output.
func (p *PluginExternalCardProvider) devAppMounts(ctx actor.PureContext) []domain.ProjectRef {
	ws, ok := ctx.LookupService("workspace")
	if !ok || ws == nil {
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	call := ws.Invoke(callCtx, "workspace.list_project", nil)
	if call == nil {
		return nil
	}
	defer call.Close()
	v, err := call.Final(callCtx)
	if err != nil || v == nil {
		return nil
	}
	var resp domain.ProjectRefListResp
	switch r := v.(type) {
	case domain.ProjectRefListResp:
		resp = r
	default:
		body, _ := json.Marshal(v)
		_ = json.Unmarshal(body, &resp)
	}
	var out []domain.ProjectRef
	for _, m := range resp.Items {
		if m.AppKind == "dev-app" {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return sanitizePathSegment(out[i].Name) < sanitizePathSegment(out[j].Name)
	})
	return out
}

// findDevDir returns the path of the mounted dev-app project whose sanitized
// mount name matches name.
func (p *PluginExternalCardProvider) findDevDir(ctx actor.PureContext, name string) string {
	for _, m := range p.devAppMounts(ctx) {
		if sanitizePathSegment(m.Name) == name {
			return m.Path
		}
	}
	return ""
}

func appendUniqueTag(tags []string, tag string) []string {
	for _, t := range tags {
		if t == tag {
			return tags
		}
	}
	return append(tags, tag)
}
