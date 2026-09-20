package buildinfo

// Build metadata variables — injected at link time via
//
//	go build -ldflags '-X github.com/qomos-w/sporemind/pkg/buildinfo.BuildType=release
//	                  -X github.com/qomos-w/sporemind/pkg/buildinfo.BuildFlavor=internal
//	                  -X github.com/qomos-w/sporemind/pkg/buildinfo.Commit=abc123
//	                  -X github.com/qomos-w/sporemind/pkg/buildinfo.BuildTime=2024-01-01T00:00:00Z
//	                  -X github.com/qomos-w/sporemind/pkg/buildinfo.Dirty=true
//	                  -X github.com/qomos-w/sporemind/pkg/buildinfo.PublicVersion=1.2.0
//	                  -X github.com/qomos-w/sporemind/pkg/buildinfo.Channel=stable'
var (
	Version       = readVersion() // fallback: env → file → "dev"
	BuildType     = "dev"         // dev|beta|release
	BuildFlavor   = "default"     // default|internal|store|cn
	Commit        = ""            // git commit hash
	BuildTime     = ""            // ISO 8601 build timestamp
	Dirty         = ""            // "true" when working tree is dirty, else ""
	PublicVersion = ""            // external semver, e.g. 1.2.0 or 1.2.0-beta.3
	Channel       = ""            // release channel: stable | beta
)

// Info is the assembled build metadata snapshot.
type Info struct {
	Version       string `json:"version"`
	BuildType     string `json:"build_type"`
	BuildFlavor   string `json:"build_flavor"`
	Commit        string `json:"commit"`
	BuildTime     string `json:"build_time"`
	Dirty         bool   `json:"dirty"`
	PublicVersion string `json:"public_version"`
	Channel       string `json:"channel"`
}

// Get returns an Info snapshot assembled from the package-level variables.
func Get() Info {
	return Info{
		Version:       Version,
		BuildType:     BuildType,
		BuildFlavor:   BuildFlavor,
		Commit:        Commit,
		BuildTime:     BuildTime,
		Dirty:         Dirty == "true",
		PublicVersion: PublicVersion,
		Channel:       Channel,
	}
}

// IsRelease reports whether the build type is "release".
func IsRelease() bool { return BuildType == "release" }

// IsBeta reports whether the build type is "beta".
func IsBeta() bool { return BuildType == "beta" }

// IsDev reports whether the build type is "dev".
func IsDev() bool { return BuildType == "dev" }

// IsInternal reports whether the build flavor is "internal".
func IsInternal() bool { return BuildFlavor == "internal" }

// FeatureFlags holds the set of feature flags gated by build type and flavor.
type FeatureFlags struct {
	Flags map[string]bool `json:"flags"`
}

// GetFeatureFlags returns feature flags appropriate for the current build.
// The gating rules follow the design in 构建产物类型与功能开关设计 §4.2:
//   - release: release-grade flags (autoUpdate, crashReport enabled; labMode disabled)
//   - beta:    beta-grade flags (crashReport enabled; autoUpdate, multiInstance disabled)
//   - dev:     dev-grade flags (labMode, multiInstance enabled; autoUpdate, crashReport disabled)
func GetFeatureFlags() FeatureFlags {
	switch BuildType {
	case "release":
		return FeatureFlags{
			Flags: map[string]bool{
				"autoUpdate":    true,
				"crashReport":   true,
				"labMode":       false,
				"internalTag":   false,
				"multiInstance": false,
			},
		}
	case "beta":
		return FeatureFlags{
			Flags: map[string]bool{
				"autoUpdate":    false,
				"crashReport":   true,
				"labMode":       true,
				"internalTag":   true,
				"multiInstance": false,
			},
		}
	default: // dev
		return FeatureFlags{
			Flags: map[string]bool{
				"autoUpdate":    false,
				"crashReport":   false,
				"labMode":       true,
				"internalTag":   true,
				"multiInstance": true,
			},
		}
	}
}
