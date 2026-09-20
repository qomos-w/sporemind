package desktop

import "github.com/qomos-w/sporemind/pkg/domain"

// sameSiteFromInt32 maps the WebView2 COREWEBVIEW2_COOKIE_SAME_SITE_KIND enum
// to the canonical string used by BrowserCookieEntry.
//  0 = None, 1 = Lax, 2 = Strict
func sameSiteFromInt32(v int32) string {
	switch v {
	case 0:
		return "None"
	case 1:
		return "Lax"
	case 2:
		return "Strict"
	default:
		return ""
	}
}

// sameSiteToInt32 maps the canonical SameSite string to the WebView2 enum.
// Empty string maps to Lax (WebView2 default for new cookies without SameSite).
func sameSiteToInt32(s string) int32 {
	switch s {
	case "None", "none":
		return 0
	case "Lax", "lax":
		return 1
	case "Strict", "strict":
		return 2
	default:
		return 1 // Lax
	}
}

// groupCookiesByDomain groups a flat cookie slice by the Domain field.
func groupCookiesByDomain(entries []domain.BrowserCookieEntry) map[string][]domain.BrowserCookieEntry {
	out := make(map[string][]domain.BrowserCookieEntry, len(entries))
	for _, e := range entries {
		key := e.Domain
		out[key] = append(out[key], e)
	}
	return out
}
