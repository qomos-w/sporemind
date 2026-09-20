package imagegen

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGenerateMiniMax(t *testing.T) {
	imageBytes := []byte{0x89, 0x50, 0x4e, 0x47}
	var gotReq minimaxImageRequest
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/image_generation":
			gotPath = r.URL.Path
			gotAuth = r.Header.Get("Authorization")
			if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
				t.Errorf("decode body: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data":       map[string]any{"image_urls": []string{"http://" + r.Host + "/img.jpg"}},
				"metadata":   map[string]any{"resolution": "1K"},
				"base_resp":  map[string]any{"status_code": 0, "status_msg": ""},
			})
		case "/img.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(imageBytes)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	result, err := Generate(context.Background(), Params{
		Protocol:   "minimax",
		Endpoint:   srv.URL, // no /v1 suffix: must be appended
		AuthToken:  "k1",
		Prompt:     "a cat",
		Model:      "image-01",
		Size:       "1K",
		AspectRatio: "16:9",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if string(result.Data) != string(imageBytes) {
		t.Fatalf("data = %v", result.Data)
	}
	if result.MimeType != "image/jpeg" {
		t.Fatalf("mime = %q", result.MimeType)
	}
	if gotPath != "/v1/image_generation" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer k1" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if gotReq.Model != "image-01" || gotReq.Prompt != "a cat" {
		t.Fatalf("req = %+v", gotReq)
	}
	if gotReq.AspectRatio != "16:9" {
		t.Fatalf("aspect_ratio = %q", gotReq.AspectRatio)
	}
	if gotReq.Width != 0 || gotReq.Height != 0 {
		t.Fatal("width/height must be omitted when aspect ratio is set")
	}
	if gotReq.ResponseFormat != "url" {
		t.Fatalf("response_format = %q", gotReq.ResponseFormat)
	}
}

func TestGenerateMiniMaxSizeAndRefs(t *testing.T) {
	var gotReq minimaxImageRequest
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/image_generation":
			if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
				t.Errorf("decode body: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data":      map[string]any{"image_urls": []string{srvURL + "/x.jpg"}},
				"base_resp": map[string]any{"status_code": 0},
			})
		case "/x.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte{1})
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	srvURL = srv.URL
	defer srv.Close()

	_, err := Generate(context.Background(), Params{
		Protocol:        "minimax",
		Endpoint:        srv.URL + "/v1/",
		AuthToken:       "k",
		Prompt:          "same cat",
		Size:            "2K",
		ReferenceImages: []string{"data:image/png;base64,QUJD", "https://r.example.com/a.png"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if gotReq.Width != 2048 || gotReq.Height != 2048 {
		t.Fatalf("size = %dx%d", gotReq.Width, gotReq.Height)
	}
	if len(gotReq.SubjectReference) != 2 {
		t.Fatalf("subject_reference = %+v", gotReq.SubjectReference)
	}
	if gotReq.SubjectReference[0].ImageFile != "QUJD" || gotReq.SubjectReference[0].Type != "character" {
		t.Fatalf("ref0 = %+v", gotReq.SubjectReference[0])
	}
	if gotReq.SubjectReference[1].ImageFile != "https://r.example.com/a.png" {
		t.Fatalf("ref1 = %+v", gotReq.SubjectReference[1])
	}
}

func TestGenerateMiniMaxDefaultModel(t *testing.T) {
	var gotReq minimaxImageRequest
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/image_generation":
			_ = json.NewDecoder(r.Body).Decode(&gotReq)
			_, _ = w.Write([]byte(`{"data":{"image_urls":["` + srvURL + `/i.jpg"]}}`))
		case "/i.jpg":
			_, _ = w.Write([]byte{1})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	srvURL = srv.URL
	defer srv.Close()

	_, err := Generate(context.Background(), Params{Protocol: "minimax", Endpoint: srv.URL, AuthToken: "k", Prompt: "p"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if gotReq.Model != minimaxDefaultImageModel {
		t.Fatalf("model = %q", gotReq.Model)
	}
}

func TestGenerateMiniMaxAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"base_resp":{"status_code":1004,"status_msg":"invalid api key"}}`))
	}))
	defer srv.Close()

	_, err := Generate(context.Background(), Params{Protocol: "minimax", Endpoint: srv.URL, AuthToken: "bad", Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "1004") {
		t.Fatalf("err = %v", err)
	}
}

func TestGenerateMiniMaxNoURLs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"image_urls":[]}}`))
	}))
	defer srv.Close()

	_, err := Generate(context.Background(), Params{Protocol: "minimax", Endpoint: srv.URL, AuthToken: "k", Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "no image urls") {
		t.Fatalf("err = %v", err)
	}
}
