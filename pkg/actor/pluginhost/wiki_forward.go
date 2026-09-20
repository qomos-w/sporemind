package pluginhost

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// handleHostBridgeWikiForward routes one project.wiki_* host call to the
// calling plugin's bound project actor via the workspace service. The
// binding (project id) arrives host-authored in the artifact's OnLoad config
// (backendLoadConfig.ProjectID, persisted in ArtifactLoads) — a zip-installed
// app with no project fails closed here instead of guessing a root.
func (a *Actor) handleHostBridgeWikiForward(callID, pluginID string, req []byte) ([]byte, error) {
	if pluginID == "" {
		return nil, fmt.Errorf("pluginhost: %s: calling plugin unknown", callID)
	}
	projectID := a.pluginProjectID(pluginID)
	if projectID == "" {
		return nil, fmt.Errorf("pluginhost: %s: plugin %q is not bound to a project (project-registered apps only)", callID, pluginID)
	}
	wsRef := a.streamServiceRef("workspace")
	if wsRef == nil {
		return nil, fmt.Errorf("pluginhost: %s: workspace service not available", callID)
	}
	fwd, err := json.Marshal(domain.ProjectWikiForwardReq{
		ProjectID: projectID,
		Callable:  callID,
		Payload:   json.RawMessage(req),
	})
	if err != nil {
		return nil, fmt.Errorf("pluginhost: %s: encode forward request: %w", callID, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), hostBridgeInvokeTimeout)
	defer cancel()
	call := wsRef.Invoke(ctx, "workspace.project_wiki_forward", fwd)
	if call == nil {
		return nil, fmt.Errorf("pluginhost: %s: workspace forward returned nil call", callID)
	}
	defer call.Close()
	raw, err := call.RecvRaw()
	if err != nil {
		return nil, fmt.Errorf("pluginhost: %s: workspace forward: %w", callID, err)
	}
	var resp domain.ProjectWikiForwardResp
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("pluginhost: %s: decode forward response: %w", callID, err)
	}
	return resp.Result, nil
}

// pluginProjectID reads the project binding the appmanager anchored for this
// plugin at artifact load (OnLoad config projectId), from the persisted load
// record. Empty when the app has no bound project.
func (a *Actor) pluginProjectID(pluginID string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	load, ok := a.ArtifactLoads[pluginID]
	if !ok {
		return ""
	}
	return parseOnLoadProjectID(load.OnLoadConfig)
}

// parseOnLoadProjectID extracts the projectId field from the plugin's
// host-authored OnLoad config JSON. Anything unparseable means no binding.
func parseOnLoadProjectID(onLoadConfig []byte) string {
	if len(onLoadConfig) == 0 {
		return ""
	}
	var cfg struct {
		ProjectID string `json:"projectId"`
	}
	if err := json.Unmarshal(onLoadConfig, &cfg); err != nil {
		return ""
	}
	return strings.TrimSpace(cfg.ProjectID)
}
