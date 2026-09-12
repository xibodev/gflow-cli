package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xibodev/gflow-cli/pkg/config"
	"github.com/xibodev/gflow-cli/pkg/history"
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
