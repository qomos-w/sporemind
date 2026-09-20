package desktop

import (
	"fmt"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func TestAppendBrowserHistory(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	t.Run("appends visits oldest first with UTC RFC3339 stamps", func(t *testing.T) {
		hist := appendBrowserHistory(nil, "https://a.example/", "A", now)
		hist = appendBrowserHistory(hist, "https://b.example/", "B", now.Add(time.Minute))
		if len(hist) != 2 {
			t.Fatalf("len = %d, want 2", len(hist))
		}
		if hist[0].URL != "https://a.example/" || hist[1].URL != "https://b.example/" {
			t.Errorf("order = [%s %s], want a then b", hist[0].URL, hist[1].URL)
		}
		if got := hist[1].VisitedAt; got != "2026-09-10T12:01:00Z" {
			t.Errorf("VisitedAt = %q, want RFC3339 UTC", got)
		}
	})

	t.Run("revisit of the most recent entry refreshes it instead of duplicating", func(t *testing.T) {
		hist := appendBrowserHistory(nil, "https://a.example/", "", now)
		hist = appendBrowserHistory(hist, "https://a.example/", "Late Title", now.Add(time.Second))
		if len(hist) != 1 {
			t.Fatalf("len = %d, want 1 (no duplicate on reload)", len(hist))
		}
		if hist[0].Title != "Late Title" || hist[0].VisitedAt != now.Add(time.Second).UTC().Format(time.RFC3339) {
			t.Errorf("entry not refreshed: %+v", hist[0])
		}
	})

	t.Run("non-consecutive revisit appends a new entry", func(t *testing.T) {
		hist := appendBrowserHistory(nil, "https://a.example/", "", now)
		hist = appendBrowserHistory(hist, "https://b.example/", "", now)
		hist = appendBrowserHistory(hist, "https://a.example/", "", now)
		if len(hist) != 3 {
			t.Fatalf("len = %d, want 3", len(hist))
		}
	})

	t.Run("empty and internal URLs are ignored", func(t *testing.T) {
		hist := appendBrowserHistory(nil, "", "x", now)
		hist = appendBrowserHistory(hist, "chrome-error://chromewebdata/", "x", now)
		hist = appendBrowserHistory(hist, "data:text/plain,hi", "x", now)
		if len(hist) != 0 {
			t.Fatalf("len = %d, want 0", len(hist))
		}
	})

	t.Run("caps history at maxBrowserHistoryEntries dropping the oldest", func(t *testing.T) {
		var hist []domain.BrowserHistoryEntry
		for i := 0; i < maxBrowserHistoryEntries+37; i++ {
			hist = appendBrowserHistory(hist, fmt.Sprintf("https://x.example/%d", i), "", now)
		}
		if len(hist) != maxBrowserHistoryEntries {
			t.Fatalf("len = %d, want %d", len(hist), maxBrowserHistoryEntries)
		}
		wantFirst := fmt.Sprintf("https://x.example/%d", 37)
		if hist[0].URL != wantFirst {
			t.Errorf("oldest surviving = %q, want %q", hist[0].URL, wantFirst)
		}
	})
}

func TestUpdateLastHistoryTitle(t *testing.T) {
	t.Run("refreshes title when the last entry matches the confirmed URL", func(t *testing.T) {
		hist := []domain.BrowserHistoryEntry{
			{URL: "https://old.example/"},
			{URL: "https://current.example/"},
		}
		updateLastHistoryTitle(hist, "https://current.example/", "SPA Title")
		if hist[1].Title != "SPA Title" {
			t.Errorf("title = %q, want refreshed", hist[1].Title)
		}
	})

	t.Run("no-op on URL mismatch or empty title", func(t *testing.T) {
		hist := []domain.BrowserHistoryEntry{{URL: "https://a.example/", Title: "kept"}}
		updateLastHistoryTitle(hist, "https://other.example/", "T")
		updateLastHistoryTitle(hist, "https://a.example/", "")
		if hist[0].Title != "kept" {
			t.Errorf("title = %q, want untouched", hist[0].Title)
		}
	})
}
