package util

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xibodev/gflow-cli/pkg/models"
)

// SniffMediaType detects whether the bytes or path represent PNG, JPEG, WebP, or MP4.
func SniffMediaType(data []byte) string {
	if len(data) >= 8 {
		// PNG magic bytes: \x89PNG\r\n\x1a\n
		if bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}) {
			return "image/png"
		}
		// JPEG magic bytes: \xFF\xD8\xFF
		if bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}) {
			return "image/jpeg"
		}
		// WebP: RIFF....WEBP
		if len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
			return "image/webp"
		}
		// MP4: ....ftyp
		if len(data) >= 12 && string(data[4:8]) == "ftyp" {
			return "video/mp4"
		}
	}
	return http.DetectContentType(data)
}

// ExtensionForMime returns standard file extension for a mime type.
func ExtensionForMime(mime string) string {
	switch {
	case strings.Contains(mime, "png"):
		return ".png"
	case strings.Contains(mime, "jpeg") || strings.Contains(mime, "jpg"):
		return ".jpg"
	case strings.Contains(mime, "webp"):
		return ".webp"
	case strings.Contains(mime, "mp4") || strings.Contains(mime, "video"):
		return ".mp4"
	default:
		return ".bin"
	}
}

// DownloadFile downloads a URL to disk, bypassing proxies and retrying on network blips.
func DownloadFile(ctx context.Context, url string, targetPath string) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return err
	}

	client := &http.Client{
		Timeout: 90 * time.Second,
		Transport: &http.Transport{
			Proxy: nil, // direct connection to GCS signed URLs
		},
	}

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0")

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(time.Duration(attempt) * time.Second)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d downloading media", resp.StatusCode)
			time.Sleep(time.Duration(attempt) * time.Second)
			continue
		}

		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}

		if len(data) == 0 {
			lastErr = errors.New("empty download payload")
			continue
		}

		if err := os.WriteFile(targetPath, data, 0644); err != nil {
			return err
		}
		return nil
	}

	return fmt.Errorf("failed to download after 3 attempts: %w", lastErr)
}

// SaveAsset writes an Asset to disk, either decoding base64 or fetching URL.
func SaveAsset(ctx context.Context, asset *models.Asset, outputPath string) (string, error) {
	finalPath := outputPath

	ext := ExtensionForMime(asset.MimeType)
	if ext == ".bin" {
		if asset.Type == "video" {
			ext = ".mp4"
		} else {
			ext = ".png"
		}
	}

	// If outputPath is empty, has no file extension (treat as directory), or is an existing directory:
	if finalPath == "" || filepath.Ext(finalPath) == "" || isDir(finalPath) {
		ts := time.Now().Format("20060102_150405")
		idShort := asset.ID
		if len(idShort) > 8 {
			idShort = idShort[:8]
		}
		filename := fmt.Sprintf("%s_%s_%s%s", asset.Type, ts, idShort, ext)
		if finalPath == "" {
			finalPath = filename
		} else {
			finalPath = filepath.Join(finalPath, filename)
		}
	}

	// Ensure unique file path so existing generations are never overwritten
	finalPath = uniqueFilePath(finalPath)

	if asset.URL != "" {
		if err := DownloadFile(ctx, asset.URL, finalPath); err != nil {
			return "", err
		}
		asset.LocalPath = finalPath
		return finalPath, nil
	}

	return "", errors.New("asset has no downloadable URL")
}

func uniqueFilePath(path string) string {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return path
	}
	dir := filepath.Dir(path)
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(filepath.Base(path), ext)

	for i := 1; i < 10000; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s_%d%s", base, i, ext))
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate
		}
	}
	return path
}

// DecodeBase64AndSave saves base64 data to targetPath.
func DecodeBase64AndSave(b64Data string, targetPath string) error {
	data, err := base64.StdEncoding.DecodeString(b64Data)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(targetPath, data, 0644)
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	if err == nil && info.IsDir() {
		return true
	}
	return strings.HasSuffix(path, "/") || strings.HasSuffix(path, "\\")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
