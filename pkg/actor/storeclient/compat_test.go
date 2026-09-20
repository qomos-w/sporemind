package storeclient

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/buildinfo"
	sdk "github.com/qomos-w/sporemind-plugin-sdk"
)

func TestParseSemver(t *testing.T) {
	for _, tc := range []struct {
		in   string
		ok   bool
		want semver
	}{
		{"0.3.1", true, semver{0, 3, 1}},
		{"1.20.300", true, semver{1, 20, 300}},
		{"1.2", false, semver{}},
		{"1.2.x", false, semver{}},
		{"1.2.-3", false, semver{}},
		{"", false, semver{}},
	} {
		got, ok := parseSemver(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("parseSemver(%q) = %v,%v want %v,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestSdkCompatible(t *testing.T) {
	host := sdk.Version
	// Same version, older patch/minor: compatible.
	for _, v := range []string{host, "0.0.1", "0.0.0"} {
		if ok, reason := sdkCompatible(v); !ok {
			t.Errorf("sdkCompatible(%q) = false (%s), want true", v, reason)
		}
	}
	// Newer minor/patch than host: incompatible.
	if ok, _ := sdkCompatible(nextPatch(host)); ok {
		t.Errorf("sdkCompatible(newer patch) = true, want false")
	}
	if ok, _ := sdkCompatible(nextMinor(host)); ok {
		t.Errorf("sdkCompatible(newer minor) = true, want false")
	}
	// Different major: incompatible.
	if ok, _ := sdkCompatible("9.0.0"); ok {
		t.Errorf("sdkCompatible(9.0.0) = true, want false")
	}
	// Unknown (legacy app without SdkVersion): allowed.
	if ok, reason := sdkCompatible(""); !ok {
		t.Errorf("sdkCompatible(\"\") = false (%s), want true", reason)
	}
	// Malformed: rejected.
	if ok, _ := sdkCompatible("1.2"); ok {
		t.Errorf("sdkCompatible(\"1.2\") = true, want false")
	}
}

func nextPatch(v string) string {
	s, _ := parseSemver(v)
	return itoa3(s.major, s.minor, s.patch+1)
}

func nextMinor(v string) string {
	s, _ := parseSemver(v)
	return itoa3(s.major, s.minor+1, 0)
}

func itoa3(a, b, c int64) string {
	return string(rune('0'+a)) + "." + string(rune('0'+b)) + "." + string(rune('0'+c))
}

func TestHostCompatible(t *testing.T) {
	// Empty requirement always passes.
	if ok, _ := hostCompatible(""); !ok {
		t.Error("hostCompatible(\"\") = false, want true")
	}
	// Dev builds (no injected PublicVersion) skip the comparison.
	if buildinfo.PublicVersion == "" {
		if ok, _ := hostCompatible("1.2"); !ok {
			t.Error("unknown local version must pass")
		}
		return
	}
	// Unparseable requirement fails closed.
	if ok, _ := hostCompatible("1.2"); ok {
		t.Error("hostCompatible(\"1.2\") = true, want false")
	}
}

func TestProtocolCompatible(t *testing.T) {
	if ok, _ := protocolCompatible(2); !ok {
		t.Error("protocolCompatible(2) = false, want true")
	}
	if ok, _ := protocolCompatible(1); ok {
		t.Error("protocolCompatible(1) = true, want false")
	}
}
