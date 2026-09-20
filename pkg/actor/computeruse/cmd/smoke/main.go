// Command smoke exercises the computeruse actor's underlying platform
// capturer end-to-end against the host display, mouse, and clipboard.
// Intended for manual smoke-testing of new platform backends — CI cannot
// run this because most build agents have no display.
//
// Subcommands:
//
//	screenshot   capture one frame and write it to disk
//	stream       capture continuously for N seconds, count frames
//	ocr          run OCR over a captured region
//	clipboard    roundtrip text/HTML through the platform clipboard
//	windows      list visible windows
//	displays     list connected displays
//	all          run every check in sequence and print pass/fail
//
// Examples:
//
//	go run ./pkg/actor/computeruse/cmd/smoke screenshot -out shot.png
//	go run ./pkg/actor/computeruse/cmd/smoke stream -seconds 3 -fps 5
//	go run ./pkg/actor/computeruse/cmd/smoke all
package main

import (
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"strings"
	"time"

	"github.com/qomos-w/sporemind/pkg/actor/computeruse/internal/capture"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	sub := os.Args[1]
	args := os.Args[2:]
	var err error
	switch sub {
	case "screenshot":
		err = runScreenshot(args)
	case "stream":
		err = runStream(args)
	case "ocr":
		err = runOCR(args)
	case "clipboard":
		err = runClipboard(args)
	case "windows":
		err = runWindows(args)
	case "displays":
		err = runDisplays(args)
	case "all":
		err = runAll(args)
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", sub)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `computeruse-smoke - end-to-end checks for the computeruse platform backend.

USAGE:
  smoke <subcommand> [flags]

SUBCOMMANDS:
  screenshot   capture one frame to disk (-out, -mode, -region, -window-title)
  stream       capture continuously, count frames (-seconds, -fps)
  ocr          run OCR over a captured region (-region, -lang)
  clipboard    roundtrip clipboard formats
  windows      list visible windows (-limit)
  displays     list connected displays
  all          run all smoke checks
`)
}

func newCapturer() (capture.Capturer, error) {
	c, err := capture.New()
	if err != nil {
		return nil, fmt.Errorf("init capturer: %w", err)
	}
	return c, nil
}

// --- screenshot ---

func runScreenshot(args []string) error {
	fs := flag.NewFlagSet("screenshot", flag.ExitOnError)
	out := fs.String("out", "smoke-screenshot.png", "output PNG path")
	mode := fs.String("mode", "display", "display | window | region")
	display := fs.Int("display", 0, "display index (mode=display)")
	regionStr := fs.String("region", "", "x,y,w,h (mode=region)")
	windowTitle := fs.String("window-title", "", "match windowTitle substring (mode=window)")
	_ = fs.Parse(args)

	c, err := newCapturer()
	if err != nil {
		return err
	}
	defer c.Close()

	opts := capture.CaptureOptions{
		Mode:         capture.ModeFull,
		DisplayID:    *display,
		EncodeFormat: "png",
		Quality:      90,
	}

	var frame *capture.Frame
	switch *mode {
	case "display":
		opts.Mode = capture.ModeFull
		frame, err = c.Capture(opts)
	case "region":
		if *regionStr == "" {
			return fmt.Errorf("-region required for mode=region (format: x,y,w,h)")
		}
		var x, y, w, h int
		if _, err := fmt.Sscanf(*regionStr, "%d,%d,%d,%d", &x, &y, &w, &h); err != nil {
			return fmt.Errorf("invalid -region %q: %w", *regionStr, err)
		}
		opts.Mode = capture.ModeRegion
		opts.Region = image.Rect(x, y, x+w, y+h)
		frame, err = c.Capture(opts)
	case "window":
		hwnd, ferr := findWindow(c, *windowTitle)
		if ferr != nil {
			return ferr
		}
		frame, err = c.CaptureWindow(hwnd, opts)
	default:
		return fmt.Errorf("unknown -mode: %s", *mode)
	}
	if err != nil {
		return fmt.Errorf("capture: %w", err)
	}

	if err := writePNG(*out, frame); err != nil {
		return fmt.Errorf("write %s: %w", *out, err)
	}
	fmt.Printf("ok: wrote %s (%dx%d, %d bytes)\n",
		*out, frame.Bounds.Dx(), frame.Bounds.Dy(), len(frame.Data))
	return nil
}

func writePNG(path string, frame *capture.Frame) error {
	if frame.EncodingFormat == "png" && len(frame.Data) > 8 &&
		frame.Data[0] == 0x89 && frame.Data[1] == 'P' {
		return os.WriteFile(path, frame.Data, 0644)
	}
	w := frame.Bounds.Dx()
	h := frame.Bounds.Dy()
	img := &image.RGBA{
		Pix:    frame.RawPixels,
		Stride: w * 4,
		Rect:   image.Rect(0, 0, w, h),
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

// --- stream ---

func runStream(args []string) error {
	fs := flag.NewFlagSet("stream", flag.ExitOnError)
	seconds := fs.Int("seconds", 3, "capture duration in seconds")
	fps := fs.Int("fps", 5, "frames per second")
	display := fs.Int("display", 0, "display index")
	_ = fs.Parse(args)

	c, err := newCapturer()
	if err != nil {
		return err
	}
	defer c.Close()

	frameInterval := time.Second / time.Duration(*fps)
	deadline := time.Now().Add(time.Duration(*seconds) * time.Second)
	ticker := time.NewTicker(frameInterval)
	defer ticker.Stop()

	count := 0
	totalBytes := 0
	start := time.Now()
	for now := range ticker.C {
		if now.After(deadline) {
			break
		}
		frame, err := c.Capture(capture.CaptureOptions{
			Mode:         capture.ModeFull,
			DisplayID:    *display,
			EncodeFormat: "raw",
		})
		if err != nil {
			return fmt.Errorf("frame %d: %w", count, err)
		}
		count++
		totalBytes += len(frame.RawPixels)
	}
	elapsed := time.Since(start).Seconds()
	fmt.Printf("ok: captured %d frames in %.2fs (%.1f fps, %.1f MB/s)\n",
		count, elapsed, float64(count)/elapsed,
		float64(totalBytes)/elapsed/1024/1024)
	if count == 0 {
		return fmt.Errorf("zero frames captured — capturer broken?")
	}
	return nil
}

// --- ocr ---

func runOCR(args []string) error {
	fs := flag.NewFlagSet("ocr", flag.ExitOnError)
	regionStr := fs.String("region", "0,0,800,200", "region to OCR: x,y,w,h")
	language := fs.String("lang", "eng", "OCR language")
	_ = fs.Parse(args)

	c, err := newCapturer()
	if err != nil {
		return err
	}
	defer c.Close()

	var x, y, w, h int
	if _, err := fmt.Sscanf(*regionStr, "%d,%d,%d,%d", &x, &y, &w, &h); err != nil {
		return fmt.Errorf("invalid -region: %w", err)
	}
	frame, err := c.Capture(capture.CaptureOptions{
		Mode:         capture.ModeRegion,
		Region:       image.Rect(x, y, x+w, y+h),
		EncodeFormat: "raw",
	})
	if err != nil {
		return fmt.Errorf("capture: %w", err)
	}

	result, err := c.OCR(frame.RawPixels, w, h, *language)
	if err != nil {
		return fmt.Errorf("OCR: %w (this is expected if no OCR engine is installed)", err)
	}
	fmt.Printf("ok: engine=%s lines=%d\n", result.Engine, len(result.Lines))
	fmt.Printf("  text: %s\n", strings.ReplaceAll(result.Text, "\n", "\\n"))
	for i, l := range result.Lines {
		if i >= 5 {
			fmt.Printf("  ... (%d more lines)\n", len(result.Lines)-5)
			break
		}
		fmt.Printf("  line[%d] @%v conf=%.2f: %q\n", i, l.Bounds, l.Confidence, l.Text)
	}
	return nil
}

// --- clipboard ---

func runClipboard(args []string) error {
	fs := flag.NewFlagSet("clipboard", flag.ExitOnError)
	_ = fs.Parse(args)

	c, err := newCapturer()
	if err != nil {
		return err
	}
	defer c.Close()

	originalText, _ := c.GetClipboard()
	defer func() { _ = c.SetClipboard(originalText) }()

	want := fmt.Sprintf("computeruse-smoke-%d", time.Now().Unix())
	if err := c.SetClipboard(want); err != nil {
		return fmt.Errorf("SetClipboard: %w", err)
	}
	got, err := c.GetClipboard()
	if err != nil {
		return fmt.Errorf("GetClipboard: %w", err)
	}
	if got != want {
		return fmt.Errorf("text roundtrip: want %q got %q", want, got)
	}
	fmt.Printf("ok: text roundtrip (%d chars)\n", len(want))

	d := capture.ClipboardData{HTML: "<p>computeruse-smoke <b>hello</b></p>"}
	if err := c.SetClipboardData(d); err != nil {
		return fmt.Errorf("SetClipboardData(html): %w", err)
	}
	back, err := c.GetClipboardData()
	if err != nil {
		return fmt.Errorf("GetClipboardData: %w", err)
	}
	if !strings.Contains(back.HTML, "hello") {
		return fmt.Errorf("html roundtrip: got %q", back.HTML)
	}
	fmt.Printf("ok: html roundtrip (kind=%s, %d bytes)\n", back.Kind, len(back.HTML))
	return nil
}

// --- windows / displays ---

func runWindows(args []string) error {
	fs := flag.NewFlagSet("windows", flag.ExitOnError)
	limit := fs.Int("limit", 20, "max windows to print")
	_ = fs.Parse(args)

	c, err := newCapturer()
	if err != nil {
		return err
	}
	defer c.Close()

	wins, err := c.Windows()
	if err != nil {
		return fmt.Errorf("Windows: %w", err)
	}
	fmt.Printf("ok: %d windows\n", len(wins))
	for i, w := range wins {
		if i >= *limit {
			fmt.Printf("  ... (%d more)\n", len(wins)-*limit)
			break
		}
		fmt.Printf("  #%d hwnd=%d pid=%d %s\n  bounds=%v %q\n",
			i, w.ID, w.PID, w.ProcessName, w.Bounds, w.Title)
	}
	return nil
}

func runDisplays(args []string) error {
	c, err := newCapturer()
	if err != nil {
		return err
	}
	defer c.Close()
	ds, err := c.Displays()
	if err != nil {
		return fmt.Errorf("Displays: %w", err)
	}
	fmt.Printf("ok: %d displays\n", len(ds))
	for _, d := range ds {
		fmt.Printf("  %d %s primary=%v %v\n", d.ID, d.Name, d.IsPrimary, d.Bounds)
	}
	return nil
}

// --- all ---

func runAll(_ []string) error {
	checks := []struct {
		name string
		fn   func() error
	}{
		{"displays", func() error { return runDisplays(nil) }},
		{"windows", func() error { return runWindows([]string{"-limit", "5"}) }},
		{"screenshot", func() error {
			tmp, err := os.CreateTemp("", "smoke-*.png")
			if err != nil {
				return err
			}
			tmp.Close()
			defer os.Remove(tmp.Name())
			return runScreenshot([]string{"-out", tmp.Name()})
		}},
		{"stream", func() error { return runStream([]string{"-seconds", "1", "-fps", "5"}) }},
		{"clipboard", func() error { return runClipboard(nil) }},
	}
	pass, fail := 0, 0
	for _, c := range checks {
		fmt.Printf("\n=== %s ===\n", c.name)
		if err := c.fn(); err != nil {
			fmt.Printf("FAIL %s: %v\n", c.name, err)
			fail++
		} else {
			pass++
		}
	}
	fmt.Printf("\n=== summary ===\npass=%d fail=%d\n", pass, fail)
	if fail > 0 {
		return fmt.Errorf("%d smoke check(s) failed", fail)
	}
	return nil
}

// --- helpers ---

func findWindow(c capture.Capturer, titleNeedle string) (int, error) {
	wins, err := c.Windows()
	if err != nil {
		return 0, err
	}
	if titleNeedle == "" {
		for _, w := range wins {
			if w.IsVisible && w.Title != "" {
				return w.ID, nil
			}
		}
		return 0, fmt.Errorf("no visible window found")
	}
	needle := strings.ToLower(titleNeedle)
	for _, w := range wins {
		if strings.Contains(strings.ToLower(w.Title), needle) {
			return w.ID, nil
		}
	}
	return 0, fmt.Errorf("no window matching %q", titleNeedle)
}
