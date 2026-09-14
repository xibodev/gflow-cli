package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/xibodev/gflow-cli/pkg/cdp"
	"github.com/xibodev/gflow-cli/pkg/models"
)

// GenerateVideoCDP submits a video generation job headlessly via Chrome/CDP
// on port 9223, polling until completion and saving the downloaded video file to outDir.
func GenerateVideoCDP(ctx context.Context, prompt string, outDir string) (string, error) {
	if strings.TrimSpace(prompt) == "" {
		return "", fmt.Errorf("%w: prompt is required", models.ErrValidation)
	}

	cdpPort := 9223
	target, err := cdp.FindGeminiTarget(cdpPort)
	var headlessCmd *exec.Cmd
	var tempUserDataDir string

	if err != nil {
		// Launch headless Chromium/Chrome/Edge in the background
		browserExe := findHeadlessBrowser()
		if browserExe != "" {
			tempUserDataDir, _ = os.MkdirTemp("", "gflow-gemini-headless-*")
			cmd := exec.Command(browserExe,
				"--headless=new",
				fmt.Sprintf("--remote-debugging-port=%d", cdpPort),
				"--user-data-dir="+tempUserDataDir,
				"--disable-gpu",
				"--no-first-run",
				"--no-default-browser-check",
			)
			cmd.Stdout = nil
			cmd.Stderr = nil
			if err := cmd.Start(); err == nil {
				headlessCmd = cmd
			}
		}

		if headlessCmd == nil {
			// Fallback to desktop app if headless browser is not found
			exePath, err := FindGeminiExecutable()
			if err != nil {
				return "", fmt.Errorf("no browser or Gemini app found: %w", err)
			}
			cmd := exec.Command(exePath, fmt.Sprintf("--remote-debugging-port=%d", cdpPort))
			cmd.Stdout = nil
			cmd.Stderr = nil
			if err := cmd.Start(); err != nil {
				return "", fmt.Errorf("failed to launch Gemini background process: %w", err)
			}
		}

		deadline := time.Now().Add(6 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(300 * time.Millisecond)
			target, err = cdp.FindTargetByPattern(cdpPort, "")
			if err == nil && target != nil && target.WebSocketDebuggerURL != "" {
				break
			}
		}
	}

	defer func() {
		if headlessCmd != nil && headlessCmd.Process != nil {
			_ = headlessCmd.Process.Kill()
		}
		if tempUserDataDir != "" {
			_ = os.RemoveAll(tempUserDataDir)
		}
	}()

	if target == nil || target.WebSocketDebuggerURL == "" {
		return "", fmt.Errorf("could not connect to background browser over CDP on port %d", cdpPort)
	}

	client, err := cdp.Connect(ctx, target.WebSocketDebuggerURL)
	if err != nil {
		return "", fmt.Errorf("failed to attach CDP to background session: %w", err)
	}
	defer client.Close()

	if outDir == "" {
		outDir = "./output"
	}
	absOutDir, err := filepath.Abs(outDir)
	if err != nil {
		absOutDir = outDir
	}
	_ = os.MkdirAll(absOutDir, 0755)

	_, _ = client.CallMethod(ctx, "Browser.setDownloadBehavior", map[string]any{
		"behavior":     "allow",
		"downloadPath": absOutDir,
	})

	// Inject session cookies if running via fresh headless browser
	if headlessCmd != nil {
		sess, err := LoadSession()
		if err == nil && sess != nil && len(sess.Cookies) > 0 {
			var cdpCookies []map[string]any
			for k, v := range sess.Cookies {
				cdpCookies = append(cdpCookies, map[string]any{
					"name":   k,
					"value":  v,
					"domain": ".google.com",
					"path":   "/",
					"secure": true,
				})
			}
			_, _ = client.CallMethod(ctx, "Network.setCookies", map[string]any{"cookies": cdpCookies})
		}
	}

	existingFiles := make(map[string]bool)
	if entries, err := os.ReadDir(absOutDir); err == nil {
		for _, e := range entries {
			existingFiles[e.Name()] = true
		}
	}

	reqPrompt := prompt
	if !strings.HasPrefix(strings.ToLower(reqPrompt), "generate a video") &&
		!strings.HasPrefix(strings.ToLower(reqPrompt), "create a video") &&
		!strings.HasPrefix(strings.ToLower(reqPrompt), "generate a short video") {
		reqPrompt = "Generate a short video: " + prompt
	}
	promptJSON, _ := json.Marshal(reqPrompt)

	_, _ = client.CallMethod(ctx, "Page.navigate", map[string]any{
		"url": "https://gemini.google.com/app",
	})
	time.Sleep(3 * time.Second)

	submitJS := fmt.Sprintf(`
	(async () => {
		for (let i = 0; i < 20; i++) {
			const el = document.querySelector('rich-textarea div[contenteditable="true"]');
			if (el) {
				el.focus();
				document.execCommand('selectAll', false, null);
				document.execCommand('insertText', false, %s);
				el.dispatchEvent(new Event('input', { bubbles: true }));
				await new Promise(r => setTimeout(r, 600));
				
				const btn = document.querySelector('button[aria-label="Send message"], button[aria-label*="Send"], button[aria-label*="Enviar"]');
				if (btn && !btn.disabled) {
					btn.click();
					return true;
				}
				const btns = Array.from(document.querySelectorAll('input-area button, .input-area button'));
				const last = btns[btns.length - 1];
				if (last && !last.disabled) {
					last.click();
					return true;
				}
			}
			await new Promise(r => setTimeout(r, 500));
		}
		return false;
	})()`, string(promptJSON))

	subRes, err := client.Evaluate(ctx, submitJS)
	if err != nil || subRes != true {
		return "", fmt.Errorf("failed submitting prompt to Gemini UI: %v", err)
	}

	pollJS := `
	(() => {
		const video = document.querySelector('video');
		const dl = document.querySelector('button[aria-label="Download video"], button[aria-label*="Download"], button[aria-label*="Transferir"]');
		const resp = document.querySelector('model-response:last-of-type');
		const progress = document.querySelector('mat-progress-spinner, mat-progress-bar, [role="progressbar"]');
		const text = resp ? resp.innerText : '';
		return {
			hasVideo: !!video,
			hasDownload: !!dl,
			hasProgress: !!progress,
			text: text.slice(0, 300)
		};
	})()`

	deadline := time.Now().Add(5 * time.Minute)
	downloadTriggered := false

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		time.Sleep(4 * time.Second)
		cres, err := client.Evaluate(ctx, pollJS)
		if err != nil {
			continue
		}
		data, _ := json.Marshal(cres)
		var state struct {
			HasVideo    bool   `json:"hasVideo"`
			HasDownload bool   `json:"hasDownload"`
			HasProgress bool   `json:"hasProgress"`
			Text        string `json:"text"`
		}
		_ = json.Unmarshal(data, &state)

		if strings.Contains(strings.ToLower(state.Text), "couldn't") || strings.Contains(strings.ToLower(state.Text), "não consegui") || strings.Contains(strings.ToLower(state.Text), "resets") || strings.Contains(strings.ToLower(state.Text), "limit") {
			return "", fmt.Errorf("gemini rejected video generation: %s", state.Text)
		}

		if state.HasDownload || (state.HasVideo && !state.HasProgress) {
			_, _ = client.Evaluate(ctx, `
				const b = document.querySelector('button[aria-label="Download video"], button[aria-label*="Download"], button[aria-label*="Transferir"]');
				if (b) b.click();
			`)
			downloadTriggered = true
			break
		}
	}

	if !downloadTriggered {
		return "", errors.New("timed out waiting for Gemini video generation to complete")
	}

	dlDeadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(dlDeadline) {
		time.Sleep(1 * time.Second)
		if entries, err := os.ReadDir(absOutDir); err == nil {
			for _, e := range entries {
				name := e.Name()
				if existingFiles[name] {
					continue
				}
				if strings.HasSuffix(name, ".crdownload") || strings.HasSuffix(name, ".tmp") {
					continue
				}
				if strings.HasSuffix(name, ".mp4") {
					fullPath := filepath.Join(absOutDir, name)
					if info, err := os.Stat(fullPath); err == nil && info.Size() > 1000 {
						return fullPath, nil
					}
				}
			}
		}
	}

	return "", errors.New("downloaded video file not found in output directory")
}

func findHeadlessBrowser() string {
	var candidates []string
	switch runtime.GOOS {
	case "windows":
		candidates = []string{
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		}
	case "darwin":
		candidates = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		}
	default:
		candidates = []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"}
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	return ""
}
