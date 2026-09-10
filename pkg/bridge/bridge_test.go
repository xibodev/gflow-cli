package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/xibodev/gflow-cli/pkg/models"
)

func testBridge() *ExtensionBridge {
	br := NewExtensionBridgeWithToken("test-bridge-token")
	br.now = time.Now
	return br
}

func authReq(t *testing.T, method, target string, body []byte) *http.Request {
	t.Helper()
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, target, bytes.NewReader(body))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	r.Header.Set("Authorization", "Bearer test-bridge-token")
	return r
}

func TestExtensionBridgeHelloAndPoll(t *testing.T) {
	br := testBridge()
	mux := http.NewServeMux()
	br.RegisterRoutes(mux)

	helloReq := models.ExtensionHello{
		Type:           "hello",
		SessionID:      "test-client-1",
		FlowKey:        "ya29.test-bearer-token",
		FlowKeyPresent: true,
	}
	body, _ := json.Marshal(helloReq)

	req := authReq(t, http.MethodPost, "/api/ext/hello", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var helloResp models.ExtensionHelloResponse
	if err := json.NewDecoder(w.Body).Decode(&helloResp); err != nil {
		t.Fatalf("failed to decode hello response: %v", err)
	}

	if !helloResp.OK || helloResp.SessionID != "test-client-1" {
		t.Errorf("unexpected hello response: %+v", helloResp)
	}
	if helloResp.Secret != "" {
		t.Errorf("hello must not return a usable secret")
	}

	if !br.IsConnected() {
		t.Error("expected bridge to be connected after hello")
	}
	if !br.HasFlowKey() {
		t.Error("expected bridge to have flow key after hello")
	}
	if br.GetFlowKey() != "ya29.test-bearer-token" {
		t.Errorf("expected ya29.test-bearer-token, got %s", br.GetFlowKey())
	}

	go func() {
		time.Sleep(50 * time.Millisecond)
		pollReq := authReq(t, http.MethodGet, "/api/ext/poll?session_id=test-client-1", nil)
		pollRec := httptest.NewRecorder()
		mux.ServeHTTP(pollRec, pollReq)
		var pollResp models.ExtensionPollResponse
		_ = json.NewDecoder(pollRec.Body).Decode(&pollResp)
		if len(pollResp.Commands) == 0 {
			t.Errorf("expected at least 1 command in poll")
			return
		}
		cmd := pollResp.Commands[0]
		cbReq := models.ExtensionCallback{
			ID:        cmd.ID,
			SessionID: "test-client-1",
			Status:    200,
			Data: map[string]any{
				"media": []any{
					map[string]any{
						"name": "media-12345",
						"image": map[string]any{
							"generatedImage": map[string]any{
								"fifeUrl": "https://storage.googleapis.com/test.png",
							},
						},
					},
				},
			},
		}
		cbBody, _ := json.Marshal(cbReq)
		cbHttpReq := authReq(t, http.MethodPost, "/api/ext/callback", cbBody)
		cbRec := httptest.NewRecorder()
		mux.ServeHTTP(cbRec, cbHttpReq)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := br.ExecuteAPIRequest(ctx, "https://test.api", map[string]any{"test": 1}, "IMAGE_GENERATION", "POST", nil)
	if err != nil {
		t.Fatalf("unexpected ExecuteAPIRequest error: %v", err)
	}
	if resp.Status != 200 {
		t.Errorf("expected 200 status, got %d", resp.Status)
	}
}

func TestBridgeRejectsUnauthenticated(t *testing.T) {
	br := testBridge()
	mux := http.NewServeMux()
	br.RegisterRoutes(mux)

	body, _ := json.Marshal(models.ExtensionHello{SessionID: "x"})
	req := httptest.NewRequest(http.MethodPost, "/api/ext/hello", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}

	poll := httptest.NewRequest(http.MethodGet, "/api/ext/poll?session_id=x", nil)
	pw := httptest.NewRecorder()
	mux.ServeHTTP(pw, poll)
	if pw.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for poll, got %d", pw.Code)
	}
}

func TestBridgeTokenIsolation(t *testing.T) {
	br := testBridge()
	mux := http.NewServeMux()
	br.RegisterRoutes(mux)

	for _, id := range []string{"sess-a", "sess-b"} {
		body, _ := json.Marshal(models.ExtensionHello{SessionID: id})
		req := authReq(t, http.MethodPost, "/api/ext/hello", body)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("hello %s: %d", id, w.Code)
		}
	}

	// Token reported by sess-a must not leak into sess-b.
	cb, _ := json.Marshal(models.ExtensionCallback{ID: "tok", SessionID: "sess-a", Type: "token_captured", FlowKey: "ya29.only-a"})
	req := authReq(t, http.MethodPost, "/api/ext/callback", cb)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("callback: %d", w.Code)
	}
	br.mu.RLock()
	bKey := br.sessions["sess-b"].FlowKey
	br.mu.RUnlock()
	if bKey != "" {
		t.Fatalf("token leaked across sessions")
	}

	// Cross-session command response must be rejected.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	type result struct {
		err error
	}
	done := make(chan result, 1)
	go func() {
		_, err := br.ExecuteAPIRequest(ctx, "https://example.test", nil, "", "GET", nil)
		done <- result{err}
	}()
	time.Sleep(50 * time.Millisecond)
	br.mu.RLock()
	var cmdID string
	for _, q := range br.commandQueue {
		if len(q) > 0 {
			cmdID = q[0].ID
		}
	}
	br.mu.RUnlock()
	if cmdID == "" {
		t.Fatalf("expected queued command")
	}
	wrong, _ := json.Marshal(models.ExtensionCallback{ID: cmdID, SessionID: "sess-b", Status: 200, Data: map[string]any{}})
	wreq := authReq(t, http.MethodPost, "/api/ext/callback", wrong)
	ww := httptest.NewRecorder()
	mux.ServeHTTP(ww, wreq)
	if ww.Code != http.StatusForbidden {
		t.Fatalf("expected 403 cross-session, got %d", ww.Code)
	}
	<-done
}

func TestBridgeUnknownSessionPollRequiresReregister(t *testing.T) {
	br := testBridge()
	mux := http.NewServeMux()
	br.RegisterRoutes(mux)
	req := authReq(t, http.MethodGet, "/api/ext/poll?session_id=missing", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
