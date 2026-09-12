package minimax

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiniMaxClientQueryAndHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("token") != "test_token" {
			t.Errorf("expected token header 'test_token', got %q", r.Header.Get("token"))
		}
		if r.Header.Get("Authorization") != "Bearer test_token" {
			t.Errorf("expected Authorization Bearer test_token, got %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-Group-Id") != "test_gid" {
			t.Errorf("expected X-Group-Id test_gid, got %q", r.Header.Get("X-Group-Id"))
		}

		q := r.URL.Query()
		if q.Get("version_code") != AppVersionCode {
			t.Errorf("expected version_code %s, got %s", AppVersionCode, q.Get("version_code"))
		}

		if r.Method == http.MethodPost {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["prompt"] != "a cute cat" {
				t.Errorf("unexpected prompt: %v", body["prompt"])
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"task_id": "task_123",
				"base_resp": map[string]any{
					"status_code": 0,
					"status_msg":  "success",
				},
			})
		}
	}))
	defer srv.Close()

	client := &Client{
		baseURL: srv.URL,
		session: &Session{
			AccessToken: "test_token",
			GroupID:     "test_gid",
			DeviceID:    "test_dev",
		},
		http: srv.Client(),
	}

	params := client.buildQueryParams()
	if params == "" {
		t.Errorf("empty query params")
	}
}

func TestMiniMaxStatusParsing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":           "success",
			"file_id":          "file_999",
			"provider_task_id": "p_123",
			"refund_status":    "none",
			"base_resp": map[string]any{
				"status_code": 0,
				"status_msg":  "success",
			},
		})
	}))
	defer srv.Close()

	client := &Client{
		baseURL: srv.URL,
		session: &Session{AccessToken: "t"},
		http:    srv.Client(),
	}

	st, err := client.PollTask(context.Background(), "t_1")
	if err != nil {
		t.Fatalf("poll error: %v", err)
	}
	if st.Status != "success" {
		t.Errorf("expected success status, got %s", st.Status)
	}
	if st.FileID != "file_999" {
		t.Errorf("expected file_id file_999, got %s", st.FileID)
	}
}
