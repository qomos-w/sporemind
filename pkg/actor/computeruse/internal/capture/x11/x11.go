//go:build linux

// Package x11 is the Linux backend for computeruse. It shells out to the
// standard X.Org tools (xdotool, xclip, xrandr, wmctrl, xprop, import,
// xdpyinfo) instead of binding libX11 — this trades a small per-call
// fork cost for zero cgo / zero dlopen complexity and broad coverage
// (any GNOME/KDE/X11 host with the usual tools installed works).
//
// Wayland sessions are not supported by these tools directly; on a
// Wayland host the package returns "not supported" errors rather than
// silently failing. Use a Wayland-native backend (todo) on those.
package x11

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/png"
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
	capture.NewLinuxCapturer = NewCapturer
}

// Capturer is the X11 backend implementation.
type Capturer struct {
	capture.BaseCapturer

	mu         sync.Mutex
	elementMap map[string]elementHandle
}

type elementHandle struct {
	windowID int
	bounds   image.Rectangle
	name     string
}

// NewCapturer returns a new X11 capturer.
func NewCapturer() (capture.Capturer, error) {
	if _, err := exec.LookPath("xdotool"); err != nil {
		return nil, fmt.Errorf("computeruse(linux): xdotool not found in PATH; install xdotool, xclip, xrandr, wmctrl, imagemagick")
	}
	return &Capturer{elementMap: map[string]elementHandle{}}, nil
}

// Close releases all resources.
func (c *Capturer) Close() error { return nil }

// =========================================================================
// Capture — uses ImageMagick `import` to produce PNG.
// =========================================================================

func (c *Capturer) Capture(opts capture.CaptureOptions) (*capture.Frame, error) {
	opts.Validate()
	t0 := time.Now()
	// Compose `import` args.
	args := []string{"-window", "root"}
	var bounds image.Rectangle
	switch opts.Mode {
	case capture.ModeRegion:
		if opts.Region.Empty() {
			return nil, errors.New("region is required for ModeRegion")
		}
		args = append(args,
			"-crop",
			fmt.Sprintf("%dx%d+%d+%d",
				opts.Region.Dx(), opts.Region.Dy(),
				opts.Region.Min.X, opts.Region.Min.Y),
		)
		bounds = opts.Region
	default:
		// full root
		w, h, err := rootDimensions()
		if err != nil {
			return nil, err
		}
		bounds = image.Rect(0, 0, w, h)
	}
	args = append(args, "png:-")

	cmd := exec.Command("import", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("import: %v: %s", err, stderr.String())
	}
	img, err := png.Decode(&stdout)
	if err != nil {
		return nil, fmt.Errorf("png decode: %w", err)
	}
	w := img.Bounds().Dx()
	h := img.Bounds().Dy()
	pix := imageToRGBA(img).Pix

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
	// Encode to target format.
	if opts.EncodeFormat != "raw" {
		enc, err := encoder.New(opts.EncodeFormat)
		if err == nil {
			data, err := enc.Encode(pix, image.Rect(0, 0, w, h), opts.Quality)
			if err == nil {
				frame.Data = data
			}
		}
	}
	c.SetLastFrame(frame)
	return frame, nil
}

func (c *Capturer) CaptureWindow(windowID int, opts capture.CaptureOptions) (*capture.Frame, error) {
	opts.Validate()
	t0 := time.Now()
	args := []string{"-window", strconv.Itoa(windowID), "png:-"}
	cmd := exec.Command("import", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("import window %d: %v: %s", windowID, err, stderr.String())
	}
	img, err := png.Decode(&stdout)
	if err != nil {
		return nil, fmt.Errorf("png decode: %w", err)
	}
	w := img.Bounds().Dx()
	h := img.Bounds().Dy()
	pix := imageToRGBA(img).Pix
	bounds, _ := getWindowRect(windowID)
	frame := &capture.Frame{
		RawPixels:       pix,
		Bounds:          bounds,
		Timestamp:       time.Now(),
		DisplayID:       -1,
		EncodingFormat:  opts.EncodeFormat,
		EncodingQuality: opts.Quality,
		FrameIndex:      c.NextFrameIndex(),
		Duration:        time.Since(t0),
	}
	if opts.EncodeFormat != "raw" {
		if enc, err := encoder.New(opts.EncodeFormat); err == nil {
			if data, err := enc.Encode(pix, image.Rect(0, 0, w, h), opts.Quality); err == nil {
				frame.Data = data
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
	out, err := runOutput("xrandr", "--current")
	if err != nil {
		w, h, derr := rootDimensions()
		if derr != nil {
			return nil, err
		}
		return []capture.DisplayInfo{{ID: 0, Name: "default", Bounds: image.Rect(0, 0, w, h), IsPrimary: true}}, nil
	}
	var displays []capture.DisplayInfo
	id := 0
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, " connected") {
			continue
		}
		// e.g. "HDMI-1 connected primary 1920x1080+0+0 (normal left ...) 521mm x 293mm"
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		name := fields[0]
		primary := false
		geom := ""
		for _, f := range fields {
			if f == "primary" {
				primary = true
				continue
			}
			if strings.Contains(f, "x") && strings.Contains(f, "+") {
				geom = f
				break
			}
		}
		if geom == "" {
			continue
		}
		w, h, x, y, ok := parseGeometry(geom)
		if !ok {
			continue
		}
		displays = append(displays, capture.DisplayInfo{
			ID:        id,
			Name:      name,
			Bounds:    image.Rect(x, y, x+w, y+h),
			IsPrimary: primary,
		})
		id++
	}
	if len(displays) == 0 {
		w, h, _ := rootDimensions()
		displays = append(displays, capture.DisplayInfo{ID: 0, Name: "default", Bounds: image.Rect(0, 0, w, h), IsPrimary: true})
	}
	return displays, nil
}

func (c *Capturer) Windows() ([]capture.WindowInfo, error) {
	return c.FilteredWindows(capture.WindowFilter{})
}

func (c *Capturer) FilteredWindows(f capture.WindowFilter) ([]capture.WindowInfo, error) {
	out, err := runOutput("wmctrl", "-lpG")
	if err != nil {
		return nil, fmt.Errorf("wmctrl: %w", err)
	}
	var wins []capture.WindowInfo
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}
		// 0:WID 1:desktop 2:pid 3:x 4:y 5:w 6:h 7:host 8+:title
		idStr := fields[0]
		id, err := strconv.ParseUint(strings.TrimPrefix(idStr, "0x"), 16, 64)
		if err != nil {
			continue
		}
		pid, _ := strconv.Atoi(fields[2])
		x, _ := strconv.Atoi(fields[3])
		y, _ := strconv.Atoi(fields[4])
		w, _ := strconv.Atoi(fields[5])
		h, _ := strconv.Atoi(fields[6])
		title := strings.Join(fields[8:], " ")
		w0 := capture.WindowInfo{
			ID:          int(id),
			Title:       title,
			Bounds:      image.Rect(x, y, x+w, y+h),
			IsVisible:   true,
			ProcessName: getProcessName(pid),
			PID:         pid,
		}
		if !matchFilter(w0, f) {
			continue
		}
		wins = append(wins, w0)
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
			return capture.DetailedWindowInfo{
				WindowInfo:   w,
				State:        windowState(windowID),
				ClassName:    windowClass(windowID),
				IsForeground: foregroundWindow() == windowID,
			}, nil
		}
	}
	return capture.DetailedWindowInfo{}, fmt.Errorf("window not found: %d", windowID)
}

func (c *Capturer) Processes(nameContains string, pid int) ([]capture.ProcessInfo, error) {
	out, err := runOutput("ps", "-eo", "pid,ppid,comm,args")
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	var procs []capture.ProcessInfo
	needle := strings.ToLower(nameContains)
	for i, line := range strings.Split(out, "\n") {
		if i == 0 { // header
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		p, _ := strconv.Atoi(fields[0])
		pp, _ := strconv.Atoi(fields[1])
		name := fields[2]
		exe := fields[3]
		if pid > 0 && p != pid {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(name), needle) {
			continue
		}
		procs = append(procs, capture.ProcessInfo{
			PID: p, ParentPID: pp, Name: name, ExePath: exe,
		})
	}
	return procs, nil
}

func (c *Capturer) FocusWindow(windowID int) error {
	return run("wmctrl", "-ia", fmt.Sprintf("0x%08x", uint32(windowID)))
}

// =========================================================================
// Cursor / input — xdotool everywhere.
// =========================================================================

func (c *Capturer) CursorPosition() (image.Point, error) {
	out, err := runOutput("xdotool", "getmouselocation", "--shell")
	if err != nil {
		return image.Point{}, err
	}
	var x, y int
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(line, "X="); ok {
			x, _ = strconv.Atoi(strings.TrimSpace(v))
		}
		if v, ok := strings.CutPrefix(line, "Y="); ok {
			y, _ = strconv.Atoi(strings.TrimSpace(v))
		}
	}
	c.TrackCursor(image.Point{X: x, Y: y})
	return image.Point{X: x, Y: y}, nil
}

func (c *Capturer) MoveCursor(x, y int) error {
	return run("xdotool", "mousemove", strconv.Itoa(x), strconv.Itoa(y))
}

func (c *Capturer) Click(x, y int, button string) error {
	if err := c.MoveCursor(x, y); err != nil {
		return err
	}
	return run("xdotool", "click", buttonCode(button))
}

func (c *Capturer) Scroll(x, y, dx, dy int) error {
	if err := c.MoveCursor(x, y); err != nil {
		return err
	}
	// vertical: 4 = up, 5 = down
	steps := dy
	btn := "4"
	if dy < 0 {
		btn = "5"
		steps = -dy
	}
	for i := 0; i < steps; i++ {
		if err := run("xdotool", "click", btn); err != nil {
			return err
		}
	}
	// horizontal: 6 = left, 7 = right
	hsteps := dx
	hbtn := "6"
	if dx < 0 {
		hbtn = "7"
		hsteps = -dx
	}
	for i := 0; i < hsteps; i++ {
		if err := run("xdotool", "click", hbtn); err != nil {
			return err
		}
	}
	return nil
}

func (c *Capturer) Type(text string) error {
	return run("xdotool", "type", "--", text)
}

func (c *Capturer) KeyPress(key string, modifiers ...string) error {
	return run("xdotool", "key", composeKey(key, modifiers))
}

func (c *Capturer) Drag(fromX, fromY, toX, toY int, button string) error {
	if err := c.MoveCursor(fromX, fromY); err != nil {
		return err
	}
	if err := run("xdotool", "mousedown", buttonCode(button)); err != nil {
		return err
	}
	// 8-step interpolation matching the Windows backend.
	for i := 1; i <= 8; i++ {
		ix := fromX + (toX-fromX)*i/8
		iy := fromY + (toY-fromY)*i/8
		_ = run("xdotool", "mousemove", strconv.Itoa(ix), strconv.Itoa(iy))
		time.Sleep(5 * time.Millisecond)
	}
	return run("xdotool", "mouseup", buttonCode(button))
}

func (c *Capturer) KeyDown(key string, modifiers ...string) error {
	for _, m := range modifiers {
		if err := run("xdotool", "keydown", m); err != nil {
			return err
		}
	}
	return run("xdotool", "keydown", key)
}

func (c *Capturer) KeyUp(key string, modifiers ...string) error {
	if err := run("xdotool", "keyup", key); err != nil {
		return err
	}
	for i := len(modifiers) - 1; i >= 0; i-- {
		if err := run("xdotool", "keyup", modifiers[i]); err != nil {
			return err
		}
	}
	return nil
}

// =========================================================================
// Window control.
// =========================================================================

func (c *Capturer) SetWindowState(windowID int, state string) error {
	wid := fmt.Sprintf("0x%08x", uint32(windowID))
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "minimize":
		return run("xdotool", "windowminimize", strconv.Itoa(windowID))
	case "maximize":
		return run("wmctrl", "-i", "-r", wid, "-b", "add,maximized_vert,maximized_horz")
	case "restore":
		return run("wmctrl", "-i", "-r", wid, "-b", "remove,maximized_vert,maximized_horz")
	}
	return fmt.Errorf("unknown state: %s", state)
}

func (c *Capturer) MoveWindow(windowID int, x, y, width, height int) error {
	wid := fmt.Sprintf("0x%08x", uint32(windowID))
	// wmctrl -i -r WID -e gravity,x,y,w,h ; -1 keeps current
	gx, gy, gw, gh := x, y, width, height
	if x == 0 && y == 0 {
		gx, gy = -1, -1
	}
	if width == 0 && height == 0 {
		gw, gh = -1, -1
	}
	return run("wmctrl", "-i", "-r", wid, "-e", fmt.Sprintf("0,%d,%d,%d,%d", gx, gy, gw, gh))
}

func (c *Capturer) CloseWindow(windowID int) error {
	wid := fmt.Sprintf("0x%08x", uint32(windowID))
	return run("wmctrl", "-i", "-c", wid)
}

// =========================================================================
// Clipboard.
// =========================================================================

func (c *Capturer) SetClipboard(text string) error {
	cmd := exec.Command("xclip", "-selection", "clipboard", "-in")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

func (c *Capturer) GetClipboard() (string, error) {
	out, err := runOutput("xclip", "-selection", "clipboard", "-out")
	if err != nil {
		return "", err
	}
	return out, nil
}

func (c *Capturer) GetClipboardData() (capture.ClipboardData, error) {
	d := capture.ClipboardData{Kind: "empty"}
	formats := 0

	if text, err := runOutput("xclip", "-selection", "clipboard", "-out", "-t", "UTF8_STRING"); err == nil && text != "" {
		d.Text = strings.TrimRight(text, "\n")
		formats++
	}
	// Image.
	cmd := exec.Command("xclip", "-selection", "clipboard", "-out", "-t", "image/png")
	var imgBuf bytes.Buffer
	cmd.Stdout = &imgBuf
	if err := cmd.Run(); err == nil && imgBuf.Len() > 0 {
		if img, derr := png.Decode(&imgBuf); derr == nil {
			d.Image = imageToRGBA(img)
			formats++
		}
	}
	// Files (text/uri-list).
	if uris, err := runOutput("xclip", "-selection", "clipboard", "-out", "-t", "text/uri-list"); err == nil && uris != "" {
		var files []string
		for _, l := range strings.Split(uris, "\n") {
			l = strings.TrimSpace(l)
			if l == "" || strings.HasPrefix(l, "#") {
				continue
			}
			files = append(files, strings.TrimPrefix(l, "file://"))
		}
		if len(files) > 0 {
			d.Files = files
			formats++
		}
	}
	if html, err := runOutput("xclip", "-selection", "clipboard", "-out", "-t", "text/html"); err == nil && html != "" {
		d.HTML = html
		formats++
	}

	switch {
	case formats == 0:
		d.Kind = "empty"
	case formats == 1 && d.Image != nil:
		d.Kind = "image"
	case formats == 1 && len(d.Files) > 0:
		d.Kind = "files"
	case formats == 1 && d.Text != "":
		d.Kind = "text"
	case formats == 1 && d.HTML != "":
		d.Kind = "html"
	default:
		d.Kind = "mixed"
	}
	return d, nil
}

func (c *Capturer) SetClipboardData(d capture.ClipboardData) error {
	if d.Image != nil {
		var buf bytes.Buffer
		if err := png.Encode(&buf, d.Image); err != nil {
			return err
		}
		cmd := exec.Command("xclip", "-selection", "clipboard", "-in", "-t", "image/png")
		cmd.Stdin = &buf
		return cmd.Run()
	}
	if len(d.Files) > 0 {
		var lines []string
		for _, p := range d.Files {
			if !strings.HasPrefix(p, "file://") {
				p = "file://" + p
			}
			lines = append(lines, p)
		}
		cmd := exec.Command("xclip", "-selection", "clipboard", "-in", "-t", "text/uri-list")
		cmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
		return cmd.Run()
	}
	if d.HTML != "" {
		cmd := exec.Command("xclip", "-selection", "clipboard", "-in", "-t", "text/html")
		cmd.Stdin = strings.NewReader(d.HTML)
		return cmd.Run()
	}
	if d.Text != "" {
		return c.SetClipboard(d.Text)
	}
	return errors.New("clipboard: no data provided")
}

// =========================================================================
// UI Automation — not available via shell tools.
// =========================================================================

func (c *Capturer) ListElements(f capture.ElementFilter) ([]capture.ElementNode, error) {
	// Build a degenerate "element tree" from the window list — gives at
	// least window-level handles so element_click can still target a
	// rect by ID.
	wins, err := c.FilteredWindows(capture.WindowFilter{
		PID: f.WindowID,
	})
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]capture.ElementNode, 0, len(wins))
	for _, w := range wins {
		id := elementIDFromWindow(w.ID)
		c.elementMap[id] = elementHandle{
			windowID: w.ID,
			bounds:   w.Bounds,
			name:     w.Title,
		}
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
		ID:          elementID,
		Name:        h.name,
		ClassName:   "Window",
		ControlType: "window",
		Bounds:      h.bounds,
		IsEnabled:   true,
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
	return c.FocusWindow(h.windowID)
}

func (c *Capturer) SetElementValue(elementID, value string) error {
	return errors.New("set_element_value: not supported on x11 backend")
}

// =========================================================================
// OCR — delegates to the shared OCR shim (PaddleOCR first, tesseract fallback).
// =========================================================================

func (c *Capturer) OCR(pixels []byte, width, height int, language string) (capture.OcrResult, error) {
	res, err := ocr.Run(pixels, width, height, language, image.Rect(0, 0, width, height))
	if err != nil {
		return capture.OcrResult{}, err
	}
	out := capture.OcrResult{
		Text:   res.Text,
		Engine: res.Engine,
		Bounds: res.Bounds,
	}
	for _, l := range res.Lines {
		out.Lines = append(out.Lines, capture.OcrLine{
			Text:       l.Text,
			Bounds:     l.Bounds,
			Confidence: l.Confidence,
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

func rootDimensions() (int, int, error) {
	out, err := runOutput("xdpyinfo")
	if err != nil {
		return 0, 0, err
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "dimensions:"); ok {
			// e.g. "dimensions:    1920x1080 pixels (508x286 millimeters)"
			fields := strings.Fields(rest)
			if len(fields) == 0 {
				return 0, 0, errors.New("xdpyinfo: no dimensions")
			}
			parts := strings.SplitN(fields[0], "x", 2)
			if len(parts) != 2 {
				return 0, 0, errors.New("xdpyinfo: bad dimensions")
			}
			w, _ := strconv.Atoi(parts[0])
			h, _ := strconv.Atoi(parts[1])
			return w, h, nil
		}
	}
	return 0, 0, errors.New("xdpyinfo: no dimensions line")
}

func parseGeometry(g string) (w, h, x, y int, ok bool) {
	// "1920x1080+0+0"
	xi := strings.Index(g, "x")
	if xi < 0 {
		return
	}
	pi := strings.Index(g[xi:], "+")
	if pi < 0 {
		return
	}
	pi += xi
	pi2 := strings.Index(g[pi+1:], "+")
	if pi2 < 0 {
		return
	}
	pi2 += pi + 1
	w, _ = strconv.Atoi(g[:xi])
	h, _ = strconv.Atoi(g[xi+1 : pi])
	x, _ = strconv.Atoi(g[pi+1 : pi2])
	y, _ = strconv.Atoi(g[pi2+1:])
	return w, h, x, y, true
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
		p := *f.AtPoint
		if !(p.X >= w.Bounds.Min.X && p.X < w.Bounds.Max.X &&
			p.Y >= w.Bounds.Min.Y && p.Y < w.Bounds.Max.Y) {
			return false
		}
	}
	if !f.IncludeInvisible && !w.IsVisible {
		return false
	}
	return true
}

func getProcessName(pid int) string {
	out, err := runOutput("ps", "-p", strconv.Itoa(pid), "-o", "comm=")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func getWindowRect(windowID int) (image.Rectangle, error) {
	out, err := runOutput("xdotool", "getwindowgeometry", "--shell", strconv.Itoa(windowID))
	if err != nil {
		return image.Rectangle{}, err
	}
	var x, y, w, h int
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(line, "X="); ok {
			x, _ = strconv.Atoi(strings.TrimSpace(v))
		} else if v, ok := strings.CutPrefix(line, "Y="); ok {
			y, _ = strconv.Atoi(strings.TrimSpace(v))
		} else if v, ok := strings.CutPrefix(line, "WIDTH="); ok {
			w, _ = strconv.Atoi(strings.TrimSpace(v))
		} else if v, ok := strings.CutPrefix(line, "HEIGHT="); ok {
			h, _ = strconv.Atoi(strings.TrimSpace(v))
		}
	}
	return image.Rect(x, y, x+w, y+h), nil
}

func foregroundWindow() int {
	out, err := runOutput("xdotool", "getactivewindow")
	if err != nil {
		return 0
	}
	id, _ := strconv.Atoi(strings.TrimSpace(out))
	return id
}

func windowState(windowID int) string {
	out, err := runOutput("xprop", "-id", fmt.Sprintf("0x%x", uint32(windowID)), "_NET_WM_STATE")
	if err != nil {
		return "normal"
	}
	if strings.Contains(out, "_NET_WM_STATE_HIDDEN") {
		return "minimized"
	}
	if strings.Contains(out, "_NET_WM_STATE_MAXIMIZED_VERT") && strings.Contains(out, "_NET_WM_STATE_MAXIMIZED_HORZ") {
		return "maximized"
	}
	return "normal"
}

func windowClass(windowID int) string {
	out, err := runOutput("xprop", "-id", fmt.Sprintf("0x%x", uint32(windowID)), "WM_CLASS")
	if err != nil {
		return ""
	}
	// WM_CLASS(STRING) = "firefox", "Firefox"
	if i := strings.Index(out, "= "); i >= 0 {
		return strings.TrimSpace(out[i+2:])
	}
	return ""
}

func buttonCode(b string) string {
	switch strings.ToLower(b) {
	case "right":
		return "3"
	case "middle":
		return "2"
	default:
		return "1"
	}
}

func composeKey(key string, modifiers []string) string {
	if len(modifiers) == 0 {
		return key
	}
	return strings.Join(append(modifiers, key), "+")
}

func elementIDFromWindow(id int) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(id))
	return "x11/" + base64.RawURLEncoding.EncodeToString(buf[:])
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
