package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/xibodev/gflow-cli/pkg/cdp"
)

// Session holds the authenticated cookies and CSRF tokens for Gemini.
type Session struct {
	Cookies   map[string]string `json:"cookies"`
	CookieStr string            `json:"cookie_str"`
	At        string            `json:"at"` // SNlM0e CSRF token
	Bl        string            `json:"bl"` // cfb2h build label
	UpdatedAt time.Time         `json:"updated_at"`
}

func sessionPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".gflow")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "gemini_session.json"), nil
}

// LoadSession loads cached session tokens from ~/.gflow/gemini_session.json.
func LoadSession() (*Session, error) {
	p, err := sessionPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, err
	}
	if sess.At == "" || sess.CookieStr == "" {
		return nil, errors.New("incomplete cached gemini session")
	}
	return &sess, nil
}

// SaveSession saves session tokens to disk.
func SaveSession(sess *Session) error {
	p, err := sessionPath()
	if err != nil {
		return err
	}
	sess.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0600)
}

// FindGeminiExecutable locates the installed Gemini desktop app on Windows.
func FindGeminiExecutable() (string, error) {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return "", errors.New("LOCALAPPDATA environment variable not set")
	}
	base := filepath.Join(localAppData, "Google", "Gemini")
	entries, err := os.ReadDir(base)
	if err != nil {
		return "", fmt.Errorf("could not access Gemini directory %s: %w", base, err)
	}

	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "app-") {
			candidate := filepath.Join(base, e.Name(), "Gemini.exe")
			if _, err := os.Stat(candidate); err == nil {
				return candidate, nil
			}
		}
	}

	direct := filepath.Join(base, "Gemini.exe")
	if _, err := os.Stat(direct); err == nil {
		return direct, nil
	}

	return "", errors.New("Gemini desktop app executable not found in %LOCALAPPDATA%\\Google\\Gemini")
}

// RefreshSession launches Gemini in the background with CDP, captures fresh
// session cookies and WIZ_global_data, then shuts it down and saves the session.
func RefreshSession(ctx context.Context, cdpPort int) (*Session, error) {
	if cdpPort <= 0 {
		cdpPort = 9223
	}

	// 1. Check if already listening on port
	target, err := cdp.FindGeminiTarget(cdpPort)
	startedProcess := false
	var cmd *exec.Cmd

	if err != nil {
		// Not listening, launch Gemini in background with CDP
		exePath, err := FindGeminiExecutable()
		if err != nil {
			return nil, fmt.Errorf("cannot refresh session: %w", err)
		}

		cmd = exec.Command(exePath, fmt.Sprintf("--remote-debugging-port=%d", cdpPort))
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Start(); err != nil {
			return nil, fmt.Errorf("failed to launch Gemini background process: %w", err)
		}
		startedProcess = true

		// Wait up to 5 seconds for CDP to become ready
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(300 * time.Millisecond)
			target, err = cdp.FindGeminiTarget(cdpPort)
			if err == nil && target != nil && target.WebSocketDebuggerURL != "" {
				break
			}
		}
	}

	defer func() {
		if startedProcess && cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}()

	if target == nil || target.WebSocketDebuggerURL == "" {
		return nil, fmt.Errorf("could not connect to Gemini over CDP on port %d", cdpPort)
	}

	// 2. Connect via CDP WebSocket
	client, err := cdp.Connect(ctx, target.WebSocketDebuggerURL)
	if err != nil {
		return nil, fmt.Errorf("failed to attach CDP to Gemini tab: %w", err)
	}
	defer client.Close()

	// 3. Get cookies
	cookies, err := client.GetCookies(ctx, []string{"https://gemini.google.com", "https://googleusercontent.com"})
	if err != nil {
		return nil, fmt.Errorf("failed to read cookies via CDP: %w", err)
	}

	cookieMap := make(map[string]string)
	var pairs []string
	for _, c := range cookies {
		cookieMap[c.Name] = c.Value
		pairs = append(pairs, fmt.Sprintf("%s=%s", c.Name, c.Value))
	}
	cookieStr := strings.Join(pairs, "; ")

	// 4. Extract SNlM0e and cfb2h from page context
	evalRes, err := client.Evaluate(ctx, `
		({
			at: window.WIZ_global_data?.SNlM0e || null,
			bl: window.WIZ_global_data?.cfb2h || null
		})
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate WIZ_global_data: %w", err)
	}

	evalMap, _ := evalRes.(map[string]any)
	at, _ := evalMap["at"].(string)
	bl, _ := evalMap["bl"].(string)

	if at == "" {
		// Fallback: fetch page directly with cookies to extract SNlM0e
		at, bl, _ = extractAtAndBl(ctx, cookieStr)
	}

	if at == "" {
		return nil, errors.New("user is not signed in on Gemini (SNlM0e token missing). Please open Gemini app and log in first")
	}

	sess := &Session{
		Cookies:   cookieMap,
		CookieStr: cookieStr,
		At:        at,
		Bl:        bl,
		UpdatedAt: time.Now(),
	}

	if err := SaveSession(sess); err != nil {
		return nil, fmt.Errorf("failed to save captured session: %w", err)
	}

	return sess, nil
}

// GetOrRefreshSession returns a valid session, refreshing if missing or expired.
func GetOrRefreshSession(ctx context.Context, forceRefresh bool) (*Session, error) {
	if !forceRefresh {
		sess, err := LoadSession()
		if err == nil && sess != nil && time.Since(sess.UpdatedAt) < 18*time.Hour {
			return sess, nil
		}
	}
	return RefreshSession(ctx, 9223)
}

func extractAtAndBl(ctx context.Context, cookieStr string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://gemini.google.com/app", nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36 GeminiWindowsApp/1.10.4")
	req.Header.Set("Cookie", cookieStr)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	var buf [64 * 1024]byte
	n, _ := resp.Body.Read(buf[:])
	s := string(buf[:n])

	at := extractSubstr(s, `"SNlM0e":"`, `"`)
	bl := extractSubstr(s, `"cfb2h":"`, `"`)
	return at, bl, nil
}

func extractSubstr(s, start, end string) string {
	i := strings.Index(s, start)
	if i < 0 {
		return ""
	}
	i += len(start)
	j := strings.Index(s[i:], end)
	if j < 0 {
		return ""
	}
	return s[i : i+j]
}
