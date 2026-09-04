package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xibodev/gflow-cli/pkg/client"
)

// Server wraps the FlowClient, Bridge, and HTTP router.
type Server struct {
	flowClient *client.FlowClient
	server     *http.Server
}

// NewServer creates a new API server.
func NewServer(flowClient *client.FlowClient) *Server {
	return &Server{
		flowClient: flowClient,
	}
}

// Start begins listening on the configured host and port.
func (s *Server) Start() error {
	mux := http.NewServeMux()

	// Register extension bridge routes
	s.flowClient.Bridge().RegisterRoutes(mux)

	// API routes
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/v1/credits", s.handleCredits)
	mux.HandleFunc("/v1/images/generations", s.handleOpenAIImages)
	mux.HandleFunc("/v1/videos/generations", s.handleVideoGenerations)
	mux.HandleFunc("/v1/videos/generations/", s.handleVideoPoll)
	mux.HandleFunc("/v1/videos/upsample", s.handleUpsample)
	mux.HandleFunc("/v1/upload", s.handleUpload)

	addr := fmt.Sprintf("%s:%d", s.flowClient.Config().Host, s.flowClient.Config().Port)
	s.server = &http.Server{
		Addr:    addr,
		Handler: corsMiddleware(mux),
	}

	log.Printf("[Server] Flow server listening on http://%s", addr)
	log.Printf("[Server] Extension bridge ready at http://%s/api/ext/hello", addr)
	return s.server.ListenAndServe()
}

// Shutdown stops the server gracefully.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Client-Id")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	bridge := s.flowClient.Bridge()
	status := "healthy"
	if !bridge.IsConnected() {
		status = "waiting_for_extension"
	} else if !bridge.HasFlowKey() {
		status = "waiting_for_flow_tab"
	}

	res := map[string]any{
		"status":              status,
		"extension_connected": bridge.IsConnected(),
		"has_flow_key":        bridge.HasFlowKey(),
		"active_sessions":     bridge.ActiveSessionCount(),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

func (s *Server) handleCredits(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	credits, err := s.flowClient.GetCredits(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(credits)
}

// OpenAI Image Generation format request
type openAIImageReq struct {
	Prompt         string `json:"prompt"`
	N              int    `json:"n"`
	Size           string `json:"size"`
	Model          string `json:"model"`
	ResponseFormat string `json:"response_format"`
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
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req openAIImageReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	aspect := "landscape"
	if strings.Contains(req.Size, "1024x1024") || strings.Contains(req.Size, "512x512") {
		aspect = "square"
	} else if strings.Contains(req.Size, "1024x1792") || strings.Contains(req.Size, "9:16") {
		aspect = "portrait"
	}

	count := req.N
	if count <= 0 {
		count = 1
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	assets, err := s.flowClient.GenerateImages(ctx, req.Prompt, aspect, count, req.Model, nil, 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := openAIImageResp{
		Created: time.Now().Unix(),
		Data:    make([]openAIImageData, len(assets)),
	}
	for i, a := range assets {
		resp.Data[i] = openAIImageData{URL: a.URL}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

type videoGenReq struct {
	Prompt     string `json:"prompt"`
	Aspect     string `json:"aspect"`
	Duration   int    `json:"duration"`
	Resolution string `json:"resolution"`
	StartImage string `json:"start_image"`
	EndImage   string `json:"end_image"`
}

func (s *Server) handleVideoGenerations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req videoGenReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	mediaIDs, err := s.flowClient.GenerateVideo(ctx, req.Prompt, req.Aspect, req.Duration, "", req.StartImage, req.EndImage, 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	res := map[string]any{
		"job_id":    mediaIDs[0],
		"media_ids": mediaIDs,
		"status":    "processing",
		"created":   time.Now().Unix(),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

func (s *Server) handleVideoPoll(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/videos/generations/")
	if path == "" {
		http.Error(w, "missing job_id", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	assets, err := s.flowClient.WaitForVideo(ctx, []string{path}, 20*time.Second)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"job_id": path,
			"status": "processing",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"job_id": path,
		"status": "succeeded",
		"assets": assets,
	})
}

func (s *Server) handleUpsample(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		MediaID    string `json:"media_id"`
		Aspect     string `json:"aspect"`
		Resolution string `json:"resolution"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	upIDs, err := s.flowClient.UpsampleVideo(ctx, req.MediaID, req.Aspect, req.Resolution, 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"job_id":    upIDs[0],
		"media_ids": upIDs,
		"status":    "processing",
	})
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var filePath string
	var tempCreated bool

	if strings.Contains(r.Header.Get("Content-Type"), "multipart/form-data") {
		err := r.ParseMultipartForm(32 << 20)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "missing 'file' in multipart form", http.StatusBadRequest)
			return
		}
		defer file.Close()

		tempFile, err := os.CreateTemp("", "flow_upload_*"+filepath.Ext(header.Filename))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = io.Copy(tempFile, file)
		tempFile.Close()
		filePath = tempFile.Name()
		tempCreated = true
	} else {
		var req struct {
			FilePath string `json:"file_path"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		filePath = req.FilePath
	}

	if filePath == "" {
		http.Error(w, "no file provided", http.StatusBadRequest)
		return
	}
	if tempCreated {
		defer os.Remove(filePath)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	mediaID, err := s.flowClient.UploadImage(ctx, filePath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"media_id": mediaID,
	})
}
