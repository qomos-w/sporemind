// Package codegen implements the generate callable that reads <name>.appdef
// and produces seven artifacts (main.gen.go, main_run.gen.go, server.gen.go,
// handlers.go, app.manifest.json, schemas_gen.go, client.gen.ts) plus go.mod.
// It also handles empty project default templates (explicit opt-in via
// Options.Template) and SDK workspace resolution.
package codegen

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/qomos-w/spore/schema"
	"github.com/qomos-w/sporemind/pkg/appbinding"
	"github.com/qomos-w/sporemind/pkg/appdef"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/protocol"
	"github.com/qomos-w/sporemind/pkg/util"
	sdk "github.com/qomos-w/sporemind-plugin-sdk"
)

// Output file names produced by the generator.
const (
	FileAppDef          = "app.appdef"
	FileMainGenGo       = "main.gen.go"
	FileMainRunGo       = "main_run.gen.go" // subprocess-mode entry (dev transport, //go:build !cgo)
	FileServerGenGo     = "server.gen.go"   // HTTP route registrations (direct-frontend data path)
	FileHandlersGo      = "handlers.go"
	FileManifestJSON    = "app.manifest.json"
	FileSchemasGenGo    = "schemas_gen.go"
	FileDescriptorsJSON = "app.descriptors.json"
	FileClientGenTS     = "client.gen.ts"
	FileGoMod           = "go.mod"
)

// GeneratedManifest is the persisted manifest of what was generated.
// It is stored in the project state so that write guards and register
// can verify file integrity and detect drift.
type GeneratedManifest struct {
	AppDefHash string            `json:"AppDefHash"`
	Files      map[string]string `json:"Files"` // rel path -> content hash (hex)
	// BundleSnapshots records what each bundle-level permission resolved to at
	// generation time (dep identity, expanded callIDs, schema hashes). The
	// dev_gate drift gate recomputes these against the CURRENT registry so a
	// dependency that changed shape cannot slip past registration with stale
	// typed callers.
	BundleSnapshots []BundleSnapshot `json:"BundleSnapshots,omitempty"`
}

// GenerateResult is the outcome returned to the caller.
type GenerateResult struct {
	Files          []string          `json:"Files"`          // relative paths of generated files
	ProtectedFiles []string          `json:"ProtectedFiles"` // relative paths of fully-generated files that must not be hand-edited
	Manifest       GeneratedManifest `json:"Manifest"`
	SDKPath        string            `json:"SDKPath"`
	FreshSDK       bool              `json:"FreshSDK"` // true if SDK was freshly resolved
	Template       bool              `json:"Template"` // true if default template was generated
	Warnings       []string          `json:"Warnings"` // advisory diagnostics that do not block generation (e.g. orphan handlers)
}

// Options configures the Generate function.
type Options struct {
	// SDKPath overrides SDK resolution. When set, the SDK is not auto-resolved.
	SDKPath string

	// Template explicitly requests the default template scaffold for a
	// directory that has no .appdef files. Without it, Generate refuses to
	// touch a directory without .appdef (zero writes) — scaffolding into a
	// wrong directory (e.g. a repository root) must never happen implicitly.
	// Even with Template set, the scaffold only runs when the directory has
	// no go.mod and no *.go files.
	Template bool

	// HostCalls carries the resolved wire protocols (keyed by SDK-facing host
	// callID) for the host capabilities an app may declare in .appdef
	// `permissions:`. Generate filters it down to the callIDs the app's
	// declared capabilities actually gate and emits them as hostproto.gen.go.
	// The appmanager dev_generate handler resolves this from the embedded
	// gospore manifest; empty is valid (no protocol extraction).
	HostCalls map[string]HostCallSchema

	// BundleDeps carries the REGISTERED dependency manifests (keyed by app ID,
	// with schema descriptors and package hash) resolved by the dev_generate
	// handler from the appmanager registry. Bundle-level `plugin.<dep>.<bundle>`
	// permissions are typed against them in bundle_calls.gen.go; empty is
	// valid (bundle permissions fall back to hostproto.gen.go raw callers).
	BundleDeps map[string]BundleDep
}

// Generate reads .appdef from projectDir and writes all artifacts.
// If no .appdef files exist, it produces a default template only when
// Options.Template is set; otherwise it returns an error without writing
// anything. go.mod is create-if-absent (existing user dependencies are
// preserved).
func Generate(projectDir string, opts ...Options) (*GenerateResult, error) {
	var opt Options
	if len(opts) > 0 {
		opt = opts[0]
	}

	appdefFiles, err := filepath.Glob(filepath.Join(projectDir, "*.appdef"))
	if err != nil {
		return nil, fmt.Errorf("codegen: glob appdef: %w", err)
	}

	if len(appdefFiles) == 0 {
		if !opt.Template {
			return nil, fmt.Errorf("codegen: no .appdef found in %s; pass Template to scaffold", projectDir)
		}
		return generateTemplate(projectDir, opt)
	}

	// Process each .appdef file (typically one, but support multiple).
	var allFiles []string
	var lastManifest GeneratedManifest
	var lastResult *GenerateResult

	for _, af := range appdefFiles {
		result, err := generateFromAppDef(projectDir, af, opt)
		if err != nil {
			return nil, fmt.Errorf("codegen: generate %s: %w", filepath.Base(af), err)
		}
		allFiles = append(allFiles, result.Files...)
		lastManifest = result.Manifest
		lastResult = result
	}
	if lastResult != nil {
		lastResult.Files = allFiles
		lastResult.Manifest = lastManifest
	}

	return lastResult, nil
}

// generateTemplate produces the minimal default scaffold for empty projects.
// It writes a minimal .appdef file, a default handlers.go (if absent), and
// then runs the full generateFromAppDef pipeline so the project is buildable
// and registerable immediately. It refuses to run when the directory already
// contains Go project markers (go.mod or *.go) — scaffolding over existing Go
// code is exactly the 2026-08-26 main-repo-root pollution (BP9) this guard
// exists to prevent.
func generateTemplate(projectDir string, opt Options) (*GenerateResult, error) {
	// 0. Refuse non-empty Go directories: an explicit Template request must
	// not scaffold over an existing Go project.
	if marker, err := goProjectMarker(projectDir); err != nil {
		return nil, err
	} else if marker != "" {
		return nil, fmt.Errorf(
			"codegen: template scaffold refused: %s contains %s; scaffold only into a directory without go.mod or *.go",
			projectDir, marker)
	}

	// 1. Write minimal .appdef so subsequent generates find it.
	appdefPath := filepath.Join(projectDir, FileAppDef)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		return nil, fmt.Errorf("codegen: create app dir: %w", err)
	}
	if err := os.WriteFile(appdefPath, []byte(defaultAppdefContent), 0o644); err != nil {
		return nil, fmt.Errorf("codegen: write %s: %w", FileAppDef, err)
	}

	// 2. Write default handlers.go if it does not exist, preserving
	// merge-append semantics: if the user already has a handlers.go, keep it.
	handlersPath := filepath.Join(projectDir, FileHandlersGo)
	if _, err := os.Stat(handlersPath); os.IsNotExist(err) {
		if err := os.WriteFile(handlersPath, []byte(defaultHandlersGo), 0o644); err != nil {
			return nil, fmt.Errorf("codegen: write %s: %w", FileHandlersGo, err)
		}
	}

	// 3. Run the full generate pipeline from the newly created .appdef. The
	// SDK source (override or host-resolved) is passed raw: generateFromAppDef
	// vendors it into vendor-sdk/ itself — vendoring must stay single-sourced,
	// since pre-vendoring here and vendoring again inside would copy the tree
	// onto its own RemoveAll-ed target.
	result, err := generateFromAppDef(projectDir, appdefPath, opt)
	if err != nil {
		return nil, fmt.Errorf("codegen: template scaffold: %w", err)
	}

	// Report the .appdef file and go.mod as part of the template output.
	result.Files = append([]string{FileAppDef, FileGoMod}, result.Files...)

	// The template result includes handlers.go in the files list.
	result.Template = true
	return result, nil
}

// goProjectMarker reports whether projectDir already contains Go project
// markers. It returns "go.mod" or the name of the first *.go file found
// (empty string when the directory is free of both). A directory holding
// either marker is not eligible for template scaffolding.
func goProjectMarker(projectDir string) (string, error) {
	if _, err := os.Stat(filepath.Join(projectDir, FileGoMod)); err == nil {
		return FileGoMod, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("codegen: stat %s: %w", FileGoMod, err)
	}
	goFiles, err := filepath.Glob(filepath.Join(projectDir, "*.go"))
	if err != nil {
		return "", fmt.Errorf("codegen: glob *.go: %w", err)
	}
	if len(goFiles) > 0 {
		return filepath.Base(goFiles[0]), nil
	}
	return "", nil
}

// resolveListens maps declared listen kinds to catalog entries, sorted by
// kind for deterministic output. Unknown kinds are filtered out after
// validateDeclaredListens has already rejected them at the entry point.
func resolveListens(decls []appdef.ListenDecl) []appbinding.HostEvent {
	out := make([]appbinding.HostEvent, 0, len(decls))
	seen := make(map[string]bool, len(decls))
	for _, l := range decls {
		if l.Kind == "" || seen[l.Kind] {
			continue
		}
		seen[l.Kind] = true
		if he, ok := appbinding.LookupHostEvent(l.Kind); ok {
			out = append(out, he)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}

// validateDeclaredListens rejects listen kinds outside the appbinding
// EventCatalog at zero writes — an unrecognized kind would otherwise parse
// cleanly and silently never fire.
func validateDeclaredListens(decls []appdef.ListenDecl) error {
	for _, l := range decls {
		if _, ok := appbinding.LookupHostEvent(l.Kind); !ok {
			return fmt.Errorf(
				"appdef listen: %q is not a subscribable host event kind (allowed: %s) — see appbinding.EventCatalog",
				l.Kind, strings.Join(appbinding.SubscribableEventKinds(), ", "))
		}
	}
	return nil
}

// DeclaredListens returns the deduped, sorted host event kinds the app
// subscribes to (`listen <kind> {}` blocks). The manifest carries them so the
// pluginhost can subscribe the gospore event bus on the plugin's behalf.
func DeclaredListens(app *appdef.AppDef) []string {
	if len(app.Listens) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(app.Listens))
	var out []string
	for _, l := range app.Listens {
		if l.Kind == "" || seen[l.Kind] {
			continue
		}
		seen[l.Kind] = true
		out = append(out, l.Kind)
	}
	sort.Strings(out)
	return out
}

// validateDeclaredHostCalls checks that every .appdef `permissions:` entry is
// a resolvable SDK host callID: an appbinding.SDKCallCatalog entry (llm.*,
// state.*, config.get, provider.get) or, when the host call surface is
// provided, a callID exposed by the gospore manifest (pass-through calls such
// as project.read_file or shell.exec). Capability strings are rejected with a
// dedicated hint — declaring callIDs and deriving capabilities is the only
// supported form.
func validateDeclaredHostCalls(perms []string, hostCalls map[string]HostCallSchema) error {
	for _, perm := range perms {
		// plugin.* entries are bundle callIDs (cross-plugin invocation
		// gates); they are not capabilities even though HostCallCapability
		// maps them to bundle.invoke via the prefix family.
		if strings.HasPrefix(perm, "plugin.") {
			continue
		}
		// Load-time capabilities (app.data: granted via the OnLoad config,
		// not gated by any host callID) have no callID form to declare — the
		// capability string IS the declaration. They pass through to the
		// manifest verbatim (DerivedCapabilities keeps unmapped entries).
		if appbinding.IsKnownHostCapability(perm) && !appbinding.CapabilityHasCallGate(perm) {
			continue
		}
		// A string that is BOTH a registered SDK host callID and a capability
		// (dialog.openFile / dialog.openFolder: Local calls whose callID equals
		// the capability id) is a legal callID declaration — the dev guide
		// documents exactly this form. Only pure capability strings whose
		// callIDs differ (llm.invoke vs llm.chat/llm.complete) are rejected.
		if _, isCall := appbinding.LookupSDKCall(perm); !isCall && appbinding.IsKnownHostCapability(perm) {
			return fmt.Errorf("appdef permissions: %q is a capability, not a callable — declare the host callIDs you use (e.g. llm.complete, state.get); capabilities are derived automatically (see appmanager.host_protocol)", perm)
		}
		// Entries outside the host callID namespaces are app-internal
		// permission labels (e.g. the `public` token for events); they pass
		// through to the manifest verbatim and need no host resolution.
		if appbinding.HostCallCapability(perm) == "" {
			continue
		}
		if _, ok := appbinding.LookupSDKCall(perm); ok {
			continue
		}
		if appbinding.IsSDKHostCallAlias(perm) {
			continue
		}
		if _, ok := hostCalls[perm]; ok {
			continue
		}
		return fmt.Errorf("appdef permissions: %q is not a known host callable (see appmanager.host_protocol for the catalog)", perm)
	}
	return nil
}

// generateFromAppDef processes a single .appdef file into all six artifacts.
func generateFromAppDef(projectDir string, appdefPath string, opt Options) (*GenerateResult, error) {
	src, err := os.ReadFile(appdefPath)
	if err != nil {
		return nil, fmt.Errorf("read appdef: %w", err)
	}
	appdefContent := string(src)

	// Parse and validate.
	app, diags, err := appdef.ParseFile(appdefContent)
	if err != nil {
		return nil, fmt.Errorf("parse appdef: %w", err)
	}
	if valDiags := appdef.Validate(app); len(valDiags) > 0 {
		diags = append(diags, valDiags...)
	}
	if len(diags) > 0 {
		var msgs []string
		for _, d := range diags {
			msgs = append(msgs, d.Error())
		}
		return nil, fmt.Errorf("appdef diagnostics:\n%s", strings.Join(msgs, "\n"))
	}

	// Declared permissions are SDK host callIDs (e.g. llm.complete). Each must
	// be resolvable: an appbinding.SDKCallCatalog entry (wire contract declared
	// in the host) or — when the host call surface is provided — a callID the
	// gospore manifest exposes (pass-through calls like project.read_file).
	// Capabilities are derived per callID, never declared.
	if err := validateDeclaredHostCalls(app.AppMeta.Permissions, opt.HostCalls); err != nil {
		return nil, err
	}
	if err := validateDeclaredListens(app.Listens); err != nil {
		return nil, err
	}

	// Resolve SDK path.
	sdkPath, fresh, err := ResolveSDKDir(projectDir)
	if opt.SDKPath != "" {
		sdkPath, fresh, err = opt.SDKPath, false, nil
	}
	if err != nil {
		return nil, fmt.Errorf("resolve SDK: %w", err)
	}

	// Vendor the SDK into the app directory unconditionally — every generated
	// app is self-contained regardless of host mode: go.mod replace always
	// points at ./vendor-sdk, never a host-machine path. Regeneration refreshes
	// the copy so host SDK upgrades propagate to existing apps.
	//
	// Self-reference guard: callers may pass an already-vendored path back as
	// the override (e.g. scaffolding tests re-generating an existing app).
	// Vendoring that path onto its own RemoveAll-ed target would destroy it,
	// so when the source IS the app's vendor-sdk the copy is skipped and the
	// existing tree is used as-is.
	if isVendoredSDKPath(projectDir, sdkPath) {
		sdkPath = filepath.Join(projectDir, VendorSDKDir)
	} else if vendoredPath, err := VendorSDKIntoAppDir(projectDir, sdkPath); err != nil {
		return nil, fmt.Errorf("vendor sdk: %w", err)
	} else {
		sdkPath = vendoredPath
	}

	appdefHash := contentHash(NormalizeLineEndings(appdefContent))
	files := make(map[string]string) // rel path -> content hash
	var generatedFiles []string
	var genSnapshots []BundleSnapshot
	bundleCallsWritten := false

	// 1. main.gen.go
	mainContent := generateMainGo(app, appdefHash)
	if err := writeFile(projectDir, FileMainGenGo, mainContent); err != nil {
		return nil, err
	}
	files[FileMainGenGo] = contentHash(mainContent)
	generatedFiles = append(generatedFiles, FileMainGenGo)

	// 1b. main_run.gen.go — subprocess-mode entry (dev transport, zero cgo).
	// Compiled only when cgo is disabled (//go:build !cgo); the release
	// c-shared build stages its own cgo shim separately.
	mainRunContent := generateMainRunGo()
	if err := writeFile(projectDir, FileMainRunGo, mainRunContent); err != nil {
		return nil, err
	}
	files[FileMainRunGo] = contentHash(mainRunContent)
	generatedFiles = append(generatedFiles, FileMainRunGo)

	// 1c. server.gen.go — HTTP route registrations for the direct-frontend
	// data path: one sdk.RegisterHTTPHandler per frontend-exposed callable
	// (expose: frontend|both), wrapping the typed handlers from handlers.go.
	// The SDK owns the listener (sdk.ServeHTTP: POST /invoke/{id}, GET /events
	// SSE, static files); this file only fills the dispatch table in init.
	serverContent := generateServerGenGo(app)
	if err := writeFile(projectDir, FileServerGenGo, serverContent); err != nil {
		return nil, err
	}
	files[FileServerGenGo] = contentHash(serverContent)
	generatedFiles = append(generatedFiles, FileServerGenGo)

	// 2. handlers.go — create if absent, append new stubs if exists
	handlersPath := filepath.Join(projectDir, FileHandlersGo)
	handlersResult, err := mergeHandlers(handlersPath, app.Callables, app.Listens, app)
	if err != nil {
		return nil, fmt.Errorf("merge handlers: %w", err)
	}
	if err := writeFile(projectDir, FileHandlersGo, handlersResult); err != nil {
		return nil, err
	}
	files[FileHandlersGo] = contentHash(handlersResult)
	generatedFiles = append(generatedFiles, FileHandlersGo)

	// 2b. Orphan-handler detection. handlers.go is append-only, so a handler
	// whose callable was removed from .appdef survives regeneration and
	// breaks the build when it references structs that are no longer
	// generated (round-6 external TOTP run: template handlePing outliving a
	// rewritten appdef). Not an error — the file is agent-owned — but the
	// agent gets an explicit warning instead of an unrelated compile error.
	var warnings []string
	declaredHandlers := make(map[string]bool, len(app.Callables)+len(app.Listens))
	for _, c := range app.Callables {
		declaredHandlers[CallableIDToHandlerName(c.ID)] = true
	}
	for _, l := range app.Listens {
		declaredHandlers[EventKindToHandlerName(l.Kind)] = true
	}
	var orphanHandlers []string
	for fn := range parseExistingFunctions(handlersResult) {
		if !declaredHandlers[fn] {
			orphanHandlers = append(orphanHandlers, fn)
		}
	}
	sort.Strings(orphanHandlers)
	for _, fn := range orphanHandlers {
		warnings = append(warnings, fmt.Sprintf(
			"handlers.go contains %s but .appdef declares no callable for it (orphan handler): remove the function from handlers.go and re-run dev_generate — an orphan handler referencing structs that are no longer generated breaks the build with no error pointing back to the cause",
			fn))
	}

	// 3. app.manifest.json
	manifestContent, err := generateManifest(app)
	if err != nil {
		return nil, fmt.Errorf("generate manifest: %w", err)
	}
	if err := writeFile(projectDir, FileManifestJSON, string(manifestContent)); err != nil {
		return nil, err
	}
	files[FileManifestJSON] = contentHash(string(manifestContent))
	generatedFiles = append(generatedFiles, FileManifestJSON)

	// 4. schemas_gen.go
	schemasContent, err := generateSchemasGo(app)
	if err != nil {
		return nil, err
	}
	if err := writeFile(projectDir, FileSchemasGenGo, schemasContent); err != nil {
		return nil, err
	}
	files[FileSchemasGenGo] = contentHash(schemasContent)
	generatedFiles = append(generatedFiles, FileSchemasGenGo)

	// 5. client.gen.ts
	tsContent := generateClientTS(app)
	if err := writeFile(projectDir, FileClientGenTS, tsContent); err != nil {
		return nil, err
	}
	files[FileClientGenTS] = contentHash(tsContent)
	generatedFiles = append(generatedFiles, FileClientGenTS)

	// 6. app.descriptors.json — schema descriptors for protocol registration
	// and LLM tool schemas. Only written when every callable schema resolves
	// to a declared struct (mirrors appSchemaRefs' fallback).
	if descriptors := GenerateDescriptorsFromAppDef(app); descriptors != nil {
		descriptorContent, err := json.MarshalIndent(descriptors, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshal %s: %w", FileDescriptorsJSON, err)
		}
		descriptorText := string(descriptorContent) + "\n"
		if err := writeFile(projectDir, FileDescriptorsJSON, descriptorText); err != nil {
			return nil, err
		}
		files[FileDescriptorsJSON] = contentHash(descriptorText)
		generatedFiles = append(generatedFiles, FileDescriptorsJSON)
	}

	// 6b. hostproto.gen.go — wire types and typed Call/Stream callers for the
	// host callIDs declared in `permissions:`, plus payload types for the
	// host event kinds declared in `listen <kind> {}` blocks (their OnXxx
	// handler stubs land in handlers.go). Emitted only when the app declares
	// callIDs or listens; a previously extracted file whose marker is ours is
	// stubbed out when the declarations are removed (generated artifact,
	// never a user file).
	listens := resolveListens(app.Listens)
	if len(app.AppMeta.Permissions) > 0 || len(listens) > 0 || fileHasMarker(projectDir, FileHostProtoGo, hostProtoMarker) {
		filtered := make(map[string]HostCallSpec, len(app.AppMeta.Permissions))
		for _, callID := range app.AppMeta.Permissions {
			// App-internal permission labels are not host calls; skip them.
			if appbinding.HostCallCapability(callID) == "" {
				continue
			}
			// Bundle calls (plugin.<target>.<callable>): emit a raw-JSON
			// caller. The target plugin's types are not in hostProtoPkgs,
			// so no typed structs can be generated — json.RawMessage is
			// the boundary (the target plugin owns the schema).
			if strings.HasPrefix(callID, "plugin.") {
				filtered[callID] = HostCallSpec{
					CallID:       callID,
					TargetCallID: callID,
					BundleCall:   true,
				}
				continue
			}
			if sc, ok := appbinding.LookupSDKCall(callID); ok {
				filtered[callID] = SpecFromSDKCall(sc)
				continue
			}
			hs, ok := opt.HostCalls[callID]
			if !ok {
				// A declared callID that is statically valid (an alias or a
				// catalog entry) but whose backing schema the caller did not
				// provide. dev_generate always provides the full host call
				// surface; this branch is reached only by codegen-only callers
				// (e.g. unit tests), so it degrades to a warning rather than
				// failing on an appdef the dev flow would have resolved.
				warnings = append(warnings, fmt.Sprintf(
					"permissions: %q skipped in hostproto.gen.go — the host call surface was not provided (dev_generate resolves it automatically)",
					callID))
				continue
			}
			spec, err := SpecFromSchema(callID, hs)
			if err != nil {
				return nil, fmt.Errorf("host protocol extraction: %w", err)
			}
			filtered[callID] = spec
		}
		hostProtoContent, err := EmitHostProtocolGo(goPackageName(projectDir), filtered, listens)
		if err != nil {
			return nil, fmt.Errorf("host protocol extraction: %w", err)
		}
		if err := writeFile(projectDir, FileHostProtoGo, hostProtoContent); err != nil {
			return nil, err
		}
		files[FileHostProtoGo] = contentHash(hostProtoContent)
		generatedFiles = append(generatedFiles, FileHostProtoGo)
	}

	// 6c. bundle_calls.gen.go — typed callers for bundle-level plugin.<dep>.<bundle>
	// permissions, resolved against the REGISTERED dependency manifests carried
	// in Options.BundleDeps. Callable-level permissions stay in hostproto.gen.go's
	// raw-JSON form; a previously extracted file whose marker is ours is stubbed
	// out when nothing resolves (generated artifact, never a user file). The
	// resolved snapshots ride on the GeneratedManifest for the drift gate.
	if len(opt.BundleDeps) > 0 || fileHasMarker(projectDir, FileBundleCallsGo, bundleCallsMarker) {
		bundleContent, snapshots, err := emitBundleCalls(goPackageName(projectDir), app, opt.BundleDeps)
		if err != nil {
			return nil, fmt.Errorf("bundle call extraction: %w", err)
		}
		if err := writeFile(projectDir, FileBundleCallsGo, bundleContent); err != nil {
			return nil, err
		}
		files[FileBundleCallsGo] = contentHash(bundleContent)
		generatedFiles = append(generatedFiles, FileBundleCallsGo)
		genSnapshots = snapshots
		bundleCallsWritten = true
	}

	// 7. go.mod — create if absent
	if err := ensureGoMod(projectDir, app.AppMeta.ID, sdkPath); err != nil {
		return nil, fmt.Errorf("ensure go.mod: %w", err)
	}

	// Report the SDK the app actually builds against: vendored apps resolve
	// the replace to ./vendor-sdk, so the host-resolved sdkPath would point at
	// a directory the project will never reference (and may not exist).
	reportedSDKPath := sdkPath
	if GoModUsesVendoredSDK(projectDir) {
		reportedSDKPath = filepath.Join(projectDir, VendorSDKDir)
	}

	genManifest := GeneratedManifest{
		AppDefHash:      appdefHash,
		Files:           files,
		BundleSnapshots: genSnapshots,
	}

	// ProtectedFiles are fully-generated files that must not be hand-edited.
	// handlers.go is excluded because it is agent-owned after creation; go.mod
	// is excluded because it is create-if-absent only.
	protectedFiles := []string{FileMainGenGo, FileMainRunGo, FileServerGenGo, FileManifestJSON, FileSchemasGenGo, FileDescriptorsJSON, FileClientGenTS}
	if bundleCallsWritten {
		protectedFiles = append(protectedFiles, FileBundleCallsGo)
	}

	return &GenerateResult{
		Files:          generatedFiles,
		ProtectedFiles: protectedFiles,
		Manifest:       genManifest,
		SDKPath:        reportedSDKPath,
		FreshSDK:       fresh,
		Warnings:       warnings,
	}, nil
}

// --- Artifact Generators ---

// generateMainGo produces main.gen.go: the registration-only entry
// (sdk.Register in init). It intentionally contains no cgo ABI exports and no
// main() — the dev-transport entry lives in main_run.gen.go (//go:build !cgo,
// sdk.RunProcess) and the release c-shared ABI is a cgo shim staged by the
// build, not a codegen artifact.
func generateMainGo(app *appdef.AppDef, appdefHash string) string {
	meta := app.AppMeta

	var callables []string
	for _, c := range app.Callables {
		resp := c.Response
		if resp == "" {
			resp = "void"
		}
		svc := c.Service
		if svc == "" {
			svc = app.AppMeta.ID
		}
		callables = append(callables, fmt.Sprintf(
			`{ID: %q, Description: %q, RequestSchema: %q, ResponseSchema: %q, Effect: %q, ToolName: %q, Service: %q, Streaming: %t, TimeoutMs: %d, Expose: %q, Watch: %s}`,
			c.ID, c.Description, c.Request, resp, c.Effect, c.ToolName, svc, c.Streaming, c.TimeoutMs,
			c.Expose, goStringSliceLiteral(c.Watch),
		))
	}

	var entrypoints []string
	for _, ep := range app.Entrypoints {
		entrypoints = append(entrypoints, fmt.Sprintf(
			`{Kind: %q, ID: %q, Title: %q, Route: %q}`,
			ep.Kind, ep.ID, ep.Title, ep.Route,
		))
	}

	var registerStmts []string
	var unregisterStmts []string
	for _, c := range app.Callables {
		handlerFn := CallableIDToHandlerName(c.ID)
		registerMethod := "RegisterCallable"
		if c.Streaming {
			registerMethod = "RegisterCallableStream"
		}
		registerStmts = append(registerStmts,
			fmt.Sprintf(`ctx.%s(%q, %s)`, registerMethod, c.ID, handlerFn))
		unregisterStmts = append(unregisterStmts,
			fmt.Sprintf(`ctx.UnregisterCallable(%q)`, c.ID))
	}
	for _, l := range app.Listens {
		he, ok := appbinding.LookupHostEvent(l.Kind)
		if !ok {
			continue
		}
		// Typed OnXxx handlers (handlers.go) are wrapped with the JSON
		// decode step here — the wrapper lives in generated code, the typed
		// logic in agent-owned code, mirroring the callable split.
		registerStmts = append(registerStmts, fmt.Sprintf(`ctx.RegisterEventListener(%q, func(payload json.RawMessage) error {
				var ev %[2]s
				if err := json.Unmarshal(payload, &ev); err != nil {
					return err
				}
				return %[3]s(ev)
			})`, l.Kind, he.PayloadType.Name(), EventKindToHandlerName(l.Kind)))
	}

	var permissionsLit string
	if caps := derivedEventPermissions(meta.Permissions, app.Events); len(caps) > 0 {
		quoted := make([]string, len(caps))
		for i, p := range caps {
			quoted[i] = fmt.Sprintf("%q", p)
		}
		permissionsLit = `[]string{` + strings.Join(quoted, ", ") + `}`
	} else {
		permissionsLit = `[]string{}`
	}

	version := meta.Version
	if version == "" {
		version = "0.1.0"
	}

	// Build callables and entrypoints string fragments.
	// Handle empty slices to avoid trailing comma syntax errors.
	callablesStr := strings.Join(callables, ",\n\t\t\t\t\t")
	if len(callables) > 0 {
		callablesStr = "\n\t\t\t\t\t" + callablesStr + ",\n\t\t\t\t"
	}
	entrypointsStr := strings.Join(entrypoints, ",\n\t\t\t\t\t")
	if len(entrypoints) > 0 {
		entrypointsStr = "\n\t\t\t\t\t" + entrypointsStr + ",\n\t\t\t\t"
	}

	return fmt.Sprintf(`// main.gen.go — Code generated from appdef. DO NOT EDIT.
// @generated-hash: %s
package main

import (
	%s
	sdk "github.com/qomos-w/sporemind-plugin-sdk"
)

func init() {
	sdk.Register(&sdk.Plugin{
		Manifest: sdk.Manifest{
			ID:          %q,
			Name:        %q,
			Version:     %q,
			Permissions: %s,
			Callables: []sdk.Callable{%s},
			Entrypoints: []sdk.Entrypoint{%s},
		},
		OnLoad: func(ctx sdk.Context) error {
			%s
			return nil
		},
		OnUnload: func(ctx sdk.Context) error {
			%s
			return nil
		},
	})
}
%s`,
		appdefHash,
		JSONImportLiteral(app.Listens),
		meta.ID, meta.Name, version, permissionsLit,
		callablesStr,
		entrypointsStr,
		strings.Join(registerStmts, "\n\t\t\t"),
		strings.Join(unregisterStmts, "\n\t\t\t"),
		emitEventWrappers(app.Events),
	)
}

// derivedEventPermissions extends the appdef-derived host-call capabilities
// with app.emit when the app declares `event` blocks: emitting declared
// events is part of the declared event surface, so codegen derives the
// capability instead of requiring a manual permissions entry.
func derivedEventPermissions(permissions []string, events []appdef.EventDecl) []string {
	caps := appbinding.DerivedCapabilities(permissions)
	if len(events) == 0 {
		return caps
	}
	for _, c := range caps {
		if c == appbinding.CapAppEmit {
			return caps
		}
	}
	return append(caps, appbinding.CapAppEmit)
}

// emitEventWrappers renders one typed publish wrapper per declared event in
// main.gen.go: EmitXxx(payload Xxx) → sdk.EmitEvent("<id>", payload). Payload
// follows the struct JSON shape (schemas_gen.go); void events emit nil.
func emitEventWrappers(events []appdef.EventDecl) string {
	if len(events) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n")
	for _, e := range events {
		fn := "Emit" + EventIDToGoFuncName(e.ID)
		call := fmt.Sprintf("return sdk.EmitEvent(%q, nil)", e.ID)
		payloadType := "payload struct{}"
		if e.Payload != "" {
			call = fmt.Sprintf("return sdk.EmitEvent(%q, payload)", e.ID)
			payloadType = "payload " + e.Payload
		}
		fmt.Fprintf(&b, "\n// %[1]s publishes the app-declared event %[2]q (main.gen.go codegen wrapper\n// over sdk.EmitEvent; the host validates the event against the app manifest\n// before broadcasting).\nfunc %[1]s(%[3]s) error {\n\t%[4]s\n}\n",
			fn, e.ID, payloadType, call)
	}
	return b.String()
}

// JSONImportLiteral returns the encoding/json import line for generated
// main.gen.go when the app declares listens (the event decode wrapper needs
// it), or an empty placeholder otherwise.
func JSONImportLiteral(listens []appdef.ListenDecl) string {
	if len(listens) == 0 {
		return ""
	}
	return "\t\"encoding/json\"\n"
}

// goStringSliceLiteral renders a []string as a Go composite literal suitable
// for embedding in generated source (main.gen.go sdk.Callable). A nil/empty
// slice renders as nil so the JSON omitempty tag drops it from the wire form.
func goStringSliceLiteral(ss []string) string {
	if len(ss) == 0 {
		return "nil"
	}
	quoted := make([]string, len(ss))
	for i, s := range ss {
		quoted[i] = fmt.Sprintf("%q", s)
	}
	return "[]string{" + strings.Join(quoted, ", ") + "}"
}

// generateMainRunGo produces main_run.gen.go, the subprocess-mode entry point
// (dev transport, zero cgo). It is compiled only when cgo is disabled
// (//go:build !cgo); the release c-shared build stages its own cgo shim
// separately. The transport itself lives in the SDK (sdk.RunProcess): the
// stdin/stdout framing, injected IPC reverse host and direct log frames.
//
// The entry also starts the plugin HTTP listener — the gateway data-path
// backend the host proxies /plugin/{id} traffic to (static assets,
// POST /invoke/{id}, GET /events SSE) — on an ephemeral port, parallel to
// the frame protocol. ServeHTTP is
// idempotent while running, so a host that pushes an httpAddr through the
// OnLoad config (LoadConfig) never double-binds: whichever side starts first
// wins and the other becomes a no-op.
func generateMainRunGo() string {
	return `// main_run.gen.go — Code generated from appdef. DO NOT EDIT.
// Subprocess entry (dev transport): built only with cgo disabled; the release
// c-shared build stages its own cgo shim elsewhere. Starts the plugin's
// HTTP listener (the gateway data-path backend for the panel frontend) in
// parallel to the stdin/stdout frame protocol (sdk.RunProcess).

//go:build !cgo

package main

import sdk "github.com/qomos-w/sporemind-plugin-sdk"

func main() {
	// Direct-HTTP data path: static files, POST /invoke/{id} and GET /events
	// SSE, served same-origin with no CORS. server.gen.go fills the HTTP
	// handler map in init; the bound address goes to the log stream and the
	// OnLoad response for the host to discover (T4).
	if srv, err := sdk.ServeHTTP("127.0.0.1:0"); err != nil {
		sdk.Log(sdk.LogLevelError, "serve http: %v", err)
	} else {
		sdk.Log(sdk.LogLevelInfo, "http listener on %s", srv.Addr())
	}
	sdk.RunProcess()
}
`
}

// callableExposedToFrontend reports whether a callable may be served to the
// panel frontend over the plugin's HTTP listener. The default (empty expose)
// is "both"; only an explicit expose: "agent" opts out of the frontend
// surface. Symmetrically, expose: "frontend" callables are filtered out of
// the agent tool registry at projection time (pkg/actor/agent toolregistry).
func callableExposedToFrontend(c appdef.CallableDecl) bool {
	return c.Expose != "agent"
}

// generateServerGenGo produces server.gen.go: the HTTP dispatch table for the
// direct-frontend data path. One sdk.RegisterHTTPHandler per frontend-exposed
// callable, registered in package init so the table is populated before
// sdk.ServeHTTP serves traffic. Each HTTPHandler adapts the typed handler
// from handlers.go to the SDK's JSON wire shape (raw request body in, response
// payload out). Streaming callables degrade to unary over HTTP: the emit
// closure drops intermediate chunks (progress should be modelled with events
// over SSE), and only the terminal response is returned.
func generateServerGenGo(app *appdef.AppDef) string {
	var b strings.Builder
	b.WriteString(`// server.gen.go — Code generated from appdef. DO NOT EDIT.
// HTTP route registrations for the direct-frontend data path: one
// sdk.RegisterHTTPHandler per frontend-exposed callable (expose: frontend|both).
// The SDK owns the listener and routes (sdk.ServeHTTP: POST /invoke/{id},
// GET /events SSE, static files); this file only fills the dispatch table.
// Callables declared expose: "agent" are agent-facing only and intentionally
// not served over HTTP.
package main

import (
	"encoding/json"

	sdk "github.com/qomos-w/sporemind-plugin-sdk"
)

func init() {
`)

	var registered []string
	for _, c := range app.Callables {
		if !callableExposedToFrontend(c) {
			continue
		}
		registered = append(registered, c.ID)
		handlerFn := CallableIDToHandlerName(c.ID)
		if c.Streaming {
			fmt.Fprintf(&b, `	// %s (streaming): HTTP invoke is unary — intermediate chunks are not
	// relayed over POST /invoke; model stream progress with events over SSE.
	sdk.RegisterHTTPHandler(%[1]q, func(payload json.RawMessage) (any, error) {
		resp, err := %[2]s(sdk.Request{Payload: payload}, func(sdk.Response) error { return nil })
		if err != nil {
			return nil, err
		}
		return resp.Payload, nil
	})
`, c.ID, handlerFn)
		} else {
			fmt.Fprintf(&b, `	sdk.RegisterHTTPHandler(%q, func(payload json.RawMessage) (any, error) {
		resp, err := %s(sdk.Request{Payload: payload})
		if err != nil {
			return nil, err
		}
		return resp.Payload, nil
	})
`, c.ID, handlerFn)
		}
	}
	if len(registered) == 0 {
		b.WriteString("	// No frontend-exposed callables declared in .appdef.\n")
	}
	b.WriteString("}\n")
	return b.String()
}

// mergeHandlers reads existing handlers.go and merges new stubs.
// Returns the new file content. Existing functions are never overwritten;
// only new callable stubs and OnXxx event handler stubs are appended.
//
// Stubs are compiling scaffolds, not ErrNotImplemented placeholders: a
// callable stub decodes the request (when it needs field echoes), echoes
// same-named request fields into the response (everything else zero-value),
// and — for effect: "mutate" callables that declare watch events — announces
// each watched event with sdk.EmitEvent (nil payload, TODO to build the real
// payload). This keeps a freshly generated app buildable and runnable end to
// end while making the stub_filling gate a no-op for new scaffolds.
func mergeHandlers(handlersPath string, callables []appdef.CallableDecl, listens []appdef.ListenDecl, app *appdef.AppDef) (string, error) {
	existingContent := ""
	if data, err := os.ReadFile(handlersPath); err == nil {
		existingContent = string(data)
	}

	// Parse which handler functions already exist.
	existingFns := parseExistingFunctions(existingContent)

	var newStubs []string
	needsJSON := false
	for _, c := range callables {
		handlerName := CallableIDToHandlerName(c.ID)
		if _, exists := existingFns[handlerName]; exists {
			continue
		}
		stub, usesJSON := generateCallableStub(c, app)
		needsJSON = needsJSON || usesJSON
		newStubs = append(newStubs, stub)
	}
	for _, l := range listens {
		he, ok := appbinding.LookupHostEvent(l.Kind)
		if !ok {
			continue
		}
		handlerName := EventKindToHandlerName(l.Kind)
		if _, exists := existingFns[handlerName]; exists {
			continue
		}
		stub := fmt.Sprintf(
			"// TODO(agent): implement %s — host event %s (%s). Stub: no-op.\nfunc %s(ev %s) error {\n\treturn nil\n}\n",
			handlerName, l.Kind, he.Note, handlerName, he.PayloadType.Name(),
		)
		newStubs = append(newStubs, stub)
	}

	if existingContent == "" {
		jsonImport := ""
		if needsJSON {
			jsonImport = "\t\"encoding/json\"\n\n"
		}
		header := `// handlers.go — created by generator; agent-owned afterwards.
// New callables get stubs appended by regenerate; existing functions are never overwritten.
// Stubs compile and run: zero-value/echo responses, no not-implemented placeholders.
package main

import (
` + jsonImport + `	sdk "github.com/qomos-w/sporemind-plugin-sdk"
)

`
		return header + strings.Join(newStubs, "\n"), nil
	}

	if len(newStubs) > 0 {
		content := existingContent
		if needsJSON {
			content = ensureHandlersJSONImport(content)
		}
		separator := "\n"
		if !strings.HasSuffix(content, "\n") {
			separator = "\n\n"
		}
		return content + separator + strings.Join(newStubs, "\n"), nil
	}
	return existingContent, nil
}

// ensureHandlersJSONImport adds the encoding/json import to an existing
// handlers.go that lacks it. Stubs that echo request fields decode the
// payload with json.Unmarshal; an appending regeneration must therefore
// upgrade the import block when the original file predates echo stubs.
// Handles both the block-import header and the single-line import form.
func ensureHandlersJSONImport(content string) string {
	if strings.Contains(content, `"encoding/json"`) {
		return content
	}
	if strings.Contains(content, "import (\n") {
		return strings.Replace(content, "import (\n", "import (\n\t\"encoding/json\"\n", 1)
	}
	const single = `import sdk "github.com/qomos-w/sporemind-plugin-sdk"`
	if strings.Contains(content, single) {
		return strings.Replace(content, single,
			"import (\n\t\"encoding/json\"\n\n"+single+"\n)", 1)
	}
	// No recognizable import form: leave the file untouched; the compile
	// error will point at the new stub, not a mangled import block.
	return content
}

// generateCallableStub renders one compiling stub for a callable. The second
// return reports whether the stub body decodes the request payload (and thus
// needs the encoding/json import).
func generateCallableStub(c appdef.CallableDecl, app *appdef.AppDef) (string, bool) {
	echoFields, respExpr, usesJSON := responseStubExpr(c, app)
	emitLines, _ := mutateEmitStmts(c, app)

	sig := fmt.Sprintf("func %s(req sdk.Request) (sdk.Response, error)", CallableIDToHandlerName(c.ID))
	if c.Streaming {
		sig = fmt.Sprintf("func %s(req sdk.Request, emit func(sdk.Response) error) (sdk.Response, error)", CallableIDToHandlerName(c.ID))
	}

	var b strings.Builder
	if c.Streaming {
		fmt.Fprintf(&b, "// TODO(agent): implement %s (streaming: call emit per intermediate chunk, then return the terminal)\n", c.ID)
	} else {
		fmt.Fprintf(&b, "// TODO(agent): implement %s\n", c.ID)
	}
	b.WriteString(sig + " {\n")

	if usesJSON {
		reqType := c.Request
		if reqType == "" || reqType == "void" {
			reqType = "struct{}"
		}
		fmt.Fprintf(&b, "\tvar payload %s\n\tif err := json.Unmarshal(req.Payload, &payload); err != nil {\n\t\treturn sdk.Response{}, err\n\t}\n", reqType)
	}
	for _, line := range emitLines {
		b.WriteString("\t" + line + "\n")
	}
	if len(echoFields) > 0 {
		fmt.Fprintf(&b, "\t// Stub: echoes same-named request fields; all other response fields are zero values.\n")
	} else if len(emitLines) > 0 {
		fmt.Fprintf(&b, "\t// Stub: emits the watched event(s) above with nil payloads — build real payloads.\n")
	} else {
		b.WriteString("\t// Stub: zero-value response.\n")
	}
	fmt.Fprintf(&b, "\treturn sdk.Response{Payload: %s}, nil\n}\n", respExpr)
	return b.String(), usesJSON
}

// responseStubExpr builds the response payload expression for a stub.
//   - void response → "nil" (Payload omitted semantics).
//   - declared struct → composite literal; same-named, same-type, same-
//     optionality request fields are echoed, everything else zero.
//   - array/map alias → empty composite literal; scalar alias or unknown ref
//     → *new(T) (valid zero value for any Go type).
//
// Echoes require decoding the request, reported via the third return.
func responseStubExpr(c appdef.CallableDecl, app *appdef.AppDef) (echoFields []string, expr string, usesJSON bool) {
	if c.Response == "" || c.Response == "void" {
		return nil, "nil", false
	}
	respStruct := findStruct(app.Structs, c.Response)
	if respStruct == nil {
		// Alias or unresolved reference: empty composite literal works for
		// slice/map aliases; scalars need *new(T).
		if isCompositeZeroOK(c.Response, app) {
			return nil, c.Response + "{}", false
		}
		return nil, "*new(" + c.Response + ")", false
	}

	reqStruct := findStruct(app.Structs, c.Request)
	var echoes []string
	var fields []string
	seen := make(map[string]bool, len(respStruct.Fields))
	for _, f := range respStruct.Fields {
		goField := exportGoName(f.Name)
		if seen[goField] {
			continue
		}
		seen[goField] = true
		// Non-echoed fields are omitted from the literal: zero by omission,
		// valid for every Go type (scalars cannot take T{} composite syntax).
		if matchRequestField(reqStruct, f) != nil {
			fields = append(fields, goField+": payload."+goField)
			echoes = append(echoes, f.Name)
		}
	}
	if len(fields) == 0 {
		return nil, c.Response + "{}", false
	}
	expr = c.Response + "{\n\t\t" + strings.Join(fields, ",\n\t\t") + ",\n\t}"
	if len(echoes) > 0 {
		usesJSON = true
	}
	return echoes, expr, usesJSON
}

// mutateEmitStmts builds the sdk.EmitEvent statements for an effect: "mutate"
// callable: every event listed in its watch field is announced (nil payload).
// The watch linkage is the declared association between a mutating callable
// and the events that announce its change; unwatched mutate callables get no
// emit (and the T1 dev_gate static check only requires EmitEvent for watched
// ones).
func mutateEmitStmts(c appdef.CallableDecl, app *appdef.AppDef) (lines []string, ok bool) {
	if c.Effect != "mutate" || len(c.Watch) == 0 {
		return nil, false
	}
	for _, evID := range c.Watch {
		if findEvent(app.Events, evID) == nil {
			continue
		}
		lines = append(lines, fmt.Sprintf("if err := sdk.EmitEvent(%q, nil); err != nil {\n\t\treturn sdk.Response{}, err\n\t}", evID))
	}
	return lines, len(lines) > 0
}

// matchRequestField finds the request struct field that an echo can copy
// from: same declared name, same type, same optionality (optional fields are
// pointers in Go on exactly one side otherwise).
func matchRequestField(req *schema.ObjectDesc, respField schema.FieldDesc) *schema.FieldDesc {
	if req == nil {
		return nil
	}
	for _, rf := range req.Fields {
		if rf.Name != respField.Name || rf.Optional != respField.Optional {
			continue
		}
		if fmt.Sprintf("%v", rf.Type) != fmt.Sprintf("%v", respField.Type) {
			continue
		}
		return &rf
	}
	return nil
}

// findStruct returns the declared struct with the given name, or nil.
func findStruct(structs []schema.ObjectDesc, name string) *schema.ObjectDesc {
	if name == "" {
		return nil
	}
	for i := range structs {
		if structs[i].Name == name {
			return &structs[i]
		}
	}
	return nil
}

// findEvent returns the declared event with the given id, or nil.
func findEvent(events []appdef.EventDecl, id string) *appdef.EventDecl {
	for i := range events {
		if events[i].ID == id {
			return &events[i]
		}
	}
	return nil
}

// isCompositeZeroOK reports whether `T{}` is a valid zero-value expression
// for a response type that is not a declared struct: true for array/map
// type aliases, false for scalar aliases.
func isCompositeZeroOK(name string, app *appdef.AppDef) bool {
	for _, a := range app.TypeAliases {
		if a.Name != name {
			continue
		}
		t := strings.TrimSpace(a.Target)
		return strings.HasPrefix(t, "array<") || strings.HasPrefix(t, "map<")
	}
	return false
}

// parseExistingFunctions finds all `func handleXxx(` and `func OnXxx(`
// function declarations.
func parseExistingFunctions(content string) map[string]bool {
	fns := make(map[string]bool)
	re := regexp.MustCompile(`^func\s+(handle\w+|On\w+)\s*\(`)
	for _, line := range strings.Split(content, "\n") {
		if m := re.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			fns[m[1]] = true
		}
	}
	return fns
}

// GenerateManifestFromAppDef produces the same AppManifest that codegen writes
// to app.manifest.json. It is exported for the gate consistency check.
func GenerateManifestFromAppDef(app *appdef.AppDef) (gen.AppManifest, error) {
	meta := app.AppMeta

	version := meta.Version
	if version == "" {
		version = "0.1.0"
	}
	namespace := meta.Namespace
	if namespace == "" {
		namespace = meta.ID
	}

	var callables []gen.AppCallableDescriptor
	for _, c := range app.Callables {
		resp := normalizeTypeRef(c.Response)
		if resp == "" {
			resp = "void"
		}
		svc := c.Service
		if svc == "" {
			svc = app.AppMeta.ID
		}
		callables = append(callables, gen.AppCallableDescriptor{
			ID:             c.ID,
			Description:    c.Description,
			RequestSchema:  c.Request,
			ResponseSchema: resp,
			Effect:         c.Effect,
			Service:        svc,
			ToolName:       c.ToolName,
			Streaming:      c.Streaming,
			TimeoutMs:      c.TimeoutMs,
			// Expose stays empty when undeclared: an empty value means the
			// default "both" (reachable from frontend panel and LLM agent
			// alike) and is consumed as-is, mirroring Effect/ToolName which
			// also omit their default rather than normalize it. Normalizing
			// here would flag drift against every pre-existing manifest.
			Expose: c.Expose,
			Watch:  c.Watch,
		})
	}

	var entrypoints []gen.AppEntrypoint
	for _, ep := range app.Entrypoints {
		entrypoints = append(entrypoints, gen.AppEntrypoint{
			Kind:  ep.Kind,
			ID:    ep.ID,
			Title: ep.Title,
			Route: ep.Route,
		})
	}

	var events []gen.AppEventDescriptor
	for _, ev := range app.Events {
		events = append(events, gen.AppEventDescriptor{
			ID:            ev.ID,
			PayloadSchema: ev.Payload,
			Permission:    ev.Permission,
		})
	}

	var bundles []gen.AppBundle
	for _, b := range app.Bundles {
		var tools []gen.AppBundleTool
		for _, toolID := range b.Tools {
			tools = append(tools, gen.AppBundleTool{CallableID: toolID})
		}
		bundles = append(bundles, gen.AppBundle{
			Title:       b.Title,
			Description: b.Description,
			Icon:        b.Icon,
			Color:       b.Color,
			Tools:       tools,
		})
	}

	var deps []gen.AppDependency
	for _, d := range app.Dependencies {
		deps = append(deps, gen.AppDependency{
			ID:      d.ID,
			Version: d.Version,
		})
	}

	return gen.AppManifest{
		ID:              meta.ID,
		Name:            meta.Name,
		Version:         version,
		Runtime:         "native",
		ProtocolVersion: 2,
		SdkVersion:      sdk.Version,
		Namespace:       namespace,
		Permissions:     derivedEventPermissions(meta.Permissions, app.Events),
		Listens:         DeclaredListens(app),
		Callables:       callables,
		Entrypoints:     entrypoints,
		Events:          events,
		Bundles:         bundles,
		Dependencies:    deps,
		AgentBinding:    buildAgentBinding(app),
		Schemas:         appSchemaRefs(app),
	}, nil
}

// appSchemaRefs emits AppSchemaRef entries for every declared struct when (and
// only when) every callable request/response schema resolves to a declared
// struct. Alias- or composite-typed request schemas fall back to the legacy
// no-refs manifest so protocol validation (which requires a descriptor per
// referenced schema) never sees an unresolvable reference.
//
// Hashes are computed over the descriptor converted back to a spore
// ObjectDesc — the exact bytes protocol.ValidateAppSchemaDescriptors hashes —
// so a generated manifest+descriptor pair always round-trips.
func appSchemaRefs(app *appdef.AppDef) []gen.AppSchemaRef {
	descriptors := GenerateDescriptorsFromAppDef(app)
	if descriptors == nil {
		return nil
	}
	objects, err := protocol.AppObjectDescriptors(descriptors)
	if err != nil {
		return nil
	}
	refs := make([]gen.AppSchemaRef, 0, len(app.Structs))
	ids := appSchemaIDs(app)
	for _, obj := range app.Structs {
		converted, ok := objects[obj.Name]
		if !ok {
			continue
		}
		encoded, marshalErr := json.Marshal(converted)
		if marshalErr != nil {
			continue
		}
		sum := sha256.Sum256(encoded)
		refs = append(refs, gen.AppSchemaRef{
			Name:     obj.Name,
			Hash:     hex.EncodeToString(sum[:]),
			SchemaID: ids[obj.Name],
		})
	}
	return refs
}

// localSchemaIDMask is the per-namespace local schema id space enforced by
// the spore registry's RegisterExternal (21 bits).
const localSchemaIDMask int64 = 0x1fffff

// appSchemaIDs derives deterministic local schema IDs for every declared
// struct: fnv64a(appID, name) masked into the local id space, with
// declaration-order linear probing to resolve collisions inside the app.
// Manifest refs and the descriptor artifact are generated together, so the
// same map feeds both and stays consistent on disk.
func appSchemaIDs(app *appdef.AppDef) map[string]int64 {
	taken := make(map[int64]bool, len(app.Structs))
	out := make(map[string]int64, len(app.Structs))
	h := fnv.New64a()
	for _, obj := range app.Structs {
		h.Reset()
		h.Write([]byte(app.AppMeta.ID))
		h.Write([]byte{0})
		h.Write([]byte(obj.Name))
		id := int64(h.Sum64()) & localSchemaIDMask
		for id == 0 || id <= protocol.BuiltinReserveEnd || taken[id] {
			id = (id + 1) & localSchemaIDMask
		}
		taken[id] = true
		out[obj.Name] = id
	}
	return out
}

// GenerateDescriptorsFromAppDef produces the schema descriptor map written to
// app.descriptors.json. It mirrors appSchemaRefs' fallback: nil when any
// callable schema is not a declared struct.
func GenerateDescriptorsFromAppDef(app *appdef.AppDef) map[string]gen.AppObjectDescriptor {
	structs := make(map[string]schema.ObjectDesc, len(app.Structs))
	for _, obj := range app.Structs {
		structs[obj.Name] = obj
	}
	for _, c := range app.Callables {
		for _, ref := range []string{normalizeTypeRef(c.Request), normalizeTypeRef(c.Response)} {
			if ref == "" || ref == "void" {
				continue
			}
			if _, ok := structs[ref]; !ok {
				return nil
			}
		}
	}
	out := make(map[string]gen.AppObjectDescriptor, len(app.Structs))
	ids := appSchemaIDs(app)
	for _, obj := range app.Structs {
		out[obj.Name] = gen.AppObjectDescriptor{
			Kind:     string(obj.Kind),
			Name:     obj.Name,
			Fields:   appFieldDescriptors(obj.Fields),
			SchemaID: ids[obj.Name],
		}
	}
	return out
}

func appFieldDescriptors(fields []schema.FieldDesc) []gen.AppFieldDescriptor {
	out := make([]gen.AppFieldDescriptor, len(fields))
	for i, f := range fields {
		out[i] = gen.AppFieldDescriptor{
			Name:        f.Name,
			Type:        appTypeDescriptorFromSpore(f.Type),
			Description: f.Description,
			Private:     f.Private,
			Optional:    f.Optional,
		}
	}
	return out
}

func appTypeDescriptorFromSpore(t schema.TypeDesc) gen.AppTypeDescriptor {
	out := gen.AppTypeDescriptor{
		Kind:      string(t.Kind),
		Name:      t.Name,
		TypeID:    int64(t.TypeID),
		ClassName: t.ClassName,
		ClassID:   int64(t.ClassID),
	}
	if t.Element != nil {
		v := appTypeDescriptorFromSpore(*t.Element)
		out.Element = &v
	}
	if t.Key != nil {
		v := appTypeDescriptorFromSpore(*t.Key)
		out.Key = &v
	}
	if t.Value != nil {
		v := appTypeDescriptorFromSpore(*t.Value)
		out.Value = &v
	}
	return out
}

// buildAgentBinding maps free_agent / plugin_agent declarations onto the host
// AppAgentBinding contract. FreeAgent and PluginAgents are populated
// independently; Surface and Capability are left nil. PluginAgents carries
// one entry per declared block in declaration order, each stamped with its
// binding slot name ("default" for the unnamed block, normalized by the
// parser) so a HEAD-equivalent single unnamed block round-trips as
// PluginAgents[0].Name == "default".
func buildAgentBinding(app *appdef.AppDef) *gen.AppAgentBinding {
	if app.FreeAgent == nil && len(app.PluginAgents) == 0 {
		return nil
	}
	out := &gen.AppAgentBinding{}
	if app.FreeAgent != nil {
		out.FreeAgent = &gen.FreeAgentBinding{
			AllowCreate:  app.FreeAgent.AllowCreate,
			AllowSwitch:  app.FreeAgent.AllowSwitch,
			AllowMessage: app.FreeAgent.AllowMessage,
			AgentKinds:   app.FreeAgent.AgentKinds,
		}
	}
	for _, pa := range app.PluginAgents {
		out.PluginAgents = append(out.PluginAgents, gen.PluginAgentBinding{
			Name:         pa.Name,
			DisplayName:  pa.DisplayName,
			SystemPrompt: pa.SystemPrompt,
			Bundles:      pa.Bundles,
			Model:        pa.Model,
		})
	}
	return out
}

// generateManifest produces app.manifest.json content.
func generateManifest(app *appdef.AppDef) ([]byte, error) {
	manifest, err := GenerateManifestFromAppDef(app)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(manifest, "", "  ")
}

// generateSchemasGo produces schemas_gen.go with Go struct definitions.
// exportGoName returns an exported Go field name for a declared spore field
// name, preserving the wire name via the json tag. A lowercase-declared field
// (e.g. "account") would otherwise emit an unexported Go field that
// encoding/json silently ignores in both directions — every handler then sees
// zero values no matter what the caller sends (observed live in an
// out-of-repo app smoke test).
func exportGoName(name string) string {
	if name == "" {
		return name
	}
	r, size := utf8.DecodeRuneInString(name)
	return string(unicode.ToUpper(r)) + name[size:]
}

func generateSchemasGo(app *appdef.AppDef) (string, error) {
	var sb strings.Builder
	sb.WriteString("// schemas_gen.go — generated from appdef. DO NOT EDIT.\n")
	sb.WriteString("package main\n")

	aliasMap := make(map[string]string, len(app.TypeAliases))
	for _, a := range app.TypeAliases {
		aliasMap[a.Name] = a.Target
	}

	for _, obj := range app.Structs {
		sb.WriteString(fmt.Sprintf("\ntype %s struct {\n", obj.Name))
		seen := make(map[string]string, len(obj.Fields))
		for _, f := range obj.Fields {
			goField := exportGoName(f.Name)
			if prev, dup := seen[goField]; dup {
				return "", fmt.Errorf("struct %q: fields %q and %q both map to Go field %q; rename one", obj.Name, prev, f.Name, goField)
			}
			seen[goField] = f.Name
			goType, err := sporeTypeToGoResolvedChecked(f.Type, aliasMap, fmt.Sprintf("%s.%s", obj.Name, f.Name))
			if err != nil {
				return "", err
			}
			if f.Optional && isScalarType(f.Type) {
				goType = "*" + goType
			}
			jsonTag := f.Name
			if f.Optional {
				jsonTag += ",omitempty"
			}
			sb.WriteString(fmt.Sprintf("\t%s %s `json:\"%s\"`\n", goField, goType, jsonTag))
		}
		sb.WriteString("}\n")
	}

	// Generate type alias definitions.
	for _, alias := range app.TypeAliases {
		goTarget := sporeTypeRefToGo(alias.Target)
		sb.WriteString(fmt.Sprintf("\ntype %s = %s\n", alias.Name, goTarget))
	}

	return sb.String(), nil
}

// isScalarType checks if a TypeDesc represents a scalar type.
func isScalarType(t schema.TypeDesc) bool {
	return t.Kind == schema.TypeKindScalar
}

// sporeTypeToGo converts a spore schema TypeDesc to a Go type string.
func sporeTypeToGo(t schema.TypeDesc) string {
	switch t.Kind {
	case schema.TypeKindScalar:
		return sporeScalarToGo(t.Name)
	case schema.TypeKindStruct:
		return t.Name
	case schema.TypeKindArray:
		if t.Element != nil {
			inner := sporeTypeToGoSimple(*t.Element)
			return "[]" + inner
		}
		return "[]interface{}"
	case schema.TypeKindMap:
		if t.Key != nil && t.Value != nil {
			k := sporeTypeToGoSimple(*t.Key)
			v := sporeTypeToGoSimple(*t.Value)
			return fmt.Sprintf("map[%s]%s", k, v)
		}
		return "map[string]interface{}"
	default:
		return "interface{}"
	}
}

// sporeTypeToGoResolvedChecked is like sporeTypeToGoResolved but returns an
// error instead of silently degrading an unknown scalar to interface{}.
// appdef.Validate already rejects unknown scalars, but codegen is the belt:
// a programmatic caller (or a future drift between the spore script parser's
// scalar classification and the appdef allowlist) must surface a field-naming
// error here instead of producing a silently-wrong generated struct.
func sporeTypeToGoResolvedChecked(t schema.TypeDesc, aliases map[string]string, path string) (string, error) {
	if target, ok := aliases[t.Name]; ok {
		return sporeTypeRefToGoResolvedChecked(target, aliases, path)
	}
	switch t.Kind {
	case schema.TypeKindScalar:
		if !appdef.IsKnownScalar(t.Name) && t.Name != "" && t.Name != "void" {
			return "", fmt.Errorf("field %s: unknown scalar %q — supported scalars: string, bool, int, int32, int64, long, float (Go float32), double (Go float64), bytes, any, void; use double for 64-bit floats", path, t.Name)
		}
		return sporeScalarToGo(t.Name), nil
	case schema.TypeKindStruct:
		return t.Name, nil
	case schema.TypeKindArray:
		if t.Element != nil {
			inner, err := sporeTypeToGoSimpleResolvedChecked(*t.Element, aliases, path+"[]")
			if err != nil {
				return "", err
			}
			return "[]" + inner, nil
		}
		return "[]interface{}", nil
	case schema.TypeKindMap:
		if t.Key != nil && t.Value != nil {
			k, err := sporeTypeToGoSimpleResolvedChecked(*t.Key, aliases, path+"[key]")
			if err != nil {
				return "", err
			}
			v, err := sporeTypeToGoSimpleResolvedChecked(*t.Value, aliases, path+"[val]")
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("map[%s]%s", k, v), nil
		}
		return "map[string]interface{}", nil
	default:
		return "interface{}", nil
	}
}

func sporeTypeRefToGoResolvedChecked(target string, aliases map[string]string, path string) (string, error) {
	target = strings.TrimSpace(target)
	if t, ok := aliases[target]; ok {
		return sporeTypeRefToGoResolvedChecked(t, aliases, path)
	}
	if strings.HasPrefix(target, "array<") && strings.HasSuffix(target, ">") {
		inner := target[len("array<") : len(target)-1]
		r, err := sporeTypeRefToGoResolvedChecked(inner, aliases, path+"[]")
		if err != nil {
			return "", err
		}
		return "[]" + r, nil
	}
	if strings.HasPrefix(target, "map<") && strings.HasSuffix(target, ">") {
		inner := target[len("map<") : len(target)-1]
		parts := strings.SplitN(inner, ",", 2)
		if len(parts) == 2 {
			k, err := sporeTypeRefToGoResolvedChecked(strings.TrimSpace(parts[0]), aliases, path+"[key]")
			if err != nil {
				return "", err
			}
			v, err := sporeTypeRefToGoResolvedChecked(strings.TrimSpace(parts[1]), aliases, path+"[val]")
			if err != nil {
				return "", err
			}
			return "map[" + k + "]" + v, nil
		}
		return "map[string]interface{}", nil
	}
	if !appdef.IsKnownScalar(target) && target != "" && target != "void" {
		// Struct/alias reference — use as-is. Scalar validation already
		// ensured unknown lowercase scalars failed at Validate time; a
		// capitalized name here is a struct reference.
		return target, nil
	}
	return sporeScalarToGo(target), nil
}

func sporeTypeToGoSimpleResolvedChecked(t schema.TypeDesc, aliases map[string]string, path string) (string, error) {
	if target, ok := aliases[t.Name]; ok {
		return sporeTypeRefToGoResolvedChecked(target, aliases, path)
	}
	switch t.Kind {
	case schema.TypeKindScalar:
		if !appdef.IsKnownScalar(t.Name) && t.Name != "" && t.Name != "void" {
			return "", fmt.Errorf("field %s: unknown scalar %q — see supported scalars in dev_guide", path, t.Name)
		}
		return sporeScalarToGo(t.Name), nil
	case schema.TypeKindStruct:
		return t.Name, nil
	case schema.TypeKindArray:
		if t.Element != nil {
			inner, err := sporeTypeToGoSimpleResolvedChecked(*t.Element, aliases, path+"[]")
			if err != nil {
				return "", err
			}
			return "[]" + inner, nil
		}
		return "[]interface{}", nil
	case schema.TypeKindMap:
		if t.Key != nil && t.Value != nil {
			k, err := sporeTypeToGoSimpleResolvedChecked(*t.Key, aliases, path+"[key]")
			if err != nil {
				return "", err
			}
			v, err := sporeTypeToGoSimpleResolvedChecked(*t.Value, aliases, path+"[val]")
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("map[%s]%s", k, v), nil
		}
		return "map[string]interface{}", nil
	default:
		return "interface{}", nil
	}
}

func sporeTypeToGoResolved(t schema.TypeDesc, aliases map[string]string) string {
	if target, ok := aliases[t.Name]; ok {
		return sporeTypeRefToGoResolved(target, aliases)
	}
	switch t.Kind {
	case schema.TypeKindScalar:
		return sporeScalarToGo(t.Name)
	case schema.TypeKindStruct:
		return t.Name
	case schema.TypeKindArray:
		if t.Element != nil {
			inner := sporeTypeToGoSimpleResolved(*t.Element, aliases)
			return "[]" + inner
		}
		return "[]interface{}"
	case schema.TypeKindMap:
		if t.Key != nil && t.Value != nil {
			k := sporeTypeToGoSimpleResolved(*t.Key, aliases)
			v := sporeTypeToGoSimpleResolved(*t.Value, aliases)
			return fmt.Sprintf("map[%s]%s", k, v)
		}
		return "map[string]interface{}"
	default:
		return "interface{}"
	}
}

// sporeTypeToGoSimpleResolved is like sporeTypeToGoSimple but with alias
// resolution for inner element types.
func sporeTypeToGoSimpleResolved(t schema.TypeDesc, aliases map[string]string) string {
	if target, ok := aliases[t.Name]; ok {
		return sporeTypeRefToGoResolved(target, aliases)
	}
	switch t.Kind {
	case schema.TypeKindScalar:
		return sporeScalarToGo(t.Name)
	case schema.TypeKindStruct:
		return t.Name
	case schema.TypeKindArray:
		if t.Element != nil {
			return "[]" + sporeTypeToGoSimpleResolved(*t.Element, aliases)
		}
		return "[]interface{}"
	case schema.TypeKindMap:
		if t.Key != nil && t.Value != nil {
			k := sporeTypeToGoSimpleResolved(*t.Key, aliases)
			v := sporeTypeToGoSimpleResolved(*t.Value, aliases)
			return fmt.Sprintf("map[%s]%s", k, v)
		}
		return "map[string]interface{}"
	default:
		return "interface{}"
	}
}

// sporeTypeToGoSimple is like sporeTypeToGo but for inner types (no pointer).
func sporeTypeToGoSimple(t schema.TypeDesc) string {
	switch t.Kind {
	case schema.TypeKindScalar:
		return sporeScalarToGo(t.Name)
	case schema.TypeKindStruct:
		return t.Name
	case schema.TypeKindArray:
		if t.Element != nil {
			return "[]" + sporeTypeToGoSimple(*t.Element)
		}
		return "[]interface{}"
	case schema.TypeKindMap:
		if t.Key != nil && t.Value != nil {
			k := sporeTypeToGoSimple(*t.Key)
			v := sporeTypeToGoSimple(*t.Value)
			return fmt.Sprintf("map[%s]%s", k, v)
		}
		return "map[string]interface{}"
	default:
		return "interface{}"
	}
}

// sporeScalarToGo maps spore scalar names to Go types.
func sporeScalarToGo(name string) string {
	switch name {
	case "string":
		return "string"
	case "bool":
		return "bool"
	case "int", "int32":
		return "int32"
	case "int64", "long":
		return "int64"
	case "float":
		return "float32"
	case "double":
		return "float64"
	case "bytes":
		return "[]byte"
	case "any":
		return "interface{}"
	default:
		return "interface{}"
	}
}

// sporeTypeRefToGo converts a type reference string (possibly composite like
// array<T> or map<K,V>) to a Go type string.
func sporeTypeRefToGo(ref string) string {
	return sporeTypeRefToGoResolved(ref, nil)
}

// sporeTypeRefToGoResolved is like sporeTypeRefToGo but recursively resolves
// alias references: a bare name matching a declared alias expands to its
// target before conversion.
func sporeTypeRefToGoResolved(ref string, aliases map[string]string) string {
	ref = strings.TrimSpace(ref)
	if target, ok := aliases[ref]; ok {
		return sporeTypeRefToGoResolved(target, aliases)
	}
	if strings.HasPrefix(ref, "array<") && strings.HasSuffix(ref, ">") {
		inner := ref[len("array<") : len(ref)-1]
		return "[]" + sporeTypeRefToGoResolved(inner, aliases)
	}
	if strings.HasPrefix(ref, "map<") && strings.HasSuffix(ref, ">") {
		inner := ref[len("map<") : len(ref)-1]
		parts := strings.SplitN(inner, ",", 2)
		if len(parts) == 2 {
			return "map[" + sporeTypeRefToGoResolved(strings.TrimSpace(parts[0]), aliases) + "]" + sporeTypeRefToGoResolved(strings.TrimSpace(parts[1]), aliases)
		}
		return "map[string]interface{}"
	}
	switch ref {
	case "string", "bool", "int", "int32", "int64", "long", "float", "double", "bytes", "any":
		return sporeScalarToGo(ref)
	}
	// Struct reference — use as-is.
	return ref
}

// generateClientTS produces client.gen.ts: a self-contained, dependency-free
// frontend client for the gateway data path. The panel iframe loads through
// the host gateway (/plugin/{id}), which reverse-proxies to the plugin's
// HTTP listener; an async appBase() gate awaits window.__sporemindAppBaseReady
// before building any URL, invoke wrappers POST JSON to {base}/invoke/{id},
// and event subscriptions consume the {base}/events SSE stream — no host
// bridge relays data. Management-plane
// signals (console, DOM snapshot, openView, theme) still travel the host
// bridge separately. Callables declared expose: "agent" get no frontend
// wrapper here; events are always frontend-facing.
func generateClientTS(app *appdef.AppDef) string {
	var sb strings.Builder
	sb.WriteString(`// client.gen.ts — generated from appdef. DO NOT EDIT.
//
// Direct-HTTP data path: the plugin process serves this page same-origin, so
// every call below is a plain fetch/EventSource against the plugin's own
// listener (POST /invoke/{id}, GET /events SSE, static files). There is no
// base64 or binary codec on this path — large media goes over HTTP-native
// binary via uploadFile/streamVideo at the bottom of this file.
//
// Mount base: under the gateway the panel iframe loads from
// /plugin/{id}/ and the host shell injects window.__sporemindAppBase
// ("/plugin/{id}"); standalone dev leaves it unset ("") and URLs stay
// root-relative. appBase()/withAppBase() below apply the prefix, and every
// data call first awaits the __sporemindAppBaseReady handshake gate the
// injected snippet exposes — so a first fetch on page load cannot race the
// handshake and 404 at the gateway root.
`)

	// ── Invoke ──
	sb.WriteString(`
// appBase resolves the mount base the host shell injects when the panel
// iframe is served through the gateway under /plugin/{id}/. Standalone dev
// (direct plugin listener) leaves it unset, so URLs stay root-relative. The
// value never carries a trailing slash: "" or "/plugin/{id}".
//
// Race gate: the shell injects the base via the postMessage bootstrap
// handshake, which can land AFTER this script runs. The injected snippet
// exposes window.__sporemindAppBaseReady — resolved when the handshake
// arrives (or "" after a bounded fallback) — and every data call below
// awaits it before building a URL, so the first fetch cannot 404 against
// the gateway root. Standalone dev pages without the snippet resolve
// immediately with the current global.
let appBaseGate: Promise<void> | null = null;
function ensureAppBaseReady(): Promise<void> {
	const w = window as any;
	if (!appBaseGate) {
		appBaseGate = w.__sporemindAppBaseReady instanceof Promise
			? w.__sporemindAppBaseReady.then(() => undefined)
			: Promise.resolve();
	}
	return appBaseGate;
}

async function appBase(): Promise<string> {
	await ensureAppBaseReady();
	return (window as any).__sporemindAppBase || "";
}

// withAppBase prefixes same-origin app paths ("/media/...") with the mount
// base; absolute URLs pass through untouched.
async function withAppBase(url: string): Promise<string> {
	return url.startsWith("/") ? (await appBase()) + url : url;
}

async function invoke<T>(callID: string, args: Record<string, unknown>): Promise<T> {
	const res = await fetch((await appBase()) + "/invoke/" + encodeURIComponent(callID), {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		credentials: "same-origin",
		body: JSON.stringify(args ?? {}),
	});
	if (!res.ok) {
		let detail = res.statusText;
		try {
			detail = ((await res.json()) as { error?: string }).error ?? detail;
		} catch {
			// non-JSON error body: keep statusText
		}
		throw new Error("invoke " + callID + " failed: " + res.status + " " + detail);
	}
	return (await res.json()) as T;
}

// invokeStream is the streaming-adjacent shape kept for source compatibility
// with streaming callables: the HTTP invoke route is unary, so
// intermediate chunks are NOT relayed (the server-side emit drops them).
// The promise resolves with the terminal response. Model stream progress
// with app events over SSE (on<Event> below) instead of chunk callbacks.
async function invokeStream<T, C>(callID: string, args: Record<string, unknown>, _onChunk: (c: C) => void): Promise<T> {
	return invoke<T>(callID, args);
}
`)

	// ── Event subscription (GET /events: WebSocket first, SSE fallback) ──
	sb.WriteString(`
// ── Event subscription (GET /events: shared WebSocket, SSE fallback) ──
//
// Why WebSocket: the desktop SPA embeds one iframe per plugin panel, all
// pointing at the same gateway origin, and browsers cap same-pool HTTP/1.1
// connections at 6 per host — a permanent SSE stream per panel starves every
// transient invoke (2026-09-14 incident). WebSocket connections live outside
// that pool. If the upgrade handshake is refused (an older SDK answers a
// plain 200 stream), the first dial fails without ever opening and we degrade
// to EventSource for this page lifetime — never worse than before.
//
// The connection is a window-level singleton keyed by URL
// (window.__sporemindEventChannels) shared with the host-injected bridge
// client, which subscribes to the same event stream for the legacy
// sporemind.subscribe surface: whichever module runs first creates the
// channel, the other reuses it, and one panel never holds two connections
// to the same URL.
type EventHandler = (payload: unknown) => void;

const eventHandlers = new Map<string, Set<EventHandler>>();

function dispatchEvent(eventKind: string, payload: unknown) {
	eventHandlers.get(eventKind)?.forEach((h) => h(payload));
}

interface EventChannel {
	subscribe(eventKind: string, cb: (eventKind: string, payload: unknown) => void): () => void;
	/** Host-driven suspend: close any live connection and stop dialing while
	 * the panel is hidden; the channel and its subscribers stay intact. */
	suspend(): void;
	/** Resume after a suspend: re-dial if anyone still subscribes. */
	resume(): void;
}

function eventChannel(url: string): EventChannel {
	const w = window as any;
	if (!w.__sporemindEventChannels) w.__sporemindEventChannels = new Map();
	const existing = w.__sporemindEventChannels.get(url);
	if (existing) return existing as EventChannel;
	const ch = createEventChannel(url);
	w.__sporemindEventChannels.set(url, ch);
	return ch;
}

function eventBackoff(attempt: number): number {
	const exp = Math.min(30000, 500 * Math.pow(2, Math.min(attempt, 6)));
	return exp * (0.7 + 0.6 * Math.random());
}

function createEventChannel(url: string): EventChannel {
	const subs = new Map<string, Set<(eventKind: string, payload: unknown) => void>>();
	let stopped = false;
	let fellBack = false;
	let suspended = false;
	let everOpened = false;
	let attempt = 0;
	let ws: WebSocket | null = null;
	let es: EventSource | null = null;
	const attachedKinds = new Set<string>();

	const wsUrl = url.startsWith("/")
		? (location.protocol === "https:" ? "wss:" : "ws:") + "//" + location.host + url
		: url.replace(/^http/, "ws");

	const teardown = () => {
		stopped = true;
		fellBack = true;
		ws?.close();
		es?.close();
		const w = window as any;
		w.__sporemindEventChannels?.delete(url);
	};

	const attachSseKind = (target: EventSource, eventKind: string) => {
		if (attachedKinds.has(eventKind)) return;
		attachedKinds.add(eventKind);
		target.addEventListener(eventKind, (ev) => {
			const msg = ev as MessageEvent;
			let payload: unknown;
			if (typeof msg.data === "string" && msg.data !== "") {
				try {
					payload = JSON.parse(msg.data);
				} catch {
					payload = msg.data;
				}
			}
			subs.get(eventKind)?.forEach((cb) => cb(eventKind, payload));
		});
	};

	// dial connects the WS channel with capped exponential backoff and
	// jitter so a dead backend never produces a reconnect storm.
	const dial = () => {
		if (stopped || fellBack || suspended) return;
		let sock: WebSocket;
		try {
			sock = new WebSocket(wsUrl);
		} catch {
			attempt++;
			setTimeout(dial, eventBackoff(attempt));
			return;
		}
		ws = sock;
		sock.onopen = () => {
			everOpened = true;
			attempt = 0;
		};
		sock.onmessage = (ev) => {
			try {
				const env = JSON.parse(String(ev.data)) as { event?: string; data?: unknown };
				if (env.event) {
					subs.get(env.event)?.forEach((cb) => cb(env.event!, env.data));
					subs.get("*")?.forEach((cb) => cb(env.event!, env.data));
				}
			} catch {
				// Malformed frame: skip.
			}
		};
		sock.onclose = () => {
			if (stopped || suspended) return;
			// First dial failing without ever opening means the handshake was
			// refused (older SDK): degrade to SSE for this page lifetime.
			if (!everOpened && attempt === 0) {
				fellBack = true;
				es = new EventSource(url, { withCredentials: true });
				for (const kind of subs.keys()) attachSseKind(es, kind);
				return;
			}
			attempt++;
			setTimeout(dial, eventBackoff(attempt));
		};
	};
	dial();

	return {
		suspend() {
			if (stopped || suspended) return;
			suspended = true;
			if (ws) {
				// Clear handlers before close: an in-flight frame can still land
				// between close() and the socket actually dying.
				ws.onmessage = null;
				ws.onclose = null;
				ws.close();
			}
			es?.close();
		},
		resume() {
			if (stopped || !suspended) return;
			suspended = false;
			if (fellBack) {
				// The SSE fallback never reconnects natively: rebuild it with the
				// currently-subscribed kinds.
				es = new EventSource(url, { withCredentials: true });
				for (const kind of subs.keys()) attachSseKind(es, kind);
				return;
			}
			everOpened = false;
			attempt = 0;
			if (subs.size > 0) dial();
		},
		subscribe(eventKind: string, cb: (eventKind: string, payload: unknown) => void) {
			let set = subs.get(eventKind);
			if (!set) {
				set = new Set();
				subs.set(eventKind, set);
				if (es) attachSseKind(es, eventKind);
			}
			set.add(cb);
			return () => {
				set!.delete(cb);
				if (set!.size === 0) {
					subs.delete(eventKind);
					if (subs.size === 0) teardown();
				}
			};
		},
	};
}

function ensureEvents() {
	void appBase().then((base) => {
		// Await the appBase gate: subscribing on module load races the
		// bootstrap handshake, and a root-relative /events URL 404s against
		// the gateway. An empty base (standalone dev open) is valid: both
		// transports then build URLs from location.host. The "*" subscription
		// receives every envelope; per-kind filtering happens in
		// dispatchEvent.
		eventChannel(base + "/events").subscribe("*", dispatchEvent);
	});
}

function subscribeEvent(eventKind: string, cb: EventHandler): () => void {
	let set = eventHandlers.get(eventKind);
	if (!set) {
		set = new Set();
		eventHandlers.set(eventKind, set);
		ensureEvents();
	}
	set.add(cb);
	return () => {
		set!.delete(cb);
		if (set!.size === 0) {
			// Keep the shared channel alive across idle periods: closing
			// it would throw away reconnect state and race in-flight dials.
			// The channel refcounts and closes itself when its last
			// subscriber (including the bridge client) leaves.
			eventHandlers.delete(eventKind);
		}
	};
}
`)

	// Generate TS interfaces for structs.
	for _, obj := range app.Structs {
		sb.WriteString(fmt.Sprintf("export interface %s {\n", obj.Name))
		for _, f := range obj.Fields {
			tsType := sporeTypeToTS(f.Type)
			opt := ""
			if f.Optional {
				opt = "?"
			}
			sb.WriteString(fmt.Sprintf("  %s%s: %s;\n", f.Name, opt, tsType))
		}
		sb.WriteString("}\n\n")
	}

	// Generate TS type aliases.
	for _, alias := range app.TypeAliases {
		tsTarget := sporeTypeRefToTS(alias.Target)
		sb.WriteString(fmt.Sprintf("export type %s = %s;\n", alias.Name, tsTarget))
	}

	// Generate invoke wrappers for frontend-exposed callables only
	// (expose: agent is LLM-facing and gets no wrapper). Streaming callables
	// keep the onChunk shape via invokeStream (terminal-only, see above).
	for _, c := range app.Callables {
		if !callableExposedToFrontend(c) {
			continue
		}
		fnName := CallableIDToTSFuncName(c.ID)
		reqType := c.Request
		if reqType == "" {
			reqType = "void"
		}
		respType := c.Response
		if respType == "" {
			respType = "void"
		}
		tsRespType := sporeTypeRefToTS(respType)

		if c.Streaming {
			sb.WriteString(fmt.Sprintf(
				"export function %s(req: %s, onChunk: (c: %s) => void): Promise<%s> {\n\treturn invokeStream<%s, %s>(%q, req as Record<string, unknown>, onChunk);\n}\n\n",
				fnName, reqType, tsRespType, tsRespType, tsRespType, tsRespType, c.ID,
			))
			continue
		}
		sb.WriteString(fmt.Sprintf(
			"export async function %s(req: %s): Promise<%s> {\n\treturn invoke<%s>(%q, req as Record<string, unknown>);\n}\n\n",
			fnName, reqType, tsRespType, tsRespType, c.ID,
		))
	}

	// Event subscription wrappers: onXxx(cb) → subscribeEvent("<event_id>", cb)
	// over the shared SSE connection. Void events call back with no argument.
	for _, ev := range app.Events {
		fnName := EventIDToTSFuncName(ev.ID)
		if ev.Payload == "" || ev.Payload == "void" {
			sb.WriteString(fmt.Sprintf(
				"export function %s(cb: () => void): () => void {\n\treturn subscribeEvent(%q, () => cb());\n}\n\n",
				fnName, ev.ID,
			))
			continue
		}
		payloadType := sporeTypeRefToTS(ev.Payload)
		sb.WriteString(fmt.Sprintf(
			"export function %s(cb: (p: %s) => void): () => void {\n\treturn subscribeEvent(%q, (raw) => cb(raw as %s));\n}\n\n",
			fnName, payloadType, ev.ID, payloadType,
		))
	}

	// Watched-list hooks: one per frontend-exposed callable that declares
	// watch events. Framework-agnostic (no React import): call once to fetch
	// immediately and refetch whenever a watched event arrives over SSE; the
	// returned function unsubscribes.
	watched := watchedFrontendCallables(app)
	if len(watched) > 0 {
		sb.WriteString(`// ── Watched-list hooks (watch field: fetch + refetch on watched events) ──
//
// Framework-agnostic by design: each hook fetches immediately and refetches
// whenever one of the callable's watch events arrives over SSE. Pass an
// onChange callback (e.g. setState in React); call the returned function to
// unsubscribe and stop refetching.
export interface WatchedQuery<T> {
	data: T | null;
	loading: boolean;
	error: string | null;
}

`)
		for _, c := range watched {
			fnName := CallableIDToTSFuncName(c.ID)
			hookName := "use" + strings.ToUpper(fnName[:1]) + fnName[1:] + "List"
			reqType := c.Request
			if reqType == "" {
				reqType = "void"
			}
			respType := c.Response
			if respType == "" {
				respType = "void"
			}
			tsRespType := sporeTypeRefToTS(respType)
			watchKinds := make([]string, len(c.Watch))
			for i, w := range c.Watch {
				watchKinds[i] = fmt.Sprintf("%q", w)
			}
			watchArr := "[" + strings.Join(watchKinds, ", ") + "]"
			sb.WriteString(fmt.Sprintf(
				`// %s: fetch %s immediately and refetch when one of %s fires.
export function %s(req: %s, onChange: (s: WatchedQuery<%s>) => void): () => void {
	const kinds = %s;
	let disposed = false;
	const emit = (partial: Partial<WatchedQuery<%s>>) => {
		if (!disposed) onChange({ data: null, loading: true, error: null, ...partial });
	};
	const refetch = () => {
		emit({ loading: true, error: null });
		%s(req as Record<string, unknown>)
			.then((data) => emit({ data, loading: false }))
			.catch((e) => emit({ loading: false, error: String(e) }));
	};
	refetch();
	const unsubs = kinds.map((kind) => subscribeEvent(kind, () => refetch()));
	return () => {
		disposed = true;
		unsubs.forEach((u) => u());
	};
}

`,
				hookName, c.ID, strings.Join(c.Watch, ", "),
				hookName, reqType, tsRespType,
				watchArr,
				tsRespType,
				fnName,
			))
		}
	}

	// ── HTTP-native binary helpers ──
	sb.WriteString(`// ── Binary helpers (HTTP-native, no base64/codec) ──
//
// Large media travels as raw HTTP bodies, not JSON: the session cookie is
// same-origin (HttpOnly, SameSite=Lax), so fetch, <video>, <audio> and <img>
// all carry it automatically.

// uploadFile POSTs a File/Blob as the raw request body to an app HTTP
// endpoint. When onProgress is given it uses XMLHttpRequest (fetch has no
// upload-progress event); otherwise it uses fetch. Returns the raw Response
// so callers can inspect status/headers.
export async function uploadFile(
	url: string,
	file: Blob,
	opts: { method?: string; headers?: Record<string, string>; onProgress?: (sent: number, total: number) => void } = {},
): Promise<Response> {
	url = await withAppBase(url);
	if (!opts.onProgress) {
		const res = await fetch(url, {
			method: opts.method ?? "POST",
			headers: opts.headers ?? {},
			credentials: "same-origin",
			body: file,
		});
		if (!res.ok) {
			throw new Error("upload to " + url + " failed: " + res.status + " " + res.statusText);
		}
		return res;
	}
	return new Promise<Response>((resolve, reject) => {
		const xhr = new XMLHttpRequest();
		xhr.open(opts.method ?? "POST", url);
		xhr.withCredentials = true;
		for (const [k, v] of Object.entries(opts.headers ?? {})) {
			xhr.setRequestHeader(k, v);
		}
		xhr.upload.onprogress = (ev) => {
			if (ev.lengthComputable) opts.onProgress!(ev.loaded, ev.total);
		};
		xhr.onload = () => {
			if (xhr.status >= 200 && xhr.status < 300) {
				resolve(new Response(xhr.response as ArrayBuffer, { status: xhr.status, statusText: xhr.statusText }));
			} else {
				reject(new Error("upload to " + url + " failed: " + xhr.status + " " + xhr.statusText));
			}
		};
		xhr.onerror = () => reject(new Error("upload to " + url + " failed: network error"));
		xhr.send(file);
	});
}

// streamVideo feeds an HTTP response (chunked or static) into a <video>
// element through Media Source Extensions, so playback starts while the body
// is still downloading. mimeType must match the container/codec actually
// served (e.g. "video/mp4; codecs=avc1.42E01E" for H.264 MP4).
export async function streamVideo(url: string, videoEl: HTMLVideoElement, mimeType = "video/mp4"): Promise<void> {
	url = await withAppBase(url);
	const mediaSource = new MediaSource();
	videoEl.src = URL.createObjectURL(mediaSource);
	await new Promise<void>((resolve) => {
		if (mediaSource.readyState === "open") {
			resolve();
		} else {
			mediaSource.addEventListener("sourceopen", () => resolve(), { once: true });
		}
	});
	const sourceBuffer = mediaSource.addSourceBuffer(mimeType);
	const res = await fetch(url, { credentials: "same-origin" });
	if (!res.ok || !res.body) {
		URL.revokeObjectURL(videoEl.src);
		throw new Error("stream " + url + " failed: " + res.status + " " + res.statusText);
	}
	const reader = res.body.getReader();
	try {
		for (;;) {
			const { done, value } = await reader.read();
			if (done) {
				break;
			}
			await new Promise<void>((resolve, reject) => {
				sourceBuffer.addEventListener("updateend", () => resolve(), { once: true });
				sourceBuffer.addEventListener("error", () => reject(new Error("MSE append failed")), { once: true });
				sourceBuffer.appendBuffer(value);
			});
		}
		if (mediaSource.readyState === "open") {
			mediaSource.endOfStream();
		}
	} finally {
		reader.releaseLock();
	}
}
`)

	return sb.String()
}

// watchedFrontendCallables returns the callables that get a useXxxList hook:
// frontend-exposed and declaring at least one watch event.
func watchedFrontendCallables(app *appdef.AppDef) []appdef.CallableDecl {
	var out []appdef.CallableDecl
	for _, c := range app.Callables {
		if callableExposedToFrontend(c) && len(c.Watch) > 0 {
			out = append(out, c)
		}
	}
	return out
}

// normalizeTypeRef converts a type reference to a canonical form.
// e.g. "array<TodoItem>" becomes "TodoItem[]"
func normalizeTypeRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, "array<") && strings.HasSuffix(ref, ">") {
		inner := ref[len("array<") : len(ref)-1]
		return normalizeTypeRef(inner) + "[]"
	}
	return ref
}

// sporeTypeToTS converts a spore schema TypeDesc to a TypeScript type string.
func sporeTypeToTS(t schema.TypeDesc) string {
	switch t.Kind {
	case schema.TypeKindScalar:
		return sporeScalarToTS(t.Name)
	case schema.TypeKindStruct:
		return t.Name
	case schema.TypeKindArray:
		if t.Element != nil {
			inner := sporeTypeRefToTS(t.Element.Name)
			return inner + "[]"
		}
		return "any[]"
	case schema.TypeKindMap:
		return "{ [key: string]: any }"
	default:
		return "any"
	}
}

// sporeScalarToTS maps spore scalar names to TypeScript types.
func sporeScalarToTS(name string) string {
	switch name {
	case "string":
		return "string"
	case "bool":
		return "boolean"
	case "int", "int32", "int64", "long", "float", "double":
		return "number"
	case "bytes":
		return "Uint8Array"
	case "any":
		return "any"
	default:
		return "any"
	}
}

// sporeTypeRefToTS converts a type reference (possibly composite like
// array<T>) to a TypeScript type string.
func sporeTypeRefToTS(ref string) string {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, "array<") && strings.HasSuffix(ref, ">") {
		inner := ref[len("array<") : len(ref)-1]
		return sporeTypeRefToTS(inner) + "[]"
	}
	if ref == "void" || ref == "" {
		return "void"
	}
	switch ref {
	case "string":
		return "string"
	case "bool":
		return "boolean"
	case "int", "int32", "int64", "long", "float", "double":
		return "number"
	case "bytes":
		return "Uint8Array"
	case "any":
		return "any"
	}
	// Struct reference or unknown.
	return ref
}

// --- Utilities ---

// CallableIDToHandlerName converts a callable ID like "list_todos" to
// "handleListTodos".
func CallableIDToHandlerName(id string) string {
	parts := strings.Split(id, "_")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return "handle" + strings.Join(parts, "")
}

// EventKindToHandlerName converts a host event kind like "app_lifecycle"
// to the OnXxx handler name "OnAppLifecycle". Appends "Event" when the kind
// would collide with a callable handler (impossible today since callable
// handlers carry the "handle" prefix, but the guard documents the namespace).
func EventKindToHandlerName(kind string) string {
	return "On" + EventIDToGoFuncName(kind)
}

// EventIDToGoFuncName converts an event ID like "todo_changed" to the Exported
// Go suffix "TodoChanged" — shared by the OnXxx listen stubs and the EmitXxx
// publish wrappers so both faces of the same event carry one name.
func EventIDToGoFuncName(id string) string {
	parts := strings.Split(id, "_")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, "")
}

// CallableIDToTSFuncName converts a callable ID to a camelCase TS function name.
func CallableIDToTSFuncName(id string) string {
	parts := strings.Split(id, "_")
	for i := range parts {
		if i == 0 {
			continue
		}
		if parts[i] == "" {
			continue
		}
		parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
	}
	return strings.Join(parts, "")
}

// EventIDToTSFuncName converts an event ID like "todo_changed" to the TS
// subscription wrapper name "onTodoChanged" (camelCase with the on prefix).
func EventIDToTSFuncName(id string) string {
	camel := CallableIDToTSFuncName(id)
	if camel == "" {
		return "on"
	}
	return "on" + strings.ToUpper(camel[:1]) + camel[1:]
}

// writeFile writes content to the given path. The directory is created when
// missing: dev_generate may target a fresh AppDir the caller never mkdir'ed
// (Template scaffold into a new subdirectory), and Windows fails the open
// with "The system cannot find the path specified" instead of creating it.
func writeFile(dir, name, content string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
}

// contentHash computes SHA-256 hex of content.
func contentHash(content string) string {
	h := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", h)
}

// normalizeLineEndings converts CRLF to LF, matching the filesystem actor's
// project.read normalization. The .appdef drift hash must be computed on the
// normalized form so dev_generate (raw bytes) and dev_gate (project.read) agree.
// NormalizeLineEndings converts CRLF to LF. The .appdef drift hash must be
// computed on this form by every consumer (dev_generate hashes raw bytes at
// generate time, dev_gate hashes bytes fetched via project.read_base64) so
// line-ending differences cannot masquerade as content drift.
func NormalizeLineEndings(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// ensureGoMod creates or updates the project's go.mod with the SDK require,
// the SDK and spore replace directives, and the go directive matching the SDK.
// It then runs `go mod tidy` so that go.sum is kept up to date.
// Existing user-added dependencies are preserved.
//
// When the SDK is vendored (replace => ./vendor-sdk), spore path resolution
// is skipped — the vendored SDK has zero external dependencies and does not
// require a spore replace directive.
func ensureGoMod(projectDir, moduleID, sdkPath string) error {
	goModPath := filepath.Join(projectDir, FileGoMod)

	// Restore a missing vendor-sdk tree when go.mod is already vendored:
	// committed repos carry only the relative ./vendor-sdk replace, so any
	// host regenerating the app re-materializes the SDK copy locally.
	if err := materializeVendoredSDKIfNeeded(projectDir, sdkPath); err != nil {
		return fmt.Errorf("materialize vendored sdk: %w", err)
	}

	goVersion, err := readSDKGoVersion(sdkPath)
	if err != nil {
		return fmt.Errorf("read SDK go version: %w", err)
	}

	// Resolve spore path — non-fatal when the SDK is vendored (no spore
	// dependency). The vendored SDK has zero external deps, so there is no
	// spore replace directive to add.
	relSporePath := ""
	sporePath, err := resolveSporePath(sdkPath)
	if err == nil {
		relSporePath, err = portableRelPath(projectDir, sporePath)
		if err != nil {
			return fmt.Errorf("compute spore relative path: %w", err)
		}
		// The spore dependency may require a newer Go than the SDK; `go mod
		// tidy` would bump the directive anyway, so emit the max upfront to
		// keep the written go.mod stable and never below what deps require.
		if sporeVer, verr := readGoDirective(filepath.Join(sporePath, FileGoMod)); verr == nil {
			goVersion = maxGoVersion(goVersion, sporeVer)
		}
	}

	relSDKPath, err := portableRelPath(projectDir, sdkPath)
	if err != nil {
		return fmt.Errorf("compute SDK relative path: %w", err)
	}

	content, modified, err := ensureGoModContent(projectDir, goModPath, moduleID, goVersion, relSDKPath, relSporePath)
	if err != nil {
		return err
	}

	if modified {
		if err := os.WriteFile(goModPath, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write go.mod: %w", err)
		}
	}

	// Best-effort go mod tidy: generates go.sum if possible. The native build
	// path (GOFLAGS=-mod=mod) will also generate/update go.sum on demand, so
	// a tidy failure (e.g. unresolvable user-added deps) is non-fatal.
	_ = runGoModTidy(projectDir)
	return nil
}

// ensureGoModContent parses an existing go.mod or builds a fresh one, ensuring
// it contains the module declaration, the SDK require, both replace
// directives, and the go directive. It returns the file content, whether it
// changed, and any error.
func ensureGoModContent(projectDir, goModPath, moduleID, goVersion, relSDKPath, relSporePath string) (string, bool, error) {
	data, err := os.ReadFile(goModPath)
	if err != nil && !os.IsNotExist(err) {
		return "", false, fmt.Errorf("read go.mod: %w", err)
	}

	var lines []string
	var modified bool
	if os.IsNotExist(err) {
		lines = []string{
			"module " + moduleID,
			"",
			"go " + goVersion,
			"",
			"require github.com/qomos-w/sporemind-plugin-sdk v0.0.0",
			"",
			"replace github.com/qomos-w/sporemind-plugin-sdk => " + relSDKPath,
		}
		if relSporePath != "" {
			lines = append(lines, "", "replace github.com/qomos-w/spore => "+relSporePath)
		}
		return strings.Join(lines, "\n"), true, nil
	}

	content := string(data)
	lines = strings.Split(content, "\n")

	// Ensure module declaration exists (preserve existing module name if present).
	moduleIdx := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "module" && !strings.HasPrefix(trimmed, "module ") {
			continue
		}
		moduleIdx = i
		// A module directive with an empty name is invalid Go: it appears when
		// the first generate ran with an empty appdef id (before validation
		// rejected it). Rewrite it in place — prepending a second module line
		// instead produced a duplicate-module go.mod on the next regenerate
		// (observed in the round-5 external TOTP run).
		name := strings.TrimSpace(strings.TrimPrefix(trimmed, "module"))
		if name == "" {
			lines[i] = "module " + moduleID
			modified = true
		} else if name == templateAppID && moduleID != templateAppID {
			// Upgrade the template placeholder module name to the real app
			// id: a scaffold-then-rewrite flow left "app.default" behind,
			// which is misleading (round-6 external TOTP run).
			lines[i] = "module " + moduleID
			modified = true
		}
		break
	}
	if moduleIdx == -1 {
		lines = append([]string{"module " + moduleID}, lines...)
		if len(lines) > 1 && lines[1] != "" {
			lines = append([]string{lines[0], ""}, lines[1:]...)
		}
		modified = true
	}

	// Ensure the go directive satisfies the required version. Only raise,
	// never lower: a directive newer than required (user-managed or bumped
	// by go mod tidy for other deps) must not be downgraded.
	goIdx := -1
	goPrefix := "go "
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, goPrefix) && !strings.Contains(trimmed, "//") {
			goIdx = i
			version := strings.TrimSpace(strings.TrimPrefix(trimmed, goPrefix))
			if compareGoVersion(version, goVersion) < 0 {
				lines[i] = goPrefix + goVersion
				modified = true
			}
			break
		}
	}
	if goIdx == -1 {
		// Insert right after module declaration if possible.
		if moduleIdx != -1 && moduleIdx+1 < len(lines) {
			if lines[moduleIdx+1] != "" {
				lines = append(lines[:moduleIdx+1], append([]string{"", goPrefix + goVersion}, lines[moduleIdx+1:]...)...)
			} else {
				lines = append(lines[:moduleIdx+2], append([]string{goPrefix + goVersion}, lines[moduleIdx+2:]...)...)
			}
		} else {
			lines = append([]string{lines[0], "", goPrefix + goVersion}, lines[1:]...)
		}
		modified = true
	}

	// Ensure SDK require exists.
	if !strings.Contains(content, "require github.com/qomos-w/sporemind-plugin-sdk") {
		lines = append(lines, "", "require github.com/qomos-w/sporemind-plugin-sdk v0.0.0")
		modified = true
	}

	// Ensure SDK replace exists and points to the correct relative path.
	if updated, newLines := ensureReplaceDirective(lines, "github.com/qomos-w/sporemind-plugin-sdk", relSDKPath); updated {
		lines = newLines
		modified = true
	}

	// Ensure spore replace exists and points to the correct relative path.
	// When spore path resolution was unavailable (the SDK is dependency-free
	// since gen-sdk-schemas --no-registry-init), strip stale spore directives
	// instead: they point at host-local paths that do not exist in user
	// environments (e.g. ../../spore), which breaks builds elsewhere.
	if relSporePath != "" {
		if updated, newLines := ensureReplaceDirective(lines, "github.com/qomos-w/spore", relSporePath); updated {
			lines = newLines
			modified = true
		}
	} else {
		if changed := removeStaleSporeLines(&lines); changed {
			modified = true
		}
	}

	return strings.Join(lines, "\n"), modified, nil
}

// ensureReplaceDirective ensures a replace directive for module exists and
// points to targetPath. It returns whether the lines changed and the new lines.
//
// When the existing replace target is ./vendor-sdk (a vendored SDK), the
// directive is preserved as-is and never upgraded: the scaffold's vendored
// minimal ABI layer is intentionally self-contained, and replacing it with a
// dev/release SDK path would break the offline build.
func ensureReplaceDirective(lines []string, module, targetPath string) (bool, []string) {
	prefix := "replace " + module + " =>"
	trimmedTarget := strings.TrimSpace(targetPath)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) {
			current := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
			if current == "./vendor-sdk" {
				return false, lines
			}
			if current != trimmedTarget {
				lines[i] = prefix + " " + targetPath
				return true, lines
			}
			return false, lines
		}
	}
	lines = append(lines, "", prefix+" "+targetPath)
	return true, lines
}

// removeStaleSporeLines drops require/replace directives for the spore module
// (single-line form and parenthesized-block entries) from an existing go.mod.
// Used when the SDK has no spore dependency anymore: leftover directives point
// at host-local paths (e.g. ../../spore) that do not exist in user
// environments. Returns whether anything was removed.
func removeStaleSporeLines(lines *[]string) bool {
	out := make([]string, 0, len(*lines))
	changed := false
	for _, line := range *lines {
		if isSporeDirectiveLine(strings.TrimSpace(line)) {
			changed = true
			continue
		}
		out = append(out, line)
	}
	*lines = out
	return changed
}

func isSporeDirectiveLine(trimmed string) bool {
	const spore = "github.com/qomos-w/spore"
	for _, kw := range []string{"replace", "require"} {
		p := kw + " " + spore
		if trimmed == p || strings.HasPrefix(trimmed, p+" ") {
			return true
		}
	}
	// Entry inside a parenthesized replace/require block: "spore v0.0.0" or
	// "spore => ../spore". Bounded by a space so sporemind-plugin-sdk never
	// matches.
	return trimmed == spore || strings.HasPrefix(trimmed, spore+" ")
}

// readSDKGoVersion extracts the "go X.Y" directive from the SDK's go.mod.
func readSDKGoVersion(sdkPath string) (string, error) {
	return readGoDirective(filepath.Join(sdkPath, FileGoMod))
}

// readGoDirective extracts the "go X.Y(.Z)" directive from a go.mod file.
func readGoDirective(modPath string) (string, error) {
	data, err := os.ReadFile(modPath)
	if err != nil {
		return "", fmt.Errorf("read go.mod: %w", err)
	}
	prefix := "go "
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) && !strings.Contains(trimmed, "//") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, prefix)), nil
		}
	}
	return "", fmt.Errorf("no go directive found in %s", modPath)
}

// maxGoVersion returns the greater of two dotted Go version strings.
func maxGoVersion(a, b string) string {
	if compareGoVersion(a, b) >= 0 {
		return a
	}
	return b
}

// compareGoVersion compares dotted numeric Go versions ("1.25.0").
// Missing segments count as 0; non-numeric segments compare as 0.
func compareGoVersion(a, b string) int {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		av, bv := 0, 0
		if i < len(as) {
			av, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			bv, _ = strconv.Atoi(bs[i])
		}
		if av != bv {
			if av > bv {
				return 1
			}
			return -1
		}
	}
	return 0
}

// resolveSporePath reads the SDK's go.mod, resolves the spore replace
// directive relative to the SDK directory, and returns the absolute spore
// module path. If the resolved path does not exist (e.g. in a git worktree),
// it falls back to locating spore as a sibling of the sporemind repository.
//
// If the SDK's go.mod has no spore dependency (no require/replace for
// github.com/qomos-w/spore), the function returns an error immediately —
// the vendored minimal ABI layer has zero external dependencies.
func resolveSporePath(sdkPath string) (string, error) {
	data, err := os.ReadFile(filepath.Join(sdkPath, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("read SDK go.mod: %w", err)
	}

	// If the SDK has no spore dependency, there is nothing to resolve. Match
	// the module token exactly: the SDK's own module path
	// (github.com/qomos-w/sporemind-plugin-sdk) contains "…/spore" as a
	// prefix, so a naive substring check would always succeed.
	hasSporeDep := false
	for _, line := range strings.Split(string(data), "\n") {
		if isSporeDirectiveLine(strings.TrimSpace(line)) {
			hasSporeDep = true
			break
		}
	}
	if !hasSporeDep {
		return "", fmt.Errorf("SDK has no spore dependency (vendored)")
	}

	prefix := "replace github.com/qomos-w/spore =>"
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) {
			rel := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
			absPath, err := filepath.Abs(filepath.Join(sdkPath, rel))
			if err != nil {
				return "", fmt.Errorf("resolve spore replace: %w", err)
			}
			if _, err := os.Stat(filepath.Join(absPath, "go.mod")); err == nil {
				return absPath, nil
			}
			break
		}
	}

	// Fallback: walk up from the SDK to find the sporemind repo root (parent
	// of .git) and use the sibling spore directory.
	for dir := sdkPath; ; {
		gitDir := filepath.Join(dir, ".git")
		if info, err := os.Stat(gitDir); err == nil && info.IsDir() {
			repoRoot := dir
			candidate := filepath.Join(repoRoot, "..", "spore")
			candidate = filepath.Clean(candidate)
			if _, err := os.Stat(filepath.Join(candidate, "go.mod")); err == nil {
				return candidate, nil
			}
			return "", fmt.Errorf("spore sibling of repo %s not found", repoRoot)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", fmt.Errorf("unable to resolve spore path from SDK at %s", sdkPath)
}

// portableRelPath returns a path relative to base using forward slashes,
// suitable for go.mod replace directives across platforms. If the paths
// are on different volumes (e.g. C: and D: on Windows), it falls back to
// the absolute path with forward slashes.
func portableRelPath(base, target string) (string, error) {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		// Fallback: absolute with forward slashes.
		return filepath.ToSlash(target), nil
	}
	rel = filepath.ToSlash(rel)
	// go.mod requires replacement paths to be rooted or start with ./ or ../.
	// filepath.Rel of a sibling/child drops the ./ prefix (e.g. "sdk"), which
	// the go tool rejects as "replacement module without version must be
	// directory path". Normalize bare relative paths to "./<rel>".
	if !strings.HasPrefix(rel, "./") && !strings.HasPrefix(rel, "../") && !filepath.IsAbs(rel) {
		rel = "./" + rel
	}
	return rel, nil
}

// runGoModTidy runs `go mod tidy` in dir. It requires the Go toolchain.
func runGoModTidy(dir string) error {
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("go toolchain not found on PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := util.CommandContext(ctx, "go", "mod", "tidy")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return nil
}
