// Package remote is the single typed client for the local gflow daemon.
// Both cmd/gflow and pkg/mcp use it so CLI and MCP share request,
// authentication, error decoding and deadline behavior.
package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xibodev/gflow-cli/pkg/models"
)

// Client talks to a running daemon over HTTP.
type Client struct {
	BaseURL     string
	APIToken    string
	HTTP        *http.Client
	PollEvery   time.Duration
	PollTimeout time.Duration
}

// New creates a remote client for host:port.
func New(host string, port int, apiToken string) *Client {
	return &Client{
		BaseURL:     fmt.Sprintf("http://%s:%d", host, port),
		APIToken:    apiToken,
		HTTP:        &http.Client{Timeout: 30 * time.Second},
		PollEvery:   6 * time.Second,
		PollTimeout: 25 * time.Minute,
	}
}

func (c *Client) req(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIToken)
	}
	return req, nil
}

func decodeError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	var envelope struct {
		Error *models.APIError `json:"error"`
	}
	if err := json.Unmarshal(b, &envelope); err == nil && envelope.Error != nil {
		return fmt.Errorf("daemon %d: %s", resp.StatusCode, envelope.Error.Error())
	}
	msg := strings.TrimSpace(string(b))
	if msg == "" {
		msg = resp.Status
	}
	return fmt.Errorf("daemon %d: %s", resp.StatusCode, msg)
}

// Probe checks the minimal unauthenticated liveness endpoint and verifies the
// expected product identity so port collisions are not mistaken for gflow.
func (c *Client) Probe(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health probe: %s", resp.Status)
	}
	var h struct {
		Product string `json:"product"`
		Status  string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return fmt.Errorf("health probe: invalid response: %w", err)
	}
	if h.Product != models.ProductIdentity {
		return fmt.Errorf("port collision: expected %s daemon, got product %q", models.ProductIdentity, h.Product)
	}
	return nil
}

// DetailedStatus returns authenticated daemon/extension readiness.
func (c *Client) DetailedStatus(ctx context.Context) (map[string]any, error) {
	req, err := c.req(ctx, http.MethodGet, "/v1/status", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, decodeError(resp)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// GenerateImages submits an image request and decodes the OpenAI envelope.
func (c *Client) GenerateImages(ctx context.Context, req models.ImageRequest) ([]models.Asset, error) {
	if err := models.ValidateImageRequest(&req); err != nil {
		return nil, err
	}
	hreq, err := c.req(ctx, http.MethodPost, "/v1/images/generations", req)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(hreq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, decodeError(resp)
	}
	var envelope struct {
		Data []struct {
			URL     string `json:"url"`
			B64JSON string `json:"b64_json"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("invalid image response: %w", err)
	}
	assets := make([]models.Asset, 0, len(envelope.Data))
	for i, d := range envelope.Data {
		assets = append(assets, models.Asset{
			ID: fmt.Sprintf("img_%d", i+1), Type: "image", URL: d.URL, MimeType: "image/png",
		})
	}
	return assets, nil
}

// SubmitVideo submits a job and returns the submission.
func (c *Client) SubmitVideo(ctx context.Context, req models.VideoSubmitRequest) (models.JobSubmission, error) {
	if err := models.ValidateVideoSubmit(&req); err != nil {
		return models.JobSubmission{}, err
	}
	hreq, err := c.req(ctx, http.MethodPost, "/v1/videos/generations", req)
	if err != nil {
		return models.JobSubmission{}, err
	}
	resp, err := c.HTTP.Do(hreq)
	if err != nil {
		return models.JobSubmission{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return models.JobSubmission{}, decodeError(resp)
	}
	var sub models.JobSubmission
	if err := json.NewDecoder(resp.Body).Decode(&sub); err != nil {
		return models.JobSubmission{}, fmt.Errorf("invalid submission response: %w", err)
	}
	if len(sub.MediaIDs) == 0 || sub.JobID == "" {
		return models.JobSubmission{}, fmt.Errorf("invalid submission response: missing job_id/media_ids")
	}
	return sub, nil
}

// CheckOnce performs a single status check (no waiting).
func (c *Client) CheckOnce(ctx context.Context, jobID string) (models.JobStatus, error) {
	hreq, err := c.req(ctx, http.MethodGet, "/v1/videos/generations/"+jobID, nil)
	if err != nil {
		return models.JobStatus{}, err
	}
	resp, err := c.HTTP.Do(hreq)
	if err != nil {
		return models.JobStatus{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// A failed job is returned as 200 with status failed; non-2xx is transport.
		return models.JobStatus{}, decodeError(resp)
	}
	var st models.JobStatus
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return models.JobStatus{}, fmt.Errorf("invalid status response: %w", err)
	}
	return st, nil
}

// Wait polls CheckOnce until success/failure or deadline/cancel.
func (c *Client) Wait(ctx context.Context, jobID string) (models.JobStatus, error) {
	deadline := time.Now().Add(c.PollTimeout)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return models.JobStatus{}, ctx.Err()
		case <-timer.C:
		}
		if time.Now().After(deadline) {
			return models.JobStatus{}, fmt.Errorf("timed out waiting for %s", jobID)
		}
		st, err := c.CheckOnce(ctx, jobID)
		if err != nil {
			// Transient transport error: keep waiting within deadline.
			timer.Reset(c.PollEvery)
			continue
		}
		if st.Status == "processing" {
			timer.Reset(c.PollEvery)
			continue
		}
		return st, nil
	}
}

// Upsample submits an upsample job.
func (c *Client) Upsample(ctx context.Context, mediaID, aspect, resolution string, seed *int64) (models.JobSubmission, error) {
	hreq, err := c.req(ctx, http.MethodPost, "/v1/videos/upsample",
		map[string]any{"media_id": mediaID, "aspect": aspect, "resolution": resolution, "seed": seed})
	if err != nil {
		return models.JobSubmission{}, err
	}
	resp, err := c.HTTP.Do(hreq)
	if err != nil {
		return models.JobSubmission{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return models.JobSubmission{}, decodeError(resp)
	}
	var sub models.JobSubmission
	if err := json.NewDecoder(resp.Body).Decode(&sub); err != nil {
		return models.JobSubmission{}, fmt.Errorf("invalid upsample response: %w", err)
	}
	if len(sub.MediaIDs) == 0 {
		return models.JobSubmission{}, fmt.Errorf("invalid upsample response: missing media_ids")
	}
	return sub, nil
}

// UploadFile streams a local file as multipart; the server never sees a path.
func (c *Client) UploadFile(ctx context.Context, filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(part, io.LimitReader(f, 32<<20+1)); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/upload", &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	if c.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIToken)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", decodeError(resp)
	}
	var out struct {
		MediaID string `json:"media_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("invalid upload response: %w", err)
	}
	if out.MediaID == "" {
		return "", fmt.Errorf("invalid upload response: missing media_id")
	}
	return out.MediaID, nil
}
