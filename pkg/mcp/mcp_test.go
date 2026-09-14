package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/xibodev/gflow-cli/pkg/config"
	"github.com/xibodev/gflow-cli/pkg/history"
	"github.com/xibodev/gflow-cli/pkg/models"
	"github.com/xibodev/gflow-cli/pkg/remote"
)

func daemonStub(t *testing.T) (*httptest.Server, *remote.Client) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"product": "gflow", "version": "1.0.0", "status": "ok"})
	})
	mux.HandleFunc("/v1/status", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"product": "gflow", "status": "healthy", "extension_connected": true, "has_flow_key": true, "active_sessions": 1})
	})
	mux.HandleFunc("/v1/images/generations", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"created": 1, "data": []map[string]any{{"url": "https://example.test/i.png"}}})
	})
	srv := httptest.NewServer(mux)
	rc := &remote.Client{BaseURL: srv.URL, HTTP: srv.Client(), PollEvery: 1, PollTimeout: 1}
	t.Cleanup(srv.Close)
	return srv, rc
}

func call(t *testing.T, s *Server, method string, id any, params any) map[string]any {
	t.Helper()
	var rawParams json.RawMessage
	if params != nil {
		b, _ := json.Marshal(params)
		rawParams = b
	}
	var rawID json.RawMessage
	if id != nil {
		b, _ := json.Marshal(id)
		rawID = b
	}
	resp := s.handleRequest(context.Background(), &Request{JSONRPC: "2.0", ID: rawID, Method: method, Params: rawParams})
	if resp == nil {
		return nil
	}
	b, _ := json.Marshal(resp)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

func TestInitializeAndToolsList(t *testing.T) {
	_, rc := daemonStub(t)
	s := NewServerRemote(rc, &config.Config{OutputDir: t.TempDir()})
	init := call(t, s, "initialize", 1, map[string]any{})
	if init["result"] == nil {
		t.Fatalf("initialize failed: %v", init)
	}
	list := call(t, s, "tools/list", 2, nil)
	res := list["result"].(map[string]any)
	tools := res["tools"].([]any)
	if len(tools) != 5 {
		t.Fatalf("want 5 tools, got %d", len(tools))
	}
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{"generate_flow_image", "generate_flow_video", "upsample_flow_video", "get_flow_status", "get_flow_history"} {
		if !names[want] {
			t.Fatalf("missing tool %s", want)
		}
	}
}

func TestUnknownToolIsErrorNotProtocolError(t *testing.T) {
	_, rc := daemonStub(t)
	s := NewServerRemote(rc, &config.Config{OutputDir: t.TempDir()})
	out := call(t, s, "tools/call", 3, map[string]any{"name": "nope", "arguments": map[string]any{}})
	if out["error"] != nil {
		t.Fatalf("unknown tool must be tool-error result, got protocol error")
	}
	res := out["result"].(map[string]any)
	if res["isError"] != true {
		t.Fatalf("want isError result: %v", out)
	}
}

func TestStatusUsesDaemon(t *testing.T) {
	_, rc := daemonStub(t)
	s := NewServerRemote(rc, &config.Config{OutputDir: t.TempDir()})
	out := call(t, s, "tools/call", 4, map[string]any{"name": "get_flow_status", "arguments": map[string]any{}})
	res := out["result"].(map[string]any)
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "healthy") {
		t.Fatalf("status must reflect daemon: %s", text)
	}
}

func TestNotificationsSilentAndIDsPreserved(t *testing.T) {
	_, rc := daemonStub(t)
	s := NewServerRemote(rc, &config.Config{OutputDir: t.TempDir()})
	var buf bytes.Buffer
	s.dispatch([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`+"\n"), &buf)
	if buf.Len() != 0 {
		t.Fatalf("notifications must be silent")
	}
	out := call(t, s, "ping", "abc-123", nil)
	if out["id"] != "abc-123" {
		t.Fatalf("string ID must be preserved: %v", out)
	}
}

func TestHistoryToolUsesInjectedStore(t *testing.T) {
	_, rc := daemonStub(t)
	s := NewServerRemote(rc, &config.Config{OutputDir: t.TempDir()})
	s.HistoryList = func(int) ([]history.Entry, error) {
		return []history.Entry{{ID: "h1", Type: "image"}}, nil
	}
	out := call(t, s, "tools/call", 5, map[string]any{"name": "get_flow_history", "arguments": map[string]any{"limit": 1}})
	text := out["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "h1") {
		t.Fatalf("history must come from store: %s", text)
	}
}

func TestAsyncVideoSubmitAndPoll(t *testing.T) {
	const timeout = 5 * time.Second
	videoPayload := []byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	requestPayload := make(chan models.VideoSubmitRequest, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/videos/generations", func(w http.ResponseWriter, r *http.Request) {
		var req models.VideoSubmitRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requestPayload <- req
		started <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"job_id":    "daemon-video-job",
			"media_ids": []string{"video-1"},
			"status":    "processing",
		})
	})
	mux.HandleFunc("/v1/videos/generations/daemon-video-job", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"job_id": "daemon-video-job",
			"status": "succeeded",
			"assets": []map[string]any{{
				"id":        "video-1",
				"type":      "video",
				"url":       "http://" + r.Host + "/video.mp4",
				"mime_type": "video/mp4",
			}},
		})
	})
	mux.HandleFunc("/video.mp4", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write(videoPayload)
	})

	srv := httptest.NewServer(mux)
	released := false
	t.Cleanup(func() {
		if !released {
			close(release)
		}
		srv.Close()
	})
	rc := &remote.Client{
		BaseURL:     srv.URL,
		HTTP:        srv.Client(),
		PollEvery:   time.Millisecond,
		PollTimeout: timeout,
	}
	s := NewServerRemote(rc, &config.Config{OutputDir: t.TempDir()})
	s.HistoryAdd = func(history.Entry) error { return nil }

	decodeToolData := func(out map[string]any) map[string]any {
		t.Helper()
		res := out["result"].(map[string]any)
		text := res["content"].([]any)[0].(map[string]any)["text"].(string)
		var data map[string]any
		if err := json.Unmarshal([]byte(text), &data); err != nil {
			t.Fatalf("tool response must be valid JSON: %v, raw: %s", err, text)
		}
		return data
	}
	poll := func(id any, jobID string) map[string]any {
		t.Helper()
		return decodeToolData(call(t, s, "tools/call", id, map[string]any{
			"name":      "get_flow_status",
			"arguments": map[string]any{"job_id": jobID},
		}))
	}

	submitDone := make(chan map[string]any, 1)
	go func() {
		submitDone <- call(t, s, "tools/call", 10, map[string]any{
			"name": "generate_flow_video",
			"arguments": map[string]any{
				"prompt":   "test scene in city",
				"duration": 10,
				"aspect":   "landscape",
			},
		})
	}()

	select {
	case <-started:
	case <-time.After(timeout):
		t.Fatal("backend did not start video generation")
	}

	var submitOut map[string]any
	select {
	case submitOut = <-submitDone:
	case <-time.After(timeout):
		t.Fatal("MCP submission blocked on backend generation")
	}
	submitData := decodeToolData(submitOut)
	if submitData["status"] != "queued" {
		t.Fatalf("expected queued submission, got %v", submitData["status"])
	}
	jobID, ok := submitData["job_id"].(string)
	if !ok || jobID == "" {
		t.Fatalf("expected non-empty job_id, got %v", submitData["job_id"])
	}

	backendRequest := <-requestPayload
	if backendRequest.Prompt != "test scene in city" || backendRequest.Duration != 10 || backendRequest.Aspect != "landscape" {
		t.Fatalf("unexpected daemon request payload: %+v", backendRequest)
	}

	processingData := poll(11, jobID)
	if processingData["status"] != "processing" || processingData["job_id"] != jobID {
		t.Fatalf("expected processing status for %s, got %v", jobID, processingData)
	}

	close(release)
	released = true
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	var terminalData map[string]any
	for terminalData == nil {
		select {
		case <-deadline.C:
			t.Fatalf("video job %s did not reach terminal state", jobID)
		default:
		}
		data := poll(12, jobID)
		switch data["status"] {
		case "queued", "processing":
		case "completed", "failed":
			terminalData = data
		default:
			t.Fatalf("unexpected video job status: %v", data)
		}
	}

	if terminalData["status"] != "completed" {
		t.Fatalf("video job failed: %v", terminalData)
	}
	if terminalData["job_id"] != jobID || terminalData["prompt"] != "test scene in city" || terminalData["duration"] != float64(10) || terminalData["aspect"] != "landscape" {
		t.Fatalf("unexpected terminal payload: %v", terminalData)
	}
	if terminalData["resolution"] != "720p" || terminalData["has_audio"] != true {
		t.Fatalf("unexpected completed video metadata: %v", terminalData)
	}
	filePath, ok := terminalData["file_path"].(string)
	if !ok || filePath == "" {
		t.Fatalf("expected completed video file path, got %v", terminalData["file_path"])
	}
	savedPayload, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read completed video: %v", err)
	}
	if !bytes.Equal(savedPayload, videoPayload) {
		t.Fatalf("saved video payload = %v, want %v", savedPayload, videoPayload)
	}
}
