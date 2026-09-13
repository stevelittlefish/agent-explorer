package main

import (
	"path/filepath"
	"strings"
)

// Cursor's workspace slug is lossy (hyphens can also mean underscores or
// separators). Keep the recorded label instead of inventing a filesystem path.
func cursorProject(path string) string {
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		if filepath.Base(dir) == "agent-transcripts" {
			return filepath.Base(filepath.Dir(dir))
		}
		if filepath.Dir(dir) == dir {
			return ""
		}
	}
}

// Current Cursor user messages wrap the prompt in context and timestamp tags.
// Unwrap only a complete envelope; leave ordinary text and partial records alone.
func cursorUserText(text string) string {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "<timestamp>") {
		if end := strings.Index(trimmed, "</timestamp>"); end >= 0 {
			trimmed = strings.TrimSpace(trimmed[end+len("</timestamp>"):])
		}
	}
	if strings.HasPrefix(trimmed, "<user_query>") && strings.HasSuffix(trimmed, "</user_query>") {
		return strings.TrimSuffix(strings.TrimPrefix(trimmed, "<user_query>"), "</user_query>")
	}
	return text
}
