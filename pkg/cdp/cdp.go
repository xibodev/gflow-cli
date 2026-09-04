package cdp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// Target represents a Chrome target from /json/list.
type Target struct {
	ID                   string `json:"id"`
	Title                string `json:"title"`
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

// Client is a lightweight pure Go CDP client.
type Client struct {
	ws     *websocket.Conn
	msgID  int64
	mu     sync.Mutex
	closed chan struct{}
}

// FindFlowTarget finds an existing Google Flow page or any usable page on port.
func FindFlowTarget(port int) (*Target, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d/json/list", port)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("could not connect to Chrome on port %d: %w", port, err)
	}
	defer resp.Body.Close()

	var targets []Target
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return nil, err
	}

	var fallback *Target
	for i := range targets {
		t := &targets[i]
		if t.Type == "page" {
			if strings.Contains(t.URL, "labs.google/fx/tools/flow") {
				return t, nil
			}
			if fallback == nil && t.WebSocketDebuggerURL != "" {
				fallback = t
			}
		}
	}

	if fallback != nil {
		return fallback, nil
	}
	return nil, errors.New("no usable page target found in Chrome")
}

// Connect connects to Chrome via its webSocketDebuggerUrl.
func Connect(ctx context.Context, wsURL string) (*Client, error) {
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("CDP dial failed: %w", err)
	}

	return &Client{
		ws:     conn,
		closed: make(chan struct{}),
	}, nil
}

// Close closes the CDP WebSocket connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ws.Close()
}

// Evaluate evaluates a JavaScript expression in the tab and returns the value as a string.
func (c *Client) Evaluate(ctx context.Context, expression string) (any, error) {
	id := atomic.AddInt64(&c.msgID, 1)

	payload := map[string]any{
		"id":     id,
		"method": "Runtime.evaluate",
		"params": map[string]any{
			"expression":    expression,
			"awaitPromise":  true,
			"returnByValue": true,
		},
	}

	c.mu.Lock()
	err := c.ws.WriteJSON(payload)
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		var raw map[string]any
		_ = c.ws.SetReadDeadline(time.Now().Add(30 * time.Second))
		if err := c.ws.ReadJSON(&raw); err != nil {
			return nil, err
		}

		respID, ok := raw["id"].(float64)
		if ok && int64(respID) == id {
			if errVal, hasErr := raw["error"]; hasErr {
				return nil, fmt.Errorf("CDP error: %v", errVal)
			}
			result, _ := raw["result"].(map[string]any)
			innerResult, _ := result["result"].(map[string]any)
			if subtype, _ := innerResult["subtype"].(string); subtype == "error" {
				return nil, fmt.Errorf("JS error: %v", innerResult["description"])
			}
			return innerResult["value"], nil
		}
	}
}

// GetRecaptchaToken requests a fresh reCAPTCHA Enterprise token from the Flow page.
func (c *Client) GetRecaptchaToken(ctx context.Context, action string) (string, error) {
	const siteKey = "6LdsFiUsAAAAAIjVDZcuLhaHiDn5nnHVXVRQGeMV"
	js := fmt.Sprintf(`
		new Promise((resolve, reject) => {
			if (typeof grecaptcha === 'undefined' || !grecaptcha.enterprise) {
				return reject(new Error('grecaptcha not available on page'));
			}
			grecaptcha.enterprise.execute('%s', {action: '%s'})
				.then(token => resolve(token))
				.catch(err => reject(err));
		})
	`, siteKey, action)

	val, err := c.Evaluate(ctx, js)
	if err != nil {
		return "", err
	}
	token, ok := val.(string)
	if !ok || len(token) < 50 {
		return "", fmt.Errorf("invalid reCAPTCHA token returned: %v", val)
	}
	return token, nil
}

// FetchInPage runs a fetch request directly from the page context.
func (c *Client) FetchInPage(ctx context.Context, url string, method string, headers map[string]string, body string) (int, string, error) {
	headersJSON, _ := json.Marshal(headers)
	bodyEscaped, _ := json.Marshal(body)

	js := fmt.Sprintf(`
		new Promise(async (resolve) => {
			try {
				const opts = {
					method: '%s',
					headers: %s,
					credentials: 'include'
				};
				if ('%s' !== 'GET' && '%s' !== 'HEAD') {
					opts.body = %s;
				}
				const res = await fetch('%s', opts);
				const text = await res.text();
				resolve(JSON.stringify({ status: res.status, body: text }));
			} catch (e) {
				resolve(JSON.stringify({ status: 0, error: e.message }));
			}
		})
	`, method, string(headersJSON), method, method, string(bodyEscaped), url)

	val, err := c.Evaluate(ctx, js)
	if err != nil {
		return 0, "", err
	}

	strVal, ok := val.(string)
	if !ok {
		return 0, "", fmt.Errorf("unexpected evaluation result: %v", val)
	}

	var res struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal([]byte(strVal), &res); err != nil {
		return 0, "", err
	}
	if res.Error != "" {
		return 0, "", errors.New(res.Error)
	}
	return res.Status, res.Body, nil
}
