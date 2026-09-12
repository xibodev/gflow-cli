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

// MaxDownloadBytes bounds a single media download (4K video safe upper bound).
const MaxDownloadBytes = int64(512 << 20)

// SniffMediaType detects whether the bytes or path represent PNG, JPEG, WebP, or MP4.
func SniffMediaType(data []byte) string {
	if len(data) >= 8 {
		if bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}) {
			return "image/png"
		}
		if bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}) {
			return "image/jpeg"
		}
		if len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
			return "image/webp"
		}
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

func extFor(assetType, mime string) string {
	ext := ExtensionForMime(mime)
	if ext == ".bin" {
		if assetType == "video" {
			return ".mp4"
		}
		return ".png"
	}
	return ext
}

// ResolveOutputPath maps a user --output value to an exact file path.
// Semantics: existing directory or trailing separator means directory; an
// explicit filename (e.g. clip.mp4) is honored for single assets; multiple
// assets with a filename produce deterministic suffixed siblings.
func ResolveOutputPath(output, assetType, assetID, mime string, index, total int) (string, error) {
	ext := extFor(assetType, mime)
	generated := func(dir string) string {
		ts := time.Now().Format("20060102_150405")
		short := assetID
		if len(short) > 8 {
			short = short[:8]
		}
		if short == "" {
			short = "asset"
		}
		name := fmt.Sprintf("%s_%s_%s%s", assetType, ts, short, ext)
		if total > 1 {
			stem := strings.TrimSuffix(name, ext)
			name = fmt.Sprintf("%s_%d%s", stem, index+1, ext)
		}
		return filepath.Join(dir, name)
	}
	if strings.TrimSpace(output) == "" {
		return generated("."), nil
	}
	if isDir(output) {
		if err := os.MkdirAll(output, 0755); err != nil {
			return "", err
		}
		return generated(output), nil
	}
	if info, err := os.Stat(output); err == nil && info.IsDir() {
		return generated(output), nil
	}
	// Non-existent path without an extension is treated as a directory.
	if filepath.Ext(output) == "" {
		if err := os.MkdirAll(output, 0755); err != nil {
			return "", err
		}
		return generated(output), nil
	}
	// Explicit filename.
	if total > 1 {
		dir := filepath.Dir(output)
		if dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return "", err
			}
		}
		extOut := filepath.Ext(output)
		stem := strings.TrimSuffix(filepath.Base(output), extOut)
		if index == 0 {
			return output, nil
		}
		return filepath.Join(dir, fmt.Sprintf("%s_%d%s", stem, index+1, extOut)), nil
	}
	return output, nil
}

// createExclusive creates parent dirs and exclusively creates path (or a
// suffixed sibling). It never overwrites an existing file.
func createExclusive(path string) (*os.File, string, error) {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, "", err
		}
	}
	if f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600); err == nil {
		return f, path, nil
	} else if !errors.Is(err, os.ErrExist) {
		return nil, "", err
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(filepath.Base(path), ext)
	for i := 1; i < 10000; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s_%d%s", base, i, ext))
		if f, err := os.OpenFile(candidate, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600); err == nil {
			return f, candidate, nil
		} else if !errors.Is(err, os.ErrExist) {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("could not allocate unique output path for %s", path)
}

// DownloadFile downloads a URL to disk, bypassing proxies and retrying on network blips.
func DownloadFile(ctx context.Context, url string, targetPath string) error {
	_, err := downloadToPath(ctx, url, targetPath)
	return err
}

func downloadToPath(ctx context.Context, url string, targetPath string) (string, error) {
	f, final, err := createExclusive(targetPath)
	if err != nil {
		return "", err
	}
	cleanup := true
	defer func() {
		_ = f.Close()
		if cleanup {
			_ = os.Remove(final)
		}
	}()
	client := &http.Client{
		Timeout:   90 * time.Second,
		Transport: &http.Transport{Proxy: nil},
	}
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if cerr := ctx.Err(); cerr != nil {
			lastErr = cerr
			break
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			lastErr = err
			break
		}
		_ = f.Truncate(0)
		req, rerr := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if rerr != nil {
			return "", rerr
		}
		req.Header.Set("User-Agent", "Mozilla/5.0")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			select {
			case <-ctx.Done():
				break
			case <-time.After(time.Duration(attempt) * time.Second):
			}
			continue
		}
		err = streamValidated(ctx, resp, f)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			// Retry only transient network conditions, not content rejections.
			if errors.Is(err, errInvalidMedia) {
				break
			}
			select {
			case <-ctx.Done():
				break
			case <-time.After(time.Duration(attempt) * time.Second):
			}
			continue
		}
		cleanup = false
		return final, nil
	}
	return "", fmt.Errorf("failed to download after 3 attempts: %w", lastErr)
}

var errInvalidMedia = errors.New("invalid media payload")

func streamValidated(ctx context.Context, resp *http.Response, f *os.File) error {
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d downloading media", resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, MaxDownloadBytes+1)
	head := make([]byte, 0, 512)
	total := int64(0)
	buf := make([]byte, 32*1024)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		n, rerr := limited.Read(buf)
		if n > 0 {
			if len(head) < 512 {
				head = append(head, buf[:n]...)
				if len(head) > 512 {
					head = head[:512]
				}
			}
			total += int64(n)
			if total > MaxDownloadBytes {
				return fmt.Errorf("download exceeds %d bytes", MaxDownloadBytes)
			}
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	if total == 0 {
		return errors.New("empty download payload")
	}
	if err := f.Sync(); err != nil {
		return err
	}
	mime := SniffMediaType(head)
	if strings.HasPrefix(mime, "text/html") || strings.HasPrefix(mime, "text/plain") {
		return fmt.Errorf("%w: unexpected %s payload", errInvalidMedia, mime)
	}
	return nil
}

// SaveAsset writes an Asset to disk by fetching its URL.
func SaveAsset(ctx context.Context, asset *models.Asset, outputPath string) (string, error) {
	return SaveAssetIndexed(ctx, asset, outputPath, 0, 1)
}

// SaveAssetIndexed saves one of total assets, honoring explicit filenames.
func SaveAssetIndexed(ctx context.Context, asset *models.Asset, outputPath string, index, total int) (string, error) {
	if asset.LocalPath != "" {
		if st, err := os.Stat(asset.LocalPath); err == nil && st.Size() > 0 {
			return asset.LocalPath, nil
		}
	}
	if asset.URL == "" {
		return "", errors.New("asset has no downloadable URL")
	}
	finalBase, err := ResolveOutputPath(outputPath, asset.Type, asset.ID, asset.MimeType, index, total)
	if err != nil {
		return "", err
	}
	actual, err := downloadToPath(ctx, asset.URL, finalBase)
	if err != nil {
		return "", err
	}
	asset.LocalPath = actual
	return actual, nil
}

// SaveResult is the per-asset outcome for centralized save handling.
type SaveResult struct {
	Asset *models.Asset
	Path  string
	Err   error
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
