package computeruse

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/actor/computeruse/internal/capture/ocr"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
)

func TestOcrState_PersistRoundTrip(t *testing.T) {
	dir := t.TempDir()
	a := &Actor{store: persist.NewFSPersist(dir), actorID: "cu-1"}
	a.ocrState = ocrSnapshot{
		ModelDir:           "D:/models/ocr",
		LastSetupStatus:    "ready",
		LastSetupAt:        "2026-08-11T00:00:00Z",
		LastSetupFiles:     []string{"det.onnx", "rec.onnx"},
		PaddleAvailable:    true,
		TesseractAvailable: true,
	}
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}

	b := &Actor{store: persist.NewFSPersist(dir), actorID: "cu-1"}
	if err := b.Load(); err != nil {
		t.Fatal(err)
	}
	if b.ocrState.ModelDir != "D:/models/ocr" {
		t.Errorf("ModelDir=%q", b.ocrState.ModelDir)
	}
	if b.ocrState.LastSetupStatus != "ready" {
		t.Errorf("LastSetupStatus=%q", b.ocrState.LastSetupStatus)
	}
	if len(b.ocrState.LastSetupFiles) != 2 {
		t.Errorf("LastSetupFiles=%v", b.ocrState.LastSetupFiles)
	}
	if !b.ocrState.PaddleAvailable || !b.ocrState.TesseractAvailable {
		t.Errorf("probe results not restored: %+v", b.ocrState)
	}
}

func TestOcrState_LoadFirstStart(t *testing.T) {
	b := &Actor{store: persist.NewFSPersist(t.TempDir()), actorID: "cu-new"}
	if err := b.Load(); err != nil {
		t.Fatalf("first-start load must succeed with zero state: %v", err)
	}
	if b.ocrState.ModelDir != "" || b.ocrState.LastSetupStatus != "" || len(b.ocrState.LastSetupFiles) != 0 ||
		b.ocrState.PaddleAvailable || b.ocrState.TesseractAvailable {
		t.Errorf("ocrState=%+v, want zero", b.ocrState)
	}
}

func TestHandleSetupOcr_PersistsAndReprobes(t *testing.T) {
	a, _, ctx := freshActor(t)
	a.store = persist.NewFSPersist(t.TempDir())
	a.actorID = "cu-setup"

	modelDir := t.TempDir()
	orig := ensurePaddleModels
	ensurePaddleModels = func(dir string, force bool) (string, []string, error) {
		if dir != modelDir {
			t.Errorf("ensurePaddleModels dir=%q, want %q", dir, modelDir)
		}
		return "ready", []string{modelDir + "/det.onnx"}, nil
	}
	t.Cleanup(func() { ensurePaddleModels = orig })
	t.Cleanup(func() { ocr.SetModelDir("") })
	t.Cleanup(ocr.ResetPaddle)

	resp, err := a.handleSetupOcr(ctx, domain.ComputerUseSetupOcrReq{Dir: modelDir})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != "ready" {
		t.Fatalf("Status=%q", resp.Status)
	}
	if a.ocrState.ModelDir != modelDir {
		t.Errorf("ocrState.ModelDir=%q, want %q", a.ocrState.ModelDir, modelDir)
	}
	if a.ocrState.LastSetupStatus != "ready" {
		t.Errorf("LastSetupStatus=%q", a.ocrState.LastSetupStatus)
	}
	if got := ocr.ModelDir(); got != modelDir {
		t.Errorf("ocr.ModelDir()=%q, want %q", got, modelDir)
	}

	// The setup result must be persisted through pkg/persist.
	b := &Actor{store: persist.NewFSPersist(a.store.(*persist.FSPersist).BaseDir), actorID: "cu-setup"}
	if err := b.Load(); err != nil {
		t.Fatal(err)
	}
	if b.ocrState.ModelDir != modelDir || b.ocrState.LastSetupStatus != "ready" {
		t.Errorf("reloaded state=%+v", b.ocrState)
	}
}

func TestHandleSetupOcr_FailureStillPersistsStatus(t *testing.T) {
	a, _, ctx := freshActor(t)
	a.store = persist.NewFSPersist(t.TempDir())
	a.actorID = "cu-setup-fail"

	orig := ensurePaddleModels
	ensurePaddleModels = func(string, bool) (string, []string, error) {
		return "failed", nil, errStubDownload
	}
	t.Cleanup(func() { ensurePaddleModels = orig })

	resp, err := a.handleSetupOcr(ctx, domain.ComputerUseSetupOcrReq{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != "failed" || resp.Error == "" {
		t.Fatalf("resp=%+v", resp)
	}
	if a.ocrState.LastSetupStatus != "failed" {
		t.Errorf("LastSetupStatus=%q, want failed", a.ocrState.LastSetupStatus)
	}
	// A failed setup must not register a model dir.
	if a.ocrState.ModelDir != "" {
		t.Errorf("ModelDir=%q after failed setup", a.ocrState.ModelDir)
	}
}

type stubError string

func (e stubError) Error() string { return string(e) }

const errStubDownload = stubError("stub: download failed")
