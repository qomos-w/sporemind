package shell

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeUTF8(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"ascii", "hello world\n"},
		{"valid multibyte", "你好，世界\n"},
		{"gbk bytes", string([]byte{0xC4, 0xE3, 0xBA, 0xC3})},
		{"split multibyte", "prefix" + string([]byte{0xE4, 0xBD})},
		{"binary", string([]byte{0x00, 0xff, 0xfe, 0x41})},
	}
	for _, tc := range cases {
		got := SanitizeUTF8(tc.in)
		if !utf8.ValidString(got) {
			t.Errorf("%s: SanitizeUTF8 produced invalid UTF-8 %q", tc.name, got)
		}
	}
	if got := SanitizeUTF8("hello"); got != "hello" {
		t.Errorf("valid string mutated: %q", got)
	}
	if got := SanitizeUTF8("你好"); got != "你好" {
		t.Errorf("valid multibyte string mutated: %q", got)
	}
	// ASCII neighbours must survive so the caller still sees the command
	// output around the replaced bytes.
	if got := SanitizeUTF8("a\xff" + "b"); !strings.HasPrefix(got, "a") || !strings.HasSuffix(got, "b") {
		t.Errorf("ascii neighbours lost: %q", got)
	}
}
