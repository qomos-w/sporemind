//go:build darwin

// Package cgmac is the macOS backend for computeruse. To keep the
// dependency surface small it shells out to the standard system
// utilities (screencapture, osascript, pbcopy/pbpaste, cliclick) rather
// than linking AppKit/Quartz via cgo. This is a "good enough" backend —
// rendering is correct and input works on standard installs, but
// per-call fork overhead means it's not suitable for high-FPS streaming.
package cgmac

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qomos-w/sporemind/pkg/actor/computeruse/internal/capture"
	"github.com/qomos-w/sporemind/pkg/actor/computeruse/internal/capture/encoder"
	"github.com/qomos-w/sporemind/pkg/actor/computeruse/internal/capture/ocr"
)

func init() {
	capture.NewDarwinCapturer = NewCapturer
}

// Capturer is the macOS backend implementation.
type Capturer struct {
	capture.BaseCapturer

	mu         sync.Mutex
	elementMap map[string]elementHandle
}

type elementHandle struct {
	app    string
	bounds image.Rectangle
	name   string
}

// NewCapturer returns a new macOS capturer.
func NewCapturer() (capture.Capturer, error) {
	if _, err := exec.LookPath("screencapture"); err != nil {
		return nil, errors.New("computeruse(darwin): screencapture not in PATH")
	}
	return &Capturer{elementMap: map[string]elementHandle{}}, nil
}

func (c *Capturer) Close() error { return nil }

// =========================================================================
// Capture.
// =========================================================================

func (c *Capturer) Capture(opts capture.CaptureOptions) (*capture.Frame, error) {
	opts.Validate()
	t0 := time.Now()

	tmpFile, err := os.CreateTemp("", "computeruse-mac-*.png")
	if err != nil {
		return nil, err
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	args := []string{"-x", "-t", "png"}
	var bounds image.Rectangle
	switch opts.Mode {
	case capture.ModeRegion:
		if opts.Region.Empty() {
			return nil, errors.New("region is required for ModeRegion")
		}
		// screencapture -R x,y,w,h
		args = append(args, "-R", fmt.Sprintf("%d,%d,%d,%d",
			opts.Region.Min.X, opts.Region.Min.Y,
			opts.Region.Dx(), opts.Region.Dy()))
		bounds = opts.Region
	default:
		// full main display
		ds, err := c.Displays()
		if err == nil && len(ds) > 0 {
			bounds = ds[0].Bounds
		}
	}
	args = append(args, tmpPath)

	cmd := exec.Command("screencapture", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("screencapture: %v: %s", err, stderr.String())
	}
	data, err := os.ReadFile(tmpPath)
	if err != nil {
		return nil, err
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("png decode: %w", err)
	}
	w := img.Bounds().Dx()
	h := img.Bounds().Dy()
	pix := imageToRGBA(img).Pix
	if bounds.Empty() {
		bounds = image.Rect(0, 0, w, h)
	}

	frame := &capture.Frame{
		RawPixels:       pix,
		Bounds:          bounds,
		Timestamp:       time.Now(),
		DisplayID:       opts.DisplayID,
		EncodingFormat:  opts.EncodeFormat,
		EncodingQuality: opts.Quality,
		FrameIndex:      c.NextFrameIndex(),
		Duration:        time.Since(t0),
	}
	if opts.IncludeCursor {
		if pt, err := c.CursorPosition(); err == nil {
			frame.CursorPos = pt
			frame.CursorValid = true
		}
	}
	if opts.EncodeFormat != "raw" {
		if enc, err := encoder.New(opts.EncodeFormat); err == nil {
			if encData, err := enc.Encode(pix, image.Rect(0, 0, w, h), opts.Quality); err == nil {
				frame.Data = encData
			}
		}
	} else {
		frame.Data = data
	}
	c.SetLastFrame(frame)
	return frame, nil
}

func (c *Capturer) CaptureWindow(windowID int, opts capture.CaptureOptions) (*capture.Frame, error) {
	opts.Validate()
	t0 := time.Now()
	tmpFile, err := os.CreateTemp("", "computeruse-mac-*.png")
	if err != nil {
		return nil, err
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)
	// screencapture -l <wid> captures a window.
	cmd := exec.Command("screencapture", "-x", "-t", "png", "-l", strconv.Itoa(windowID), tmpPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("screencapture window: %v: %s", err, stderr.String())
	}
	data, err := os.ReadFile(tmpPath)
	if err != nil {
		return nil, err
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	w := img.Bounds().Dx()
	h := img.Bounds().Dy()
	pix := imageToRGBA(img).Pix
	frame := &capture.Frame{
		RawPixels:       pix,
		Bounds:          image.Rect(0, 0, w, h),
		Timestamp:       time.Now(),
		DisplayID:       -1,
		EncodingFormat:  opts.EncodeFormat,
		EncodingQuality: opts.Quality,
		FrameIndex:      c.NextFrameIndex(),
		Duration:        time.Since(t0),
	}
	if opts.EncodeFormat == "raw" {
		frame.Data = data
	} else {
		if enc, err := encoder.New(opts.EncodeFormat); err == nil {
			if encData, err := enc.Encode(pix, image.Rect(0, 0, w, h), opts.Quality); err == nil {
				frame.Data = encData
			}
		}
	}
	c.SetLastFrame(frame)
	return frame, nil
}

// =========================================================================
// Displays, windows, processes.
// =========================================================================

func (c *Capturer) Displays() ([]capture.DisplayInfo, error) {
	// system_profiler SPDisplaysDataType — heavyweight. Use osascript to
	// query AppleScript's "size of bounds of desktop" for main display.
	script := `tell application "Finder" to get bounds of window of desktop`
	out, err := runOutput("osascript", "-e", script)
	if err != nil {
		return nil, fmt.Errorf("osascript displays: %w", err)
	}
	// out like "0, 0, 1920, 1080"
	fields := strings.Split(strings.TrimSpace(out), ", ")
	if len(fields) < 4 {
		return nil, errors.New("unparseable display bounds")
	}
	x1, _ := strconv.Atoi(fields[0])
	y1, _ := strconv.Atoi(fields[1])
	x2, _ := strconv.Atoi(fields[2])
	y2, _ := strconv.Atoi(fields[3])
	return []capture.DisplayInfo{{
		ID:        0,
		Name:      "main",
		Bounds:    image.Rect(x1, y1, x2, y2),
		IsPrimary: true,
	}}, nil
}

func (c *Capturer) Windows() ([]capture.WindowInfo, error) {
	return c.FilteredWindows(capture.WindowFilter{})
}

func (c *Capturer) FilteredWindows(f capture.WindowFilter) ([]capture.WindowInfo, error) {
	// AppleScript: enumerate visible windows across processes.
	script := `tell application "System Events"
		set wlist to ""
		repeat with proc in (processes whose visible is true)
			set pname to name of proc
			set ppid to unix id of proc
			repeat with w in (windows of proc)
				try
					set wname to name of w
					set wpos to position of w
					set wsize to size of w
					set wlist to wlist & pname & "|" & ppid & "|" & wname & "|" & (item 1 of wpos) & "|" & (item 2 of wpos) & "|" & (item 1 of wsize) & "|" & (item 2 of wsize) & linefeed
				end try
			end repeat
		end repeat
		return wlist
	end tell`
	out, err := runOutput("osascript", "-e", script)
	if err != nil {
		return nil, fmt.Errorf("osascript windows: %w", err)
	}
	var wins []capture.WindowInfo
	id := 1
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, "|")
		if len(fields) < 7 {
			continue
		}
		pname := fields[0]
		pid, _ := strconv.Atoi(fields[1])
		title := fields[2]
		x, _ := strconv.Atoi(fields[3])
		y, _ := strconv.Atoi(fields[4])
		w, _ := strconv.Atoi(fields[5])
		h, _ := strconv.Atoi(fields[6])
		wi := capture.WindowInfo{
			ID:          id,
			Title:       title,
			Bounds:      image.Rect(x, y, x+w, y+h),
			IsVisible:   true,
			ProcessName: pname,
			PID:         pid,
		}
		if !matchFilter(wi, f) {
			id++
			continue
		}
		wins = append(wins, wi)
		id++
	}
	return wins, nil
}

func (c *Capturer) WindowInfo(windowID int) (capture.DetailedWindowInfo, error) {
	wins, err := c.Windows()
	if err != nil {
		return capture.DetailedWindowInfo{}, err
	}
	for _, w := range wins {
		if w.ID == windowID {
			return capture.DetailedWindowInfo{WindowInfo: w, State: "normal"}, nil
		}
	}
	return capture.DetailedWindowInfo{}, fmt.Errorf("window not found: %d", windowID)
}

func (c *Capturer) Processes(nameContains string, pid int) ([]capture.ProcessInfo, error) {
	out, err := runOutput("ps", "-eo", "pid,ppid,comm")
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	var procs []capture.ProcessInfo
	needle := strings.ToLower(nameContains)
	for i, line := range strings.Split(out, "\n") {
		if i == 0 {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		p, _ := strconv.Atoi(fields[0])
		pp, _ := strconv.Atoi(fields[1])
		name := strings.Join(fields[2:], " ")
		if pid > 0 && p != pid {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(name), needle) {
			continue
		}
		procs = append(procs, capture.ProcessInfo{PID: p, ParentPID: pp, Name: name, ExePath: name})
	}
	return procs, nil
}

func (c *Capturer) FocusWindow(windowID int) error {
	wins, err := c.Windows()
	if err != nil {
		return err
	}
	for _, w := range wins {
		if w.ID == windowID {
			script := fmt.Sprintf(`tell application %q to activate`, w.ProcessName)
			return run("osascript", "-e", script)
		}
	}
	return fmt.Errorf("window not found: %d", windowID)
}

// =========================================================================
// Cursor / input.
// =========================================================================

func (c *Capturer) CursorPosition() (image.Point, error) {
	if _, err := exec.LookPath("cliclick"); err == nil {
		out, err := runOutput("cliclick", "p")
		if err != nil {
			return image.Point{}, err
		}
		// "123,456"
		parts := strings.Split(strings.TrimSpace(out), ",")
		if len(parts) != 2 {
			return image.Point{}, errors.New("cliclick p: bad output")
		}
		x, _ := strconv.Atoi(parts[0])
		y, _ := strconv.Atoi(parts[1])
		pt := image.Point{X: x, Y: y}
		c.TrackCursor(pt)
		return pt, nil
	}
	return image.Point{}, errors.New("cursor position: cliclick not installed")
}

func (c *Capturer) MoveCursor(x, y int) error {
	return run("cliclick", fmt.Sprintf("m:%d,%d", x, y))
}

func (c *Capturer) Click(x, y int, button string) error {
	prefix := "c"
	switch strings.ToLower(button) {
	case "right":
		prefix = "rc"
	case "middle":
		return errors.New("middle click: not supported on macOS (cliclick has no middle button)")
	}
	return run("cliclick", fmt.Sprintf("%s:%d,%d", prefix, x, y))
}

func (c *Capturer) Scroll(x, y, dx, dy int) error {
	if err := c.MoveCursor(x, y); err != nil {
		return err
	}
	if _, err := exec.LookPath("cliclick"); err != nil {
		return errors.New("scroll: cliclick not installed")
	}
	// cliclick uses w:dx,dy (mouse wheel)
	return run("cliclick", fmt.Sprintf("w:%d,%d", dx, dy))
}

func (c *Capturer) Type(text string) error {
	return run("cliclick", "t:"+text)
}

func (c *Capturer) KeyPress(key string, modifiers ...string) error {
	if len(modifiers) > 0 {
		// kd / ku for key down / up of modifiers
		for _, m := range modifiers {
			if err := run("cliclick", "kd:"+m); err != nil {
				return err
			}
		}
		defer func() {
			for i := len(modifiers) - 1; i >= 0; i-- {
				_ = run("cliclick", "ku:"+modifiers[i])
			}
		}()
	}
	return run("cliclick", "kp:"+key)
}

func (c *Capturer) Drag(fromX, fromY, toX, toY int, button string) error {
	switch strings.ToLower(button) {
	case "right", "middle":
		return fmt.Errorf("drag(%s): not supported on macOS (cliclick only supports left-button drag)", button)
	}
	if err := run("cliclick", fmt.Sprintf("dd:%d,%d", fromX, fromY)); err != nil {
		return err
	}
	// move intermediate points
	for i := 1; i <= 8; i++ {
		ix := fromX + (toX-fromX)*i/8
		iy := fromY + (toY-fromY)*i/8
		_ = run("cliclick", fmt.Sprintf("m:%d,%d", ix, iy))
		time.Sleep(5 * time.Millisecond)
	}
	return run("cliclick", fmt.Sprintf("du:%d,%d", toX, toY))
}

func (c *Capturer) KeyDown(key string, modifiers ...string) error {
	for _, m := range modifiers {
		if err := run("cliclick", "kd:"+m); err != nil {
			return err
		}
	}
	return run("cliclick", "kd:"+key)
}

func (c *Capturer) KeyUp(key string, modifiers ...string) error {
	if err := run("cliclick", "ku:"+key); err != nil {
		return err
	}
	for i := len(modifiers) - 1; i >= 0; i-- {
		if err := run("cliclick", "ku:"+modifiers[i]); err != nil {
			return err
		}
	}
	return nil
}

// =========================================================================
// Window control.
// =========================================================================

func (c *Capturer) SetWindowState(windowID int, state string) error {
	wins, err := c.Windows()
	if err != nil {
		return err
	}
	var app, title string
	for _, w := range wins {
		if w.ID == windowID {
			app = w.ProcessName
			title = w.Title
			break
		}
	}
	if app == "" {
		return fmt.Errorf("window not found: %d", windowID)
	}
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "minimize":
		script := fmt.Sprintf(`tell application "System Events" to tell process %q to set value of attribute "AXMinimized" of window %q to true`, app, title)
		return run("osascript", "-e", script)
	case "restore":
		script := fmt.Sprintf(`tell application "System Events" to tell process %q to set value of attribute "AXMinimized" of window %q to false`, app, title)
		return run("osascript", "-e", script)
	case "maximize":
		// Best-effort: zoom button.
		script := fmt.Sprintf(`tell application "System Events" to tell process %q to perform action "AXZoomWindow" of window %q`, app, title)
		return run("osascript", "-e", script)
	}
	return fmt.Errorf("unknown state: %s", state)
}

func (c *Capturer) MoveWindow(windowID int, x, y, width, height int) error {
	wins, err := c.Windows()
	if err != nil {
		return err
	}
	var app, title string
	var cur image.Rectangle
	for _, w := range wins {
		if w.ID == windowID {
			app = w.ProcessName
			title = w.Title
			cur = w.Bounds
			break
		}
	}
	if app == "" {
		return fmt.Errorf("window not found: %d", windowID)
	}
	if x == 0 && y == 0 {
		x, y = cur.Min.X, cur.Min.Y
	}
	if width == 0 && height == 0 {
		width, height = cur.Dx(), cur.Dy()
	}
	script := fmt.Sprintf(`tell application "System Events" to tell process %q to tell window %q
		set position to {%d, %d}
		set size to {%d, %d}
	end tell`, app, title, x, y, width, height)
	return run("osascript", "-e", script)
}

func (c *Capturer) CloseWindow(windowID int) error {
	wins, err := c.Windows()
	if err != nil {
		return err
	}
	for _, w := range wins {
		if w.ID == windowID {
			script := fmt.Sprintf(`tell application "System Events" to tell process %q to click button 1 of window %q`, w.ProcessName, w.Title)
			return run("osascript", "-e", script)
		}
	}
	return fmt.Errorf("window not found: %d", windowID)
}

// =========================================================================
// Clipboard.
// =========================================================================

func (c *Capturer) SetClipboard(text string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

func (c *Capturer) GetClipboard() (string, error) {
	return runOutput("pbpaste")
}

func (c *Capturer) GetClipboardData() (capture.ClipboardData, error) {
	d := capture.ClipboardData{Kind: "empty"}
	formats := 0
	if t, err := runOutput("pbpaste"); err == nil && t != "" {
		d.Text = t
		formats++
	}
	// HTML.
	if h, err := runOutput("pbpaste", "-Prefer", "html"); err == nil && h != "" && h != d.Text {
		d.HTML = h
		formats++
	}
	// macOS clipboard binary types (image, file URLs) require AppKit;
	// we leave Image/Files unset on the shell-out path.
	switch {
	case formats == 0:
		d.Kind = "empty"
	case formats == 1 && d.Text != "":
		d.Kind = "text"
	default:
		d.Kind = "mixed"
	}
	return d, nil
}

func (c *Capturer) SetClipboardData(d capture.ClipboardData) error {
	if d.Image != nil {
		// AppleScript path: write to temp, then `set the clipboard to (read tmp as «class PNGf»)`.
		tmpFile, err := os.CreateTemp("", "cb-img-*.png")
		if err != nil {
			return err
		}
		tmpPath := tmpFile.Name()
		if err := png.Encode(tmpFile, d.Image); err != nil {
			tmpFile.Close()
			os.Remove(tmpPath)
			return err
		}
		tmpFile.Close()
		defer os.Remove(tmpPath)
		script := fmt.Sprintf(`set the clipboard to (read POSIX file %q as «class PNGf»)`, tmpPath)
		return run("osascript", "-e", script)
	}
	if d.HTML != "" {
		// macOS will infer HTML when pbcopy receives HTML and -Prefer is set.
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(d.HTML)
		return cmd.Run()
	}
	if len(d.Files) > 0 {
		// Set clipboard to list of file POSIX paths as text — coarse fallback.
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(strings.Join(d.Files, "\n"))
		return cmd.Run()
	}
	if d.Text != "" {
		return c.SetClipboard(d.Text)
	}
	return errors.New("clipboard: no data provided")
}

// =========================================================================
// UI Automation — degenerate window-tree.
// =========================================================================

func (c *Capturer) ListElements(f capture.ElementFilter) ([]capture.ElementNode, error) {
	wins, err := c.Windows()
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]capture.ElementNode, 0, len(wins))
	for _, w := range wins {
		if f.WindowID != 0 && w.ID != f.WindowID {
			continue
		}
		id := elementIDFromWindow(w.ID)
		c.elementMap[id] = elementHandle{app: w.ProcessName, bounds: w.Bounds, name: w.Title}
		out = append(out, capture.ElementNode{
			ID:          id,
			Name:        w.Title,
			ClassName:   "Window",
			ControlType: "window",
			Bounds:      w.Bounds,
			IsEnabled:   true,
		})
	}
	return out, nil
}

func (c *Capturer) ElementInfo(elementID string) (capture.ElementNode, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h, ok := c.elementMap[elementID]
	if !ok {
		return capture.ElementNode{}, fmt.Errorf("element not found: %s", elementID)
	}
	return capture.ElementNode{
		ID: elementID, Name: h.name, ClassName: "Window", ControlType: "window",
		Bounds: h.bounds, IsEnabled: true,
	}, nil
}

func (c *Capturer) ClickElement(elementID, button string) error {
	c.mu.Lock()
	h, ok := c.elementMap[elementID]
	c.mu.Unlock()
	if !ok {
		return fmt.Errorf("element not found: %s", elementID)
	}
	cx := (h.bounds.Min.X + h.bounds.Max.X) / 2
	cy := (h.bounds.Min.Y + h.bounds.Max.Y) / 2
	return c.Click(cx, cy, button)
}

func (c *Capturer) FocusElement(elementID string) error {
	c.mu.Lock()
	h, ok := c.elementMap[elementID]
	c.mu.Unlock()
	if !ok {
		return fmt.Errorf("element not found: %s", elementID)
	}
	return run("osascript", "-e", fmt.Sprintf(`tell application %q to activate`, h.app))
}

func (c *Capturer) SetElementValue(elementID, value string) error {
	return errors.New("set_element_value: not supported on darwin shell backend")
}

// =========================================================================
// OCR — shared OCR shim (PaddleOCR first, tesseract fallback).
// =========================================================================

func (c *Capturer) OCR(pixels []byte, width, height int, language string) (capture.OcrResult, error) {
	res, err := ocr.Run(pixels, width, height, language, image.Rect(0, 0, width, height))
	if err != nil {
		return capture.OcrResult{}, err
	}
	out := capture.OcrResult{
		Text: res.Text, Engine: res.Engine, Bounds: res.Bounds,
	}
	for _, l := range res.Lines {
		out.Lines = append(out.Lines, capture.OcrLine{
			Text: l.Text, Bounds: l.Bounds, Confidence: l.Confidence,
		})
	}
	return out, nil
}

// =========================================================================
// Helpers.
// =========================================================================

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %v: %s", name, err, stderr.String())
	}
	return nil
}

func runOutput(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %v: %s", name, err, stderr.String())
	}
	return stdout.String(), nil
}

func matchFilter(w capture.WindowInfo, f capture.WindowFilter) bool {
	if f.PID > 0 && w.PID != f.PID {
		return false
	}
	if f.ProcessName != "" && !strings.Contains(strings.ToLower(w.ProcessName), strings.ToLower(f.ProcessName)) {
		return false
	}
	if f.TitlePattern != "" && !strings.Contains(strings.ToLower(w.Title), strings.ToLower(f.TitlePattern)) {
		return false
	}
	if f.AtPoint != nil {
		if !(f.AtPoint.X >= w.Bounds.Min.X && f.AtPoint.X < w.Bounds.Max.X &&
			f.AtPoint.Y >= w.Bounds.Min.Y && f.AtPoint.Y < w.Bounds.Max.Y) {
			return false
		}
	}
	if !f.IncludeInvisible && !w.IsVisible {
		return false
	}
	return true
}

func elementIDFromWindow(id int) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(id))
	return "darwin/" + base64.RawURLEncoding.EncodeToString(buf[:])
}

func imageToRGBA(src image.Image) *image.RGBA {
	if r, ok := src.(*image.RGBA); ok {
		return r
	}
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			r, g, bb, a := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
			i := (y*b.Dx() + x) * 4
			dst.Pix[i+0] = uint8(r >> 8)
			dst.Pix[i+1] = uint8(g >> 8)
			dst.Pix[i+2] = uint8(bb >> 8)
			dst.Pix[i+3] = uint8(a >> 8)
		}
	}
	return dst
}
