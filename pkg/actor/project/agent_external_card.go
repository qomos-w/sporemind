package project

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/timeutil"
)

const agentExternalPrefix = "agent:"

// AgentExternalCardProvider surfaces workspace agents as external cards.
type AgentExternalCardProvider struct{}

func newAgentExternalCardProvider() *AgentExternalCardProvider { return &AgentExternalCardProvider{} }

func (p *AgentExternalCardProvider) Prefix() string { return agentExternalPrefix }

func agentCardID(id string) string { return agentExternalPrefix + sanitizePathSegment(id) }

func agentNameFromID(id string) (string, bool) {
	if !strings.HasPrefix(id, agentExternalPrefix) {
		return "", false
	}
	name := strings.TrimPrefix(id, agentExternalPrefix)
	return name, name != ""
}

func agentArchiveFile(name string) string {
	return filepath.Join(config.DataDir(), "data", "agents", name+".md")
}

func (p *AgentExternalCardProvider) list(ctx actor.PureContext) []domain.AgentRef {
	workspace, ok := ctx.LookupService("workspace")
	if !ok || workspace == nil {
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	payload, _ := json.Marshal(domain.WorkspaceListAgentsReq{ProjectID: ctx.Self().ID().String()})
	call := workspace.Invoke(callCtx, "workspace.list_agents", payload)
	if call == nil {
		return nil
	}
	defer call.Close()
	v, _ := call.Final(callCtx)
	if v == nil {
		return nil
	}
	var resp domain.AgentRefListResp
	switch x := v.(type) {
	case domain.AgentRefListResp:
		resp = x
	default:
		body, _ := json.Marshal(v)
		_ = json.Unmarshal(body, &resp)
	}
	return resp.Items
}

func agentCardTitle(ref domain.AgentRef) string {
	if ref.Title != "" {
		return ref.Title
	}
	if ref.DisplayName != "" {
		return ref.DisplayName
	}
	return ref.ID
}

func agentCardItem(ref domain.AgentRef, now string) domain.MonoCardListItem {
	data := map[string]any{}
	if ref.ID != "" {
		data["agentId"] = ref.ID
	}
	if ref.ActorID != "" {
		data["agentActorId"] = ref.ActorID
	}
	if ref.AgentKind != "" {
		data["agentKind"] = ref.AgentKind
	}
	if ref.Status != "" {
		data["agentStatus"] = ref.Status
	}
	if ref.Title != "" {
		data["agentTitle"] = ref.Title
	}
	if ref.DisplayName != "" {
		data["agentDisplayName"] = ref.DisplayName
	}
	data["storage"] = "external"
	data["visibility"] = "component"
	data["protected"] = true
	modified := now
	if ref.LastActivity != "" {
		modified = timeutil.ToLocalISO(ref.LastActivity)
	}
	return domain.MonoCardListItem{
		ID: agentCardID(ref.ID), Type: "agent", Source: "workspace", Storage: "external", Visibility: "component",
		Tags: []string{"agent"}, List: []string{}, Created: now, Modified: modified, Raw: "",
		Protected: true, Editable: false, Deletable: false, Data: data,
	}
}

func (p *AgentExternalCardProvider) List(ctx actor.PureContext) ([]domain.MonoCardListItem, error) {
	now := time.Now().Format(time.RFC3339)
	refs := p.list(ctx)
	items := make([]domain.MonoCardListItem, 0, len(refs))
	for _, ref := range refs {
		items = append(items, agentCardItem(ref, now))
	}
	return items, nil
}

func (p *AgentExternalCardProvider) Get(ctx actor.PureContext, id string) (string, error) {
	name, ok := agentNameFromID(id)
	if !ok {
		return "", fmt.Errorf("agent external card: invalid id %s", id)
	}
	archive := agentArchiveFile(name)
	if data, err := os.ReadFile(archive); err == nil {
		return string(data), nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("agent external card: read archive: %w", err)
	}
	for _, ref := range p.list(ctx) {
		if sanitizePathSegment(ref.ID) != name {
			continue
		}
		title := agentCardTitle(ref)
		return fmt.Sprintf("---\nid: %s\ntype: agent\ntags: [agent]\ndata:\n  agentId: %s\n  agentKind: %s\n  agentStatus: %s\n  agentTitle: %s\n  agentActorId: %s\n  agentDisplayName: %s\n---\n\n# %s\n", title, ref.ID, ref.AgentKind, ref.Status, ref.Title, ref.ActorID, ref.DisplayName, title), nil
	}
	return "", fmt.Errorf("agent external card: agent not found")
}

func (p *AgentExternalCardProvider) Save(_ actor.PureContext, id string, raw string) error {
	name, ok := agentNameFromID(id)
	if !ok {
		return fmt.Errorf("agent external card: invalid id %s", id)
	}
	path := agentArchiveFile(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("agent external card: create dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		return fmt.Errorf("agent external card: write archive: %w", err)
	}
	return nil
}

func (p *AgentExternalCardProvider) Delete(_ actor.PureContext, id string) error {
	name, ok := agentNameFromID(id)
	if !ok {
		return fmt.Errorf("agent external card: invalid id %s", id)
	}
	if err := os.Remove(agentArchiveFile(name)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("agent external card: delete archive: %w", err)
	}
	return nil
}
