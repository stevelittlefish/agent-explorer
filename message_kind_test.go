package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexSteering(t *testing.T) {
	event := func(typ, id string) any {
		return map[string]any{"type": "event_msg", "payload": map[string]any{"type": typ, "turn_id": id}}
	}
	user := func(text, id string, kinds ...string) any {
		meta := map[string]any{"turn_id": id}
		if kinds != nil {
			meta["content_item_kinds"] = kinds
		}
		return map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": text}}, "internal_chat_message_metadata_passthrough": meta}}
	}
	path := filepath.Join(t.TempDir(), "chat.jsonl")
	writeRecords(t, path,
		user("Without lifecycle", ""), user("Still unknown", ""),
		event("task_started", "a"),
		user("Injected context", "a", "environments.environment_context"),
		user("# AGENTS.md instructions", "a"),
		user("First request", "a", "user.text"),
		// Duplicate event representation must not mark the first response as steering.
		map[string]any{"type": "event_msg", "payload": map[string]any{"type": "user_message", "message": "First request"}},
		user("First steering", "a", "user.text"),
		user("More context", "a", "agents_md.instructions"),
		user("Wrong turn", "other", "user.text"),
		event("task_complete", "other"),
		user("Second steering", "a", "user.text"),
		event("task_complete", "a"), user("After completion", "a", "user.text"),
		event("task_started", "b"), user("Next request", "b", "user.text"),
		user("<turn_aborted>cancelled</turn_aborted>", "b"),
		event("turn_aborted", "b"), user("After abort", "b", "user.text"),
		event("task_started", "c"), user("Older initial", ""), user("Older steering", ""),
	)
	chat, err := readChat(path, "codex", true)
	if err != nil {
		t.Fatal(err)
	}
	marked := 0
	for _, m := range chat.Messages {
		want := ""
		if m.Text == "First steering" || m.Text == "Second steering" || m.Text == "Older steering" {
			want = "Steering"
			marked++
		}
		if m.Kind != want {
			t.Fatalf("%q: got %q want %q", m.Text, m.Kind, want)
		}
	}
	if marked != 3 || len(chat.Messages) != 15 {
		t.Fatalf("unexpected messages: %+v", chat.Messages)
	}
}

func TestCodexEventSteering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.jsonl")
	var records []any
	for _, v := range []struct{ typ, text string }{
		{"task_started", ""}, {"user_message", "First"}, {"agent_message", "Working"}, {"user_message", "Correction"}, {"task_complete", ""}, {"task_started", ""}, {"user_message", "Next"},
	} {
		records = append(records, map[string]any{"type": "event_msg", "payload": map[string]any{"type": v.typ, "message": v.text}})
	}
	writeRecords(t, path, records...)
	chat, err := readChat(path, "codex", true)
	if err != nil || len(chat.Messages) != 4 {
		t.Fatalf("%+v %v", chat, err)
	}
	for _, m := range chat.Messages {
		want := ""
		if m.Text == "Correction" {
			want = "Steering"
		}
		if m.Kind != want {
			t.Fatalf("bad kind: %+v", m)
		}
	}
}

func TestMessageKindNavigationAndExports(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		t.Run(agent, func(t *testing.T) {
			root := t.TempDir()
			app := newApp(filepath.Join(root, "codex"), filepath.Join(root, "claude"), filepath.Join(root, "pi"), filepath.Join(root, "cursor"))
			path := filepath.Join(app.store.Roots[agent][0], "chat.jsonl")
			kind := "Queued"
			if agent == "codex" {
				kind = "Steering"
				writeRecords(t, path,
					map[string]any{"type": "event_msg", "payload": map[string]any{"type": "task_started", "turn_id": "a"}},
					map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": "Initial"}},
					map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": "Correction <script>"}},
				)
			} else {
				writeRecords(t, path,
					map[string]any{"type": "user", "promptSource": "typed", "message": map[string]any{"content": "Initial"}},
					map[string]any{"type": "queue-operation", "operation": "enqueue", "content": "Not yet delivered"},
					map[string]any{"type": "user", "promptSource": "queued", "message": map[string]any{"content": "Correction <script>"}},
					map[string]any{"type": "user", "promptSource": "queued", "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "content": "Tool output"}}}},
					map[string]any{"type": "assistant", "promptSource": "queued", "message": map[string]any{"content": "Answer"}},
					map[string]any{"type": "user", "message": map[string]any{"content": "No metadata"}},
				)
			}
			c, err := readChat(path, agent, true)
			if err != nil {
				t.Fatal(err)
			}
			for i, m := range c.Messages {
				want := ""
				if i == 1 {
					want = kind
				}
				if m.Kind != want {
					t.Fatalf("bad message: %+v", m)
				}
			}
			base := "/" + agent + "/projects/" + c.ProjectID + "/chats/" + c.ID
			for _, suffix := range []string{"", "/export?format=html", "/export?format=txt", "/export?format=jsonl"} {
				w := httptest.NewRecorder()
				app.ServeHTTP(w, httptest.NewRequest("GET", base+suffix, nil))
				if w.Code != 200 {
					t.Fatalf("%s: %d", suffix, w.Code)
				}
				body := w.Body.String()
				switch {
				case strings.HasSuffix(suffix, "jsonl"):
					original, _ := os.ReadFile(path)
					if body != string(original) {
						t.Fatal("raw export changed")
					}
				case strings.HasSuffix(suffix, "txt"):
					if strings.Count(body, "["+kind+"]") != 1 {
						t.Fatal("missing text label", body)
					}
				default:
					if strings.Count(body, `<span class="message-kind">`+kind+`</span>`) != 1 || strings.Contains(body, "Correction <script>") {
						t.Fatal("missing label or unsafe HTML", body)
					}
				}
			}
		})
	}
}
