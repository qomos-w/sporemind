package domain

import (
	"encoding/json"
	"strings"
)

// Skill is a skill asset. Previously generated from schemas/skill._1548.spore;
// now maintained by hand as part of the mono-card migration.
type Skill struct {
	ID           string            `json:"Id"`
	Source       string            `json:"Source"`
	Version      int32             `json:"Version"`
	ForkOf       string            `json:"ForkOf"`
	BuiltinKey   string            `json:"BuiltinKey"`
	Name         string            `json:"Name"`
	Description  string            `json:"Description"`
	Tags         []string          `json:"Tags"`
	Tools        []string          `json:"Tools"`
	Params       []string          `json:"Params"`
	Context      string            `json:"Context"`
	Permission   string            `json:"Permission"`
	SubAgentType string            `json:"SubAgentType"`
	Unit         ModelUnit         `json:"Unit"`
	Template     string            `json:"Template"`
	Editable     bool              `json:"Editable"`
	Deletable    bool              `json:"Deletable"`
	ProjectID    string            `json:"ProjectID,omitempty"`
	Meta         map[string]string `json:"Meta,omitempty"`
	ModuleKind   string            `json:"ModuleKind,omitempty"`
}

// SkillManifest is a lightweight skill summary for system prompt.
type SkillManifest struct {
	ID          string   `json:"Id"`
	Name        string   `json:"Name"`
	Description string   `json:"Description"`
	Tags        []string `json:"Tags"`
	Tools       []string `json:"Tools"`
	Params      []string `json:"Params"`
	Context     string   `json:"Context,omitempty"`
	ProjectID   string   `json:"ProjectID,omitempty"`
}

// SkillManifestListResp wraps a skill manifest list response.
type SkillManifestListResp struct {
	Items []SkillManifest `json:"Items"`
}

// SkillManagerGetBodyReq is the request for skillmanager.get_body.
type SkillManagerGetBodyReq struct {
	ID   string `json:"Id"`
	Args string `json:"Args,omitempty"`
}

// SkillManagerGetBodyResp is the response for skillmanager.get_body.
type SkillManagerGetBodyResp struct {
	ID           string   `json:"Id"`
	Version      int32    `json:"Version"`
	Body         string   `json:"Body"`
	AllowedTools []string `json:"AllowedTools,omitempty"`
	Context      string   `json:"Context,omitempty"`
	ProjectID    string   `json:"ProjectID,omitempty"`
	ModuleKind   string   `json:"ModuleKind,omitempty"`
}

// SkillManagerGetReq is the request for skillmanager.get.
type SkillManagerGetReq struct {
	ID string `json:"Id"`
}

// SkillManagerListReq is the request for skillmanager.list.
type SkillManagerListReq struct {
	ProjectID string `json:"ProjectID,omitempty"`
}

// SkillManagerManifestListReq is the request for skillmanager.manifest.list.
type SkillManagerManifestListReq struct {
	ProjectID string `json:"ProjectID,omitempty"`
}

// SkillManagerSaveReq is the request for skillmanager.save.
type SkillManagerSaveReq struct {
	ID           string    `json:"Id"`
	Name         string    `json:"Name"`
	Description  string    `json:"Description"`
	Tags         []string  `json:"Tags"`
	Tools        []string  `json:"Tools"`
	Params       []string  `json:"Params"`
	Context      string    `json:"Context"`
	Permission   string    `json:"Permission"`
	SubAgentType string    `json:"SubAgentType"`
	Unit         ModelUnit `json:"Unit"`
	Template     string    `json:"Template"`
	ModuleKind   string    `json:"ModuleKind,omitempty"`
}

// SkillManagerDeleteReq is the request for skillmanager.delete.
type SkillManagerDeleteReq struct {
	ID string `json:"Id"`
}

// SkillInstallReq is the request for skillmanager.install.
type SkillInstallReq struct {
	Source SkillInstallSource `json:"Source"`
}

// SkillInstallSource describes an external skill source for skillmanager.install.
type SkillInstallSource struct {
	URL          string `json:"Url,omitempty"`
	GitURL       string `json:"GitUrl,omitempty"`
	GitRef       string `json:"GitRef,omitempty"`
	GitPath      string `json:"GitPath,omitempty"`
	LocalPath    string `json:"LocalPath,omitempty"`
	RegistryName string `json:"RegistryName,omitempty"`
	SkillName    string `json:"SkillName,omitempty"`
	Version      string `json:"Version,omitempty"`
}

// SkillImportReq is the request for skillmanager.import.
type SkillImportReq struct {
	Sources         []SkillImportSource `json:"Sources"`
	TargetProjectID string              `json:"TargetProjectId,omitempty"`
}

// SkillImportResp is the response for skillmanager.import.
type SkillImportResp struct {
	Items []SkillImportResult `json:"Items"`
}

// SkillImportResult is a single imported skill result.
type SkillImportResult struct {
	SkillID string `json:"SkillId"`
	Name    string `json:"Name"`
	Source  string `json:"Source"`
	Status  string `json:"Status"`
	Error   string `json:"Error"`
}

// SkillImportSource describes an external skill source for skillmanager.import.
type SkillImportSource struct {
	Kind string `json:"Kind"`
	Path string `json:"Path"`
}

// SkillListResp is the response for skillmanager.list.
type SkillListResp struct {
	Items []Skill `json:"Items"`
}

// AgentSkillMountReq is the request for agent_skill_mount.
type AgentSkillMountReq struct {
	SkillID string `json:"SkillId"`
	Args    string `json:"Args,omitempty"`
	Context string `json:"Context,omitempty"`
	TurnID  string `json:"TurnId,omitempty"`
}

// UnmarshalJSON parses AgentSkillMountReq with case-insensitive top-level keys.
// This tolerates LLMs that emit lowercase property names (skillId/args/turnId)
// even though the generated JSON tags use PascalCase.
func (r *AgentSkillMountReq) UnmarshalJSON(data []byte) error {
	type alias AgentSkillMountReq // avoid recursion
	_ = json.Unmarshal(data, (*alias)(r)) // ignore partial-match errors
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if v := getStringCI(raw, "SkillId"); v != "" {
		r.SkillID = v
	}
	if v := getStringCI(raw, "Args"); v != "" {
		r.Args = v
	}
	if v := getStringCI(raw, "Context"); v != "" {
		r.Context = v
	}
	if v := getStringCI(raw, "TurnId"); v != "" {
		r.TurnID = v
	}
	return nil
}

// getStringCI returns the string value for key from m using a case-insensitive
// lookup. If the value is not a string, it returns the empty string.
func getStringCI(m map[string]interface{}, key string) string {
	lower := strings.ToLower(key)
	for k, v := range m {
		if strings.ToLower(k) == lower {
			if s, ok := v.(string); ok {
				return s
			}
			return ""
		}
	}
	return ""
}

// AgentSkillMountResp is the response for agent_skill_mount.
type AgentSkillMountResp struct {
	MountID string `json:"MountId"`
	Body    string `json:"Body,omitempty"`
	SkillID string `json:"SkillId,omitempty"`
}
