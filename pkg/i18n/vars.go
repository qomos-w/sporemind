package i18n

// Lookup tables

// catalog holds translations for every supported locale.
// Keys are dot-separated lowercase identifiers such as "common.confirm".
var catalog = map[Locale]map[string]string{
	ZhCN: {},
	EnUS: {},
}

// Mutable singletons
// TODO(actor-ownership): migrate to actor-owned state
var defaultLocale = ZhCN
