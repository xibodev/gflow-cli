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

func TestExtensionBridgeHelloAndPoll(t *testing.T) {
	br := NewExtensionBridge()
	mux := http.NewServeMux()
	br.RegisterRoutes(mux)

	// Step 1: Send hello from extension
	helloReq := models.ExtensionHello{
		Type:           "hello",
		SessionID:      "test-client-1",
		FlowKey:        "ya29.test-bearer-token",
		FlowKeyPresent: true,
	}
	body, _ := json.Marshal(helloReq)

	req := httptest.NewRequest(http.MethodPost, "/api/ext/hello", bytes.NewReader(body))
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

	if !br.IsConnected() {
		t.Error("expected bridge to be connected after hello")
	}

	if !br.HasFlowKey() {
		t.Error("expected bridge to have flow key after hello")
	}

	if br.GetFlowKey() != "ya29.test-bearer-token" {
		t.Errorf("expected ya29.test-bearer-token, got %s", br.GetFlowKey())
	}

	// Step 2: Test API request dispatch and callback
	go func() {
		// Wait for command to appear in poll queue
		time.Sleep(50 * time.Millisecond)

		pollReq := httptest.NewRequest(http.MethodGet, "/api/ext/poll?session_id=test-client-1", nil)
		pollRec := httptest.NewRecorder()
		mux.ServeHTTP(pollRec, pollReq)

		var pollResp models.ExtensionPollResponse
		_ = json.NewDecoder(pollRec.Body).Decode(&pollResp)

		if len(pollResp.Commands) == 0 {
			t.Errorf("expected at least 1 command in poll")
			return
		}

		cmd := pollResp.Commands[0]

		// Post callback back
		cbReq := models.ExtensionCallback{
			ID:     cmd.ID,
			Status: 200,
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
		cbHttpReq := httptest.NewRequest(http.MethodPost, "/api/ext/callback", bytes.NewReader(cbBody))
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
