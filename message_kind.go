package main

import "strings"

// Track the two Codex representations independently: event messages are often
// duplicates of response items, and are used only for event-only transcripts.
type codexTurn struct {
	id                              string
	active, responseUser, eventUser bool
}

func (t *codexTurn) event(p map[string]any) {
	switch str(p, "type") {
	case "task_started":
		*t = codexTurn{id: str(p, "turn_id"), active: true}
	case "task_complete", "turn_aborted":
		if id := str(p, "turn_id"); id == "" || t.id == "" || id == t.id {
			*t = codexTurn{}
		}
	}
}

func (t *codexTurn) input(p map[string]any, text string, event bool) string {
	if !t.active {
		return ""
	}
	meta := obj(p, "internal_chat_message_metadata_passthrough")
	if id := str(meta, "turn_id"); id != "" && id != t.id {
		return ""
	}
	// Metadata distinguishes actual user input from injected instructions/context.
	if kinds, ok := meta["content_item_kinds"].([]any); ok {
		user := false
		for _, kind := range kinds {
			if s, ok := kind.(string); ok && strings.HasPrefix(s, "user.") {
				user = true
			}
		}
		if !user {
			return ""
		}
	} else {
		text = strings.TrimSpace(text)
		if text == "" || strings.HasPrefix(text, "# AGENTS.md") || strings.HasPrefix(text, "<environment_context>") || strings.HasPrefix(text, "<permissions instructions>") || strings.HasPrefix(text, "<turn_aborted>") {
			return ""
		}
	}
	seen := &t.responseUser
	if event {
		seen = &t.eventUser
	}
	kind := ""
	if *seen {
		kind = "Steering"
	}
	*seen = true
	return kind
}
