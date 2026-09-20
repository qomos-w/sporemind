package logging

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/qomos-w/gospore/gateway"
)

// ConsoleEntry mirrors the frontend console-patch store entry.
type ConsoleEntry struct {
	Level    string `json:"level"`
	Message  string `json:"message"`
	Time     int64  `json:"time"`
	Location string `json:"location,omitempty"`
}

// ConsoleStore persists frontend console logs to daily JSONL files, batched
// and size-rotated just like FileStore.
type ConsoleStore struct {
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

	// streamer forwards every persisted entry to the live log stream with
	// source "console", so the frontend log viewer can switch between
	// backend and app console streams through the same workspace.log event.
	streamer *LogStreamer
}

// NewConsoleStore creates a file-backed console log store rooted at dir.
// Logs are written to dir/console/.
func NewConsoleStore(dir string) (*ConsoleStore, error) {
	consoleDir := filepath.Join(dir, "console")
	if err := os.MkdirAll(consoleDir, 0755); err != nil {
		return nil, fmt.Errorf("logging: mkdir %s: %w", consoleDir, err)
	}
	return &ConsoleStore{
		dir:            consoleDir,
		maxSize:        defaultMaxSize,
		flushInterval:  defaultFlushInterval,
		flushThreshold: defaultFlushThreshold,
		lastFlush:      time.Now(),
	}, nil
}

// Close flushes and closes the active log file.
func (s *ConsoleStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeCurrent()
}

func (s *ConsoleStore) closeCurrent() error {
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

func (s *ConsoleStore) resetCurrent() {
	s.currentFile = nil
	s.currentWriter = nil
	s.currentDate = ""
	s.currentSize = 0
	s.pending = 0
	s.lastFlush = time.Now()
}

// Flush writes any buffered data to the current file.
func (s *ConsoleStore) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushLocked()
}

func (s *ConsoleStore) flushLocked() error {
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

// Write appends a console entry to the current day's file. Writes are batched
// the same way as FileStore. After persisting, the entry is forwarded to the
// live log streamer (if configured) with source "console".
func (s *ConsoleStore) Write(e ConsoleEntry) error {
	date := time.UnixMilli(e.Time).UTC().Format("2006-01-02")

	b, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("logging: marshal console entry: %w", err)
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
		return fmt.Errorf("logging: write console entry: %w", err)
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

	// Forward to the live streamer (outside the lock, non-blocking).
	if s.streamer != nil {
		entry := gateway.LogEntry{
			Timestamp: formatConsoleTimestamp(e.Time),
			Level:     e.Level,
			Caller:    e.Location,
			Message:   e.Message,
		}
		s.streamer.Push(entry, SourceConsole)
	}
	return nil
}

// SetStreamer wires a batched live-log streamer so every persisted console
// entry is forwarded with source "console".
func (s *ConsoleStore) SetStreamer(st *LogStreamer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.streamer = st
}

// formatConsoleTimestamp converts a frontend console entry millisecond epoch
// to an RFC3339Nano string so console entries share the same Timestamp shape
// as backend log entries.
func formatConsoleTimestamp(ms int64) string {
	return time.UnixMilli(ms).UTC().Format(time.RFC3339Nano)
}

func (s *ConsoleStore) openDay(date string) error {
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

func (s *ConsoleStore) rotateLocked(date string) error {
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
		s.resetCurrent()
		_ = s.openDay(date)
		return fmt.Errorf("logging: rotate rename: %w", err)
	}
	s.resetCurrent()
	return s.openDay(date)
}

func (s *ConsoleStore) nextRotatedName(date string) string {
	for i := 1; ; i++ {
		name := filepath.Join(s.dir, fmt.Sprintf("%s.%03d.jsonl", date, i))
		if _, err := os.Stat(name); os.IsNotExist(err) {
			return name
		}
	}
}

// Tail returns the last n console entries in chronological order.
func (s *ConsoleStore) Tail(n int) ([]ConsoleEntry, error) {
	if n <= 0 {
		return nil, nil
	}
	_ = s.Flush()
	files, err := s.listFiles()
	if err != nil {
		return nil, err
	}
	var result []ConsoleEntry
	for i := len(files) - 1; i >= 0 && len(result) < n; i-- {
		entries, err := tailConsoleEntries(files[i], n, int64(n)*2048)
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

// parseBeforeCursor parses a Before cursor. Cursors normally round-trip from
// a previous response as RFC3339Nano UTC, but callers also hand-write plain
// "2006-01-02T15:04:05" timestamps stripped of their zone; treat those as UTC
// per the UTC-storage convention instead of failing the whole query.
func parseBeforeCursor(before string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, before); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02T15:04:05.999999999", before, time.UTC); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("logging: parse before %q: not a timestamp", before)
}

// QueryBefore returns up to limit console entries with Time strictly before
// the given timestamp, in chronological order. The before cursor
// comes from a previous response's NextBefore (itself an entry timestamp), so
// parsing it back to milliseconds keeps the comparison exact.
func (s *ConsoleStore) QueryBefore(before string, limit int) ([]ConsoleEntry, error) {
	if limit <= 0 {
		return nil, nil
	}
	t, err := parseBeforeCursor(before)
	if err != nil {
		return nil, err
	}
	beforeMs := t.UnixMilli()
	_ = s.Flush()
	files, err := s.listFiles()
	if err != nil {
		return nil, err
	}
	var result []ConsoleEntry
	for i := len(files) - 1; i >= 0 && len(result) < limit; i-- {
		entries, err := scanConsoleFileBackward(files[i], beforeMs, limit)
		if err != nil {
			continue
		}
		for j := len(entries) - 1; j >= 0 && len(result) < limit; j-- {
			result = append(result, entries[j])
		}
	}
	reverse(result)
	return result, nil
}

// scanConsoleFileBackward reads path from its end backwards, collecting up to
// limit entries with Time < beforeMs. When the tail window is a dense run of
// newer entries (a page boundary deep inside a busy file), the window widens
// until the older entries further up the file are found, so paging never
// silently skips a segment.
func scanConsoleFileBackward(path string, beforeMs int64, limit int) ([]ConsoleEntry, error) {
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
	window := int64(limit) * 2048
	if window > size {
		window = size
	}
	nl := []byte{10}
	for {
		buf := make([]byte, window)
		start := size - window
		if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
			return nil, err
		}
		all := bytes.Split(buf, nl)
		// The first fragment is a partial line unless we started at offset 0.
		if start > 0 && len(all) > 0 {
			all = all[1:]
		}
		if n := len(all); n > 0 && len(all[n-1]) == 0 {
			all = all[:n-1]
		}
		var result []ConsoleEntry
		for i := len(all) - 1; i >= 0 && len(result) < limit; i-- {
			if len(all[i]) == 0 {
				continue
			}
			var e ConsoleEntry
			if err := json.Unmarshal(all[i], &e); err != nil {
				continue
			}
			if e.Time < beforeMs {
				result = append(result, e)
			}
		}
		if len(result) == limit || start == 0 {
			reverse(result)
			return result, nil
		}
		// Dense newer run hid older entries; widen the window and rescan.
		window *= 2
		if window > size {
			window = size
		}
	}
}

// Cleanup removes log files older than age. Size-rotated siblings are cleaned
// by the same age rule.
func (s *ConsoleStore) Cleanup(age time.Duration) error {
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

func (s *ConsoleStore) listFiles() ([]string, error) {
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

func tailConsoleEntries(path string, maxLines int, maxScan int64) ([]ConsoleEntry, error) {
	lines, err := tailLines(path, maxLines, maxScan)
	if err != nil {
		return nil, err
	}
	out := make([]ConsoleEntry, 0, len(lines))
	for _, line := range lines {
		var e ConsoleEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}
