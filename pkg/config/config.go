package config

import (
	"os"
	"path/filepath"
	"strconv"
)

// Default configuration constants for Google Flow APIs.
const (
	APIBase          = "https://aisandbox-pa.googleapis.com"
	APIKey           = "AIzaSyBtrm0o5ab1c-Ec8ZuLcGt3oJAA5VWt3pY"
	RecaptchaSiteKey = "6LdsFiUsAAAAAIjVDZcuLhaHiDn5nnHVXVRQGeMV"
	ToolName         = "PINHOLE"
	DefaultProjectID = "0143adf4-5864-4cb4-abb5-fe4254ad0dc7"
	OriginURL        = "https://labs.google"
	FlowURL          = "https://labs.google/fx/tools/flow"
	LabsAPIBase      = "https://labs.google/fx/api"
)

// Endpoints for aisandbox-pa.googleapis.com
const (
	EndpointBatchGenerateImages = "/v1/projects/%s/flowMedia:batchGenerateImages"
	EndpointGenerateVideoText   = "/v1/video:batchAsyncGenerateVideoText"
	EndpointGenerateVideoStart  = "/v1/video:batchAsyncGenerateVideoStartImage"
	EndpointGenerateVideoFL     = "/v1/video:batchAsyncGenerateVideoStartAndEndImage"
	EndpointGenerateVideoRef    = "/v1/video:batchAsyncGenerateVideoReferenceImages"
	EndpointGenerateVideoEdit   = "/v1/video:batchAsyncGenerateVideoEditVideo"
	EndpointUpsampleVideo       = "/v1/video:batchAsyncGenerateVideoUpsampleVideo"
	EndpointPollVideoStatus     = "/v1/video:batchCheckAsyncVideoGenerationStatus"
	EndpointGetMedia            = "/v1/media/%s"
	EndpointGetFlowMedia        = "/v1/flowMedia/%s"
	EndpointUploadImage         = "/v1/flow/uploadImage"
	EndpointCredits             = "/v1/credits"
)

// Image model identifiers
const (
	ImageModelDefault    = "NARWHAL"    // Imagen 4 / Nano Banana 2
	ImageModelLite       = "HARBOR_SEAL"// Nano Banana 2 Lite
	ImageModelPro        = "GEM_PIX_2"  // Nano Banana Pro
)

// Video model identifiers
const (
	VideoModelDefault = "abra_t2v_10s"
	VideoModelUltra   = "veo_3_1_t2v_fast_ultra"
)

// Video upsampler models
const (
	VideoUpsampler1080p = "veo_3_1_upsampler_1080p"
	VideoUpsampler4k    = "veo_3_1_upsampler_4k"
)

// Resolution enums
const (
	Resolution1080pEnum = "VIDEO_RESOLUTION_1080P"
	Resolution4kEnum    = "VIDEO_RESOLUTION_4K"
)

// Aspect ratio mappings for images
var ImageAspectMap = map[string]string{
	"landscape": "IMAGE_ASPECT_RATIO_LANDSCAPE",
	"16:9":      "IMAGE_ASPECT_RATIO_LANDSCAPE",
	"square":    "IMAGE_ASPECT_RATIO_SQUARE",
	"1:1":       "IMAGE_ASPECT_RATIO_SQUARE",
	"portrait":  "IMAGE_ASPECT_RATIO_PORTRAIT",
	"9:16":      "IMAGE_ASPECT_RATIO_PORTRAIT",
	"4:3":       "IMAGE_ASPECT_RATIO_4_3",
	"4x3":       "IMAGE_ASPECT_RATIO_4_3",
	"3:4":       "IMAGE_ASPECT_RATIO_3_4",
	"3x4":       "IMAGE_ASPECT_RATIO_3_4",
}

// Aspect ratio mappings for videos
var VideoAspectMap = map[string]string{
	"landscape": "VIDEO_ASPECT_RATIO_LANDSCAPE",
	"16:9":      "VIDEO_ASPECT_RATIO_LANDSCAPE",
	"square":    "VIDEO_ASPECT_RATIO_SQUARE",
	"1:1":       "VIDEO_ASPECT_RATIO_SQUARE",
	"portrait":  "VIDEO_ASPECT_RATIO_PORTRAIT",
	"9:16":      "VIDEO_ASPECT_RATIO_PORTRAIT",
}

// Config represents runtime configuration
type Config struct {
	Host             string
	Port             int
	OutputDir        string
	ProjectID        string
	DefaultImageModel string
	CDPPort          int
	Debug            bool
}

// LoadConfig loads configuration from environment variables with defaults.
func LoadConfig() *Config {
	host := getEnv("FLOW_HOST", "127.0.0.1")
	portStr := getEnv("FLOW_PORT", "8001")
	port, err := strconv.Atoi(portStr)
	if err != nil {
		port = 8001
	}

	outDir := getEnv("FLOW_OUTPUT_DIR", "")
	if outDir == "" {
		cwd, err := os.Getwd()
		if err == nil {
			outDir = filepath.Join(cwd, "output")
		} else {
			outDir = "./output"
		}
	}

	cdpPortStr := getEnv("FLOW_CDP_PORT", "9222")
	cdpPort, err := strconv.Atoi(cdpPortStr)
	if err != nil {
		cdpPort = 9222
	}

	return &Config{
		Host:              host,
		Port:              port,
		OutputDir:         outDir,
		ProjectID:         getEnv("DEFAULT_PROJECT", DefaultProjectID),
		DefaultImageModel: getEnv("IMAGE_MODEL", ImageModelDefault),
		CDPPort:           cdpPort,
		Debug:             os.Getenv("FLOW_DEBUG") == "true" || os.Getenv("FLOW_DEBUG") == "1",
	}
}

func getEnv(key, defaultVal string) string {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	return val
}
