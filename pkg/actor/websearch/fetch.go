package websearch

import (
	"fmt"
	"io"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/qomos-w/gospore/actor"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"golang.org/x/net/html"
)

const (
	// maxBodyBytes limits the HTTP response body read to 2 MB.
	maxBodyBytes int64 = 2 * 1024 * 1024
	// defaultMaxChars is the default character limit for extracted text.
	defaultMaxChars = 10_000
)

// handleFetch fetches a URL via HTTP GET, parses the HTML, strips non-content
// elements, extracts visible text and page metadata.
//
// Stateless (PureContext): the GET can block for defaultHTTPTimeout (30s), so
// it must not occupy the owner lane (constraints "Owner Lane 禁阻塞"). It
// touches no shared mutable state — only the immutable a.http client — so it
// is safe to run on forked goroutines with no mailbox serialization.
func (a *Actor) handleFetch(_ actor.PureContext, req gen.WebFetchReq) (gen.WebFetchResp, error) {
	url := strings.TrimSpace(req.URL)
	if url == "" {
		return gen.WebFetchResp{}, fmt.Errorf("websearch.fetch: url is required")
	}

	resp, err := a.http.Get(url)
	if err != nil {
		return gen.WebFetchResp{}, fmt.Errorf("websearch.fetch: GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return gen.WebFetchResp{}, fmt.Errorf("websearch.fetch: GET %s returned HTTP %d", url, resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return gen.WebFetchResp{}, fmt.Errorf("websearch.fetch: read body: %w", err)
	}
	bodyTooLarge := int64(len(bodyBytes)) > maxBodyBytes
	if bodyTooLarge {
		bodyBytes = bodyBytes[:maxBodyBytes]
	}

	// Skip non-HTML content types; return empty result without parsing.
	ct := resp.Header.Get("Content-Type")
	if ct != "" && !strings.HasPrefix(ct, "text/html") && !strings.HasPrefix(ct, "text/plain") {
		return gen.WebFetchResp{URL: url}, nil
	}

	doc, err := html.Parse(strings.NewReader(string(bodyBytes)))
	if err != nil {
		return gen.WebFetchResp{}, fmt.Errorf("websearch.fetch: parse HTML: %w", err)
	}

	title := extractTitle(doc)
	meta := extractMeta(doc)
	meta.Lang = extractLang(doc)

	removeElements(doc, "script", "style", "nav", "aside", "footer", "header")

	text := extractText(doc)
	text = strings.TrimSpace(text)
	sanitized := bluemonday.UGCPolicy().Sanitize(text)
	sanitized = strings.TrimSpace(sanitized)

	maxChars := int(req.MaxChars)
	truncated := bodyTooLarge
	if maxChars <= 0 {
		maxChars = defaultMaxChars
	}
	runes := []rune(sanitized)
	if len(runes) > maxChars {
		sanitized = string(runes[:maxChars])
		truncated = true
	}

	return gen.WebFetchResp{
		URL:       url,
		Title:     title,
		Text:      sanitized,
		Meta:      meta,
		Truncated: truncated,
	}, nil
}

// extractTitle finds the text content of the first <title> element.
func extractTitle(n *html.Node) string {
	if n.Type == html.ElementNode && n.Data == "title" {
		return strings.TrimSpace(getTextContent(n))
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if t := extractTitle(c); t != "" {
			return t
		}
	}
	return ""
}

// extractMeta extracts meta description and site name from the document.
func extractMeta(n *html.Node) gen.WebFetchMeta {
	var meta gen.WebFetchMeta
	if n.Type == html.ElementNode && n.Data == "meta" {
		name := getAttr(n, "name")
		property := getAttr(n, "property")
		content := getAttr(n, "content")

		if meta.Description == "" && (strings.EqualFold(name, "description") || strings.EqualFold(property, "og:description")) {
			meta.Description = content
		}
		if meta.SiteName == "" && (strings.EqualFold(name, "site_name") || strings.EqualFold(property, "og:site_name")) {
			meta.SiteName = content
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		sub := extractMeta(c)
		if meta.Description == "" {
			meta.Description = sub.Description
		}
		if meta.SiteName == "" {
			meta.SiteName = sub.SiteName
		}
	}
	return meta
}

// extractLang extracts the lang attribute from the <html> element.
func extractLang(n *html.Node) string {
	if n.Type == html.ElementNode && n.Data == "html" {
		return getAttr(n, "lang")
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if lang := extractLang(c); lang != "" {
			return lang
		}
	}
	return ""
}

// removeElements removes all elements with the given tag names from the tree.
func removeElements(n *html.Node, tags ...string) {
	var next *html.Node
	for c := n.FirstChild; c != nil; c = next {
		next = c.NextSibling
		if c.Type == html.ElementNode && containsTag(tags, c.Data) {
			n.RemoveChild(c)
		} else {
			removeElements(c, tags...)
		}
	}
}

// extractText recursively extracts visible text content from the node tree.
func extractText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	if n.Type == html.ElementNode && isBlockElement(n.Data) {
		var parts []string
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if t := strings.TrimSpace(extractText(c)); t != "" {
				parts = append(parts, t)
			}
		}
		if len(parts) > 0 {
			return "\n" + strings.Join(parts, "\n") + "\n"
		}
		return ""
	}
	var text string
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		text += extractText(c)
	}
	return text
}

// getTextContent extracts all text content from a node's descendants.
func getTextContent(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var text string
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		text += getTextContent(c)
	}
	return text
}

// getAttr returns the value of an attribute or empty string.
func getAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// containsTag checks if a tag name is in the given list.
func containsTag(tags []string, tag string) bool {
	for _, t := range tags {
		if strings.EqualFold(t, tag) {
			return true
		}
	}
	return false
}

// isBlockElement returns true for block-level HTML elements.
func isBlockElement(tag string) bool {
	switch tag {
	case "p", "div", "h1", "h2", "h3", "h4", "h5", "h6",
		"li", "ul", "ol", "blockquote", "pre", "br",
		"section", "article", "main", "figure", "figcaption",
		"table", "tr", "td", "th", "thead", "tbody", "tfoot",
		"dl", "dt", "dd", "details", "summary":
		return true
	}
	return false
}
