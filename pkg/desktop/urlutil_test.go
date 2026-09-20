package desktop

import "testing"

func TestIsBrowserInternalURL(t *testing.T) {
	cases := []struct {
		url      string
		internal bool
	}{
		{"https://example.com", false},
		{"http://localhost:5173", false},
		{"about:blank", false},
		{"file:///D:/web/page.html", false},
		{"file:///home/user/page.html", false},
		{"file://D:/web/page.html", false},
		{"chrome-error://chromewebdata/", true},
		{"chrome://version/", true},
		{"edge://settings/", true},
		{"about:neterror", true},
		{"data:text/html,foo", true},
		{"javascript:void(0)", true},
		{"", true},
		{"not-a-url", true},
	}

	for _, c := range cases {
		got := IsBrowserInternalURL(c.url)
		if got != c.internal {
			t.Errorf("IsBrowserInternalURL(%q) = %v, want %v", c.url, got, c.internal)
		}
	}
}
