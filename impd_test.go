package main

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// impdDatabase writes sessions the way impd stores them: entry JSON per row.
func impdDatabase(t *testing.T, path string, sessions map[string][]any) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE TABLE session (id TEXT PRIMARY KEY, cwd TEXT NOT NULL DEFAULT '', title TEXT NOT NULL DEFAULT '',
		title_custom INTEGER NOT NULL DEFAULT 0, provider TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL, updated_at TEXT NOT NULL DEFAULT '');
	CREATE TABLE message (seq INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL, entry_id TEXT NOT NULL,
		parent_id TEXT NOT NULL DEFAULT '', type TEXT NOT NULL, data BLOB NOT NULL, created_at TEXT NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	for id, entries := range sessions {
		if _, err := db.Exec(`INSERT INTO session (id, cwd, title, created_at, updated_at) VALUES (?, '/work/daemon', '', '2026-07-01T10:00:00Z', '2026-07-01T11:00:00Z')`, id); err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			data, _ := json.Marshal(e)
			if _, err := db.Exec(`INSERT INTO message (session_id, entry_id, type, data, created_at) VALUES (?, '', 'message', ?, '')`, id, data); err != nil {
				t.Fatal(err)
			}
		}
	}
	return db
}

func TestImpdSessions(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "imp", "impd", "sessions.db")
	writeRecords(t, filepath.Join(root, "imp", "sessions", "work-cli", "s.jsonl"), impEntries(impUser("From the terminal"))...)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
		t.Fatal(err)
	}
	db := impdDatabase(t, dbPath, map[string][]any{
		"s1": impEntries(impUser("From the browser"), impAssistant("anthropic-messages", 10, 0, 4000, 30, impText("Hello from impd"))),
	})
	app := newApp(filepath.Join(root, "codex"), filepath.Join(root, "claude"), filepath.Join(root, "pi"), filepath.Join(root, "cursor"), filepath.Join(root, "imp"))
	t.Cleanup(app.store.closeImpd) // Windows can't remove the temp dir with the database open
	chats, err := app.store.List("imp")
	if err != nil || len(chats) != 2 {
		t.Fatalf("%d chats, %v", len(chats), err)
	}
	var daemon Chat
	for _, c := range chats {
		if c.Session == "s1" {
			daemon = c
		}
	}
	if daemon.Project != "/work/daemon" || daemon.Title != "From the browser" || daemon.Updated.IsZero() {
		t.Fatalf("daemon chat %+v", daemon)
	}

	// A title impd generates replaces the first prompt, without any new entries.
	if _, err := db.Exec(`UPDATE session SET title = 'Greetings' WHERE id = 's1'`); err != nil {
		t.Fatal(err)
	}
	base := "/imp/projects/" + key("/work/daemon") + "/chats/" + daemon.ID
	for _, url := range []string{base, base + "/analysis", base + "/export?format=jsonl"} {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
		body := w.Body.String()
		if w.Code != 200 {
			t.Fatalf("%s: %d", url, w.Code)
		}
		switch {
		case strings.HasSuffix(url, "jsonl"):
			if strings.Count(body, "\n") != 2 || !strings.Contains(body, `"Hello from impd"`) {
				t.Fatalf("export %q", body)
			}
		case !strings.Contains(body, "Greetings") || !strings.Contains(body, "Estimated $"):
			t.Fatalf("%s: title or estimate missing", url)
		}
	}
}

func TestImpdUnreadable(t *testing.T) {
	root := t.TempDir()
	writeRecords(t, filepath.Join(root, "imp", "sessions", "work-cli", "s.jsonl"), impEntries(impUser("From the terminal"))...)
	writeRecords(t, filepath.Join(root, "imp", "impd", "sessions.db"), map[string]any{"not": "sqlite"})
	app := newApp(filepath.Join(root, "codex"), filepath.Join(root, "claude"), filepath.Join(root, "pi"), filepath.Join(root, "cursor"), filepath.Join(root, "imp"))
	t.Cleanup(app.store.closeImpd) // Windows can't remove the temp dir with the database open
	chats, err := app.store.List("imp")
	if err != nil || len(chats) != 1 {
		t.Fatalf("a broken impd database hid the terminal sessions: %d chats, %v", len(chats), err)
	}
}
