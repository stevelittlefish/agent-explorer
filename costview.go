package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// Pricing prices a session as if every main-model call ran on Main and every
// background (Haiku) call on Small. The aim is to judge a workflow at today's
// prices, so older sessions are deliberately repriced rather than priced as run.
type Pricing struct{ Main, Small Price }

func pricingFor(model string) Pricing {
	main, ok := priceFor(model)
	if !ok {
		main, _ = priceFor(defaultPriceModel)
	}
	small, _ := priceFor(smallPriceModel)
	return Pricing{main, small}
}

func (p Pricing) bucket(t TokenUse, b int) float64 {
	sum := 0.0
	for role, pr := range []Price{p.Main, p.Small} {
		rate := [nBuckets]float64{pr.Output, pr.Input, pr.Read, pr.Write5m, pr.Write1h, webSearchPrice * 1e6}[b]
		sum += t[role][b] * rate / 1e6
	}
	return sum
}

func (p Pricing) cost(t TokenUse) float64 {
	sum := 0.0
	for b := 0; b < nBuckets; b++ {
		sum += p.bucket(t, b)
	}
	return sum
}

type RowCost struct {
	Total, Direct, Carried, Subagent float64
	Reads                            int
	Bar                              int // percent of the costliest row
}

func (c RowCost) Label() string { return money(c.Total) }

// Made and Repeated split the total: the cost when the row was produced
// (including any subagent it ran) and what later calls paid to carry it.
func (c RowCost) Made() string     { return money(c.Direct + c.Subagent) }
func (c RowCost) Repeated() string { return money(c.Carried) }

// Detail explains where a row's cost came from.
func (c RowCost) Detail() string {
	parts := []string{money(c.Direct) + " when made"}
	if c.Carried > 0 {
		parts = append(parts, fmt.Sprintf("%s re-read by %d later %s", money(c.Carried), c.Reads, plural(c.Reads, "call")))
	}
	if c.Subagent > 0 {
		parts = append(parts, money(c.Subagent)+" subagent")
	}
	return strings.Join(parts, " + ")
}

func plural(n int, noun string) string {
	switch {
	case n == 1:
		return noun
	case strings.HasSuffix(noun, "y"):
		return noun[:len(noun)-1] + "ies"
	}
	return noun + "s"
}

func money(v float64) string {
	switch {
	case v == 0:
		return "$0"
	case v < 0.001:
		return "<$0.001"
	case v < 1:
		return fmt.Sprintf("$%.3f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}

type costliest struct {
	Anchor, Label string
	Cost          RowCost
}

type CostView struct {
	Pricing
	Options                                           []Price
	Total, Output, Write, Read, Input, Search, Agents float64
	Warnings, Notes                                   []string
	Costliest                                         []costliest
	Rebuilds                                          int     // prompt cache rebuild rows
	RebuildCost                                       float64 // what writing the cache again cost
	FirstRebuild                                      string  // anchor of the first rebuild row
}

func (v *CostView) Money(f float64) string { return money(f) }

// priceRows prices each row and the session. It returns nil when the chat has
// no Claude Code usage to estimate from.
func priceRows(rows []conversationRow, chat *Chat, p Pricing) *CostView {
	if chat.Costs == nil {
		return nil
	}
	v := &CostView{Pricing: p, Options: priceTable}
	var all TokenUse
	add := func(t TokenUse) { all.add(t) }
	add(chat.Costs.Overhead)
	add(chat.Costs.Unattributed)
	maxRow := 0.0
	for i := range rows {
		items := rows[i].Tools
		if items == nil {
			items = []Message{rows[i].Message}
		}
		var rc RowCost
		has := false
		for _, m := range items {
			if m.Cost == nil {
				continue
			}
			has = true
			add(m.Cost.Direct)
			add(m.Cost.Carried)
			rc.Direct += p.cost(m.Cost.Direct)
			rc.Carried += p.cost(m.Cost.Carried)
			rc.Subagent += p.cost(m.Cost.Subagent)
			rc.Reads = max(rc.Reads, m.Cost.Reads)
			v.Agents += p.cost(m.Cost.Subagent)
		}
		if !has {
			continue
		}
		rc.Total = rc.Direct + rc.Carried + rc.Subagent
		rows[i].Price = &rc
		if rows[i].Role == "cache" {
			if v.Rebuilds == 0 {
				v.FirstRebuild = rows[i].Anchor
			}
			v.Rebuilds++
			v.RebuildCost += rc.Total
		}
		maxRow = math.Max(maxRow, rc.Total)
		v.Costliest = append(v.Costliest, costliest{rows[i].Anchor, rowLabel(rows[i]), rc})
	}
	for i := range rows {
		if rows[i].Price != nil && maxRow > 0 {
			rows[i].Price.Bar = int(math.Round(100 * rows[i].Price.Total / maxRow))
		}
	}
	sort.SliceStable(v.Costliest, func(i, j int) bool { return v.Costliest[i].Cost.Total > v.Costliest[j].Cost.Total })
	if len(v.Costliest) > 10 {
		v.Costliest = v.Costliest[:10]
	}
	v.Output, v.Input, v.Read, v.Search = p.bucket(all, bOutput), p.bucket(all, bInput), p.bucket(all, bRead), p.bucket(all, bSearch)
	v.Write = p.bucket(all, bWrite5m) + p.bucket(all, bWrite1h)
	v.Total = v.Output + v.Input + v.Read + v.Write + v.Search + v.Agents
	v.check(chat)
	return v
}

func rowLabel(r conversationRow) string {
	switch {
	case r.Tools != nil:
		return r.Label
	case r.Role == "cache":
		return "Prompt cache rebuilt"
	case r.Detail:
		return strings.ToUpper(r.Role[:1]) + r.Role[1:]
	case r.Role == "user":
		return "You: " + short(r.Text)
	}
	return "Claude: " + short(r.Text)
}

// check compares the estimate with what Claude Code recorded and notes where
// the estimate is known to be rough.
func (v *CostView) check(chat *Chat) {
	sc := chat.Costs
	var ran []string
	for model := range sc.Calls {
		if !strings.Contains(model, "haiku") {
			ran = append(ran, model)
		}
	}
	sort.Slice(ran, func(i, j int) bool { return sc.Calls[ran[i]] > sc.Calls[ran[j]] })
	repriced := len(ran) > 0 && !sameModel(ran[0], v.Main.Model)

	tableWrong := false
	var models []string
	for model := range chat.Recorded {
		models = append(models, model)
	}
	sort.Strings(models)
	for _, model := range models {
		rec := chat.Recorded[model]
		pr, ok := priceFor(model)
		if !ok {
			v.Warnings = append(v.Warnings, fmt.Sprintf("Claude Code recorded usage for %s, which isn't in the price table. Ask Claude to update the prices.", model))
			tableWrong = true
			continue
		}
		if rec.Cost < 0.01 {
			continue
		}
		w := sc.Writes[model]
		share1h := 1.0 // Claude Code's current default when the transcript has no writes to go by
		if w[0]+w[1] > 0 {
			share1h = w[1] / (w[0] + w[1])
		}
		writeRate := pr.Write5m*(1-share1h) + pr.Write1h*share1h
		expected := (rec.Input*pr.Input+rec.Output*pr.Output+rec.Read*pr.Read+rec.Write*writeRate)/1e6 + rec.Searches*webSearchPrice
		if math.Abs(expected-rec.Cost)/rec.Cost > 0.02 {
			v.Warnings = append(v.Warnings, fmt.Sprintf("The price table gives %s for this session's %s usage, but Claude Code recorded %s. Prices have changed since, or the table (as of %s) is out of date.", money(expected), pr.Name, money(rec.Cost), pricesUpdated))
			tableWrong = true
		}
	}

	// Claude Code's tally restarts when a session is resumed, so compare only
	// the calls it covers. Subagent calls are left out of both sides.
	compared := v.Total - v.Agents
	if since := chat.RecordedSince; !since.IsZero() && len(sc.PerCall) > 0 && sc.PerCall[0].At.Before(since.Add(-time.Minute)) {
		compared = 0
		for _, c := range sc.PerCall {
			if !c.At.Before(since) {
				compared += v.cost(c.Use)
			}
		}
		v.Notes = append(v.Notes, fmt.Sprintf("Claude Code's recorded cost only covers calls since %s, when this session was last resumed; the estimate covers the whole transcript.", since.Local().Format("02 Jan · 15:04")))
	}
	if chat.Usage.HasCost && chat.Usage.Cost > 0 {
		diff := (compared - chat.Usage.Cost) / chat.Usage.Cost
		if math.Abs(diff) > 0.10 {
			dir := "higher"
			if diff < 0 {
				dir = "lower"
			}
			msg := fmt.Sprintf("The estimate for the same calls is %.0f%% %s than the recorded cost", math.Abs(diff)*100, dir)
			switch {
			case repriced:
				var names []string
				for _, model := range ran {
					if pr, ok := priceFor(model); ok {
						model = pr.Name
					}
					names = append(names, model)
				}
				msg += fmt.Sprintf(" because it prices this session as %s; it ran on %s.", v.Main.Name, strings.Join(names, ", "))
			case tableWrong:
				msg += "; see the price warning above."
			default:
				msg += ". Claude Code also makes background calls that the transcript doesn't record"
				var seen []string
				if sc.Away > 0 {
					seen = append(seen, fmt.Sprintf("%d away %s", sc.Away, plural(sc.Away, "summary")))
				}
				if sc.Compactions > 0 {
					seen = append(seen, fmt.Sprintf("%d %s", sc.Compactions, plural(sc.Compactions, "compaction")))
				}
				if len(seen) > 0 {
					msg += "; this session has " + strings.Join(seen, " and ")
				}
				msg += "."
			}
			v.Warnings = append(v.Warnings, msg)
		}
	}

	for _, model := range ran {
		if pr, ok := priceFor(model); ok && pr.NewTokenizer != v.Main.NewTokenizer {
			if v.Main.NewTokenizer {
				v.Notes = append(v.Notes, fmt.Sprintf("%s uses the older tokenizer; %s counts about 30%% more tokens for the same text, so this estimate is low.", pr.Name, v.Main.Name))
			} else {
				v.Notes = append(v.Notes, fmt.Sprintf("%s uses the newer tokenizer, which counts about 30%% more tokens than %s, so this estimate is high.", pr.Name, v.Main.Name))
			}
			break
		}
	}
	if sc.Fast {
		v.Notes = append(v.Notes, "Some calls used fast mode; they are estimated at standard speed.")
	}
}

func sameModel(recorded, table string) bool {
	p, ok := priceFor(recorded)
	return ok && p.Model == table
}
