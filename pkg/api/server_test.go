package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xibodev/gflow-cli/pkg/client"
	"github.com/xibodev/gflow-cli/pkg/config"
	"github.com/xibodev/gflow-cli/pkg/models"
)

type fakeBridge struct{ connected, hasKey bool }

func (f *fakeBridge) IsConnected() bool       { return f.connected }
func (f *fakeBridge) HasFlowKey() bool        { return f.hasKey }
func (f *fakeBridge) ActiveSessionCount() int { return 1 }

type fakeService struct {
	lastAspect string
	lastRefs   []string
	lastSeed   *int64
	poll       []client.VideoMediaState
	pollErr    error
	assets     []models.Asset
	upIDs      []string
	upErr      error
	uploadID   string
}

func (f *fakeService) GenerateImages(ctx context.Context, prompt, aspect string, count int, model string, refs []string, seed *int64) ([]models.Asset, error) {
	f.lastAspect = aspect
	f.lastRefs = append([]string(nil), refs...)
	f.lastSeed = seed
	return []models.Asset{{ID: "m1", Type: "image", URL: "https://example.test/i.png", MimeType: "image/png"}}, nil
}
func (f *fakeService) GenerateVideo(ctx context.Context, prompt, aspect string, duration int, model, start, end string, seed *int64) ([]string, error) {
	return []string{"job1"}, nil
}
func (f *fakeService) CheckVideoStatus(ctx context.Context, ids []string) ([]client.VideoMediaState, error) {
	if f.pollErr != nil {
		return nil, f.pollErr
	}
	return f.poll, nil
}
func (f *fakeService) ResolveVideoAssets(ctx context.Context, ids []string) ([]models.Asset, error) {
	return f.assets, nil
}
func (f *fakeService) UpsampleVideo(ctx context.Context, mediaID, aspect, resolution string, seed *int64) ([]string, error) {
	if f.upErr != nil {
		return nil, f.upErr
	}
	return f.upIDs, nil
}
func (f *fakeService) UploadImageBytes(ctx context.Context, data []byte, filename string) (string, error) {
	return f.uploadID, nil
}
func (f *fakeService) GetCredits(ctx context.Context) (any, error) { return map[string]any{}, nil }

func testServer(f *fakeService) *Server {
	cfg := &config.Config{Host: "127.0.0.1", Port: 1, APIToken: "api-test-token", BridgeToken: "bridge-test-token"}
	return NewServerWithService(f, &fakeBridge{connected: true, hasKey: true}, cfg)
}

func doReq(t *testing.T, h http.Handler, method, path string, body any, token, origin string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestHealthMinimalUnauthenticated(t *testing.T) {
	s := testServer(&fakeService{})
	w := doReq(t, s.Handler(), "GET", "/health", nil, "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("health: %d", w.Code)
	}
	var h map[string]any
	_ = json.NewDecoder(w.Body).Decode(&h)
	if h["product"] != "gflow" || h["status"] != "ok" {
		t.Fatalf("minimal health wrong: %v", h)
	}
	if _, ok := h["extension_connected"]; ok {
		t.Fatalf("minimal probe must not leak session state")
	}
}

func TestAPIRequiresAuthAndRejectsOrigin(t *testing.T) {
	s := testServer(&fakeService{})
	h := s.Handler()
	if w := doReq(t, h, "POST", "/v1/images/generations", map[string]any{"prompt": "p"}, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
	if w := doReq(t, h, "POST", "/v1/images/generations", map[string]any{"prompt": "p"}, "api-test-token", "https://evil.test"); w.Code != http.StatusForbidden {
		t.Fatalf("want 403 origin, got %d", w.Code)
	}
}

func TestImageAspectRefSeedPassthrough(t *testing.T) {
	f := &fakeService{}
	s := testServer(f)
	seed := int64(0)
	w := doReq(t, s.Handler(), "POST", "/v1/images/generations", map[string]any{
		"prompt": "p", "n": 1, "aspect": "square", "reference_media_ids": []string{"r1"}, "seed": seed,
	}, "api-test-token", "")
	if w.Code != http.StatusOK {
		t.Fatalf("image: %d %s", w.Code, w.Body.String())
	}
	if f.lastAspect != "square" {
		t.Fatalf("aspect not preserved: %q", f.lastAspect)
	}
	if len(f.lastRefs) != 1 || f.lastRefs[0] != "r1" {
		t.Fatalf("refs not preserved: %v", f.lastRefs)
	}
	if f.lastSeed == nil || *f.lastSeed != 0 {
		t.Fatalf("explicit zero seed must be preserved")
	}
}

func TestImageRejectsUnknownAspect(t *testing.T) {
	s := testServer(&fakeService{})
	w := doReq(t, s.Handler(), "POST", "/v1/images/generations", map[string]any{"prompt": "p", "aspect": "panorama"}, "api-test-token", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestVideoSubmitRejectsUpsampleResolution(t *testing.T) {
	s := testServer(&fakeService{})
	w := doReq(t, s.Handler(), "POST", "/v1/videos/generations", map[string]any{"prompt": "p", "duration": 4, "resolution": "1080p"}, "api-test-token", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d %s", w.Code, w.Body.String())
	}
}

func TestVideoPollMapsStates(t *testing.T) {
	f := &fakeService{poll: []client.VideoMediaState{{ID: "job1", Status: "processing"}}}
	s := testServer(f)
	w := doReq(t, s.Handler(), "GET", "/v1/videos/generations/job1", nil, "api-test-token", "")
	var st map[string]any
	_ = json.NewDecoder(w.Body).Decode(&st)
	if st["status"] != "processing" {
		t.Fatalf("want processing, got %v", st)
	}

	f.poll = []client.VideoMediaState{{ID: "job1", Status: "failed", Reason: "blocked"}}
	w = doReq(t, s.Handler(), "GET", "/v1/videos/generations/job1", nil, "api-test-token", "")
	_ = json.NewDecoder(w.Body).Decode(&st)
	if st["status"] != "failed" {
		t.Fatalf("want failed, got %v", st)
	}

	f.poll = []client.VideoMediaState{{ID: "job1", Status: "succeeded"}}
	f.assets = []models.Asset{{ID: "job1", Type: "video", URL: "https://example.test/v.mp4"}}
	w = doReq(t, s.Handler(), "GET", "/v1/videos/generations/job1", nil, "api-test-token", "")
	_ = json.NewDecoder(w.Body).Decode(&st)
	if st["status"] != "succeeded" {
		t.Fatalf("want succeeded, got %v", st)
	}
}

func TestUpsampleEmptyIDsNoPanic(t *testing.T) {
	f := &fakeService{upIDs: []string{}}
	s := testServer(f)
	w := doReq(t, s.Handler(), "POST", "/v1/videos/upsample", map[string]any{"media_id": "m", "resolution": "1080p"}, "api-test-token", "")
	if w.Code != http.StatusBadGateway {
		t.Fatalf("want 502, got %d", w.Code)
	}
}

func TestUploadRejectsLocalPath(t *testing.T) {
	s := testServer(&fakeService{uploadID: "mid"})
	sentinel := t.TempDir() + "/sentinel.png"
	_ = sentinel
	w := doReq(t, s.Handler(), "POST", "/v1/upload", map[string]any{"file_path": sentinel}, "api-test-token", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("JSON path upload must be rejected, got %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "sentinel") {
		t.Fatalf("error must not echo local path contents")
	}
}
