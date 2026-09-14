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
	Tools      []Message
	Label      string
	UserID     string // anchor id for a visible user message; empty otherwise
	NextUserID string // anchor id of the following user message, for static navigation
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
	// Assign anchor ids to visible user messages and link each to the next one,
	// so the HTML export can offer next-user navigation without JavaScript.
	var userRows []int
	for i, row := range rows {
		if row.Tools == nil && !row.Detail && row.Role == "user" {
			userRows = append(userRows, i)
		}
	}
	for n, i := range userRows {
		rows[i].UserID = fmt.Sprintf("user-%d", n+1)
		if n+1 < len(userRows) {
			rows[i].NextUserID = fmt.Sprintf("user-%d", n+2)
		}
	}
	return rows
}
// isChat reports whether a row is a visible chat message (a user or assistant
// turn), as opposed to a condensable non-chat row such as tool activity or
// reasoning.
func (r conversationRow) isChat() bool {
	return r.Tools == nil && !r.Detail && (r.Role == "user" || r.Role == "assistant")
}

// nonChatSummary describes a run of consecutive non-chat rows in a few words,
// e.g. "5 tool calls, 2 reasoning blocks", for the condensed text export.
func nonChatSummary(run []conversationRow) string {
	var calls, results, reasoning int
	other := map[string]int{}
	var otherOrder []string
	for _, r := range run {
		if r.Tools != nil {
			for _, m := range r.Tools {
				if m.ToolResult {
					results++
				} else {
					calls++
				}
			}
			continue
		}
		switch r.Role {
		case "reasoning":
			reasoning++
		default:
			if other[r.Role] == 0 {
				otherOrder = append(otherOrder, r.Role)
			}
			other[r.Role]++
		}
	}
	var parts []string
	if calls > 0 {
		parts = append(parts, count(calls, "tool call"))
	}
	if results > 0 && calls == 0 {
		parts = append(parts, count(results, "tool result"))
	}
	if reasoning > 0 {
		parts = append(parts, count(reasoning, "reasoning block"))
	}
	for _, role := range otherOrder {
		parts = append(parts, count(other[role], role+" message"))
	}
	return strings.Join(parts, ", ")
}

func count(n int, noun string) string {
	if n != 1 {
		noun += "s"
	}
	return fmt.Sprintf("%d %s", n, noun)
}

func proseText(text string) string { return strings.Trim(text, "\r\n") }
