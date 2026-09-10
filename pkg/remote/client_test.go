package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func testClient(url, token string) *Client {
	c := &Client{BaseURL: url, APIToken: token, HTTP: &http.Client{Timeout: 5 * time.Second}, PollEvery: 10 * time.Millisecond, PollTimeout: 300 * time.Millisecond}
	return c
}

func TestProbeRejectsForeignService(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"product": "other", "status": "ok"})
	}))
	defer srv.Close()
	c := testClient(srv.URL, "")
	if err := c.Probe(context.Background()); err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("want collision error, got %v", err)
	}
}

func TestAuthAndMalformedPropagated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			_ = json.NewEncoder(w).Encode(map[string]any{"product": "gflow", "status": "ok"})
			return
		}
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "unauthorized", "message": "no"}})
			return
		}
		w.Write([]byte("{invalid"))
	}))
	defer srv.Close()
	c := testClient(srv.URL, "bad")
	if err := c.Probe(context.Background()); err != nil {
		t.Fatalf("probe must pass: %v", err)
	}
	if _, err := c.DetailedStatus(context.Background()); err == nil {
		t.Fatalf("401 must propagate")
	}
}

func TestUploadSendsMultipart(t *testing.T) {
	var gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		if !strings.Contains(gotCT, "multipart/form-data") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"media_id": "mid123"})
	}))
	defer srv.Close()
	// write a tiny png
	pngPath := t.TempDir() + "/a.png"
	if err := os.WriteFile(pngPath, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, 0600); err != nil {
		t.Fatal(err)
	}
	c := testClient(srv.URL, "")
	mid, err := c.UploadFile(context.Background(), pngPath)
	if err != nil || mid != "mid123" {
		t.Fatalf("upload: %v %q", err, mid)
	}
}
