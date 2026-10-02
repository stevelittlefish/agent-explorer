package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Cost attribution for Claude Code sessions.
//
// Every API call records how much of its context was read from the prompt
// cache, written to it, or sent uncached. Until a compaction the context only
// grows by appending, so the new tokens in call N are exactly what reached the
// transcript after call N-1 started: that call's output plus the tool results
// and messages that followed. Laying transcript items end to end gives each
// one a fixed token range, and each later call's read, write and input charges
// fall on whichever ranges they cover. The only estimate is how one step's new
// tokens divide between its items, which is by character count.
//
// Attribution is in tokens. Prices are applied afterwards (see Pricing), so a
// session can be repriced as any model without reading it again.

const (
	bOutput = iota
	bInput
	bRead
	bWrite5m
	bWrite1h
	bSearch
	nBuckets
)

// Calls by the small background model (Haiku) are priced separately from
// calls by the main model.
const (
	roleMain = iota
	roleSmall
)

// TokenUse counts tokens per bucket and role. bSearch counts web searches.
type TokenUse [2][nBuckets]float64

func (t *TokenUse) add(o TokenUse) {
	for r := range t {
		for b := range t[r] {
			t[r][b] += o[r][b]
		}
	}
}

type ItemCost struct {
	Direct   TokenUse // the call that produced the item, or first sent it
	Carried  TokenUse // later calls that re-read or re-sent it
	Subagent TokenUse // calls made by a subagent this item started
	Reads    int      // later calls that carried it
}

// RecordedModel is Claude Code's own per-model tally from cost-state.
type RecordedModel struct {
	Input, Output, Read, Write, Searches, Cost float64
}

type SessionCost struct {
	Overhead     TokenUse // system prompt and tools, when no system prompt row exists
	Unattributed TokenUse // tokens no visible row can take, such as thinking-only calls
	Calls        map[string]int
	Writes       map[string][2]float64 // per model: 5m and 1h cache-write tokens
	Fast         bool
	Away         int // away summaries and compactions: background calls the transcript leaves out
	Compactions  int
	PerCall      []timedUse // each call's own usage, to compare with a partial recorded total
}

type timedUse struct {
	At                 time.Time
	Use                TokenUse
	Compacted, Rebuilt bool
}

const rebuildMinimum = 1024 // tokens; smaller shortfalls are normal cache-boundary noise

type apiCall struct {
	model                        string
	role                         int
	in, read, w5, w1h, out, srch float64
	at                           time.Time
	epoch                        bool    // first call, or first after a compaction
	event                        int     // index of a cache-rebuild row, or -1
	prevCtx                      float64 // context size of the previous call
}

func (c apiCall) ctx() float64 { return c.in + c.read + c.w5 + c.w1h }

type segment struct{ outputs, inputs []int }

type costTracker struct {
	calls    []apiCall
	byID     map[string]int
	segs     []segment // segs[0] is before the first call; segs[n+1] starts with call n's output
	boundary bool
	// toolsChanged records a change to the available tools since the last
	// call; mcpChanged that an MCP server's tools or instructions changed.
	toolsChanged, mcpChanged bool
	sysPrompt                int
	subagents                map[int]string // tool result row -> subagent id
	lastTime                 time.Time
	cost                     SessionCost
}

func newCostTracker() *costTracker {
	return &costTracker{byID: map[string]int{}, segs: []segment{{}}, sysPrompt: -1, subagents: map[int]string{},
		cost: SessionCost{Calls: map[string]int{}, Writes: map[string][2]float64{}}}
}

func readCall(m map[string]any) (apiCall, bool) {
	u := obj(m, "usage")
	n := func(v map[string]any, k string) float64 { f, _ := number(v, k); return f }
	c := apiCall{model: str(m, "model"), in: n(u, "input_tokens"), read: n(u, "cache_read_input_tokens"), out: n(u, "output_tokens"),
		srch: n(obj(u, "server_tool_use"), "web_search_requests"), event: -1}
	write := n(u, "cache_creation_input_tokens")
	if cc := obj(u, "cache_creation"); cc != nil {
		c.w5, c.w1h = n(cc, "ephemeral_5m_input_tokens"), n(cc, "ephemeral_1h_input_tokens")
	}
	if c.w5+c.w1h != write {
		c.w5, c.w1h = write, 0 // no breakdown: the API default is the 5 minute cache
	}
	if strings.Contains(c.model, "haiku") {
		c.role = roleSmall
	}
	return c, c.ctx()+c.out > 0
}

// assistant is called before an assistant record's content is added. It may
// append a cache-rebuild row, which belongs to no segment.
func (t *costTracker) assistant(r map[string]any, msgs *[]Message, stamp string) {
	m := obj(r, "message")
	c, ok := readCall(m)
	if !ok {
		return
	}
	if str(obj(m, "usage"), "speed") == "fast" {
		t.cost.Fast = true
	}
	t.call(str(m, "id"), c, msgs, stamp)
}

// call records one API call. It may append a cache-rebuild row.
func (t *costTracker) call(id string, c apiCall, msgs *[]Message, stamp string) {
	if i, seen := t.byID[id]; seen {
		old := t.calls[i]
		c.epoch, c.event, c.prevCtx, c.at = old.epoch, old.event, old.prevCtx, old.at
		t.calls[i] = c // streamed blocks repeat the message; keep its latest usage
		return
	}
	now, _ := time.Parse(time.RFC3339Nano, stamp)
	c.at = now
	if len(t.calls) == 0 {
		c.epoch = true
	} else {
		c.prevCtx = t.calls[len(t.calls)-1].ctx()
		if t.boundary || c.ctx()+rebuildMinimum < c.prevCtx {
			c.epoch = true
		} else if c.read+rebuildMinimum < c.prevCtx {
			cause := Message{Role: "cache", Time: stamp}
			again := "written again"
			if c.w5+c.w1h == 0 {
				again = "sent again uncached" // providers that charge nothing extra to fill the cache
			}
			text := fmt.Sprintf("%s tokens %s", Usage{Tokens: c.prevCtx - c.read}.TokenLabel(), again)
			// The cache is a prefix and tool definitions come first, so any change
			// to the tools invalidates all of it. Otherwise the cache may simply
			// have expired: entries live 5 minutes, or an hour if written that way.
			prev := t.calls[len(t.calls)-1]
			ttl := 5 * time.Minute
			if prev.w1h > 0 {
				ttl = time.Hour
			}
			gap := now.Sub(t.lastTime)
			if search := t.loadedTools(*msgs); search != nil {
				text += " after ToolSearch loaded more tools"
				cause.Tool, cause.MCP = "ToolSearch", search.MCP
			} else if t.toolsChanged {
				text += " after the available tools changed"
				if t.mcpChanged {
					text += " (an MCP server connected or disconnected)"
				}
				cause.MCP = t.mcpChanged
			} else if !t.lastTime.IsZero() && !now.IsZero() && gap >= ttl {
				text += " after " + idle(gap) + " idle, longer than the cache lasts"
			}
			c.event = len(*msgs)
			cause.Text = text
			*msgs = append(*msgs, cause)
		}
	}
	t.boundary, t.toolsChanged, t.mcpChanged = false, false, false
	if !now.IsZero() {
		t.lastTime = now
	}
	t.byID[id] = len(t.calls)
	t.calls = append(t.calls, c)
	t.segs = append(t.segs, segment{})
	t.cost.Calls[c.model]++
	w := t.cost.Writes[c.model]
	t.cost.Writes[c.model] = [2]float64{w[0] + c.w5, w[1] + c.w1h}
}

// loadedTools finds a ToolSearch call since the previous API call.
func (t *costTracker) loadedTools(msgs []Message) *Message {
	s := t.segs[len(t.segs)-1]
	for _, i := range append(s.outputs, s.inputs...) {
		if msgs[i].Role == "tool" && !msgs[i].ToolResult && strings.HasPrefix(msgs[i].Text, "ToolSearch\n") {
			return &msgs[i]
		}
	}
	return nil
}

// attachment notes Claude Code's records of the tool list changing.
func (t *costTracker) attachment(a map[string]any) {
	kind := str(a, "type")
	if kind != "deferred_tools_delta" && kind != "mcp_instructions_delta" {
		return
	}
	var names []any
	for _, k := range []string{"addedNames", "removedNames"} {
		list, _ := a[k].([]any)
		names = append(names, list...)
	}
	if len(names) == 0 {
		return
	}
	t.toolsChanged = true
	for _, n := range names {
		if s, _ := n.(string); kind == "mcp_instructions_delta" || strings.HasPrefix(s, "mcp__") {
			t.mcpChanged = true
		}
	}
}

func idle(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}

// added records rows [from, to) as output of the latest call or as input.
func (t *costTracker) added(msgs []Message, from, to int, output bool) {
	s := &t.segs[len(t.segs)-1]
	for i := from; i < to; i++ {
		if msgs[i].Role == "system prompt" && t.sysPrompt < 0 && len(t.calls) == 0 {
			t.sysPrompt = i
			continue
		}
		if output && len(t.calls) > 0 {
			s.outputs = append(s.outputs, i)
		} else {
			s.inputs = append(s.inputs, i)
		}
	}
}

// subagent links the tool result rows just added to the subagent they ran.
func (t *costTracker) subagent(r map[string]any, msgs []Message, from int) {
	id := str(obj(r, "toolUseResult"), "agentId")
	for i := len(msgs) - 1; id != "" && i >= from; i-- {
		if msgs[i].ToolResult {
			t.subagents[i] = id
			return
		}
	}
}

type span struct {
	item       int // row index, or overheadItem / unattributedItem
	start, end float64
	born       int
}

const (
	overheadItem     = -1
	unattributedItem = -2
)

// finish lays out the context and charges every call to the rows it covered.
func (t *costTracker) finish(msgs []Message, path string) *SessionCost {
	if len(t.calls) == 0 {
		return nil
	}
	sc := &t.cost
	use := func(item int) *ItemCost {
		if item < 0 {
			return nil
		}
		if msgs[item].Cost == nil {
			msgs[item].Cost = &ItemCost{}
		}
		return msgs[item].Cost
	}
	chars := func(items []int) (total float64) {
		for _, i := range items {
			total += weight(msgs[i])
		}
		return
	}
	var layout []span
	overhead := 0.0
	overheadRow := t.sysPrompt
	if overheadRow < 0 {
		overheadRow = overheadItem
	}
	for _, c := range t.calls {
		var u TokenUse
		u[c.role] = [nBuckets]float64{c.out, c.in, c.read, c.w5, c.w1h, c.srch}
		sc.PerCall = append(sc.PerCall, timedUse{c.at, u, c.epoch && len(sc.PerCall) > 0, c.event >= 0})
	}
	for j, c := range t.calls {
		s := t.segs[j]
		ctx := c.ctx()
		end := 0.0
		if len(layout) > 0 {
			end = layout[len(layout)-1].end
		}
		place := func(items []int, tokens float64) {
			if tokens <= 0 {
				return
			}
			if len(items) == 0 {
				layout = append(layout, span{unattributedItem, end, end + tokens, j})
				end += tokens
				return
			}
			total := chars(items)
			for _, i := range items {
				n := tokens * weight(msgs[i]) / total
				layout = append(layout, span{i, end, end + n, j})
				end += n
			}
		}
		growth := 0.0
		if c.epoch {
			layout, end = nil, 0
			if j == 0 {
				// The first prefix holds the system prompt and tool definitions,
				// which the transcript does not size, plus the opening messages.
				overhead = math.Max(ctx-chars(s.inputs)/3, 0)
			}
			first := math.Min(overhead, ctx)
			if j > 0 && c.read > 0 {
				first = math.Min(first, c.read) // after a compaction only the stable prefix is still cached
			}
			layout = append(layout, span{overheadRow, 0, first, j})
			end = first
			growth = ctx - first
			place(append(append([]int{}, s.outputs...), s.inputs...), growth)
		} else {
			growth = math.Max(ctx-end, 0)
			out := 0.0
			if len(s.outputs) > 0 {
				out = math.Min(t.calls[j-1].out, growth)
			}
			if len(s.inputs) == 0 {
				out = growth
			}
			place(s.outputs, out)
			place(s.inputs, growth-out)
		}

		// Charge this call's input side to the ranges it covered.
		write := c.w5 + c.w1h
		share1h := 0.0
		if write > 0 {
			share1h = c.w1h / write
		}
		lo, hi := 0.0, 0.0
		if c.event >= 0 {
			lo, hi = c.read, c.prevCtx
		}
		for _, sp := range layout {
			overlap := func(a, b float64) float64 { return math.Max(0, math.Min(sp.end, b)-math.Max(sp.start, a)) }
			between := func(a, b float64) float64 { // part of [a,b) that is also in the rebuilt range
				return math.Max(0, math.Min(math.Min(sp.end, b), hi)-math.Max(math.Max(sp.start, a), lo))
			}
			read := overlap(0, c.read)
			wr := overlap(c.read, c.read+write)
			in := overlap(c.read+write, ctx)
			rw, ri := between(c.read, c.read+write), between(c.read+write, ctx)
			var u, ev TokenUse
			u[c.role][bRead] = read
			u[c.role][bWrite5m] = (wr - rw) * (1 - share1h)
			u[c.role][bWrite1h] = (wr - rw) * share1h
			u[c.role][bInput] = in - ri
			ev[c.role][bWrite5m] = rw * (1 - share1h)
			ev[c.role][bWrite1h] = rw * share1h
			ev[c.role][bInput] = ri
			if rw+ri > 0 {
				use(c.event).Direct.add(ev)
			}
			if read+wr+in-rw-ri <= 0 {
				continue
			}
			switch {
			case sp.item == overheadItem:
				sc.Overhead.add(u)
			case sp.item == unattributedItem:
				sc.Unattributed.add(u)
			case sp.born == j:
				use(sp.item).Direct.add(u)
			default:
				ic := use(sp.item)
				ic.Carried.add(u)
				ic.Reads++
			}
		}

		// Output is charged to the rows this call produced.
		var out TokenUse
		out[c.role][bOutput] = c.out
		out[c.role][bSearch] = c.srch
		produced := t.segs[j+1].outputs
		if len(produced) == 0 {
			sc.Unattributed.add(out)
			continue
		}
		total := chars(produced)
		for _, i := range produced {
			var part TokenUse
			for b := range out[c.role] {
				part[c.role][b] = out[c.role][b] * weight(msgs[i]) / total
			}
			use(i).Direct.add(part)
		}
	}
	for i, id := range t.subagents {
		file := filepath.Join(strings.TrimSuffix(path, ".jsonl"), "subagents", "agent-"+id+".jsonl")
		if u, ok := subagentUse(file); ok {
			use(i).Subagent.add(u)
		}
	}
	return sc
}

func weight(m Message) float64 { return float64(len(m.Text) + 1) }

// subagentUse totals a subagent transcript's calls. They are not broken down
// by row: the whole run is part of what the tool call that started it cost.
func subagentUse(path string) (TokenUse, bool) {
	var u TokenUse
	f, err := os.Open(path)
	if err != nil {
		return u, false
	}
	defer f.Close()
	calls := map[string]apiCall{}
	var order []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for scanner.Scan() {
		var r map[string]any
		if json.Unmarshal(scanner.Bytes(), &r) != nil || str(r, "type") != "assistant" {
			continue
		}
		m := obj(r, "message")
		if c, ok := readCall(m); ok {
			if _, seen := calls[str(m, "id")]; !seen {
				order = append(order, str(m, "id"))
			}
			calls[str(m, "id")] = c
		}
	}
	for _, id := range order {
		c := calls[id]
		u[c.role][bInput] += c.in
		u[c.role][bRead] += c.read
		u[c.role][bWrite5m] += c.w5
		u[c.role][bWrite1h] += c.w1h
		u[c.role][bOutput] += c.out
		u[c.role][bSearch] += c.srch
	}
	return u, len(order) > 0
}
