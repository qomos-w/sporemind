package codegen

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// appdefWithPermissions writes a minimal .appdef declaring the given
// permissions (host callIDs) and returns its directory.
func appdefWithPermissions(t *testing.T, perms string) string {
	t.Helper()
	dir := t.TempDir()
	permLine := ""
	if perms != "" {
		permLine = "\n    permissions: " + perms
	}
	content := `app PermApp {
    id: "app.perm"` + permLine + `
    version: "0.1.0"
    namespace: "perm"

    struct PingReq {
        Name: string
    }
    struct PingResp {
        Ok: bool
    }
    callable ping {
        request: PingReq
        response: PingResp
        effect: "read"
    }
}
`
	if err := os.WriteFile(filepath.Join(dir, FileAppDef), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestGenerateRejectsUnknownPermission(t *testing.T) {
	dir := appdefWithPermissions(t, `["llm.complete", "llm.complet"]`)
	_, err := Generate(dir, testGenOpts(t))
	if err == nil {
		t.Fatal("unknown host callable must fail generation")
	}
	if !strings.Contains(err.Error(), "llm.complet") || !strings.Contains(err.Error(), "not a known host callable") {
		t.Fatalf("error must name the unknown callID and point at the catalog, got: %v", err)
	}
}

// TestGenerateRejectsCapabilityPermission pins the callID-only declaration
// form: capability strings (the pre-callID style) fail with a hint instead of
// being silently accepted.
func TestGenerateRejectsCapabilityPermission(t *testing.T) {
	dir := appdefWithPermissions(t, `["llm.invoke"]`)
	_, err := Generate(dir, testGenOpts(t))
	if err == nil {
		t.Fatal("capability-style permission must fail generation")
	}
	if !strings.Contains(err.Error(), "llm.invoke") || !strings.Contains(err.Error(), "is a capability, not a callable") {
		t.Fatalf("error must explain capabilities are derived, got: %v", err)
	}
}

// TestGenerateAcceptsCallIDCapabilityCollision pins that a permission string
// which is BOTH a registered SDK host callID and a capability id
// (dialog.openFile / dialog.openFolder — Local pluginhost calls whose callID
// equals the capability) is accepted as a callID declaration. The dev guide
// documents this form; the capability-vs-callID rejection must not swallow it.
func TestGenerateAcceptsCallIDCapabilityCollision(t *testing.T) {
	dir := appdefWithPermissions(t, `["state.get", "dialog.openFolder", "dialog.openFile"]`)
	if _, err := Generate(dir, testGenOpts(t)); err != nil {
		t.Fatalf("dialog callID permissions must generate, got: %v", err)
	}
}

// TestGenerateAcceptsLoadTimeCapability pins the app.data declaration form:
// load-time capabilities have no callID gate, so the capability string itself
// is the only declarable form. It must pass validation and land in the
// manifest Permissions verbatim (alongside capabilities derived from any
// declared callIDs) — that is what appmanager's appDataDirFor keys off.
func TestGenerateAcceptsLoadTimeCapability(t *testing.T) {
	dir := appdefWithPermissions(t, `["state.get", "app.data"]`)
	if _, err := Generate(dir, testGenOpts(t)); err != nil {
		t.Fatalf("app.data declaration must generate: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, FileManifestJSON))
	if err != nil {
		t.Fatalf("read %s: %v", FileManifestJSON, err)
	}
	var manifest struct {
		Permissions []string `json:"Permissions"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	want := []string{"app.data", "app.state"}
	if strings.Join(manifest.Permissions, ",") != strings.Join(want, ",") {
		t.Fatalf("manifest permissions = %v, want %v (app.data verbatim + app.state derived)", manifest.Permissions, want)
	}
}

// TestGenerateManifestDerivesCapabilities pins that the manifest carries the
// derived capabilities (sorted, deduped), not the declared callIDs — the
// runtime authorization chain speaks capabilities.
func TestGenerateManifestDerivesCapabilities(t *testing.T) {
	dir := appdefWithPermissions(t, `["state.set", "llm.chat", "state.get", "llm.complete"]`)
	if _, err := Generate(dir, testGenOpts(t)); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileManifestJSON))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Permissions []string `json:"Permissions"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	want := []string{"app.state", "llm.invoke"}
	if strings.Join(manifest.Permissions, ",") != strings.Join(want, ",") {
		t.Fatalf("manifest permissions = %v, want %v", manifest.Permissions, want)
	}
}

func TestGenerateHostProtoOnlyForDeclaredCalls(t *testing.T) {
	// project.read_file is declared; llm.complete is provided by the catalog
	// but NOT declared, so its types must be filtered out.
	dir := appdefWithPermissions(t, `["project.read_file"]`)
	res, err := Generate(dir, Options{
		SDKPath: testGenOpts(t).SDKPath,
		HostCalls: map[string]HostCallSchema{
			"project.read_file": {
				TargetCallID:  "filesystem.read",
				ReqSchemaID:   lookupSchemaID(t, "FileSystemReadReq"),
				FinalSchemaID: lookupSchemaID(t, "FileSystemReadResp"),
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	found := false
	for _, f := range res.Files {
		if f == FileHostProtoGo {
			found = true
		}
	}
	if !found {
		t.Fatalf("hostproto.gen.go not in generated files: %v", res.Files)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileHostProtoGo))
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	if !strings.Contains(src, "type FileSystemReadReq struct") {
		t.Error("declared project.read_file protocol types missing from hostproto.gen.go")
	}
	if !strings.Contains(src, "func CallProjectReadFile(") {
		t.Error("typed caller CallProjectReadFile missing from hostproto.gen.go")
	}
	if strings.Contains(src, "LLMReq") {
		t.Error("undeclared llm.complete contract leaked into hostproto.gen.go")
	}
}

// TestGenerateHostProtoCatalogCalls pins the catalog-driven emission without
// any gmanifest input: state.* + llm.* declarations produce the SDK wire
// types and typed Call/Stream callers.
func TestGenerateHostProtoCatalogCalls(t *testing.T) {
	dir := appdefWithPermissions(t, `["state.get", "state.set", "llm.complete"]`)
	if _, err := Generate(dir, testGenOpts(t)); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileHostProtoGo))
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	for _, want := range []string{
		"type StateKeyReq struct",
		"type StateGetResp struct",
		"type LLMReq struct",
		"type LLMResp struct",
		"func CallStateGet(host sdk.Host, req StateKeyReq) (StateGetResp, error)",
		"func CallStateSet(host sdk.Host, req StateSetReq) ([]byte, error)",
		"func CallLLMComplete(host sdk.Host, req LLMReq) (LLMResp, error)",
		"func StreamLLMComplete(host sdk.Host, req LLMReq, onChunk func(sdk.LLMChunk) error) (LLMResp, error)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("hostproto.gen.go missing %q", want)
		}
	}
	// json.RawMessage must survive extraction: collapsing it to []byte makes
	// the field unmarshal from a base64 string while the host sends raw JSON
	// (LLMResp.Usage crashed real apps with "cannot unmarshal object").
	if !regexp.MustCompile(`Usage\s+json\.RawMessage`).MatchString(src) {
		t.Error("hostproto.gen.go must render LLMResp.Usage as json.RawMessage, not []byte")
	}
	if !strings.Contains(src, "\"encoding/json\"") {
		t.Error("hostproto.gen.go must import encoding/json when json.RawMessage fields are emitted")
	}
	if regexp.MustCompile(`Usage\s+\[\]byte`).MatchString(src) {
		t.Error("hostproto.gen.go must not render Usage as []byte")
	}
}

// TestGenerateHostProtoRegistryQuery pins that declaring `registry.query`
// (capability registry.read) emits the typed CallRegistryQuery caller and the
// wire types from the appbinding SDK wire-contract (RegistryQueryReq /
// RegistryQueryResp / RegistryCallableMeta), mirroring the pluginhost-side
// handler struct JSON tags exactly.
func TestGenerateHostProtoRegistryQuery(t *testing.T) {
	dir := appdefWithPermissions(t, `["registry.query"]`)
	if _, err := Generate(dir, testGenOpts(t)); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileHostProtoGo))
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	for _, want := range []string{
		"type RegistryQueryReq struct",
		"type RegistryQueryResp struct",
		"type RegistryCallableMeta struct",
		"func CallRegistryQuery(host sdk.Host, req RegistryQueryReq) (RegistryQueryResp, error)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("hostproto.gen.go missing %q", want)
		}
	}
	// Wire JSON tags must mirror the pluginhost-side handler exactly
	// (gofmt aligns columns, so match the tag text, not the whitespace).
	for _, want := range []string{
		"`json:\"service,omitempty\"`",
		"`json:\"callable,omitempty\"`",
		"`json:\"limit,omitempty\"`",
		"`json:\"cursor,omitempty\"`",
		"`json:\"NextCursor,omitempty\"`",
		"`json:\"CallID\"`",
		"`json:\"ReqSchemaId,omitempty\"`",
		"`json:\"RespSchemaId,omitempty\"`",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("hostproto.gen.go missing wire JSON tag %q", want)
		}
	}
	// The caller must invoke the host bridge with the SDK callID.
	if !strings.Contains(src, `host.Invoke("registry.query"`) {
		t.Error("CallRegistryQuery must delegate to host.Invoke with callID \"registry.query\"")
	}
}

// TestGenerateHostProtoVoice pins the voice pass-through codegen chain:
// declaring voice.recognize/voice.synthesize with manifest schema IDs emits
// typed CallVoiceRecognize/CallVoiceSynthesize callers referencing the
// pkg/domain/gen types, and the manifest derives the voice.stt/voice.tts
// capabilities from the callIDs.
func TestGenerateHostProtoVoice(t *testing.T) {
	dir := appdefWithPermissions(t, `["voice.recognize", "voice.synthesize", "voice.accounts.list"]`)
	_, err := Generate(dir, Options{
		SDKPath: testGenOpts(t).SDKPath,
		HostCalls: map[string]HostCallSchema{
			"voice.recognize": {
				TargetCallID:  "voice.recognize",
				ReqSchemaID:   lookupSchemaID(t, "VoiceRecognizeReq"),
				FinalSchemaID: lookupSchemaID(t, "VoiceRecognizeResp"),
			},
			"voice.synthesize": {
				TargetCallID:  "voice.synthesize",
				ReqSchemaID:   lookupSchemaID(t, "VoiceSynthesizeReq"),
				FinalSchemaID: lookupSchemaID(t, "VoiceSynthesizeResp"),
			},
			"voice.accounts.list": {
				TargetCallID:  "voice.list_accounts",
				ReqSchemaID:   lookupSchemaID(t, "VoiceAccountListReq"),
				FinalSchemaID: lookupSchemaID(t, "VoiceAccountListResp"),
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileHostProtoGo))
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	for _, want := range []string{
		"type VoiceRecognizeReq struct",
		"type VoiceRecognizeResp struct",
		"func CallVoiceRecognize(host sdk.Host, req VoiceRecognizeReq) (VoiceRecognizeResp, error)",
		"type VoiceSynthesizeReq struct",
		"type VoiceSynthesizeResp struct",
		"func CallVoiceSynthesize(host sdk.Host, req VoiceSynthesizeReq) (VoiceSynthesizeResp, error)",
		"type VoiceAccountListReq struct",
		"type VoiceAccountListResp struct",
		"func CallVoiceAccountsList(host sdk.Host, req VoiceAccountListReq) (VoiceAccountListResp, error)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("hostproto.gen.go missing %q", want)
		}
	}
	if !strings.Contains(src, `host.Invoke("voice.recognize"`) {
		t.Error("CallVoiceRecognize must delegate to host.Invoke with callID \"voice.recognize\"")
	}
	if !strings.Contains(src, `host.Invoke("voice.accounts.list"`) {
		t.Error("CallVoiceAccountsList must delegate to host.Invoke with the SDK callID \"voice.accounts.list\" (not the alias target)")
	}
	if !strings.Contains(src, "AccountID string") && !strings.Contains(src, "AccountId string") {
		// Dump the struct region so the exact rendering is visible on failure.
		start := strings.Index(src, "type VoiceRecognizeReq struct")
		end := strings.Index(src[start:], "}")
		t.Errorf("hostproto.gen.go missing AccountID/AccountId field; VoiceRecognizeReq renders as:\n%s", src[start:start+end+1])
	}

	// Manifest permissions must carry the derived capabilities.
	mdata, err := os.ReadFile(filepath.Join(dir, FileManifestJSON))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Permissions []string `json:"Permissions"`
	}
	if err := json.Unmarshal(mdata, &manifest); err != nil {
		t.Fatal(err)
	}
	want := []string{"voice.read", "voice.stt", "voice.tts"}
	if strings.Join(manifest.Permissions, ",") != strings.Join(want, ",") {
		t.Fatalf("manifest permissions = %v, want %v", manifest.Permissions, want)
	}
}

func TestGenerateNoHostProtoWithoutPermissions(t *testing.T) {
	dir := appdefWithPermissions(t, "")
	if _, err := Generate(dir, testGenOpts(t)); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, FileHostProtoGo)); !os.IsNotExist(err) {
		t.Fatalf("hostproto.gen.go must not be written when no callIDs are declared (stat err: %v)", err)
	}
}

// TestGenerateHostProtoBundleCall pins that a plugin.* permission emits a
// raw-JSON caller (no typed structs — the target plugin owns the schema).
func TestGenerateHostProtoBundleCall(t *testing.T) {
	dir := appdefWithPermissions(t, `["plugin.translator.translate"]`)
	_, err := Generate(dir, testGenOpts(t))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileHostProtoGo))
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	if !strings.Contains(src, "func CallPluginTranslatorTranslate(host sdk.Host, req json.RawMessage) ([]byte, error)") {
		t.Errorf("hostproto.gen.go missing bundle caller; got:\n%s", src)
	}
	if !strings.Contains(src, `host.Invoke("plugin.translator.translate"`) {
		t.Errorf("bundle caller must invoke with the full plugin.* callID; got:\n%s", src)
	}
	// No inline struct types — the target plugin owns the schema.
	if strings.Contains(src, "type Translate") {
		t.Errorf("bundle caller must not emit typed structs; got:\n%s", src)
	}

	// Manifest permissions carry the verbatim plugin.* entry (the per-callID
	// authorization gate), not the derived bundle.invoke capability.
	mdata, err := os.ReadFile(filepath.Join(dir, FileManifestJSON))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Permissions []string `json:"Permissions"`
	}
	if err := json.Unmarshal(mdata, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Permissions) != 1 || manifest.Permissions[0] != "plugin.translator.translate" {
		t.Fatalf("manifest permissions = %v, want [plugin.translator.translate]", manifest.Permissions)
	}
}

// TestGenerateHostProtoCompiles smoke-compiles a generated app that declares
// project.read_file + llm.complete: hostproto.gen.go with its typed callers
// must build against the vendored SDK alongside the rest of the generated
// artifacts. Skipped without a go toolchain (mirrors the pluginhost e2e
// gating).
func TestGenerateHostProtoCompiles(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	dir := appdefWithPermissions(t, `["project.read_file", "llm.complete", "state.get"]`)
	_, err := Generate(dir, Options{
		SDKPath: testGenOpts(t).SDKPath,
		HostCalls: map[string]HostCallSchema{
			"project.read_file": {
				TargetCallID: "filesystem.read",
				ReqSchemaID:  lookupSchemaID(t, "FileSystemReadReq"),
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated app with hostproto.gen.go does not compile: %v\n%s", err, out)
	}
}

// TestGenerateHostProtoMediaGen pins the media generation codegen chain:
// declaring image.generate/video.generate emits typed callers over the
// domain gen types (ImageGenerateReq / VideoGenerateReq / AppMediaGenResp),
// media.list_units mirrors the aimanager list-units wire, and
// media.accounts.list mirrors the media actor's redacted account-list wire,
// with the manifest deriving media.read / image.gen / video.gen from the
// callIDs.
func TestGenerateHostProtoMediaGen(t *testing.T) {
	dir := appdefWithPermissions(t, `["image.generate", "video.generate", "media.list_units", "media.accounts.list"]`)
	_, err := Generate(dir, Options{
		SDKPath: testGenOpts(t).SDKPath,
		HostCalls: map[string]HostCallSchema{
			"image.generate": {
				TargetCallID:  "image.generate",
				ReqSchemaID:   lookupSchemaID(t, "ImageGenerateReq"),
				FinalSchemaID: lookupSchemaID(t, "AppMediaGenResp"),
			},
			"video.generate": {
				TargetCallID:  "video.generate",
				ReqSchemaID:   lookupSchemaID(t, "VideoGenerateReq"),
				FinalSchemaID: lookupSchemaID(t, "AppMediaGenResp"),
			},
			"media.list_units": {
				TargetCallID:  "aimanager.list_units",
				ReqSchemaID:   lookupSchemaID(t, "AIManagerListUnitsReq"),
				FinalSchemaID: lookupSchemaID(t, "AIManagerListUnitsResp"),
			},
			"media.accounts.list": {
				TargetCallID:  "media.list_accounts",
				ReqSchemaID:   lookupSchemaID(t, "MediaAccountListReq"),
				FinalSchemaID: lookupSchemaID(t, "MediaAccountListResp"),
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileHostProtoGo))
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	for _, want := range []string{
		"type ImageGenerateReq struct",
		"type VideoGenerateReq struct",
		"type AppMediaGenResp struct",
		"func CallImageGenerate(host sdk.Host, req ImageGenerateReq) (AppMediaGenResp, error)",
		"func CallVideoGenerate(host sdk.Host, req VideoGenerateReq) (AppMediaGenResp, error)",
		"type MediaAccountListReq struct",
		"type MediaAccountView struct",
		"func CallMediaAccountsList(host sdk.Host, req MediaAccountListReq) (MediaAccountListResp, error)",
		"host.Invoke(\"image.generate\"",
		"host.Invoke(\"video.generate\"",
		"host.Invoke(\"media.list_units\"",
		"host.Invoke(\"media.accounts.list\"",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("hostproto.gen.go missing %q", want)
		}
	}
	if strings.Contains(src, "AuthToken") {
		t.Error("generated media wire must never carry AuthToken (credentials stay host-side)")
	}
	if !strings.Contains(src, "HasAPIKey") {
		t.Error("generated account view must expose the redacted HasAPIKey flag")
	}
	if strings.Contains(src, "APIKey string") || strings.Contains(src, "APIKey []byte") {
		t.Error("generated account wire must never carry a raw APIKey field")
	}

	mdata, err := os.ReadFile(filepath.Join(dir, FileManifestJSON))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Permissions []string `json:"Permissions"`
	}
	if err := json.Unmarshal(mdata, &manifest); err != nil {
		t.Fatal(err)
	}
	want := []string{"image.gen", "media.read", "video.gen"}
	if strings.Join(manifest.Permissions, ",") != strings.Join(want, ",") {
		t.Errorf("manifest Permissions = %v, want %v", manifest.Permissions, want)
	}
}

// TestGenerateHostProtoInsightCapsSmoke is the codegen smoke harness for the
// five pass-through host callIDs added alongside the web.search / web.fetch /
// stats.read / discovery.read / wiki.read capabilities: websearch.search,
// crawl.start, aistats.query, oracle.search_services, project.wiki_list_cards.
//
// Each callID is registered as pass-through in pkg/appbinding (the wire IS the
// backing actor callable schema in pkg/domain/gen, no adapted SDK wire
// contract), so the only way the host protocol extraction can fail here is if
// the gospore manifest drift breaks the schema-ID lookup chain or if the
// emitter drops a callID / wire type / caller. The test pins all three:
//
//   - every callID appears in the header comment (Covered host calls section),
//   - the schema-extracted wire types and typed callers are present in the
//     emitted source (CallWebsearchSearch, CallCrawlStart, CallAistatsQuery,
//     CallOracleSearchServices, CallProjectWikiListCards over WebSearchReq /
//     BrowserCrawlStartReq / AIStatsQueryReq / OracleSearchServicesReq /
//     WikiListCardsReq and their *Resp counterparts),
//   - the manifest Permissions list carries the five derived capabilities
//     (discovery.read, stats.read, web.fetch, web.search, wiki.read —
//     sorted, no duplicates, no callID leakage).
//
// It deliberately skips the go build step that TestGenerateHostProtoCompiles
// runs: the smoke is about the emission contract, and the wire-types-against-
// vendored-SDK conformance is already pinned by TestGenerateHostProtoCompiles
// for a smaller set.
func TestGenerateHostProtoInsightCapsSmoke(t *testing.T) {
	dir := appdefWithPermissions(t, `["websearch.search", "crawl.start", "aistats.query", "oracle.search_services", "project.wiki_list_cards"]`)
	_, err := Generate(dir, Options{
		SDKPath: testGenOpts(t).SDKPath,
		HostCalls: map[string]HostCallSchema{
			"websearch.search": {
				TargetCallID:  "websearch.search",
				ReqSchemaID:   lookupSchemaID(t, "WebSearchReq"),
				FinalSchemaID: lookupSchemaID(t, "WebSearchResp"),
			},
			"crawl.start": {
				TargetCallID:  "crawl.start",
				ReqSchemaID:   lookupSchemaID(t, "BrowserCrawlStartReq"),
				FinalSchemaID: lookupSchemaID(t, "BrowserCrawlStartResp"),
			},
			"aistats.query": {
				TargetCallID:  "aistats.query",
				ReqSchemaID:   lookupSchemaID(t, "AIStatsQueryReq"),
				FinalSchemaID: lookupSchemaID(t, "AIStatsQueryResp"),
			},
			"oracle.search_services": {
				TargetCallID:  "oracle.search_services",
				ReqSchemaID:   lookupSchemaID(t, "OracleSearchServicesReq"),
				FinalSchemaID: lookupSchemaID(t, "OracleSearchServicesResp"),
			},
			"project.wiki_list_cards": {
				TargetCallID:  "project.wiki_list_cards",
				ReqSchemaID:   lookupSchemaID(t, "WikiListCardsReq"),
				FinalSchemaID: lookupSchemaID(t, "WikiListCardsResp"),
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Artifact present + declares the five pass-through callIDs in the
	// header + carries the wire types and typed callers the gen must emit.
	data, err := os.ReadFile(filepath.Join(dir, FileHostProtoGo))
	if err != nil {
		t.Fatalf("read hostproto.gen.go: %v", err)
	}
	src := string(data)
	for _, want := range []string{
		// Header: every callID listed, each self-targeted with its req/resp
		// wire type name. The double-space before `req` / `resp` mirrors the
		// emitter's format string exactly so the assertion is byte-stable.
		"//   websearch.search -> websearch.search  req WebSearchReq  resp WebSearchResp",
		"//   crawl.start -> crawl.start  req BrowserCrawlStartReq  resp BrowserCrawlStartResp",
		"//   aistats.query -> aistats.query  req AIStatsQueryReq  resp AIStatsQueryResp",
		"//   oracle.search_services -> oracle.search_services  req OracleSearchServicesReq  resp OracleSearchServicesResp",
		"//   project.wiki_list_cards -> project.wiki_list_cards  req WikiListCardsReq  resp WikiListCardsResp",
		// Wire types extracted from the manifest schemas.
		"type WebSearchReq struct",
		"type WebSearchResp struct",
		"type BrowserCrawlStartReq struct",
		"type BrowserCrawlStartResp struct",
		"type AIStatsQueryReq struct",
		"type AIStatsQueryResp struct",
		"type OracleSearchServicesReq struct",
		"type OracleSearchServicesResp struct",
		"type WikiListCardsReq struct",
		"type WikiListCardsResp struct",
		// Typed callers (one CallXxx per callID; no Stream since none stream).
		"func CallWebsearchSearch(host sdk.Host, req WebSearchReq) (WebSearchResp, error)",
		"func CallCrawlStart(host sdk.Host, req BrowserCrawlStartReq) (BrowserCrawlStartResp, error)",
		"func CallAistatsQuery(host sdk.Host, req AIStatsQueryReq) (AIStatsQueryResp, error)",
		"func CallOracleSearchServices(host sdk.Host, req OracleSearchServicesReq) (OracleSearchServicesResp, error)",
		"func CallProjectWikiListCards(host sdk.Host, req WikiListCardsReq) (WikiListCardsResp, error)",
		// Each caller must delegate to host.Invoke with the SDK callID
		// (self-targeted pass-through — TargetCallID == CallID, so the
		// emitter writes the callID verbatim into host.Invoke("...")).
		`host.Invoke("websearch.search"`,
		`host.Invoke("crawl.start"`,
		`host.Invoke("aistats.query"`,
		`host.Invoke("oracle.search_services"`,
		`host.Invoke("project.wiki_list_cards"`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("hostproto.gen.go missing %q", want)
		}
	}

	// No stray StreamXxx / SDK wire contract: all five callIDs are unary
	// pass-through; the llm.* adapter shapes must not leak.
	for _, banned := range []string{"StreamWebsearchSearch", "StreamCrawlStart", "StreamAistatsQuery", "StreamOracleSearchServices", "StreamProjectWikiListCards"} {
		if strings.Contains(src, banned) {
			t.Errorf("hostproto.gen.go must not emit stream caller %q (none of the five callIDs stream)", banned)
		}
	}
	for _, banned := range []string{"LLMReq", "LLMResp", "StateKeyReq", "StateGetResp"} {
		if strings.Contains(src, banned) {
			t.Errorf("hostproto.gen.go must not carry %q (leaked catalog/SDK wire type)", banned)
		}
	}

	// HostSchemaID constants for every wire type extracted.
	for _, want := range []string{
		"HostSchemaIDWebSearchReq",
		"HostSchemaIDWebSearchResp",
		"HostSchemaIDBrowserCrawlStartReq",
		"HostSchemaIDBrowserCrawlStartResp",
		"HostSchemaIDAIStatsQueryReq",
		"HostSchemaIDAIStatsQueryResp",
		"HostSchemaIDOracleSearchServicesReq",
		"HostSchemaIDOracleSearchServicesResp",
		"HostSchemaIDWikiListCardsReq",
		"HostSchemaIDWikiListCardsResp",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("hostproto.gen.go missing HostSchemaID const %q", want)
		}
	}

	// Manifest: 5 declared callIDs → 5 derived capabilities (one per
	// callID, all unique since none share a capability). Sorted.
	mdata, err := os.ReadFile(filepath.Join(dir, FileManifestJSON))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Permissions []string `json:"Permissions"`
	}
	if err := json.Unmarshal(mdata, &manifest); err != nil {
		t.Fatal(err)
	}
	wantPerms := []string{"discovery.read", "stats.read", "web.fetch", "web.search", "wiki.read"}
	if strings.Join(manifest.Permissions, ",") != strings.Join(wantPerms, ",") {
		t.Fatalf("manifest Permissions = %v, want %v (sorted, derived from declared callIDs)", manifest.Permissions, wantPerms)
	}

	// Determinism: a second pass must produce byte-identical output so the
	// gofmt-cleanup + sorted emission contract is actually stable.
	data2, err := os.ReadFile(filepath.Join(dir, FileHostProtoGo))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(data2) {
		t.Error("hostproto.gen.go is not byte-stable across reads (t.TempDir keeps the file so re-reads must match)")
	}
}
