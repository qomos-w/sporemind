package storeclient

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/qomos-w/sporemind/pkg/buildinfo"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/pluginhost"
	sdk "github.com/qomos-w/sporemind-plugin-sdk"
)

// hostProtocolVersion pins the store compat check to the host's frame
// protocol (v2-only codec; ValidateNativeManifest enforces the same value).
var hostProtocolVersion = int32(pluginhost.HostProtocolVersion)

// semver is a parsed strict x.y.z version.
type semver struct {
	major, minor, patch int64
}

func parseSemver(s string) (semver, bool) {
	parts := strings.SplitN(strings.TrimSpace(s), ".", 3)
	if len(parts) != 3 {
		return semver{}, false
	}
	var v semver
	for i, p := range parts {
		if p == "" {
			return semver{}, false
		}
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil || n < 0 {
			return semver{}, false
		}
		switch i {
		case 0:
			v.major = n
		case 1:
			v.minor = n
		case 2:
			v.patch = n
		}
	}
	return v, true
}

func compareSemver(a, b semver) int {
	if a.major != b.major {
		return cmpInt64(a.major, b.major)
	}
	if a.minor != b.minor {
		return cmpInt64(a.minor, b.minor)
	}
	return cmpInt64(a.patch, b.patch)
}

func cmpInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// sdkCompatible reports whether an app built against SDK version appVer runs
// on this host: major must match exactly (breaking releases), minor.patch must
// not exceed the host SDK. Empty appVer (pre-SdkVersion apps) is treated as
// unknown-but-allowed.
func sdkCompatible(appVer string) (bool, string) {
	if strings.TrimSpace(appVer) == "" {
		return true, ""
	}
	app, ok := parseSemver(appVer)
	if !ok {
		return false, fmt.Sprintf("SdkVersion %q is not strict semver (x.y.z)", appVer)
	}
	host, ok := parseSemver(sdk.Version)
	if !ok {
		// Host SDK itself carries a non-semver constant: fail closed.
		return false, fmt.Sprintf("host SDK version %q is not strict semver", sdk.Version)
	}
	if app.major != host.major {
		return false, fmt.Sprintf("SdkVersion %s requires host SDK major %d (host has %s)", appVer, app.major, sdk.Version)
	}
	if compareSemver(app, host) > 0 {
		return false, fmt.Sprintf("SdkVersion %s is newer than host SDK %s", appVer, sdk.Version)
	}
	return true, ""
}

// hostCompatible reports whether the local host build satisfies a
// min_host_version. Unknown local version (dev builds without PublicVersion)
// or empty requirement both pass.
func hostCompatible(minHost string) (bool, string) {
	min := strings.TrimSpace(minHost)
	if min == "" {
		return true, ""
	}
	local := strings.TrimSpace(buildinfo.PublicVersion)
	if local == "" {
		return true, ""
	}
	mv, ok := parseSemver(min)
	if !ok {
		return false, fmt.Sprintf("min_host_version %q is not strict semver (x.y.z)", min)
	}
	lv, ok := parseSemver(local)
	if !ok {
		return true, ""
	}
	if compareSemver(lv, mv) < 0 {
		return false, fmt.Sprintf("requires host >= %s (local %s)", min, local)
	}
	return true, ""
}

// protocolCompatible enforces store/cloud protocol_version equality with the
// host frame protocol.
func protocolCompatible(protocolVersion int32) (bool, string) {
	if protocolVersion != hostProtocolVersion {
		return false, fmt.Sprintf("requires protocol_version %d (host speaks %d only)", protocolVersion, hostProtocolVersion)
	}
	return true, ""
}

// checkVersionCompat annotates a store version view with local compatibility.
func checkVersionCompat(v *gen.StoreVersionView) {
	if ok, reason := protocolCompatible(v.ProtocolVersion); !ok {
		v.Compatible = false
		v.IncompatibleReason = reason
		return
	}
	if ok, reason := sdkCompatible(v.SdkVersion); !ok {
		v.Compatible = false
		v.IncompatibleReason = reason
		return
	}
	if ok, reason := hostCompatible(v.MinHostVersion); !ok {
		v.Compatible = false
		v.IncompatibleReason = reason
		return
	}
	v.Compatible = true
	v.IncompatibleReason = ""
}

// checkCommunityCompat annotates a community entry view with local
// compatibility.
func checkCommunityCompat(e *gen.StoreCommunityView) {
	if ok, reason := protocolCompatible(e.ProtocolVersion); !ok {
		e.Compatible = false
		e.IncompatibleReason = reason
		return
	}
	if ok, reason := sdkCompatible(e.SdkVersion); !ok {
		e.Compatible = false
		e.IncompatibleReason = reason
		return
	}
	e.Compatible = true
	e.IncompatibleReason = ""
}
