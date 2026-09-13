package gemini

import (
	"context"
	"testing"
	"time"
)

func TestFindGeminiExecutable(t *testing.T) {
	exe, err := FindGeminiExecutable()
	if err != nil {
		t.Skipf("Gemini executable not found: %v", err)
	}
	t.Logf("Found Gemini executable: %s", exe)
}

func TestRefreshSession(t *testing.T) {
	if _, err := FindGeminiExecutable(); err != nil {
		t.Skipf("Skipping session refresh test (Gemini app not installed): %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	sess, err := RefreshSession(ctx, 9223)
	if err != nil {
		t.Fatalf("RefreshSession error: %v", err)
	}
	t.Logf("Session refreshed successfully: at=%s, bl=%s, cookie count=%d", sess.At, sess.Bl, len(sess.Cookies))
}
