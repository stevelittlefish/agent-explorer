package main

import (
	"os"
	"path/filepath"
	"strings"
)

// imp sessions are pi-style append-only trees: entries chain by parentId, and a
// leaf entry or a branch from an earlier turn moves the active tip. Entries are
// shown in file order, like pi; the original JSONL keeps the tree IDs.
type impSession struct {
	last string // id of the previous entry, to spot a switch to another branch
}

func (s *impSession) read(r map[string]any, stamp string, c *Chat, costs *costTracker, add func(string, string, string, bool)) {
	if costs != nil && s.last != "" && str(r, "parentId") != s.last {
		costs.boundary = true // the next call carries another branch's history
	}
	s.last = str(r, "id")
	from := len(c.Messages)
	switch str(r, "type") {
	case "message":
		m := obj(r, "message")
		switch role := str(m, "role"); role {
		case "user":
			readContent(m["content"], role, stamp, add)
		case "assistant":
			call, ok := impCall(m)
			if costs != nil && ok {
				costs.call(s.last, call, &c.Messages, stamp)
				from = len(c.Messages)
			}
			readContent(m["content"], role, stamp, add)
			add("error", str(m, "errorMessage"), stamp, true)
			if costs != nil {
				costs.added(c.Messages, from, len(c.Messages), ok)
			}
			return
		case "toolResult":
			text := contentText(m["content"])
			if m["isError"] == true {
				text = "[Error]\n" + text
			}
			add("tool_result", text, stamp, true)
		}
	case "compaction":
		if costs != nil {
			costs.boundary = true
			costs.cost.Compactions++
		}
		add("compaction", str(r, "summary"), stamp, true)
	case "active_tools_change":
		if costs != nil {
			costs.toolsChanged = true // tool definitions lead the prompt, so the cache starts again
		}
	}
	if costs != nil {
		costs.added(c.Messages, from, len(c.Messages), false)
	}
}

// impCall reads one assistant message's usage. imp records each provider's
// own accounting: OpenAI-compatible chat APIs count cache reads and writes
// inside input, the others report them separately.
func impCall(m map[string]any) (apiCall, bool) {
	u := obj(m, "usage")
	n := func(k string) float64 { f, _ := number(u, k); return f }
	write := n("cacheWrite")
	c := apiCall{model: str(m, "model"), in: n("input"), read: n("cacheRead"), w1h: min(n("cacheWrite1h"), write), out: n("output"), event: -1}
	c.w5 = write - c.w1h
	if str(m, "api") == "openai-chat" {
		c.in = max(c.in-c.read-write, 0)
	}
	return c, c.ctx()+c.out > 0
}

// imp names a session folder after its working directory with every "/" made
// "-" (home-steve-git-imp), which is lossy when folder names hold hyphens.
// Rebuild the path from folders that still exist; otherwise keep the label.
func impProject(path string) string {
	slug := filepath.Base(filepath.Dir(path))
	if slug == "root" {
		return "/"
	}
	parts := strings.Split(slug, "-")
	var find func(dir string, i int) string
	find = func(dir string, i int) string {
		for j := i; j < len(parts); j++ {
			name := strings.Join(parts[i:j+1], "-")
			if name == "" {
				continue
			}
			next := filepath.Join(dir, name)
			if info, err := os.Stat(next); err != nil || !info.IsDir() {
				continue
			}
			if j == len(parts)-1 {
				return next
			}
			if found := find(next, j+1); found != "" {
				return found
			}
		}
		return ""
	}
	if found := find("/", 0); found != "" {
		return found
	}
	return slug
}
