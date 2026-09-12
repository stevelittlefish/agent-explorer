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
