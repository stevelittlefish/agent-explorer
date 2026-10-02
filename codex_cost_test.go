package main

import (
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// A three-call Codex session: a tool call, a reply whose cached prefix falls
// the usual block short (not a rebuild), and a reply after the prompt cache
// missed. Newer Codex writes a token_usage_record straight after
// each call's output; older Codex only a token_count event, after the tool
// output that follows, sometimes repeated.
func codexSession(records bool) []string {
	total := 0
	usage := func(in, cached, out int) string {
		u := fmt.Sprintf(`{"input_tokens":%d,"cached_input_tokens":%d,"output_tokens":%d,"total_tokens":%d}`, in, cached, out, in+out)
		if records {
			return `{"type":"token_usage_record","payload":{"response_id":"r` + fmt.Sprint(in) + `","usage":` + u + `}}`
		}
		total += in + out
		return fmt.Sprintf(`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":%d},"last_token_usage":%s}}}`, total, u)
	}
	msg := func(role, text string) string {
		return fmt.Sprintf(`{"type":"response_item","timestamp":"2026-10-01T10:00:00Z","payload":{"type":"message","role":%q,"content":[{"type":"input_text","text":%q}]}}`, role, text)
	}
	first, second, third := usage(10000, 0, 100), usage(10200, 8900, 50), usage(10300, 0, 20)
	lines := []string{
		`{"type":"session_meta","payload":{"cwd":"/work","base_instructions":{"text":"You are Codex."}}}`,
		`{"type":"turn_context","payload":{"model":"gpt-6-sol"}}`,
		msg("developer", "Sandbox rules"),
		msg("user", "Hello"),
		`{"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"ls\"}"}}`,
	}
	output := `{"type":"response_item","payload":{"type":"function_call_output","output":"` + strings.Repeat("x", 800) + `"}}`
	if records {
		lines = append(lines, first, output)
	} else {
		lines = append(lines, output, first, first)
	}
	return append(lines, msg("assistant", "Done"), second, msg("user", "Next"), msg("assistant", "Again"), third)
}

// Every call at GPT-6.1 Sol prices: $2 input, $0.10 cached, $10 output.
const codexTotal = (20000 + 1000 + 2600 + 890 + 500 + 20600 + 200) / 1e6

func TestCodexCosts(t *testing.T) {
	for _, records := range []bool{true, false} {
		path := filepath.Join(t.TempDir(), "chat.jsonl")
		writeLines(t, path, codexSession(records))
		chat, err := readChat(path, "codex", true)
		if err != nil || chat.Costs == nil {
			t.Fatal(err, "no costs")
		}
		rows := conversationRows(chat.Messages)
		v := priceRows(rows, &chat, pricingFor("codex", ""))
		if v.Main.Model != defaultCodexPriceModel || v.Small.Model != "" || !near(v.Total, codexTotal) || chat.Costs.Calls["gpt-6-sol"] != 3 {
			t.Fatalf("records %v: priced as %s, total %v, want %v, calls %v", records, v.Main.Model, v.Total, codexTotal, chat.Costs.Calls)
		}
		var order []string
		for _, m := range chat.Messages {
			order = append(order, m.Role)
		}
		if got := strings.Join(order, " "); got != "system prompt developer user tool tool assistant user cache assistant" {
			t.Fatalf("records %v: rows %s", records, got)
		}
		if v.Rebuilds != 1 || !strings.Contains(chat.Messages[7].Text, "10,200 tokens sent again uncached") {
			t.Fatalf("records %v: rebuild row %+v", records, chat.Messages[7])
		}
		a := buildAnalysis(rows, &chat, v, "codex")
		for _, s := range a.Kinds.Slices {
			if s.Name == "MCP" || s.Name == "Skills" {
				t.Fatalf("Codex analysis shows %s", s.Name)
			}
		}
		for _, s := range a.Types.Slices {
			if s.Name == "Cache write" {
				t.Fatal("Codex analysis shows cache writes it never made")
			}
		}
	}
}

func TestCodexCostPages(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "codex", "sessions", "chat.jsonl")
	writeLines(t, path, codexSession(true))
	app := newApp(filepath.Join(root, "codex"), filepath.Join(root, "claude"), filepath.Join(root, "pi"), filepath.Join(root, "cursor"))
	base := "/codex/projects/" + key("/work") + "/chats/" + key(path)
	for _, url := range []string{base, base + "/analysis?price=gpt-6-astra"} {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
		body := w.Body.String()
		if w.Code != 200 || !strings.Contains(body, "Estimated $") || !strings.Contains(body, `<option value="gpt-5.5"`) || strings.Contains(body, "Opus") || strings.Contains(body, "background calls") {
			t.Fatalf("%s: %d", url, w.Code)
		}
	}
}

func TestCodexPriceTable(t *testing.T) {
	if _, ok := priceFor(defaultCodexPriceModel); !ok {
		t.Fatal("the default Codex model is not priced")
	}
	for _, p := range codexPriceTable {
		if p.Input <= 0 || p.Output <= 0 || p.Read <= 0 || p.Write5m <= 0 || p.Write1h != p.Write5m || p.Name == "" {
			t.Errorf("incomplete price row %+v", p)
		}
	}
	if pricingFor("claude", "gpt-6-sol").Main.Model != defaultPriceModel || pricingFor("codex", "claude-opus-5").Main.Model != defaultCodexPriceModel {
		t.Fatal("a session can be priced as another agent's model")
	}
}
