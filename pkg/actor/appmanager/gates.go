package appmanager

import (
	"fmt"
	"sort"
	"strings"

	"github.com/qomos-w/sporemind/pkg/appdef"
	"github.com/qomos-w/sporemind/pkg/codegen"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// GateName identifies one of the five registration gates.
type GateName string

const (
	GateBuild    GateName = "build"
	GateStubFill GateName = "stub_filling"
	GateManifest GateName = "manifest_consistency"
	GateCoverage GateName = "coverage"
	GateVendor   GateName = "vendor_freshness"
	GateFrontend GateName = "frontend_transport"
	GateBundle   GateName = "bundle_drift"
)

// GateError is a structured gate failure with diagnosis and fix instructions.
type GateError struct {
	Gate   GateName `json:"gate"`
	Detail string   `json:"detail"`
	Fix    string   `json:"fix"`
}

func (e *GateError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("gate %s failed: %s (fix: %s)", e.Gate, e.Detail, e.Fix)
}

// GateResult is the outcome of a single gate.
type GateResult struct {
	Gate   GateName   `json:"gate"`
	Passed bool       `json:"passed"`
	Error  *GateError `json:"error,omitempty"`
}

// GateReport is the combined outcome of all gates.
type GateReport struct {
	Passed  bool         `json:"passed"`
	Results []GateResult `json:"results"`
}

// ToResponse converts the internal report to the generated response type.
func (r GateReport) ToResponse() gen.AppManagerDevGateResp {
	resp := gen.AppManagerDevGateResp{
		Passed:  r.Passed,
		Results: make([]gen.AppManagerGateResult, 0, len(r.Results)),
	}
	for _, res := range r.Results {
		entry := gen.AppManagerGateResult{Gate: string(res.Gate), Passed: res.Passed}
		if res.Error != nil {
			entry.Error = &gen.AppManagerGateError{
				Gate:   string(res.Error.Gate),
				Detail: res.Error.Detail,
				Fix:    res.Error.Fix,
			}
		}
		resp.Results = append(resp.Results, entry)
	}
	return resp
}

// GateDeps provides everything needed to run the five gates. Callers supply
// the inputs they have already resolved; the gate logic itself is pure and
// side-effect free, which makes unit testing straightforward.
type GateDeps struct {
	// SourceFiles maps relative source paths to file content for the
	// stub-filling gate. Only .go files need to be included.
	SourceFiles map[string]string

	// AppDefContent is the raw .appdef source text.
	AppDefContent string

	// AppDefHash is the SHA-256 hex digest of AppDefContent.
	AppDefHash string

	// Manifest is the on-disk app.manifest.json parsed into a value type.
	Manifest gen.AppManifest

	// GeneratedManifest is the persisted manifest from the last dev_generate
	// run. It may be nil when no generation has happened yet.
	GeneratedManifest *codegen.GeneratedManifest

	// BuildResult is the outcome of pluginhost.native_build. It can be
	// injected by tests to avoid invoking the real compiler.
	BuildResult gen.NativeBuildResult

	// RegisteredCallables is the set of namespaced callable IDs that are
	// currently installed in the dispatch registry (e.g.
	// "plugin.myapp.greet"). For register_project these come from the
	// artifact_load response; for dev_gate they come from a temporary load.
	RegisteredCallables []string

	// LoadDiagnostic explains why the coverage gate's registry measurement
	// does not reflect the freshly built artifact: the temporary load
	// failed, or an already-running instance holds an older (stale)
	// artifact. The coverage gate reports this root cause instead of a
	// misleading declared-vs-registered diff.
	LoadDiagnostic string

	// VendorStamp is the content digest recorded in the app's vendor-sdk
	// stamp file (codegen.ReadVendorStamp); empty when the app is not
	// vendored or the tree predates stamping.
	VendorStamp string

	// HostSDKDigest is the current digest of the host plugin SDK
	// (codegen.SDKDigest); empty when the host SDK could not be digested.
	HostSDKDigest string

	// FrontendFiles maps app-root-relative HTML file paths to their content
	// for the frontend_transport gate. Empty map skips the gate (no HTML to
	// lint); nil is treated as empty.
	FrontendFiles map[string]string

	// BundleSnapshots is what dev_generate resolved and persisted at
	// generation time (GeneratedManifest.BundleSnapshots). Nil when the app
	// declares no bundle-level permissions or was never generated.
	BundleSnapshots []codegen.BundleSnapshot

	// HasGeneratedManifest reports whether a dev_generate record exists for
	// this app dir at all. Registration of a never-generated tree is legal,
	// so the drift gate only fires when a record exists.
	HasGeneratedManifest bool

	// ExpectedBundleSnapshots is what the CURRENT registry resolves the same
	// appdef's bundle-level permissions to. Nil when nothing resolves.
	ExpectedBundleSnapshots []codegen.BundleSnapshot

	// BundleDriftDiagnostic carries the recomputation failure (unparseable
	// appdef, inconsistent dependency manifest) so the gate can report the
	// root cause instead of a misleading diff.
	BundleDriftDiagnostic string
}

// RunAllGates executes the five registration gates and returns a structured
// report. Any failure marks Passed=false but all gates are still evaluated so
// the caller can report every problem at once.
func RunAllGates(deps GateDeps) GateReport {
	report := GateReport{Passed: true, Results: make([]GateResult, 0, len(gateOrder))}
	for _, name := range gateOrder {
		var res GateResult
		switch name {
		case GateBuild:
			res = checkBuildGate(deps.BuildResult)
		case GateStubFill:
			res = checkStubFillingGate(deps.SourceFiles)
		case GateManifest:
			res = checkManifestGate(deps.AppDefContent, deps.AppDefHash, deps.Manifest, deps.GeneratedManifest)
		case GateCoverage:
			res = checkCoverageGate(deps.Manifest, deps.RegisteredCallables, deps.LoadDiagnostic)
		case GateVendor:
			res = checkVendorGate(deps.VendorStamp, deps.HostSDKDigest)
		case GateFrontend:
			res = checkFrontendGate(deps.FrontendFiles)
		case GateBundle:
			res = checkBundleDriftGate(deps.HasGeneratedManifest, deps.BundleSnapshots, deps.ExpectedBundleSnapshots, deps.BundleDriftDiagnostic)
		}
		report.Results = append(report.Results, res)
		if !res.Passed {
			report.Passed = false
		}
	}
	if report.Passed {
		report.Passed = true
	}
	return report
}

func checkBuildGate(result gen.NativeBuildResult) GateResult {
	if !result.Success {
		msg := result.Diagnostic
		if strings.TrimSpace(msg) == "" {
			msg = "c-shared build failed with no diagnostic"
		}
		return failGate(GateBuild, msg,
			"Fix the compilation errors reported by the Go compiler and rerun the gate.")
	}
	return passGate(GateBuild)
}

// checkVendorGate detects a vendored SDK copy that drifted from the host SDK
// (host upgraded after the app last vendored). A stale copy still compiles,
// so without this check apps silently ship old SDK behavior — including
// protocol fixes like bootstrap-snippet injection. Undecidable cases (no
// stamp: not vendored or pre-stamp legacy tree; no host digest: workspace
// unavailable) pass: only a confirmed mismatch fails.
func checkVendorGate(vendorStamp, hostDigest string) GateResult {
	if vendorStamp == "" || hostDigest == "" || vendorStamp == hostDigest {
		return passGate(GateVendor)
	}
	return failGate(GateVendor,
		fmt.Sprintf("vendor-sdk stamp %s != host SDK digest %s — the vendored SDK copy is stale", vendorStamp, hostDigest),
		"Run appmanager.sdk_vendor to refresh vendor-sdk from the current host SDK (dev_generate refreshes it automatically).")
}

// legacyFrontendTokens are the dead pre-T5 bridge surfaces. A handwritten
// index.html calling them gets silent 404s / undefined references — the
// admin-tools migration shipped one for hours before anyone noticed. The
// frontend_transport gate fails the registration instead.
var legacyFrontendTokens = []string{
	"/api/pluginhost.invoke",
	"window.sporemind",
	"sporemind.invoke",
}

// checkFrontendGate scans the app's HTML entry documents for dead transport
// tokens. No HTML files means nothing to lint (pass). The gateway path
// (window.__sporemindAppBase + /invoke/{id}) is the only supported transport.
func checkFrontendGate(frontendFiles map[string]string) GateResult {
	if len(frontendFiles) == 0 {
		return passGate(GateFrontend)
	}
	var hits []string
	for path, content := range frontendFiles {
		lower := strings.ToLower(content)
		for _, token := range legacyFrontendTokens {
			if strings.Contains(lower, strings.ToLower(token)) {
				hits = append(hits, fmt.Sprintf("%s uses %q", path, token))
			}
		}
	}
	sort.Strings(hits)
	if len(hits) > 0 {
		return failGate(GateFrontend,
			strings.Join(hits, "; "),
			"The old bridge is dead. Use the direct data path: await window.__sporemindAppBaseReady, then fetch((window.__sporemindAppBase||'') + '/invoke/'+callableId) with a JSON body — see dev_guide errors.")
	}
	return passGate(GateFrontend)
}

// checkBundleDriftGate compares what dev_generate snapshotted at generation
// time against what the CURRENT registry resolves for the same appdef's
// bundle-level permissions. A dependency that re-registered with different
// callables or schemas after the app was generated leaves the generated typed
// callers (and their wire structs) silently stale — the drift gate fails
// registration until dev_generate is rerun. A never-generated tree (no
// dev_generate record) is a legal registration path and passes.
func checkBundleDriftGate(hasGenerated bool, persisted, expected []codegen.BundleSnapshot, diagnostic string) GateResult {
	if diagnostic != "" {
		return failGate(GateBundle, diagnostic,
			"Fix the reported inconsistency (dependency manifest or appdef) and rerun dev_generate.")
	}
	if len(expected) == 0 && len(persisted) == 0 {
		return passGate(GateBundle)
	}
	if !hasGenerated {
		// Registered without ever running dev_generate: nothing to be stale
		// against — the raw-JSON hostproto callers cover plugin.* permissions.
		return passGate(GateBundle)
	}
	if len(expected) > 0 && len(persisted) == 0 {
		return failGate(GateBundle,
			"bundle-level permissions resolve against the current registry but the generation snapshots are empty (generated before the dependencies were registered)",
			"Run appmanager.dev_generate against the current dependencies, then re-register.")
	}
	var problems []string
	exp := map[string]codegen.BundleSnapshot{}
	for _, s := range expected {
		exp[s.DepID] = s
	}
	per := map[string]codegen.BundleSnapshot{}
	for _, s := range persisted {
		per[s.DepID] = s
	}
	for _, id := range sortedGateKeys(exp) {
		got, ok := per[id]
		if !ok {
			problems = append(problems, fmt.Sprintf("dependency %q: snapshot missing (dependency registered after generation)", id))
			continue
		}
		problems = append(problems, diffBundleSnapshot(id, got, exp[id])...)
	}
	for _, id := range sortedGateKeys(per) {
		if _, ok := exp[id]; !ok {
			problems = append(problems, fmt.Sprintf("dependency %q: no longer resolvable (unregistered, undeclared, or its bundle vanished)", id))
		}
	}
	if len(problems) > 0 {
		return failGate(GateBundle, strings.Join(problems, "; "),
			"The dependencies changed shape since dev_generate. Rerun appmanager.dev_generate to refresh bundle_calls.gen.go and the snapshots, then re-register.")
	}
	return passGate(GateBundle)
}

// diffBundleSnapshot lists every drift between one dependency's persisted and
// expected snapshot.
func diffBundleSnapshot(depID string, persisted, expected codegen.BundleSnapshot) []string {
	var problems []string
	if persisted.DepVersion != expected.DepVersion {
		problems = append(problems, fmt.Sprintf("%s: version %s -> %s", depID, persisted.DepVersion, expected.DepVersion))
	}
	if persisted.DepPackageHash != expected.DepPackageHash {
		problems = append(problems, fmt.Sprintf("%s: package hash changed", depID))
	}
	if diffs := diffStringSets(persisted.Callables, expected.Callables); len(diffs) > 0 {
		problems = append(problems, fmt.Sprintf("%s: callable set changed (%s)", depID, strings.Join(diffs, ", ")))
	}
	if diffs := diffStringMaps(persisted.SchemaHashes, expected.SchemaHashes); len(diffs) > 0 {
		problems = append(problems, fmt.Sprintf("%s: schema hashes changed (%s)", depID, strings.Join(diffs, ", ")))
	}
	return problems
}

// diffStringSets returns "added X"/"removed X" entries for two sorted sets.
func diffStringSets(persisted, expected []string) []string {
	pSet := gateKeySet(persisted)
	eSet := gateKeySet(expected)
	var diffs []string
	for _, v := range expected {
		if !pSet[v] {
			diffs = append(diffs, "added "+v)
		}
	}
	for _, v := range persisted {
		if !eSet[v] {
			diffs = append(diffs, "removed "+v)
		}
	}
	sort.Strings(diffs)
	return diffs
}

// diffStringMaps returns "name: old -> new" entries for changed or
// added/removed schema hashes.
func diffStringMaps(persisted, expected map[string]string) []string {
	var diffs []string
	for name, want := range expected {
		got, ok := persisted[name]
		if !ok {
			diffs = append(diffs, fmt.Sprintf("%s: added %s", name, want))
			continue
		}
		if got != want {
			diffs = append(diffs, fmt.Sprintf("%s: %s -> %s", name, got, want))
		}
	}
	for name, got := range persisted {
		if _, ok := expected[name]; !ok {
			diffs = append(diffs, fmt.Sprintf("%s: removed %s", name, got))
		}
	}
	sort.Strings(diffs)
	return diffs
}

func gateKeySet(items []string) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, v := range items {
		out[v] = true
	}
	return out
}

func sortedGateKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func checkStubFillingGate(sourceFiles map[string]string) GateResult {
	var hits []string
	for path, content := range sourceFiles {
		if strings.Contains(content, "ErrNotImplemented") {
			hits = append(hits, path)
		}
	}
	sort.Strings(hits)
	if len(hits) > 0 {
		return failGate(GateStubFill,
			fmt.Sprintf("ErrNotImplemented found in %s", strings.Join(hits, ", ")),
			"Implement every remaining callable stub in handlers.go (or remove ErrNotImplemented) before registering.")
	}
	return passGate(GateStubFill)
}

func checkManifestGate(appDefContent, appDefHash string, diskManifest gen.AppManifest, generated *codegen.GeneratedManifest) GateResult {
	app, diags, err := appdef.ParseFile(appDefContent)
	if err != nil {
		return failGate(GateManifest,
			fmt.Sprintf("failed to parse .appdef: %v", err),
			"Fix .appdef syntax errors and rerun the gate.")
	}
	if len(diags) > 0 {
		var msgs []string
		for _, d := range diags {
			msgs = append(msgs, d.Error())
		}
		return failGate(GateManifest,
			fmt.Sprintf(".appdef validation failed: %s", strings.Join(msgs, "; ")),
			"Fix .appdef validation errors and rerun the gate.")
	}

	expected, err := codegen.GenerateManifestFromAppDef(app)
	if err != nil {
		return failGate(GateManifest,
			fmt.Sprintf("failed to derive manifest from .appdef: %v", err),
			"Fix .appdef content so it can be turned into app.manifest.json.")
	}

	if diff := diffManifests(expected, diskManifest); diff != "" {
		return failGate(GateManifest,
			fmt.Sprintf("app.manifest.json does not match .appdef: %s", diff),
			"Run appmanager.dev_generate to regenerate app.manifest.json and the Go/TypeScript artifacts from .appdef.")
	}

	// Drift detection: if we have a previous generation record, the .appdef
	// hash must still match. A mismatch means the user edited .appdef without
	// regenerating the downstream artifacts.
	if generated != nil && generated.AppDefHash != "" && !strings.EqualFold(generated.AppDefHash, appDefHash) {
		return failGate(GateManifest,
			fmt.Sprintf(".appdef has changed since last dev_generate (stored hash %s, current %s)", generated.AppDefHash, appDefHash),
			"Run appmanager.dev_generate to refresh generated artifacts from the current .appdef.")
	}

	return passGate(GateManifest)
}

func checkCoverageGate(manifest gen.AppManifest, registeredCallables []string, loadDiagnostic string) GateResult {
	regSet := make(map[string]bool, len(registeredCallables))
	for _, id := range registeredCallables {
		// Accept both namespaced and local forms so callers can use whichever
		// representation the pluginhost surface returns.
		local := localCallableID(manifest.ID, id)
		regSet[local] = true
	}
	var missing []string
	for _, c := range manifest.Callables {
		if !regSet[c.ID] {
			missing = append(missing, c.ID)
		}
	}
	if len(missing) > 0 {
		if loadDiagnostic != "" {
			// The registry could not be measured against the built
			// artifact (load failure or a stale running instance). Report
			// the root cause; the missing-callables list is a symptom,
			// not the fix target.
			return failGate(GateCoverage,
				fmt.Sprintf("coverage could not be measured against the built artifact: %s", loadDiagnostic),
				"Resolve the cause above (fix the load error, or reload the running plugin so it activates the new build); the declared-vs-registered diff is unreliable until the built artifact is measured.")
		}
		return failGate(GateCoverage,
			fmt.Sprintf("callables declared in manifest but not registered in dispatch registry: %s", strings.Join(missing, ", ")),
			"Ensure every declared callable is registered via sdk.RegisterCallable in the plugin source, then rebuild and reload.")
	}
	return passGate(GateCoverage)
}

// localCallableID strips the "plugin.<appID>." prefix from a namespaced
// callable id, returning the id unchanged if it does not carry the prefix.
func localCallableID(appID, callID string) string {
	prefix := "plugin." + appID + "."
	if strings.HasPrefix(callID, prefix) {
		return strings.TrimPrefix(callID, prefix)
	}
	return callID
}

func passGate(name GateName) GateResult {
	return GateResult{Gate: name, Passed: true}
}

func failGate(name GateName, detail, fix string) GateResult {
	return GateResult{
		Gate:   name,
		Passed: false,
		Error:  &GateError{Gate: name, Detail: detail, Fix: fix},
	}
}

// diffManifests returns a human-readable, deterministic description of the
// semantic differences between an .appdef-derived manifest and the manifest on
// disk. Empty string means the manifests are semantically equal.
func diffManifests(expected, actual gen.AppManifest) string {
	var diffs []string

	if expected.ID != actual.ID {
		diffs = append(diffs, fmt.Sprintf("Id: expected %q, got %q", expected.ID, actual.ID))
	}
	if expected.Name != actual.Name {
		diffs = append(diffs, fmt.Sprintf("Name: expected %q, got %q", expected.Name, actual.Name))
	}
	if expected.Version != actual.Version {
		diffs = append(diffs, fmt.Sprintf("Version: expected %q, got %q", expected.Version, actual.Version))
	}
	if expected.Runtime != actual.Runtime {
		diffs = append(diffs, fmt.Sprintf("Runtime: expected %q, got %q", expected.Runtime, actual.Runtime))
	}
	if expected.Namespace != actual.Namespace {
		diffs = append(diffs, fmt.Sprintf("Namespace: expected %q, got %q", expected.Namespace, actual.Namespace))
	}
	if diff := diffStringSlices("Permissions", expected.Permissions, actual.Permissions); diff != "" {
		diffs = append(diffs, diff)
	}
	if diff := diffStringSlices("Listens", expected.Listens, actual.Listens); diff != "" {
		diffs = append(diffs, diff)
	}
	if diff := diffCallableSlices(expected.Callables, actual.Callables); diff != "" {
		diffs = append(diffs, diff)
	}
	if diff := diffEntrypointSlices(expected.Entrypoints, actual.Entrypoints); diff != "" {
		diffs = append(diffs, diff)
	}
	if diff := diffEventSlices(expected.Events, actual.Events); diff != "" {
		diffs = append(diffs, diff)
	}
	if diff := diffBundleSlices(expected.Bundles, actual.Bundles); diff != "" {
		diffs = append(diffs, diff)
	}
	if diff := diffDependencySlices(expected.Dependencies, actual.Dependencies); diff != "" {
		diffs = append(diffs, diff)
	}

	sort.Strings(diffs)
	return strings.Join(diffs, "; ")
}

func diffStringSlices(label string, a, b []string) string {
	sa, sb := sortedCopy(a), sortedCopy(b)
	if !slicesEqual(sa, sb) {
		return fmt.Sprintf("%s: expected %v, got %v", label, sa, sb)
	}
	return ""
}

func diffCallableSlices(expected, actual []gen.AppCallableDescriptor) string {
	em := callableMap(expected)
	am := callableMap(actual)
	var keys []string
	for k := range em {
		keys = append(keys, k)
	}
	for k := range am {
		if _, ok := em[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ""
	}
	var diffs []string
	for _, k := range keys {
		e, eOk := em[k]
		a, aOk := am[k]
		switch {
		case eOk && !aOk:
			diffs = append(diffs, fmt.Sprintf("callable %q missing in manifest", k))
		case !eOk && aOk:
			diffs = append(diffs, fmt.Sprintf("callable %q extra in manifest", k))
		default:
			if e.RequestSchema != a.RequestSchema {
				diffs = append(diffs, fmt.Sprintf("callable %q RequestSchema: expected %q, got %q", k, e.RequestSchema, a.RequestSchema))
			}
			if e.ResponseSchema != a.ResponseSchema {
				diffs = append(diffs, fmt.Sprintf("callable %q ResponseSchema: expected %q, got %q", k, e.ResponseSchema, a.ResponseSchema))
			}
			if e.Effect != a.Effect {
				diffs = append(diffs, fmt.Sprintf("callable %q Effect: expected %q, got %q", k, e.Effect, a.Effect))
			}
			if e.Service != a.Service {
				diffs = append(diffs, fmt.Sprintf("callable %q Service: expected %q, got %q", k, e.Service, a.Service))
			}
			if e.ToolName != a.ToolName {
				diffs = append(diffs, fmt.Sprintf("callable %q ToolName: expected %q, got %q", k, e.ToolName, a.ToolName))
			}
			if e.Streaming != a.Streaming {
				diffs = append(diffs, fmt.Sprintf("callable %q Streaming: expected %v, got %v", k, e.Streaming, a.Streaming))
			}
			if e.Expose != a.Expose {
				diffs = append(diffs, fmt.Sprintf("callable %q Expose: expected %q, got %q", k, e.Expose, a.Expose))
			}
			if diff := diffStringSlices(fmt.Sprintf("callable %q Watch", k), e.Watch, a.Watch); diff != "" {
				diffs = append(diffs, diff)
			}
		}
	}
	return strings.Join(diffs, "; ")
}

func callableMap(callables []gen.AppCallableDescriptor) map[string]gen.AppCallableDescriptor {
	m := make(map[string]gen.AppCallableDescriptor, len(callables))
	for _, c := range callables {
		m[c.ID] = c
	}
	return m
}

func diffEntrypointSlices(expected, actual []gen.AppEntrypoint) string {
	em := make(map[string]gen.AppEntrypoint, len(expected))
	for _, e := range expected {
		em[e.ID] = e
	}
	am := make(map[string]gen.AppEntrypoint, len(actual))
	for _, e := range actual {
		am[e.ID] = e
	}
	var keys []string
	for k := range em {
		keys = append(keys, k)
	}
	for k := range am {
		if _, ok := em[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ""
	}
	var diffs []string
	for _, k := range keys {
		e, eOk := em[k]
		a, aOk := am[k]
		switch {
		case eOk && !aOk:
			diffs = append(diffs, fmt.Sprintf("entrypoint %q missing in manifest", k))
		case !eOk && aOk:
			diffs = append(diffs, fmt.Sprintf("entrypoint %q extra in manifest", k))
		default:
			if e.Kind != a.Kind {
				diffs = append(diffs, fmt.Sprintf("entrypoint %q Kind: expected %q, got %q", k, e.Kind, a.Kind))
			}
			if e.Title != a.Title {
				diffs = append(diffs, fmt.Sprintf("entrypoint %q Title: expected %q, got %q", k, e.Title, a.Title))
			}
			if e.Route != a.Route {
				diffs = append(diffs, fmt.Sprintf("entrypoint %q Route: expected %q, got %q", k, e.Route, a.Route))
			}
		}
	}
	return strings.Join(diffs, "; ")
}

func diffEventSlices(expected, actual []gen.AppEventDescriptor) string {
	em := make(map[string]gen.AppEventDescriptor, len(expected))
	for _, e := range expected {
		em[e.ID] = e
	}
	am := make(map[string]gen.AppEventDescriptor, len(actual))
	for _, e := range actual {
		am[e.ID] = e
	}
	var keys []string
	for k := range em {
		keys = append(keys, k)
	}
	for k := range am {
		if _, ok := em[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ""
	}
	var diffs []string
	for _, k := range keys {
		e, eOk := em[k]
		a, aOk := am[k]
		switch {
		case eOk && !aOk:
			diffs = append(diffs, fmt.Sprintf("event %q missing in manifest", k))
		case !eOk && aOk:
			diffs = append(diffs, fmt.Sprintf("event %q extra in manifest", k))
		default:
			if e.PayloadSchema != a.PayloadSchema {
				diffs = append(diffs, fmt.Sprintf("event %q PayloadSchema: expected %q, got %q", k, e.PayloadSchema, a.PayloadSchema))
			}
			if e.Permission != a.Permission {
				diffs = append(diffs, fmt.Sprintf("event %q Permission: expected %q, got %q", k, e.Permission, a.Permission))
			}
		}
	}
	return strings.Join(diffs, "; ")
}

func diffBundleSlices(expected, actual []gen.AppBundle) string {
	em := make(map[string]gen.AppBundle, len(expected))
	for _, b := range expected {
		em[b.Title] = b
	}
	am := make(map[string]gen.AppBundle, len(actual))
	for _, b := range actual {
		am[b.Title] = b
	}
	var keys []string
	for k := range em {
		keys = append(keys, k)
	}
	for k := range am {
		if _, ok := em[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ""
	}
	var diffs []string
	for _, k := range keys {
		e, eOk := em[k]
		a, aOk := am[k]
		switch {
		case eOk && !aOk:
			diffs = append(diffs, fmt.Sprintf("bundle %q missing in manifest", k))
		case !eOk && aOk:
			diffs = append(diffs, fmt.Sprintf("bundle %q extra in manifest", k))
		default:
			if e.Description != a.Description {
				diffs = append(diffs, fmt.Sprintf("bundle %q Description: expected %q, got %q", k, e.Description, a.Description))
			}
			etools := toolIDs(e.Tools)
			atools := toolIDs(a.Tools)
			if !slicesEqual(etools, atools) {
				diffs = append(diffs, fmt.Sprintf("bundle %q Tools: expected %v, got %v", k, etools, atools))
			}
		}
	}
	return strings.Join(diffs, "; ")
}

func toolIDs(tools []gen.AppBundleTool) []string {
	var out []string
	for _, t := range tools {
		out = append(out, t.CallableID)
	}
	return sortedCopy(out)
}

func diffDependencySlices(expected, actual []gen.AppDependency) string {
	em := make(map[string]gen.AppDependency, len(expected))
	for _, d := range expected {
		em[d.ID] = d
	}
	am := make(map[string]gen.AppDependency, len(actual))
	for _, d := range actual {
		am[d.ID] = d
	}
	var keys []string
	for k := range em {
		keys = append(keys, k)
	}
	for k := range am {
		if _, ok := em[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ""
	}
	var diffs []string
	for _, k := range keys {
		e, eOk := em[k]
		a, aOk := am[k]
		switch {
		case eOk && !aOk:
			diffs = append(diffs, fmt.Sprintf("dependency %q missing in manifest", k))
		case !eOk && aOk:
			diffs = append(diffs, fmt.Sprintf("dependency %q extra in manifest", k))
		default:
			if e.Version != a.Version {
				diffs = append(diffs, fmt.Sprintf("dependency %q Version: expected %q, got %q", k, e.Version, a.Version))
			}
		}
	}
	return strings.Join(diffs, "; ")
}

func sortedCopy(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
