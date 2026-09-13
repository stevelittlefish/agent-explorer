package main

import (
	"fmt"
	"math"
	"strconv"
)

// Presence is separate from value: a recorded zero is different from no data.
type Usage struct {
	Tokens             float64
	Cost               float64
	HasTokens, HasCost bool
}

func (u Usage) Present() bool { return u.HasTokens || u.HasCost }
func (u Usage) TokenLabel() string {
	s := strconv.FormatFloat(u.Tokens, 'f', 0, 64)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
func (u Usage) CostLabel() string {
	if u.Cost > 0 && u.Cost < .0001 {
		return "< $0.0001 USD"
	}
	return fmt.Sprintf("$%.4f USD", u.Cost)
}
func number(m map[string]any, key string) (float64, bool) {
	n, ok := m[key].(float64)
	return n, ok && n >= 0 && !math.IsNaN(n) && !math.IsInf(n, 0)
}
func tokens(m map[string]any, total string, parts ...string) Usage {
	var u Usage
	if n, ok := number(m, total); ok {
		u.Tokens, u.HasTokens = n, true
		return u
	}
	for _, key := range parts {
		if n, ok := number(m, key); ok {
			u.Tokens += n
			u.HasTokens = true
		}
	}
	return u
}

type usageReader struct {
	total    Usage
	messages map[string]Usage
}

func (s *usageReader) read(agent string, r map[string]any) {
	switch agent {
	case "codex":
		p := obj(r, "payload")
		var m map[string]any
		if str(r, "type") == "token_usage_record" {
			m = obj(p, "thread_token_usage")
		}
		if str(r, "type") == "event_msg" && str(p, "type") == "token_count" {
			m = obj(obj(p, "info"), "total_token_usage")
		}
		// These are cumulative snapshots, including the duplicate event representation.
		// Reasoning and cached input are subsets, so do not add them again.
		u := tokens(m, "total_tokens", "input_tokens", "output_tokens")
		if u.HasTokens {
			s.total.Tokens, s.total.HasTokens = u.Tokens, true
		}
	case "claude":
		if str(r, "type") == "cost-state" {
			if n, ok := number(r, "totalCostUSD"); ok {
				s.total.Cost, s.total.HasCost = n, true
			}
		}
		if str(r, "type") != "assistant" {
			return
		}
		m := obj(r, "message")
		u := tokens(obj(m, "usage"), "total_tokens", "input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens")
		s.message(str(m, "id"), u)
	case "pi":
		if str(r, "type") != "message" {
			return
		}
		m := obj(r, "message")
		if str(m, "role") != "assistant" {
			return
		}
		v := obj(m, "usage")
		u := tokens(v, "totalTokens", "input", "output", "cacheRead", "cacheWrite")
		u.Cost, u.HasCost = number(obj(v, "cost"), "total")
		s.message(str(r, "id"), u)
	}
}
func (s *usageReader) message(id string, u Usage) {
	if id != "" {
		if s.messages == nil {
			s.messages = make(map[string]Usage)
		}
		old := s.messages[id]
		// Streamed content blocks can repeat the same API message. Keep its latest
		// available usage, rather than counting every block as another request.
		if u.HasTokens {
			s.total.Tokens -= old.Tokens
			old.Tokens, old.HasTokens = u.Tokens, true
		}
		if u.HasCost {
			s.total.Cost -= old.Cost
			old.Cost, old.HasCost = u.Cost, true
		}
		s.messages[id] = old
	}
	if u.HasTokens {
		s.total.Tokens += u.Tokens
		s.total.HasTokens = true
	}
	if u.HasCost {
		s.total.Cost += u.Cost
		s.total.HasCost = true
	}
}
