package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCursorSessions(t *testing.T) {
	root := t.TempDir()
	cursor := filepath.Join(root, "cursor")
	app := newApp(filepath.Join(root, "codex"), filepath.Join(root, "claude"), filepath.Join(root, "pi"), cursor)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest("GET", "/cursor", nil))
	if w.Code != 404 {
		t.Fatal("missing Cursor is visible")
	}
	project := "home-user-my-project"
	paths := []string{
		filepath.Join(cursor, "projects", project, "agent-transcripts", "flat.jsonl"),
		filepath.Join(cursor, "projects", project, "agent-transcripts", "session", "session.jsonl"),
		filepath.Join(cursor, "projects", project, "agent-transcripts", "session", "subagents", "child.jsonl"),
	}
	prompt := "\nFix <script>alert(1)</script>\n  keep spacing\n"
	for _, path := range paths {
		writeRecords(t, path,
			map[string]any{"role": "user", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": "<timestamp>today</timestamp>\n<user_query>" + prompt + "</user_query>"}}}},
			map[string]any{"role": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": "Working"}, map[string]any{"type": "tool_use", "name": "Shell", "input": map[string]any{"command": "pwd"}}}}},
			map[string]any{"type": "status", "status": "completed"},
			map[string]any{"role": "assistant", "message": map[string]any{"content": "Done"}},
		)
	}
	writeRecords(t, filepath.Join(cursor, "projects", project, "unrelated", "data.jsonl"), map[string]any{"role": "user"})
	writeRecords(t, filepath.Join(cursor, "projects", project, "data.jsonl"), map[string]any{"role": "user"})
	chats, err := app.store.List("cursor")
	if err != nil || len(chats) != 3 {
		t.Fatalf("chats: %v, %v", chats, err)
	}
	for _, c := range chats {
		if c.Project != project || c.Title != short(prompt) {
			t.Fatalf("bad summary: %+v", c)
		}
		full, err := readChat(c.Path, "cursor", true)
		if err != nil || len(full.Messages) != 4 || full.Messages[0].Text != prompt || full.Messages[2].Role != "tool" || !full.Messages[2].Detail {
			t.Fatalf("bad transcript: %+v %v", full, err)
		}
		base := "/cursor/projects/" + c.ProjectID + "/chats/" + c.ID
		for _, url := range []string{"/", "/cursor", "/cursor/projects/" + c.ProjectID, base, base + "/export?format=html", base + "/export?format=txt", base + "/export?format=jsonl"} {
			w := httptest.NewRecorder()
			app.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
			if w.Code != 200 {
				t.Fatalf("%s: %d", url, w.Code)
			}
			if strings.Contains(w.Header().Get("Content-Type"), "text/html") && strings.Contains(w.Body.String(), "<script>alert(1)</script>") {
				t.Fatal("unescaped transcript")
			}
			if strings.HasSuffix(url, "format=jsonl") {
				original, _ := os.ReadFile(c.Path)
				if w.Body.String() != string(original) {
					t.Fatal("raw export changed")
				}
			}
		}
	}
	// File changes refresh the cached title; a malformed line does not hide a chat.
	if err := os.WriteFile(paths[0], []byte("invalid\n{\"role\":\"user\",\"cwd\":\"/actual/project\",\"message\":{\"content\":\"Updated title\"}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	chats, err = app.store.List("cursor")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range chats {
		if c.Path == paths[0] {
			found = true
			if c.Title != "Updated title" || c.Project != "/actual/project" || c.Warnings != 1 {
				t.Fatalf("stale chat: %+v", c)
			}
		}
	}
	if !found {
		t.Fatal("updated chat missing")
	}
}

func TestCursorUserText(t *testing.T) {
	for _, text := range []string{"  ordinary text\n", "<user_query>incomplete", "<timestamp>today</timestamp>\nordinary", "prefix <user_query>quoted</user_query>"} {
		if cursorUserText(text) != text {
			t.Fatalf("changed plain text %q", text)
		}
	}
}
