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
