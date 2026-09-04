package client

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/xibodev/gflow-cli/pkg/bridge"
	"github.com/xibodev/gflow-cli/pkg/config"
	"github.com/xibodev/gflow-cli/pkg/models"
)

// FlowClient is the high-level client for Google Flow operations.
type FlowClient struct {
	cfg    *config.Config
	bridge *bridge.ExtensionBridge
}

// NewFlowClient creates a new FlowClient with configuration and bridge.
func NewFlowClient(cfg *config.Config, b *bridge.ExtensionBridge) *FlowClient {
	return &FlowClient{
		cfg:    cfg,
		bridge: b,
	}
}

// Bridge returns the underlying ExtensionBridge.
func (c *FlowClient) Bridge() *bridge.ExtensionBridge {
	return c.bridge
}

// Config returns the configuration.
func (c *FlowClient) Config() *config.Config {
	return c.cfg
}

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

// GenerateImages generates images using Imagen 4 / Nano Banana 2.
func (c *FlowClient) GenerateImages(
	ctx context.Context,
	prompt string,
	aspect string,
	count int,
	model string,
	refMediaIDs []string,
	seed int64,
) ([]models.Asset, error) {
	if count < 1 {
		count = 1
	}
	if count > 4 {
		count = 4
	}

	aspectVal, ok := config.ImageAspectMap[strings.ToLower(aspect)]
	if !ok {
		aspectVal = "IMAGE_ASPECT_RATIO_LANDSCAPE"
	}

	targetModel := c.cfg.DefaultImageModel
	if model != "" {
		switch strings.ToLower(model) {
		case "lite", "harbor_seal":
			targetModel = config.ImageModelLite
		case "pro", "gem_pix_2":
			targetModel = config.ImageModelPro
		default:
			targetModel = config.ImageModelDefault
		}
	}

	if seed == 0 {
		seed = time.Now().UnixNano() % 1000000
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
			Seed: (seed + int64(i*1000)) % 4294967296,
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

	log.Printf("[Client] Generating %d image(s): %q [%s, %s]", count, prompt, aspectVal, targetModel)
	resp, err := c.bridge.ExecuteAPIRequest(ctx, endpoint, batchReq, "IMAGE_GENERATION", "POST", nil)
	if err != nil {
		return nil, fmt.Errorf("generate images error: %w", err)
	}

	if resp.Status != 200 {
		return nil, fmt.Errorf("API error (status %d): %v", resp.Status, resp.Data)
	}

	// Parse media results
	dataBytes, err := json.Marshal(resp.Data)
	if err != nil {
		return nil, err
	}

	var batchResp models.BatchGenerateImagesResponse
	if err := json.Unmarshal(dataBytes, &batchResp); err != nil {
		return nil, fmt.Errorf("failed to parse image response: %w", err)
	}

	if len(batchResp.Media) == 0 {
		return nil, errors.New("no media returned in generation response")
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
	seed int64,
) ([]string, error) {
	aspectVal, ok := config.VideoAspectMap[strings.ToLower(aspect)]
	if !ok {
		aspectVal = "VIDEO_ASPECT_RATIO_LANDSCAPE"
	}

	if duration != 4 && duration != 6 && duration != 8 && duration != 10 {
		duration = 10
	}

	modelKey := model
	if modelKey == "" {
		modelKey = fmt.Sprintf("abra_t2v_%ds", duration)
	}

	if seed == 0 {
		seed = time.Now().UnixNano() % 1000000
	}

	clientCtx := c.buildClientContext()
	reqItem := models.VideoRequestItem{
		AspectRatio:   aspectVal,
		Seed:          seed % 4294967296,
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
		MediaGenerationContext: &models.MediaGenerationContext{
			BatchID: randomUUID(),
		},
		ClientContext:    clientCtx,
		Requests:         []models.VideoRequestItem{reqItem},
		UseV2ModelConfig: true,
	}

	log.Printf("[Client] Submitting video job: %q [%s, %ds, %s]", prompt, aspectVal, duration, modelKey)
	resp, err := c.bridge.ExecuteAPIRequest(ctx, endpoint, payload, "VIDEO_GENERATION", "POST", nil)
	if err != nil {
		return nil, fmt.Errorf("submit video error: %w", err)
	}

	if resp.Status != 200 {
		return nil, fmt.Errorf("video API error (status %d): %v", resp.Status, resp.Data)
	}

	dataBytes, _ := json.Marshal(resp.Data)
	var opResp models.VideoOperationResponse
	if err := json.Unmarshal(dataBytes, &opResp); err != nil {
		return nil, fmt.Errorf("failed to parse video response: %w", err)
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
		return nil, errors.New("no media ID returned in video generation response")
	}

	return mediaIDs, nil
}

// WaitForVideo polls the status endpoint until the video finishes rendering or timeout occurs.
func (c *FlowClient) WaitForVideo(ctx context.Context, mediaIDs []string, timeout time.Duration) ([]models.Asset, error) {
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}

	deadline := time.Now().Add(timeout)
	pollEndpoint := fmt.Sprintf("%s%s?key=%s", config.APIBase, config.EndpointPollVideoStatus, config.APIKey)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		checkReq := models.VideoStatusCheckRequest{
			Media: make([]models.VideoStatusCheckItem, len(mediaIDs)),
		}
		for i, id := range mediaIDs {
			checkReq.Media[i] = models.VideoStatusCheckItem{
				Name:      id,
				ProjectID: c.cfg.ProjectID,
			}
		}

		resp, err := c.bridge.ExecuteAPIRequest(ctx, pollEndpoint, checkReq, "", "POST", nil)
		if err != nil {
			log.Printf("[Client] Poll status error: %v, retrying...", err)
			time.Sleep(5 * time.Second)
			continue
		}

		dataBytes, _ := json.Marshal(resp.Data)
		var statusResp models.VideoStatusCheckResponse
		_ = json.Unmarshal(dataBytes, &statusResp)

		allDone := true
		var completedIDs []string

		for _, item := range statusResp.Media {
			status := item.MediaMetadata.MediaStatus.MediaGenerationStatus
			log.Printf("[Client] Media %s status: %s", item.Name[:min(8, len(item.Name))], status)

			if strings.Contains(status, "FAILED") || strings.Contains(status, "BLOCKED") {
				reason := item.MediaMetadata.MediaStatus.FailureReason
				if reason == "" {
					reason = item.MediaMetadata.MediaStatus.ErrorMessage
				}
				return nil, fmt.Errorf("video generation failed for %s: %s", item.Name, reason)
			}

			if status == "MEDIA_GENERATION_STATUS_SUCCESSFUL" || status == "MEDIA_GENERATION_STATUS_COMPLETE" {
				completedIDs = append(completedIDs, item.Name)
			} else {
				allDone = false
			}
		}

		if allDone && len(completedIDs) > 0 {
			var assets []models.Asset
			for _, id := range completedIDs {
				videoURL := ""
				// Try signed media URL via bridge
				u, err := c.bridge.RequestMediaURL(ctx, id)
				if err == nil && u != "" {
					videoURL = u
				}

				// Fallback to flowMedia lookup
				if videoURL == "" {
					detailEndpoint := fmt.Sprintf("%s%s?key=%s", config.APIBase, fmt.Sprintf(config.EndpointGetFlowMedia, id), config.APIKey)
					detailResp, err := c.bridge.ExecuteAPIRequest(ctx, detailEndpoint, nil, "", "GET", nil)
					if err == nil && detailResp.Status == 200 {
						detailBytes, _ := json.Marshal(detailResp.Data)
						var detail models.FlowMediaResponse
						_ = json.Unmarshal(detailBytes, &detail)
						videoURL = detail.Video.GeneratedVideo.FifeURL
					}
				}

				assets = append(assets, models.Asset{
					ID:       id,
					Type:     "video",
					URL:      videoURL,
					MimeType: "video/mp4",
				})
			}
			return assets, nil
		}

		time.Sleep(8 * time.Second)
	}

	return nil, fmt.Errorf("video rendering timed out after %v", timeout)
}

// UpsampleVideo submits an upsampling pass (1080p or 4K).
func (c *FlowClient) UpsampleVideo(
	ctx context.Context,
	mediaID string,
	aspect string,
	resolution string,
	seed int64,
) ([]string, error) {
	aspectVal, ok := config.VideoAspectMap[strings.ToLower(aspect)]
	if !ok {
		aspectVal = "VIDEO_ASPECT_RATIO_LANDSCAPE"
	}

	modelKey := config.VideoUpsampler1080p
	resEnum := config.Resolution1080pEnum

	if strings.ToLower(resolution) == "4k" {
		modelKey = config.VideoUpsampler4k
		resEnum = config.Resolution4kEnum
	}

	if seed == 0 {
		seed = time.Now().UnixNano() % 1000000
	}

	clientCtx := c.buildClientContext()
	reqItem := models.VideoRequestItem{
		AspectRatio:   aspectVal,
		VideoModelKey: modelKey,
		Resolution:    resEnum,
		Seed:          seed % 4294967296,
		VideoInput:    &models.VideoMediaRef{MediaID: mediaID},
		Metadata:      map[string]any{},
	}

	endpoint := fmt.Sprintf("%s%s?key=%s", config.APIBase, config.EndpointUpsampleVideo, config.APIKey)

	payload := models.BatchGenerateVideoRequest{
		MediaGenerationContext: &models.MediaGenerationContext{
			BatchID: randomUUID(),
		},
		ClientContext: clientCtx,
		Requests:      []models.VideoRequestItem{reqItem},
	}

	log.Printf("[Client] Submitting upsample for %s to %s", mediaID[:min(8, len(mediaID))], resolution)
	resp, err := c.bridge.ExecuteAPIRequest(ctx, endpoint, payload, "VIDEO_GENERATION", "POST", nil)
	if err != nil {
		return nil, fmt.Errorf("upsample error: %w", err)
	}

	if resp.Status != 200 {
		return nil, fmt.Errorf("upsample API error (status %d): %v", resp.Status, resp.Data)
	}

	dataBytes, _ := json.Marshal(resp.Data)
	var opResp models.VideoOperationResponse
	_ = json.Unmarshal(dataBytes, &opResp)

	var mediaIDs []string
	for _, m := range opResp.Media {
		if m.Name != "" {
			mediaIDs = append(mediaIDs, m.Name)
		}
	}
	return mediaIDs, nil
}

// UploadImage uploads a local image to Google Flow for reference/I2V use.
func (c *FlowClient) UploadImage(ctx context.Context, imagePath string) (string, error) {
	data, err := os.ReadFile(imagePath)
	if err != nil {
		return "", err
	}

	b64 := base64.StdEncoding.EncodeToString(data)
	var req models.UploadImageRequest
	req.ClientContext.Tool = config.ToolName
	req.ClientContext.ProjectID = c.cfg.ProjectID
	req.ImageBytes = b64

	endpoint := fmt.Sprintf("%s%s?key=%s", config.APIBase, config.EndpointUploadImage, config.APIKey)

	resp, err := c.bridge.ExecuteAPIRequest(ctx, endpoint, req, "", "POST", nil)
	if err != nil {
		return "", fmt.Errorf("upload image error: %w", err)
	}

	if resp.Status != 200 {
		return "", fmt.Errorf("upload image API error (status %d): %v", resp.Status, resp.Data)
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
		return "", fmt.Errorf("could not extract mediaId from response: %v", resp.Data)
	}

	log.Printf("[Client] Image uploaded successfully, mediaId: %s", mediaID)
	return mediaID, nil
}

// GetCredits retrieves remaining credits from /v1/credits.
func (c *FlowClient) GetCredits(ctx context.Context) (any, error) {
	endpoint := fmt.Sprintf("%s%s?key=%s", config.APIBase, config.EndpointCredits, config.APIKey)
	resp, err := c.bridge.ExecuteAPIRequest(ctx, endpoint, nil, "", "GET", nil)
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
