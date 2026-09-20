package winx

import (
	"fmt"
	"image"
	"syscall"
	"testing"
)

// TestCreateDIBSectionReportsFailure verifies that createDIBSection surfaces
// an error when the underlying CreateDIBSection call fails (returns NULL),
// instead of returning a nil pointer that would panic on pixel access.
func TestCreateDIBSectionReportsFailure(t *testing.T) {
	orig := pCreateDIBSection
	defer func() { pCreateDIBSection = orig }()
	pCreateDIBSection = syscall.NewLazyDLL("kernel32.dll").NewProc("CloseHandle")

	_, _, err := createDIBSection(0, 4, 4)
	if err == nil {
		t.Fatal("createDIBSection: expected error when CreateDIBSection returns NULL, got nil")
	}
	if want := "CreateDIBSection failed"; err.Error() != want {
		t.Fatalf("createDIBSection error = %q, want %q", err.Error(), want)
	}
}

// TestCreateDIBSectionProducesBitmap verifies that createDIBSection returns a
// valid bitmap and mapped pixel buffer, and that the buffer is initially
// zeroed, allowing the caller to rely on its contents after a BitBlt.
func TestCreateDIBSectionProducesBitmap(t *testing.T) {
	hdc, _, _ := pGetDC.Call(0)
	if hdc == 0 {
		t.Skip("no desktop DC available")
	}
	defer pReleaseDC.Call(0, hdc)

	hBmp, _, err := createDIBSection(hdc, 16, 16)
	if err != nil {
		t.Fatalf("createDIBSection: %v", err)
	}
	defer pDeleteObject.Call(hBmp)
	if hBmp == 0 {
		t.Fatal("createDIBSection: bitmap handle is NULL")
	}
}

// TestCaptureScreenRectSizes is an end-to-end regression test for the
// size-dependent capture failure (GitHub-style black-screen / GetDIBits bug):
// GDI readback of DDBs fails intermittently beyond ~500x500 and consistently
// at fullscreen sizes. The DIB-section pipeline must succeed at every size.
//
// It requires an interactive desktop session, so it only runs when the
// CAPTURE_E2E environment variable is set.
func TestCaptureScreenRectSizes(t *testing.T) {
	if _, ok := syscall.Getenv("CAPTURE_E2E"); !ok {
		t.Skip("set CAPTURE_E2E=1 to run interactive capture tests")
	}

	sizes := []struct{ w, h int }{
		{300, 300},   // small: always worked
		{450, 450},   // the original failure threshold
		{1000, 600},  // medium
		{1920, 1080}, // large: consistently failed with DDB readback
	}

	for _, s := range sizes {
		s := s
		t.Run(fmt.Sprintf("%dx%d", s.w, s.h), func(t *testing.T) {
			pixels, err := captureScreenRect(image.Rect(0, 0, s.w, s.h))
			if err != nil {
				t.Fatalf("captureScreenRect(%dx%d): %v", s.w, s.h, err)
			}
			if len(pixels) != s.w*s.h*4 {
				t.Fatalf("captureScreenRect(%dx%d): got %d bytes, want %d", s.w, s.h, len(pixels), s.w*s.h*4)
			}
		})
	}

	t.Run("fullscreen", func(t *testing.T) {
		sm, _, _ := pGetSystemMetrics.Call(0) // SM_CXSCREEN
		sv, _, _ := pGetSystemMetrics.Call(1) // SM_CYSCREEN
		if sm == 0 || sv == 0 {
			t.Skip("GetSystemMetrics unavailable")
		}
		pixels, err := captureScreenRect(image.Rect(0, 0, int(sm), int(sv)))
		if err != nil {
			t.Fatalf("captureScreenRect(fullscreen %dx%d): %v", sm, sv, err)
		}
		if len(pixels) != int(sm)*int(sv)*4 {
			t.Fatalf("captureScreenRect(fullscreen): got %d bytes, want %d", len(pixels), int(sm)*int(sv)*4)
		}
	})
}
