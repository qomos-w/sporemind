//go:build windows

package winx

import (
	"errors"
	"fmt"
	"image"
	"strings"
	"syscall"
	"unsafe"
)

// CursorPosition returns the current screen cursor position.
func (c *Capturer) CursorPosition() (image.Point, error) {
	var p point
	ret, _, _ := pGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	if ret == 0 {
		return image.Point{}, errors.New("GetCursorPos failed")
	}
	return image.Point{X: int(p.X), Y: int(p.Y)}, nil
}

// MoveCursor moves the cursor to (x, y) in screen coordinates.
func (c *Capturer) MoveCursor(x, y int) error {
	ret, _, _ := pSetCursorPos.Call(uintptr(int32(x)), uintptr(int32(y)))
	if ret == 0 {
		return errors.New("SetCursorPos failed")
	}
	c.TrackCursor(image.Point{X: x, Y: y})
	return nil
}

// Click performs a mouse click at (x, y) using the named button.
// Implemented as a single SendInput batch (absolute move → button-down →
// button-up) so the OS won't splice in real user input between the move
// and the click.
func (c *Capturer) Click(x, y int, button string) error {
	down, up, err := buttonFlags(button)
	if err != nil {
		return err
	}
	vx, vy, vw, vh := virtualScreen()
	if vw <= 0 || vh <= 0 {
		return errors.New("invalid virtual-screen dimensions")
	}
	events := []input{
		mouseAbsoluteMove(x, y, vx, vy, vw, vh),
		mouseFlags(down),
		mouseFlags(up),
	}
	if err := sendInputs(events); err != nil {
		return err
	}
	c.TrackCursor(image.Point{X: x, Y: y})
	return nil
}

// Scroll scrolls (dx, dy) at the given screen position. Positive dy = down.
// Per Win32, MOUSEEVENTF_WHEEL takes "WHEEL_DELTA*clicks", positive=up; we
// invert dy so "positive = scroll down" matches the user-facing API.
func (c *Capturer) Scroll(x, y, dx, dy int) error {
	if dx == 0 && dy == 0 {
		return nil
	}
	vx, vy, vw, vh := virtualScreen()
	if vw <= 0 || vh <= 0 {
		return errors.New("invalid virtual-screen dimensions")
	}
	events := []input{mouseAbsoluteMove(x, y, vx, vy, vw, vh)}
	if dy != 0 {
		events = append(events, mouseWheel(int32(-dy*120), false))
	}
	if dx != 0 {
		events = append(events, mouseWheel(int32(dx*120), true))
	}
	if err := sendInputs(events); err != nil {
		return err
	}
	c.TrackCursor(image.Point{X: x, Y: y})
	return nil
}

// buttonFlags maps "left"/"right"/"middle" (or "" → left) to down/up flags.
func buttonFlags(button string) (down, up uint32, err error) {
	switch strings.ToLower(button) {
	case "", "left":
		return moveLDown, moveLUp, nil
	case "right":
		return moveRDown, moveRUp, nil
	case "middle":
		return moveMDown, moveMUp, nil
	default:
		return 0, 0, fmt.Errorf("unknown button: %s", button)
	}
}

// Drag presses button at (fromX,fromY), interpolates to (toX,toY), and
// releases. Uses SendInput as a single batch so the OS won't interleave
// real user input. The 8-step interpolation is enough for most UI
// drag-and-drop / range-slider work without flooding games that filter
// fast inputs.
func (c *Capturer) Drag(fromX, fromY, toX, toY int, button string) error {
	down, up, err := buttonFlags(button)
	if err != nil {
		return err
	}

	vx, vy, vw, vh := virtualScreen()
	if vw <= 0 || vh <= 0 {
		return errors.New("invalid virtual-screen dimensions")
	}

	const steps = 8
	events := make([]input, 0, steps+4)
	events = append(events, mouseAbsoluteMove(fromX, fromY, vx, vy, vw, vh))
	events = append(events, mouseFlags(down))
	for i := 1; i < steps; i++ {
		ix := fromX + (toX-fromX)*i/steps
		iy := fromY + (toY-fromY)*i/steps
		events = append(events, mouseAbsoluteMove(ix, iy, vx, vy, vw, vh))
	}
	events = append(events, mouseAbsoluteMove(toX, toY, vx, vy, vw, vh))
	events = append(events, mouseFlags(up))

	if err := sendInputs(events); err != nil {
		return err
	}
	c.TrackCursor(image.Point{X: toX, Y: toY})
	return nil
}

// mouseAbsoluteMove builds an INPUT for an absolute virtual-desktop move.
// Win32 ABSOLUTE coords are 0..65535 normalised over the destination space;
// MOUSEEVENTF_VIRTUALDESK extends that space to the union of all monitors.
func mouseAbsoluteMove(x, y, vx, vy, vw, vh int) input {
	nx := int32(((int64(x-vx) * 65535) + int64(vw)/2) / int64(vw))
	ny := int32(((int64(y-vy) * 65535) + int64(vh)/2) / int64(vh))
	var ev input
	ev.Type = inputMouse
	mi := mouseInput{
		DX:      nx,
		DY:      ny,
		DWFlags: moveMove | moveAbsolute | moveVirtualDesk,
	}
	*(*mouseInput)(unsafe.Pointer(&ev.U[0])) = mi
	return ev
}

func mouseFlags(flags uint32) input {
	var ev input
	ev.Type = inputMouse
	mi := mouseInput{DWFlags: flags}
	*(*mouseInput)(unsafe.Pointer(&ev.U[0])) = mi
	return ev
}

// mouseWheel builds a SendInput-style wheel event. delta is in WHEEL_DELTA
// units (120 per click). horizontal=true issues MOUSEEVENTF_HWHEEL.
func mouseWheel(delta int32, horizontal bool) input {
	var ev input
	ev.Type = inputMouse
	flags := uint32(moveWheel)
	if horizontal {
		flags = moveHWheel
	}
	mi := mouseInput{
		MouseData: uint32(delta),
		DWFlags:   flags,
	}
	*(*mouseInput)(unsafe.Pointer(&ev.U[0])) = mi
	return ev
}

// virtualScreen returns the origin (x,y) and size (w,h) of the virtual
// desktop spanning every monitor. Used by SendInput-absolute paths.
func virtualScreen() (x, y, w, h int) {
	vx, _, _ := pGetSystemMetrics.Call(smXVirtScreen)
	vy, _, _ := pGetSystemMetrics.Call(smYVirtScreen)
	vw, _, _ := pGetSystemMetrics.Call(smCxVirtScrn)
	vh, _, _ := pGetSystemMetrics.Call(smCyVirtScrn)
	return int(int32(vx)), int(int32(vy)), int(int32(vw)), int(int32(vh))
}

// Type types a unicode string using SendInput with KEYEVENTF_UNICODE.
func (c *Capturer) Type(text string) error {
	if text == "" {
		return nil
	}
	runes := []rune(text)
	// each rune may need a surrogate pair; allocate worst case.
	events := make([]input, 0, len(runes)*4)
	for _, r := range runes {
		if r <= 0xFFFF {
			events = appendUnicodeKey(events, uint16(r))
		} else {
			r -= 0x10000
			hi := 0xD800 + uint16(r>>10)
			lo := 0xDC00 + uint16(r&0x3FF)
			events = appendUnicodeKey(events, hi)
			events = appendUnicodeKey(events, lo)
		}
	}
	return sendInputs(events)
}

func appendUnicodeKey(dst []input, code uint16) []input {
	var down, up input
	down.Type = inputKeyboard
	up.Type = inputKeyboard
	ki := keybdInput{WScan: code, DWFlags: keyUnicode}
	upi := keybdInput{WScan: code, DWFlags: keyUnicode | keyUp}
	*(*keybdInput)(unsafe.Pointer(&down.U[0])) = ki
	*(*keybdInput)(unsafe.Pointer(&up.U[0])) = upi
	return append(dst, down, up)
}

// KeyPress presses key with optional modifiers (ctrl/shift/alt).
func (c *Capturer) KeyPress(key string, modifiers ...string) error {
	vk, scan, useUnicode := lookupKey(key)
	if !useUnicode && vk == 0 {
		return fmt.Errorf("unknown key: %s", key)
	}
	mods, err := parseModifiers(modifiers)
	if err != nil {
		return err
	}

	events := make([]input, 0, (len(mods)+1)*2)
	for _, mv := range mods {
		events = appendVK(events, mv, false)
	}
	if useUnicode {
		events = appendUnicodeKey(events, scan)
	} else {
		events = appendVK(events, vk, false)
		events = appendVK(events, vk, true)
	}
	// release modifiers in reverse.
	for i := len(mods) - 1; i >= 0; i-- {
		events = appendVK(events, mods[i], true)
	}
	return sendInputs(events)
}

// KeyDown holds modifiers then presses key (down only). Caller pairs with KeyUp.
func (c *Capturer) KeyDown(key string, modifiers ...string) error {
	vk, scan, useUnicode := lookupKey(key)
	if !useUnicode && vk == 0 {
		return fmt.Errorf("unknown key: %s", key)
	}
	mods, err := parseModifiers(modifiers)
	if err != nil {
		return err
	}
	events := make([]input, 0, len(mods)+1)
	for _, mv := range mods {
		events = appendVK(events, mv, false)
	}
	if useUnicode {
		// Unicode path: send down only — no scan up.
		var down input
		down.Type = inputKeyboard
		ki := keybdInput{WScan: scan, DWFlags: keyUnicode}
		*(*keybdInput)(unsafe.Pointer(&down.U[0])) = ki
		events = append(events, down)
	} else {
		events = appendVK(events, vk, false)
	}
	return sendInputs(events)
}

// KeyUp releases key then releases modifiers in reverse order.
func (c *Capturer) KeyUp(key string, modifiers ...string) error {
	vk, scan, useUnicode := lookupKey(key)
	if !useUnicode && vk == 0 {
		return fmt.Errorf("unknown key: %s", key)
	}
	mods, err := parseModifiers(modifiers)
	if err != nil {
		return err
	}
	events := make([]input, 0, len(mods)+1)
	if useUnicode {
		var up input
		up.Type = inputKeyboard
		ki := keybdInput{WScan: scan, DWFlags: keyUnicode | keyUp}
		*(*keybdInput)(unsafe.Pointer(&up.U[0])) = ki
		events = append(events, up)
	} else {
		events = appendVK(events, vk, true)
	}
	for i := len(mods) - 1; i >= 0; i-- {
		events = appendVK(events, mods[i], true)
	}
	return sendInputs(events)
}

// parseModifiers converts modifier names to VK codes.
func parseModifiers(modifiers []string) ([]uint16, error) {
	if len(modifiers) == 0 {
		return nil, nil
	}
	mods := make([]uint16, 0, len(modifiers))
	for _, m := range modifiers {
		var mv uint16
		switch strings.ToLower(m) {
		case "ctrl":
			mv = 0x11
		case "shift":
			mv = 0x10
		case "alt":
			mv = 0x12
		case "win":
			mv = 0x5B
		default:
			return nil, fmt.Errorf("unknown modifier: %s", m)
		}
		mods = append(mods, mv)
	}
	return mods, nil
}

func appendVK(dst []input, vk uint16, up bool) []input {
	var ev input
	ev.Type = inputKeyboard
	ki := keybdInput{WVK: vk}
	if up {
		ki.DWFlags = keyUp
	}
	*(*keybdInput)(unsafe.Pointer(&ev.U[0])) = ki
	return append(dst, ev)
}

func sendInputs(events []input) error {
	if len(events) == 0 {
		return nil
	}
	cb := uint32(unsafe.Sizeof(input{}))
	ret, _, _ := pSendInput.Call(uintptr(uint32(len(events))),
		uintptr(unsafe.Pointer(&events[0])), uintptr(cb))
	if ret == 0 {
		return errors.New("SendInput returned 0")
	}
	return nil
}

// lookupKey maps a key name to (VK code, unicode-scan, isUnicode).
// Returns useUnicode=true with the rune in scan when no VK match.
func lookupKey(name string) (vk uint16, scan uint16, useUnicode bool) {
	switch strings.ToLower(name) {
	case "return", "enter":
		return 0x0D, 0, false
	case "tab":
		return 0x09, 0, false
	case "escape", "esc":
		return 0x1B, 0, false
	case "backspace", "back":
		return 0x08, 0, false
	case "delete", "del":
		return 0x2E, 0, false
	case "space":
		return 0x20, 0, false
	case "up":
		return 0x26, 0, false
	case "down":
		return 0x28, 0, false
	case "left":
		return 0x25, 0, false
	case "right":
		return 0x27, 0, false
	case "home":
		return 0x24, 0, false
	case "end":
		return 0x23, 0, false
	case "pageup", "pgup":
		return 0x21, 0, false
	case "pagedown", "pgdn":
		return 0x22, 0, false
	case "ctrl":
		return 0x11, 0, false
	case "shift":
		return 0x10, 0, false
	case "alt":
		return 0x12, 0, false
	case "win":
		return 0x5B, 0, false
	}
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "f") && len(lower) <= 3 {
		n := 0
		for _, c := range lower[1:] {
			if c < '0' || c > '9' {
				n = 0
				break
			}
			n = n*10 + int(c-'0')
		}
		if n >= 1 && n <= 24 {
			return uint16(0x6F + n), 0, false // VK_F1 = 0x70
		}
	}
	// Single character: ask VkKeyScan, fallback to unicode.
	runes := []rune(name)
	if len(runes) == 1 {
		r := runes[0]
		ret, _, _ := pVkKeyScanW.Call(uintptr(uint16(r)))
		lo := uint16(ret & 0xFFFF)
		if lo != 0xFFFF {
			vk := uint16(lo & 0xFF)
			// Shift state is encoded in the high byte but we ignore it —
			// caller passes modifiers separately.
			return vk, 0, false
		}
		return 0, uint16(r), true
	}
	return 0, 0, false
}

// SetClipboard puts text into the Windows clipboard.
func (c *Capturer) SetClipboard(text string) error {
	ret, _, _ := pOpenClipboard.Call(0)
	if ret == 0 {
		return errors.New("OpenClipboard failed")
	}
	defer pCloseClipboard.Call()

	pEmptyClipboard.Call()

	utf16, err := syscall.UTF16FromString(text)
	if err != nil {
		return err
	}
	sz := uintptr(len(utf16) * 2)
	hMem, _, _ := pGlobalAlloc.Call(gmemMoveable, sz)
	if hMem == 0 {
		return errors.New("GlobalAlloc failed")
	}
	dst, _, _ := pGlobalLock.Call(hMem)
	if dst == 0 {
		pGlobalFree.Call(hMem)
		return errors.New("GlobalLock failed")
	}
	src := unsafe.Pointer(&utf16[0])
	copyBytes(dst, src, int(sz))
	pGlobalUnlock.Call(hMem)
	if set, _, _ := pSetClipboardData.Call(cfUnicode, hMem); set == 0 {
		pGlobalFree.Call(hMem)
		return errors.New("SetClipboardData failed")
	}
	return nil
}

// GetClipboard reads text from the clipboard.
func (c *Capturer) GetClipboard() (string, error) {
	ret, _, _ := pOpenClipboard.Call(0)
	if ret == 0 {
		return "", errors.New("OpenClipboard failed")
	}
	defer pCloseClipboard.Call()
	hData, _, _ := pGetClipboardData.Call(cfUnicode)
	if hData == 0 {
		return "", nil
	}
	ptrRaw, _, _ := pGlobalLock.Call(hData)
	if ptrRaw == 0 {
		return "", errors.New("GlobalLock failed")
	}
	defer pGlobalUnlock.Call(hData)

	// Read UTF-16 string up to NUL. ptrRaw is a GlobalLock OS pointer, not
	// GC-managed — viewed as a bounded uint16 slice and stopped at first NUL.
	const maxRunes = 1 << 20
	view := unsafe.Slice((*uint16)(ptrFromUintptr(ptrRaw)), maxRunes)
	n := 0
	for n < len(view) && view[n] != 0 {
		n++
	}
	return syscall.UTF16ToString(view[:n]), nil
}

// copyBytes copies n bytes from src to dst. dst is an OS pointer (e.g. GlobalLock),
// not GC-managed, so the unsafe.Pointer round-trip is safe.
func copyBytes(dst uintptr, src unsafe.Pointer, n int) {
	d := unsafe.Slice((*byte)(ptrFromUintptr(dst)), n)
	s := unsafe.Slice((*byte)(src), n)
	copy(d, s)
}
