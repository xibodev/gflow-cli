package models

import (
	"errors"
	"fmt"
	"strings"
)

// Single source for product/protocol identity exposed by the daemon.
const (
	AppName               = "gflow"
	AppVersion            = "1.0.0"
	ProtocolVersion       = "2024-11-05"
	ProductIdentity       = "gflow"
	MaxSeed         int64 = 4294967295
)

// Typed errors for the local daemon boundary. Use errors.Is/As to branch.
var (
	ErrValidation = errors.New("validation error")
	ErrUpstream   = errors.New("upstream error")
	ErrAuth       = errors.New("authentication error")
	ErrTerminal   = errors.New("terminal generation failure")
	ErrNotFound   = errors.New("not found")
)

// APIError is the structured error payload returned by the local daemon.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }

// ImageRequest is the local daemon contract for image generation. It preserves
// the OpenAI-compatible fields while carrying gflow extensions explicitly.
type ImageRequest struct {
	Prompt            string   `json:"prompt"`
	N                 int      `json:"n"`
	Size              string   `json:"size,omitempty"`
	Model             string   `json:"model,omitempty"`
	ResponseFormat    string   `json:"response_format,omitempty"`
	Aspect            string   `json:"aspect,omitempty"`
	ReferenceMediaIDs []string `json:"reference_media_ids,omitempty"`
	Seed              *int64   `json:"seed,omitempty"`
}

// VideoSubmitRequest is the local daemon contract for submitting a video job.
// Resolution is intentionally validated at this layer: only native 720p (or
// omitted) may be submitted here. Upsampling is a separate operation.
type VideoSubmitRequest struct {
	Prompt     string `json:"prompt"`
	Aspect     string `json:"aspect,omitempty"`
	Duration   int    `json:"duration,omitempty"`
	StartImage string `json:"start_image,omitempty"`
	EndImage   string `json:"end_image,omitempty"`
	Seed       *int64 `json:"seed,omitempty"`
	Resolution string `json:"resolution,omitempty"`
}

// JobSubmission is returned when a video/upsample job is accepted.
type JobSubmission struct {
	JobID    string   `json:"job_id"`
	MediaIDs []string `json:"media_ids"`
	Status   string   `json:"status"`
	Created  int64    `json:"created"`
}

// JobStatus is returned by single-shot status checks.
type JobStatus struct {
	JobID  string    `json:"job_id"`
	Status string    `json:"status"` // processing|succeeded|failed
	Assets []Asset   `json:"assets,omitempty"`
	Error  *APIError `json:"error,omitempty"`
}

// ValidateSeed checks presence semantics without collapsing explicit zero.
func ValidateSeed(seed *int64) (int64, bool, error) {
	if seed == nil {
		return 0, false, nil
	}
	if *seed < 0 || *seed > MaxSeed {
		return 0, false, fmt.Errorf("%w: seed must be in [0,%d]", ErrValidation, MaxSeed)
	}
	return *seed, true, nil
}

// ValidateImageRequest enforces daemon-side contracts before any dispatch.
func ValidateImageRequest(req *ImageRequest) error {
	if req == nil {
		return fmt.Errorf("%w: nil image request", ErrValidation)
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return fmt.Errorf("%w: prompt is required", ErrValidation)
	}
	if req.N < 1 || req.N > 4 {
		return fmt.Errorf("%w: n must be in [1,4]", ErrValidation)
	}
	if _, _, err := ValidateSeed(req.Seed); err != nil {
		return err
	}
	if req.ResponseFormat != "" && req.ResponseFormat != "url" && req.ResponseFormat != "b64_json" {
		return fmt.Errorf("%w: response_format must be url or b64_json", ErrValidation)
	}
	return nil
}

// ValidateVideoSubmit enforces daemon-side contracts before any dispatch.
func ValidateVideoSubmit(req *VideoSubmitRequest) error {
	if req == nil {
		return fmt.Errorf("%w: nil video request", ErrValidation)
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return fmt.Errorf("%w: prompt is required", ErrValidation)
	}
	if req.Duration != 0 && req.Duration != 4 && req.Duration != 6 && req.Duration != 8 && req.Duration != 10 {
		return fmt.Errorf("%w: duration must be 4, 6, 8, or 10", ErrValidation)
	}
	if req.EndImage != "" && req.StartImage == "" {
		return fmt.Errorf("%w: end_image requires start_image", ErrValidation)
	}
	if _, _, err := ValidateSeed(req.Seed); err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(req.Resolution)) {
	case "", "720p", "native":
		// accepted at submit time
	case "1080p", "4k":
		return fmt.Errorf("%w: resolution %q requires the upsample operation after native generation completes", ErrValidation, req.Resolution)
	default:
		return fmt.Errorf("%w: unknown resolution %q", ErrValidation, req.Resolution)
	}
	return nil
}
