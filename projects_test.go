package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProjectSorting(t *testing.T) {
	root := t.TempDir()
	app := newApp(filepath.Join(root, "codex"), filepath.Join(root, "claude"), filepath.Join(root, "pi"), filepath.Join(root, "cursor"))
	// Full-path order differs from displayed-name order. Alpha's old chat must
	// not override its newer activity when the project summary is assembled.
	for i, v := range []struct {
		project string
		day     int
	}{{"/z/alpha", 1}, {"/a/Beta", 2}, {"/b/charlie", 3}, {"/z/alpha", 4}} {
		path := filepath.Join(root, "claude", "projects", string(rune('a'+i))+".jsonl")
		writeRecords(t, path, map[string]any{"type": "user", "cwd": v.project, "message": map[string]any{"content": "Hello"}})
		stamp := time.Date(2026, 1, v.day, 12, 0, 0, 0, time.UTC)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		query, mode string
		names       []string
	}{
		{"", "recent", []string{"alpha", "charlie", "Beta"}},
		{"?sort=recent", "recent", []string{"alpha", "charlie", "Beta"}},
		{"?sort=oldest", "oldest", []string{"Beta", "charlie", "alpha"}},
		{"?sort=name", "name", []string{"alpha", "Beta", "charlie"}},
		{"?sort=name-desc", "name-desc", []string{"charlie", "Beta", "alpha"}},
		{"?sort=invalid", "recent", []string{"alpha", "charlie", "Beta"}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			w := httptest.NewRecorder()
			app.ServeHTTP(w, httptest.NewRequest("GET", "/claude"+tc.query, nil))
			if w.Code != 200 {
				t.Fatal(w.Code)
			}
			body := w.Body.String()
			pos := -1
			for _, name := range tc.names {
				next := strings.Index(body, "<strong>"+name+"</strong>")
				if next <= pos {
					t.Fatalf("wrong order for %s", tc.query)
				}
				pos = next
			}
			if !strings.Contains(body, `href="/claude?sort=`+tc.mode+`" aria-current="true"`) {
				t.Fatal("active sort not identified")
			}
			for _, mode := range []string{"recent", "oldest", "name", "name-desc"} {
				if !strings.Contains(body, `href="/claude?sort=`+mode+`"`) {
					t.Fatal("missing sort link", mode)
				}
			}
			if !strings.Contains(body, "2 chats · Last activity") {
				t.Fatal("missing project activity")
			}
		})
	}
}

func TestProjectSortingTies(t *testing.T) {
	for _, mode := range []string{"recent", "oldest", "name", "name-desc"} {
		projects := []Project{{ID: "b", Name: "/z/alpha"}, {ID: "c", Name: "/a/Alpha"}, {ID: "a", Name: "/a/alpha"}}
		sortProjects(projects, mode)
		want := []string{"a", "c", "b"}
		if mode == "name-desc" {
			want = []string{"b", "c", "a"}
		}
		for i, id := range want {
			if projects[i].ID != id {
				t.Fatalf("%s: %+v", mode, projects)
			}
		}
	}
}
