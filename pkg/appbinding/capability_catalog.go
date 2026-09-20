package appbinding

import "fmt"

// Risk levels for host capabilities. Keep them as stable, UI-friendly strings
// so the frontend can map them directly to color/severity tokens.
const (
	RiskLow    = "low"
	RiskMedium = "medium"
	RiskHigh   = "high"
)

// CapabilityLocale holds the human-readable title and description for a single
// locale.
type CapabilityLocale struct {
	Title       string
	Description string
}

// Capability holds the canonical metadata for one host capability. The catalog
// is the single source of truth for the consent dialog and the settings panel;
// unknown capabilities are always rejected rather than silently defaulted.
//
// Title and Description are the default zh-CN strings; Locales contains the
// translations currently shipped (zh-CN and en-US).
type Capability struct {
	ID                 string
	Title              string
	Description        string
	RiskLevel          string
	I18nTitleKey       string
	I18nDescriptionKey string
	Locales            map[string]CapabilityLocale
}

// LocalizedTitle returns the title for the requested locale, falling back to
// the default zh-CN title when the locale is unknown.
func (c Capability) LocalizedTitle(locale string) string {
	if l, ok := c.Locales[locale]; ok && l.Title != "" {
		return l.Title
	}
	return c.Title
}

// LocalizedDescription returns the description for the requested locale,
// falling back to the default zh-CN description when the locale is unknown.
func (c Capability) LocalizedDescription(locale string) string {
	if l, ok := c.Locales[locale]; ok && l.Description != "" {
		return l.Description
	}
	return c.Description
}

// CapabilityCatalog is the authoritative map of known host capabilities. It
// starts empty and is populated at package init by RegisterCapability calls
// (see capabilities_registered.go and cap_*.go); it is intentionally exported
// as a read-only lookup table. Code that needs a single capability should use
// GetCapability so that unknown IDs are surfaced as errors instead of
// returning a zero value.
var CapabilityCatalog = map[string]Capability{}

// GetCapability returns the canonical metadata for a known host capability.
// Unknown IDs return an error so callers never silently fall back to a zero
// value or a default descriptor.
func GetCapability(id string) (Capability, error) {
	c, ok := CapabilityCatalog[id]
	if !ok {
		return Capability{}, fmt.Errorf("appbinding: unknown host capability %q", id)
	}
	return c, nil
}
