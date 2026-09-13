package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecordedUsage(t *testing.T) {
	cases := []struct {
		name, agent, records string
		tokens, cost         float64
		hasTokens, hasCost   bool
	}{
		{"codex cumulative and duplicate formats", "codex", `
 {"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":100}}}}
 {"type":"token_usage_record","payload":{"usage":{"total_tokens":50},"thread_token_usage":{"input_tokens":120,"output_tokens":30,"cached_input_tokens":80,"reasoning_output_tokens":20}}}
 {"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":150}}}}
 {"type":"event_msg","payload":{"type":"token_count","info":null}}
 `, 150, 0, true, false},
		{"claude repeated message and cost snapshots", "claude", `
 {"type":"assistant","message":{"id":"a","usage":{"input_tokens":10,"output_tokens":2,"cache_read_input_tokens":100,"cache_creation_input_tokens":20}}}
 {"type":"assistant","message":{"id":"a","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":100,"cache_creation_input_tokens":20}}}
 {"type":"assistant","message":{"id":"a"}}
 {"type":"assistant","message":{"id":"b","usage":{"input_tokens":4,"output_tokens":1}}}
 {"type":"cost-state","totalCostUSD":0.1}
 {"type":"cost-state","totalCostUSD":0.25}
 `, 140, .25, true, true},
		{"pi per-message totals", "pi", `
 {"type":"message","id":"a","message":{"role":"assistant","usage":{"totalTokens":100,"input":50,"output":10,"cacheRead":40,"cost":{"total":0.2}}}}
 {"type":"message","id":"a","message":{"role":"assistant","usage":{"totalTokens":100,"cost":{"total":0.2}}}}
 {"type":"message","id":"b","message":{"role":"assistant","usage":{"input":10,"output":20,"cacheRead":30,"cacheWrite":40,"cost":{"total":0.3}}}}
 `, 200, .5, true, true},
		{"zero is present", "pi", `{"type":"message","message":{"role":"assistant","usage":{"totalTokens":0,"cost":{"total":0}}}}`, 0, 0, true, true},
		{"cost only", "claude", `{"type":"cost-state","totalCostUSD":0.01}`, 0, .01, false, true},
		{"invalid fields", "pi", `{"type":"message","message":{"role":"assistant","usage":{"totalTokens":-5,"input":"bad","cost":{"total":-1}}}}`, 0, 0, false, false},
		{"cursor absent", "cursor", `{"role":"assistant","message":{"content":[]}}`, 0, 0, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "chat.jsonl")
			if err := os.WriteFile(path, []byte(strings.TrimSpace(tc.records)), 0600); err != nil {
				t.Fatal(err)
			}
			for _, full := range []bool{false, true} {
				c, err := readChat(path, tc.agent, full)
				if err != nil {
					t.Fatal(err)
				}
				want := Usage{Tokens: tc.tokens, Cost: tc.cost, HasTokens: tc.hasTokens, HasCost: tc.hasCost}
				if c.Usage != want {
					t.Fatalf("full=%v: got %+v, want %+v", full, c.Usage, want)
				}
			}
		})
	}
}

func TestUsageOnChatAndHTMLExport(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "present"}[present], func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "claude", "projects", "project", "chat.jsonl")
			records := []any{map[string]any{"type": "user", "cwd": "/project", "message": map[string]any{"content": "Hello"}}}
			if present {
				for _, line := range []string{`{"type":"assistant","message":{"id":"a","usage":{"input_tokens":1200,"output_tokens":34}}}`, `{"type":"cost-state","totalCostUSD":0.1234}`} {
					var r map[string]any
					if err := json.Unmarshal([]byte(line), &r); err != nil {
						t.Fatal(err)
					}
					records = append(records, r)
				}
			}
			writeRecords(t, path, records...)
			app := newApp(filepath.Join(root, "codex"), filepath.Join(root, "claude"), filepath.Join(root, "pi"), filepath.Join(root, "cursor"))
			base := "/claude/projects/" + key("/project") + "/chats/" + key(path)
			for _, suffix := range []string{"", "/export?format=html"} {
				w := httptest.NewRecorder()
				app.ServeHTTP(w, httptest.NewRequest("GET", base+suffix, nil))
				if w.Code != 200 {
					t.Fatal(w.Code)
				}
				body := w.Body.String()
				if strings.Contains(body, `aria-label="Recorded usage"`) != present {
					t.Fatal("usage visibility incorrect")
				}
				if present && (!strings.Contains(body, "1,234 tokens") || !strings.Contains(body, "Recorded cost $0.1234 USD")) {
					t.Fatal("usage values missing")
				}
			}
		})
	}
}
