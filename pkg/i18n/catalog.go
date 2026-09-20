package i18n

// Register adds translations for a locale. It is intended to be called from
// init functions in catalog_*.go files generated or maintained alongside the
// codebase.
func Register(locale Locale, messages map[string]string) {
	if catalog[locale] == nil {
		catalog[locale] = make(map[string]string)
	}
	for k, v := range messages {
		catalog[locale][k] = v
	}
}

// Lookup returns the translation for key in the requested locale. If the key
// is missing, it falls back to the default locale and finally returns the key
// itself so that missing translations remain visible.
func Lookup(locale Locale, key string) string {
	if msg, ok := catalog[locale][key]; ok {
		return msg
	}
	if locale != defaultLocale {
		if msg, ok := catalog[defaultLocale][key]; ok {
			return msg
		}
	}
	return key
}
