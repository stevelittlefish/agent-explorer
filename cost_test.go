package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// A four-call session: a tool call, a reply, a call that rebuilds the whole
// cache, and a final call that reads it normally. All writes use the 1 hour
// cache. Costs below are at Opus 5.5 prices ($4 in, $8 1h write, $0.20 read,
// $20 out per million tokens).
func costSession(extra ...string) []string {
	usage := func(in, read, write, out int) string {
		return fmt.Sprintf(`{"input_tokens":%d,"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":%d},"output_tokens":%d}`, in, read, write, write, out)
	}
	call := func(id, at, content string, in, read, write, out int) string {
		return fmt.Sprintf(`{"type":"assistant","timestamp":"2026-09-30T10:%s:00Z","message":{"id":%q,"model":"claude-opus-5-5","content":[%s],"usage":%s}}`, at, id, content, usage(in, read, write, out))
	}
	return append([]string{
		`{"type":"attachment","cwd":"/project","attachment":{"type":"prompt_snapshot","systemPrompt":["You are Claude Code."]}}`,
		`{"type":"user","timestamp":"2026-09-30T10:00:00Z","message":{"content":"Hello"}}`,
		call("a", "00", `{"type":"tool_use","name":"Read","input":{"path":"x"}}`, 2, 0, 10000, 100),
		`{"type":"user","message":{"content":[{"type":"tool_result","content":"` + strings.Repeat("x", 3000) + `"}]}}`,
		call("b", "01", `{"type":"text","text":"Done"}`, 2, 10000, 1102, 50),
		`{"type":"user","message":{"content":"Next"}}`,
		call("c", "02", `{"type":"text","text":"Again"}`, 2, 0, 11166, 20),
		call("d", "03", `{"type":"text","text":"End"}`, 2, 11168, 10, 5),
	}, extra...)
}

const sessionTotal = (32 + 3500 + 21168*0.2 + 22278*8) / 1e6 // every call priced on its own

func writeLines(t *testing.T, path string, lines []string) {
	t.Helper()
	var records []any
	for _, line := range lines {
		var r map[string]any
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatal(line, err)
		}
		records = append(records, r)
	}
	writeRecords(t, path, records...)
}

func pricedSession(t *testing.T, lines []string) (Chat, []conversationRow, *CostView) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "chat.jsonl")
	writeLines(t, path, lines)
	chat, err := readChat(path, "claude", true)
	if err != nil {
		t.Fatal(err)
	}
	rows := conversationRows(chat.Messages)
	return chat, rows, priceRows(rows, &chat, pricingFor(""))
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestCostAttributionAddsUp(t *testing.T) {
	_, rows, v := pricedSession(t, costSession())
	if v == nil || !near(v.Total, sessionTotal) {
		t.Fatalf("total %v, want %v", v, sessionTotal)
	}
	sum := 0.0
	byLabel := map[string]RowCost{}
	for _, r := range rows {
		if r.Price != nil {
			sum += r.Price.Total
			byLabel[rowLabel(r)] = *r.Price
		}
	}
	if !near(sum, sessionTotal) {
		t.Fatalf("rows add up to %v, want %v", sum, sessionTotal)
	}
	// Call c rebuilt the whole cache: 11,104 tokens written again at $8/M.
	rebuild, ok := byLabel["Prompt cache rebuilt"]
	if !ok || !near(rebuild.Total, 11104*8/1e6) {
		t.Fatalf("rebuild row %+v", rebuild)
	}
	// The tool result was sent once by call b, rebuilt by c, and re-read by d.
	tools := byLabel["1 tool call"]
	if tools.Reads != 1 || tools.Carried <= 0 || tools.Direct <= 0 {
		t.Fatalf("tool row %+v", tools)
	}
	if byLabel["System prompt"].Total <= tools.Total {
		t.Fatal("the system prompt prefix should be the costliest row here")
	}
}

func TestRecordedCostChecks(t *testing.T) {
	recorded := func(model string, cost float64) string {
		return fmt.Sprintf(`{"type":"cost-state","totalCostUSD":%v,"startTime":1790762400000,"modelUsage":{%q:{"inputTokens":8,"outputTokens":175,"cacheReadInputTokens":21168,"cacheCreationInputTokens":22278,"costUSD":%v}}}`, cost, model, cost)
	}
	cases := []struct {
		name, record, warning string
	}{
		{"matching prices", recorded("claude-opus-5-5", sessionTotal), ""},
		{"changed prices", recorded("claude-opus-5-5", sessionTotal*1.5), "Prices have changed"},
		{"unknown model", recorded("claude-opus-9", sessionTotal), "isn't in the price table"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, v := pricedSession(t, costSession(tc.record))
			got := strings.Join(v.Warnings, "\n")
			if tc.warning == "" && got != "" || !strings.Contains(got, tc.warning) {
				t.Fatalf("warnings %q, want %q", got, tc.warning)
			}
		})
	}
}

func TestRepricingWarnsAboutModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.jsonl")
	writeLines(t, path, costSession(fmt.Sprintf(`{"type":"cost-state","totalCostUSD":%v}`, sessionTotal)))
	chat, err := readChat(path, "claude", true)
	if err != nil {
		t.Fatal(err)
	}
	v := priceRows(conversationRows(chat.Messages), &chat, pricingFor("claude-fable-5-1"))
	if len(v.Warnings) != 1 || !strings.Contains(v.Warnings[0], "prices this session as Fable 5.1; it ran on Opus 5.5") {
		t.Fatalf("%q", v.Warnings)
	}
}

func TestResumedSessionComparesCoveredCalls(t *testing.T) {
	// Claude Code's tally restarted at 10:02, so it only covers calls c and d.
	covered := (8 + 11166*8 + 400 + 8 + 11168*0.2 + 80 + 100) / 1e6
	resumed := fmt.Sprintf(`{"type":"cost-state","totalCostUSD":%v,"startTime":%d}`, covered, int64(1790762520000))
	_, _, v := pricedSession(t, costSession(resumed))
	if len(v.Warnings) != 0 || len(v.Notes) != 1 || !strings.Contains(v.Notes[0], "last resumed") {
		t.Fatalf("warnings %q notes %q", v.Warnings, v.Notes)
	}
}

func TestSubagentCostJoinsItsToolCall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chat.jsonl")
	lines := costSession()
	lines[3] = `{"type":"user","toolUseResult":{"agentId":"abc"},"message":{"content":[{"type":"tool_result","content":"` + strings.Repeat("x", 3000) + `"}]}}`
	writeLines(t, path, lines)
	writeLines(t, filepath.Join(dir, "chat", "subagents", "agent-abc.jsonl"), []string{
		`{"type":"assistant","isSidechain":true,"message":{"id":"s","model":"claude-opus-5-5","usage":{"cache_creation_input_tokens":1000,"cache_creation":{"ephemeral_1h_input_tokens":1000},"output_tokens":100}}}`,
	})
	chat, err := readChat(path, "claude", true)
	if err != nil {
		t.Fatal(err)
	}
	rows := conversationRows(chat.Messages)
	v := priceRows(rows, &chat, pricingFor(""))
	if !near(v.Agents, 0.01) || !near(v.Total, sessionTotal+0.01) {
		t.Fatalf("subagents %v total %v", v.Agents, v.Total)
	}
}

func TestPriceLookup(t *testing.T) {
	for model, want := range map[string]string{"claude-haiku-4-5-20251001": "Haiku 4.5", "claude-opus-5-5": "Opus 5.5", "claude-opus-5": "Opus 5", "claude-opus-5-6": "", "<synthetic>": ""} {
		p, _ := priceFor(model)
		if p.Name != want {
			t.Errorf("%s: got %q, want %q", model, p.Name, want)
		}
	}
	for _, model := range []string{defaultPriceModel, smallPriceModel} {
		if _, ok := priceFor(model); !ok {
			t.Errorf("%s is not in the price table", model)
		}
	}
	for _, p := range priceTable {
		if p.Input <= 0 || p.Output <= 0 || p.Read <= 0 || p.Write5m <= 0 || p.Write1h <= 0 || p.Name == "" {
			t.Errorf("incomplete price row %+v", p)
		}
	}
}

func TestCostsOnPagesAndExport(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "claude", "projects", "project", "chat.jsonl")
	writeLines(t, path, costSession(fmt.Sprintf(`{"type":"cost-state","totalCostUSD":%v}`, sessionTotal)))
	app := newApp(filepath.Join(root, "codex"), filepath.Join(root, "claude"), filepath.Join(root, "pi"), filepath.Join(root, "cursor"))
	project := "/claude/projects/" + key("/project")
	base := project + "/chats/" + key(path)
	get := func(url string) string {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
		if w.Code != 200 {
			t.Fatal(url, w.Code)
		}
		return w.Body.String()
	}
	if body := get(project); !strings.Contains(body, "$0.186 recorded") {
		t.Fatal("chat list lacks the recorded cost")
	}
	for _, url := range []string{base, base + "/export?format=html&price=claude-sonnet-5"} {
		body := get(url)
		for _, want := range []string{"Estimated $", `class="row-cost"`, `class="cost-split">($`, "Prompt cache rebuilt", "Costliest", `aria-label="About this estimate"`, "Not a subscription bill"} {
			if !strings.Contains(body, want) {
				t.Fatalf("%s lacks %q", url, want)
			}
		}
		if strings.Contains(url, "export") != !strings.Contains(body, `name="price"`) {
			t.Fatal("the price menu belongs on the live page only")
		}
	}
	if !strings.Contains(get(base+"/export?format=html&price=claude-sonnet-5"), "at Sonnet 5 prices") {
		t.Fatal("export ignores the chosen price model")
	}
	if strings.Contains(get(base+"/export?format=txt"), "cache rebuilt") {
		t.Fatal("cost rows leak into the text export")
	}
}

func TestAnalysisCategoriesAndPage(t *testing.T) {
	lines := costSession(
		`{"type":"assistant","timestamp":"2026-09-30T10:04:00Z","message":{"id":"e","model":"claude-opus-5-5","content":[{"type":"tool_use","id":"s1","name":"Skill","input":{"skill":"x"}}],"usage":{"input_tokens":2,"cache_read_input_tokens":11180,"cache_creation_input_tokens":20,"output_tokens":10}}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"s1","content":"Launching skill: x"}]}}`,
		`{"type":"user","isMeta":true,"sourceToolUseID":"s1","message":{"content":[{"type":"text","text":"Base directory for this skill: /x`+strings.Repeat(" instructions", 200)+`"}]}}`,
		`{"type":"assistant","timestamp":"2026-09-30T10:05:00Z","message":{"id":"f","model":"claude-opus-5-5","content":[{"type":"text","text":"Ok"}],"usage":{"input_tokens":2,"cache_read_input_tokens":11202,"cache_creation_input_tokens":3000,"output_tokens":5}}}`,
	)
	lines[2] = `{"type":"assistant","timestamp":"2026-09-30T10:00:00Z","message":{"id":"a","model":"claude-opus-5-5","content":[{"type":"tool_use","id":"t1","name":"mcp__chrome__navigate","input":{"url":"x"}}],"usage":{"input_tokens":2,"cache_creation_input_tokens":10000,"output_tokens":100}}}`
	lines[3] = `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"` + strings.Repeat("x", 3000) + `"}]}}`
	chat, rows, v := pricedSession(t, lines)
	a := buildAnalysis(rows, &chat, v)
	sum := 0.0
	byName := map[string]float64{}
	for _, s := range a.Kinds.Slices {
		sum += s.Cost
		byName[s.Name] = s.Cost
	}
	if !near(sum, v.Total) {
		t.Fatalf("slices add up to %v, want %v", sum, v.Total)
	}
	for _, name := range []string{"MCP", "Skills", "User messages", "Responses & reasoning", "System prompt & tools", "Cache rebuilds"} {
		if byName[name] <= 0 {
			t.Errorf("%s has no cost: %v", name, byName)
		}
	}
	if byName["Tool calls & results"] != 0 {
		t.Errorf("the MCP and Skill calls were counted as plain tools: %v", byName)
	}
	typed := 0.0
	for _, s := range a.Types.Slices {
		typed += s.Cost
	}
	if !near(typed, v.Total) || len(a.Types.Slices) != 4 || a.Types.Slices[2].Name != "Cache read" || a.Types.Slices[2].Cost <= 0 {
		t.Fatalf("token types %+v add up to %v, want %v", a.Types.Slices, typed, v.Total)
	}
	if a.Calls != 6 || len(a.Context.Hits) != 6 || len(a.Context.Markers) != 1 {
		t.Fatalf("calls %d, hits %d, markers %d", a.Calls, len(a.Context.Hits), len(a.Context.Markers))
	}

	root := t.TempDir()
	path := filepath.Join(root, "claude", "projects", "project", "chat.jsonl")
	writeLines(t, path, lines)
	app := newApp(filepath.Join(root, "codex"), filepath.Join(root, "claude"), filepath.Join(root, "pi"), filepath.Join(root, "cursor"))
	base := "/claude/projects/" + key("/project") + "/chats/" + key(path)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest("GET", base, nil))
	if !strings.Contains(w.Body.String(), base+"/analysis?price=claude-opus-5-5") {
		t.Fatal("the chat page does not link to its analysis")
	}
	for _, url := range []string{base + "/analysis", base + "/analysis?format=html&price=claude-sonnet-5"} {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
		body := w.Body.String()
		if w.Code != 200 || !strings.Contains(body, "Cost by kind") || !strings.Contains(body, "Cost by token type") || !strings.Contains(body, `class="line-chart"`) {
			t.Fatalf("%s: %d", url, w.Code)
		}
		export := strings.Contains(url, "format=html")
		if export != strings.Contains(w.Header().Get("Content-Disposition"), "-analysis.html") || export && (strings.Contains(body, "<script") || !strings.Contains(body, "<style>") || !strings.Contains(body, "Sonnet 5 prices")) {
			t.Fatalf("%s is not a self-contained export", url)
		}
	}
}

func TestRebuildAfterMCPReconnect(t *testing.T) {
	lines := costSession()
	reconnect := `{"type":"attachment","attachment":{"type":"deferred_tools_delta","addedNames":["mcp__chrome__navigate"],"removedNames":[]}}`
	lines = append(lines[:6], append([]string{reconnect}, lines[6:]...)...)
	_, rows, _ := pricedSession(t, lines)
	for _, r := range rows {
		if r.Role == "cache" {
			if !strings.Contains(r.Text, "MCP server connected or disconnected") || category(r.Message) != catMCP {
				t.Fatalf("rebuild row %q", r.Text)
			}
			return
		}
	}
	t.Fatal("no rebuild row")
}
