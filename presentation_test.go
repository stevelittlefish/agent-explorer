package main

import "testing"

func TestTextBlocks(t *testing.T) {
	tests := []struct {
		name, input string
		want        []textBlock
	}{
		{"plain text", "Hello <script>\nWorld", []textBlock{{Text: "Hello <script>\nWorld"}}},
		{"fenced code", "Before\n```go\nfmt.Println(\"hi\")\n```\nAfter", []textBlock{{Text: "Before\n"}, {Text: "fmt.Println(\"hi\")\n", Language: "go", Code: true}, {Text: "After"}}},
		{"long fence", "````md\n```go\nx\n```\n````", []textBlock{{Text: "```go\nx\n```\n", Language: "md", Code: true}}},
		{"unfinished fence", "~~~sh\necho hello", []textBlock{{Text: "echo hello", Language: "sh", Code: true}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := textBlocks(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("got %#v, want %#v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("block %d: got %#v, want %#v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestConversationRows(t *testing.T) {
	messages := []Message{
		{Role: "user", Text: "Question"},
		{Role: "tool", Text: "call 1", Detail: true},
		{Role: "tool", Text: "result 1", Detail: true, ToolResult: true},
		{Role: "tool", Text: "call 2", Detail: true},
		{Role: "tool", Text: "result 2", Detail: true, ToolResult: true},
		{Role: "reasoning", Text: "Reasoning separates runs", Detail: true},
		{Role: "tool", Text: "orphan result", Detail: true, ToolResult: true},
		{Role: "assistant", Text: "Answer"},
		{Role: "tool", Text: "call 3", Detail: true},
	}
	rows := conversationRows(messages)
	if len(rows) != 6 {
		t.Fatalf("got %d rows", len(rows))
	}
	if rows[1].Label != "2 tool calls" || len(rows[1].Tools) != 4 {
		t.Fatalf("bad group: %+v", rows[1])
	}
	if rows[3].Label != "1 tool result" || rows[5].Label != "1 tool call" {
		t.Fatal("incorrect singular or result-only label")
	}
	var flattened []Message
	for _, row := range rows {
		if row.Tools != nil {
			flattened = append(flattened, row.Tools...)
		} else {
			flattened = append(flattened, row.Message)
		}
	}
	for i, m := range messages {
		if flattened[i] != m {
			t.Fatalf("message %d changed order or content", i)
		}
	}
	if len(conversationRows(nil)) != 0 {
		t.Fatal("empty conversation has rows")
	}
}

func TestNonChatSummary(t *testing.T) {
	rows := conversationRows([]Message{
		{Role: "reasoning", Text: "think", Detail: true},
		{Role: "tool", Text: "a", Detail: true},
		{Role: "tool", Text: "b", Detail: true},
		{Role: "tool", Text: "r", Detail: true, ToolResult: true},
	})
	if got := nonChatSummary(rows); got != "2 tool calls, 1 reasoning block" {
		t.Fatalf("summary = %q", got)
	}
	single := conversationRows([]Message{{Role: "tool", Text: "x", Detail: true}})
	if got := nonChatSummary(single); got != "1 tool call" {
		t.Fatalf("singular summary = %q", got)
	}
	results := conversationRows([]Message{{Role: "tool", Text: "r", Detail: true, ToolResult: true}})
	if got := nonChatSummary(results); got != "1 tool result" {
		t.Fatalf("result-only summary = %q", got)
	}
}

func TestUserMessageNavigationAnchors(t *testing.T) {
	rows := conversationRows([]Message{
		{Role: "user", Text: "first"},
		{Role: "assistant", Text: "reply"},
		{Role: "user", Text: "hidden", Detail: true},
		{Role: "user", Text: "second"},
		{Role: "assistant", Text: "reply"},
		{Role: "user", Text: "third"},
	})
	if rows[0].UserID != "user-1" || rows[0].NextUserID != "user-2" {
		t.Fatalf("first user row: %+v", rows[0])
	}
	if rows[2].UserID != "" {
		t.Fatal("detail user message should not get an anchor")
	}
	if rows[3].UserID != "user-2" || rows[3].NextUserID != "user-3" {
		t.Fatalf("second user row: %+v", rows[3])
	}
	if rows[5].UserID != "user-3" || rows[5].NextUserID != "" {
		t.Fatalf("last user row should have no next: %+v", rows[5])
	}
}
