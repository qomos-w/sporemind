package nativetools

import (
	"strings"

	"github.com/qomos-w/sporemind/pkg/domain"
)

type bigmodelProvider struct{}

func (bigmodelProvider) Name() string { return "bigmodel" }

// Protocol: GLM's web_search is a native extension of the bigmodel API. In this
// runtime GLM is reached via the openai/endpoint protocol, whose Chat
// Completions schema only accepts "function"/"plugin" tools — so declaring
// "bigmodel" here makes Reconcile drop web_search for glm-over-openai (the
// cause of the "unknown tool type: web_search" 400 on a model switch).
func (bigmodelProvider) Protocol() string { return "bigmodel" }

func (bigmodelProvider) Match(model string) bool {
	return strings.HasPrefix(strings.ToLower(model), "glm-")
}

func (bigmodelProvider) WebSearch() (domain.ToolSpec, bool) {
	return domain.ToolSpec{
		Name:         "web_search",
		Type:         "web_search",
		NativeConfig: map[string]any{"web_search": map[string]any{"enable": true}},
	}, true
}
