package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/xibodev/gflow-cli/pkg/bridge"
	"github.com/xibodev/gflow-cli/pkg/config"
	"github.com/xibodev/gflow-cli/pkg/models"
)

// Poll intervals are vars so tests can shorten them.
var (
	StatusPollInterval = 8 * time.Second
	StatusRetryDelay   = 5 * time.Second
	MaxUploadBytes     = int64(32 << 20)
)

// VideoMediaState is a single-shot upstream status observation.
type VideoMediaState struct {
	ID     string
	Status string // processing|succeeded|failed|unknown
	Reason string
}

// RequestExecutor abstracts the communication channel to Google Flow.
// It can be satisfied by *bridge.ExtensionBridge (daemon/extension mode)
// or *cdp.FlowBridge (extension-free CDP mode).
type RequestExecutor interface {
	ExecuteAPIRequest(ctx context.Context, urlPath string, body any, captchaAction string, method string, headers map[string]string) (*models.ExtensionCallback, error)
	RequestMediaURL(ctx context.Context, mediaID string) (string, error)
}

// FlowClient is the high-level client for Google Flow operations.
type FlowClient struct {
	cfg      *config.Config
	executor RequestExecutor
	bridge   *bridge.ExtensionBridge
}

// NewFlowClient creates a new FlowClient with configuration and bridge.
func NewFlowClient(cfg *config.Config, b *bridge.ExtensionBridge) *FlowClient {
	return &FlowClient{cfg: cfg, executor: b, bridge: b}
}

// NewFlowClientWithExecutor creates a FlowClient with any RequestExecutor (e.g. extension-free CDP).
func NewFlowClientWithExecutor(cfg *config.Config, exec RequestExecutor) *FlowClient {
	b, _ := exec.(*bridge.ExtensionBridge)
	return &FlowClient{cfg: cfg, executor: exec, bridge: b}
}

// Bridge returns the underlying ExtensionBridge if present.
func (c *FlowClient) Bridge() *bridge.ExtensionBridge { return c.bridge }

// Executor returns the underlying RequestExecutor.
func (c *FlowClient) Executor() RequestExecutor { return c.executor }

// Config returns the configuration.
func (c *FlowClient) Config() *config.Config { return c.cfg }

func (c *FlowClient) buildClientContext() *models.ClientContext {
	return &models.ClientContext{
		ProjectID:       c.cfg.ProjectID,
		Tool:            config.ToolName,
		UserPaygateTier: "PAYGATE_TIER_ONE",
		SessionID:       fmt.Sprintf(";%d", time.Now().UnixMilli()),
		RecaptchaContext: &models.RecaptchaContext{
			ApplicationType: "RECAPTCHA_APPLICATION_TYPE_WEB",
			Token:           "", // injected by extension
		},
	}
}

func resolveSeed(seed *int64) (int64, error) {
	if seed == nil {
		return time.Now().UnixNano() % 1000000, nil
	}
	if *seed < 0 || *seed > models.MaxSeed {
		return 0, fmt.Errorf("%w: seed must be in [0,%d]", models.ErrValidation, models.MaxSeed)
	}
	return *seed, nil
}

// GenerateImages generates images using Imagen 4 / Nano Banana 2.
func (c *FlowClient) GenerateImages(
	ctx context.Context,
	prompt string,
	aspect string,
	count int,
	model string,
	refMediaIDs []string,
	seed *int64,
) ([]models.Asset, error) {
	if _, err := c.cfg.RequireProjectID(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("%w: prompt is required", models.ErrValidation)
	}
	if count < 1 {
		count = 1
	}
	if count > 4 {
		count = 4
	}
	aspectName, err := config.ResolveImageAspect(aspect, "")
	if err != nil {
		// aspect may already be an upstream enum from older callers; validate loosely
		aspectName = strings.ToLower(strings.TrimSpace(aspect))
		if _, ok := config.ImageAspectMap[aspectName]; !ok {
			return nil, err
		}
	}
	aspectVal := config.ImageAspectMap[aspectName]

	targetModel := c.cfg.DefaultImageModel
	if model != "" {
		switch strings.ToLower(model) {
		case "lite", "harbor_seal":
			targetModel = config.ImageModelLite
		case "pro", "gem_pix_2":
			targetModel = config.ImageModelPro
		case "narwhal":
			targetModel = config.ImageModelDefault
		default:
			// Accept upstream enum directly; reject anything else.
			upper := strings.ToUpper(model)
			if upper != config.ImageModelDefault && upper != config.ImageModelLite && upper != config.ImageModelPro {
				return nil, fmt.Errorf("%w: unknown image model %q", models.ErrValidation, model)
			}
			targetModel = upper
		}
	}

	seedVal, err := resolveSeed(seed)
	if err != nil {
		return nil, err
	}

	clientCtx := c.buildClientContext()
	requests := make([]models.ImageRequestItem, count)
	for i := 0; i < count; i++ {
		reqItem := models.ImageRequestItem{
			ClientContext:    clientCtx,
			ImageModelName:   targetModel,
			ImageAspectRatio: aspectVal,
			StructuredPrompt: &models.StructuredPrompt{
				Parts: []models.TextPart{{Text: prompt}},
			},
			Seed: (seedVal + int64(i*1000)) % 4294967296,
		}
		if len(refMediaIDs) > 0 {
			reqItem.ImageInputs = make([]models.ImageInput, len(refMediaIDs))
			for j, mid := range refMediaIDs {
				reqItem.ImageInputs[j] = models.ImageInput{
					Name:           mid,
					ImageInputType: "IMAGE_INPUT_TYPE_REFERENCE",
				}
			}
		}
		requests[i] = reqItem
	}

	batchReq := models.BatchGenerateImagesRequest{
		ClientContext: clientCtx,
		MediaGenerationContext: &models.MediaGenerationContext{
			BatchID: randomUUID(),
		},
		UseNewMedia: true,
		Requests:    requests,
	}

	endpoint := fmt.Sprintf("%s%s?key=%s", config.APIBase, fmt.Sprintf(config.EndpointBatchGenerateImages, c.cfg.ProjectID), config.APIKey)

	log.Printf("[Client] Generating %d image(s) [%s, %s]", count, aspectVal, targetModel)
	resp, err := c.executor.ExecuteAPIRequest(ctx, endpoint, batchReq, "IMAGE_GENERATION", "POST", nil)
	if err != nil {
		return nil, fmt.Errorf("generate images error: %w", err)
	}
	if resp.Status != 200 {
		return nil, fmt.Errorf("%w: API error (status %d): %v", models.ErrUpstream, resp.Status, resp.Data)
	}
	dataBytes, err := json.Marshal(resp.Data)
	if err != nil {
		return nil, err
	}
	var batchResp models.BatchGenerateImagesResponse
	if err := json.Unmarshal(dataBytes, &batchResp); err != nil {
		return nil, fmt.Errorf("%w: failed to parse image response: %v", models.ErrUpstream, err)
	}
	if len(batchResp.Media) == 0 {
		return nil, fmt.Errorf("%w: no media returned in generation response", models.ErrUpstream)
	}
	var assets []models.Asset
	for _, m := range batchResp.Media {
		imgURL := m.Image.GeneratedImage.FifeURL
		if imgURL == "" {
			imgURL = m.Image.GeneratedImage.ImageURI
		}
		assets = append(assets, models.Asset{
			ID:       m.Name,
			Type:     "image",
			URL:      imgURL,
			Prompt:   prompt,
			MimeType: "image/png",
		})
	}
	return assets, nil
}

// GenerateVideo submits a video generation job (Veo 3.1) and returns media IDs.
func (c *FlowClient) GenerateVideo(
	ctx context.Context,
	prompt string,
	aspect string,
	duration int,
	model string,
	startMediaID string,
	endMediaID string,
	seed *int64,
) ([]string, error) {
	if _, err := c.cfg.RequireProjectID(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("%w: prompt is required", models.ErrValidation)
	}
	aspectName, err := config.ResolveVideoAspect(aspect)
	if err != nil {
		return nil, err
	}
	aspectVal := config.VideoAspectMap[aspectName]
	if duration == 0 {
		duration = 10
	}
	if duration != 4 && duration != 6 && duration != 8 && duration != 10 {
		return nil, fmt.Errorf("%w: duration must be 4, 6, 8, or 10", models.ErrValidation)
	}
	if endMediaID != "" && startMediaID == "" {
		return nil, fmt.Errorf("%w: end_image requires start_image", models.ErrValidation)
	}
	modelKey := model
	if modelKey == "" {
		modelKey = fmt.Sprintf("abra_t2v_%ds", duration)
	}
	seedVal, err := resolveSeed(seed)
	if err != nil {
		return nil, err
	}

	clientCtx := c.buildClientContext()
	reqItem := models.VideoRequestItem{
		AspectRatio:   aspectVal,
		Seed:          seedVal % 4294967296,
		VideoModelKey: modelKey,
		Metadata:      map[string]any{},
		TextInput: &models.VideoTextInput{
			StructuredPrompt: &models.StructuredPrompt{
				Parts: []models.TextPart{{Text: prompt}},
			},
		},
	}

	endpointPath := config.EndpointGenerateVideoText
	if startMediaID != "" && endMediaID != "" {
		endpointPath = config.EndpointGenerateVideoFL
		reqItem.StartImage = &models.VideoMediaRef{MediaID: startMediaID}
		reqItem.EndImage = &models.VideoMediaRef{MediaID: endMediaID}
	} else if startMediaID != "" {
		endpointPath = config.EndpointGenerateVideoStart
		reqItem.StartImage = &models.VideoMediaRef{MediaID: startMediaID}
	}

	endpoint := fmt.Sprintf("%s%s?key=%s", config.APIBase, endpointPath, config.APIKey)
	payload := models.BatchGenerateVideoRequest{
		MediaGenerationContext: &models.MediaGenerationContext{BatchID: randomUUID()},
		ClientContext:          clientCtx,
		Requests:               []models.VideoRequestItem{reqItem},
		UseV2ModelConfig:       true,
	}

	log.Printf("[Client] Submitting video job [%s, %ds, %s]", aspectVal, duration, modelKey)
	resp, err := c.executor.ExecuteAPIRequest(ctx, endpoint, payload, "VIDEO_GENERATION", "POST", nil)
	if err != nil {
		return nil, fmt.Errorf("submit video error: %w", err)
	}
	if resp.Status != 200 {
		return nil, fmt.Errorf("%w: video API error (status %d): %v", models.ErrUpstream, resp.Status, resp.Data)
	}
	dataBytes, _ := json.Marshal(resp.Data)
	var opResp models.VideoOperationResponse
	if err := json.Unmarshal(dataBytes, &opResp); err != nil {
		return nil, fmt.Errorf("%w: failed to parse video response: %v", models.ErrUpstream, err)
	}
	var mediaIDs []string
	for _, m := range opResp.Media {
		if m.Name != "" {
			mediaIDs = append(mediaIDs, m.Name)
		}
	}
	if len(mediaIDs) == 0 {
		for _, op := range opResp.Operations {
			if op.Operation.Name != "" {
				mediaIDs = append(mediaIDs, op.Operation.Name)
			}
		}
	}
	if len(mediaIDs) == 0 {
		return nil, fmt.Errorf("%w: no media ID returned in video generation response", models.ErrUpstream)
	}
	return mediaIDs, nil
}

// CheckVideoStatus performs a single upstream status check.
func (c *FlowClient) CheckVideoStatus(ctx context.Context, mediaIDs []string) ([]VideoMediaState, error) {
	if len(mediaIDs) == 0 {
		return nil, fmt.Errorf("%w: no media IDs to check", models.ErrValidation)
	}
	if _, err := c.cfg.RequireProjectID(); err != nil {
		return nil, err
	}
	pollEndpoint := fmt.Sprintf("%s%s?key=%s", config.APIBase, config.EndpointPollVideoStatus, config.APIKey)
	checkReq := models.VideoStatusCheckRequest{
		Media: make([]models.VideoStatusCheckItem, len(mediaIDs)),
	}
	for i, id := range mediaIDs {
		checkReq.Media[i] = models.VideoStatusCheckItem{Name: id, ProjectID: c.cfg.ProjectID}
	}
	resp, err := c.executor.ExecuteAPIRequest(ctx, pollEndpoint, checkReq, "", "POST", nil)
	if err != nil {
		return nil, fmt.Errorf("%w: status check transport: %v", models.ErrUpstream, err)
	}
	if resp.Status != 200 {
		return nil, fmt.Errorf("%w: status check (status %d): %v", models.ErrUpstream, resp.Status, resp.Data)
	}
	dataBytes, _ := json.Marshal(resp.Data)
	var statusResp models.VideoStatusCheckResponse
	if err := json.Unmarshal(dataBytes, &statusResp); err != nil {
		return nil, fmt.Errorf("%w: failed to parse status response: %v", models.ErrUpstream, err)
	}
	if len(statusResp.Media) == 0 {
		return nil, fmt.Errorf("%w: empty status response", models.ErrUpstream)
	}
	byName := make(map[string]VideoMediaState, len(statusResp.Media))
	for _, item := range statusResp.Media {
		status := item.MediaMetadata.MediaStatus.MediaGenerationStatus
		state := VideoMediaState{ID: item.Name}
		switch {
		case strings.Contains(status, "FAILED") || strings.Contains(status, "BLOCKED"):
			state.Status = "failed"
			state.Reason = item.MediaMetadata.MediaStatus.FailureReason
			if state.Reason == "" {
				state.Reason = item.MediaMetadata.MediaStatus.ErrorMessage
			}
			if state.Reason == "" {
				state.Reason = status
			}
		case status == "MEDIA_GENERATION_STATUS_SUCCESSFUL" || status == "MEDIA_GENERATION_STATUS_COMPLETE":
			state.Status = "succeeded"
		case status == "" || status == "MEDIA_GENERATION_STATUS_UNKNOWN":
			state.Status = "unknown"
			state.Reason = "unrecognized status " + status
		case strings.Contains(status, "PENDING") || strings.Contains(status, "PROCESSING") || strings.Contains(status, "IN_PROGRESS") || strings.Contains(status, "RUNNING"):
			state.Status = "processing"
		default:
			state.Status = "unknown"
			state.Reason = "unrecognized status " + status
		}
		byName[item.Name] = state
	}
	out := make([]VideoMediaState, 0, len(mediaIDs))
	for _, id := range mediaIDs {
		st, ok := byName[id]
		if !ok {
			return nil, fmt.Errorf("%w: missing status for %s", models.ErrUpstream, id)
		}
		out = append(out, st)
	}
	return out, nil
}

// WaitForVideo polls CheckVideoStatus until all media succeed or timeout occurs.
// Terminal failures return ErrTerminal; transport/decode problems are retried
// only while the deadline allows and never reported as success.
func (c *FlowClient) WaitForVideo(ctx context.Context, mediaIDs []string, timeout time.Duration) ([]models.Asset, error) {
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("video rendering timed out after %v: %w", timeout, models.ErrUpstream)
		}
		states, err := c.CheckVideoStatus(ctx, mediaIDs)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}
			log.Printf("[Client] Poll status error: %v, retrying...", err)
			timer.Reset(StatusRetryDelay)
			continue
		}
		var completed []string
		allDone := true
		for _, st := range states {
			switch st.Status {
			case "failed":
				return nil, fmt.Errorf("video generation failed for %s: %s: %w", st.ID, st.Reason, models.ErrTerminal)
			case "succeeded":
				completed = append(completed, st.ID)
			case "processing":
				allDone = false
			default:
				return nil, fmt.Errorf("video generation returned unknown status for %s: %s: %w", st.ID, st.Reason, models.ErrUpstream)
			}
		}
		if allDone && len(completed) > 0 {
			var assets []models.Asset
			for _, id := range completed {
				videoURL := ""
				u, err := c.executor.RequestMediaURL(ctx, id)
				if err == nil && u != "" {
					videoURL = u
				}
				if videoURL == "" {
					detailEndpoint := fmt.Sprintf("%s%s?key=%s", config.APIBase, fmt.Sprintf(config.EndpointGetFlowMedia, id), config.APIKey)
					detailResp, err := c.executor.ExecuteAPIRequest(ctx, detailEndpoint, nil, "", "GET", nil)
					if err == nil && detailResp.Status == 200 {
						detailBytes, _ := json.Marshal(detailResp.Data)
						var detail models.FlowMediaResponse
						_ = json.Unmarshal(detailBytes, &detail)
						videoURL = detail.Video.GeneratedVideo.FifeURL
					}
				}
				if videoURL == "" {
					return nil, fmt.Errorf("%w: could not resolve download URL for %s", models.ErrUpstream, id)
				}
				assets = append(assets, models.Asset{ID: id, Type: "video", URL: videoURL, MimeType: "video/mp4"})
			}
			return assets, nil
		}
		timer.Reset(StatusPollInterval)
	}
}

// ResolveVideoAssets checks status once and resolves download URLs when all
// media succeeded. Processing work returns ErrUpstream-wrapped pending marker
// via IsProcessing helper; callers should map terminal failures to failed jobs
// and transport problems to non-2xx API errors, never to success.
func (c *FlowClient) ResolveVideoAssets(ctx context.Context, mediaIDs []string) ([]models.Asset, error) {
	states, err := c.CheckVideoStatus(ctx, mediaIDs)
	if err != nil {
		return nil, err
	}
	for _, st := range states {
		switch st.Status {
		case "failed":
			return nil, fmt.Errorf("video generation failed for %s: %s: %w", st.ID, st.Reason, models.ErrTerminal)
		case "succeeded":
		case "processing":
			return nil, fmt.Errorf("%w: job still processing", models.ErrUpstream)
		default:
			return nil, fmt.Errorf("%w: unknown status for %s: %s", models.ErrUpstream, st.ID, st.Reason)
		}
	}
	var assets []models.Asset
	for _, id := range mediaIDs {
		videoURL := ""
		u, err := c.executor.RequestMediaURL(ctx, id)
		if err == nil && u != "" {
			videoURL = u
		}
		if videoURL == "" {
			detailEndpoint := fmt.Sprintf("%s%s?key=%s", config.APIBase, fmt.Sprintf(config.EndpointGetFlowMedia, id), config.APIKey)
			detailResp, err := c.executor.ExecuteAPIRequest(ctx, detailEndpoint, nil, "", "GET", nil)
			if err == nil && detailResp.Status == 200 {
				detailBytes, _ := json.Marshal(detailResp.Data)
				var detail models.FlowMediaResponse
				_ = json.Unmarshal(detailBytes, &detail)
				videoURL = detail.Video.GeneratedVideo.FifeURL
			}
		}
		if videoURL == "" {
			return nil, fmt.Errorf("%w: could not resolve download URL for %s", models.ErrUpstream, id)
		}
		assets = append(assets, models.Asset{ID: id, Type: "video", URL: videoURL, MimeType: "video/mp4"})
	}
	return assets, nil
}

// UpsampleVideo submits an upsampling pass (1080p or 4K).
func (c *FlowClient) UpsampleVideo(
	ctx context.Context,
	mediaID string,
	aspect string,
	resolution string,
	seed *int64,
) ([]string, error) {
	if strings.TrimSpace(mediaID) == "" {
		return nil, fmt.Errorf("%w: media ID is required", models.ErrValidation)
	}
	aspectName, err := config.ResolveVideoAspect(aspect)
	if err != nil {
		return nil, err
	}
	aspectVal := config.VideoAspectMap[aspectName]
	modelKey := config.VideoUpsampler1080p
	resEnum := config.Resolution1080pEnum
	switch strings.ToLower(strings.TrimSpace(resolution)) {
	case "", "1080p":
		// default
	case "4k":
		modelKey = config.VideoUpsampler4k
		resEnum = config.Resolution4kEnum
	default:
		return nil, fmt.Errorf("%w: unknown upsample resolution %q", models.ErrValidation, resolution)
	}
	seedVal, err := resolveSeed(seed)
	if err != nil {
		return nil, err
	}
	if _, err := c.cfg.RequireProjectID(); err != nil {
		return nil, err
	}
	clientCtx := c.buildClientContext()
	reqItem := models.VideoRequestItem{
		AspectRatio:   aspectVal,
		VideoModelKey: modelKey,
		Resolution:    resEnum,
		Seed:          seedVal % 4294967296,
		VideoInput:    &models.VideoMediaRef{MediaID: mediaID},
		Metadata:      map[string]any{},
	}
	endpoint := fmt.Sprintf("%s%s?key=%s", config.APIBase, config.EndpointUpsampleVideo, config.APIKey)
	payload := models.BatchGenerateVideoRequest{
		MediaGenerationContext: &models.MediaGenerationContext{BatchID: randomUUID()},
		ClientContext:          clientCtx,
		Requests:               []models.VideoRequestItem{reqItem},
	}
	resp, err := c.executor.ExecuteAPIRequest(ctx, endpoint, payload, "VIDEO_GENERATION", "POST", nil)
	if err != nil {
		return nil, fmt.Errorf("upsample error: %w", err)
	}
	if resp.Status != 200 {
		return nil, fmt.Errorf("%w: upsample API error (status %d): %v", models.ErrUpstream, resp.Status, resp.Data)
	}
	dataBytes, _ := json.Marshal(resp.Data)
	var opResp models.VideoOperationResponse
	if err := json.Unmarshal(dataBytes, &opResp); err != nil {
		return nil, fmt.Errorf("%w: failed to parse upsample response: %v", models.ErrUpstream, err)
	}
	var mediaIDs []string
	for _, m := range opResp.Media {
		if m.Name != "" {
			mediaIDs = append(mediaIDs, m.Name)
		}
	}
	if len(mediaIDs) == 0 {
		return nil, fmt.Errorf("%w: no media ID returned by upsample request", models.ErrUpstream)
	}
	return mediaIDs, nil
}

// UploadImageBytes uploads image bytes without inventing server-side paths.
func (c *FlowClient) UploadImageBytes(ctx context.Context, data []byte, filename string) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("%w: empty image data", models.ErrValidation)
	}
	if int64(len(data)) > MaxUploadBytes {
		return "", fmt.Errorf("%w: image exceeds 32MiB limit", models.ErrValidation)
	}
	if _, err := c.cfg.RequireProjectID(); err != nil {
		return "", err
	}
	var req models.UploadImageRequest
	req.ClientContext.Tool = config.ToolName
	req.ClientContext.ProjectID = c.cfg.ProjectID
	req.ImageBytes = base64.StdEncoding.EncodeToString(data)
	endpoint := fmt.Sprintf("%s%s?key=%s", config.APIBase, config.EndpointUploadImage, config.APIKey)
	resp, err := c.executor.ExecuteAPIRequest(ctx, endpoint, req, "", "POST", nil)
	if err != nil {
		return "", fmt.Errorf("upload image error: %w", err)
	}
	if resp.Status != 200 {
		return "", fmt.Errorf("%w: upload image API error (status %d): %v", models.ErrUpstream, resp.Status, resp.Data)
	}
	dataBytes, _ := json.Marshal(resp.Data)
	var upResp models.UploadImageResponse
	_ = json.Unmarshal(dataBytes, &upResp)
	mediaID := upResp.MediaID
	if mediaID == "" {
		mediaID = upResp.Name
	}
	if mediaID == "" && upResp.Media != nil {
		mediaID = upResp.Media.Name
		if mediaID == "" {
			mediaID = upResp.Media.MediaID
		}
	}
	if mediaID == "" {
		return "", fmt.Errorf("%w: could not extract mediaId from response", models.ErrUpstream)
	}
	return mediaID, nil
}

// UploadImage uploads a local image to Google Flow for reference/I2V use.
func (c *FlowClient) UploadImage(ctx context.Context, imagePath string) (string, error) {
	data, err := readBounded(imagePath, MaxUploadBytes)
	if err != nil {
		return "", err
	}
	return c.UploadImageBytes(ctx, data, imagePath)
}

func readBounded(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	limited := io.LimitReader(f, max+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%w: file exceeds 32MiB limit", models.ErrValidation)
	}
	return data, nil
}

// GetCredits retrieves remaining credits from /v1/credits.
func (c *FlowClient) GetCredits(ctx context.Context) (any, error) {
	endpoint := fmt.Sprintf("%s%s?key=%s", config.APIBase, config.EndpointCredits, config.APIKey)
	resp, err := c.executor.ExecuteAPIRequest(ctx, endpoint, nil, "", "GET", nil)
	if err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func randomUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]),
	)
}

var _ = bytes.MinRead // keep bytes import if unused in future edits
