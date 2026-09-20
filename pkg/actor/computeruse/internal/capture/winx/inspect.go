//go:build windows

package winx

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
	"unsafe"

	"github.com/qomos-w/sporemind/pkg/actor/computeruse/internal/capture"
)

// Processes enumerates running processes via Toolhelp32Snapshot. The
// snapshot covers all processes (no PROCESS_VM_READ rights required).
// ExePath uses GetModuleFileNameEx and may be empty for privileged or
// 32/64-bit-mismatch processes; that is expected, not an error.
func (c *Capturer) Processes(nameContains string, pid int) ([]capture.ProcessInfo, error) {
	snap, _, _ := pCreateToolhelp32Snapshot.Call(th32CSSnapProcess, 0)
	if snap == 0 || snap == ^uintptr(0) {
		return nil, errors.New("CreateToolhelp32Snapshot failed")
	}
	defer pCloseHandle.Call(snap)

	var pe processEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	ret, _, _ := pProcess32FirstW.Call(snap, uintptr(unsafe.Pointer(&pe)))
	if ret == 0 {
		return nil, errors.New("Process32FirstW failed")
	}

	needle := strings.ToLower(strings.TrimSpace(nameContains))
	var out []capture.ProcessInfo
	for {
		name := syscall.UTF16ToString(pe.ExeFile[:])
		include := true
		if pid > 0 && int(pe.ProcessID) != pid {
			include = false
		}
		if include && needle != "" && !strings.Contains(strings.ToLower(name), needle) {
			include = false
		}
		if include {
			out = append(out, capture.ProcessInfo{
				PID:       int(pe.ProcessID),
				ParentPID: int(pe.ParentProcessID),
				Name:      name,
				ExePath:   getProcessExePath(pe.ProcessID),
			})
		}
		nextRet, _, _ := pProcess32NextW.Call(snap, uintptr(unsafe.Pointer(&pe)))
		if nextRet == 0 {
			break
		}
	}
	return out, nil
}

// FilteredWindows runs EnumWindows and applies the filter in one pass.
// IncludeInvisible bypasses the visibility + app-window heuristics.
func (c *Capturer) FilteredWindows(f capture.WindowFilter) ([]capture.WindowInfo, error) {
	// Hit-test path short-circuits enumeration.
	if f.AtPoint != nil {
		hwnd := windowFromScreenPoint(f.AtPoint.X, f.AtPoint.Y)
		if hwnd == 0 {
			return nil, nil
		}
		w, err := windowInfo(hwnd, true)
		if err != nil {
			return nil, err
		}
		if !filterMatches(w, f) {
			return nil, nil
		}
		return []capture.WindowInfo{w}, nil
	}
	// Foreground-only path.
	if f.ForegroundOnly {
		hwnd := getForegroundWindow()
		if hwnd == 0 {
			return nil, nil
		}
		w, err := windowInfo(hwnd, true)
		if err != nil {
			return nil, err
		}
		if !filterMatches(w, f) {
			return nil, nil
		}
		return []capture.WindowInfo{w}, nil
	}

	var list []capture.WindowInfo
	cb := syscall.NewCallback(func(hwnd HWND, _ uintptr) uintptr {
		visible := isWindowVisible(hwnd)
		if !f.IncludeInvisible && (!visible || !isValidAppWindow(hwnd)) {
			return 1
		}
		r, err := getWindowRect(hwnd)
		if err != nil || (!f.IncludeInvisible && (r.Dx() < 10 || r.Dy() < 10)) {
			return 1
		}
		pid := getWindowPID(hwnd)
		w := capture.WindowInfo{
			ID:          int(hwnd),
			Title:       getWindowText(hwnd),
			Bounds:      r,
			IsVisible:   visible,
			ProcessName: getProcessName(pid),
			PID:         int(pid),
		}
		if filterMatches(w, f) {
			list = append(list, w)
		}
		return 1
	})
	pEnumWindows.Call(cb, 0)
	return list, nil
}

func filterMatches(w capture.WindowInfo, f capture.WindowFilter) bool {
	if f.PID > 0 && w.PID != f.PID {
		return false
	}
	if f.ProcessName != "" && !strings.Contains(strings.ToLower(w.ProcessName), strings.ToLower(f.ProcessName)) {
		return false
	}
	if f.TitlePattern != "" && !strings.Contains(strings.ToLower(w.Title), strings.ToLower(f.TitlePattern)) {
		return false
	}
	return true
}

// WindowInfo returns detailed info for one HWND.
func (c *Capturer) WindowInfo(windowID int) (capture.DetailedWindowInfo, error) {
	hwnd := HWND(uintptr(windowID))
	if hwnd == 0 {
		return capture.DetailedWindowInfo{}, errors.New("invalid windowID")
	}
	wi, err := windowInfo(hwnd, false)
	if err != nil {
		return capture.DetailedWindowInfo{}, err
	}
	ex := getWindowLong(hwnd, gwlExStyle)
	return capture.DetailedWindowInfo{
		WindowInfo:   wi,
		State:        getWindowState(hwnd),
		ClassName:    getClassName(hwnd),
		IsForeground: getForegroundWindow() == hwnd,
		IsTopmost:    ex&wsExTopmost != 0,
	}, nil
}

// windowInfo builds a WindowInfo for hwnd without filtering, returning
// errors only for fatal failures (rect lookup). validateApp triggers
// the "drop non-app windows" heuristic.
func windowInfo(hwnd HWND, _ bool) (capture.WindowInfo, error) {
	r, err := getWindowRect(hwnd)
	if err != nil {
		return capture.WindowInfo{}, fmt.Errorf("GetWindowRect: %w", err)
	}
	pid := getWindowPID(hwnd)
	return capture.WindowInfo{
		ID:          int(hwnd),
		Title:       getWindowText(hwnd),
		Bounds:      r,
		IsVisible:   isWindowVisible(hwnd),
		ProcessName: getProcessName(pid),
		PID:         int(pid),
	}, nil
}

// FocusWindow brings hwnd to the foreground. Windows refuses
// SetForegroundWindow when called from a non-foreground process unless
// thread-input is attached; this implementation tries that bypass.
func (c *Capturer) FocusWindow(windowID int) error {
	hwnd := HWND(uintptr(windowID))
	if hwnd == 0 {
		return errors.New("invalid windowID")
	}
	// Restore if minimized.
	if iconic, _, _ := pIsIconic.Call(uintptr(hwnd)); iconic != 0 {
		pShowWindow.Call(uintptr(hwnd), swRestore)
	}
	ret, _, _ := pSetForegroundWindow.Call(uintptr(hwnd))
	if ret != 0 {
		return nil
	}
	// Bypass: attach to the target window's thread input.
	curTID, _, _ := pGetCurrentThreadID.Call()
	var targetPID uint32
	targetTID, _, _ := pGetWindowThreadProcessID.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&targetPID)))
	if targetTID == curTID || targetTID == 0 {
		return errors.New("SetForegroundWindow failed")
	}
	pAttachThreadInput.Call(curTID, targetTID, 1)
	pSetForegroundWindow.Call(uintptr(hwnd))
	pAttachThreadInput.Call(curTID, targetTID, 0)
	if getForegroundWindow() == hwnd {
		return nil
	}
	// Last-ditch: at least make it visible.
	pShowWindow.Call(uintptr(hwnd), swShow)
	if getForegroundWindow() == hwnd {
		return nil
	}
	return errors.New("could not bring window to foreground")
}

// =========================================================================
// (cursor visibility is now driven entirely by CaptureOptions.IncludeCursor;
// hiding the system cursor is intentionally out of scope.)

// SetWindowState applies a show-state command to a window.
// state: "minimize" | "maximize" | "restore".
func (c *Capturer) SetWindowState(windowID int, state string) error {
	hwnd := HWND(uintptr(windowID))
	if hwnd == 0 {
		return errors.New("invalid windowID")
	}
	var cmd uintptr
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "minimize":
		cmd = swMinimize
	case "maximize":
		cmd = swMaximize
	case "restore":
		cmd = swRestore
	default:
		return fmt.Errorf("unknown window state: %s", state)
	}
	pShowWindow.Call(uintptr(hwnd), cmd)
	return nil
}

// MoveWindow moves and/or resizes a window. (0,0) for (x,y) keeps the
// current position; (0,0) for (w,h) keeps the current size. To force
// move-to-origin or zero-size, set the other dimension explicitly.
func (c *Capturer) MoveWindow(windowID int, x, y, width, height int) error {
	hwnd := HWND(uintptr(windowID))
	if hwnd == 0 {
		return errors.New("invalid windowID")
	}
	flags := uintptr(swpNoZOrder | swpNoActivate)
	if x == 0 && y == 0 {
		flags |= swpNoMove
	}
	if width == 0 && height == 0 {
		flags |= swpNoSize
	}
	ret, _, _ := pSetWindowPos.Call(
		uintptr(hwnd), 0,
		uintptr(int32(x)), uintptr(int32(y)),
		uintptr(int32(width)), uintptr(int32(height)),
		flags,
	)
	if ret == 0 {
		return errors.New("SetWindowPos failed")
	}
	return nil
}

// CloseWindow sends WM_CLOSE to ask the window to shut down gracefully.
// The target may refuse (e.g. an editor with unsaved changes that pops a
// confirmation dialog); use a process-level kill if a hard close is needed.
func (c *Capturer) CloseWindow(windowID int) error {
	hwnd := HWND(uintptr(windowID))
	if hwnd == 0 {
		return errors.New("invalid windowID")
	}
	pSendMessageW.Call(uintptr(hwnd), wmClose, 0, 0)
	return nil
}