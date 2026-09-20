//go:build windows

package winx

import (
	"errors"
	"fmt"
	"image"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	ole "github.com/go-ole/go-ole"

	"github.com/qomos-w/sporemind/pkg/actor/computeruse/internal/capture"
)

// Minimal IUIAutomation v8 subset. We only call a handful of methods, so
// we declare partial vtable structs that include just enough function
// pointer slots up to the methods we actually invoke. Order MUST match
// UIAutomationClient.h exactly.
//
// All of this code runs exclusively inside the UIA helper subprocess. The
// host process proxies through uiaClient and never touches COM pointers.

var (
	// CLSID_CUIAutomation: ff48dba4-60ef-4201-aa87-54103eef594e
	clsidCUIAutomation = &ole.GUID{
		Data1: 0xff48dba4, Data2: 0x60ef, Data3: 0x4201,
		Data4: [8]byte{0xaa, 0x87, 0x54, 0x10, 0x3e, 0xef, 0x59, 0x4e},
	}
	// IID_IUIAutomation: 30cbe57d-d9d0-452a-ab13-7ac5ac4825ee
	iidIUIAutomation = &ole.GUID{
		Data1: 0x30cbe57d, Data2: 0xd9d0, Data3: 0x452a,
		Data4: [8]byte{0xab, 0x13, 0x7a, 0xc5, 0xac, 0x48, 0x25, 0xee},
	}
)

// Vtable slots (zero-indexed, IUnknown occupies 0..2). Values verified by
// runtime probing against uiautomationcore.dll on Windows 10/11 x64.
const (
	uiaGetRootElement    = 5
	uiaElementFromHandle = 6
	uiaGetFocusedElement = 8

	// IUIAutomationElement methods (after IUnknown's 3).
	elemSetFocus                    = 3
	elemGetRuntimeID                = 4
	elemFindFirst                   = 5
	elemFindAll                     = 6
	elemGetCurrentPropertyValue     = 10
	elemGetCurrentPattern           = 16
	elemCurrentProcessID            = 20
	elemCurrentControlType          = 21
	elemCurrentLocalizedControlType = 22
	elemCurrentName                 = 23
	elemCurrentAcceleratorKey       = 27
	elemCurrentAccessKey            = 28
	elemCurrentHasKeyboardFocus     = 31
	elemCurrentIsKeyboardFocusable  = 32
	elemCurrentIsEnabled            = 33
	elemCurrentAutomationID         = 35
	elemCurrentClassName            = 30
	elemCurrentBoundingRectangle    = 43

	// Pattern IDs.
	patternInvoke = 10000
	patternValue  = 10002

	// IUIAutomationInvokePattern Invoke method index (after IUnknown).
	invokePatternInvoke = 3

	// IUIAutomationValuePattern SetValue method index (after IUnknown).
	valuePatternSetValue = 3

	// UIA property IDs for GetCurrentPropertyValue fallback.
	propChildren = 30007
)

// uiaState holds the singleton IUIAutomation instance plus an LRU element
// cache. Access is gated through onCOMThread; the cache itself is
// protected by its own lock since lookups can race with eviction.
type uiaState struct {
	auto    uintptr // *IUIAutomation
	initErr error

	cacheMu sync.Mutex
	cache   map[string]uintptr // ID -> *IUIAutomationElement
	order   []string           // LRU order; tail = most recent
	counter atomic.Uint64
}

var (
	uia     *uiaState
	uiaOnce sync.Once
)

const elementCacheMax = 1024

// requireHelper refuses to run the direct COM path from the host process.
// All UIA COM calls must happen inside the helper subprocess.
func requireHelper() error {
	if !uiaHelperMode.Load() {
		return errors.New("UIA COM calls are only allowed in the helper process")
	}
	return nil
}

func getUIA() (*uiaState, error) {
	if err := requireHelper(); err != nil {
		return nil, err
	}
	uiaOnce.Do(func() {
		uia = &uiaState{cache: make(map[string]uintptr)}
		uia.initErr = onCOMThread(func() {
			unk, err := ole.CreateInstance(clsidCUIAutomation, iidIUIAutomation)
			if err != nil {
				uia.initErr = fmt.Errorf("CoCreateInstance(CUIAutomation): %w", err)
				return
			}
			uia.auto = uintptr(unsafe.Pointer(unk))
		})
	})
	if uia.initErr != nil {
		return nil, uia.initErr
	}
	if uia.auto == 0 {
		return nil, errors.New("UIA not available")
	}
	return uia, nil
}

// vtableFn returns the function pointer at the given slot of the COM
// interface pointed to by p. p must be a valid IUnknown-derived COM
// pointer obtained on the apartment thread.
func vtableFn(p uintptr, slot int) uintptr {
	if p == 0 {
		return 0
	}
	vtbl := *(*uintptr)(ptrFromUintptr(p))
	return *(*uintptr)(unsafe.Add(ptrFromUintptr(vtbl), uintptr(slot)*unsafe.Sizeof(uintptr(0))))
}

// callCOM invokes vtable slot with the given args. First arg is implicit
// (this pointer). HRESULT is returned as int32.
func callCOM(p uintptr, slot int, args ...uintptr) int32 {
	fn := vtableFn(p, slot)
	if fn == 0 {
		return -1
	}
	full := append([]uintptr{p}, args...)
	ret, _, _ := syscall.SyscallN(fn, full...)
	return int32(ret)
}

// release calls IUnknown::Release (slot 2). Must run on COM thread.
func release(p uintptr) {
	if p == 0 {
		return
	}
	callCOM(p, 2)
}

// uiaGetBSTRProp reads a BSTR property and returns it as a Go string.
func uiaGetBSTRProp(obj uintptr, slot int) string {
	var bstr *uint16
	hr := callCOM(obj, slot, uintptr(unsafe.Pointer(&bstr)))
	if hr != 0 || bstr == nil {
		return ""
	}
	// Guard against invalid BSTR pointers (e.g. method returned a
	// non-BSTR value into the out param).
	ptr := uintptr(unsafe.Pointer(bstr))
	if ptr < 0x10000 {
		return ""
	}
	defer ole.SysFreeString((*int16)(unsafe.Pointer(bstr)))
	return ole.BstrToString(bstr)
}

// uiaGetBOOLProp reads a VARIANT_BOOL property.
func uiaGetBOOLProp(obj uintptr, slot int) bool {
	var v int16
	hr := callCOM(obj, slot, uintptr(unsafe.Pointer(&v)))
	return hr == 0 && v != 0
}

// uiaGetInt32Prop reads a 32-bit int property.
func uiaGetInt32Prop(obj uintptr, slot int) int32 {
	var v int32
	callCOM(obj, slot, uintptr(unsafe.Pointer(&v)))
	return v
}

// uiaGetRectProp reads a RECT-typed property (used for BoundingRectangle).
func uiaGetRectProp(obj uintptr, slot int) image.Rectangle {
	var r rect
	hr := callCOM(obj, slot, uintptr(unsafe.Pointer(&r)))
	if hr != 0 {
		return image.Rectangle{}
	}
	return image.Rect(int(r.Left), int(r.Top), int(r.Right), int(r.Bottom))
}

// controlTypeName maps UIA ControlTypeId to a short string. The full
// list is in UIAutomationCore.h; we only name the common ones.
func controlTypeName(id int32) string {
	switch id {
	case 50000:
		return "button"
	case 50001:
		return "calendar"
	case 50002:
		return "checkbox"
	case 50003:
		return "combobox"
	case 50004:
		return "edit"
	case 50005:
		return "hyperlink"
	case 50006:
		return "image"
	case 50007:
		return "listitem"
	case 50008:
		return "list"
	case 50009:
		return "menu"
	case 50010:
		return "menubar"
	case 50011:
		return "menuitem"
	case 50012:
		return "progressbar"
	case 50013:
		return "radiobutton"
	case 50014:
		return "scrollbar"
	case 50015:
		return "slider"
	case 50016:
		return "spinner"
	case 50017:
		return "statusbar"
	case 50018:
		return "tab"
	case 50019:
		return "tabitem"
	case 50020:
		return "text"
	case 50021:
		return "toolbar"
	case 50022:
		return "tooltip"
	case 50023:
		return "tree"
	case 50024:
		return "treeitem"
	case 50025:
		return "custom"
	case 50026:
		return "group"
	case 50027:
		return "thumb"
	case 50028:
		return "datagrid"
	case 50029:
		return "dataitem"
	case 50030:
		return "document"
	case 50031:
		return "splitbutton"
	case 50032:
		return "window"
	case 50033:
		return "pane"
	case 50034:
		return "header"
	case 50035:
		return "headeritem"
	case 50036:
		return "table"
	case 50037:
		return "titlebar"
	case 50038:
		return "separator"
	case 50039:
		return "semanticzoom"
	case 50040:
		return "appbar"
	}
	return fmt.Sprintf("controltype:%d", id)
}

// snapshotElement reads the common properties of an IUIAutomationElement
// into an ElementNode. Caller owns the element; this does NOT release.
func snapshotElement(elem uintptr) capture.ElementNode {
	return capture.ElementNode{
		AutomationID:        uiaGetBSTRProp(elem, elemCurrentAutomationID),
		Name:                uiaGetBSTRProp(elem, elemCurrentName),
		ClassName:           uiaGetBSTRProp(elem, elemCurrentClassName),
		ControlType:         controlTypeName(uiaGetInt32Prop(elem, elemCurrentControlType)),
		Bounds:              uiaGetRectProp(elem, elemCurrentBoundingRectangle),
		IsEnabled:           uiaGetBOOLProp(elem, elemCurrentIsEnabled),
		IsKeyboardFocusable: uiaGetBOOLProp(elem, elemCurrentIsKeyboardFocusable),
		HasKeyboardFocus:    uiaGetBOOLProp(elem, elemCurrentHasKeyboardFocus),
		Value:               elementValue(elem),
	}
}

// elementValue tries IUIAutomationValuePattern::CurrentValue.
func elementValue(elem uintptr) string {
	var patt uintptr
	hr := callCOM(elem, elemGetCurrentPattern,
		uintptr(patternValue),
		uintptr(unsafe.Pointer(&patt)),
	)
	if hr != 0 || patt == 0 {
		return ""
	}
	defer release(patt)
	// IUIAutomationValuePattern::CurrentValue is slot 4 (after IUnknown's 3
	// + SetValue at 3 + CurrentValue at 4).
	var bstr *uint16
	hr = callCOM(patt, 4, uintptr(unsafe.Pointer(&bstr)))
	if hr != 0 || bstr == nil {
		return ""
	}
	if uintptr(unsafe.Pointer(bstr)) < 0x10000 {
		return ""
	}
	defer ole.SysFreeString((*int16)(unsafe.Pointer(bstr)))
	return ole.BstrToString(bstr)
}

// cacheElement assigns a fresh ID and stores the element. Caller transfers
// ownership of the COM pointer to the cache. Evicts LRU when full.
func (s *uiaState) cacheElement(elem uintptr) string {
	id := fmt.Sprintf("e%d", s.counter.Add(1))
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if len(s.order) >= elementCacheMax {
		// Evict oldest.
		old := s.order[0]
		s.order = s.order[1:]
		if p, ok := s.cache[old]; ok {
			delete(s.cache, old)
			// Schedule release on COM thread; ignore error path.
			_ = onCOMThread(func() { release(p) })
		}
	}
	s.cache[id] = elem
	s.order = append(s.order, id)
	return id
}

func (s *uiaState) lookupElement(id string) uintptr {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	return s.cache[id]
}

// enumChildWindows calls fn for each child window of parent (0 = all
// top-level windows). Returns the collected handles.
func enumChildWindows(parent uintptr) []uintptr {
	user32 := syscall.NewLazyDLL("user32.dll")
	proc := user32.NewProc("EnumChildWindows")
	var handles []uintptr
	cb := syscall.NewCallback(func(hwnd, lparam uintptr) uintptr {
		handles = append(handles, hwnd)
		return 1
	})
	proc.Call(parent, cb, 0)
	return handles
}

// enumTopWindows enumerates top-level windows using EnumWindows.
func enumTopWindows() []uintptr {
	user32 := syscall.NewLazyDLL("user32.dll")
	proc := user32.NewProc("EnumWindows")
	isVisible := user32.NewProc("IsWindowVisible")
	var handles []uintptr
	cb := syscall.NewCallback(func(hwnd, lparam uintptr) uintptr {
		if v, _, _ := isVisible.Call(hwnd); v != 0 {
			handles = append(handles, hwnd)
		}
		return 1
	})
	proc.Call(cb, 0)
	return handles
}

// walkTreeWin32 enumerates windows via Win32 API and creates UIA elements
// from their handles. This avoids the unreliable IUIAutomation TreeWalker
// and condition-based methods. depth 0 = root/window only, depth 1 adds
// children, etc.
func walkTreeWin32(auto uintptr, rootHwnd uintptr, depth, maxDepth int, filter func(capture.ElementNode) bool, sink func(elem uintptr, node capture.ElementNode), limitRemaining *int) {
	if *limitRemaining <= 0 || depth > maxDepth {
		return
	}

	var handles []uintptr
	if rootHwnd == 0 {
		handles = enumTopWindows()
	} else {
		handles = []uintptr{rootHwnd}
	}

	for _, hwnd := range handles {
		if *limitRemaining <= 0 {
			return
		}
		var elem uintptr
		hr := callCOM(auto, uiaElementFromHandle,
			hwnd,
			uintptr(unsafe.Pointer(&elem)),
		)
		if hr != 0 || elem == 0 {
			continue
		}

		node := snapshotElement(elem)
		node.HWND = hwnd
		accepted := filter == nil || filter(node)
		if accepted {
			sink(elem, node)
			*limitRemaining--
		} else {
			release(elem)
		}

		if *limitRemaining <= 0 || depth >= maxDepth {
			continue
		}

		// Enumerate children recursively
		childHwnds := enumChildWindows(hwnd)
		for _, childHwnd := range childHwnds {
			if *limitRemaining <= 0 {
				break
			}
			var childElem uintptr
			hr := callCOM(auto, uiaElementFromHandle,
				childHwnd,
				uintptr(unsafe.Pointer(&childElem)),
			)
			if hr != 0 || childElem == 0 {
				continue
			}
			childNode := snapshotElement(childElem)
			childNode.HWND = childHwnd
			if filter == nil || filter(childNode) {
				sink(childElem, childNode)
				*limitRemaining--
			} else {
				release(childElem)
			}
		}
	}
}

func buildElementFilter(f capture.ElementFilter) func(capture.ElementNode) bool {
	needName := strings.ToLower(strings.TrimSpace(f.NameContains))
	needType := strings.ToLower(strings.TrimSpace(f.ControlType))
	if needName == "" && needType == "" {
		return nil
	}
	return func(n capture.ElementNode) bool {
		if needName != "" {
			haystack := strings.ToLower(n.Name + " " + n.AutomationID)
			if !strings.Contains(haystack, needName) {
				return false
			}
		}
		if needType != "" && !strings.EqualFold(n.ControlType, needType) {
			return false
		}
		return true
	}
}

// --- Host-side entry points (proxied to the helper subprocess) -----------

// ListElements walks the UI tree rooted at f.WindowID (or desktop if 0),
// via the isolated UIA helper subprocess.
func (c *Capturer) ListElements(f capture.ElementFilter) ([]capture.ElementNode, error) {
	return c.uia.ListElements(f)
}

// ElementInfo returns the latest snapshot of the cached element.
func (c *Capturer) ElementInfo(elementID string) (capture.ElementNode, error) {
	return c.uia.ElementInfo(elementID)
}

// ClickElement tries Invoke in the helper, then falls back to a coordinate
// click on the bounding rectangle's centre (executed in the host process).
func (c *Capturer) ClickElement(elementID, button string) error {
	return c.uia.ClickElement(elementID, button, c.Click)
}

// FocusElement requests keyboard focus on the element.
func (c *Capturer) FocusElement(elementID string) error {
	return c.uia.FocusElement(elementID)
}

// SetElementValue replaces the value of a value-pattern element.
func (c *Capturer) SetElementValue(elementID, value string) error {
	return c.uia.SetElementValue(elementID, value)
}

// --- Helper-side operations (run inside the helper subprocess) -----------

// uiaHelperList enumerates the tree and caches elements, returning nodes
// with helper-local IDs.
func uiaHelperList(f capture.ElementFilter) ([]capture.ElementNode, error) {
	s, err := getUIA()
	if err != nil {
		return nil, err
	}
	maxDepth := f.MaxDepth
	if maxDepth <= 0 || maxDepth > 8 {
		maxDepth = 8
	}
	maxResults := f.MaxResults
	if maxResults <= 0 {
		maxResults = 256
	}
	filter := buildElementFilter(f)

	var out []capture.ElementNode
	rootOK := false
	errOut := onCOMThread(func() {
		rootHwnd := uintptr(f.WindowID)
		// Validate the root window exists (or use 0 for desktop)
		if rootHwnd != 0 {
			var root uintptr
			hr := callCOM(s.auto, uiaElementFromHandle,
				rootHwnd,
				uintptr(unsafe.Pointer(&root)),
			)
			if hr != 0 || root == 0 {
				return
			}
			release(root)
		}
		rootOK = true

		remaining := maxResults
		walkTreeWin32(s.auto, rootHwnd, 0, maxDepth, filter, func(elem uintptr, node capture.ElementNode) {
			id := s.cacheElement(elem)
			node.ID = id
			out = append(out, node)
		}, &remaining)
	})
	if errOut != nil {
		return nil, errOut
	}
	if !rootOK {
		return nil, errors.New("uia: could not resolve root element (window may be closed or UIA unavailable)")
	}
	if out == nil {
		out = []capture.ElementNode{}
	}
	return out, nil
}

// uiaHelperInfo snapshots a cached element.
func uiaHelperInfo(elementID string) (capture.ElementNode, error) {
	s, err := getUIA()
	if err != nil {
		return capture.ElementNode{}, err
	}
	elem := s.lookupElement(elementID)
	if elem == 0 {
		return capture.ElementNode{}, fmt.Errorf("element not found: %s", elementID)
	}
	var node capture.ElementNode
	if err := onCOMThread(func() {
		node = snapshotElement(elem)
		node.ID = elementID
	}); err != nil {
		return capture.ElementNode{}, err
	}
	return node, nil
}

// uiaHelperClick tries InvokePattern; when the element has no invokable
// pattern it returns the element snapshot so the host can coordinate-click
// its centre.
func uiaHelperClick(elementID string) (fallback bool, node *capture.ElementNode, err error) {
	s, err := getUIA()
	if err != nil {
		return false, nil, err
	}
	elem := s.lookupElement(elementID)
	if elem == 0 {
		return false, nil, fmt.Errorf("element not found: %s", elementID)
	}
	invoked := false
	errOut := onCOMThread(func() {
		var patt uintptr
		hr := callCOM(elem, elemGetCurrentPattern,
			uintptr(patternInvoke),
			uintptr(unsafe.Pointer(&patt)),
		)
		if hr == 0 && patt != 0 {
			defer release(patt)
			if callCOM(patt, invokePatternInvoke) == 0 {
				invoked = true
			}
		}
	})
	if errOut != nil {
		return false, nil, errOut
	}
	if invoked {
		return false, nil, nil
	}
	var snap capture.ElementNode
	if err := onCOMThread(func() {
		snap = snapshotElement(elem)
		snap.ID = elementID
	}); err != nil {
		return false, nil, err
	}
	return true, &snap, nil
}

// uiaHelperFocus requests keyboard focus on a cached element.
func uiaHelperFocus(elementID string) error {
	s, err := getUIA()
	if err != nil {
		return err
	}
	elem := s.lookupElement(elementID)
	if elem == 0 {
		return fmt.Errorf("element not found: %s", elementID)
	}
	var hr int32
	if err := onCOMThread(func() {
		hr = callCOM(elem, elemSetFocus)
	}); err != nil {
		return err
	}
	if hr != 0 {
		return fmt.Errorf("SetFocus hr=0x%x", uint32(hr))
	}
	return nil
}

// uiaHelperSetValue replaces the value of a value-pattern element.
func uiaHelperSetValue(elementID, value string) error {
	s, err := getUIA()
	if err != nil {
		return err
	}
	elem := s.lookupElement(elementID)
	if elem == 0 {
		return fmt.Errorf("element not found: %s", elementID)
	}
	bstr := ole.SysAllocStringLen(value)
	if bstr == nil {
		return errors.New("SysAllocStringLen failed")
	}
	defer ole.SysFreeString(bstr)

	var setErr error
	if err := onCOMThread(func() {
		var patt uintptr
		hr := callCOM(elem, elemGetCurrentPattern,
			uintptr(patternValue),
			uintptr(unsafe.Pointer(&patt)),
		)
		if hr != 0 || patt == 0 {
			setErr = errors.New("element does not support ValuePattern")
			return
		}
		defer release(patt)
		if hr := callCOM(patt, valuePatternSetValue, uintptr(unsafe.Pointer(bstr))); hr != 0 {
			setErr = fmt.Errorf("SetValue hr=0x%x", uint32(hr))
		}
	}); err != nil {
		return err
	}
	return setErr
}
