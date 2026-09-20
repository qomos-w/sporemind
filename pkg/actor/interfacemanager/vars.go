package interfacemanager

// This file consolidates package-level variable declarations for the
// interfacemanager package.

// --- Valid omnibox values ---

// validViewModes mirrors the content-mode options exposed by the omnibox.
var validViewModes = map[string]struct{}{
	"conversation": {},
	"topology":     {},
	"files":        {},
	"problems":     {},
	"notes":        {},
	"ssh":          {},
	"multiconsole": {},
}

// validSettingCategories mirrors the settings categories exposed by the omnibox.
var validSettingCategories = map[string]struct{}{
	"general":   {},
	"account":   {},
	"voice":     {},
	"model":     {},
	"agent":     {},
	"skills":    {},
	"prompts":   {},
	"frp":       {},
	"mcp":       {},
	"plugins":   {},
	"commands":  {},
	"index":     {},
	"developer": {},
}

// validInteractionKinds are the allowed interaction kinds for the "interact" action.
var validInteractionKinds = map[string]struct{}{
	"click":            {},
	"focus":            {},
	"input":            {},
	"scroll_into_view": {},
	"guide_progress":   {},
}
