//go:build windows

package desktop

import (
	"syscall"
	"unsafe"
)

var (
	user32        = syscall.NewLazyDLL("user32.dll")
	procSendInput = user32.NewProc("SendInput")
)

const (
	inputKeyboard         = 1
	keyEventFKeyUp        = 0x0002
	vkControl      uint16 = 0x11
	vkShift        uint16 = 0x10
	vkF12          uint16 = 0x7B
)

type kbdInput struct {
	wVk         uint16
	wScan       uint16
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

type input struct {
	type_ uint32
	ki    kbdInput
	_     [8]byte // padding: union must be 32 bytes to match MOUSEINPUT size on 64-bit
}

func sendKeybdInput(inputs []input) {
	if len(inputs) == 0 {
		return
	}
	procSendInput.Call(
		uintptr(len(inputs)),
		uintptr(unsafe.Pointer(&inputs[0])),
		uintptr(unsafe.Sizeof(input{})),
	)
}

func openDevTools() {
	// Send Ctrl+Shift+F12 — Wails Windows frontend listens for this accelerator.
	sendKeybdInput([]input{
		{type_: inputKeyboard, ki: kbdInput{wVk: vkControl}},
		{type_: inputKeyboard, ki: kbdInput{wVk: vkShift}},
		{type_: inputKeyboard, ki: kbdInput{wVk: vkF12}},
		{type_: inputKeyboard, ki: kbdInput{wVk: vkF12, dwFlags: keyEventFKeyUp}},
		{type_: inputKeyboard, ki: kbdInput{wVk: vkShift, dwFlags: keyEventFKeyUp}},
		{type_: inputKeyboard, ki: kbdInput{wVk: vkControl, dwFlags: keyEventFKeyUp}},
	})
}

func init() {
	openDevToolsImpl = openDevTools
}
