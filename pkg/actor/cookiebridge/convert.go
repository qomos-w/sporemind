package cookiebridge

import (
	"sort"
	"strings"
	"time"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// ChromeCookie mirrors the chrome.cookies.Cookie object returned by
// chrome.cookies.getAll in the extension. Field names follow the Chrome API
// (camelCase) so the extension can POST payloads verbatim.
type ChromeCookie struct {
	Domain           string  `json:"domain"`
	Name             string  `json:"name"`
	Value            string  `json:"value"`
	Path             string  `json:"path"`
	Secure           bool    `json:"secure"`
	HTTPOnly         bool    `json:"httpOnly"`
	SameSite         string  `json:"sameSite"`
	ExpirationDate   float64 `json:"expirationDate"`
	HostOnly         bool    `json:"hostOnly"`
	Session          bool    `json:"session"`
	PartitionKeyJSON string  `json:"partitionKey,omitempty"`
}

// sessionCookieTTL is applied to Chrome session cookies (no expirationDate)
// so the migrated login state survives an instance restart. WebView2 writes
// Expires==0 as a session cookie, which would silently drop the login on
// window close — the opposite of what a migration tool must do.
const sessionCookieTTL = 30 * 24 * time.Hour

// convertChromeCookies maps Chrome cookies onto WebView2 cookie entries,
// optionally filtered to the requested domain. It returns the converted
// groups plus the number of cookies skipped (unrelated domain or no name).
// Cookie values are copied but never logged by the caller.
func convertChromeCookies(cookies []ChromeCookie, domainFilter string) (map[string][]domain.BrowserCookieEntry, int) {
	groups := make(map[string][]domain.BrowserCookieEntry)
	now := time.Now().Unix()
	skipped := 0
	for _, c := range cookies {
		if c.Name == "" || c.Domain == "" {
			skipped++
			continue
		}
		if domainFilter != "" && normalizeCookieDomain(c.Domain) != normalizeCookieDomain(domainFilter) {
			skipped++
			continue
		}
		e := domain.BrowserCookieEntry{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Expires:  int64(c.ExpirationDate),
			HttpOnly: c.HTTPOnly,
			Secure:   c.Secure,
			SameSite: sameSiteFromChrome(c.SameSite),
		}
		if e.Path == "" {
			e.Path = "/"
		}
		if e.Expires <= 0 {
			e.Expires = now + int64(sessionCookieTTL/time.Second)
		}
		key := normalizeCookieDomain(c.Domain)
		groups[key] = append(groups[key], e)
	}
	return groups, skipped
}

// sameSiteFromChrome maps chrome.cookies sameSite enums onto the WebView2
// values accepted by BrowserCookieEntry ("Strict", "Lax", "None", "").
func sameSiteFromChrome(s string) string {
	switch s {
	case "no_restriction", "None":
		return "None"
	case "lax", "Lax":
		return "Lax"
	case "strict", "Strict":
		return "Strict"
	default:
		return ""
	}
}

func normalizeCookieDomain(d string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(d), "."))
}

// domainsOf lists the normalized domains present in a cookie group map, for
// reporting counts and domain names (never cookie values) in tool results.
func domainsOf(groups map[string][]domain.BrowserCookieEntry) []string {
	out := make([]string, 0, len(groups))
	for k := range groups {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
