package project

import (
	"fmt"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	"gopkg.in/yaml.v2"
)

// stubMcpServers returns a listServers function yielding the given views.
func stubMcpServers(servers ...domain.McpServerView) func(actor.PureContext) []domain.McpServerView {
	return func(actor.PureContext) []domain.McpServerView { return servers }
}

func viewer(id, name, transport string) domain.McpServerView {
	return domain.McpServerView{ID: id, Name: name, Transport: transport, Enabled: true}
}

func TestMcpExternalCardProvider_List(t *testing.T) {
	p := &McpExternalCardProvider{listServers: stubMcpServers(
		viewer("srv-0", "filesystem", "stdio"),
		viewer("srv-1", "fetch", "http"),
	)}
	items, err := p.List(actor.PureContext(nil))
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 cards, got %d", len(items))
	}
	if items[0].ID != "mcp:srv-0" || items[1].ID != "mcp:srv-1" {
		t.Errorf("unexpected card IDs: %q, %q", items[0].ID, items[1].ID)
	}

	item := items[0]
	if item.Type != "bundle" {
		t.Errorf("Type = %q, want bundle", item.Type)
	}
	if item.Source != "mcpmanager" || item.Storage != "external" || item.Visibility != "component" {
		t.Errorf("unexpected metadata: source=%q storage=%q visibility=%q", item.Source, item.Storage, item.Visibility)
	}
	if !item.Protected || item.Editable || item.Deletable {
		t.Errorf("expected Protected=true Editable=false Deletable=false, got %+v", item)
	}
	if item.Data["componentKind"] != "bundle" {
		t.Errorf("componentKind = %v, want bundle", item.Data["componentKind"])
	}
	if item.Data["mcpServerId"] != "srv-0" || item.Data["mcpServerName"] != "filesystem" || item.Data["mcpTransport"] != "stdio" {
		t.Errorf("unexpected MCP data: %v", item.Data)
	}
	if item.Data["icon"] != "plug" {
		t.Errorf("icon = %v, want plug (composer badges require a non-empty icon)", item.Data["icon"])
	}
	// Design constraint: MCP cards must NOT carry data.tools.
	if _, hasTools := item.Data["tools"]; hasTools {
		t.Errorf("mcp card must not carry data.tools: %v", item.Data)
	}
}

func TestMcpExternalCardProvider_ListTracksServerChanges(t *testing.T) {
	servers := []domain.McpServerView{viewer("srv-0", "a", "stdio"), viewer("srv-1", "b", "http")}
	p := &McpExternalCardProvider{listServers: stubMcpServers(servers...)}
	items, err := p.List(actor.PureContext(nil))
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 cards before removal, got %d", len(items))
	}

	// Server srv-1 removed: List must silently skip it.
	p.listServers = stubMcpServers(viewer("srv-0", "a", "stdio"))
	items, err = p.List(actor.PureContext(nil))
	if err != nil {
		t.Fatalf("List after removal failed: %v", err)
	}
	if len(items) != 1 || items[0].ID != "mcp:srv-0" {
		t.Fatalf("expected only mcp:srv-0 after removal, got %+v", items)
	}

	// Server renamed: card reflects the latest config.
	p.listServers = stubMcpServers(viewer("srv-0", "renamed", "stdio"))
	items, err = p.List(actor.PureContext(nil))
	if err != nil {
		t.Fatalf("List after update failed: %v", err)
	}
	if got := items[0].Data["mcpServerName"]; got != "renamed" {
		t.Errorf("mcpServerName after update = %v, want renamed", got)
	}
}

func TestMcpExternalCardProvider_Get(t *testing.T) {
	p := &McpExternalCardProvider{listServers: stubMcpServers(
		viewer("srv-0", "filesystem", "stdio"),
	)}
	raw, err := p.Get(actor.PureContext(nil), "mcp:srv-0")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	for _, want := range []string{
		`id: "mcp:srv-0"`,
		"type: bundle",
		"componentKind: bundle",
		"title: \"filesystem\"",
		"icon: plug",
		`mcpServerId: "srv-0"`,
		`mcpServerName: "filesystem"`,
		`mcpTransport: "stdio"`,
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("raw missing %q:\n%s", want, raw)
		}
	}
	if strings.Contains(raw, "tools:") {
		t.Errorf("raw must not contain data.tools:\n%s", raw)
	}
	// The frontmatter must be parseable YAML so the compiler/UI can read it.
	fm, ok := strings.CutPrefix(raw, "---\n")
	if !ok {
		t.Fatalf("raw has no frontmatter:\n%s", raw)
	}
	end := strings.Index(fm, "\n---")
	if end < 0 {
		t.Fatalf("raw frontmatter has no closing ---:\n%s", raw)
	}
	var fmData map[string]any
	if err := yaml.Unmarshal([]byte(fm[:end]), &fmData); err != nil {
		t.Fatalf("frontmatter is not valid YAML: %v\n%s", err, fm[:end])
	}
	// yaml.v2 yields map[interface{}]interface{} for nested maps.
	data, ok := fmData["data"].(map[interface{}]interface{})
	if !ok {
		t.Fatalf("frontmatter has no data block: %v", fmData)
	}
	if fmt.Sprint(data["mcpServerId"]) != "srv-0" || fmt.Sprint(data["componentKind"]) != "bundle" {
		t.Errorf("unexpected frontmatter data: %v", data)
	}
	if fmt.Sprint(data["title"]) != "filesystem" || fmt.Sprint(data["icon"]) != "plug" {
		t.Errorf("frontmatter must declare display title and icon: %v", data)
	}
}

func TestMcpExternalCardProvider_GetUnknownOrRemovedServer(t *testing.T) {
	p := &McpExternalCardProvider{listServers: stubMcpServers(viewer("srv-0", "filesystem", "stdio"))}
	// Unknown server id.
	if _, err := p.Get(actor.PureContext(nil), "mcp:nope"); err == nil {
		t.Error("expected error for unknown server id")
	}
	// Server removed from config: Get must return an error.
	p.listServers = stubMcpServers()
	if _, err := p.Get(actor.PureContext(nil), "mcp:srv-0"); err == nil {
		t.Error("expected error for removed server")
	}
}

func TestMcpExternalCardProvider_GetInvalidID(t *testing.T) {
	p := &McpExternalCardProvider{listServers: stubMcpServers()}
	for _, id := range []string{"", "mcp:", "foo", "agent:foo", "plugin:foo", "builtin:bundle:x", "ext-skill:claude:x"} {
		if _, err := p.Get(actor.PureContext(nil), id); err == nil {
			t.Errorf("expected error for invalid id %q", id)
		}
	}
}

func TestMcpExternalCardProvider_SaveDeleteRejected(t *testing.T) {
	p := &McpExternalCardProvider{listServers: stubMcpServers(viewer("srv-0", "filesystem", "stdio"))}
	if err := p.Save(actor.PureContext(nil), "mcp:srv-0", "raw"); err == nil {
		t.Error("expected Save to be rejected")
	}
	if err := p.Delete(actor.PureContext(nil), "mcp:srv-0"); err == nil {
		t.Error("expected Delete to be rejected")
	}
}

// TestMcpExternalPrefixNoConflict verifies that "mcp:" resolves to the MCP
// provider and does not collide with the other four external providers.
func TestMcpExternalPrefixNoConflict(t *testing.T) {
	a := &Actor{
		externalProviders: []ExternalCardProvider{
			newBuiltinComponentCardProvider(),
			newPluginExternalCardProvider(),
			newAgentExternalCardProvider(),
			newExternalSkillCardProvider(""),
			newMcpExternalCardProvider(),
		},
	}
	cases := map[string]string{
		"builtin:bundle:project-wiki": "builtin:",
		"plugin:foo":                 "plugin:",
		"agent:agent-1":              "agent:",
		"ext-skill:claude:skill":     "ext-skill:",
		"mcp:srv-0":                  "mcp:",
	}
	for id, wantPrefix := range cases {
		provider := a.externalProviderFor(id)
		if provider == nil {
			t.Errorf("no provider for %q", id)
			continue
		}
		if got := provider.Prefix(); got != wantPrefix {
			t.Errorf("provider for %q has prefix %q, want %q", id, got, wantPrefix)
		}
	}
}

// TestNewActorRegistersMcpExternalCardProvider verifies the provider is part
// of the default external provider set built by NewActor (project OnInit
// registration path).
func TestNewActorRegistersMcpExternalCardProvider(t *testing.T) {
	a := NewActor(t.TempDir())().(*Actor)
	if provider := a.externalProviderFor("mcp:srv-0"); provider == nil {
		t.Fatal("mcp: provider not registered in NewActor externalProviders")
	} else if provider.Prefix() != "mcp:" {
		t.Errorf("unexpected prefix %q", provider.Prefix())
	}
}

// TestMcpExternalCardProvider_ListEmpty: no configured servers produce an
// empty card list, not an error.
func TestMcpExternalCardProvider_ListEmpty(t *testing.T) {
	p := &McpExternalCardProvider{listServers: stubMcpServers()}
	items, err := p.List(actor.PureContext(nil))
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected empty card list, got %d items", len(items))
	}
}

// TestMcpExternalCardProvider_ListSortedByID: cards are sorted by card ID
// regardless of the underlying server list order.
func TestMcpExternalCardProvider_ListSortedByID(t *testing.T) {
	p := &McpExternalCardProvider{listServers: stubMcpServers(
		viewer("srv-2", "zulu", "stdio"),
		viewer("srv-0", "alpha", "http"),
		viewer("srv-1", "bravo", "stdio"),
	)}
	items, err := p.List(actor.PureContext(nil))
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 cards, got %d", len(items))
	}
	for i, want := range []string{"mcp:srv-0", "mcp:srv-1", "mcp:srv-2"} {
		if items[i].ID != want {
			t.Errorf("items[%d].ID = %q, want %q (cards must be sorted by ID)", i, items[i].ID, want)
		}
	}
}

// TestMcpExternalCardProvider_NameFallbackToID: a server without a display
// name falls back to its ID for the card title and the rendered heading.
func TestMcpExternalCardProvider_NameFallbackToID(t *testing.T) {
	p := &McpExternalCardProvider{listServers: stubMcpServers(
		domain.McpServerView{ID: "srv-0", Transport: "stdio", Enabled: true},
	)}
	items, err := p.List(actor.PureContext(nil))
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if got := items[0].Data["title"]; got != "srv-0" {
		t.Errorf("List title = %v, want fallback to server ID", got)
	}
	raw, err := p.Get(actor.PureContext(nil), "mcp:srv-0")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if !strings.Contains(raw, `title: "srv-0"`) {
		t.Errorf("rendered title should fall back to server ID:\n%s", raw)
	}
}

// TestMcpExternalCardProvider_GetReflectsServerUpdates: updates to the server
// config (rename, transport change) are immediately visible in Get's render,
// covering the server-update leg of the lifecycle.
func TestMcpExternalCardProvider_GetReflectsServerUpdates(t *testing.T) {
	p := &McpExternalCardProvider{listServers: stubMcpServers(viewer("srv-0", "filesystem", "stdio"))}
	raw, err := p.Get(actor.PureContext(nil), "mcp:srv-0")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if !strings.Contains(raw, `mcpServerName: "filesystem"`) || !strings.Contains(raw, `mcpTransport: "stdio"`) {
		t.Fatalf("expected original config in card:\n%s", raw)
	}
	p.listServers = stubMcpServers(viewer("srv-0", "renamed", "http"))
	raw, err = p.Get(actor.PureContext(nil), "mcp:srv-0")
	if err != nil {
		t.Fatalf("Get after update failed: %v", err)
	}
	if !strings.Contains(raw, `mcpServerName: "renamed"`) || !strings.Contains(raw, `mcpTransport: "http"`) {
		t.Errorf("Get must reflect updated server config:\n%s", raw)
	}
}

// TestMcpServerIDFromCardID checks the sanitized server-id extraction used by
// Get and the provider router: prefix + non-empty, non-path segment.
func TestMcpServerIDFromCardID(t *testing.T) {
	cases := []struct {
		in     string
		wantID string
		ok     bool
	}{
		{"mcp:srv-0", "srv-0", true},
		{"mcp:my_server", "my_server", true},
		{"mcp:", "", false},
		{"mcp:.", "", false},
		{"mcp:..", "", false},
		{"mcp:a/b", "", false},
		{"mcp:a\\b", "", false},
		{"foo:srv-0", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		id, ok := mcpServerIDFromCardID(c.in)
		if ok != c.ok || id != c.wantID {
			t.Errorf("mcpServerIDFromCardID(%q) = (%q,%v), want (%q,%v)", c.in, id, ok, c.wantID, c.ok)
		}
	}
}

// TestMcpExternalCardFilteredFromListing verifies that handleWikiListCards
// excludes MCP cards from the default listing (no IncludeBuiltin) but keeps
// them when IncludeBuiltin=true (topology graph path).
func TestMcpExternalCardFilteredFromListing(t *testing.T) {
	a := &Actor{
		store: &testCardStore{},
		externalProviders: []ExternalCardProvider{
			&McpExternalCardProvider{listServers: stubMcpServers(viewer("srv-0", "filesystem", "stdio"))},
		},
	}
	ctx := actor.PureContext(nil)

	resp, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true})
	if err != nil {
		t.Fatalf("listCards failed: %v", err)
	}
	for _, c := range resp.Cards {
		if strings.HasPrefix(c.ID, "mcp:") {
			t.Errorf("MCP card %q should be filtered from default listing", c.ID)
		}
	}

	resp, err = a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true, IncludeBuiltin: true})
	if err != nil {
		t.Fatalf("listCards with IncludeBuiltin failed: %v", err)
	}
	found := false
	for _, c := range resp.Cards {
		if c.ID == "mcp:srv-0" {
			found = true
		}
	}
	if !found {
		t.Error("MCP card mcp:srv-0 should appear with IncludeBuiltin=true")
	}
}
