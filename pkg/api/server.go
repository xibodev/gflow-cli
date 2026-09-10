package api

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/xibodev/gflow-cli/pkg/bridge"
	"github.com/xibodev/gflow-cli/pkg/client"
	"github.com/xibodev/gflow-cli/pkg/config"
	"github.com/xibodev/gflow-cli/pkg/models"
	"github.com/xibodev/gflow-cli/pkg/util"
)

// BridgeStatus is the health surface needed by the daemon.
type BridgeStatus interface {
	IsConnected() bool
	HasFlowKey() bool
	ActiveSessionCount() int
}

// Service is the narrow upstream surface consumed by the router. Tests inject
// fakes; production wires *client.FlowClient which satisfies it.
type Service interface {
	GenerateImages(ctx context.Context, prompt, aspect string, count int, model string, refs []string, seed *int64) ([]models.Asset, error)
	GenerateVideo(ctx context.Context, prompt, aspect string, duration int, model, start, end string, seed *int64) ([]string, error)
	CheckVideoStatus(ctx context.Context, ids []string) ([]client.VideoMediaState, error)
	ResolveVideoAssets(ctx context.Context, ids []string) ([]models.Asset, error)
	UpsampleVideo(ctx context.Context, mediaID, aspect, resolution string, seed *int64) ([]string, error)
	UploadImageBytes(ctx context.Context, data []byte, filename string) (string, error)
	GetCredits(ctx context.Context) (any, error)
}

// Server wraps the FlowClient, Bridge, and HTTP router.
type Server struct {
	svc      Service
	bridge   BridgeStatus
	cfg      *config.Config
	server   *http.Server
	apiToken string
}

// NewServer creates a new API server backed by a FlowClient.
func NewServer(flowClient *client.FlowClient) *Server {
	return &Server{
		svc:      flowClient,
		bridge:   flowClient.Bridge(),
		cfg:      flowClient.Config(),
		apiToken: flowClient.Config().APIToken,
	}
}

// NewServerWithService creates a server with an injected service (tests).
func NewServerWithService(svc Service, br BridgeStatus, cfg *config.Config) *Server {
	return &Server{svc: svc, bridge: br, cfg: cfg, apiToken: cfg.APIToken}
}

// Handler builds the full route tree for production and httptest use.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// Bridge routes are registered when the concrete bridge is available.
	if br, ok := s.bridge.(*bridge.ExtensionBridge); ok {
		br.RegisterRoutes(mux)
	}
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/v1/status", s.handleDetailedStatus)
	mux.HandleFunc("/v1/credits", s.requireAPI(s.handleCredits))
	mux.HandleFunc("/v1/images/generations", s.requireAPI(s.handleOpenAIImages))
	mux.HandleFunc("/v1/videos/generations", s.requireAPI(s.handleVideoGenerations))
	mux.HandleFunc("/v1/videos/generations/", s.requireAPI(s.handleVideoPoll))
	mux.HandleFunc("/v1/videos/upsample", s.requireAPI(s.handleUpsample))
	mux.HandleFunc("/v1/upload", s.requireAPI(s.handleUpload))
	return originGuard(corsPreflight(mux))
}

// Start begins listening on the configured host and port.
func (s *Server) Start() error {
	if err := config.ValidateBindHost(s.cfg.Host); err != nil {
		return err
	}
	if err := config.ValidatePort(s.cfg.Port); err != nil {
		return err
	}
	if s.apiToken != "" && s.cfg.BridgeToken != "" && s.apiToken == s.cfg.BridgeToken {
		return fmt.Errorf("API and bridge tokens must be distinct")
	}
	// Ensure the concrete bridge is registered (Handler already does it).
	addr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)
	s.server = &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	log.Printf("[Server] Flow server listening on http://%s", addr)
	return s.server.ListenAndServe()
}

// Shutdown stops the server gracefully.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

func (s *Server) authorized(r *http.Request) bool {
	if s.apiToken == "" {
		return true // test mode without configured tokens
	}
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) <= len(prefix) || h[:len(prefix)] != prefix {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(h[len(prefix):]), []byte(s.apiToken)) == 1
}

func (s *Server) requireAPI(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid API credentials")
			return
		}
		next(w, r)
	}
}

func corsPreflight(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Vary", "Origin")
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// originGuard rejects arbitrary website origins. Extension service workers and
// native clients (no Origin) are allowed; https/http pages are not.
func originGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(origin, "chrome-extension://") {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			next.ServeHTTP(w, r)
			return
		}
		writeError(w, http.StatusForbidden, "forbidden", "origin not allowed")
	})
}

// handleHealth is the minimal unauthenticated liveness probe.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"product": models.ProductIdentity,
		"version": models.AppVersion,
		"status":  "ok",
	})
}

func (s *Server) detailedSnapshot() map[string]any {
	connected := s.bridge.IsConnected()
	flowKey := s.bridge.HasFlowKey()
	status := "healthy"
	if !connected {
		status = "waiting_for_extension"
	} else if !flowKey {
		status = "waiting_for_flow_tab"
	}
	return map[string]any{
		"product":             models.ProductIdentity,
		"version":             models.AppVersion,
		"status":              status,
		"extension_connected": connected,
		"has_flow_key":        flowKey,
		"active_sessions":     s.bridge.ActiveSessionCount(),
	}
}

func (s *Server) handleDetailedStatus(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid API credentials")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.detailedSnapshot())
}

func (s *Server) handleCredits(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	credits, err := s.svc.GetCredits(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream", "credit lookup failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(credits)
}

// OpenAI Image Generation format request (extended with gflow fields).
type openAIImageReq struct {
	Prompt            string   `json:"prompt"`
	N                 int      `json:"n"`
	Size              string   `json:"size"`
	Model             string   `json:"model"`
	ResponseFormat    string   `json:"response_format"`
	Aspect            string   `json:"aspect"`
	ReferenceMediaIDs []string `json:"reference_media_ids"`
	Seed              *int64   `json:"seed"`
}

type openAIImageData struct {
	URL     string `json:"url,omitempty"`
	B64JSON string `json:"b64_json,omitempty"`
}

type openAIImageResp struct {
	Created int64             `json:"created"`
	Data    []openAIImageData `json:"data"`
}

func (s *Server) handleOpenAIImages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req openAIImageReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	aspect, err := config.ResolveImageAspect(req.Aspect, req.Size)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	count := req.N
	if count <= 0 {
		count = 1
	}
	dto := &models.ImageRequest{
		Prompt: req.Prompt, N: count, Size: req.Size, Model: req.Model,
		ResponseFormat: req.ResponseFormat, Aspect: aspect,
		ReferenceMediaIDs: req.ReferenceMediaIDs, Seed: req.Seed,
	}
	if err := models.ValidateImageRequest(dto); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	assets, err := s.svc.GenerateImages(ctx, req.Prompt, aspect, count, req.Model, req.ReferenceMediaIDs, req.Seed)
	if err != nil {
		if errors.Is(err, models.ErrValidation) {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, "upstream", "image generation failed")
		return
	}
	format := req.ResponseFormat
	if format == "" {
		format = "url"
	}
	resp := openAIImageResp{Created: time.Now().Unix(), Data: make([]openAIImageData, len(assets))}
	for i, a := range assets {
		if format == "b64_json" {
			b64, err := fetchB64(ctx, a.URL)
			if err != nil {
				writeError(w, http.StatusBadGateway, "upstream", "failed to encode image payload")
				return
			}
			resp.Data[i] = openAIImageData{B64JSON: b64}
		} else {
			resp.Data[i] = openAIImageData{URL: a.URL}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func fetchB64(ctx context.Context, url string) (string, error) {
	if url == "" {
		return "", fmt.Errorf("empty URL")
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
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > 32<<20 {
		return "", fmt.Errorf("payload too large")
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

type videoGenReq struct {
	Prompt     string `json:"prompt"`
	Aspect     string `json:"aspect"`
	Duration   int    `json:"duration"`
	Resolution string `json:"resolution"`
	StartImage string `json:"start_image"`
	EndImage   string `json:"end_image"`
	Seed       *int64 `json:"seed"`
	Model      string `json:"model"`
}

func (s *Server) handleVideoGenerations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req videoGenReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	dto := &models.VideoSubmitRequest{
		Prompt: req.Prompt, Aspect: req.Aspect, Duration: req.Duration,
		StartImage: req.StartImage, EndImage: req.EndImage, Seed: req.Seed, Resolution: req.Resolution,
	}
	if err := models.ValidateVideoSubmit(dto); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	duration := req.Duration
	if duration == 0 {
		duration = 10
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	mediaIDs, err := s.svc.GenerateVideo(ctx, req.Prompt, req.Aspect, duration, req.Model, req.StartImage, req.EndImage, req.Seed)
	if err != nil {
		if errors.Is(err, models.ErrValidation) {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, "upstream", "video submission failed")
		return
	}
	if len(mediaIDs) == 0 {
		writeError(w, http.StatusBadGateway, "upstream", "no media ID returned")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"job_id": mediaIDs[0], "media_ids": mediaIDs, "status": "processing", "created": time.Now().Unix(),
	})
}

func (s *Server) handleVideoPoll(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/videos/generations/")
	if path == "" || strings.Contains(path, "/") {
		writeError(w, http.StatusBadRequest, "bad_request", "missing job_id")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	states, err := s.svc.CheckVideoStatus(ctx, []string{path})
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream", "status check failed")
		return
	}
	if len(states) != 1 {
		writeError(w, http.StatusBadGateway, "upstream", "invalid status response")
		return
	}
	switch states[0].Status {
	case "processing":
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"job_id": path, "status": "processing"})
		return
	case "failed":
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"job_id": path, "status": "failed",
			"error": map[string]string{"code": "terminal", "message": states[0].Reason},
		})
		return
	case "succeeded":
		assets, err := s.svc.ResolveVideoAssets(ctx, []string{path})
		if err != nil {
			writeError(w, http.StatusBadGateway, "upstream", "failed to resolve video URL")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"job_id": path, "status": "succeeded", "assets": assets})
		return
	default:
		writeError(w, http.StatusBadGateway, "upstream", "unknown job status")
		return
	}
}

func (s *Server) handleUpsample(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		MediaID    string `json:"media_id"`
		Aspect     string `json:"aspect"`
		Resolution string `json:"resolution"`
		Seed       *int64 `json:"seed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if strings.TrimSpace(req.MediaID) == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "media_id is required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	upIDs, err := s.svc.UpsampleVideo(ctx, req.MediaID, req.Aspect, req.Resolution, req.Seed)
	if err != nil {
		if errors.Is(err, models.ErrValidation) {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, "upstream", "upsample failed")
		return
	}
	if len(upIDs) == 0 {
		writeError(w, http.StatusBadGateway, "upstream", "no media ID returned")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"job_id": upIDs[0], "media_ids": upIDs, "status": "processing"})
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}
	ct := r.Header.Get("Content-Type")
	if !strings.Contains(ct, "multipart/form-data") {
		writeError(w, http.StatusBadRequest, "bad_request", "multipart upload with 'file' field is required; server-side file paths are not accepted")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 33<<20)
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "upload too large or malformed")
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "missing 'file' in multipart form")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 32<<20+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "failed to read upload")
		return
	}
	if int64(len(data)) > 32<<20 {
		writeError(w, http.StatusBadRequest, "bad_request", "upload exceeds 32MiB")
		return
	}
	if len(data) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "empty upload")
		return
	}
	mime := util.SniffMediaType(data)
	if !strings.HasPrefix(mime, "image/") {
		writeError(w, http.StatusBadRequest, "bad_request", "unsupported image content")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	mediaID, err := s.svc.UploadImageBytes(ctx, data, header.Filename)
	if err != nil {
		if errors.Is(err, models.ErrValidation) {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, "upstream", "upload failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"media_id": mediaID})
}
