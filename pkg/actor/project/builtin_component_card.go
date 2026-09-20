package project

import (
	"fmt"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/agentkit"
	"github.com/qomos-w/sporemind/pkg/domain"
)

type BuiltinComponentCardProvider struct{}

func newBuiltinComponentCardProvider() *BuiltinComponentCardProvider {
	return &BuiltinComponentCardProvider{}
}
func (p *BuiltinComponentCardProvider) Prefix() string { return "builtin:" }

func (p *BuiltinComponentCardProvider) List(_ actor.PureContext) ([]domain.MonoCardListItem, error) {
	now := time.Now().Format(time.RFC3339)
	assets, err := agentkit.LoadBuiltinAssets()
	if err != nil {
		return nil, err
	}
	items := make([]domain.MonoCardListItem, 0, len(assets))
	for _, asset := range assets {
		kind := asset.Type
		if kind == "" {
			kind = "component"
		}
		item := domain.MonoCardListItem{
			ID: asset.Title, Type: kind, Source: asset.Source, Storage: asset.Storage, Visibility: asset.Visibility,
			Tags: asset.Tags, Created: now, Modified: now,
			Protected: asset.Protected, Editable: asset.Editable, Deletable: asset.Deletable,
			Data: map[string]any{
				"componentKind": kind, "source": asset.Source, "storage": asset.Storage, "visibility": asset.Visibility,
				"protected": asset.Protected, "editable": asset.Editable, "deletable": asset.Deletable,
				"title": asset.BuiltinTitle, "icon": asset.Icon,
			},
		}
		if asset.Visual != nil {
			item.Data["visual"] = map[string]any{
				"icon": asset.Visual.Icon, "accent": asset.Visual.Accent, "emphasis": asset.Visual.Emphasis,
				"color": asset.Visual.Color, "background": asset.Visual.Background, "border": asset.Visual.Border,
			}
			if asset.Visual.Size != 0 {
				item.Data["visual"].(map[string]any)["size"] = asset.Visual.Size
			}
		}
		if len(asset.Tools) > 0 {
			item.Data["tools"] = asset.Tools
		}
		if len(asset.Dependencies) > 0 {
			item.Data["requires"] = asset.Dependencies
		}
		for _, key := range []string{"settingsVisible", "settingsProtected", "modeManaged", "lifecycleManaged", "flow", "devOnly"} {
			if value, ok := asset.Data[key]; ok {
				item.Data[key] = value
			}
		}
		items = append(items, item)
	}
	return items, nil
}

func (p *BuiltinComponentCardProvider) Get(_ actor.PureContext, id string) (string, error) {
	assets, err := agentkit.LoadBuiltinAssets()
	if err != nil {
		return "", err
	}
	for _, asset := range assets {
		if asset.Title == id {
			return asset.Raw, nil
		}
	}
	return "", fmt.Errorf("builtin component %q not found", id)
}
func (p *BuiltinComponentCardProvider) Save(_ actor.PureContext, _ string, _ string) error {
	return fmt.Errorf("builtin components are protected")
}
func (p *BuiltinComponentCardProvider) Delete(_ actor.PureContext, _ string) error {
	return fmt.Errorf("builtin components are protected")
}
