//go:build windows

package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/qomos-w/sporemind/pkg/buildinfo"
)

// Native crash capture: an unhandled-exception filter that writes a JSON
// sidecar (exception code/address/thread) plus a MiniDumpWriteDump dump next
// to the other crash records. The Go runtime handles its own faults first;
// this filter only sees exceptions that escape it — typically access
// violations inside syscall/COM interop such as the WebView2 callback path,
// which otherwise kill the process without leaving any trace.
//
// The filter deliberately avoids the os package for file IO: another
// goroutine may hold os-internal locks when the fault happened, and
// blocking on them would turn the dump into a hang. Raw kernel32 calls
// cannot take those locks.

const (
	exceptionContinueSearch             = 0
	miniDumpWithIndirectlyReferencedMem = 0x00000040
	genericWrite                        = 0x40000000
	fileCreateAlways                    = 2
	fileAttributeNormal                 = 0x80
	invalidHandleValue                  = ^uintptr(0)
)

var (
	nativeCrashDir     string
	nativeCrashVersion string
	nativeCrashEntered uint32
)

var (
	nativeKernel32 = syscall.NewLazyDLL("kernel32.dll")
	nativeDbghelp  = syscall.NewLazyDLL("dbghelp.dll")

	procSetUnhandledExceptionFilter = nativeKernel32.NewProc("SetUnhandledExceptionFilter")
	procMiniDumpWriteDump           = nativeDbghelp.NewProc("MiniDumpWriteDump")
	procGetCurrentProcess           = nativeKernel32.NewProc("GetCurrentProcess")
	procGetCurrentThreadId          = nativeKernel32.NewProc("GetCurrentThreadId")
	procCreateFileW                 = nativeKernel32.NewProc("CreateFileW")
	procWriteFile                   = nativeKernel32.NewProc("WriteFile")
	procCloseHandle                 = nativeKernel32.NewProc("CloseHandle")
)

type nativeExceptionPointers struct {
	exceptionRecord uintptr // PEXCEPTION_RECORD
	contextRecord   uintptr // PCONTEXT
}

type nativeExceptionRecord struct {
	code             uint32
	flags            uint32
	record           uintptr
	address          uintptr
	numberParameters uint32
	// information [15]uintptr follows; not read.
}

type minidumpExceptionInformation struct {
	threadID          uint32
	exceptionPointers uintptr
	clientPointers    int32
}

// InstallNativeCrashHandler registers the SEH filter and pre-creates the
// crash directory. Call as early as possible after config load.
func InstallNativeCrashHandler() {
	dir := crashStoreDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	nativeCrashDir = dir
	nativeCrashVersion = buildinfo.Version
	if procSetUnhandledExceptionFilter.Find() != nil {
		return
	}
	procSetUnhandledExceptionFilter.Call(syscall.NewCallback(nativeExceptionFilter))
}

func nativeExceptionFilter(ep uintptr) uintptr {
	if atomic.SwapUint32(&nativeCrashEntered, 1) != 0 {
		return exceptionContinueSearch
	}
	dir := nativeCrashDir
	if dir == "" {
		return exceptionContinueSearch
	}
	code, address := "0x0", "0x0"
	if ep != 0 {
		pts := (*nativeExceptionPointers)(unsafe.Pointer(ep))
		if pts.exceptionRecord != 0 {
			rec := (*nativeExceptionRecord)(unsafe.Pointer(pts.exceptionRecord))
			code = fmt.Sprintf("0x%08X", rec.code)
			address = fmt.Sprintf("0x%X", rec.address)
		}
	}
	tid := uintptr(0)
	if v, _, _ := procGetCurrentThreadId.Call(); v != 0 {
		tid = v
	}
	ts := time.Now().Format("20060102-150405")
	pid := os.Getpid()
	base := filepath.Join(dir, fmt.Sprintf("%s-%d-native", ts, pid))
	// Sidecar format mirrors nativeSidecar in crashrec.go.
	sidecar := fmt.Sprintf(`{"code":%q,"address":%q,"thread":%d,"time":%q,"pid":%d,"version":%q}`+"\n",
		code, address, tid, time.Now().Format(time.RFC3339), pid, nativeCrashVersion)
	rawWriteFile(base+".json", []byte(sidecar))
	if procMiniDumpWriteDump.Find() == nil {
		if h, ok := rawCreateFile(base + ".dmp"); ok {
			mdei := minidumpExceptionInformation{threadID: uint32(tid), exceptionPointers: ep}
			procMiniDumpWriteDump.Call(currentProcessHandle(), uintptr(pid), h,
				miniDumpWithIndirectlyReferencedMem, uintptr(unsafe.Pointer(&mdei)), 0, 0)
			_, _, _ = procCloseHandle.Call(h)
		}
	}
	// Let the default handling (WER / process termination) proceed unchanged.
	return exceptionContinueSearch
}

func currentProcessHandle() uintptr {
	h, _, _ := procGetCurrentProcess.Call()
	return h
}

func rawCreateFile(path string) (uintptr, bool) {
	p16, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, false
	}
	h, _, _ := procCreateFileW.Call(
		uintptr(unsafe.Pointer(p16)),
		genericWrite, 0, 0, fileCreateAlways, fileAttributeNormal, 0,
	)
	if h == invalidHandleValue {
		return 0, false
	}
	return h, true
}

func rawWriteFile(path string, data []byte) {
	h, ok := rawCreateFile(path)
	if !ok {
		return
	}
	defer func() { _, _, _ = procCloseHandle.Call(h) }()
	for len(data) > 0 {
		chunk := data
		if len(chunk) > 1<<20 {
			chunk = chunk[:1<<20]
		}
		var written uint32
		_, _, _ = procWriteFile.Call(
			h,
			uintptr(unsafe.Pointer(&chunk[0])),
			uintptr(len(chunk)),
			uintptr(unsafe.Pointer(&written)),
			0,
		)
		if written == 0 {
			return
		}
		data = data[written:]
	}
}
