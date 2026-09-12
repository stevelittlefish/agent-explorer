package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNavigationAndExports(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "claude", "projects", "encoded-project", "chat.jsonl")
	writeRecords(t, path, map[string]any{"type": "user", "cwd": "/work/<project>", "message": map[string]any{"content": "Hello <script>alert(1)</script>"}})
	app := newApp(filepath.Join(root, "codex"), filepath.Join(root, "claude"))
	base := "/claude/projects/" + key("/work/<project>") + "/chats/" + key(path)
	for _, url := range []string{"/", "/claude", "/codex", "/claude/projects/" + key("/work/<project>"), base, base + "/export?format=html", base + "/export?format=txt", base + "/export?format=jsonl", "/static/style.css", "/static/app.js"} {
		t.Run(url, func(t *testing.T) {
			w := httptest.NewRecorder()
			app.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
			if w.Code != 200 {
				t.Fatalf("%d: %s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Header().Get("Content-Type"), "text/html") {
				if strings.Contains(w.Body.String(), "<script>alert(1)</script>") {
					t.Fatal("unescaped HTML")
				}
				if strings.Contains(url, "format=html") && (!strings.Contains(w.Body.String(), "--bg:#faf9f6") || strings.Contains(w.Body.String(), "ZgotmplZ") || strings.Contains(w.Body.String(), "src=")) {
					t.Fatal("export is not self contained", w.Body.String())
				}
			}
			if strings.Contains(url, "/export") && !strings.Contains(w.Header().Get("Content-Disposition"), "attachment;") {
				t.Fatal("not a download")
			}
			if strings.Contains(url, "format=jsonl") {
				b, _ := os.ReadFile(path)
				if w.Body.String() != string(b) {
					t.Fatal("raw export changed")
				}
			}
		})
	}
	for _, url := range []string{"/missing", "/claude/projects/nope", "/claude/projects/" + key("/work/<project>") + "/chats/nope", base + "/wrong", "/static/../main.go"} {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
		if w.Code != 404 {
			t.Fatalf("%s: %d", url, w.Code)
		}
	}
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest("POST", base, nil))
	if w.Code != 405 {
		t.Fatal(w.Code)
	}
}
