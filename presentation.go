package main

import (
	"fmt"
	"strings"
	"time"
)

// textBlocks recognizes fenced code without interpreting transcript text as HTML.
// Everything else retains its original text and whitespace.
type textBlock struct {
	Text, Language string
	Code           bool
}

func textBlocks(text string) []textBlock {
	var result []textBlock
	var body strings.Builder
	var fence, language string
	flush := func(code bool) {
		if body.Len() > 0 {
			result = append(result, textBlock{Text: body.String(), Language: language, Code: code})
			body.Reset()
		}
	}
	for _, line := range strings.SplitAfter(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if fence == "" {
			marker := ""
			if strings.HasPrefix(trimmed, "```") {
				marker = "`"
			} else if strings.HasPrefix(trimmed, "~~~") {
				marker = "~"
			}
			if marker != "" {
				count := len(trimmed) - len(strings.TrimLeft(trimmed, marker))
				flush(false)
				fence = strings.Repeat(marker, count)
				language = strings.TrimSpace(trimmed[count:])
				continue
			}
		} else if len(trimmed) >= len(fence) && strings.Trim(trimmed, fence[:1]) == "" {
			flush(true)
			fence = ""
			language = ""
			continue
		}
		body.WriteString(line)
	}
	flush(fence != "")
	return result
}
func displayStamp(value string) string {
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t.Local().Format("02 Jan · 15:04")
	}
	return value
}

// conversationRows groups only adjacent tool records, preserving transcript order.
// Results stay with their calls but do not inflate the call count.
type conversationRow struct {
	Message
	Tools []Message
	Label string
}

func conversationRows(messages []Message) []conversationRow {
	var rows []conversationRow
	for i := 0; i < len(messages); {
		if messages[i].Role != "tool" {
			rows = append(rows, conversationRow{Message: messages[i]})
			i++
			continue
		}
		start, calls := i, 0
		for i < len(messages) && messages[i].Role == "tool" {
			if !messages[i].ToolResult {
				calls++
			}
			i++
		}
		count, noun := calls, "tool call"
		if calls == 0 {
			count, noun = i-start, "tool result"
		}
		if count != 1 {
			noun += "s"
		}
		rows = append(rows, conversationRow{Tools: messages[start:i], Label: fmt.Sprintf("%d %s", count, noun)})
	}
	return rows
}
func proseText(text string) string { return strings.Trim(text, "\r\n") }
