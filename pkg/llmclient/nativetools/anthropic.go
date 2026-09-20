package nativetools

import (
	"strings"

	"github.com/qomos-w/sporemind/pkg/domain"
)

type anthropicProvider struct{}

func (anthropicProvider) Name() string { return "anthropic" }

// Protocol: anthropic's web_search_20250305 is a first-class server tool of the
// anthropic Messages API, valid only over the "anthropic" wire protocol.
func (anthropicProvider) Protocol() string { return "anthropic" }

func (anthropicProvider) Match(model string) bool {
	return strings.Contains(strings.ToLower(model), "claude")
}

func (anthropicProvider) WebSearch() (domain.ToolSpec, bool) {
	return domain.ToolSpec{
		Name: "web_search",
		Type: "web_search_20250305",
	}, true
}
