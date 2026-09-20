package agentkit

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v2"
)

type CardAsset struct {
	Path         string
	Title        string
	BuiltinTitle string
	Tags         []string
	Type         string
	Source       string
	Storage      string
	Visibility   string
	Protected    bool
	Editable     bool
	Deletable    bool
	Icon         string
	Visual       *CardVisual
	Tools        []string
	Dependencies []string
	Fork         *ForkDecl
	ModeManaged  bool
	LifecycleManaged bool
	Flow         string
	Data         map[string]any
	Body         string
	Raw          string
}

// CardVisual mirrors the data.visual block of a MonoCard: icon-library
// presentation hints (lucide name, accent palette, explicit icon color).
type CardVisual struct {
	Icon       string
	Accent     string
	Emphasis   string
	Color      string
	Background string
	Border     string
	Size       int
}

// ForkDecl is the structured form of a bundle card's data.fork frontmatter.
// It declares a fork tool alias routed to workspace.agent_spawn_by_type. The
// bundle card is the single source of truth for fork tool metadata.
type ForkDecl struct {
	ToolName      string
	ChildKind     string
	Description   string
	MaxIterations int
}

type PromptAsset struct {
	Path string
	Body string
}

type SkillAsset struct {
	Title string
	Body  string
	Raw   string
}

func LoadCardAssets() ([]CardAsset, error) {
	var out []CardAsset
	seen := make(map[string]struct{})
	err := fs.WalkDir(embeddedAssets, "builtin/cards", func(filePath string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || path.Ext(entry.Name()) != ".md" {
			return nil
		}
		raw, err := fs.ReadFile(embeddedAssets, filePath)
		if err != nil {
			return fmt.Errorf("card asset %s: %w", filePath, err)
		}
		asset, err := parseCardAsset(filePath, string(raw))
		if err != nil {
			return fmt.Errorf("card asset %s: %w", filePath, err)
		}
		if _, ok := seen[asset.Title]; ok {
			return fmt.Errorf("duplicate card asset id %q", asset.Title)
		}
		seen[asset.Title] = struct{}{}
		out = append(out, asset)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	// Drift guard: builtin card ID prefix must agree with frontmatter type.
	// This catches accidental removal or mismatch of the type: field.
	for _, asset := range out {
		switch {
		case strings.HasPrefix(asset.Title, "builtin:bundle:") && asset.Type != "bundle":
			return nil, fmt.Errorf("builtin card %q: frontmatter type %q mismatches ID prefix (expected bundle)", asset.Title, asset.Type)
		case strings.HasPrefix(asset.Title, "builtin:mode:") && asset.Type != "mode":
			return nil, fmt.Errorf("builtin card %q: frontmatter type %q mismatches ID prefix (expected mode)", asset.Title, asset.Type)
		}
	}
	return out, nil
}

func parseCardAsset(filePath, raw string) (CardAsset, error) {
	asset := CardAsset{Path: filePath, Data: map[string]any{}, Body: raw, Raw: raw}
	if strings.HasPrefix(raw, "---") {
		end := strings.Index(raw[3:], "---")
		if end < 0 {
			return CardAsset{}, fmt.Errorf("unterminated YAML frontmatter")
		}
		front := raw[3 : 3+end]
		var metadata struct {
			ID          string         `yaml:"id"`
			Type        string         `yaml:"type"`
			Title       string         `yaml:"title"`
			Name        string         `yaml:"name"`
			Description string         `yaml:"description"`
			Tags        []string       `yaml:"tags"`
			Data        map[string]any `yaml:"data"`
		}
		if err := yaml.Unmarshal([]byte(front), &metadata); err != nil {
			return CardAsset{}, err
		}
		asset.Title = metadata.ID
		asset.BuiltinTitle = metadata.Title
		if asset.BuiltinTitle == "" {
			asset.BuiltinTitle = metadata.Name
		}
		asset.Tags = metadata.Tags
		asset.Data = metadata.Data
		if asset.Data == nil {
			asset.Data = map[string]any{}
		}
		asset.Type = metadata.Type
		asset.Source, _ = asset.Data["source"].(string)
		asset.Storage, _ = asset.Data["storage"].(string)
		asset.Visibility, _ = asset.Data["visibility"].(string)
		asset.Protected, _ = asset.Data["protected"].(bool)
		asset.Editable, _ = asset.Data["editable"].(bool)
		asset.Deletable, _ = asset.Data["deletable"].(bool)
		asset.ModeManaged, _ = asset.Data["modeManaged"].(bool)
	asset.LifecycleManaged, _ = asset.Data["lifecycleManaged"].(bool)
	asset.Flow, _ = asset.Data["flow"].(string)
		asset.Icon, _ = asset.Data["icon"].(string)
		if tools, ok := asset.Data["tools"].([]any); ok {
			for _, tool := range tools {
				if value, ok := tool.(string); ok {
					asset.Tools = append(asset.Tools, value)
				}
			}
		}
		// "requires" is the canonical wiki-card key (parsed identically by
		// project card_projection); "dependencies" is accepted for parity with
		// older card assets. Both populate the same dependency list.
		for _, depKey := range []string{"requires", "dependencies"} {
			deps, ok := asset.Data[depKey].([]any)
			if !ok {
				continue
			}
			for _, dep := range deps {
				switch v := dep.(type) {
				case string:
					asset.Dependencies = append(asset.Dependencies, v)
				case map[interface{}]any:
					if id, ok := v["id"].(string); ok {
						asset.Dependencies = append(asset.Dependencies, id)
					}
				case map[string]any:
					if id, ok := v["id"].(string); ok {
						asset.Dependencies = append(asset.Dependencies, id)
					}
				}
			}
		}
		if forkMap, ok := asset.Data["fork"].(map[interface{}]any); ok {
			fork := &ForkDecl{}
			fork.ToolName, _ = forkMap["toolName"].(string)
			fork.ChildKind, _ = forkMap["childKind"].(string)
			fork.Description, _ = forkMap["description"].(string)
			fork.MaxIterations = forkIntFromAny(forkMap["maxIterations"])
			if fork.ToolName != "" && fork.ChildKind != "" {
				asset.Fork = fork
			}
		}
		if _, ok := asset.Data["visual"]; ok {
			if v := parseCardVisual(asset.Data["visual"]); v != nil {
				asset.Visual = v
			}
		}
		if metadata.Description != "" {
			asset.Data["description"] = metadata.Description
		}
		asset.Body = strings.TrimSpace(raw[3+end+3:])
		asset.Raw = raw
	}
	if asset.Title == "" {
		asset.Title = derivedCardAssetTitle(filePath)
	}
	if asset.BuiltinTitle == "" {
		asset.BuiltinTitle = strings.TrimSuffix(path.Base(filePath), path.Ext(filePath))
	}
	return asset, nil
}

// parseCardVisual reads a frontmatter data.visual block into a CardVisual.
// Accepts both map[interface{}]any (yaml.v2) and map[string]any shapes.
func parseCardVisual(raw any) *CardVisual {
	fields := visualFields(raw)
	if fields == nil {
		return nil
	}
	str := func(key string) string {
		if s, ok := fields[key].(string); ok {
			return s
		}
		return ""
	}
	v := &CardVisual{}
	set := false
	if s := str("icon"); s != "" {
		v.Icon, set = s, true
	}
	if s := str("accent"); s != "" {
		v.Accent, set = s, true
	}
	if s := str("emphasis"); s != "" {
		v.Emphasis, set = s, true
	}
	if s := str("color"); s != "" {
		v.Color, set = s, true
	}
	if s := str("background"); s != "" {
		v.Background, set = s, true
	}
	if s := str("border"); s != "" {
		v.Border, set = s, true
	}
	if n := forkIntFromAny(fields["size"]); n != 0 {
		v.Size, set = n, true
	}
	if !set {
		return nil
	}
	return v
}

// visualFields normalizes the two yaml map shapes parseCardVisual accepts into a
// plain string-keyed lookup.
func visualFields(raw any) map[string]any {
	switch m := raw.(type) {
	case map[interface{}]any:
		out := make(map[string]any, len(m))
		for k, v := range m {
			if ks, ok := k.(string); ok {
				out[ks] = v
			}
		}
		return out
	case map[string]any:
		return m
	default:
		return nil
	}
}

func derivedCardAssetTitle(filePath string) string {
	rel := strings.TrimPrefix(filePath, "builtin/cards/")
	key := strings.TrimSuffix(rel, ".md")
	parts := strings.SplitN(key, "/", 2)
	if len(parts) == 2 {
		switch parts[0] {
		case "agent-roles":
			return "prompt:profile:project." + strings.ReplaceAll(parts[1], "/", ".")
		case "fragments":
			return "prompt:fragment:" + strings.ReplaceAll(parts[1], "/", "-")
		case "skills":
			return "skill:" + strings.ReplaceAll(parts[1], "/", "-")
		case "mode":
			if parts[1] == "goal-mode" {
				return "builtin:mode:goal"
			}
		}
	}
	return "builtin:" + strings.ReplaceAll(key, "/", ":")
}

func forkIntFromAny(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

// ForkDeclsFromAssets collects all non-nil Fork declarations from the given
// card assets, preserving asset order. Assets without a fork declaration are
// skipped.
func ForkDeclsFromAssets(assets []CardAsset) []ForkDecl {
	var out []ForkDecl
	for _, a := range assets {
		if a.Fork != nil {
			out = append(out, *a.Fork)
		}
	}
	return out
}

func LoadBuiltinAssets() ([]CardAsset, error) {
	assets, err := LoadCardAssets()
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]struct{}, len(BuiltinCards))
	for _, card := range BuiltinCards {
		allowed[card.Title] = struct{}{}
	}
	out := make([]CardAsset, 0, len(allowed))
	for _, asset := range assets {
		if _, ok := allowed[asset.Title]; ok {
			out = append(out, asset)
		}
	}
	if len(out) != len(allowed) {
		return nil, fmt.Errorf("builtin card assets=%d want=%d", len(out), len(allowed))
	}
	return out, nil
}

func LoadSkillAssets() ([]SkillAsset, error) {
	assets, err := LoadCardAssets()
	if err != nil {
		return nil, err
	}
	out := make([]SkillAsset, 0)
	for _, asset := range assets {
		if strings.HasPrefix(asset.Title, "skill:") {
			out = append(out, SkillAsset{Title: strings.TrimPrefix(asset.Title, "skill:"), Body: asset.Body, Raw: asset.Raw})
		}
	}
	return out, nil
}

func LoadPromptAssets() ([]PromptAsset, error) {
	assets, err := LoadCardAssets()
	if err != nil {
		return nil, err
	}
	out := make([]PromptAsset, 0)
	for _, asset := range assets {
		if strings.HasPrefix(asset.Title, "prompt:profile:") || strings.HasPrefix(asset.Title, "prompt:fragment:") {
			out = append(out, PromptAsset{Path: asset.Path, Body: asset.Body})
		}
	}
	return out, nil
}
