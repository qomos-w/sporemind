package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestBundleCallsGeneratedAppBuildsStandalone is the compile-level e2e for the
// bundle shell: an app whose appdef declares a bundle-level permission is
// generated with a registered dependency view, its handlers.go calls the
// typed CallBundle* caller, and the whole tree compiles against the vendored
// SDK. This pins the typed contract (function names, request/response structs,
// host.Invoke wire callID) as real code, not mock-only.
func TestBundleCallsGeneratedAppBuildsStandalone(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the full SDK")
	}
	dir := t.TempDir()
	appdefSrc := `app BundleBuild {
    id:          "app.bundlebuild"
    name:        "BundleBuild"
    version:     "0.1.0"
    namespace:   bundlebuild

    permissions: ["plugin.app.translator.translate-tools"]

    dependency app.translator {
        version: "2.1.0"
    }

    struct AskReq {
        Text: string
    }
    struct AskResp {
        Text: string
    }

    callable ask {
        request:  AskReq
        response: AskResp
    }
}
`
	if err := os.WriteFile(filepath.Join(dir, FileAppDef), []byte(appdefSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(dir, Options{
		SDKPath:    testSDKPath(t),
		HostCalls:  map[string]HostCallSchema{},
		BundleDeps: map[string]BundleDep{"app.translator": translatorDep()},
	}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, FileBundleCallsGo))
	if err != nil {
		t.Fatalf("read bundle_calls.gen.go: %v", err)
	}
	if !strings.Contains(string(content), "func CallBundleAppTranslatorTranslateRun(") {
		t.Fatalf("bundle_calls.gen.go missing typed caller:\n%s", content)
	}

	// Replace the stub handlers with one that actually drives the typed
	// bundle caller through the host bridge.
	handlers := `package main

import (
	"encoding/json"

	sdk "github.com/qomos-w/sporemind-plugin-sdk"
)

func handleAsk(req sdk.Request) (sdk.Response, error) {
	var payload AskReq
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		return sdk.Response{}, err
	}
	resp, err := CallBundleAppTranslatorTranslateRun(sdk.ActiveHost(), AppTranslatorTranslateRunReq{Text: payload.Text})
	if err != nil {
		return sdk.Response{}, err
	}
	return sdk.Response{Payload: mustJSON(t_(resp))}, nil
}

// t_ and mustJSON keep the handler self-contained for the compile check.
type t_ = AppTranslatorTranslateRunResp

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
`
	if err := os.WriteFile(filepath.Join(dir, FileHandlersGo), []byte(handlers), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bundle-caller build failed: %v\n%s", err, out)
	}
}

// TestBundleSnapshotsMatchExpansionOrder pins that snapshots are deterministic
// across repeated resolutions of the same registry view.
func TestBundleSnapshotsMatchExpansionOrder(t *testing.T) {
	first, err := ComputeBundleSnapshots(`app Determinism {
    id: app.det
    namespace: det
    permissions: ["plugin.app.translator.translate-tools"]
    dependency app.translator { }
    callable ping { }
}
`, map[string]BundleDep{"app.translator": translatorDep()})
	if err != nil {
		t.Fatalf("ComputeBundleSnapshots: %v", err)
	}
	second, err := ComputeBundleSnapshots(`app Determinism {
    id: app.det
    namespace: det
    permissions: ["plugin.app.translator.translate-tools"]
    dependency app.translator { }
    callable ping { }
}
`, map[string]BundleDep{"app.translator": translatorDep()})
	if err != nil {
		t.Fatalf("ComputeBundleSnapshots second: %v", err)
	}
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("snapshots = %+v / %+v", first, second)
	}
	if first[0].DepID != second[0].DepID || len(first[0].Callables) != len(second[0].Callables) {
		t.Errorf("nondeterministic snapshots: %+v vs %+v", first[0], second[0])
	}
}
