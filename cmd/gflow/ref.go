package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

var mediaIDPattern = regexp.MustCompile(`^[A-Za-z0-9_\-/:.]{8,256}$`)

// isMediaID reports whether s looks like an upstream media identifier.
func isMediaID(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if strings.ContainsAny(s, " \t\n\r\"'") {
		return false
	}
	return mediaIDPattern.MatchString(s)
}

// resolveReference distinguishes a local file from a media ID. A missing file
// that looks like an intended path is an error, never silently an ID.
func resolveReference(s string) (path string, mediaID string, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", nil
	}
	if st, serr := os.Stat(s); serr == nil && !st.IsDir() {
		return s, "", nil
	}
	lower := strings.ToLower(s)
	isPathLike := strings.ContainsAny(s, `/\`) ||
		strings.HasSuffix(lower, ".png") || strings.HasSuffix(lower, ".jpg") ||
		strings.HasSuffix(lower, ".jpeg") || strings.HasSuffix(lower, ".webp")
	if isPathLike {
		return "", "", fmt.Errorf("file not found: %s", s)
	}
	if !isMediaID(s) {
		return "", "", fmt.Errorf("invalid reference %q: not a readable file or media ID", s)
	}
	return "", s, nil
}
