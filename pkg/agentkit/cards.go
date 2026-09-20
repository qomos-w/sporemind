package agentkit

import (
	"fmt"
	"sort"
	"strings"
)

// BuiltinCard identifies a builtin component card asset.
type BuiltinCard struct {
	Title string
}

type BuiltinPromptCard struct {
	Title        string
	BuiltinTitle string
	Body         string
	Scope        string
	Role         string
	Priority     int32
}

// BuiltinCardAssets loads the canonical embedded Markdown and registration
// metadata for every builtin card.
func BuiltinPromptCards() ([]BuiltinPromptCard, error) {
	assets, err := LoadCardAssets()
	if err != nil {
		return nil, err
	}
	cards := make([]BuiltinPromptCard, 0)
	for _, asset := range assets {
		if !strings.HasPrefix(asset.Title, "prompt:profile:") && !strings.HasPrefix(asset.Title, "prompt:fragment:") {
			continue
		}
		cards = append(cards, BuiltinPromptCard{
			Title:        asset.Title,
			BuiltinTitle: asset.BuiltinTitle,
			Body:         asset.Body,
			Scope:        assetDataString(asset.Data, "scope"),
			Role:         assetDataString(asset.Data, "role"),
			Priority:     assetDataInt(asset.Data, "priority"),
		})
	}
	sort.Slice(cards, func(i, j int) bool { return cards[i].Title < cards[j].Title })
	return cards, nil
}

// BuiltinModeCard is a builtin mode component card projected for slash-mode
// interception in the conversation composer. Name is the short token the user
// types after "/" (e.g. "goal" for builtin:mode:goal).
type BuiltinModeCard struct {
	CardID string
	Name   string
	Title  string
	Icon   string
}

// BuiltinModeCards returns every builtin mode component card (builtin:mode:*).
// It is the data source for the workspace.builtin.modes.list callable so the
// frontend can preload a static slash-mode interception filter at startup.
func BuiltinModeCards() ([]BuiltinModeCard, error) {
	assets, err := LoadBuiltinAssets()
	if err != nil {
		return nil, err
	}
	var modes []BuiltinModeCard
	for _, asset := range assets {
		if asset.Type != "mode" {
			continue
		}
		name := strings.TrimPrefix(asset.Title, "builtin:mode:")
		if name == "" || name == asset.Title {
			continue
		}
		modes = append(modes, BuiltinModeCard{
			CardID: asset.Title,
			Name:   name,
			Title:  asset.BuiltinTitle,
			Icon:   asset.Icon,
		})
	}
	sort.Slice(modes, func(i, j int) bool { return modes[i].Name < modes[j].Name })
	return modes, nil
}

// SlashSlug normalizes text into a canonical hyphenated slug: lowercase with
// whitespace and underscores collapsed to single hyphens. It is the shared
// spelling rule that lets slash input match component cards whose display
// titles contain spaces ("Web Search" -> "web-search").
func SlashSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '_'
	})
	return strings.Join(parts, "-")
}

// slashMatchKeys returns the normalized forms a slash fragment may match on:
// the hyphenated slug and the separator-free compact form ("web-search",
// "websearch").
func slashMatchKeys(s string) map[string]bool {
	keys := map[string]bool{}
	if slug := SlashSlug(s); slug != "" {
		keys[slug] = true
		keys[strings.ReplaceAll(slug, "-", "")] = true
	}
	return keys
}

// ResolveBuiltinBundle maps a slash command fragment (name plus optional
// remaining args) onto a builtin bundle card. Slash input is tokenized on
// whitespace while bundle titles contain spaces, so matching accepts the
// hyphenated card slug ("web-search"), the concatenated form ("websearch"),
// and spaced input where the leading words spell the title ("/web search"
// arrives as name "web", args "search"). Leading words are consumed
// longest-first; unconsumed trailing words are returned as rest so the caller
// can forward them as the turn input, mirroring the builtin-mode slash path.
func ResolveBuiltinBundle(name, args string) (cardID, rest string, ok bool) {
	name = strings.TrimSpace(name)
	args = strings.TrimSpace(args)
	if name == "" {
		return "", "", false
	}
	assets, err := LoadBuiltinAssets()
	if err != nil {
		return "", "", false
	}
	words := strings.Fields(name + " " + args)
	for take := len(words); take >= 1; take-- {
		keys := slashMatchKeys(strings.Join(words[:take], " "))
		if len(keys) == 0 {
			continue
		}
		remainder := strings.TrimSpace(strings.Join(words[take:], " "))
		for _, asset := range assets {
			if asset.Type != "bundle" || !strings.HasPrefix(asset.Title, "builtin:bundle:") {
				continue
			}
			aliases := slashMatchKeys(strings.TrimPrefix(asset.Title, "builtin:bundle:"))
			for key := range slashMatchKeys(asset.BuiltinTitle) {
				aliases[key] = true
			}
			for key := range keys {
				if aliases[key] {
					return asset.Title, remainder, true
				}
			}
		}
	}
	return "", "", false
}

func assetDataString(data map[string]any, key string) string {
	value, _ := data[key].(string)
	return value
}

func assetDataInt(data map[string]any, key string) int32 {
	switch value := data[key].(type) {
	case int:
		return int32(value)
	case int32:
		return value
	case int64:
		return int32(value)
	case float64:
		return int32(value)
	default:
		return 0
	}
}

func sanitizeAssetKey(key string) string {
	return strings.NewReplacer("/", "-", "\\", "-", " ", "-").Replace(key)
}

func BuiltinCardAssets() (map[string]string, error) {
	assets, err := LoadBuiltinAssets()
	if err != nil {
		return nil, err
	}
	bodies := make(map[string]string, len(assets))
	for _, asset := range assets {
		bodies[asset.Title] = asset.Raw
	}
	for _, card := range BuiltinCards {
		if _, ok := bodies[card.Title]; !ok {
			return nil, fmt.Errorf("builtin card %q has no embedded asset", card.Title)
		}
	}
	return bodies, nil
}
