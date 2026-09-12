package gemini

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xibodev/gflow-cli/pkg/util"
)

// DownloadMedia downloads a generated image or video URL from Google,
// following all 302 redirects with session cookies and validating media bytes.
func (c *Client) DownloadMedia(ctx context.Context, mediaURL string, targetPath string) (string, error) {
	if mediaURL == "" {
		return "", errors.New("empty media URL")
	}

	// Resolve destination path
	dest := targetPath
	if dest == "" || isDir(dest) || filepath.Ext(dest) == "" {
		ext := ".jpg"
		if strings.Contains(mediaURL, "video") || strings.Contains(mediaURL, ".mp4") {
			ext = ".mp4"
		}
		name := fmt.Sprintf("gemini_%s%s", time.Now().Format("20060102_150405"), ext)
		if dest == "" {
			dest = name
		} else {
			dest = filepath.Join(dest, name)
		}
	}

	dir := filepath.Dir(dest)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return "", err
		}
	}

	client := &http.Client{
		Timeout: 5 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			// Retain cookies and headers across Google redirects
			if c.session.CookieStr != "" {
				req.Header.Set("Cookie", c.session.CookieStr)
			}
			req.Header.Set("User-Agent", DefaultUserAgent)
			req.Header.Set("Referer", "https://gemini.google.com/")
			return nil
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mediaURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", DefaultUserAgent)
	if c.session.CookieStr != "" {
		req.Header.Set("Cookie", c.session.CookieStr)
	}
	req.Header.Set("Referer", "https://gemini.google.com/")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("media download request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d downloading media from %s", resp.StatusCode, mediaURL)
	}

	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return "", err
	}
	defer f.Close()

	written, err := io.Copy(f, resp.Body)
	if err != nil {
		_ = os.Remove(dest)
		return "", fmt.Errorf("failed writing downloaded media: %w", err)
	}

	if written == 0 {
		_ = os.Remove(dest)
		return "", errors.New("downloaded empty media payload")
	}

	// Validate magic bytes to ensure not an HTML error
	_ = f.Sync()
	dataHead := make([]byte, 512)
	_, _ = f.ReadAt(dataHead, 0)
	mime := util.SniffMediaType(dataHead)
	if mime == "text/html" || mime == "text/plain" {
		_ = os.Remove(dest)
		return "", fmt.Errorf("downloaded payload is %s, not valid media", mime)
	}

	return dest, nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	if err == nil && info.IsDir() {
		return true
	}
	return len(path) > 0 && (path[len(path)-1] == '/' || path[len(path)-1] == '\\')
}
