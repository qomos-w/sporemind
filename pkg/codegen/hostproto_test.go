package codegen

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"

	"github.com/qomos-w/spore/schema"
	"github.com/qomos-w/sporemind/pkg/appbinding"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// realHostCallSpecs returns a spec mapping grounded in the real compiled-in
// registry: llm.complete from the appbinding SDK catalog (adapted wire
// contract), provider.list / project.read_file as pass-through calls resolved
// from the gospore manifest. Schema IDs are looked up by struct name through
// gen.SchemaIDs so the test never hardcodes numeric IDs.
func realHostCallSpecs(t *testing.T) map[string]HostCallSpec {
	t.Helper()
	specs := map[string]HostCallSpec{}
	if sc, ok := appbinding.LookupSDKCall("llm.complete"); !ok {
		t.Fatal("llm.complete missing from appbinding.SDKCallCatalog")
	} else {
		specs["llm.complete"] = SpecFromSDKCall(sc)
	}
	for callID, hs := range map[string]HostCallSchema{
		"provider.list": {
			TargetCallID:  "aimanager.provider_list",
			Service:       "aimanager",
			FinalSchemaID: lookupSchemaID(t, "ProviderListResp"),
		},
		"project.read_file": {
			TargetCallID:  "filesystem.read",
			Service:       "filesystem",
			ReqSchemaID:   lookupSchemaID(t, "FileSystemReadReq"),
			FinalSchemaID: lookupSchemaID(t, "FileSystemReadResp"),
		},
	} {
		spec, err := SpecFromSchema(callID, hs)
		if err != nil {
			t.Fatalf("SpecFromSchema(%s): %v", callID, err)
		}
		specs[callID] = spec
	}
	return specs
}

func TestEmitHostProtocolGoProducesValidSource(t *testing.T) {
	src, err := EmitHostProtocolGo("main", realHostCallSpecs(t), nil)
	if err != nil {
		t.Fatalf("EmitHostProtocolGo: %v", err)
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "hostproto.gen.go", src, 0)
	if err != nil {
		t.Fatalf("emitted source does not parse: %v\n---\n%s", err, src)
	}
	if file.Name.Name != "main" {
		t.Fatalf("package = %q, want main", file.Name.Name)
	}

	decls := map[string]*ast.GenDecl{}
	var funcNames []string
	var constNames []string
	for _, d := range file.Decls {
		switch decl := d.(type) {
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					decls[s.Name.Name] = decl
				case *ast.ValueSpec:
					for _, n := range s.Names {
						constNames = append(constNames, n.Name)
					}
				}
			}
		case *ast.FuncDecl:
			funcNames = append(funcNames, decl.Name.Name)
		}
	}

	// Top-level req/resp types must all be present: the SDK catalog types for
	// llm.* (never the adapted backing schemas) and the manifest schemas for
	// pass-through calls.
	for _, name := range []string{
		"LLMReq", "LLMResp", "LLMMessage",
		"ProviderListResp",
		"FileSystemReadReq", "FileSystemReadResp",
	} {
		if _, ok := decls[name]; !ok {
			t.Errorf("emitted source missing type %s", name)
		}
	}
	if _, ok := decls["SendSessionMessageReq"]; ok {
		t.Error("emitted source contains SendSessionMessageReq — the backing schema must not leak into the llm.* SDK contract")
	}

	// Typed callers: one Call function per callID plus Stream functions for
	// streaming catalog entries.
	for _, name := range []string{
		"CallLLMComplete", "StreamLLMComplete",
		"CallProviderList",
		"CallProjectReadFile",
	} {
		found := false
		for _, fn := range funcNames {
			if fn == name {
				found = true
			}
		}
		if !found {
			t.Errorf("emitted source missing function %s (have %v)", name, funcNames)
		}
	}

	// The llm stream caller must ride the InvokeStream primitive with the
	// shared chunk decode, not a hand-written capability client.
	if !strings.Contains(src, `host.InvokeStream("llm.complete", req, sdk.ForwardLLMChunks(onChunk))`) {
		t.Error("StreamLLMComplete must delegate to host.InvokeStream + sdk.ForwardLLMChunks")
	}
	if strings.Contains(src, "host.LLM()") {
		t.Error("generated callers must not reference the removed host.LLM() facade")
	}

	// Transitive dependencies referenced by the top-level payloads must also
	// be extracted rather than left dangling.
	refs := map[string]bool{}
	for _, d := range file.Decls {
		ast.Inspect(d, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				refs[id.Name] = true
			}
			return true
		})
	}
	for _, name := range []string{"LLMReq", "ProviderListResp"} {
		if !refs[name] {
			t.Errorf("type %s never referenced; extraction may be dropping payloads", name)
		}
	}

	// Schema ID constants must exist for manifest-backed types.
	wantConst := "HostSchemaIDProviderListResp"
	found := false
	for _, c := range constNames {
		if c == wantConst {
			found = true
		}
	}
	if !found {
		t.Errorf("emitted source missing const %s (have %v)", wantConst, constNames)
	}

	// Header must document the covered host calls.
	for _, callID := range []string{"llm.complete", "provider.list", "project.read_file"} {
		if !strings.Contains(src, callID) {
			t.Errorf("header comment missing callID %s", callID)
		}
	}
}

func TestEmitHostProtocolGoDeterministic(t *testing.T) {
	a, err := EmitHostProtocolGo("main", realHostCallSpecs(t), nil)
	if err != nil {
		t.Fatalf("first emit: %v", err)
	}
	b, err := EmitHostProtocolGo("main", realHostCallSpecs(t), nil)
	if err != nil {
		t.Fatalf("second emit: %v", err)
	}
	if a != b {
		t.Fatal("emitted output is not deterministic across runs")
	}
}

func TestEmitHostProtocolGoEmpty(t *testing.T) {
	src, err := EmitHostProtocolGo("main", nil, nil)
	if err != nil {
		t.Fatalf("empty emit: %v", err)
	}
	if !strings.Contains(src, hostProtoMarker) {
		t.Error("empty emit must still carry the generated marker")
	}
	if strings.Contains(src, "HostSchemaID") {
		t.Error("empty emit must not declare schema constants")
	}
	if strings.Contains(src, "func Call") {
		t.Error("empty emit must not declare callers")
	}
}

func TestSpecFromSchemaUnknownSchemaID(t *testing.T) {
	_, err := SpecFromSchema("llm.complete", HostCallSchema{
		TargetCallID: "aiaggregator.dispatch",
		ReqSchemaID:  999999,
	})
	if err == nil || !strings.Contains(err.Error(), "not found in the host registry") {
		t.Fatalf("unknown schema ID must fail with registry error, got: %v", err)
	}
}

func TestEmitHostProtocolGoPreservesJSONTags(t *testing.T) {
	src, err := EmitHostProtocolGo("main", map[string]HostCallSpec{
		"project.read_file": {
			CallID:       "project.read_file",
			TargetCallID: "filesystem.read",
			ReqType:      typeOf(t, "FileSystemReadReq"),
			RespType:     typeOf(t, "FileSystemReadResp"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	// The host gen tags are PascalCase (e.g. `json:"Path"`); the emitted
	// fields must reproduce them verbatim so JSON round-trips unchanged.
	if !strings.Contains(src, "`json:\"") {
		t.Error("emitted struct fields carry no json tags")
	}
}

// TestEmitHostProtocolGoListens covers `listen` emission: payload types for
// catalog kinds land in hostproto.gen.go, the header documents them, and the
// empty-permissions + no-listens case stays empty.
func TestEmitHostProtocolGoListens(t *testing.T) {
	le, ok := appbinding.LookupHostEvent("app_lifecycle")
	if !ok {
		t.Fatal("app_lifecycle missing from EventCatalog")
	}
	src, err := EmitHostProtocolGo("main", nil, []appbinding.HostEvent{le})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if !strings.Contains(src, "type AppLifecycleEvent struct") {
		t.Error("listen payload type AppLifecycleEvent not emitted")
	}
	if !strings.Contains(src, "//   app_lifecycle  payload AppLifecycleEvent") {
		t.Error("listen header comment missing")
	}
	if strings.Contains(src, "intentionally empty") {
		t.Error("empty-file banner must not appear when listens are declared")
	}
	// Deterministic output.
	b, err := EmitHostProtocolGo("main", nil, []appbinding.HostEvent{le})
	if err != nil {
		t.Fatalf("emit 2: %v", err)
	}
	if src != b {
		t.Error("emit is not deterministic for identical listens")
	}
}

// TestEmitHostProtocolGoGenericStreamCaller pins the pass-through streaming
// emission: a spec with Streaming derived from the manifest (no
// StreamChunkKind decode) emits a StreamXxx caller whose onChunk receives the
// raw envelope wire bytes and whose terminal keeps the call's typed RespType.
func TestEmitHostProtocolGoGenericStreamCaller(t *testing.T) {
	spec := HostCallSpec{
		CallID:       "aiaggregator.dispatch",
		TargetCallID: "aiaggregator.dispatch",
		Service:      "aiaggregator",
		Streaming:    true,
		ReqType:      reflect.TypeOf(appbinding.LLMReq{}),
		RespType:     reflect.TypeOf(appbinding.LLMResp{}),
	}
	src, err := EmitHostProtocolGo("main", map[string]HostCallSpec{"aiaggregator.dispatch": spec}, nil)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if !strings.Contains(src, "func StreamAiaggregatorDispatch(host sdk.Host, req LLMReq, onChunk func([]byte) error) (LLMResp, error)") {
		t.Error("generic stream caller signature missing")
	}
	if !strings.Contains(src, `return host.InvokeStream("aiaggregator.dispatch", req, onChunk)`) {
		t.Error("caller must delegate to host.InvokeStream")
	}
	if strings.Contains(src, "host.LLM()") {
		t.Error("pass-through streaming must not use the sdk.LLM facade")
	}
}

func TestCallIDToFuncName(t *testing.T) {
	cases := map[string]string{
		"llm.complete":       "LLMComplete",
		"llm.chat":           "LLMChat",
		"project.read_file":  "ProjectReadFile",
		"state.delete":       "StateDelete",
		"aggregator.list":    "AggregatorList",
		"sshmanager.exec_id": "SshmanagerExecId",
	}
	for callID, want := range cases {
		if got := callIDToFuncName(callID); got != want {
			t.Errorf("callIDToFuncName(%q) = %q, want %q", callID, got, want)
		}
	}
}

func typeOf(t *testing.T, name string) reflect.Type {
	t.Helper()
	typ, ok := schema.StructTypeByID(lookupSchemaID(t, name))
	if !ok {
		t.Fatalf("registry type %s not resolvable", name)
	}
	return typ
}

func lookupSchemaID(t *testing.T, name string) uint64 {
	t.Helper()
	for id, n := range gen.SchemaIDs {
		if n == name {
			return id
		}
	}
	t.Fatalf("type %s not found in registry", name)
	return 0
}
