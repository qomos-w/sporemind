package pluginhost

import (
	"encoding/json"
	"strings"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// validManifest returns a manifest that satisfies every contract check. Tests
// copy the value and mutate a single field to exercise a specific rejection.
func validManifest() gen.AppManifest {
	return gen.AppManifest{
		ID:              "example.native",
		Name:            "Example",
		Version:         "1.0.0",
		Runtime:         NativeRuntime,
		ProtocolVersion: HostProtocolVersion,
		Namespace:       "plugin.example.native",
		Callables: []gen.AppCallableDescriptor{
			{ID: "run", RequestSchema: "Req", ResponseSchema: "Resp"},
		},
	}
}

// validABI returns an ABI that satisfies every contract check.
func validABI() gen.PluginAbi {
	return gen.PluginAbi{
		Name:            "spore-plugin",
		Version:         1,
		Encoding:        BinaryCodecV1,
		InvokeSymbol:    "PluginInvoke",
		ContractVersion: "1",
		Isolation:       IsolationInProcess,
		TrustClass:      TrustFirstParty,
		Signer:          "sporemind.first-party",
	}
}

func TestValidateNativeManifest(t *testing.T) {
	if err := ValidateNativeManifest(validManifest(), validABI(), ""); err != nil {
		t.Fatal(err)
	}
}

func TestValidateNativeManifestRejectsWrongRuntime(t *testing.T) {
	m := validManifest()
	m.Runtime = "spore"
	if err := ValidateNativeManifest(m, validABI(), ""); err == nil {
		t.Fatal("expected runtime rejection")
	}
}

func TestValidateNativeManifestRejectsInvalidIsolationAndCapability(t *testing.T) {
	base := validManifest()

	abi := validABI()
	abi.Isolation = "unsafe"
	if err := ValidateNativeManifest(base, abi, ""); err == nil {
		t.Fatal("expected invalid isolation rejection")
	}

	abi = validABI()
	abi.Capabilities = []string{"secret.read"}
	if err := ValidateNativeManifest(base, abi, ""); err == nil {
		t.Fatal("expected undeclared capability rejection")
	}
}

func TestValidateNativeManifestRejectsUntrustedOrUnavailableIsolation(t *testing.T) {
	for name, mutate := range map[string]func(*gen.PluginAbi){
		"missing isolation": func(abi *gen.PluginAbi) { abi.Isolation = "" },
		"process":           func(abi *gen.PluginAbi) { abi.Isolation = IsolationProcess },
		"sandbox":           func(abi *gen.PluginAbi) { abi.Isolation = IsolationSandbox },
		"missing trust":     func(abi *gen.PluginAbi) { abi.TrustClass = "" },
		"unlisted trust":    func(abi *gen.PluginAbi) { abi.TrustClass = "untrusted_in_house" },
		"unknown isolation": func(abi *gen.PluginAbi) { abi.Isolation = "sidecar" },
		"missing signer":    func(abi *gen.PluginAbi) { abi.Signer = "" },
	} {
		t.Run(name, func(t *testing.T) {
			abi := validABI()
			mutate(&abi)
			if err := ValidateNativeManifest(validManifest(), abi, ""); err == nil {
				t.Fatal("expected native trust/isolation rejection")
			}
		})
	}
}

func TestValidateNativeManifestAcceptsSubprocessAndThirdParty(t *testing.T) {
	for name, mutate := range map[string]func(*gen.PluginAbi){
		"subprocess isolation": func(abi *gen.PluginAbi) { abi.Isolation = IsolationSubprocess },
		"third party trust":    func(abi *gen.PluginAbi) { abi.TrustClass = TrustThirdParty },
		"subprocess third party": func(abi *gen.PluginAbi) {
			abi.Isolation = IsolationSubprocess
			abi.TrustClass = TrustThirdParty
			abi.Signer = "acme.signing.key"
		},
	} {
		t.Run(name, func(t *testing.T) {
			abi := validABI()
			mutate(&abi)
			if err := ValidateNativeManifest(validManifest(), abi, ""); err != nil {
				t.Fatalf("expected acceptance: %v", err)
			}
		})
	}
}

func TestInvokeFrame(t *testing.T) {
	payload := []byte("request")
	decoded, err := DecodeInvokeFrame(EncodeInvokeFrame(payload))
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != string(payload) {
		t.Fatalf("decoded %q", decoded)
	}
	if _, err := DecodeInvokeFrame([]byte{0, 0, 0, 2, 1}); err == nil {
		t.Fatal("expected length mismatch")
	}
}

func TestValidateProtocolVersion(t *testing.T) {
	// Zero protocol version is rejected (must be explicitly set).
	m := validManifest()
	m.ProtocolVersion = 0
	if err := ValidateNativeManifest(m, validABI(), ""); err == nil {
		t.Fatal("expected protocol version required rejection")
	}

	// Versions above the host's are rejected.
	m = validManifest()
	m.ProtocolVersion = HostProtocolVersion + 1
	if err := ValidateNativeManifest(m, validABI(), ""); err == nil {
		t.Fatalf("expected rejection for protocol version %d", m.ProtocolVersion)
	}

	// v1 is rejected at the gate: the frame codec is v2-only, so a stale v1
	// artifact must fail here with a clear error instead of dying at the
	// frame layer with an opaque decode failure.
	m = validManifest()
	m.ProtocolVersion = 1
	err := ValidateNativeManifest(m, validABI(), "")
	if err == nil {
		t.Fatal("expected rejection for legacy protocol version 1")
	}
	if !strings.Contains(err.Error(), "v2-only frame codec") {
		t.Fatalf("error must point at the v2-only codec and the fix (regenerate), got: %v", err)
	}
}

func TestValidateProtocolVersionAccepts(t *testing.T) {
	m := validManifest()
	m.ProtocolVersion = HostProtocolVersion
	if err := ValidateNativeManifest(m, validABI(), ""); err != nil {
		t.Fatalf("expected protocol version %d to be accepted: %v", m.ProtocolVersion, err)
	}
}

func TestValidateABIContractVersion(t *testing.T) {
	// A mismatched contract version is rejected.
	abi := validABI()
	abi.ContractVersion = "2"
	if err := ValidateNativeManifest(validManifest(), abi, ""); err == nil {
		t.Fatal("expected contract version mismatch rejection")
	}

	// A missing contract version is also rejected (it cannot equal the expected).
	abi = validABI()
	abi.ContractVersion = ""
	if err := ValidateNativeManifest(validManifest(), abi, ""); err == nil {
		t.Fatal("expected missing contract version rejection")
	}
}

func TestValidateSchemaHashRequired(t *testing.T) {
	m := validManifest()
	m.Schemas = []gen.AppSchemaRef{{Name: "Req", Hash: "deadbeefdeadbeef"}}

	// Schemas declared but an empty descriptor schema hash is rejected.
	if err := ValidateNativeManifest(m, validABI(), ""); err == nil {
		t.Fatal("expected schema hash required rejection")
	}

	// Schemas declared with a populated descriptor schema hash are accepted.
	if err := ValidateNativeManifest(m, validABI(), "abc123def456"); err != nil {
		t.Fatalf("expected schemas with schema hash to be accepted: %v", err)
	}

	// A manifest without schemas does not require a schema hash.
	if err := ValidateNativeManifest(validManifest(), validABI(), ""); err != nil {
		t.Fatalf("expected schema-less manifest to be accepted: %v", err)
	}
}

func TestValidateDependencies(t *testing.T) {
	// Missing dependency ID.
	m := validManifest()
	m.Dependencies = []gen.AppDependency{{ID: "", Version: "1.0.0"}}
	if err := ValidateNativeManifest(m, validABI(), ""); err == nil {
		t.Fatal("expected missing dependency ID rejection")
	}

	// Missing dependency version.
	m = validManifest()
	m.Dependencies = []gen.AppDependency{{ID: "dep.one", Version: ""}}
	if err := ValidateNativeManifest(m, validABI(), ""); err == nil {
		t.Fatal("expected missing dependency version rejection")
	}

	// Malformed hash (too short).
	m = validManifest()
	m.Dependencies = []gen.AppDependency{{ID: "dep.one", Version: "1.0.0", Hash: "short"}}
	if err := ValidateNativeManifest(m, validABI(), ""); err == nil {
		t.Fatal("expected malformed dependency hash rejection")
	}

	// Hash that is entirely whitespace (passes the length band but is invalid).
	m = validManifest()
	m.Dependencies = []gen.AppDependency{{ID: "dep.one", Version: "1.0.0", Hash: "        "}}
	if err := ValidateNativeManifest(m, validABI(), ""); err == nil {
		t.Fatal("expected whitespace dependency hash rejection")
	}

	// A well-formed dependency with a valid hash is accepted.
	m = validManifest()
	m.Dependencies = []gen.AppDependency{{ID: "dep.one", Version: "1.0.0", Hash: "0123456789abcdef0123456789abcdef"}}
	if err := ValidateNativeManifest(m, validABI(), ""); err != nil {
		t.Fatalf("expected valid dependency to be accepted: %v", err)
	}
}

// TestSDKManifestAcceptedByValidator proves that a manifest produced by the
// SDK (via Manifest.Normalize / WriteManifest, which uses PascalCase JSON keys
// and fills Runtime/ProtocolVersion/Namespace defaults) is directly accepted
// by the host's ValidateNativeManifest when paired with a first-party ABI.
func TestSDKManifestAcceptedByValidator(t *testing.T) {
	// Simulate the JSON the SDK's WriteManifest produces after Normalize().
	sdkJSON := `{"Id":"app.test","Name":"Test","Version":"1.0.0","Runtime":"native","ProtocolVersion":2,"Namespace":"app.test","Callables":[{"Id":"ping","RequestSchema":"PingRequest","ResponseSchema":"PingResponse"}],"Entrypoints":[{"Kind":"command","Id":"test.ping","Title":"Ping","Route":"ping"}]}`
	var manifest gen.AppManifest
	if err := json.Unmarshal([]byte(sdkJSON), &manifest); err != nil {
		t.Fatalf("unmarshal SDK manifest JSON: %v", err)
	}
	abi := gen.PluginAbi{
		Name:            "spore-plugin",
		Version:         1,
		Encoding:        BinaryCodecV1,
		InvokeSymbol:    "PluginInvoke",
		ContractVersion: "1",
		Isolation:       IsolationInProcess,
		TrustClass:      TrustFirstParty,
		Signer:          NativeBuildSigner,
	}
	schemaHash := ComputeSchemaHash(manifest.Schemas)
	if err := ValidateNativeManifest(manifest, abi, schemaHash); err != nil {
		t.Fatalf("SDK-produced manifest rejected by host validator: %v", err)
	}
}

// TestNativeBuildAbiPassesValidator proves that the ABI constructed by
// handleNativeBuild (which uses NativeBuildSigner for first-party builds)
// satisfies ValidateNativeManifest.
func TestNativeBuildAbiPassesValidator(t *testing.T) {
	abi := gen.PluginAbi{
		Name:            "spore-plugin",
		Version:         1,
		Encoding:        BinaryCodecV1,
		InvokeSymbol:    "PluginInvoke",
		ContractVersion: "1",
		Isolation:       IsolationInProcess,
		TrustClass:      TrustFirstParty,
		Signer:          NativeBuildSigner,
	}
	if err := ValidateNativeManifest(validManifest(), abi, ""); err != nil {
		t.Fatalf("native-build ABI rejected by validator: %v", err)
	}
}
