package nativetools

import (
	"strings"

	"github.com/qomos-w/sporemind/pkg/domain"
)

type openaiProvider struct{}

func (openaiProvider) Name() string { return "openai" }

// Protocol: native web_search is disabled for OpenAI-family models (see
// WebSearch), so no protocol binding is relevant here.
func (openaiProvider) Protocol() string { return "" }

func (openaiProvider) Match(model string) bool {
	m := strings.ToLower(model)
	return strings.HasPrefix(m, "gpt-") || strings.HasPrefix(m, "o1") || strings.HasPrefix(m, "o3")
}

func (openaiProvider) WebSearch() (domain.ToolSpec, bool) {
	// OpenAI Chat Completions (the protocol we use for /chat/completions)
	// only accepts tool types "function" and "plugin".  Emitting a native
	// "web_search" tool here causes the upstream to return:
	//   "unknown tool type: web_search, currently only function and plugin
	//    are supported"
	// when the model is switched to a GPT/O1/O3 family model.  Disable the
	// native web_search declaration for this family; rely on provider-native
	// adapters (anthropic, bigmodel) for models that actually support it.
	return domain.ToolSpec{}, false
}
