package appmanager

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

// iconCatalogGen is generated from web/src/ui/ai/components/cardIconCatalog.ts
// by web/scripts/gen-icon-catalog.ts. Regenerate with `make gen-icon-catalog`.
//
//go:embed icon_catalog.gen.json
var iconCatalogGen []byte

var iconCatalogEntries []gen.AppManagerIconEntry

func init() {
	if err := json.Unmarshal(iconCatalogGen, &iconCatalogEntries); err != nil {
		panic(fmt.Sprintf("appmanager: embedded icon catalog is invalid: %v", err))
	}
	if len(iconCatalogEntries) == 0 {
		panic("appmanager: embedded icon catalog is empty")
	}
}

// handleIconNames serves the card/bundle icon catalog so agents pick valid
// names for data.icon / data.visual.icon instead of guessing lucide names.
func (a *Actor) handleIconNames(_ actor.PureContext, req gen.AppManagerIconNamesReq) (gen.AppManagerIconNamesResp, error) {
	query := strings.ToLower(strings.TrimSpace(req.Query))
	category := strings.TrimSpace(req.Category)
	items := make([]gen.AppManagerIconEntry, 0, len(iconCatalogEntries))
	for _, entry := range iconCatalogEntries {
		if category != "" && entry.Category != category {
			continue
		}
		if query != "" && !iconEntryMatches(entry, query) {
			continue
		}
		items = append(items, entry)
	}
	return gen.AppManagerIconNamesResp{Items: items}, nil
}

func iconEntryMatches(entry gen.AppManagerIconEntry, query string) bool {
	if strings.Contains(strings.ToLower(entry.Name), query) ||
		strings.Contains(strings.ToLower(entry.Label), query) ||
		strings.Contains(strings.ToLower(entry.Category), query) {
		return true
	}
	for _, keyword := range entry.Keywords {
		if strings.Contains(strings.ToLower(keyword), query) {
			return true
		}
	}
	return false
}

// bundleIconOrFallback resolves a declared bundle icon against the embedded
// host icon catalog. Empty or unknown names fall back to "package" — the same
// neutral default the frontend renders for out-of-catalog glyphs.
func bundleIconOrFallback(icon string) string {
	icon = strings.TrimSpace(icon)
	if icon == "" {
		return "package"
	}
	for _, entry := range iconCatalogEntries {
		if entry.Name == icon {
			return icon
		}
	}
	return "package"
}
