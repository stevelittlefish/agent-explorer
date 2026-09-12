package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRecords(t *testing.T, path string, records ...any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, r := range records {
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
}
func TestCodex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.jsonl")
	writeRecords(t, path,
		map[string]any{"type": "session_meta", "payload": map[string]any{"cwd": "/work/project"}},
		map[string]any{"type": "event_msg", "payload": map[string]any{"type": "user_message", "message": "Hello"}},
		map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "Hello"}}}},
		map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Hi <script>alert(1)</script>"}}}},
		map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call", "name": "shell", "arguments": "pwd"}},
		map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call_output", "output": "/work/project"}})
	c, err := readChat(path, "codex", true)
	if err != nil {
		t.Fatal(err)
	}
	if c.Project != "/work/project" || c.Title != "Hello" || len(c.Messages) != 4 {
		t.Fatalf("unexpected chat: %+v", c)
	}
	if !c.Messages[2].Detail || c.Messages[2].Role != "tool" {
		t.Fatal("missing tool detail")
	}
}
func TestClaudeAndMalformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.jsonl")
	writeRecords(t, path,
		map[string]any{"type": "user", "cwd": "/work/a-b", "message": map[string]any{"content": "Question"}},
		map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "thinking", "thinking": "Thinking"}, map[string]any{"type": "text", "text": "Answer"}, map[string]any{"type": "tool_use", "name": "Read", "input": map[string]any{"path": "a"}}}}},
		map[string]any{"type": "user", "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "content": []any{map[string]any{"type": "text", "text": "file contents"}}}}}},
		map[string]any{"type": "ai-title", "aiTitle": "A useful title"})
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	f.WriteString("broken\n{\"type\":")
	f.Close()
	c, err := readChat(path, "claude", true)
	if err != nil {
		t.Fatal(err)
	}
	if c.Project != "/work/a-b" || c.Title != "A useful title" || len(c.Messages) != 5 || c.Warnings != 2 {
		t.Fatalf("unexpected: %+v", c)
	}
	if c.Messages[4].Role != "tool" || c.Messages[4].Text != "file contents" {
		t.Fatal(c.Messages[4])
	}
}
func TestEventFallbackAndLargeRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.jsonl")
	long := strings.Repeat("x", 100000)
	writeRecords(t, path, map[string]any{"type": "event_msg", "payload": map[string]any{"type": "user_message", "message": long}})
	c, err := readChat(path, "codex", true)
	if err != nil || len(c.Messages) != 1 || c.Messages[0].Text != long {
		t.Fatal("large event fallback failed", err)
	}
}
func TestStoreRefreshAndSymlink(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sessions", "chat.jsonl")
	writeRecords(t, path, map[string]any{"type": "user", "cwd": "/first", "message": map[string]any{"content": "One"}})
	s := Store{Roots: map[string][]string{"claude": {filepath.Join(root, "sessions"), filepath.Join(root, "missing")}}}
	c, err := s.List("claude")
	if err != nil || len(c) != 1 {
		t.Fatal(c, err)
	}
	writeRecords(t, path, map[string]any{"type": "user", "cwd": "/second", "message": map[string]any{"content": "A longer second title"}})
	if err := os.Symlink(path, filepath.Join(root, "sessions", "link.jsonl")); err != nil {
		t.Fatal(err)
	}
	c, err = s.List("claude")
	if err != nil || len(c) != 1 || c[0].Project != "/second" {
		t.Fatal(c, err)
	}
}

func TestToolCountsForBothAgents(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		t.Run(agent, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tools.jsonl")
			var records []any
			for i := 0; i < 4; i++ {
				if agent == "codex" {
					records = append(records, map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call", "name": "Read", "arguments": "file"}}, map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call_output", "output": "contents"}})
				} else {
					records = append(records, map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "name": "Read", "input": "file"}}}}, map[string]any{"type": "user", "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "content": "contents"}}}})
				}
			}
			writeRecords(t, path, records...)
			chat, err := readChat(path, agent, true)
			if err != nil {
				t.Fatal(err)
			}
			rows := conversationRows(chat.Messages)
			if len(rows) != 1 || rows[0].Label != "4 tool calls" || len(rows[0].Tools) != 8 {
				t.Fatalf("incorrect calls/results: %+v", rows)
			}
		})
	}
}
