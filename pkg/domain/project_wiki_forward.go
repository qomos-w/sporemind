package domain

import "encoding/json"

// ProjectWikiForwardReq is the wire shape of workspace.project_wiki_forward:
// the pluginhost routes a plugin's project.wiki_* host call to the plugin's
// bound project actor through the workspace (the only actor that can resolve
// a project actor by mount name — project domains are ExposeChildren).
type ProjectWikiForwardReq struct {
	ProjectID string          `json:"ProjectId"`
	Callable  string          `json:"Callable"`
	Payload   json.RawMessage `json:"Payload,omitempty"`
}

// ProjectWikiForwardResp echoes the forwarded callable's raw JSON response.
type ProjectWikiForwardResp struct {
	Result json.RawMessage `json:"Result,omitempty"`
}
