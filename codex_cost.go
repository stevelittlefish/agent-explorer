package main

import (
	"fmt"
	"strings"
)

// Cost attribution for Codex sessions, using the same costTracker as Claude
// Code. Codex writes each call's usage after the rows it produced: newer
// versions add a token_usage_record per response straight after the call's
// output, older ones only a token_count event, which lands after the tool
// output that follows. So rows and usage are collected while reading, and
// replay feeds them to the tracker in call order: each call just before the
// run of output rows it produced.
//
// Codex counts cached and cache-write tokens inside input_tokens, and
// reasoning inside output_tokens.

type codexEvent struct {
	from, to int      // rows [from, to)
	call     *apiCall // or a call
	id       string
	stamp    string
	compact  bool // or a compaction
}

type codexCosts struct {
	model     string
	events    []codexEvent
	mark      int  // rows before this are already in events
	records   bool // token_usage_record seen: ignore token_count events
	lastTotal float64
	calls     int
}

// read notes one Codex record. Call it after the record's rows are added.
func (x *codexCosts) read(r map[string]any, msgs []Message) {
	p := obj(r, "payload")
	switch str(r, "type") {
	case "turn_context":
		if m := str(p, "model"); m != "" {
			x.model = m
		}
	case "compacted":
		x.rows(msgs)
		x.events = append(x.events, codexEvent{compact: true})
	case "token_usage_record":
		x.records = true
		x.usage(obj(p, "usage"), str(p, "response_id"), str(r, "timestamp"), msgs)
	case "event_msg":
		if str(p, "type") != "token_count" || x.records {
			return
		}
		info := obj(p, "info")
		total, _ := number(obj(info, "total_token_usage"), "total_tokens")
		if total == x.lastTotal {
			return // Codex repeats the last count, for instance around rate limit updates
		}
		x.lastTotal = total
		x.usage(obj(info, "last_token_usage"), fmt.Sprintf("count-%d", x.calls), str(r, "timestamp"), msgs)
	}
}

func (x *codexCosts) usage(u map[string]any, id, stamp string, msgs []Message) {
	n := func(k string) float64 { f, _ := number(u, k); return f }
	c := apiCall{model: x.model, read: n("cached_input_tokens"), w5: n("cache_write_input_tokens"), out: n("output_tokens"), event: -1}
	c.in = max(n("input_tokens")-c.read-c.w5, 0)
	if c.ctx()+c.out == 0 {
		return
	}
	x.rows(msgs)
	x.events = append(x.events, codexEvent{call: &c, id: id, stamp: stamp})
	x.calls++
}

func (x *codexCosts) rows(msgs []Message) {
	if len(msgs) > x.mark {
		x.events = append(x.events, codexEvent{from: x.mark, to: len(msgs)})
		x.mark = len(msgs)
	}
}

// codexOutput tells rows the model produced from rows sent to it.
func codexOutput(m Message) bool {
	return m.Role == "assistant" || m.Role == "reasoning" || m.Role == "tool" && !m.ToolResult
}

// replay runs the tracker over msgs in call order and returns the rows with
// any cache rebuild rows inserted where they happened.
func (x *codexCosts) replay(t *costTracker, msgs []Message) []Message {
	x.rows(msgs)
	// Split row ranges into single rows, then move each call back to the start
	// of the latest run of output rows not yet claimed by a call.
	type step struct {
		row  int
		call int // index into x.events, or -1
	}
	var steps []step
	open := -1 // position in steps where the unclaimed output run starts
	for i, e := range x.events {
		switch {
		case e.call != nil:
			s := step{-1, i}
			if open >= 0 {
				steps = append(steps[:open+1], steps[open:]...)
				steps[open] = s
				open = -1
			} else {
				steps = append(steps, s)
			}
		case e.compact:
			steps = append(steps, step{-1, i})
			open = -1
		default:
			for r := e.from; r < e.to; r++ {
				if codexOutput(msgs[r]) {
					if open < 0 || steps[len(steps)-1].row < 0 || !codexOutput(msgs[steps[len(steps)-1].row]) {
						open = len(steps)
					}
				}
				steps = append(steps, step{r, -1})
			}
		}
	}
	var out []Message
	for _, s := range steps {
		if s.row < 0 {
			e := x.events[s.call]
			if e.compact {
				t.boundary = true
				t.cost.Compactions++
				continue
			}
			t.call(e.id, *e.call, &out, e.stamp)
			continue
		}
		out = append(out, msgs[s.row])
		t.added(out, len(out)-1, len(out), codexOutput(msgs[s.row]))
	}
	return out
}

// spawnedAgents counts Codex spawn_agent calls, whose work runs in sessions of
// its own and is not part of this estimate.
func spawnedAgents(msgs []Message) int {
	n := 0
	for _, m := range msgs {
		if m.Role == "tool" && !m.ToolResult && strings.HasPrefix(m.Text, "spawn_agent\n") {
			n++
		}
	}
	return n
}
