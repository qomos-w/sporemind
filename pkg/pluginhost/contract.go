package pluginhost

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/qomos-w/sporemind/pkg/appbinding"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

const (
	NativeRuntime      = "native"
	BinaryCodecV1      = "binarycodec-v1"
	IsolationInProcess = "inprocess"
	IsolationProcess   = "process"
	IsolationSandbox   = "sandbox"
	// IsolationSubprocess is the shared-library-free transport: the plugin
	// runs as an independent OS process speaking the stdin/stdout framing
	// protocol (dev mode; hot reload + crash isolation + debuggability).
	// Mirrors sporemind-plugin-sdk manifestation (T2 schema value).
	IsolationSubprocess = "subprocess"
	TrustFirstParty     = "first_party"
	// TrustThirdParty marks plugins that are not host-signed. They must run
	// on the subprocess transport (crash isolation): the host enforces this
	// at transport selection, never in-process.
	TrustThirdParty = "third_party"

	// NativeBuildSigner is the signer identity applied to first-party native
	// artifacts built by the host's pluginhost actor. Native builds are
	// performed in-process by the trusted host, so the signer is a well-known
	// first-party identity rather than a per-plugin signing key. The SDK
	// mirrors this constant as sdk.DefaultSigner.
	NativeBuildSigner = "sporemind.first-party"

	// HostProtocolVersion is the subprocess frame protocol version the host
	// speaks — exactly v2. The codec is v2-only (no v1 fallback): every
	// frame carries a correlation callID in the header, enabling full-duplex
	// reverse-bridge calls at any time rather than only inside an invoke
	// dispatch window. Manifests must declare exactly this version so that
	// stale v1 artifacts fail at the manifest gate with a clear error
	// instead of dying at the frame layer.
	HostProtocolVersion = 2
	// ExpectedContractVersion is the ABI contract version every native plugin
	// must speak. The ABI carries the value as a string; it must equal the
	// decimal representation of this constant.
	ExpectedContractVersion = 1

	// depHashMinLen / depHashMaxLen bound the acceptable length of a
	// dependency content hash (e.g. a sha256 hex digest or multihash).
	depHashMinLen = 8
	depHashMaxLen = 128
)

// ReservedEventPrefix namespaces host→plugin event delivery: the pluginhost
// pushes bus events by invoking the plugin under this reserved call name, and
// the SDK (sporemind-plugin-sdk/events.go) dispatches it to
// RegisterEventListener handlers. Mirrors the SDK constant — the two modules
// cannot share a package, so the wire contract lives in both and CI-visible
// e2e tests pin them together.
const ReservedEventPrefix = "__event__:"

// EventRoute returns the namespaced host-side handler route for a listened
// host event kind (the event-delivery twin of PluginCallID).
func EventRoute(pluginID, kind string) string {
	return PluginCallID(pluginID, ReservedEventPrefix+kind)
}

// PluginCallID is the canonical route for a plugin callable, namespaced as
// "plugin.<pluginID>.<callable>". Callers (AppManager) send the plugin ID and
// the local callable name separately via PluginInvokeReq; the host constructs
// this route to look up the installed handler. ArtifactLoader registers
// handlers under the same route so both paths agree.
func PluginCallID(pluginID, callable string) string {
	return "plugin." + pluginID + "." + callable
}

// ValidateNativeManifest validates a native plugin manifest, its ABI, and the
// aggregate schema hash (computed by the registration path) against the host
// contract. schemaHash is the descriptor-level hash derived from
// manifest.Schemas; pass "" when the manifest declares no schemas.
func ValidateNativeManifest(manifest gen.AppManifest, abi gen.PluginAbi, schemaHash string) error {
	if manifest.ID == "" || manifest.Name == "" || manifest.Version == "" {
		return fmt.Errorf("plugin manifest metadata is incomplete")
	}
	if manifest.Runtime != NativeRuntime {
		return fmt.Errorf("plugin %q runtime must be %q", manifest.ID, NativeRuntime)
	}
	if manifest.ProtocolVersion != HostProtocolVersion {
		return fmt.Errorf("plugin %q protocol version %d is not supported: host speaks v%d only (v2-only frame codec, no v1 fallback; regenerate the app)", manifest.ID, manifest.ProtocolVersion, HostProtocolVersion)
	}
	if manifest.Namespace == "" {
		return fmt.Errorf("plugin namespace cannot be empty")
	}
	// The ABI must speak the host's contract version. The field is a string to
	// remain forward-compatible, so compare against the decimal representation.
	if abi.ContractVersion != strconv.Itoa(ExpectedContractVersion) {
		return fmt.Errorf("plugin %q ABI contract version %q does not match expected %d", manifest.ID, abi.ContractVersion, ExpectedContractVersion)
	}
	if abi.Name == "" || abi.Version <= 0 || abi.Encoding != BinaryCodecV1 || abi.InvokeSymbol == "" {
		return fmt.Errorf("plugin ABI metadata is invalid")
	}
	// Isolation: inprocess and subprocess are the two supported transports;
	// the host's transport selector (pkg/actor/pluginhost) maps them to the
	// FFI/purego loader and the process opener respectively. process/sandbox
	// remain reserved and unavailable.
	if abi.Isolation != IsolationInProcess && abi.Isolation != IsolationSubprocess {
		if abi.Isolation == IsolationProcess || abi.Isolation == IsolationSandbox {
			return fmt.Errorf("plugin isolation mode %q is unavailable; supported modes are %q and %q", abi.Isolation, IsolationInProcess, IsolationSubprocess)
		}
		return fmt.Errorf("plugin isolation mode must be %q or %q", IsolationInProcess, IsolationSubprocess)
	}
	// Trust class: first-party plugins may load in-process; third-party
	// plugins are accepted here but the transport selector forces them onto
	// the subprocess transport (crash isolation).
	if abi.TrustClass != TrustFirstParty && abi.TrustClass != TrustThirdParty {
		return fmt.Errorf("plugin trust class must be %q or %q", TrustFirstParty, TrustThirdParty)
	}
	if strings.TrimSpace(abi.Signer) == "" {
		return fmt.Errorf("plugin signer is required")
	}
	// Schema hash enforcement: when schemas are declared the descriptor must
	// carry a non-empty aggregate hash. The hash itself is computed by the
	// registration path (see computeSchemaHash); an empty value here signals a
	// bug where schemas were declared but hashing was skipped.
	if len(manifest.Schemas) > 0 && strings.TrimSpace(schemaHash) == "" {
		return fmt.Errorf("plugin %q declares schemas but schema hash is empty", manifest.ID)
	}
	allowed := map[string]struct{}{"app.invoke": {}, "filesystem.read": {}, "filesystem.write": {}, "shell.exec": {}, "browser.open": {}}
	seenCapabilities := make(map[string]struct{}, len(abi.Capabilities))
	for _, capability := range abi.Capabilities {
		if _, ok := allowed[capability]; !ok {
			return fmt.Errorf("plugin capability %q is not allowed", capability)
		}
		if _, ok := seenCapabilities[capability]; ok {
			return fmt.Errorf("duplicate plugin capability %q", capability)
		}
		seenCapabilities[capability] = struct{}{}
	}
	seen := make(map[string]struct{}, len(manifest.Callables))
	for _, callable := range manifest.Callables {
		if callable.ID == "" || callable.RequestSchema == "" || callable.ResponseSchema == "" {
			return fmt.Errorf("plugin callable descriptor is incomplete")
		}
		if _, ok := seen[callable.ID]; ok {
			return fmt.Errorf("duplicate plugin callable %q", callable.ID)
		}
		seen[callable.ID] = struct{}{}
	}
	// Listen kinds must resolve in the appbinding EventCatalog: an unknown
	// kind has no emitter and would silently never fire (dev_generate
	// already rejects it; this is the load-time backstop for hand-crafted
	// or drifted manifests).
	for _, kind := range manifest.Listens {
		if _, ok := appbinding.LookupHostEvent(kind); !ok {
			return fmt.Errorf("plugin listen kind %q is not a subscribable host event (allowed: %s)", kind, strings.Join(appbinding.SubscribableEventKinds(), ", "))
		}
	}
	// Dependency validation: each dependency must identify itself and its
	// version; an optional content hash, when present, must be well-formed.
	for _, dep := range manifest.Dependencies {
		if dep.ID == "" {
			return fmt.Errorf("plugin dependency ID is empty")
		}
		if dep.Version == "" {
			return fmt.Errorf("plugin dependency %q version is empty", dep.ID)
		}
		if dep.Hash != "" {
			if strings.TrimSpace(dep.Hash) == "" {
				return fmt.Errorf("plugin dependency %q hash must be non-whitespace", dep.ID)
			}
			if len(dep.Hash) < depHashMinLen || len(dep.Hash) > depHashMaxLen {
				return fmt.Errorf("plugin dependency %q hash length %d is out of range [%d,%d]", dep.ID, len(dep.Hash), depHashMinLen, depHashMaxLen)
			}
		}
	}
	return nil
}
