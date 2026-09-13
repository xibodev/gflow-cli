package gemini

import (
	"testing"
)

func TestParseGeminiTextResponse(t *testing.T) {
	raw := `)]}'
200
[["wrb.fr",null,"[null,[\"c_123\",\"r_456\"],null,null,[[\"rc_789\",[\"Hello from Gemini\"],null,null,null,null,null,null,[1],\"en\"]]]"]]
`
	res, err := ParseGeminiResponse(raw)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if res.Text != "Hello from Gemini" {
		t.Errorf("expected text 'Hello from Gemini', got %q", res.Text)
	}
	if res.ConversationID != "c_123" {
		t.Errorf("expected conversation ID 'c_123', got %q", res.ConversationID)
	}
}

func TestParseGeminiImageResponse(t *testing.T) {
	raw := `)]}'
500
[["wrb.fr",null,"[null,[\"c_123\",\"r_456\"],null,null,[[\"rc_789\",[\"Here is an image:\\n\\nhttp://googleusercontent.com/image_generation_content/0_1\"],null,null,null,null,null,null,[1],\"en\",null,null,[null,null,null,null,null,null,null,[]],null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,[],null,null,null,null,null,null,null,null,null,null,null,null,null,[[null,null,null,[null,1,\"watermarked.jpg\",\"https://lh3.googleusercontent.com/gg-dl/AAQ_test123\"]]]]]]"]]
`
	res, err := ParseGeminiResponse(raw)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(res.ImageURLs) == 0 {
		t.Fatalf("expected at least 1 image URL, got 0")
	}
	if res.ImageURLs[0] != "https://lh3.googleusercontent.com/gg-dl/AAQ_test123" {
		t.Errorf("expected https://lh3.googleusercontent.com/gg-dl/AAQ_test123, got %q", res.ImageURLs[0])
	}
}

func TestParseGeminiVideoResponse(t *testing.T) {
	raw := `)]}'
500
[["wrb.fr",null,"[null,[\"c_123\",\"r_456\"],null,null,[[\"rc_789\",[\"Your video is ready!\\n\\nhttp://googleusercontent.com/generated_video_content/12345\"],null,null,null,null,null,null,[1],\"en\",null,null,[null,null,null,null,null,null,null,[]],null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,[],null,null,null,null,null,null,null,null,null,null,null,null,null,[[null,2,\"video.mp4\",null,null,null,null,[\"https://lh3.googleusercontent.com/gg-dl/AAQ_v1\",\"https://contribution.usercontent.google.com/download?c=test\u0026filename=video.mp4\u0026opi=123\"]]]]]]"]]
`
	res, err := ParseGeminiResponse(raw)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if res.VideoURL == "" {
		t.Fatalf("expected video URL to be found")
	}
	if res.VideoFormat != "video/mp4" {
		t.Errorf("expected video/mp4, got %q", res.VideoFormat)
	}
	if res.VideoURL != "https://contribution.usercontent.google.com/download?c=test&filename=video.mp4&opi=123" {
		t.Errorf("unexpected video URL: %q", res.VideoURL)
	}
}

func TestParseGeminiVideoResponseWithEscapedAmpersands(t *testing.T) {
	// In real Gemini responses, the outer JSON contains \\u0026, which unmarshals into \u0026 inside innerStr.
	raw := `)]}'
500
[["wrb.fr",null,"[null,[\"c_123\",\"r_456\"],null,null,[[\"rc_789\",[\"Your video is ready!\\n\\nhttp://googleusercontent.com/generated_video_content/12345\"],null,null,null,null,null,null,[1],\"en\",null,null,[null,null,null,null,null,null,null,[]],null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,[],null,null,null,null,null,null,null,null,null,null,null,null,null,[[null,2,\"video.mp4\",null,null,null,null,[\"https://lh3.googleusercontent.com/gg-dl/AAQ_v1\",\"https://contribution.usercontent.google.com/download?c=test\\u0026filename=video.mp4\\u0026opi=123\"]]]]]]"]]
`
	res, err := ParseGeminiResponse(raw)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if res.VideoURL == "" {
		t.Fatalf("expected video URL to be found")
	}
	expectedURL := "https://contribution.usercontent.google.com/download?c=test&filename=video.mp4&opi=123"
	if res.VideoURL != expectedURL {
		t.Errorf("expected video URL %q, got %q", expectedURL, res.VideoURL)
	}
}

func TestParseGeminiVideoResponseWithEscapedEqualsAndAmpersands(t *testing.T) {
	// In real Gemini responses, the outer JSON contains \\u003d and \\u0026, which unmarshal into \u003d and \u0026 inside innerStr.
	raw := `)]}'
500
[["wrb.fr",null,"[null,[\"c_123\",\"r_456\"],null,null,[[\"rc_789\",[\"Your video is ready!\\n\\nhttp://googleusercontent.com/generated_video_content/12345\"],null,null,null,null,null,null,[1],\"en\",null,null,[null,null,null,null,null,null,null,[]],null,null,null,null,null,null,null,null,null,null,null,null,null,null,null,[],null,null,null,null,null,null,null,null,null,null,null,null,null,[[null,2,\"video.mp4\",null,null,null,null,[\"https://lh3.googleusercontent.com/gg-dl/AAQ_v1\",\"https://contribution.usercontent.google.com/download?c\\u003dtest\\u0026filename\\u003dvideo.mp4\\u0026opi\\u003d123\"]]]]]]"]]
`
	res, err := ParseGeminiResponse(raw)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if res.VideoURL == "" {
		t.Fatalf("expected video URL to be found")
	}
	expectedURL := "https://contribution.usercontent.google.com/download?c=test&filename=video.mp4&opi=123"
	if res.VideoURL != expectedURL {
		t.Errorf("expected video URL %q, got %q", expectedURL, res.VideoURL)
	}
}

func TestVideoURLRegex(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "plain &",
			input:    `"https://contribution.usercontent.google.com/download?c=test&filename=video.mp4&opi=123"`,
			expected: "https://contribution.usercontent.google.com/download?c=test&filename=video.mp4&opi=123",
		},
		{
			name:     `escaped \u0026 in JSON quotes`,
			input:    `[\"https://contribution.usercontent.google.com/download?c=test\u0026filename=video.mp4\u0026opi=123\"]`,
			expected: `https://contribution.usercontent.google.com/download?c=test\u0026filename=video.mp4\u0026opi=123`,
		},
		{
			name:     `escaped \u003d in JSON quotes`,
			input:    `[\"https://contribution.usercontent.google.com/download?c\u003dtest\u0026filename\u003dvideo.mp4\u0026opi\u003d123\"]`,
			expected: `https://contribution.usercontent.google.com/download?c\u003dtest\u0026filename\u003dvideo.mp4\u0026opi\u003d123`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			match := videoURLRegex.FindString(tt.input)
			if match != tt.expected {
				t.Errorf("expected match %q, got %q", tt.expected, match)
			}
		})
	}
}

func TestParseGeminiEmptyFails(t *testing.T) {
	raw := ")]}'\n\n[[]]"
	_, err := ParseGeminiResponse(raw)
	if err == nil {
		t.Errorf("expected error for empty response")
	}
}
