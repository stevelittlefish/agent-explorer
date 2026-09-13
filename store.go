package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Message struct {
	Role, Text, Time string
	Kind             string
	Detail           bool
	ToolResult       bool
}
type Chat struct {
	ID, ProjectID, Project, Title, Path string
	Updated                             time.Time
	Messages                            []Message
	Warnings                            int
	Usage                               Usage
}
type cachedChat struct {
	size     int64
	modified time.Time
	chat     Chat
}
type Store struct {
	Roots map[string][]string
	mu    sync.Mutex
	cache map[string]cachedChat
}

func key(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:16]) }
func (s *Store) List(agent string) ([]Chat, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cache == nil {
		s.cache = make(map[string]cachedChat)
	}
	var chats []Chat
	for _, root := range s.Roots[agent] {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if os.IsNotExist(err) && path == root {
				return nil
			}
			if err != nil {
				return err
			}
			if agent == "cursor" && d.IsDir() && path != root {
				rel, err := filepath.Rel(root, path)
				if err != nil {
					return err
				}
				parts := strings.Split(rel, string(filepath.Separator))
				if len(parts) == 2 && parts[1] != "agent-transcripts" {
					return filepath.SkipDir
				}
			}
			if agent == "cursor" && !d.IsDir() && cursorProject(path) == "" {
				return nil
			}
			if d.IsDir() || d.Type()&os.ModeSymlink != 0 || filepath.Ext(path) != ".jsonl" {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			cached, ok := s.cache[path]
			if !ok || cached.size != info.Size() || !cached.modified.Equal(info.ModTime()) {
				chat, err := readChat(path, agent, false)
				if err != nil {
					return fmt.Errorf("read %s: %w", path, err)
				}
				chat.Updated = info.ModTime()
				cached = cachedChat{info.Size(), info.ModTime(), chat}
				s.cache[path] = cached
			}
			chats = append(chats, cached.chat)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(chats, func(i, j int) bool {
		if chats[i].Updated.Equal(chats[j].Updated) {
			return chats[i].ID < chats[j].ID
		}
		return chats[i].Updated.After(chats[j].Updated)
	})
	return chats, nil
}
func str(m map[string]any, k string) string         { s, _ := m[k].(string); return s }
func obj(m map[string]any, k string) map[string]any { o, _ := m[k].(map[string]any); return o }
func pretty(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}
func short(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > 100 {
		return string(r[:100]) + "…"
	}
	return s
}
func readChat(path, agent string, full bool) (Chat, error) {
	c := Chat{ID: key(path), Path: path, Title: strings.TrimSuffix(filepath.Base(path), ".jsonl")}
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	reader := bufio.NewReader(f)
	var events []Message
	hasResponses := false
	titleFound := false
	var turn codexTurn
	var usage usageReader
	messageKind := ""
	add := func(role, text, stamp string, detail bool) {
		if strings.TrimSpace(text) == "" {
			return
		}
		if role == "user" && !detail && !titleFound && !strings.HasPrefix(strings.TrimSpace(text), "<environment_context>") && !strings.HasPrefix(strings.TrimSpace(text), "# AGENTS.md") {
			c.Title = short(text)
			titleFound = true
		}
		if full {
			isResult := role == "tool_result"
			if isResult {
				role = "tool"
			}
			kind := ""
			if role == "user" && !detail {
				kind = messageKind
			}
			c.Messages = append(c.Messages, Message{Kind: kind, Role: role, Text: text, Time: stamp, Detail: detail, ToolResult: isResult})
		}
	}
	for {
		line, e := reader.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var r map[string]any
			if json.Unmarshal(line, &r) != nil {
				c.Warnings++
			} else {
				messageKind = ""
				usage.read(agent, r)
				typ, stamp := str(r, "type"), str(r, "timestamp")
				if agent == "codex" {
					p := obj(r, "payload")
					switch typ {
					case "session_meta":
						if cwd := str(p, "cwd"); cwd != "" {
							c.Project = cwd
						}
					case "event_msg":
						turn.event(p)
						role := ""
						switch str(p, "type") {
						case "user_message":
							role = "user"
						case "agent_message":
							role = "assistant"
						}
						if role != "" {
							kind := ""
							if role == "user" {
								kind = turn.input(p, str(p, "message"), true)
							}
							events = append(events, Message{Role: role, Text: str(p, "message"), Time: stamp, Kind: kind})
						}
					case "response_item":
						switch str(p, "type") {
						case "message":
							role := str(p, "role")
							if role == "user" || role == "assistant" {
								hasResponses = true
							}
							if role == "user" {
								messageKind = turn.input(p, contentText(p["content"]), false)
							}
							readContent(p["content"], role, stamp, add)
						case "function_call", "custom_tool_call":
							v := p["arguments"]
							if v == nil {
								v = p["input"]
							}
							add("tool", str(p, "name")+"\n"+pretty(v), stamp, true)
						case "function_call_output", "custom_tool_call_output":
							add("tool_result", pretty(p["output"]), stamp, true)
						case "reasoning":
							readContent(p["summary"], "reasoning", stamp, add)
						}
					}
				} else if agent == "cursor" {
					if cwd := str(r, "cwd"); cwd != "" {
						c.Project = cwd
					}
					role := str(r, "role")
					if role == "user" || role == "assistant" {
						readContent(obj(r, "message")["content"], role, stamp, func(role, text, stamp string, detail bool) {
							if role == "user" && !detail {
								text = cursorUserText(text)
							}
							add(role, text, stamp, detail)
						})
					}
				} else if agent == "pi" {
					readPiRecord(r, stamp, &c, &titleFound, add)
				} else {
					if c.Project == "" {
						c.Project = str(r, "cwd")
					}
					switch typ {
					case "user", "assistant":
						p := obj(r, "message")
						if typ == "user" && str(r, "promptSource") == "queued" {
							messageKind = "Queued"
						}
						readContent(p["content"], typ, stamp, add)
					case "summary":
						if t := str(r, "summary"); t != "" {
							c.Title = short(t)
							titleFound = true
						}
					case "custom-title":
						if t := str(r, "customTitle"); t != "" {
							c.Title = t
							titleFound = true
						}
					case "ai-title":
						if t := str(r, "aiTitle"); t != "" {
							c.Title = t
							titleFound = true
						}
					}
				}
			}
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return c, e
		}
	}
	if agent == "codex" && !hasResponses {
		for _, m := range events {
			messageKind = m.Kind
			add(m.Role, m.Text, m.Time, false)
		}
	}
	if c.Project == "" {
		if agent == "cursor" {
			c.Project = cursorProject(path)
			if c.Project == "" {
				c.Project = "Unknown project"
			}
		} else if agent == "claude" || agent == "pi" {
			c.Project = filepath.Base(filepath.Dir(path))
		} else {
			c.Project = "Unknown project"
		}
	}
	c.Usage = usage.total
	c.ProjectID = key(c.Project)
	return c, nil
}
func readContent(v any, role, stamp string, add func(string, string, string, bool)) {
	detail := role != "user" && role != "assistant"
	if s, ok := v.(string); ok {
		add(role, s, stamp, detail)
		return
	}
	blocks, _ := v.([]any)
	var texts []string
	flush := func() {
		if len(texts) > 0 {
			add(role, strings.Join(texts, "\n\n"), stamp, detail)
			texts = nil
		}
	}
	for _, b := range blocks {
		m, ok := b.(map[string]any)
		if !ok {
			continue
		}
		switch str(m, "type") {
		case "text", "input_text", "output_text", "summary_text":
			texts = append(texts, str(m, "text"))
		case "thinking":
			flush()
			add("reasoning", str(m, "thinking"), stamp, true)
		case "toolCall":
			flush()
			add("tool", str(m, "name")+"\n"+pretty(m["arguments"]), stamp, true)
		case "tool_use":
			flush()
			add("tool", str(m, "name")+"\n"+pretty(m["input"]), stamp, true)
		case "tool_result":
			flush()
			add("tool_result", contentText(m["content"]), stamp, true)
		case "image", "input_image":
			texts = append(texts, "[Image attachment — available in the original JSONL]")
		default:
			texts = append(texts, "["+str(m, "type")+" content — available in the original JSONL]")
		}
	}
	flush()
}
func contentText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	var parts []string
	readContent(v, "tool", "", func(_, text, _ string, _ bool) { parts = append(parts, text) })
	if len(parts) == 0 {
		return pretty(v)
	}
	return strings.Join(parts, "\n\n")
}
