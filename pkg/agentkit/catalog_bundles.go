package agentkit

import (
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// CatalogBundles converts the builtin card assets into AppBundle entries.
// Each builtin card becomes one AppBundle:
//
//   - Title: the card's human-readable BuiltinTitle (e.g. "Web Search")
//   - Description: from frontmatter description (if present)
//   - GuideCardID: the card's canonical ID (builtin:bundle:web-search)
//   - Tools: the card's declared callable IDs as bare AppBundleTool entries
//     (no Effect/Service — card domain entries are a discovery cache, not
//     authoritative effect declarations; those come from the callable
//     registration surface at mount time)
//
// This is a pure function: it reads embedded assets and returns derived
// data. It does not create an execution entity.
func CatalogBundles() ([]gen.AppBundle, error) {
	assets, err := LoadBuiltinAssets()
	if err != nil {
		return nil, err
	}
	bundles := make([]gen.AppBundle, 0, len(assets))
	for _, asset := range assets {
		tools := make([]gen.AppBundleTool, 0, len(asset.Tools))
		for _, toolID := range asset.Tools {
			tools = append(tools, gen.AppBundleTool{
				CallableID: toolID,
			})
		}
		desc, _ := asset.Data["description"].(string)
		bundles = append(bundles, gen.AppBundle{
			Title:       asset.BuiltinTitle,
			Description: desc,
			GuideCardID: asset.Title,
			Tools:       tools,
		})
	}
	return bundles, nil
}
