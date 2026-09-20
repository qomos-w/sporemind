package videogen

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newLocalhostServer starts an httptest server bound to the loopback address
// and returns its URL typed with the "localhost" hostname. The hostname string
// differs from a default httptest server ("127.0.0.1"), letting tests verify
// that the Bearer token is withheld on cross-host downloads.
func newLocalhostServer(t *testing.T, h http.Handler) (srv *httptest.Server, url string) {
	t.Helper()
	srv = httptest.NewUnstartedServer(h)
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return srv, "http://localhost:" + port
}

// arkSubmitted mirrors the Ark submit body so tests can assert field names and
// content ordering/roles.
type arkSubmitted struct {
	Model         string `json:"model"`
	GenerateAudio bool   `json:"generate_audio"`
	Ratio         string `json:"ratio"`
	Duration      int    `json:"duration"`
	Watermark     bool   `json:"watermark"`
	Content       []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL struct{ URL string } `json:"image_url"`
		VideoURL struct{ URL string } `json:"video_url"`
		AudioURL struct{ URL string } `json:"audio_url"`
		Role     string `json:"role"`
	} `json:"content"`
}

func TestGenerateArk_MultiReferenceContent(t *testing.T) {
	setFastPoll(t)
	polls := 0
	var submitPath, submitAuth, downloadAuth, rawSubmitBody string
	var submitted arkSubmitted

	_, dlURL := newLocalhostServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloadAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("mp4-bytes"))
	}))

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/contents/generations/tasks":
			submitPath = r.URL.Path
			submitAuth = r.Header.Get("Authorization")
			raw, _ := io.ReadAll(r.Body)
			rawSubmitBody = string(raw)
			if err := json.Unmarshal(raw, &submitted); err != nil {
				t.Errorf("decode submit body: %v", err)
			}
			_, _ = w.Write([]byte(`{"id":"task-1"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/contents/generations/tasks/task-1":
			polls++
			if polls < 2 {
				_, _ = w.Write([]byte(`{"id":"task-1","status":"running"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"task-1","status":"succeeded","content":{"video_url":"` + dlURL + `/v.mp4"}}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer api.Close()

	result, err := GenerateArk(context.Background(), Params{
		Prompt:          "make the dancer follow the beat",
		Model:           "dreamina-seedance-2-0-260128",
		Endpoint:        api.URL,
		AuthToken:       "ark-key",
		AspectRatio:     "16:9",
		Duration:        11,
		GenerateAudio:   true,
		Watermark:       false,
		ReferenceImages: []string{"https://example.com/dancer.png", "data:image/png;base64,AAAA"},
		ReferenceVideos: []string{"https://example.com/motion.mp4"},
		ReferenceAudios: []string{"https://example.com/track.wav"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if submitPath != "/contents/generations/tasks" {
		t.Fatalf("submit path = %q, want /contents/generations/tasks", submitPath)
	}
	if submitAuth != "Bearer ark-key" {
		t.Fatalf("submit auth = %q, want Bearer ark-key", submitAuth)
	}
	if downloadAuth != "" {
		t.Fatalf("cross-host download auth = %q, want empty (presigned CDN must not receive Authorization)", downloadAuth)
	}
	if submitted.Model != "dreamina-seedance-2-0-260128" {
		t.Errorf("model = %q", submitted.Model)
	}
	if !submitted.GenerateAudio {
		t.Errorf("generate_audio = false, want true")
	}
	if submitted.Ratio != "16:9" {
		t.Errorf("ratio = %q, want 16:9", submitted.Ratio)
	}
	if submitted.Duration != 11 {
		t.Errorf("duration = %d, want 11", submitted.Duration)
	}
	if submitted.Watermark {
		t.Errorf("watermark = true, want false")
	}
	// The sample API body always carries these scalars; assert the raw JSON so a
	// missing (omitempty) field cannot masquerade as an explicit false/zero.
	for _, want := range []string{
		`"generate_audio":true`,
		`"ratio":"16:9"`,
		`"duration":11`,
		`"watermark":false`,
	} {
		if !strings.Contains(rawSubmitBody, want) {
			t.Errorf("submit body missing %s, got: %s", want, rawSubmitBody)
		}
	}

	// Content must be ordered text → images → videos → audios, with each
	// reference carrying its dedicated role; two images must produce two
	// independent image_url entries.
	if len(submitted.Content) != 5 {
		t.Fatalf("content len = %d, want 5: %+v", len(submitted.Content), submitted.Content)
	}
	if c := submitted.Content[0]; c.Type != "text" || c.Text != "make the dancer follow the beat" || c.Role != "" {
		t.Errorf("content[0] = %+v, want text prompt without role", c)
	}
	if c := submitted.Content[1]; c.Type != "image_url" || c.Role != "reference_image" || c.ImageURL.URL != "https://example.com/dancer.png" {
		t.Errorf("content[1] = %+v, want first reference_image", c)
	}
	if c := submitted.Content[2]; c.Type != "image_url" || c.Role != "reference_image" || c.ImageURL.URL != "data:image/png;base64,AAAA" {
		t.Errorf("content[2] = %+v, want second reference_image (data URL passed through)", c)
	}
	if c := submitted.Content[3]; c.Type != "video_url" || c.Role != "reference_video" || c.VideoURL.URL != "https://example.com/motion.mp4" {
		t.Errorf("content[3] = %+v, want reference_video", c)
	}
	if c := submitted.Content[4]; c.Type != "audio_url" || c.Role != "reference_audio" || c.AudioURL.URL != "https://example.com/track.wav" {
		t.Errorf("content[4] = %+v, want reference_audio", c)
	}

	if polls != 2 {
		t.Fatalf("polls = %d, want 2", polls)
	}
	if string(result.Data) != "mp4-bytes" {
		t.Fatalf("data = %q, want mp4-bytes", result.Data)
	}
	if result.MimeType != "video/mp4" {
		t.Fatalf("mime = %q, want video/mp4", result.MimeType)
	}
	if result.URL != dlURL+"/v.mp4" {
		t.Fatalf("url = %q, want %q", result.URL, dlURL+"/v.mp4")
	}
}

func TestGenerateArk_SameHostDownloadSendsBearer(t *testing.T) {
	setFastPoll(t)
	var downloadAuth string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/contents/generations/tasks":
			_, _ = w.Write([]byte(`{"id":"task-2"}`))
		case "/contents/generations/tasks/task-2":
			_, _ = w.Write([]byte(`{"id":"task-2","status":"succeeded","content":{"video_url":"http://` + r.Host + `/file/v.mp4"}}`))
		case "/file/v.mp4":
			downloadAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte("same-host-bytes"))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer api.Close()

	result, err := GenerateArk(context.Background(), Params{
		Prompt:    "a wave",
		Model:     "seedance-1",
		Endpoint:  api.URL,
		AuthToken: "ark-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if downloadAuth != "Bearer ark-key" {
		t.Fatalf("same-host download auth = %q, want Bearer ark-key", downloadAuth)
	}
	if string(result.Data) != "same-host-bytes" {
		t.Fatalf("data = %q, want same-host-bytes", result.Data)
	}
}

func TestGenerateArk_FailedStatusTruncatesError(t *testing.T) {
	setFastPoll(t)
	longMsg := strings.Repeat("a", 512) + strings.Repeat("b", 88) // 600 bytes total
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/contents/generations/tasks":
			_, _ = w.Write([]byte(`{"id":"task-fail"}`))
		case "/contents/generations/tasks/task-fail":
			_, _ = w.Write([]byte(`{"id":"task-fail","status":"failed","error":{"code":"E123","message":"` + longMsg + `"}}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer api.Close()

	_, err := GenerateArk(context.Background(), Params{
		Prompt:    "x",
		Model:     "seedance-1",
		Endpoint:  api.URL,
		AuthToken: "ark-key",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	want := longMsg[:512] + "..."
	if !strings.Contains(err.Error(), "task task-fail failed") {
		t.Errorf("error should mention the failed task, got: %v", err)
	}
	if !strings.Contains(err.Error(), "[E123]") {
		t.Errorf("error should include the provider error code, got: %v", err)
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error should include the truncated error.message %q, got: %v", want, err)
	}
	if strings.Contains(err.Error(), strings.Repeat("b", 88)) {
		t.Errorf("error must not include the untruncated tail of error.message, got: %v", err)
	}
}

func TestGenerateArk_CtxCancellation(t *testing.T) {
	// Use a poll interval longer than the test's runtime so that after the
	// context is cancelled during the first poll, the next loop iteration
	// deterministically selects the ctx.Done() branch instead of the timer.
	prev := pollInterval
	pollInterval = 300 * time.Millisecond
	t.Cleanup(func() { pollInterval = prev })

	ctx, cancel := context.WithCancel(context.Background())
	polls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/contents/generations/tasks":
			_, _ = w.Write([]byte(`{"id":"task-cancel"}`))
		case "/contents/generations/tasks/task-cancel":
			polls++
			cancel()
			_, _ = w.Write([]byte(`{"id":"task-cancel","status":"running"}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer api.Close()

	_, err := GenerateArk(ctx, Params{
		Prompt:    "x",
		Model:     "seedance-1",
		Endpoint:  api.URL,
		AuthToken: "ark-key",
	})
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("expected context canceled error, got %v", err)
	}
	if polls != 1 {
		t.Fatalf("polls = %d, want 1 (no polling after cancellation)", polls)
	}
}

func TestGenerateArk_Validation(t *testing.T) {
	if _, err := GenerateArk(context.Background(), Params{Prompt: "x"}); err == nil {
		t.Fatal("expected model-required error")
	}
	if _, err := GenerateArk(context.Background(), Params{Model: "seedance-1"}); err == nil {
		t.Fatal("expected prompt-or-reference-required error")
	}
}