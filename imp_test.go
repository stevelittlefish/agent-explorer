package main

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// impEntries chains records into an imp session tree, one minute apart.
func impEntries(records ...map[string]any) []any {
	var out []any
	parent := ""
	for i, r := range records {
		id := fmt.Sprintf("e%d", i+1)
		r["id"], r["timestamp"] = id, fmt.Sprintf("2026-07-01T10:%02d:00Z", i)
		if p, ok := r["parentId"]; ok {
			parent = p.(string)
		}
		if parent != "" {
			r["parentId"] = parent
		}
		parent = id
		out = append(out, r)
	}
	return out
}

func impUser(text string) map[string]any {
	return map[string]any{"type": "message", "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": text}}}}
}

func impAssistant(api string, in, read, write, out float64, content ...any) map[string]any {
	return map[string]any{"type": "message", "message": map[string]any{"role": "assistant", "api": api, "provider": "test", "model": "anthropic/claude-sonnet-4.6",
		"content": content, "usage": map[string]any{"input": in, "output": out, "cacheRead": read, "cacheWrite": write, "totalTokens": in + out, "cost": map[string]any{"total": 0.01}}}}
}

func impText(text string) map[string]any { return map[string]any{"type": "text", "text": text} }

func TestImpTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "home-nobody-no-such-project", "20260701T100000Z_x.jsonl")
	writeRecords(t, path, impEntries(
		impUser("Read the file"),
		impAssistant("anthropic-messages", 10, 0, 5000, 20,
			map[string]any{"type": "thinking", "thinking": "Look first"},
			map[string]any{"type": "toolCall", "id": "c1", "name": "read", "arguments": map[string]any{"path": "main.go"}}),
		map[string]any{"type": "message", "message": map[string]any{"role": "toolResult", "toolCallId": "c1", "toolName": "read", "isError": true, "content": []any{impText("no such file")}}},
		impAssistant("anthropic-messages", 0, 0, 0, 0),
		map[string]any{"type": "compaction", "summary": "Earlier work", "firstKeptEntryId": "e3"},
		impAssistant("anthropic-messages", 10, 5000, 40, 30, impText("Done")),
	)...)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"type":"message","id":"e7","parentId":"e6","message":{"role":"assistant","content":null,"usage":{},"stopReason":"error","errorMessage":"provider returned 404"}}` + "\n")
	f.Close()
	chat, err := readChat(path, "imp", true)
	if err != nil {
		t.Fatal(err)
	}
	if chat.Title != "Read the file" || chat.Project != "home-nobody-no-such-project" {
		t.Fatalf("title %q, project %q", chat.Title, chat.Project)
	}
	var roles []string
	for _, m := range chat.Messages {
		roles = append(roles, m.Role)
	}
	if got := strings.Join(roles, ","); got != "user,reasoning,tool,tool,compaction,assistant,error" {
		t.Fatal(got)
	}
	if !strings.HasPrefix(chat.Messages[3].Text, "[Error]\nno such file") || !chat.Messages[3].ToolResult || chat.Messages[6].Text != "provider returned 404" {
		t.Fatal("tool error or failed call missing")
	}
	if chat.Costs == nil || chat.Costs.Compactions != 1 || len(chat.Costs.PerCall) != 2 || !chat.Costs.PerCall[1].Compacted {
		t.Fatalf("costs %+v", chat.Costs)
	}
	if !chat.Usage.HasCost || chat.Usage.Cost < 0.0299 || chat.Usage.Cost > 0.0301 {
		t.Fatalf("recorded usage %+v", chat.Usage)
	}
	summary, err := readChat(path, "imp", false)
	if err != nil || len(summary.Messages) != 0 || summary.Costs != nil || summary.Title != chat.Title {
		t.Fatal("summary parsing failed", err)
	}
}

// OpenAI-compatible chat APIs count cache reads inside input; providers without
// a prompt cache read nothing, which must not look like a rebuild every call.
func TestImpCacheAccounting(t *testing.T) {
	dir := t.TempDir()
	turns := func(api string, cached bool) []any {
		var records []map[string]any
		ctx := 0.0
		for i := 0; i < 4; i++ {
			records = append(records, impUser(strings.Repeat("question ", 100)))
			ctx += 5000
			read := 0.0
			if cached && i != 2 { // the third call misses the cache
				read = ctx - 5000
			}
			in := ctx - read
			if api == "openai-chat" {
				in = ctx
			}
			records = append(records, impAssistant(api, in, read, 0, 50, impText("answer")))
		}
		return impEntries(records...)
	}
	for _, tc := range []struct {
		api      string
		cached   bool
		rebuilds int
	}{{"openai-chat", true, 1}, {"openai-responses", true, 1}, {"openai-chat", false, 0}} {
		path := filepath.Join(dir, tc.api+fmt.Sprint(tc.cached), "s.jsonl")
		writeRecords(t, path, turns(tc.api, tc.cached)...)
		chat, err := readChat(path, "imp", true)
		if err != nil {
			t.Fatal(err)
		}
		rebuilds := 0
		for _, m := range chat.Messages {
			if m.Role == "cache" {
				rebuilds++
			}
		}
		last := chat.Costs.PerCall[3].Use[roleMain]
		if rebuilds != tc.rebuilds || last[bInput]+last[bRead] != 20000 {
			t.Errorf("%s cached=%v: %d rebuilds, last call %v", tc.api, tc.cached, rebuilds, last)
		}
		if tc.cached && last[bRead] != 15000 {
			t.Errorf("%s: cache reads counted as input", tc.api)
		}
		v := priceRows(conversationRows(chat.Messages), &chat, pricingFor("imp", ""))
		noCache := strings.Contains(strings.Join(v.Notes, " "), "no prompt caching")
		if noCache == tc.cached {
			t.Errorf("%s cached=%v: notes %q", tc.api, tc.cached, v.Notes)
		}
	}
}

func TestImpProject(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "my-app", "web")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	slug := strings.ReplaceAll(strings.Trim(project, "/"), "/", "-")
	if got := impProject(filepath.Join(root, "sessions", slug, "s.jsonl")); got != project {
		t.Fatalf("got %q, want %q", got, project)
	}
	if got := impProject(filepath.Join(root, "sessions", "root", "s.jsonl")); got != "/" {
		t.Fatal(got)
	}
}

func TestImpPages(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "imp", "sessions", "work-gone", "s.jsonl")
	writeRecords(t, path, impEntries(impUser("Hello imp"), impAssistant("openai-chat", 3000, 0, 0, 40, impText("Hello Steve")))...)
	app := newApp(filepath.Join(root, "codex"), filepath.Join(root, "claude"), filepath.Join(root, "pi"), filepath.Join(root, "cursor"), filepath.Join(root, "imp"))
	base := "/imp/projects/" + key("work-gone") + "/chats/" + key(path)
	for _, url := range []string{"/", "/imp", base, base + "/analysis?price=gpt-6-sol"} {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
		body := w.Body.String()
		if w.Code != 200 || !strings.Contains(body, "imp") {
			t.Fatalf("%s: %d", url, w.Code)
		}
		selected := `<option value="` + defaultPriceModel + `" selected`
		if strings.Contains(url, "price=") {
			selected = `<option value="gpt-6-sol" selected`
		}
		if strings.HasPrefix(url, base) && (!strings.Contains(body, "Estimated $") || !strings.Contains(body, selected) || strings.Contains(body, "background calls")) {
			t.Fatalf("%s: cost estimate missing", url)
		}
	}
}

func TestOpenRouterModelIDs(t *testing.T) {
	if p, ok := priceFor("anthropic/claude-sonnet-4.6"); !ok || p.Model != "claude-sonnet-4-6" {
		t.Fatal("OpenRouter Claude ID not matched")
	}
	if p, ok := priceFor("openai/gpt-5.5"); !ok || p.Model != "gpt-5.5" {
		t.Fatal("OpenRouter OpenAI ID not matched")
	}
	if _, ok := priceFor("~anthropic/claude-sonnet-latest"); ok {
		t.Fatal("an alias was priced as a specific model")
	}
}
