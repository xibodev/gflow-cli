package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/xibodev/gflow-cli/pkg/models"
)

const (
	EndpointStreamGenerate = "https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate"
	DefaultUserAgent       = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36 GeminiWindowsApp/1.10.4"
	DefaultBuildLabel      = "boq_assistant-bard-web-server_20260907.07_p3"
)

var (
	videoURLRegex = regexp.MustCompile(`https://contribution\.usercontent\.google\.com/download\?(?:\\+[uU][0-9a-fA-F]{4}|[^\s"'\\])+`)
	imageURLRegex = regexp.MustCompile(`https://lh3\.googleusercontent\.com/gg-dl/(?:\\+[uU][0-9a-fA-F]{4}|[^\s"'\\])+`)
)

// GenerationResult represents media or text returned by Gemini.
type GenerationResult struct {
	Text           string
	ImageURLs      []string
	VideoURL       string
	VideoFormat    string
	ConversationID string
	ResponseID     string
	ChoiceID       string
}

// Client interacts with the Gemini chat and multimodal generation pipeline.
type Client struct {
	session *Session
	http    *http.Client
}

// NewClient creates a new Gemini client, refreshing local desktop tokens if needed.
func NewClient(ctx context.Context, forceRefresh bool) (*Client, error) {
	sess, err := GetOrRefreshSession(ctx, forceRefresh)
	if err != nil {
		return nil, fmt.Errorf("gemini authentication failed: %w", err)
	}

	return &Client{
		session: sess,
		http: &http.Client{
			Timeout: 10 * time.Minute,
		},
	}, nil
}

// Generate sends a prompt to Gemini and returns the parsed generation result.
func (c *Client) Generate(ctx context.Context, prompt string) (*GenerationResult, error) {
	if strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("%w: prompt is required", models.ErrValidation)
	}

	res, err := c.executeStreamGenerate(ctx, prompt)
	if err != nil {
		// If auth expired (401 / session rejected), try refreshing session once
		if errors.Is(err, models.ErrAuth) {
			refreshed, rerr := RefreshSession(ctx, 9223)
			if rerr == nil && refreshed != nil {
				c.session = refreshed
				return c.executeStreamGenerate(ctx, prompt)
			}
		}
		return nil, err
	}

	return res, nil
}

func (c *Client) executeStreamGenerate(ctx context.Context, prompt string) (*GenerationResult, error) {
	bl := c.session.Bl
	if bl == "" {
		bl = DefaultBuildLabel
	}

	reqID := rand.Intn(900000) + 100000
	reqURL := fmt.Sprintf("%s?bl=%s&_reqid=%d&rt=c", EndpointStreamGenerate, url.QueryEscape(bl), reqID)

	innerReq := []any{
		[]any{prompt, 0, nil, nil, nil, nil, 0},
		[]string{"en"},
		[]any{"", "", "", nil, nil, nil},
		nil, nil, nil, []int{1},
	}
	innerBytes, err := json.Marshal(innerReq)
	if err != nil {
		return nil, err
	}

	fReq := []any{nil, string(innerBytes)}
	fReqBytes, err := json.Marshal(fReq)
	if err != nil {
		return nil, err
	}

	form := url.Values{}
	form.Set("f.req", string(fReqBytes))
	if c.session.At != "" {
		form.Set("at", c.session.At)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
	req.Header.Set("User-Agent", DefaultUserAgent)
	if c.session.CookieStr != "" {
		req.Header.Set("Cookie", c.session.CookieStr)
	}
	req.Header.Set("Origin", "https://gemini.google.com")
	req.Header.Set("Referer", "https://gemini.google.com/app")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gemini request error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("%w: status %d from Gemini (session expired)", models.ErrAuth, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("%w: status %d from Gemini: %s", models.ErrUpstream, resp.StatusCode, string(b))
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	rawText := string(bodyBytes)
	return ParseGeminiResponse(rawText)
}

// ParseGeminiResponse parses the chunked response format from StreamGenerate.
func ParseGeminiResponse(raw string) (*GenerationResult, error) {
	lines := strings.Split(raw, "\n")
	result := &GenerationResult{}

	var textParts []string
	var imageURLs []string
	var videoURL string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "[[") {
			continue
		}

		var outer []any
		if err := json.Unmarshal([]byte(line), &outer); err != nil {
			continue
		}
		if len(outer) == 0 {
			continue
		}

		item, ok := outer[0].([]any)
		if !ok || len(item) < 3 {
			continue
		}

		innerStr, ok := item[2].(string)
		if !ok || innerStr == "" {
			continue
		}

		var inner []any
		if err := json.Unmarshal([]byte(innerStr), &inner); err != nil {
			continue
		}

		// Extract conversation metadata: inner[1] = [convId, respId]
		if len(inner) > 1 {
			if meta, ok := inner[1].([]any); ok && len(meta) >= 2 {
				if cid, ok := meta[0].(string); ok && cid != "" {
					result.ConversationID = cid
				}
				if rid, ok := meta[1].(string); ok && rid != "" {
					result.ResponseID = rid
				}
			}
		}

		// Extract candidates: inner[4] = [[choiceId, [text], ...]]
		if len(inner) > 4 {
			if cands, ok := inner[4].([]any); ok && len(cands) > 0 {
				if cand, ok := cands[0].([]any); ok {
					if len(cand) > 0 {
						if chid, ok := cand[0].(string); ok {
							result.ChoiceID = chid
						}
					}
					if len(cand) > 1 {
						if parts, ok := cand[1].([]any); ok && len(parts) > 0 {
							var chunkText []string
							for _, p := range parts {
								if s, ok := p.(string); ok && s != "" && !strings.Contains(s, "image_generation_content") && !strings.Contains(s, "generated_video_content") {
									chunkText = append(chunkText, s)
								}
							}
							if len(chunkText) > 0 {
								textParts = chunkText // take latest cumulative text chunk
							}
						}
					}
				}
			}
		}

		// Unescape unicode-encoded ampersands and equals before matching URLs
		unescapedInner := strings.ReplaceAll(innerStr, `\\u0026`, "&")
		unescapedInner = strings.ReplaceAll(unescapedInner, `\\U0026`, "&")
		unescapedInner = strings.ReplaceAll(unescapedInner, `\u0026`, "&")
		unescapedInner = strings.ReplaceAll(unescapedInner, `\U0026`, "&")
		unescapedInner = strings.ReplaceAll(unescapedInner, `&amp;`, "&")
		unescapedInner = strings.ReplaceAll(unescapedInner, `\\u003d`, "=")
		unescapedInner = strings.ReplaceAll(unescapedInner, `\\U003d`, "=")
		unescapedInner = strings.ReplaceAll(unescapedInner, `\\u003D`, "=")
		unescapedInner = strings.ReplaceAll(unescapedInner, `\\U003D`, "=")
		unescapedInner = strings.ReplaceAll(unescapedInner, `\u003d`, "=")
		unescapedInner = strings.ReplaceAll(unescapedInner, `\U003d`, "=")
		unescapedInner = strings.ReplaceAll(unescapedInner, `\u003D`, "=")
		unescapedInner = strings.ReplaceAll(unescapedInner, `\U003D`, "=")

		// Scan for video URLs in this chunk
		if videoMatches := videoURLRegex.FindAllString(unescapedInner, -1); len(videoMatches) > 0 {
			for _, u := range videoMatches {
				// Clean URL encoding escapes
				cleanURL := strings.ReplaceAll(u, `\\u0026`, "&")
				cleanURL = strings.ReplaceAll(cleanURL, `\\U0026`, "&")
				cleanURL = strings.ReplaceAll(cleanURL, `\u0026`, "&")
				cleanURL = strings.ReplaceAll(cleanURL, `\U0026`, "&")
				cleanURL = strings.ReplaceAll(cleanURL, `&amp;`, "&")
				cleanURL = strings.ReplaceAll(cleanURL, `\\u003d`, "=")
				cleanURL = strings.ReplaceAll(cleanURL, `\\U003d`, "=")
				cleanURL = strings.ReplaceAll(cleanURL, `\\u003D`, "=")
				cleanURL = strings.ReplaceAll(cleanURL, `\\U003D`, "=")
				cleanURL = strings.ReplaceAll(cleanURL, `\u003d`, "=")
				cleanURL = strings.ReplaceAll(cleanURL, `\U003d`, "=")
				cleanURL = strings.ReplaceAll(cleanURL, `\u003D`, "=")
				cleanURL = strings.ReplaceAll(cleanURL, `\U003D`, "=")
				cleanURL = strings.ReplaceAll(cleanURL, `\`, "")
				cleanURL = strings.TrimRight(cleanURL, `"',]`)
				if videoURL == "" {
					videoURL = cleanURL
				}
			}
		}

		// Scan for image URLs in this chunk
		if imgMatches := imageURLRegex.FindAllString(unescapedInner, -1); len(imgMatches) > 0 {
			for _, u := range imgMatches {
				cleanURL := strings.ReplaceAll(u, `\\u0026`, "&")
				cleanURL = strings.ReplaceAll(cleanURL, `\\U0026`, "&")
				cleanURL = strings.ReplaceAll(cleanURL, `\u0026`, "&")
				cleanURL = strings.ReplaceAll(cleanURL, `\U0026`, "&")
				cleanURL = strings.ReplaceAll(cleanURL, `&amp;`, "&")
				cleanURL = strings.ReplaceAll(cleanURL, `\`, "")
				cleanURL = strings.TrimRight(cleanURL, `"',]`)
				if !contains(imageURLs, cleanURL) {
					imageURLs = append(imageURLs, cleanURL)
				}
			}
		}
	}

	result.Text = strings.Join(textParts, "\n")
	result.ImageURLs = imageURLs
	result.VideoURL = videoURL
	if videoURL != "" {
		result.VideoFormat = "video/mp4"
	}

	if result.Text == "" && len(result.ImageURLs) == 0 && result.VideoURL == "" {
		return nil, fmt.Errorf("%w: empty response from Gemini", models.ErrUpstream)
	}

	return result, nil
}

func contains(arr []string, s string) bool {
	for _, a := range arr {
		if a == s {
			return true
		}
	}
	return false
}
