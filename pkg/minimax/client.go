package minimax

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xibodev/gflow-cli/pkg/models"
	"github.com/xibodev/gflow-cli/pkg/util"
)

const (
	CloudBaseURL   = "https://design.minimax.io"
	AppVersionCode = "3.0.14"
	AppID          = "3001"
	BizID          = "0"
)

// Client executes direct-to-cloud operations on MiniMax Design API.
type Client struct {
	baseURL string
	session *Session
	http    *http.Client
}

// NewClient creates a new MiniMax client with resolved session.
func NewClient(ctx context.Context) (*Client, error) {
	sess, err := LoadSession()
	if err != nil {
		return nil, err
	}
	return &Client{
		baseURL: CloudBaseURL,
		session: sess,
		http: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}, nil
}

func (c *Client) getBaseURL() string {
	if c.baseURL != "" {
		return c.baseURL
	}
	return CloudBaseURL
}

func (c *Client) buildQueryParams() string {
	devID := c.session.DeviceID
	if devID == "" {
		devID = "default"
	}
	nowUnix := fmt.Sprintf("%d", time.Now().Unix())

	q := url.Values{}
	q.Set("device_platform", "desktop")
	q.Set("app_id", AppID)
	q.Set("version_code", AppVersionCode)
	q.Set("biz_id", BizID)
	q.Set("unix", nowUnix)
	q.Set("os_name", "windows")
	q.Set("device_id", devID)
	q.Set("uuid", devID)
	q.Set("download_source", "default")
	return q.Encode()
}

func (c *Client) setAuthHeaders(req *http.Request) {
	tok := c.session.AccessToken
	req.Header.Set("token", tok)
	req.Header.Set("Authorization", "Bearer "+tok)
	if c.session.GroupID != "" {
		req.Header.Set("X-Group-Id", c.session.GroupID)
	}
	req.Header.Set("X-Hilo-Lang", "en")
	req.Header.Set("Content-Type", "application/json")
}

// GenerateVideo submits a video generation job to MiniMax H3.
func (c *Client) GenerateVideo(ctx context.Context, prompt, resolution string, duration int, ratio string) (string, error) {
	if strings.TrimSpace(prompt) == "" {
		return "", fmt.Errorf("%w: prompt is required", models.ErrValidation)
	}
	if resolution == "" {
		resolution = "768P"
	}
	if duration <= 0 {
		duration = 4
	}
	if ratio == "" {
		ratio = "16:9"
	}

	payload := map[string]any{
		"prompt":     prompt,
		"resolution": resolution,
		"duration":   duration,
		"ratio":      ratio,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	reqURL := fmt.Sprintf("%s/api/v1/video/minimax-v3/generate?%s", c.getBaseURL(), c.buildQueryParams())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	c.setAuthHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("minimax submit request error: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", fmt.Errorf("%w: MiniMax authentication expired (HTTP %d): %s", models.ErrAuth, resp.StatusCode, string(body))
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: MiniMax API error (HTTP %d): %s", models.ErrUpstream, resp.StatusCode, string(body))
	}

	var res struct {
		TaskID   string `json:"task_id"`
		BaseResp struct {
			StatusCode int    `json:"status_code"`
			StatusMsg  string `json:"status_msg"`
		} `json:"base_resp"`
	}

	if err := json.Unmarshal(body, &res); err != nil {
		return "", fmt.Errorf("invalid submit response: %w", err)
	}
	if res.TaskID == "" {
		return "", fmt.Errorf("%w: no task_id returned from MiniMax: %s", models.ErrUpstream, string(body))
	}

	return res.TaskID, nil
}

// TaskStatus represents the current status of a generation task.
type TaskStatus struct {
	TaskID          string `json:"task_id"`
	Status          string `json:"status"` // "processing", "success", "failed"
	FileID          string `json:"file_id"`
	ProviderTaskID  string `json:"provider_task_id"`
	RefundStatus    string `json:"refund_status,omitempty"`
	RefundedCredits int    `json:"refunded_credits,omitempty"`
	ErrorMessage    string `json:"error_message,omitempty"`
}

// PollTask checks the status of an in-flight video generation.
func (c *Client) PollTask(ctx context.Context, taskID string) (*TaskStatus, error) {
	reqURL := fmt.Sprintf("%s/api/v1/video/minimax-v3/tasks/%s?%s", c.getBaseURL(), taskID, c.buildQueryParams())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	c.setAuthHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("task poll HTTP %d: %s", resp.StatusCode, string(body))
	}

	var raw struct {
		Status          string `json:"status"`
		FileID          string `json:"file_id"`
		TaskID          string `json:"task_id"`
		ProviderTaskID  string `json:"provider_task_id"`
		RefundStatus    string `json:"refund_status"`
		RefundedCredits int    `json:"refunded_credits"`
		BaseResp        struct {
			StatusCode int    `json:"status_code"`
			StatusMsg  string `json:"status_msg"`
		} `json:"base_resp"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse task status: %w", err)
	}

	st := &TaskStatus{
		TaskID:          taskID,
		Status:          raw.Status,
		FileID:          raw.FileID,
		ProviderTaskID:  raw.ProviderTaskID,
		RefundStatus:    raw.RefundStatus,
		RefundedCredits: raw.RefundedCredits,
	}

	if raw.Status == "failed" {
		st.ErrorMessage = raw.BaseResp.StatusMsg
		if st.ErrorMessage == "" {
			st.ErrorMessage = "generation failed on MiniMax"
		}
	}

	return st, nil
}

// GetDownloadURL retrieves the final public video download URL for a completed file_id.
func (c *Client) GetDownloadURL(ctx context.Context, fileID string) (string, error) {
	reqURL := fmt.Sprintf("%s/api/v1/video/minimax/files/%s?%s", c.getBaseURL(), fileID, c.buildQueryParams())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", err
	}
	c.setAuthHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var res struct {
		File struct {
			DownloadURL string `json:"download_url"`
			FileID      string `json:"file_id"`
		} `json:"file"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}

	if res.File.DownloadURL == "" {
		return "", errors.New("no download_url in files response")
	}

	return res.File.DownloadURL, nil
}

// WaitForVideo polls until the video renders or timeout occurs.
func (c *Client) WaitForVideo(ctx context.Context, taskID string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		st, err := c.PollTask(ctx, taskID)
		if err != nil {
			time.Sleep(5 * time.Second)
			continue
		}

		switch st.Status {
		case "success":
			fileID := st.FileID
			if fileID == "" {
				fileID = taskID
			}
			return c.GetDownloadURL(ctx, fileID)
		case "failed":
			return "", fmt.Errorf("minimax generation failed: %s (refund: %s)", st.ErrorMessage, st.RefundStatus)
		}

		time.Sleep(8 * time.Second)
	}

	return "", fmt.Errorf("minimax video rendering timed out after %v", timeout)
}

// DownloadFile downloads a URL to targetPath and validates magic bytes.
func (c *Client) DownloadFile(ctx context.Context, url string, targetPath string) (string, error) {
	dest := targetPath
	if dest == "" || isDir(dest) || filepath.Ext(dest) == "" {
		name := fmt.Sprintf("minimax_%s.mp4", time.Now().Format("20060102_150405"))
		if dest == "" {
			dest = name
		} else {
			dest = filepath.Join(dest, name)
		}
	}

	dir := filepath.Dir(dest)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return "", err
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d downloading media: %s", resp.StatusCode, url)
	}

	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return "", err
	}
	defer f.Close()

	written, err := io.Copy(f, resp.Body)
	if err != nil || written == 0 {
		_ = os.Remove(dest)
		return "", errors.New("downloaded empty payload or write error")
	}

	_ = f.Sync()
	dataHead := make([]byte, 512)
	_, _ = f.ReadAt(dataHead, 0)
	mime := util.SniffMediaType(dataHead)
	if !strings.Contains(mime, "video") && !strings.Contains(mime, "mp4") {
		_ = os.Remove(dest)
		return "", fmt.Errorf("downloaded payload is %s, expected video/mp4", mime)
	}

	return dest, nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	if err == nil && info.IsDir() {
		return true
	}
	return len(path) > 0 && (path[len(path)-1] == '/' || path[len(path)-1] == '\\')
}
