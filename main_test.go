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
	writeRecords(t, path, map[string]any{"type": "user", "cwd": "/work/<project>", "message": map[string]any{"content": "Hello <script>alert(1)</script>\n\n```html\n<script>alert(1)</script>\n```"}})
	app := newApp(filepath.Join(root, "codex"), filepath.Join(root, "claude"), filepath.Join(root, "pi"))
	base := "/claude/projects/" + key("/work/<project>") + "/chats/" + key(path)
	for _, url := range []string{"/", "/claude", "/claude/projects/" + key("/work/<project>"), base, base + "/export?format=html", base + "/export?format=txt", base + "/export?format=jsonl", "/static/style.css", "/static/app.js"} {
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
				if strings.Contains(url, "format=html") && (!strings.Contains(w.Body.String(), "<style>") || !strings.Contains(w.Body.String(), "color-scheme: dark;") || strings.Contains(w.Body.String(), "ZgotmplZ") || strings.Contains(w.Body.String(), "src=")) {
					t.Fatal("export is not self contained", w.Body.String())
				}
			}
			if url == base && !strings.Contains(w.Body.String(), `aria-current="page"`) {
				t.Fatal("current conversation is not identified")
			}
			if strings.Contains(url, "format=html") && (strings.Contains(w.Body.String(), `class="chat-index"`) || strings.Contains(w.Body.String(), `data-copy`+" hidden")) {
				t.Fatal("export contains interactive navigation")
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
	for _, url := range []string{"/missing", "/codex", "/pi", "/claude/projects/nope", "/claude/projects/" + key("/work/<project>") + "/chats/nope", base + "/wrong", "/static/../main.go"} {
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

func TestAgentDiscovery(t *testing.T) {
	root := t.TempDir()
	codex, claude, pi := filepath.Join(root, "codex"), filepath.Join(root, "claude"), filepath.Join(root, "pi")
	app := newApp(codex, claude, pi)
	request := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}
	home := request("/").Body.String()
	if !strings.Contains(home, "No agent data folders found") || strings.Contains(home, `class="agent-card"`) || strings.Contains(home, `class="agent-nav"`) {
		t.Fatal("empty discovery has agent options")
	}
	if err := os.WriteFile(codex, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pi, 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/pi"} {
		w := request(path)
		body := w.Body.String()
		if w.Code != 200 || !strings.Contains(body, `href="/pi"`) || strings.Contains(body, `href="/codex"`) || strings.Contains(body, `href="/claude"`) {
			t.Fatalf("bad discovery for %s", path)
		}
	}
	if request("/codex").Code != 404 || request("/claude").Code != 404 {
		t.Fatal("absent agent is routable")
	}
	if err := os.Remove(pi); err != nil {
		t.Fatal(err)
	}
	if request("/pi").Code != 404 {
		t.Fatal("removed folder still visible")
	}
	// Custom session locations can be used even without a default config directory.
	custom := filepath.Join(root, "custom-sessions")
	if err := os.MkdirAll(custom, 0700); err != nil {
		t.Fatal(err)
	}
	app.store.Roots["pi"] = []string{custom}
	if request("/pi").Code != 200 {
		t.Fatal("custom sessions not discovered")
	}
}

func TestPiNavigationAndExports(t *testing.T) {
	root := t.TempDir()
	pi := filepath.Join(root, "pi")
	path := filepath.Join(pi, "sessions", "encoded-project", "session.jsonl")
	piFixture(t, path)
	app := newApp(filepath.Join(root, "codex"), filepath.Join(root, "claude"), pi)
	project := "/pi/projects/" + key("/work/pi-project")
	chat := project + "/chats/" + key(path)
	for _, url := range []string{"/", "/pi", project, chat, chat + "/export?format=html", chat + "/export?format=txt", chat + "/export?format=jsonl"} {
		t.Run(url, func(t *testing.T) {
			w := httptest.NewRecorder()
			app.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
			if w.Code != 200 {
				t.Fatalf("%d: %s", w.Code, w.Body.String())
			}
			body := w.Body.String()
			if strings.Contains(w.Header().Get("Content-Type"), "text/html") {
				if strings.Contains(body, "<script>file contents</script>") {
					t.Fatal("unsafe transcript HTML")
				}
				if strings.Contains(body, `href="/codex"`) || strings.Contains(body, `href="/claude"`) {
					t.Fatal("missing agents in navigation")
				}
			}
			if strings.HasSuffix(url, "format=jsonl") {
				raw, _ := os.ReadFile(path)
				if body != string(raw) {
					t.Fatal("raw export changed")
				}
			}
			if strings.HasSuffix(url, "format=txt") && !strings.Contains(body, "file contents") {
				t.Fatal("text export lacks tool output")
			}
		})
	}
}
