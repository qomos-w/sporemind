//go:build windows

package desktop

// Native Windows drag-out (OLE DoDragDrop) for StartFileDragOut.
//
// The COM objects (IDataObject, IDropSource, IEnumFORMATETC) are hand-written
// with explicit vtables instead of go-ole, so the data object can serve:
//   - CF_HDROP with one or many files (allocHDROP),
//   - CFSTR_PREFERREDDROPEFFECT = DROPEFFECT_COPY, telling Explorer the drag
//     is a pure copy (never a move that deletes the source afterwards).
//
// DoDragDrop is modal: it runs its own message loop until the user drops or
// cancels. It must run on a thread initialized with OleInitialize (STA), so
// the drag runs on a dedicated runtime.LockOSThread goroutine while the
// binding call blocks on a channel. The user keeps the mouse button held
// from the initiating dragstart until the drop, which is what makes the
// captured mouse transfer work (same pattern as Tauri's drag-out).

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"github.com/qomos-w/sporemind/pkg/archive"
)

var (
	ole32 = syscall.NewLazyDLL("ole32.dll")

	procOleInitialize   = ole32.NewProc("OleInitialize")
	procOleUninitialize = ole32.NewProc("OleUninitialize")
	procDoDragDrop      = ole32.NewProc("DoDragDrop")

	procRegisterClipboardFormat = user32.NewProc("RegisterClipboardFormatW")
)

const (
	// cfstrPreferredDropEffect is the registered clipboard format name behind
	// the CFSTR_PREFERREDDROPEFFECT preprocessor define in shlobj.h.
	cfstrPreferredDropEffect = "Preferred DropEffect"

	defaultDragOutRootName = "sporemind-dragout"

	dropEffectCopy = 1 // DROPEFFECT_COPY
	tymedHGlobal   = 1 // TYMED_HGLOBAL
	dvaspectCont   = 1 // DVASPECT_CONTENT
	dataDirGet     = 1 // DATADIR_GET

	mkLButton   = 0x0001
	mkRButton   = 0x0002
	mkMButton   = 0x0010
	mkXButton1  = 0x0020
	mkXButton2  = 0x0040
	mkAnyButton = mkLButton | mkRButton | mkMButton | mkXButton1 | mkXButton2

	// HRESULTs (compared as int32 of the low 32 bits).
	sOK                     = 0
	sFalse                  = 1
	dvEFormatEtc     uint32 = 0x80040064
	dvETymed         uint32 = 0x80040069
	dvEDvaspect      uint32 = 0x8004006B
	eNoInterface     uint32 = 0x80004002
	ePointer         uint32 = 0x80004003
	eInvalidArg      uint32 = 0x80070057
	eOutOfMemory     uint32 = 0x8007000E
	eNotImpl         uint32 = 0x80004001
	oleEAdviseNotSup uint32 = 0x80040003

	// DoDragDrop success codes (positive HRESULTs).
	dragDropSDrop              = 0x00040100 // DRAGDROP_S_DROP
	dragDropSCancel            = 0x00040101 // DRAGDROP_S_CANCEL
	dragDropSUseDefaultCursors = 0x00040102 // DRAGDROP_S_USEDEFAULTCURSORS
)

// ---------------------------------------------------------------------------
// COM plumbing
// ---------------------------------------------------------------------------

type comGUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

var (
	iidIUnknown       = comGUID{0x00000000, 0x0000, 0x0000, [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidIDataObject    = comGUID{0x0000010E, 0x0000, 0x0000, [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidIDropSource    = comGUID{0x00000121, 0x0000, 0x0000, [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidIEnumFORMATETC = comGUID{0x00000103, 0x0000, 0x0000, [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
)

func guidEqual(a, b *comGUID) bool {
	return a.Data1 == b.Data1 && a.Data2 == b.Data2 && a.Data3 == b.Data3 && a.Data4 == b.Data4
}

// formatEtc mirrors the Win32 FORMATETC struct.
type formatEtc struct {
	cfFormat uint16
	ptd      uintptr // DVTARGETDEVICE*, always NULL here
	dwAspect uint32
	lindex   int32
	tymed    uint32
}

// stgMedium mirrors the Win32 STGMEDIUM struct.
type stgMedium struct {
	tymed          uint32
	hGlobal        uintptr // union member used here
	pUnkForRelease uintptr
}

// hresultFailed reports whether a uintptr-returned HRESULT is a failure.
func hresultFailed(hr uintptr) bool {
	return int32(uint32(hr)) < 0
}

// ---------------------------------------------------------------------------
// startFileDragOut entry point
// ---------------------------------------------------------------------------

// startFileDragOut validates the request, extracts the optional archive to a
// temp dir, then runs the modal native drag. Temp data is removed when the
// drag returns.
func startFileDragOut(req FileDragOutRequest) error {
	for _, p := range req.LocalPaths {
		if strings.TrimSpace(p) == "" {
			return fmt.Errorf("drag-out: empty local path")
		}
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("drag-out: local path %q: %w", p, err)
		}
	}

	paths := make([]string, 0, len(req.LocalPaths)+1)
	paths = append(paths, req.LocalPaths...)

	if req.ArchiveTarGzBase64 != "" {
		dir, err := prepareDragOutArchive(req.ArchiveTarGzBase64, req.ArchiveRootName)
		if err != nil {
			return err
		}
		defer os.RemoveAll(filepath.Dir(dir)) // parent is the MkdirTemp root
		paths = append(paths, dir)
	}

	if len(paths) == 0 {
		return fmt.Errorf("drag-out: nothing to drag (no LocalPaths and no archive)")
	}
	return runDoDragDrop(paths)
}

// prepareDragOutArchive decodes the base64 tar.gz, validates the root folder
// name, and extracts the archive under a fresh temp directory. It returns the
// extracted folder path (inside the temp root, so the caller removes the
// root).
func prepareDragOutArchive(b64, rootName string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return "", fmt.Errorf("drag-out: base64 decode archive: %w", err)
	}

	rootName = strings.TrimSpace(rootName)
	if rootName == "" {
		rootName = defaultDragOutRootName
	}
	if !validDragOutRootName(rootName) {
		return "", fmt.Errorf("drag-out: invalid archive root name %q", rootName)
	}

	tmpRoot, err := os.MkdirTemp("", "sporemind-dragout-")
	if err != nil {
		return "", fmt.Errorf("drag-out: create temp dir: %w", err)
	}
	dest := filepath.Join(tmpRoot, rootName)

	// archive.Extract rejects absolute paths, ".." components, symlinks,
	// duplicates and oversized archives before anything touches the disk.
	_, _, err = archive.ExtractBytes(raw, filepath.ToSlash(dest), archive.ExtractHandlers{
		Mkdir: func(p string) error {
			return os.MkdirAll(filepath.FromSlash(p), 0o755)
		},
		WriteFile: func(p string, content []byte) error {
			return os.WriteFile(filepath.FromSlash(p), content, 0o644)
		},
	})
	if err != nil {
		os.RemoveAll(tmpRoot)
		return "", fmt.Errorf("drag-out: extract archive: %w", err)
	}
	return dest, nil
}

// validDragOutRootName accepts a single safe path component (no separators,
// no drive colon, no control characters, not "." or "..").
func validDragOutRootName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c < 0x20 || c == '/' || c == '\\' || c == ':' || c == '<' || c == '>' || c == '"' || c == '|' || c == '?' || c == '*' {
			return false
		}
	}
	return true
}

// runDoDragDrop performs the modal drag on a dedicated STA thread.
func runDoDragDrop(paths []string) error {
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		hr, _, _ := procOleInitialize.Call()
		if hresultFailed(hr) {
			done <- fmt.Errorf("drag-out: OleInitialize failed: 0x%08x", uint32(hr))
			return
		}
		defer procOleUninitialize.Call()

		done <- doDragDropOnOLEThread(paths)
	}()
	return <-done
}

func doDragDropOnOLEThread(paths []string) error {
	prefer, _, _ := procRegisterClipboardFormat.Call(
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(cfstrPreferredDropEffect))))
	if prefer == 0 {
		return fmt.Errorf("drag-out: RegisterClipboardFormat(%q) failed", cfstrPreferredDropEffect)
	}

	obj := newDragOutDataObject(paths, uint16(prefer))
	src := newDragOutDropSource()

	var effect uint32
	hr, _, _ := procDoDragDrop.Call(
		uintptr(unsafe.Pointer(obj)),
		uintptr(unsafe.Pointer(src)),
		dropEffectCopy, // allowed effects: copy only
		uintptr(unsafe.Pointer(&effect)),
	)
	runtime.KeepAlive(obj)
	runtime.KeepAlive(src)
	obj.retire() // release Go references handed to COM

	switch code := int32(uint32(hr)); code {
	case dragDropSDrop, dragDropSCancel:
		// Normal completion — a cancel (Esc, or release outside a target) is
		// not an error for the caller.
		return nil
	default:
		if hresultFailed(hr) {
			return fmt.Errorf("drag-out: DoDragDrop failed: 0x%08x (last effect %d)", uint32(hr), effect)
		}
		return nil
	}
}

// ---------------------------------------------------------------------------
// IDataObject implementation
// ---------------------------------------------------------------------------

type dragOutDataObject struct {
	vtbl   *dragOutDataObjectVtbl
	refs   int32
	paths  []string
	prefer uint16 // registered CFSTR_PREFERREDDROPEFFECT format id

	mu    sync.Mutex
	enums []uintptr // live IEnumFORMATETC instances handed to COM
}

type dragOutDataObjectVtbl struct {
	queryInterface        uintptr
	addRef                uintptr
	release               uintptr
	getData               uintptr
	getDataHere           uintptr
	queryGetData          uintptr
	getCanonicalFormatEtc uintptr
	setData               uintptr
	enumFormatEtc         uintptr
	dAdvise               uintptr
	dUnadvise             uintptr
	enumDAdvise           uintptr
}

var dragOutDataObjectVtblInst = &dragOutDataObjectVtbl{
	queryInterface:        syscall.NewCallback(dragOutDataObjectQueryInterface),
	addRef:                syscall.NewCallback(dragOutDataObjectAddRef),
	release:               syscall.NewCallback(dragOutDataObjectRelease),
	getData:               syscall.NewCallback(dragOutDataObjectGetData),
	getDataHere:           syscall.NewCallback(dragOutDataObjectGetDataHere),
	queryGetData:          syscall.NewCallback(dragOutDataObjectQueryGetData),
	getCanonicalFormatEtc: syscall.NewCallback(dragOutDataObjectGetCanonicalFormatEtc),
	setData:               syscall.NewCallback(dragOutDataObjectSetData),
	enumFormatEtc:         syscall.NewCallback(dragOutDataObjectEnumFormatEtc),
	dAdvise:               syscall.NewCallback(dragOutDataObjectDAdvise),
	dUnadvise:             syscall.NewCallback(dragOutDataObjectDUnadvise),
	enumDAdvise:           syscall.NewCallback(dragOutDataObjectEnumDAdvise),
}

func newDragOutDataObject(paths []string, prefer uint16) *dragOutDataObject {
	return &dragOutDataObject{
		vtbl:   dragOutDataObjectVtblInst,
		refs:   1,
		paths:  append([]string(nil), paths...),
		prefer: prefer,
	}
}

// formats returns the advertised FORMATETC list (CF_HDROP first).
func (d *dragOutDataObject) formats() []formatEtc {
	fe := formatEtc{ptd: 0, dwAspect: dvaspectCont, lindex: -1, tymed: tymedHGlobal}
	fe.cfFormat = cfHDROP
	fmts := []formatEtc{fe}
	if d.prefer != 0 {
		fe.cfFormat = d.prefer
		fmts = append(fmts, fe)
	}
	return fmts
}

func (d *dragOutDataObject) formatSupported(cf uint16) bool {
	return cf == cfHDROP || (d.prefer != 0 && cf == d.prefer)
}

// trackEnum pins an enumerator handed to COM so the GC cannot collect it
// while only COM holds a raw pointer.
func (d *dragOutDataObject) trackEnum(p uintptr) {
	d.mu.Lock()
	d.enums = append(d.enums, p)
	d.mu.Unlock()
}

// retire drops Go-side references after DoDragDrop returns; nothing more
// will call into the object.
func (d *dragOutDataObject) retire() {
	d.mu.Lock()
	d.enums = nil
	d.mu.Unlock()
}

func dragOutDataObjectQueryInterface(self, riid, ppv uintptr) uintptr {
	if riid == 0 || ppv == 0 {
		return uintptr(ePointer)
	}
	d := (*dragOutDataObject)(unsafe.Pointer(self))
	g := (*comGUID)(unsafe.Pointer(riid))
	if guidEqual(g, &iidIUnknown) || guidEqual(g, &iidIDataObject) {
		*(*uintptr)(unsafe.Pointer(ppv)) = self
		atomic.AddInt32(&d.refs, 1)
		return uintptr(sOK)
	}
	*(*uintptr)(unsafe.Pointer(ppv)) = 0
	return uintptr(eNoInterface)
}

func dragOutDataObjectAddRef(self uintptr) uintptr {
	d := (*dragOutDataObject)(unsafe.Pointer(self))
	return uintptr(atomic.AddInt32(&d.refs, 1))
}

func dragOutDataObjectRelease(self uintptr) uintptr {
	d := (*dragOutDataObject)(unsafe.Pointer(self))
	return uintptr(atomic.AddInt32(&d.refs, -1))
}

func dragOutDataObjectGetData(self, pformatetcIn, pmedium uintptr) uintptr {
	if pformatetcIn == 0 || pmedium == 0 {
		return uintptr(ePointer)
	}
	d := (*dragOutDataObject)(unsafe.Pointer(self))
	fe := (*formatEtc)(unsafe.Pointer(pformatetcIn))

	if !d.formatSupported(fe.cfFormat) {
		return uintptr(dvEFormatEtc)
	}
	if fe.dwAspect != dvaspectCont {
		return uintptr(dvEDvaspect)
	}
	if fe.tymed&tymedHGlobal == 0 {
		return uintptr(dvETymed)
	}

	var hMem uintptr
	switch fe.cfFormat {
	case cfHDROP:
		h, err := allocHDROP(d.paths...)
		if err != nil {
			return uintptr(eOutOfMemory)
		}
		hMem = h
	case d.prefer:
		h, err := allocDragOutDWORD(dropEffectCopy)
		if err != nil {
			return uintptr(eOutOfMemory)
		}
		hMem = h
	}

	med := (*stgMedium)(unsafe.Pointer(pmedium))
	med.tymed = tymedHGlobal
	med.hGlobal = hMem
	med.pUnkForRelease = 0 // target owns the HGLOBAL (ReleaseStgMedium frees)
	return uintptr(sOK)
}

func dragOutDataObjectGetDataHere(_, _, _ uintptr) uintptr {
	return uintptr(eNotImpl)
}

func dragOutDataObjectQueryGetData(self, pformatetc uintptr) uintptr {
	if pformatetc == 0 {
		return uintptr(ePointer)
	}
	d := (*dragOutDataObject)(unsafe.Pointer(self))
	fe := (*formatEtc)(unsafe.Pointer(pformatetc))
	if !d.formatSupported(fe.cfFormat) {
		return uintptr(dvEFormatEtc)
	}
	if fe.dwAspect != dvaspectCont {
		return uintptr(dvEDvaspect)
	}
	if fe.tymed&tymedHGlobal == 0 {
		return uintptr(dvETymed)
	}
	return uintptr(sOK)
}

func dragOutDataObjectGetCanonicalFormatEtc(_, pformatetcIn, pformatetcOut uintptr) uintptr {
	if pformatetcOut == 0 {
		return uintptr(ePointer)
	}
	if pformatetcIn != 0 {
		*(*formatEtc)(unsafe.Pointer(pformatetcOut)) = *(*formatEtc)(unsafe.Pointer(pformatetcIn))
	}
	(*formatEtc)(unsafe.Pointer(pformatetcOut)).ptd = 0
	return uintptr(sFalse) // no more specific format available
}

func dragOutDataObjectSetData(_, _, _, _ uintptr) uintptr {
	return uintptr(eNotImpl) // read-only data object
}

func dragOutDataObjectEnumFormatEtc(self, dwDirection, ppenum uintptr) uintptr {
	if ppenum == 0 {
		return uintptr(ePointer)
	}
	if dwDirection != dataDirGet {
		*(*uintptr)(unsafe.Pointer(ppenum)) = 0
		return uintptr(eNotImpl)
	}
	d := (*dragOutDataObject)(unsafe.Pointer(self))
	enum := newDragOutEnumFmt(d, d.formats())
	d.trackEnum(uintptr(unsafe.Pointer(enum)))
	*(*uintptr)(unsafe.Pointer(ppenum)) = uintptr(unsafe.Pointer(enum))
	return uintptr(sOK)
}

func dragOutDataObjectDAdvise(_, _, _, _ uintptr) uintptr {
	return uintptr(oleEAdviseNotSup)
}

func dragOutDataObjectDUnadvise(_, _, _ uintptr) uintptr {
	return uintptr(oleEAdviseNotSup)
}

func dragOutDataObjectEnumDAdvise(_, _ uintptr) uintptr {
	return uintptr(oleEAdviseNotSup)
}

// allocDragOutDWORD builds a moveable HGLOBAL holding one DWORD.
func allocDragOutDWORD(v uint32) (uintptr, error) {
	hMem, _, _ := procGlobalAlloc.Call(uintptr(gmemMoveable|gmemZeroInit), 4)
	if hMem == 0 {
		return 0, fmt.Errorf("GlobalAlloc(4) failed")
	}
	ptr, _, _ := procGlobalLock.Call(hMem)
	if ptr == 0 {
		procGlobalFree.Call(hMem)
		return 0, fmt.Errorf("GlobalLock failed")
	}
	putUI32(unsafe.Slice((*byte)(unsafe.Pointer(ptr)), 4), v)
	procGlobalUnlock.Call(hMem)
	return hMem, nil
}

// ---------------------------------------------------------------------------
// IEnumFORMATETC implementation
// ---------------------------------------------------------------------------

type dragOutEnumFmt struct {
	vtbl    *dragOutEnumFmtVtbl
	refs    int32
	owner   *dragOutDataObject // for tracking clones
	formats []formatEtc        // immutable after construction
	pos     int
}

type dragOutEnumFmtVtbl struct {
	queryInterface uintptr
	addRef         uintptr
	release        uintptr
	next           uintptr
	skip           uintptr
	reset          uintptr
	clone          uintptr
}

var dragOutEnumFmtVtblInst *dragOutEnumFmtVtbl

func init() {
	dragOutEnumFmtVtblInst = &dragOutEnumFmtVtbl{
		queryInterface: syscall.NewCallback(dragOutEnumFmtQueryInterface),
		addRef:         syscall.NewCallback(dragOutEnumFmtAddRef),
		release:        syscall.NewCallback(dragOutEnumFmtRelease),
		next:           syscall.NewCallback(dragOutEnumFmtNext),
		skip:           syscall.NewCallback(dragOutEnumFmtSkip),
		reset:          syscall.NewCallback(dragOutEnumFmtReset),
		clone:          syscall.NewCallback(dragOutEnumFmtClone),
	}
}

func newDragOutEnumFmt(owner *dragOutDataObject, formats []formatEtc) *dragOutEnumFmt {
	return &dragOutEnumFmt{
		vtbl:    dragOutEnumFmtVtblInst,
		refs:    1,
		owner:   owner,
		formats: append([]formatEtc(nil), formats...),
	}
}

func dragOutEnumFmtQueryInterface(self, riid, ppv uintptr) uintptr {
	if riid == 0 || ppv == 0 {
		return uintptr(ePointer)
	}
	e := (*dragOutEnumFmt)(unsafe.Pointer(self))
	g := (*comGUID)(unsafe.Pointer(riid))
	if guidEqual(g, &iidIUnknown) || guidEqual(g, &iidIEnumFORMATETC) {
		*(*uintptr)(unsafe.Pointer(ppv)) = self
		atomic.AddInt32(&e.refs, 1)
		return uintptr(sOK)
	}
	*(*uintptr)(unsafe.Pointer(ppv)) = 0
	return uintptr(eNoInterface)
}

func dragOutEnumFmtAddRef(self uintptr) uintptr {
	e := (*dragOutEnumFmt)(unsafe.Pointer(self))
	return uintptr(atomic.AddInt32(&e.refs, 1))
}

func dragOutEnumFmtRelease(self uintptr) uintptr {
	e := (*dragOutEnumFmt)(unsafe.Pointer(self))
	return uintptr(atomic.AddInt32(&e.refs, -1))
}

func dragOutEnumFmtNext(self, celt, rgelt, pceltFetched uintptr) uintptr {
	if celt == 0 || rgelt == 0 {
		return uintptr(eInvalidArg)
	}
	e := (*dragOutEnumFmt)(unsafe.Pointer(self))
	elem := unsafe.Sizeof(formatEtc{})
	fetched := 0
	for fetched < int(celt) && e.pos < len(e.formats) {
		dst := (*formatEtc)(unsafe.Pointer(rgelt + uintptr(fetched)*elem))
		*dst = e.formats[e.pos]
		e.pos++
		fetched++
	}
	if pceltFetched != 0 {
		*(*uint32)(unsafe.Pointer(pceltFetched)) = uint32(fetched)
	}
	if fetched == int(celt) {
		return uintptr(sOK)
	}
	return uintptr(sFalse)
}

func dragOutEnumFmtSkip(self, celt uintptr) uintptr {
	e := (*dragOutEnumFmt)(unsafe.Pointer(self))
	remaining := len(e.formats) - e.pos
	if int(celt) > remaining {
		e.pos = len(e.formats)
		return uintptr(sFalse)
	}
	e.pos += int(celt)
	return uintptr(sOK)
}

func dragOutEnumFmtReset(self uintptr) uintptr {
	e := (*dragOutEnumFmt)(unsafe.Pointer(self))
	e.pos = 0
	return uintptr(sOK)
}

func dragOutEnumFmtClone(self, ppenum uintptr) uintptr {
	if ppenum == 0 {
		return uintptr(ePointer)
	}
	e := (*dragOutEnumFmt)(unsafe.Pointer(self))
	c := newDragOutEnumFmt(e.owner, e.formats)
	c.pos = e.pos
	if e.owner != nil {
		e.owner.trackEnum(uintptr(unsafe.Pointer(c)))
	}
	*(*uintptr)(unsafe.Pointer(ppenum)) = uintptr(unsafe.Pointer(c))
	return uintptr(sOK)
}

// ---------------------------------------------------------------------------
// IDropSource implementation
// ---------------------------------------------------------------------------

type dragOutDropSource struct {
	vtbl *dragOutDropSourceVtbl
	refs int32
}

type dragOutDropSourceVtbl struct {
	queryInterface    uintptr
	addRef            uintptr
	release           uintptr
	queryContinueDrag uintptr
	giveFeedback      uintptr
}

var dragOutDropSourceVtblInst = &dragOutDropSourceVtbl{
	queryInterface:    syscall.NewCallback(dragOutDropSourceQueryInterface),
	addRef:            syscall.NewCallback(dragOutDropSourceAddRef),
	release:           syscall.NewCallback(dragOutDropSourceRelease),
	queryContinueDrag: syscall.NewCallback(dragOutDropSourceQueryContinueDrag),
	giveFeedback:      syscall.NewCallback(dragOutDropSourceGiveFeedback),
}

func newDragOutDropSource() *dragOutDropSource {
	return &dragOutDropSource{vtbl: dragOutDropSourceVtblInst, refs: 1}
}

func dragOutDropSourceQueryInterface(self, riid, ppv uintptr) uintptr {
	if riid == 0 || ppv == 0 {
		return uintptr(ePointer)
	}
	s := (*dragOutDropSource)(unsafe.Pointer(self))
	g := (*comGUID)(unsafe.Pointer(riid))
	if guidEqual(g, &iidIUnknown) || guidEqual(g, &iidIDropSource) {
		*(*uintptr)(unsafe.Pointer(ppv)) = self
		atomic.AddInt32(&s.refs, 1)
		return uintptr(sOK)
	}
	*(*uintptr)(unsafe.Pointer(ppv)) = 0
	return uintptr(eNoInterface)
}

func dragOutDropSourceAddRef(self uintptr) uintptr {
	s := (*dragOutDropSource)(unsafe.Pointer(self))
	return uintptr(atomic.AddInt32(&s.refs, 1))
}

func dragOutDropSourceRelease(self uintptr) uintptr {
	s := (*dragOutDropSource)(unsafe.Pointer(self))
	return uintptr(atomic.AddInt32(&s.refs, -1))
}

// dragOutDropSourceQueryContinueDrag: Esc cancels, releasing every mouse
// button completes the drop, anything else continues the drag. The drag
// starts from a JS dragstart with the button already held.
func dragOutDropSourceQueryContinueDrag(_, fEscapePressed, grfKeyState uintptr) uintptr {
	if fEscapePressed != 0 {
		return uintptr(dragDropSCancel)
	}
	if grfKeyState&mkAnyButton == 0 {
		return uintptr(dragDropSDrop)
	}
	return uintptr(sOK)
}

func dragOutDropSourceGiveFeedback(_, _ uintptr) uintptr {
	return uintptr(dragDropSUseDefaultCursors)
}
