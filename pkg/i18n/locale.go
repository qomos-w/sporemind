package i18n

import (
	"context"
	"strings"
)

// Locale identifies a language/region pair supported by the application.
type Locale string

const (
	ZhCN Locale = "zh-CN"
	EnUS Locale = "en-US"
)

// SetDefault changes the fallback locale used by T and FromContext.
func SetDefault(locale Locale) {
	defaultLocale = locale
}

// Default returns the current fallback locale.
func Default() Locale {
	return defaultLocale
}

const localeCtxKey = ctxKey("sporemind:locale")

type ctxKey string

// Supported returns all locales available to the application.
func Supported() []Locale {
	return []Locale{ZhCN, EnUS}
}

// ParseLocale normalizes a raw locale string and falls back to defaultLocale
// when the value is unknown.
func ParseLocale(raw string) Locale {
	switch strings.ToLower(strings.ReplaceAll(raw, "_", "-")) {
	case "zh", "zh-cn", "zh-hans", "zh-sg":
		return ZhCN
	case "en", "en-us", "en-gb", "en-au", "en-ca":
		return EnUS
	default:
		return defaultLocale
	}
}

// WithLocale returns a context that carries the given locale.
func WithLocale(ctx context.Context, locale Locale) context.Context {
	return context.WithValue(ctx, localeCtxKey, locale)
}

// FromContext extracts the locale from the context, or returns defaultLocale.
func FromContext(ctx context.Context) Locale {
	if ctx == nil {
		return defaultLocale
	}
	if v, ok := ctx.Value(localeCtxKey).(Locale); ok && v != "" {
		return v
	}
	return defaultLocale
}
