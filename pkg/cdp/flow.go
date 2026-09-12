package cdp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"

	"github.com/xibodev/gflow-cli/pkg/models"
)

// FlowBridge executes Google Flow API requests and reCAPTCHA tokens directly
// through an open browser tab via Chrome DevTools Protocol (CDP).
// It eliminates the need for any Chrome extension or localhost daemon.
type FlowBridge struct {
	port   int
	client *Client
	target *Target
	mu     sync.Mutex
}

// NewFlowBridge creates a new extension-free Flow bridge for port (default 9222).
func NewFlowBridge(port int) *FlowBridge {
	if port <= 0 {
		port = 9222
	}
	return &FlowBridge{port: port}
}

// EnsureConnected ensures a live CDP connection to the Google Flow tab.
func (b *FlowBridge) EnsureConnected(ctx context.Context) (*Client, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.client != nil {
		// Ping to verify alive
		_, err := b.client.Evaluate(ctx, "1+1")
		if err == nil {
			return b.client, nil
		}
		_ = b.client.Close()
		b.client = nil
	}

	target, err := FindFlowTarget(b.port)
	if err != nil {
		return nil, fmt.Errorf("no Google Flow tab found on port %d: %w", b.port, err)
	}

	client, err := Connect(ctx, target.WebSocketDebuggerURL)
	if err != nil {
		return nil, fmt.Errorf("failed connecting to Flow tab via CDP: %w", err)
	}

	b.target = target
	b.client = client
	return b.client, nil
}

// Close closes the underlying CDP connection.
func (b *FlowBridge) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.client != nil {
		err := b.client.Close()
		b.client = nil
		return err
	}
	return nil
}

// ExecuteAPIRequest implements client.RequestExecutor without any browser extension.
func (b *FlowBridge) ExecuteAPIRequest(ctx context.Context, urlPath string, body any, captchaAction string, method string, headers map[string]string) (*models.ExtensionCallback, error) {
	cli, err := b.EnsureConnected(ctx)
	if err != nil {
		return nil, err
	}

	finalBody := body

	// Step 1: Solve reCAPTCHA Enterprise directly in page context if needed
	if captchaAction != "" {
		token, err := cli.GetRecaptchaToken(ctx, captchaAction)
		if err != nil {
			return nil, fmt.Errorf("failed to get reCAPTCHA token via CDP: %w", err)
		}

		if finalBody != nil {
			data, err := json.Marshal(finalBody)
			if err == nil {
				var rawMap map[string]any
				if err := json.Unmarshal(data, &rawMap); err == nil {
					injectRecaptchaToken(rawMap, token)
					finalBody = rawMap
				}
			}
		}
	}

	bodyJSON := ""
	if finalBody != nil {
		if s, ok := finalBody.(string); ok {
			bodyJSON = s
		} else {
			b, err := json.Marshal(finalBody)
			if err != nil {
				return nil, err
			}
			bodyJSON = string(b)
		}
	}

	if headers == nil {
		headers = make(map[string]string)
	}
	if headers["Content-Type"] == "" {
		headers["Content-Type"] = "text/plain;charset=UTF-8"
	}
	headers["Accept"] = "*/*"

	// Step 2: Run fetch directly inside the authenticated browser tab
	status, respBody, err := cli.FetchInPage(ctx, urlPath, method, headers, bodyJSON)
	if err != nil {
		return nil, fmt.Errorf("in-page fetch error: %w", err)
	}

	var parsedData any
	if err := json.Unmarshal([]byte(respBody), &parsedData); err != nil {
		parsedData = respBody
	}

	return &models.ExtensionCallback{
		Status: status,
		Data:   parsedData,
	}, nil
}

// RequestMediaURL resolves a signed media redirect URL via the page context.
func (b *FlowBridge) RequestMediaURL(ctx context.Context, mediaID string) (string, error) {
	cli, err := b.EnsureConnected(ctx)
	if err != nil {
		return "", err
	}

	reqURL := fmt.Sprintf("https://labs.google/fx/api/trpc/media.getMediaUrlRedirect?name=%s", url.QueryEscape(mediaID))
	js := fmt.Sprintf(`
		new Promise(async (resolve, reject) => {
			try {
				const res = await fetch('%s', { credentials: 'include', redirect: 'follow' });
				resolve(res.url);
			} catch (e) {
				reject(e);
			}
		})
	`, reqURL)

	val, err := cli.Evaluate(ctx, js)
	if err != nil {
		return "", err
	}

	u, ok := val.(string)
	if !ok || u == "" {
		return "", fmt.Errorf("invalid media redirect URL returned: %v", val)
	}
	return u, nil
}

func injectRecaptchaToken(obj map[string]any, token string) {
	if clientCtx, ok := obj["clientContext"].(map[string]any); ok {
		if recap, ok := clientCtx["recaptchaContext"].(map[string]any); ok {
			recap["token"] = token
		} else {
			clientCtx["recaptchaContext"] = map[string]any{
				"token":           token,
				"applicationType": "RECAPTCHA_APPLICATION_TYPE_WEB",
			}
		}
	}
	if reqs, ok := obj["requests"].([]any); ok {
		for _, r := range reqs {
			if rMap, ok := r.(map[string]any); ok {
				injectRecaptchaToken(rMap, token)
			}
		}
	}
}
