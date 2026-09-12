package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func piFixture(t *testing.T, path string) {
	t.Helper()
	writeRecords(t, path,
		map[string]any{"type": "session", "version": 3, "cwd": "/work/pi-project"},
		map[string]any{"type": "model_change", "modelId": "example-model"},
		map[string]any{"type": "message", "message": map[string]any{"role": "user", "content": "A pi question"}},
		map[string]any{"type": "message", "message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "thinking", "thinking": "Consider the file"},
			map[string]any{"type": "text", "text": "Reading now"},
			map[string]any{"type": "toolCall", "id": "call-1", "name": "read", "arguments": map[string]any{"path": "main.go"}},
		}}},
		map[string]any{"type": "message", "message": map[string]any{"role": "toolResult", "toolCallId": "call-1", "content": []any{map[string]any{"type": "text", "text": "<script>file contents</script>"}}}},
		map[string]any{"type": "message", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "Finished"}}}},
		map[string]any{"type": "compaction", "summary": "Previous work"},
		map[string]any{"type": "branch_summary", "summary": "Other branch"},
		map[string]any{"type": "message", "message": map[string]any{"role": "bashExecution", "command": "pwd", "output": "/work/pi-project", "exitCode": 0}},
		map[string]any{"type": "custom_message", "content": "Extension note", "display": true},
		map[string]any{"type": "session_info", "name": "Named pi conversation"},
	)
}
func TestPiTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	piFixture(t, path)
	chat, err := readChat(path, "pi", true)
	if err != nil {
		t.Fatal(err)
	}
	if chat.Project != "/work/pi-project" || chat.Title != "Named pi conversation" || len(chat.Messages) != 10 {
		t.Fatalf("unexpected chat: %+v", chat)
	}
	if chat.Messages[1].Role != "reasoning" || chat.Messages[3].Role != "tool" || !strings.Contains(chat.Messages[3].Text, "main.go") || !chat.Messages[4].ToolResult {
		t.Fatal("reasoning, arguments or result missing")
	}
	rows := conversationRows(chat.Messages)
	if rows[3].Label != "1 tool call" || len(rows[3].Tools) != 2 {
		t.Fatal("tool group is incorrect")
	}
	if chat.Messages[6].Role != "compaction" || chat.Messages[7].Role != "branch summary" || !strings.Contains(chat.Messages[8].Text, "Exit code: 0") {
		t.Fatal("summary or shell output missing")
	}
	summary, err := readChat(path, "pi", false)
	if err != nil || len(summary.Messages) != 0 || summary.Title != chat.Title {
		t.Fatal("summary parsing failed", err)
	}
}
