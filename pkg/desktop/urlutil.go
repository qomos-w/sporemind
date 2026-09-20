package desktop

import "net/url"

// IsBrowserInternalURL reports whether u is a browser-internal URL that should
// not be surfaced as the user-visible URL or persisted in browser state.
// Normal http/https/file URLs and about:blank are allowed; everything else is
// treated as internal so that error pages like chrome-error://chromewebdata/
// cannot pollute the address bar or saved window state.
func IsBrowserInternalURL(u string) bool {
	if u == "" {
		return true
	}
	parsed, err := url.Parse(u)
	if err != nil || parsed.Scheme == "" {
		return true
	}
	switch parsed.Scheme {
	case "http", "https", "file":
		return false
	case "about":
		// about:blank is a legitimate blank page; all other about: URLs
		// (e.g. about:neterror) are internal. url.Parse puts "blank" in Opaque.
		if parsed.Host == "" && (parsed.Opaque == "blank" || parsed.Path == "blank") {
			return false
		}
	}
	return true
}
