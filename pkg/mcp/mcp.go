package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/xibodev/gflow-cli/pkg/client"
	"github.com/xibodev/gflow-cli/pkg/config"
	"github.com/xibodev/gflow-cli/pkg/gemini"
	"github.com/xibodev/gflow-cli/pkg/history"
	"github.com/xibodev/gflow-cli/pkg/models"
	"github.com/xibodev/gflow-cli/pkg/remote"
	"github.com/xibodev/gflow-cli/pkg/util"
)

// Request represents a JSON-RPC 2.0 request with raw ID preservation.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response represents a JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Error represents a JSON-RPC error.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// VideoJob tracks asynchronous video generation progress.
type VideoJob struct {
	ID          string     `json:"job_id"`
	Status      string     `json:"status"` // "queued", "processing", "completed", "failed"
	Prompt      string     `json:"prompt"`
	Aspect      string     `json:"aspect"`
	Duration    int        `json:"duration"`
	Resolution  string     `json:"resolution"`
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	FilePath    string     `json:"file_path,omitempty"`
	HasAudio    bool       `json:"has_audio,omitempty"`
	Error       string     `json:"error,omitempty"`

	seed       *int64
	startImage string
	endImage   string
}

// Server implements an MCP stdio server backed by the local daemon or Gemini.
type Server struct {
	provider string
	remote   *remote.Client
	cfg      *config.Config
	// legacy direct client (tests); when set, generation uses it instead.
	flowClient *client.FlowClient

	outMu  sync.Mutex
	mu     sync.Mutex
	cancel map[string]context.CancelFunc
	wg     sync.WaitGroup

	jobsMu     sync.RWMutex // protects jobs and all VideoJob fields after publication
	jobs       map[string]*VideoJob
	videoQueue chan *VideoJob

	HistoryList func(int) ([]history.Entry, error)
	HistoryAdd  func(history.Entry) error
}

// NewServer creates a server backed by a FlowClient (legacy/tests).
func NewServer(fc *client.FlowClient) *Server {
	s := &Server{
		provider:    "flow",
		flowClient:  fc,
		cfg:         fc.Config(),
		cancel:      make(map[string]context.CancelFunc),
		jobs:        make(map[string]*VideoJob),
		videoQueue:  make(chan *VideoJob, 100),
		HistoryList: history.List,
		HistoryAdd:  history.Add,
	}
	go s.startVideoWorker()
	return s
}

// NewServerRemote creates a daemon-backed server used by `gflow mcp`.
func NewServerRemote(r *remote.Client, cfg *config.Config) *Server {
	s := &Server{
		provider:    "flow",
		remote:      r,
		cfg:         cfg,
		cancel:      make(map[string]context.CancelFunc),
		jobs:        make(map[string]*VideoJob),
		videoQueue:  make(chan *VideoJob, 100),
		HistoryList: history.List,
		HistoryAdd:  history.Add,
	}
	go s.startVideoWorker()
	return s
}

// NewServerGemini creates an extension-free, daemon-free MCP server powered by Gemini.
func NewServerGemini(cfg *config.Config) *Server {
	s := &Server{
		provider:    "gemini",
		cfg:         cfg,
		cancel:      make(map[string]context.CancelFunc),
		jobs:        make(map[string]*VideoJob),
		videoQueue:  make(chan *VideoJob, 100),
		HistoryList: history.List,
		HistoryAdd:  history.Add,
	}
	go s.startVideoWorker()
	return s
}

func (s *Server) startVideoWorker() {
	defer func() {
		if r := recover(); r != nil {
			go s.startVideoWorker()
		}
	}()

	for job := range s.videoQueue {
		s.runSingleVideoJob(job)
	}
}

func (s *Server) runSingleVideoJob(job *VideoJob) {
	defer func() {
		if r := recover(); r != nil {
			now := time.Now()
			s.jobsMu.Lock()
			job.Error = fmt.Sprintf("worker error: %v", r)
			job.CompletedAt = &now
			job.Status = "failed"
			s.jobsMu.Unlock()
		}
	}()

	s.jobsMu.Lock()
	prompt := job.Prompt
	aspect := job.Aspect
	duration := job.Duration
	resolution := job.Resolution
	startImage := job.startImage
	endImage := job.endImage
	seed := job.seed
	now := time.Now()
	job.StartedAt = &now
	job.Status = "processing"
	s.jobsMu.Unlock()

	bgCtx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	assets, outDir, deliveredRes, err := s.generateVideo(bgCtx, prompt, aspect, duration, resolution, startImage, endImage, seed)

	if err != nil {
		completedAt := time.Now()
		s.jobsMu.Lock()
		job.CompletedAt = &completedAt
		job.Error = err.Error()
		job.Status = "failed"
		s.jobsMu.Unlock()
		return
	}

	saved, _ := s.saveAssets(bgCtx, assets, outDir, "video", prompt, aspect, "")
	if len(saved) == 0 {
		completedAt := time.Now()
		s.jobsMu.Lock()
		job.CompletedAt = &completedAt
		job.Error = "generation produced no downloadable assets"
		job.Status = "failed"
		s.jobsMu.Unlock()
		return
	}
	absPath, _ := filepath.Abs(saved[0])
	completedAt := time.Now()
	s.jobsMu.Lock()
	job.CompletedAt = &completedAt
	job.FilePath = absPath
	job.Resolution = deliveredRes
	job.HasAudio = true
	job.Status = "completed"
	s.jobsMu.Unlock()
}

// Run starts the JSON-RPC stdio loop. Stdout carries only JSON-RPC messages.
func (s *Server) Run() error {
	return s.RunWithIO(os.Stdin, os.Stdout)
}

// RunWithIO serves one stream pair; used by tests.
func (s *Server) RunWithIO(in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				if len(bytes.TrimSpace(line)) > 0 {
					s.dispatch(line, out)
				}
				s.waitIdle(5 * time.Second)
				s.cancelAll()
				return nil
			}
			return err
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		cp := append([]byte(nil), line...)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.dispatch(cp, out)
		}()
	}
}

func (s *Server) waitIdle(d time.Duration) {
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
	}
}

func (s *Server) cancelAll() {
	s.mu.Lock()
	for _, c := range s.cancel {
		c()
	}
	s.mu.Unlock()
}

func idKey(id json.RawMessage) string { return string(bytes.TrimSpace(id)) }

func (s *Server) dispatch(line []byte, out io.Writer) {
	defer func() {
		if r := recover(); r != nil {
			s.write(out, &Response{JSONRPC: "2.0", Error: &Error{Code: -32603, Message: fmt.Sprintf("internal error: %v", r)}})
		}
	}()
	var req Request
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	if err := dec.Decode(&req); err != nil {
		s.write(out, &Response{JSONRPC: "2.0", Error: &Error{Code: -32700, Message: "parse error"}})
		return
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		s.write(out, &Response{JSONRPC: "2.0", ID: req.ID, Error: &Error{Code: -32600, Message: "invalid request"}})
		return
	}
	// Notifications carry no ID and get no response.
	if len(bytes.TrimSpace(req.ID)) == 0 {
		if req.Method == "notifications/cancelled" {
			var p struct {
				RequestID json.RawMessage `json:"requestId"`
			}
			_ = json.Unmarshal(req.Params, &p)
			s.mu.Lock()
			if c, ok := s.cancel[idKey(p.RequestID)]; ok {
				c()
			}
			s.mu.Unlock()
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	s.mu.Lock()
	s.cancel[idKey(req.ID)] = cancel
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.cancel, idKey(req.ID))
		s.mu.Unlock()
	}()
	resp := s.handleRequest(ctx, &req)
	if resp != nil {
		s.write(out, resp)
	}
}

func (s *Server) write(out io.Writer, resp *Response) {
	b, _ := json.Marshal(resp)
	s.outMu.Lock()
	defer s.outMu.Unlock()
	_, _ = io.WriteString(out, string(b)+"\n")
}

func (s *Server) handleRequest(ctx context.Context, req *Request) *Response {
	switch req.Method {
	case "initialize":
		return &Response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"protocolVersion": models.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": models.AppName, "version": models.AppVersion},
		}}
	case "notifications/initialized", "notifications/cancelled":
		return nil
	case "ping":
		return &Response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}
	case "tools/list":
		return &Response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": s.getToolsList()}}
	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		dec := json.NewDecoder(bytes.NewReader(req.Params))
		dec.UseNumber()
		if err := dec.Decode(&params); err != nil || params.Name == "" {
			return &Response{JSONRPC: "2.0", ID: req.ID, Error: &Error{Code: -32602, Message: "invalid params"}}
		}
		result, err := s.executeTool(ctx, params.Name, params.Arguments)
		if err != nil {
			return &Response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
				"isError": true,
				"content": []map[string]any{{"type": "text", "text": fmt.Sprintf("Error: %v", err)}},
			}}
		}
		return &Response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"content": []map[string]any{{"type": "text", "text": result}},
		}}
	default:
		return &Response{JSONRPC: "2.0", ID: req.ID, Error: &Error{Code: -32601, Message: fmt.Sprintf("method not found: %s", req.Method)}}
	}
}

func (s *Server) getToolsList() []map[string]any {
	return []map[string]any{
		{
			"name": "generate_flow_image", "description": "Generate AI images using Google Flow (Imagen 4 / Nano Banana 2).",
			"inputSchema": map[string]any{"type": "object",
				"properties": map[string]any{
					"prompt":          map[string]any{"type": "string", "description": "Detailed image prompt."},
					"aspect":          map[string]any{"type": "string", "description": "landscape, square, portrait, 4:3, 3:4.", "default": "landscape"},
					"model":           map[string]any{"type": "string", "description": "narwhal, harbor_seal, gem_pix_2.", "default": "narwhal"},
					"count":           map[string]any{"type": "integer", "description": "Variations 1-4.", "default": 1, "minimum": 1, "maximum": 4},
					"reference_image": map[string]any{"type": "string", "description": "Reference image file path or media ID."},
					"seed":            map[string]any{"type": "integer", "description": "Reproducible seed 0-4294967295."},
				}, "required": []string{"prompt"}},
		},
		{
			"name": "generate_flow_video",
			"description": "Start AI video generation with native synchronous audio (dialogue, voiceover, SFX, ambience) using Veo 3.1.\n\n" +
				"NON-BLOCKING WORKFLOW:\n" +
				"Returns immediately with a 'job_id' (in <1s) to prevent client socket timeouts. Poll 'get_flow_status(job_id=...)' every 10-15s until 'status' is 'completed', which returns the final 'file_path'. Multiple video jobs can be started in parallel.\n\n" +
				"PROMPTING TIPS FOR AGENTS:\n" +
				"• Multi-Scene Cuts: Use timestamp prefixes to cut scenes within a clip: '[00:00-00:04] Wide shot of... [00:04-00:08] Close-up of...'.\n" +
				"• Dialogue & Lip-Sync: Use colon syntax to speak and avoid burnt-in subtitles: 'Character looks at camera and says: \"Hello world\"'.\n" +
				"• Voice & Language: Specify narrator gender, language, and tone: 'Audio: A female French narrator speaks calmly in French, saying: \"...\"'.\n" +
				"• Foley & Ambience: Layer background sounds: 'SFX: gentle rain, crackling fireplace. Audio: no background music'.\n" +
				"• Subtitle Prevention: Always append '(no subtitles, no text overlays)' at the end.\n" +
				"• Multi-Clip Assembly: For long videos (>10s), generate sequential continuation clips that an agent can concatenate via ffmpeg.",
			"inputSchema": map[string]any{"type": "object",
				"properties": map[string]any{
					"prompt":      map[string]any{"type": "string", "description": "Scene, camera motion, and audio prompt (dialogue, SFX, voiceover, language)."},
					"duration":    map[string]any{"type": "integer", "description": "Clip duration in seconds: 4, 6, 8, or 10.", "default": 10, "enum": []int{4, 6, 8, 10}},
					"aspect":      map[string]any{"type": "string", "description": "landscape (16:9), portrait (9:16), square (1:1).", "default": "landscape"},
					"resolution":  map[string]any{"type": "string", "description": "720p native, or 1080p/4k via upsample.", "default": "720p"},
					"start_image": map[string]any{"type": "string", "description": "Start frame file path or media ID for image-to-video."},
					"end_image":   map[string]any{"type": "string", "description": "End frame file path or media ID for first-to-last frame interpolation."},
					"seed":        map[string]any{"type": "integer", "description": "Reproducible seed."},
				}, "required": []string{"prompt"}},
		},
		{
			"name": "upsample_flow_video", "description": "Upsample a finished video to 1080p or 4K.",
			"inputSchema": map[string]any{"type": "object",
				"properties": map[string]any{
					"media_id":   map[string]any{"type": "string", "description": "Source video media ID."},
					"aspect":     map[string]any{"type": "string", "default": "landscape"},
					"resolution": map[string]any{"type": "string", "description": "1080p or 4k.", "default": "1080p"},
					"seed":       map[string]any{"type": "integer"},
				}, "required": []string{"media_id"}},
		},
		{
			"name":        "get_flow_status",
			"description": "Check AI provider readiness or poll an ongoing video generation job. Pass 'job_id' to check if a video has completed rendering and retrieve its file_path.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"job_id": map[string]any{
						"type":        "string",
						"description": "Optional video job ID (e.g. 'job_vid_...') to check generation progress or retrieve output file_path.",
					},
				},
			},
		},
		{
			"name": "get_flow_history", "description": "List recent generations.",
			"inputSchema": map[string]any{"type": "object",
				"properties": map[string]any{"limit": map[string]any{"type": "integer", "default": 10, "minimum": 1, "maximum": 500}}},
		},
	}
}

func asSeed(v any) (*int64, error) {
	if v == nil {
		return nil, nil
	}
	var n int64
	switch t := v.(type) {
	case json.Number:
		i, err := t.Int64()
		if err != nil {
			return nil, fmt.Errorf("invalid seed")
		}
		n = i
	case float64:
		n = int64(t)
	case int:
		n = int64(t)
	case int64:
		n = t
	default:
		return nil, fmt.Errorf("invalid seed")
	}
	if n < 0 || n > models.MaxSeed {
		return nil, fmt.Errorf("seed must be in [0,%d]", models.MaxSeed)
	}
	return &n, nil
}

func (s *Server) resolveImageRef(ctx context.Context, ref string) (string, error) {
	if ref == "" {
		return "", nil
	}
	if st, err := os.Stat(ref); err == nil && !st.IsDir() {
		if s.remote != nil {
			return s.remote.UploadFile(ctx, ref)
		}
		return s.flowClient.UploadImage(ctx, ref)
	}
	if looksLikePath(ref) {
		return "", fmt.Errorf("reference not found: %s", ref)
	}
	return ref, nil
}

func looksLikePath(v string) bool {
	if strings.ContainsAny(v, `/\`) {
		return true
	}
	lower := strings.ToLower(v)
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".webp"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

func (s *Server) executeTool(ctx context.Context, name string, args map[string]any) (string, error) {
	if args == nil {
		args = map[string]any{}
	}
	switch name {
	case "get_flow_status":
		jobID, _ := args["job_id"].(string)
		if jobID == "" {
			jobID, _ = args["jobId"].(string)
		}
		if jobID != "" {
			s.jobsMu.RLock()
			storedJob, ok := s.jobs[jobID]
			var job VideoJob
			if ok {
				job = *storedJob
			}
			s.jobsMu.RUnlock()
			if !ok {
				res := map[string]any{
					"status":  "not_found",
					"job_id":  jobID,
					"message": fmt.Sprintf("job %s not found in registry", jobID),
				}
				b, _ := json.MarshalIndent(res, "", "  ")
				return string(b), nil
			}
			res := map[string]any{
				"status":   job.Status,
				"job_id":   job.ID,
				"prompt":   job.Prompt,
				"duration": job.Duration,
				"aspect":   job.Aspect,
			}
			if job.Status == "queued" {
				res["message"] = "Video job is queued waiting for active generation to finish. Continue polling."
			} else if job.Status == "processing" {
				elapsed := 0
				if job.StartedAt != nil {
					elapsed = int(time.Since(*job.StartedAt).Seconds())
				} else {
					elapsed = int(time.Since(job.CreatedAt).Seconds())
				}
				res["elapsed_seconds"] = elapsed
				res["message"] = "Video is currently generating in background. Continue polling."
			} else if job.Status == "completed" {
				res["file_path"] = job.FilePath
				res["resolution"] = job.Resolution
				res["has_audio"] = job.HasAudio
				res["message"] = "Video generation completed successfully."
			} else if job.Status == "failed" {
				res["error"] = job.Error
				res["message"] = "Video generation failed."
			}
			b, _ := json.MarshalIndent(res, "", "  ")
			return string(b), nil
		}

		if s.provider == "gemini" {
			sess, err := gemini.LoadSession()
			exe, exeErr := gemini.FindGeminiExecutable()
			status := "ready"
			updatedStr := "never"
			if err != nil || sess == nil || sess.At == "" {
				status = "unauthenticated"
			} else {
				updatedStr = sess.UpdatedAt.Format(time.RFC3339)
			}
			return fmt.Sprintf("Provider: Gemini (Extension-Free)\nApp Installed: %v (%s)\nSession Status: %s\nUpdated: %s",
				exeErr == nil, exe, status, updatedStr), nil
		}
		if s.remote != nil {
			st, err := s.remote.DetailedStatus(ctx)
			if err != nil {
				return "", err
			}
			b, _ := json.MarshalIndent(st, "", "  ")
			return string(b), nil
		}
		br := s.flowClient.Bridge()
		return fmt.Sprintf("Extension Connected: %v\nHas Flow Token: %v\nActive Sessions: %d",
			br.IsConnected(), br.HasFlowKey(), br.ActiveSessionCount()), nil
	case "get_flow_history":
		limit := 10
		switch l := args["limit"].(type) {
		case json.Number:
			if i, err := l.Int64(); err == nil && i > 0 {
				limit = int(i)
			}
		case float64:
			if l > 0 {
				limit = int(l)
			}
		}
		entries, err := s.HistoryList(limit)
		if err != nil {
			return "", err
		}
		data, _ := json.MarshalIndent(entries, "", "  ")
		return string(data), nil
	case "generate_flow_image":
		prompt, _ := args["prompt"].(string)
		if strings.TrimSpace(prompt) == "" {
			return "", fmt.Errorf("prompt is required")
		}
		aspect, _ := args["aspect"].(string)
		if aspect == "" {
			aspect = "landscape"
		}
		model, _ := args["model"].(string)
		if model == "" {
			model = "narwhal"
		}
		count := 1
		switch c := args["count"].(type) {
		case json.Number:
			if i, err := c.Int64(); err == nil {
				count = int(i)
			}
		case float64:
			count = int(c)
		}
		seed, err := asSeed(args["seed"])
		if err != nil {
			return "", err
		}
		var refs []string
		if r, _ := args["reference_image"].(string); r != "" {
			mid, err := s.resolveImageRef(ctx, r)
			if err != nil {
				return "", err
			}
			refs = append(refs, mid)
		}
		assets, outDir, err := s.generateImages(ctx, prompt, aspect, count, model, refs, seed)
		if err != nil {
			return "", err
		}
		saved, saveErrs := s.saveAssets(ctx, assets, outDir, "image", prompt, aspect, model)
		if len(saved) == 0 {
			return "", fmt.Errorf("generation produced no downloadable assets (save errors: %v)", saveErrs)
		}
		absPath, _ := filepath.Abs(saved[0])
		resultData := map[string]any{
			"status":    "completed",
			"file_path": absPath,
			"files":     saved,
			"count":     len(saved),
			"aspect":    aspect,
			"model":     model,
			"prompt":    prompt,
		}
		jsonBytes, _ := json.MarshalIndent(resultData, "", "  ")
		msg := fmt.Sprintf("Generated %d image(s):\n%s", len(saved), string(jsonBytes))
		for _, e := range saveErrs {
			msg += "\nWarning: " + e
		}
		return msg, nil
	case "generate_flow_video":
		prompt, _ := args["prompt"].(string)
		if strings.TrimSpace(prompt) == "" {
			return "", fmt.Errorf("prompt is required")
		}
		duration := 10
		switch d := args["duration"].(type) {
		case json.Number:
			if i, err := d.Int64(); err == nil {
				duration = int(i)
			}
		case float64:
			duration = int(d)
		}
		aspect, _ := args["aspect"].(string)
		if aspect == "" {
			aspect = "landscape"
		}
		res, _ := args["resolution"].(string)
		if res == "" {
			res = "720p"
		}
		seed, err := asSeed(args["seed"])
		if err != nil {
			return "", err
		}
		start, _ := args["start_image"].(string)
		end, _ := args["end_image"].(string)
		if start != "" {
			if mid, err := s.resolveImageRef(ctx, start); err != nil {
				return "", err
			} else {
				start = mid
			}
		}
		if end != "" {
			if mid, err := s.resolveImageRef(ctx, end); err != nil {
				return "", err
			} else {
				end = mid
			}
		}
		jobID := fmt.Sprintf("job_vid_%d", time.Now().UnixNano())
		job := &VideoJob{
			ID:         jobID,
			Status:     "queued",
			Prompt:     prompt,
			Aspect:     aspect,
			Duration:   duration,
			Resolution: res,
			CreatedAt:  time.Now(),
			seed:       seed,
			startImage: start,
			endImage:   end,
		}
		s.jobsMu.Lock()
		s.jobs[jobID] = job
		qLen := len(s.videoQueue)
		s.jobsMu.Unlock()

		s.videoQueue <- job

		resultData := map[string]any{
			"status":   "queued",
			"job_id":   jobID,
			"prompt":   prompt,
			"duration": duration,
			"aspect":   aspect,
			"message":  fmt.Sprintf("Video job queued (queue depth: %d). Poll get_flow_status(job_id=%q) every 10-15s until completed.", qLen+1, jobID),
		}
		jsonBytes, _ := json.MarshalIndent(resultData, "", "  ")
		return string(jsonBytes), nil
	case "upsample_flow_video":
		mediaID, _ := args["media_id"].(string)
		if mediaID == "" {
			mediaID, _ = args["mediaId"].(string)
		}
		if strings.TrimSpace(mediaID) == "" {
			return "", fmt.Errorf("media_id is required")
		}
		aspect, _ := args["aspect"].(string)
		if aspect == "" {
			aspect = "landscape"
		}
		res, _ := args["resolution"].(string)
		if res == "" {
			res = "1080p"
		}
		seed, err := asSeed(args["seed"])
		if err != nil {
			return "", err
		}
		assets, outDir, err := s.upsample(ctx, mediaID, aspect, res, seed)
		if err != nil {
			return "", err
		}
		saved, saveErrs := s.saveAssets(ctx, assets, outDir, "video", "Upsample "+mediaID, aspect, "")
		if len(saved) == 0 {
			return "", fmt.Errorf("upsample produced no downloadable assets (save errors: %v)", saveErrs)
		}
		return fmt.Sprintf("Upsampled video (%s):\n%s", res, formatBulletList(saved)), nil
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func (s *Server) outDir() string {
	if s.cfg != nil && s.cfg.OutputDir != "" {
		return s.cfg.OutputDir
	}
	return "./output"
}

func (s *Server) generateImages(ctx context.Context, prompt, aspect string, count int, model string, refs []string, seed *int64) ([]models.Asset, string, error) {
	if s.provider == "gemini" {
		gc, err := gemini.NewClient(ctx, false)
		if err != nil {
			return nil, "", err
		}
		res, err := gc.Generate(ctx, "Generate an image of: "+prompt)
		if err != nil {
			return nil, "", err
		}
		var assets []models.Asset
		for i, u := range res.ImageURLs {
			p, err := gc.DownloadMedia(ctx, u, s.outDir())
			if err == nil {
				assets = append(assets, models.Asset{
					ID:        fmt.Sprintf("gemini_img_%d", i+1),
					Type:      "image",
					URL:       u,
					LocalPath: p,
					Prompt:    prompt,
					MimeType:  "image/jpeg",
				})
			}
		}
		return assets, s.outDir(), nil
	}
	if s.remote != nil {
		assets, err := s.remote.GenerateImages(ctx, models.ImageRequest{
			Prompt: prompt, N: count, Aspect: aspect, Model: model, ReferenceMediaIDs: refs, Seed: seed,
		})
		return assets, s.outDir(), err
	}
	assets, err := s.flowClient.GenerateImages(ctx, prompt, aspect, count, model, refs, deref(seed))
	return assets, s.outDir(), err
}

func (s *Server) generateVideo(ctx context.Context, prompt, aspect string, duration int, res, start, end string, seed *int64) ([]models.Asset, string, string, error) {
	if s.provider == "gemini" {
		savedPath, err := gemini.GenerateVideoCDP(ctx, prompt, s.outDir())
		if err == nil && savedPath != "" {
			asset := models.Asset{
				ID:        fmt.Sprintf("gemini_vid_%d", time.Now().Unix()),
				Type:      "video",
				LocalPath: savedPath,
				Prompt:    prompt,
				MimeType:  "video/mp4",
			}
			return []models.Asset{asset}, s.outDir(), "720p", nil
		}

		gc, err := gemini.NewClient(ctx, false)
		if err != nil {
			return nil, "", "", err
		}
		genRes, err := gc.Generate(ctx, "Generate a video of: "+prompt)
		if err != nil {
			return nil, "", "", err
		}
		if genRes.VideoURL == "" {
			return nil, "", "", fmt.Errorf("gemini response: %s", genRes.Text)
		}
		p, err := gc.DownloadMedia(ctx, genRes.VideoURL, s.outDir())
		if err != nil {
			return nil, "", "", err
		}
		asset := models.Asset{
			ID:        fmt.Sprintf("gemini_vid_%d", time.Now().Unix()),
			Type:      "video",
			URL:       genRes.VideoURL,
			LocalPath: p,
			Prompt:    prompt,
			MimeType:  "video/mp4",
		}
		return []models.Asset{asset}, s.outDir(), "720p", nil
	}
	delivered := "720p"
	if s.remote != nil {
		sub, err := s.remote.SubmitVideo(ctx, models.VideoSubmitRequest{
			Prompt: prompt, Aspect: aspect, Duration: duration, StartImage: start, EndImage: end, Seed: seed,
		})
		if err != nil {
			return nil, "", "", err
		}
		st, err := s.remote.Wait(ctx, sub.JobID)
		if err != nil {
			return nil, "", "", err
		}
		if st.Status == "failed" {
			msg := ""
			if st.Error != nil {
				msg = st.Error.Message
			}
			return nil, "", "", fmt.Errorf("video generation failed: %s", msg)
		}
		assets := st.Assets
		if res == "1080p" || res == "4k" {
			up, err := s.remote.Upsample(ctx, assets[0].ID, aspect, res, seed)
			if err != nil {
				return nil, "", "", fmt.Errorf("native video %s ready but upsample failed: %w", assets[0].ID, err)
			}
			ust, err := s.remote.Wait(ctx, up.JobID)
			if err != nil {
				return nil, "", "", fmt.Errorf("native video %s ready but upsample wait failed: %w", assets[0].ID, err)
			}
			if ust.Status == "failed" {
				return nil, "", "", fmt.Errorf("native video %s ready but upsample failed", assets[0].ID)
			}
			assets = ust.Assets
			delivered = res
		}
		return assets, s.outDir(), delivered, nil
	}
	mediaIDs, err := s.flowClient.GenerateVideo(ctx, prompt, aspect, duration, "", start, end, deref(seed))
	if err != nil {
		return nil, "", "", err
	}
	assets, err := s.flowClient.WaitForVideo(ctx, mediaIDs, 12*time.Minute)
	if err != nil {
		return nil, "", "", err
	}
	if res == "1080p" || res == "4k" {
		upIDs, err := s.flowClient.UpsampleVideo(ctx, assets[0].ID, aspect, res, deref(seed))
		if err != nil {
			return nil, "", "", fmt.Errorf("native video %s ready but upsample failed: %w", assets[0].ID, err)
		}
		upAssets, err := s.flowClient.WaitForVideo(ctx, upIDs, 10*time.Minute)
		if err != nil {
			return nil, "", "", fmt.Errorf("native video %s ready but upsample wait failed: %w", assets[0].ID, err)
		}
		assets = upAssets
		delivered = res
	}
	return assets, s.outDir(), delivered, nil
}

func (s *Server) upsample(ctx context.Context, mediaID, aspect, res string, seed *int64) ([]models.Asset, string, error) {
	if s.remote != nil {
		sub, err := s.remote.Upsample(ctx, mediaID, aspect, res, seed)
		if err != nil {
			return nil, "", err
		}
		st, err := s.remote.Wait(ctx, sub.JobID)
		if err != nil {
			return nil, "", fmt.Errorf("source %s retained; upsample wait failed: %w", mediaID, err)
		}
		if st.Status == "failed" {
			return nil, "", fmt.Errorf("upsample failed for source %s", mediaID)
		}
		return st.Assets, s.outDir(), nil
	}
	upIDs, err := s.flowClient.UpsampleVideo(ctx, mediaID, aspect, res, deref(seed))
	if err != nil {
		return nil, "", err
	}
	assets, err := s.flowClient.WaitForVideo(ctx, upIDs, 10*time.Minute)
	if err != nil {
		return nil, "", fmt.Errorf("source %s retained; upsample wait failed: %w", mediaID, err)
	}
	return assets, s.outDir(), nil
}

func (s *Server) saveAssets(ctx context.Context, assets []models.Asset, outDir, typ, prompt, aspect, model string) ([]string, []string) {
	var saved, errs []string
	for i := range assets {
		a := &assets[i]
		p, err := util.SaveAssetIndexed(ctx, a, outDir, i, len(assets))
		if err != nil {
			errs = append(errs, fmt.Sprintf("asset %d: %v", i+1, err))
			continue
		}
		saved = append(saved, p)
		_ = s.HistoryAdd(history.Entry{ID: a.ID, Type: typ, Prompt: prompt, LocalPath: p, URL: a.URL, Aspect: aspect, Model: model})
	}
	return saved, errs
}

func deref(p *int64) *int64 { return p }

func formatBulletList(items []string) string {
	res := ""
	for _, it := range items {
		res += fmt.Sprintf("- %s\n", it)
	}
	return res
}
