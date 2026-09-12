package main

import "fmt"

// pi sessions are append-only logs. Display their entries in file order, including
// saved branch and compaction summaries; the original JSONL keeps the tree IDs.
func readPiRecord(r map[string]any, stamp string, c *Chat, titleFound *bool, add func(string, string, string, bool)) {
	switch str(r, "type") {
	case "session":
		if cwd := str(r, "cwd"); cwd != "" {
			c.Project = cwd
		}
	case "session_info":
		if name := str(r, "name"); name != "" {
			c.Title = name
			*titleFound = true
		}
	case "message":
		m := obj(r, "message")
		switch role := str(m, "role"); role {
		case "user", "assistant":
			readContent(m["content"], role, stamp, add)
		case "toolResult":
			add("tool_result", contentText(m["content"]), stamp, true)
		case "bashExecution":
			text := str(m, "command") + "\n" + str(m, "output")
			if code := m["exitCode"]; code != nil {
				text += fmt.Sprintf("\nExit code: %v", code)
			}
			if m["cancelled"] == true {
				text += "\n[Cancelled]"
			}
			if m["truncated"] == true {
				text += "\n[Output truncated]"
			}
			add("shell", text, stamp, true)
		case "custom":
			readContent(m["content"], "extension", stamp, add)
		case "branchSummary", "compactionSummary":
			add("summary", str(m, "summary"), stamp, true)
		}
	case "compaction":
		add("compaction", str(r, "summary"), stamp, true)
	case "branch_summary":
		add("branch summary", str(r, "summary"), stamp, true)
	case "custom_message":
		readContent(r["content"], "extension", stamp, add)
	}
}
