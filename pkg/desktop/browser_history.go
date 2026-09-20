package desktop

import (
	"time"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// maxBrowserHistoryEntries caps the per-instance visit history persisted in
// BrowserWindowState.History; the oldest entries are dropped beyond the cap.
const maxBrowserHistoryEntries = 500

// appendBrowserHistory records one confirmed visit, oldest first. A revisit of
// the most recent entry (reload, re-submitted address) refreshes its title and
// timestamp in place instead of duplicating it; earlier entries are never
// deduplicated so the trail stays chronological. Empty and browser-internal
// URLs (chrome-error://, data:, …) are ignored so they never pollute history —
// mirroring what the address bar accepts as a confirmed document.
func appendBrowserHistory(hist []domain.BrowserHistoryEntry, url, title string, now time.Time) []domain.BrowserHistoryEntry {
	if url == "" || IsBrowserInternalURL(url) {
		return hist
	}
	ts := now.UTC().Format(time.RFC3339)
	if n := len(hist); n > 0 && hist[n-1].URL == url {
		hist[n-1].Title = title
		hist[n-1].VisitedAt = ts
		return hist
	}
	hist = append(hist, domain.BrowserHistoryEntry{URL: url, Title: title, VisitedAt: ts})
	if len(hist) > maxBrowserHistoryEntries {
		hist = hist[len(hist)-maxBrowserHistoryEntries:]
	}
	return hist
}

// updateLastHistoryTitle refreshes the title of the most recent entry when it
// matches the confirmed document: SPA titles routinely arrive after the commit
// boundary via injected page-info messages.
func updateLastHistoryTitle(hist []domain.BrowserHistoryEntry, url, title string) {
	if n := len(hist); n > 0 && title != "" && hist[n-1].URL == url {
		hist[n-1].Title = title
	}
}
