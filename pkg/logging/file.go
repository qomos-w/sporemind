package logging

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/qomos-w/gospore/gateway"
)

// LogRing is the surface required by the runtime to wire logging into the
// gateway /logs endpoint and the gospore log hook.
type LogRing interface {
	Append(gateway.LogEntry)
	QueryLogs(gateway.LogQuery) []gateway.LogEntry
	All() []gateway.LogEntry
	Tail(n int) ([]gateway.LogEntry, error)
	QueryBefore(before string, limit int) ([]gateway.LogEntry, error)
}

const (
	// defaultMaxSize caps a single day's log file. When exceeded the active
	// file is rotated to a numbered sibling so one runaway day cannot fill
	// the disk.
	defaultMaxSize int64 = 64 << 20 // 64 MiB
	// defaultFlushInterval bounds how long a batch may sit in the bufio buffer
	// before it is flushed to disk, trading a little latency for far fewer
	// syscalls under heavy logging.
	defaultFlushInterval = time.Second
	// defaultFlushThreshold forces a flush once this many entries are buffered,
	// complementing the time-based trigger.
	defaultFlushThreshold = 256
)

// FileStore persists structured backend logs to daily JSONL files under dir.
// Each day gets its own file (rotated by size into YYYY-MM-DD.NNN.jsonl) so
// logs are naturally split and old days can be cleaned up independently.
type FileStore struct {
	dir string

	maxSize        int64
	flushInterval  time.Duration
	flushThreshold int

	mu            sync.Mutex
	currentDate   string
	currentFile   *os.File
	currentWriter *bufio.Writer
	currentSize   int64
	pending       int
	lastFlush     time.Time
}

// NewFileStore creates a file-backed log store rooted at dir with default
// batching and rotation settings. Logs are written to dir/backend/.
func NewFileStore(dir string) (*FileStore, error) {
	return newFileStore(dir, defaultMaxSize, defaultFlushInterval, defaultFlushThreshold)
}

func newFileStore(dir string, maxSize int64, flushInterval time.Duration, flushThreshold int) (*FileStore, error) {
	backendDir := filepath.Join(dir, "backend")
	if err := os.MkdirAll(backendDir, 0755); err != nil {
		return nil, fmt.Errorf("logging: mkdir %s: %w", backendDir, err)
	}
	if maxSize <= 0 {
		maxSize = defaultMaxSize
	}
	if flushInterval <= 0 {
		flushInterval = defaultFlushInterval
	}
	if flushThreshold <= 0 {
		flushThreshold = defaultFlushThreshold
	}
	return &FileStore{
		dir:            backendDir,
		maxSize:        maxSize,
		flushInterval:  flushInterval,
		flushThreshold: flushThreshold,
		lastFlush:      time.Now(),
	}, nil
}

// Close flushes and closes the active log file.
func (s *FileStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeCurrent()
}

func (s *FileStore) closeCurrent() error {
	if s.currentWriter != nil {
		if err := s.currentWriter.Flush(); err != nil {
			_ = s.currentFile.Close()
			s.resetCurrent()
			return err
		}
	}
	if s.currentFile != nil {
		if err := s.currentFile.Close(); err != nil {
			s.resetCurrent()
			return err
		}
	}
	s.resetCurrent()
	return nil
}

func (s *FileStore) resetCurrent() {
	s.currentFile = nil
	s.currentWriter = nil
	s.currentDate = ""
	s.currentSize = 0
	s.pending = 0
	s.lastFlush = time.Now()
}

// Flush writes any buffered data to the current file. It is safe to call
// concurrently with Write.
func (s *FileStore) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushLocked()
}

func (s *FileStore) flushLocked() error {
	if s.currentWriter == nil {
		return nil
	}
	if err := s.currentWriter.Flush(); err != nil {
		return err
	}
	s.pending = 0
	s.lastFlush = time.Now()
	return nil
}

// Write appends a log entry to the current day's file. Writes are batched:
// the bufio buffer is flushed only when the flush interval elapses, the
// threshold is reached, the day rolls over, or rotation/Close occurs.
func (s *FileStore) Write(e gateway.LogEntry) error {
	date := dateOf(e.Timestamp)
	if date == "" {
		date = time.Now().UTC().Format("2006-01-02")
	}

	b, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("logging: marshal entry: %w", err)
	}
	line := append(b, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()

	if date != s.currentDate {
		if err := s.closeCurrent(); err != nil {
			return err
		}
		if err := s.openDay(date); err != nil {
			return err
		}
	} else if s.currentWriter == nil {
		if err := s.openDay(date); err != nil {
			return err
		}
	}

	if _, err := s.currentWriter.Write(line); err != nil {
		return fmt.Errorf("logging: write entry: %w", err)
	}
	s.currentSize += int64(len(line))
	s.pending++

	if s.currentSize >= s.maxSize {
		if err := s.rotateLocked(date); err != nil {
			return err
		}
	} else if s.pending >= s.flushThreshold || time.Since(s.lastFlush) >= s.flushInterval {
		if err := s.flushLocked(); err != nil {
			return err
		}
	}
	return nil
}

func (s *FileStore) openDay(date string) error {
	path := filepath.Join(s.dir, date+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("logging: open %s: %w", path, err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("logging: stat %s: %w", path, err)
	}
	s.currentDate = date
	s.currentFile = f
	s.currentWriter = bufio.NewWriter(f)
	s.currentSize = info.Size()
	return nil
}

// rotateLocked closes the current file, renames it to the next available
// YYYY-MM-DD.NNN.jsonl sibling, and opens a fresh active file for the day.
func (s *FileStore) rotateLocked(date string) error {
	if err := s.flushLocked(); err != nil {
		return err
	}
	active := filepath.Join(s.dir, date+".jsonl")
	rotated := s.nextRotatedName(date)
	if err := s.currentFile.Close(); err != nil {
		s.resetCurrent()
		return fmt.Errorf("logging: close for rotate: %w", err)
	}
	if err := os.Rename(active, rotated); err != nil {
		// Best effort: reopen the original so logging can continue.
		_ = reopen(s, date)
		return fmt.Errorf("logging: rotate rename: %w", err)
	}
	s.resetCurrent()
	return s.openDay(date)
}

func reopen(s *FileStore, date string) error {
	s.resetCurrent()
	return s.openDay(date)
}

func (s *FileStore) nextRotatedName(date string) string {
	for i := 1; ; i++ {
		name := filepath.Join(s.dir, fmt.Sprintf("%s.%03d.jsonl", date, i))
		if _, err := os.Stat(name); os.IsNotExist(err) {
			return name
		}
	}
}

// Tail returns the last n entries in chronological order.
func (s *FileStore) Tail(n int) ([]gateway.LogEntry, error) {
	if n <= 0 {
		return nil, nil
	}
	_ = s.Flush()
	files, err := s.listFiles()
	if err != nil {
		return nil, err
	}
	var result []gateway.LogEntry
	for i := len(files) - 1; i >= 0 && len(result) < n; i-- {
		entries, err := tailEntries(files[i], n, int64(n)*2048)
		if err != nil {
			continue
		}
		for j := len(entries) - 1; j >= 0 && len(result) < n; j-- {
			result = append(result, entries[j])
		}
	}
	reverse(result)
	return result, nil
}

// QueryBefore returns up to limit entries with Timestamp strictly before
// the given RFC3339Nano value, in chronological order.
func (s *FileStore) QueryBefore(before string, limit int) ([]gateway.LogEntry, error) {
	if limit <= 0 {
		return nil, nil
	}
	_ = s.Flush()
	files, err := s.listFiles()
	if err != nil {
		return nil, err
	}
	var result []gateway.LogEntry
	for i := len(files) - 1; i >= 0 && len(result) < limit; i-- {
		entries, err := tailEntries(files[i], limit, 16<<20)
		if err != nil {
			continue
		}
		for j := len(entries) - 1; j >= 0 && len(result) < limit; j-- {
			if entries[j].Timestamp < before {
				result = append(result, entries[j])
			}
		}
	}
	reverse(result)
	return result, nil
}

// Cleanup removes log files older than age. Size-rotated siblings are cleaned
// by the same age rule.
func (s *FileStore) Cleanup(age time.Duration) error {
	files, err := s.listFiles()
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-age)
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(f)
		}
	}
	return nil
}

func (s *FileStore) listFiles() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("logging: read dir %s: %w", s.dir, err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		files = append(files, filepath.Join(s.dir, e.Name()))
	}
	sort.Strings(files)
	return files, nil
}

// tailEntries reads up to the last maxLines entries from path in
// chronological order, scanning at most maxScan bytes from the end. It never
// loads the whole file into memory, which keeps large log files (hundreds of
// MiB) from spiking resident memory on every read.
func tailEntries(path string, maxLines int, maxScan int64) ([]gateway.LogEntry, error) {
	lines, err := tailLines(path, maxLines, maxScan)
	if err != nil {
		return nil, err
	}
	out := make([]gateway.LogEntry, 0, len(lines))
	for _, line := range lines {
		var e gateway.LogEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// tailLines returns the last non-empty lines of path (chronological order),
// reading at most maxScan bytes from the end and yielding at most maxLines.
func tailLines(path string, maxLines int, maxScan int64) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	if size == 0 {
		return nil, nil
	}
	if maxScan <= 0 || maxScan > size {
		maxScan = size
	}
	start := size - maxScan
	buf := make([]byte, maxScan)
	if _, err := f.ReadAt(buf, start); err != nil {
		return nil, err
	}
	// Split into lines. If we read from the middle of the file, the first
	// fragment is a partial line; drop it unless we started at offset 0.
	all := strings.Split(string(buf), "\n")
	if start > 0 && len(all) > 0 {
		all = all[1:]
	}
	// Trim a trailing empty element produced by a final newline.
	if n := len(all); n > 0 && all[n-1] == "" {
		all = all[:n-1]
	}
	// Keep only the last maxLines non-empty lines, in order.
	var keep []string
	for i := len(all) - 1; i >= 0 && len(keep) < maxLines; i-- {
		if all[i] == "" {
			continue
		}
		keep = append(keep, all[i])
	}
	reverse(keep)
	return keep, nil
}

func dateOf(ts string) string {
	if ts == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

func reverse[T any](s []T) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}
