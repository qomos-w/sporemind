package desktop

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qomos-w/gospore/gateway"
	"github.com/qomos-w/sporemind/pkg/buildinfo"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// Crash record kinds.
const (
	CrashKindPanic        = "panic"
	CrashKindFatal        = "fatal"
	CrashKindNative       = "native"
	CrashKindAbnormalExit = "abnormal-exit"
)

// Crash directory file names. Record files follow the naming scheme
// <yyyymmdd-hhmmss>-<pid>-<kind>.<log|json|dmp> so the scanner can parse
// kind, pid and time from the name alone.
const (
	crashSessionFile = "session.json"
	crashAckFile     = "ack.json"
	crashErrorsFile  = "errors.log"
)

const (
	crashDetailCap   = 8 << 10   // per-record detail cap surfaced to the frontend
	crashSummaryCap  = 240       // per-record summary cap
	crashMessageCap  = 512       // error message cap in errors.log / records
	crashStackCap    = 240       // stack hint cap in errors.log
	crashErrorsMax   = 256 << 10 // append-only errors.log hard cap
	crashErrorsTailN = 24        // errors.log lines included in the boot report
	crashLogTailN    = 40        // persisted log entries included in the boot report
	crashLogLineCap  = 300       // per-line cap of the boot log tail
	crashRetainDays  = 30        // record files older than this are pruned
	crashRetainMax   = 20        // max record files surfaced / kept
	crashRetainDumps = 5         // max minidumps kept
	crashAckMax      = 200       // max acknowledged ids kept in ack.json
)

// CrashSessionInfo is the live-session marker. Its presence at startup means
// the previous process never reached a clean shutdown.
type CrashSessionInfo struct {
	PID       int    `json:"pid"`
	StartedAt string `json:"startedAt"`
	Version   string `json:"version,omitempty"`
}

// CrashRecordInfo is one crash artifact surfaced to the frontend at boot.
type CrashRecordInfo struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Time     string `json:"time"`
	PID      int    `json:"pid,omitempty"`
	Summary  string `json:"summary"`
	Detail   string `json:"detail,omitempty"`
	DumpFile string `json:"dumpFile,omitempty"`
}

// BootCrashReport describes how the previous session ended. The frontend
// fetches it once at startup and renders a crash notice when non-empty.
type BootCrashReport struct {
	AbnormalExit    bool              `json:"abnormalExit"`
	PreviousSession *CrashSessionInfo `json:"previousSession,omitempty"`
	Records         []CrashRecordInfo `json:"records"`
	ErrorsTail      []string          `json:"errorsTail,omitempty"`
	LogTail         []string          `json:"logTail,omitempty"`
}

func crashStoreDir() string {
	return filepath.Join(config.DataDir(), "crashes")
}

// ClaimedCrashSession is the previous session's end state, captured before
// this process claims the marker. Passed opaquely from main to
// FinishBootCrashReport.
type ClaimedCrashSession struct {
	abnormal bool
	prev     *CrashSessionInfo
}

// ClaimCrashSession reads the previous session marker and immediately claims
// it for this process. Called early — before application.New — so even a
// crash during startup leaves an abnormal-exit trace for the next boot.
// A duplicate instance that exits inside application.New may transiently own
// the marker; the surviving instance has already overwritten it with its own
// claim, and every clean shutdown removes it, so this cannot produce a false
// abnormal-exit report.
func ClaimCrashSession(pid int) ClaimedCrashSession {
	return claimCrashSession(crashStoreDir(), pid, time.Now())
}

func claimCrashSession(dir string, pid int, now time.Time) ClaimedCrashSession {
	cs := ClaimedCrashSession{}
	if data, err := os.ReadFile(filepath.Join(dir, crashSessionFile)); err == nil {
		cs.abnormal = true
		var prev CrashSessionInfo
		if json.Unmarshal(data, &prev) == nil {
			info := prev
			cs.prev = &info
		}
	}
	if err := os.MkdirAll(dir, 0o755); err == nil {
		data, _ := json.MarshalIndent(CrashSessionInfo{
			PID:       pid,
			StartedAt: now.Format(time.RFC3339),
			Version:   buildinfo.Version,
		}, "", "  ")
		_ = persist.WriteFileAtomic(filepath.Join(dir, crashSessionFile), data, 0o644)
	}
	return cs
}

// FinishBootCrashReport assembles the boot crash report for the frontend.
// Called after application.New (the duplicate-instance exit point) so only
// the surviving instance surfaces the previous session's crash state.
func FinishBootCrashReport(cs ClaimedCrashSession, logEntries []gateway.LogEntry) *BootCrashReport {
	return finishBootCrashReport(crashStoreDir(), cs, logEntries, time.Now())
}

func finishBootCrashReport(dir string, cs ClaimedCrashSession, logEntries []gateway.LogEntry, now time.Time) *BootCrashReport {
	report := &BootCrashReport{PreviousSession: cs.prev}
	report.Records, report.AbnormalExit = crashRecordsView(dir, cs.abnormal, cs.prev)
	report.ErrorsTail = crashErrorsTailLines(dir, crashErrorsTailN)
	report.LogTail = formatLogTail(logEntries, crashLogTailN)
	pruneCrashRecords(dir, now)
	return report
}

// crashRecordsView rebuilds the ack-filtered record list for the current crash
// state. The frontend re-fetches the boot report on every webview reload, so
// the served view must be re-derived from the crash store instead of replaying
// the boot-time snapshot: acknowledged records stay dismissed within the same
// process, and an acknowledged synthesized abnormal-exit record also clears
// AbnormalExit so the crash overlay does not re-open on every refresh.
func crashRecordsView(dir string, abnormal bool, prev *CrashSessionInfo) ([]CrashRecordInfo, bool) {
	acked := readCrashAcks(dir)
	records := scanCrashRecords(dir, acked)
	if !abnormal {
		return records, false
	}
	if len(records) > 0 {
		return records, true
	}
	cand := abnormalExitRecord(prev)
	if acked[cand.ID] {
		return nil, false
	}
	return []CrashRecordInfo{cand}, true
}

// EndCrashSession releases the session marker on a clean shutdown so the
// next startup does not report an abnormal exit.
func EndCrashSession() {
	_ = os.Remove(filepath.Join(crashStoreDir(), crashSessionFile))
}

// CrashErrorHandler is the wails ErrorHandler. Every error is appended to a
// synchronous on-disk log before anything else can kill the process — the
// webview2 error path calls this and immediately os.Exit(1)s, so the usual
// async ring persistence would race with process death and lose the final
// message. FatalError additionally gets its own record file.
func CrashErrorHandler(err error) {
	if err == nil {
		return
	}
	appendCrashError(crashStoreDir(), err, time.Now())
	log.Printf("[wails error] %s", truncateForLog(err.Error(), crashMessageCap))
}

func abnormalExitRecord(prev *CrashSessionInfo) CrashRecordInfo {
	id := "exit-unknown"
	when := ""
	if prev != nil {
		id = "exit-" + strings.NewReplacer(":", "", ".", "-").Replace(prev.StartedAt)
		when = prev.StartedAt
	}
	return CrashRecordInfo{
		ID:      id,
		Kind:    CrashKindAbnormalExit,
		Time:    when,
		Summary: "Previous session exited without a clean shutdown (crash, forced termination, or power loss). No detailed crash record was captured; see the log tails below.",
	}
}

type crashFileMeta struct {
	name string // full file name
	id   string // file name without extension
	kind string
	pid  int
	when time.Time
}

func parseCrashFileName(name string) (crashFileMeta, bool) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	parts := strings.Split(stem, "-")
	if len(parts) != 4 {
		return crashFileMeta{}, false
	}
	when, err := time.ParseInLocation("20060102-150405", parts[0]+"-"+parts[1], time.Local)
	if err != nil {
		return crashFileMeta{}, false
	}
	pid, err := strconv.Atoi(parts[2])
	if err != nil {
		return crashFileMeta{}, false
	}
	kind := parts[3]
	switch kind {
	case CrashKindPanic, CrashKindFatal:
		if ext != ".log" {
			return crashFileMeta{}, false
		}
	case CrashKindNative:
		if ext != ".json" {
			return crashFileMeta{}, false
		}
	default:
		return crashFileMeta{}, false
	}
	return crashFileMeta{name: name, id: stem, kind: kind, pid: pid, when: when}, true
}

func scanCrashRecords(dir string, acked map[string]bool) []CrashRecordInfo {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var records []CrashRecordInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		meta, ok := parseCrashFileName(e.Name())
		if !ok || acked[meta.id] {
			continue
		}
		records = append(records, buildCrashRecord(dir, meta))
	}
	// Names start with a sortable timestamp, so lexical order is chronological.
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	if len(records) > crashRetainMax {
		records = records[len(records)-crashRetainMax:]
	}
	return records
}

func buildCrashRecord(dir string, meta crashFileMeta) CrashRecordInfo {
	rec := CrashRecordInfo{
		ID:   meta.id,
		Kind: meta.kind,
		Time: meta.when.Format(time.RFC3339),
		PID:  meta.pid,
	}
	data, err := os.ReadFile(filepath.Join(dir, meta.name))
	if err != nil {
		rec.Summary = "(record file unreadable: " + err.Error() + ")"
		return rec
	}
	switch meta.kind {
	case CrashKindNative:
		fillNativeRecord(&rec, data, dir, meta)
	default:
		fillTextRecord(&rec, data)
	}
	return rec
}

// nativeSidecar is the JSON written next to the minidump by the SEH filter.
// Kept in sync with crash_native_windows.go.
type nativeSidecar struct {
	Code    string `json:"code"`
	Address string `json:"address"`
	Thread  int    `json:"thread"`
	Time    string `json:"time"`
	PID     int    `json:"pid"`
	Version string `json:"version"`
}

var nativeExceptionNames = map[string]string{
	"0xC0000005": "EXCEPTION_ACCESS_VIOLATION",
	"0xC0000409": "EXCEPTION_STACK_BUFFER_OVERRUN / fail-fast",
	"0xC00000FD": "EXCEPTION_STACK_OVERFLOW",
	"0x80000003": "EXCEPTION_BREAKPOINT",
	"0xC0000142": "EXCEPTION_DLL_INIT_FAILED",
	"0xC0000374": "EXCEPTION_HEAP_CORRUPTION",
}

func fillNativeRecord(rec *CrashRecordInfo, data []byte, dir string, meta crashFileMeta) {
	var sc nativeSidecar
	if err := json.Unmarshal(data, &sc); err != nil {
		rec.Summary = "(native crash; unreadable sidecar: " + err.Error() + ")"
		return
	}
	name := nativeExceptionNames[sc.Code]
	if name == "" {
		name = "unknown exception"
	}
	rec.Summary = truncateForLog(fmt.Sprintf("Native exception %s (%s) at %s", sc.Code, name, sc.Address), crashSummaryCap)
	rec.Detail = truncateForLog(string(data), crashDetailCap)
	dump := filepath.Join(dir, meta.id+".dmp")
	if _, err := os.Stat(dump); err == nil {
		rec.DumpFile = dump
	}
}

func fillTextRecord(rec *CrashRecordInfo, data []byte) {
	text := string(data)
	rec.Detail = truncateForLog(text, crashDetailCap)
	summary := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if summary == "" {
			summary = line
		}
		if strings.HasPrefix(line, "Error: ") {
			summary = strings.TrimPrefix(line, "Error: ")
			break
		}
	}
	rec.Summary = truncateForLog(summary, crashSummaryCap)
}

func truncateForLog(s string, cap int) string {
	if len(s) > cap {
		return s[:cap] + "...(truncated)"
	}
	return s
}

func formatLogTail(entries []gateway.LogEntry, n int) []string {
	if len(entries) == 0 {
		return nil
	}
	if len(entries) > n {
		entries = entries[len(entries)-n:]
	}
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		line := fmt.Sprintf("%s [%s] %s", e.Timestamp, e.Level, e.Message)
		lines = append(lines, truncateForLog(line, crashLogLineCap))
	}
	return lines
}

// crashErrorsMu serializes appends within the process; the fatal path may
// call appendCrashError from any goroutine right before os.Exit.
var crashErrorsMu sync.Mutex

// benignWailsErrorPrefixes lists wails-internal error reports that describe
// handled degradation, not crash evidence. Recording them in errors.log
// floods the boot crash report with non-fatal noise.
var benignWailsErrorPrefixes = []string{
	// Windows request cancellation (wails v3 PR #6100): WebView2 answers the
	// per-worker CDP handshake with ERROR_INVALID_PARAMETER for worker sessions
	// it cannot address (worker terminated mid-handshake, or a domain the
	// target type does not support). The worker is always released afterwards
	// via Runtime.runIfWaitingForDebugger; only per-worker request-abort
	// tracking is lost.
	"Worker request cancellation setup: ",
	"Resuming attached WebView target: ",
}

func isBenignWailsError(err error) bool {
	msg := err.Error()
	for _, prefix := range benignWailsErrorPrefixes {
		if strings.HasPrefix(msg, prefix) {
			return true
		}
	}
	return false
}

func appendCrashError(dir string, err error, now time.Time) {
	if isBenignWailsError(err) {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	msg := truncateForLog(strings.ReplaceAll(err.Error(), "\n", " | "), crashMessageCap)
	entry := fmt.Sprintf("%s\t%s\t%s\n", now.Format(time.RFC3339), msg, crashStackHint())
	var fe *application.FatalError
	if errors.As(err, &fe) {
		pid := os.Getpid()
		name := fmt.Sprintf("%s-%d-%s.log", now.Format("20060102-150405"), pid, CrashKindFatal)
		content := fmt.Sprintf("sporemind fatal error\nTime: %s\nError: %s\n\nStack hint: %s\n",
			now.Format(time.RFC3339Nano), truncateForLog(err.Error(), 1<<16), crashStackHint())
		_ = persist.WriteFileAtomic(filepath.Join(dir, name), []byte(content), 0o644)
	}
	path := filepath.Join(dir, crashErrorsFile)
	crashErrorsMu.Lock()
	defer crashErrorsMu.Unlock()
	f, ferr := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if ferr != nil {
		return
	}
	if _, err := f.WriteString(entry); err != nil {
		f.Close()
		return
	}
	if st, err := f.Stat(); err == nil && st.Size() > crashErrorsMax {
		f.Close()
		trimCrashErrorsLocked(path)
		return
	}
	f.Close()
}

// trimCrashErrorsLocked rewrites the errors log keeping only the newest
// half. The caller holds crashErrorsMu, so no concurrent append can interleave.
func trimCrashErrorsLocked(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	keep := len(data) / 2
	if idx := bytes.LastIndexByte(data[:len(data)-keep], '\n'); idx >= 0 {
		keep = len(data) - idx - 1
	}
	_ = persist.WriteFileAtomic(path, data[len(data)-keep:], 0o644)
}

func crashErrorsTailLines(dir string, n int) []string {
	data, err := os.ReadFile(filepath.Join(dir, crashErrorsFile))
	if err != nil {
		return nil
	}
	// Only surface lines appended after the acknowledged offset; otherwise a
	// plain webview reload replays the whole historical tail on every fetch.
	if off := loadCrashAcks(dir).ErrorsAck; off > 0 {
		if off >= int64(len(data)) {
			return nil
		}
		data = data[off:]
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			out = append(out, truncateForLog(l, crashLogLineCap))
		}
	}
	return out
}

// crashStackHint returns a compact caller chain for attribution: which wails
// or webview2 frame reported the error.
func crashStackHint() string {
	pcs := make([]uintptr, 8)
	n := runtime.Callers(3, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	var parts []string
	for {
		frame, more := frames.Next()
		if frame.Function != "" {
			parts = append(parts, frame.Function)
		}
		if len(parts) >= 4 || !more {
			break
		}
	}
	return truncateForLog(strings.Join(parts, " <- "), crashStackCap)
}

type crashAcks struct {
	IDs []string `json:"ids"`
	// ErrorsAck is the byte offset of errors.log whose lines have already
	// been surfaced and dismissed. Tail reads only report content beyond it.
	ErrorsAck int64 `json:"errorsAck,omitempty"`
}

func loadCrashAcks(dir string) crashAcks {
	var ack crashAcks
	if data, err := os.ReadFile(filepath.Join(dir, crashAckFile)); err == nil {
		_ = json.Unmarshal(data, &ack)
	}
	return ack
}

func readCrashAcks(dir string) map[string]bool {
	ack := loadCrashAcks(dir)
	m := make(map[string]bool, len(ack.IDs))
	for _, id := range ack.IDs {
		m[id] = true
	}
	return m
}

// ackCrashRecords marks records as acknowledged so they are not reported on
// subsequent startups. Abnormal-exit records carry a session-derived id so
// one dismissal covers that crash.
func ackCrashRecords(dir string, ids []string) {
	if len(ids) == 0 {
		return
	}
	ack := loadCrashAcks(dir)
	seen := make(map[string]bool, len(ack.IDs))
	for _, id := range ack.IDs {
		seen[id] = true
	}
	for _, id := range ids {
		if id != "" && !seen[id] {
			ack.IDs = append(ack.IDs, id)
			seen[id] = true
		}
	}
	if len(ack.IDs) > crashAckMax {
		ack.IDs = ack.IDs[len(ack.IDs)-crashAckMax:]
	}
	// Acknowledging the crash notice also retires the error tail it carried:
	// advance the offset to the current end of errors.log so a later refresh
	// does not replay the same historical lines.
	if st, err := os.Stat(filepath.Join(dir, crashErrorsFile)); err == nil && st.Size() > ack.ErrorsAck {
		ack.ErrorsAck = st.Size()
	}
	if err := os.MkdirAll(dir, 0o755); err == nil {
		data, _ := json.MarshalIndent(ack, "", "  ")
		_ = persist.WriteFileAtomic(filepath.Join(dir, crashAckFile), data, 0o644)
	}
}

// pruneCrashRecords removes record artifacts older than the retention
// window, keeps only the newest crashRetainMax record files and newest
// crashRetainDumps minidumps.
func pruneCrashRecords(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type fileMeta struct {
		name    string
		modTime time.Time
	}
	var records, dumps []fileMeta
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		switch {
		case strings.HasSuffix(e.Name(), ".dmp"):
			dumps = append(dumps, fileMeta{e.Name(), info.ModTime()})
		default:
			if _, ok := parseCrashFileName(e.Name()); ok {
				records = append(records, fileMeta{e.Name(), info.ModTime()})
			}
		}
	}
	removeExpired := func(files []fileMeta) {
		for _, fm := range files {
			if now.Sub(fm.modTime) > crashRetainDays*24*time.Hour {
				_ = os.Remove(filepath.Join(dir, fm.name))
			}
		}
	}
	removeExpired(records)
	removeExpired(dumps)

	sort.Slice(records, func(i, j int) bool { return records[i].modTime.After(records[j].modTime) })
	if len(records) > crashRetainMax {
		for _, fm := range records[crashRetainMax:] {
			_ = os.Remove(filepath.Join(dir, fm.name))
		}
	}
	sort.Slice(dumps, func(i, j int) bool { return dumps[i].modTime.After(dumps[j].modTime) })
	if len(dumps) > crashRetainDumps {
		for _, fm := range dumps[crashRetainDumps:] {
			_ = os.Remove(filepath.Join(dir, fm.name))
		}
	}

	// Drop orphaned minidumps whose sidecar was pruned.
	sidecars := make(map[string]bool, len(records))
	for _, fm := range records {
		if meta, ok := parseCrashFileName(fm.name); ok && meta.kind == CrashKindNative {
			sidecars[meta.id] = true
		}
	}
	for _, fm := range dumps {
		stem := strings.TrimSuffix(fm.name, filepath.Ext(fm.name))
		if !sidecars[stem] {
			_ = os.Remove(filepath.Join(dir, fm.name))
		}
	}
}
