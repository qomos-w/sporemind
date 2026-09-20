package i18n

import (
	"context"
	"fmt"
	"strings"
)

// T returns the translation for key using the package default locale.
// It is a shorthand for applications that do not carry a per-request context.
func T(key string, args ...any) string {
	return TLocale(defaultLocale, key, args...)
}

// TCtx returns the translation for key using the locale stored in ctx.
func TCtx(ctx context.Context, key string, args ...any) string {
	return TLocale(FromContext(ctx), key, args...)
}

// TLocale returns the translation for key in the requested locale.
//
// When args are provided, the message is treated as a fmt-style format string
// and passed through fmt.Sprintf. For named placeholder substitution use the
// NV helper.
func TLocale(locale Locale, key string, args ...any) string {
	msg := Lookup(locale, key)
	if len(args) == 0 {
		return msg
	}
	return fmt.Sprintf(msg, args...)
}

// NV (name/value) is a helper for named placeholder substitution.
// Use it with T like: T("errors.fileNotFound", i18n.NV("path", path))
type NV struct {
	Name  string
	Value string
}

// Substitute replaces "{name}" placeholders in msg with the provided values.
func Substitute(msg string, pairs []NV) string {
	for _, p := range pairs {
		msg = strings.ReplaceAll(msg, "{"+p.Name+"}", p.Value)
	}
	return msg
}
