package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// The cost analysis page: context growth per API call, cost per call, the
// costliest rows, and cost by kind of content. Charts are inline SVG built
// here, with native tooltips, so the page and its HTML export need no script.

// Categories keep a fixed colour each, in the palette order validated for the
// dark panel surface (adjacent pairs stay distinguishable with colour vision
// deficiencies). Other is neutral grey.
var categories = []struct{ Name, Color string }{
	{"User messages", "#3987e5"},
	{"Tool calls & results", "#d95926"},
	{"Responses & reasoning", "#199e70"},
	{"MCP", "#c98500"},
	{"Skills", "#d55181"},
	{"System prompt & tools", "#008300"},
	{"Cache rebuilds", "#9085e9"},
	{"Other", "#6f7a70"},
}

const (
	catUser = iota
	catTools
	catResponses
	catMCP
	catSkills
	catSystem
	catRebuild
	catOther
)

func category(m Message) int {
	switch {
	case m.Role == "system prompt":
		return catSystem
	case m.Role == "cache" && m.MCP:
		return catMCP // ToolSearch loaded MCP tools and so rebuilt the cache
	case m.Role == "cache":
		return catRebuild
	case m.Skill || m.Tool == "Skill":
		return catSkills
	case m.MCP:
		return catMCP
	case m.Role == "tool":
		return catTools
	case m.Role == "user":
		return catUser
	case m.Role == "assistant" || m.Role == "reasoning":
		return catResponses
	}
	return catOther
}

type Slice struct {
	Name, Color      string
	Cost, Share      float64
	Dash, Offset     string // stroke-dasharray / offset on the donut circle
	Percent, Dollars string
}

type tick struct {
	At    float64
	Label string
}

type hit struct {
	X, W  float64
	Title string
}

type marker struct {
	X, Y float64
	Kind string
}

type LineChart struct {
	Title, Desc  string
	W, H         float64
	Left, Top    float64
	Right, Bot   float64 // plot edges
	AxisX, PlotH float64 // y-label anchor and plot height
	XLabelY      float64
	Line, Area   string
	YTicks       []tick
	XTicks       []tick
	Hits         []hit
	Markers      []marker
	HasRebuild   bool
	HasCompacted bool
}

type TopRow struct {
	Anchor, Label, Color, Category string
	Cost                           RowCost
	Width                          float64
}

type CallRow struct {
	N                  int
	Time, Tokens, Cost string
	Rebuilt, Compacted bool
}

type Analysis struct {
	Calls   int
	Context LineChart
	Spend   LineChart
	Slices  []Slice
	Top     []TopRow
	Table   []CallRow
	Peak    string
	Circ    float64
}

func buildAnalysis(rows []conversationRow, chat *Chat, v *CostView) *Analysis {
	a := &Analysis{Calls: len(chat.Costs.PerCall)}

	// Cost by category, from every priced row item and the session leftovers.
	sums := make([]float64, len(categories))
	for _, r := range rows {
		items := r.Tools
		if items == nil {
			items = []Message{r.Message}
		}
		for _, m := range items {
			if m.Cost != nil {
				sums[category(m)] += v.cost(m.Cost.Direct) + v.cost(m.Cost.Carried) + v.cost(m.Cost.Subagent)
			}
		}
	}
	sums[catSystem] += v.cost(chat.Costs.Overhead)
	sums[catOther] += v.cost(chat.Costs.Unattributed)
	total := 0.0
	for _, s := range sums {
		total += s
	}
	const radius = 70.0
	a.Circ = 2 * math.Pi * radius
	offset := 0.0
	for i, c := range categories {
		s := Slice{Name: c.Name, Color: c.Color, Cost: sums[i], Dollars: money(sums[i])}
		if total > 0 {
			s.Share = sums[i] / total
		}
		s.Percent = fmt.Sprintf("%.1f%%", s.Share*100)
		if s.Share > 0 {
			length := s.Share * a.Circ
			gap := math.Min(2, length/2) // a 2px surface gap between slices
			s.Dash = fmt.Sprintf("%.2f %.2f", length-gap, a.Circ-length+gap)
			s.Offset = fmt.Sprintf("%.2f", -offset)
			offset += length
		}
		a.Slices = append(a.Slices, s)
	}

	// The costliest rows, coloured by the category that dominates each.
	for _, r := range rows {
		if r.Price == nil {
			continue
		}
		items := r.Tools
		if items == nil {
			items = []Message{r.Message}
		}
		best, bestCost := catOther, -1.0
		byCat := map[int]float64{}
		for _, m := range items {
			if m.Cost != nil {
				byCat[category(m)] += v.cost(m.Cost.Direct) + v.cost(m.Cost.Carried) + v.cost(m.Cost.Subagent)
			}
		}
		for c, cost := range byCat {
			if cost > bestCost || cost == bestCost && c < best {
				best, bestCost = c, cost
			}
		}
		a.Top = append(a.Top, TopRow{Anchor: r.Anchor, Label: rowLabel(r), Color: categories[best].Color, Category: categories[best].Name, Cost: *r.Price})
	}
	sort.SliceStable(a.Top, func(i, j int) bool { return a.Top[i].Cost.Total > a.Top[j].Cost.Total })
	if len(a.Top) > 15 {
		a.Top = a.Top[:15]
	}
	for i := range a.Top {
		a.Top[i].Width = 100 * a.Top[i].Cost.Total / a.Top[0].Cost.Total
	}

	// Context size and cost for each API call.
	ctx := make([]float64, a.Calls)
	spend := make([]float64, a.Calls)
	peak := 0.0
	for i, c := range chat.Costs.PerCall {
		for _, role := range c.Use {
			ctx[i] += role[bInput] + role[bRead] + role[bWrite5m] + role[bWrite1h]
		}
		spend[i] = v.cost(c.Use)
		peak = math.Max(peak, ctx[i])
		stamp := ""
		if !c.At.IsZero() {
			stamp = displayStamp(c.At.Format(time.RFC3339Nano))
		}
		a.Table = append(a.Table, CallRow{i + 1, stamp, Usage{Tokens: ctx[i]}.TokenLabel(), money(spend[i]), c.Rebuilt, c.Compacted})
	}
	a.Peak = Usage{Tokens: peak}.TokenLabel()
	a.Context = lineChart(ctx, chat.Costs.PerCall, tokenTick, func(i int) string {
		return fmt.Sprintf("Call %d · %s · %s tokens of context", i+1, a.Table[i].Time, a.Table[i].Tokens)
	})
	a.Context.Title, a.Context.Desc = "Context size per call", "Tokens sent with each API call: cached reads, cache writes and uncached input."
	a.Spend = lineChart(spend, chat.Costs.PerCall, costTick, func(i int) string {
		return fmt.Sprintf("Call %d · %s · %s", i+1, a.Table[i].Time, a.Table[i].Cost)
	})
	a.Spend.Title, a.Spend.Desc = "Cost per call", fmt.Sprintf("Each API call's own cost at %s prices: reading the cache, writing new context, and output.", v.Main.Name)
	return a
}

func tokenTick(v float64) string {
	switch {
	case v >= 1e6:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", v/1e6), ".0") + "M"
	case v >= 1e3:
		return fmt.Sprintf("%.0fk", v/1e3)
	}
	return fmt.Sprintf("%.0f", v)
}

func costTick(v float64) string {
	if v > 0 && v < 0.01 {
		return fmt.Sprintf("$%.3f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}

// niceStep picks a 1, 2 or 5 step giving about four gridlines.
func niceStep(max float64) float64 {
	if max <= 0 {
		return 1
	}
	raw := max / 4
	mag := math.Pow(10, math.Floor(math.Log10(raw)))
	for _, m := range []float64{1, 2, 5, 10} {
		if raw <= m*mag {
			return m * mag
		}
	}
	return 10 * mag
}

func lineChart(values []float64, calls []timedUse, label func(float64) string, title func(int) string) LineChart {
	c := LineChart{W: 760, H: 240, Left: 64, Top: 14}
	c.Right, c.Bot = c.W-14, c.H-30
	c.AxisX, c.PlotH, c.XLabelY = c.Left-8, c.Bot-c.Top, c.Bot+20
	n := len(values)
	if n == 0 {
		return c
	}
	max := 0.0
	for _, v := range values {
		max = math.Max(max, v)
	}
	step := niceStep(max)
	top := math.Max(step, math.Ceil(max/step)*step)
	x := func(i int) float64 {
		if n == 1 {
			return (c.Left + c.Right) / 2
		}
		return c.Left + float64(i)*(c.Right-c.Left)/float64(n-1)
	}
	y := func(v float64) float64 { return c.Bot - v/top*(c.Bot-c.Top) }
	for v := 0.0; v <= top+step/2; v += step {
		c.YTicks = append(c.YTicks, tick{y(v), label(v)})
	}
	xStep := niceStep(float64(n))
	if xStep < 1 {
		xStep = 1
	}
	for i := 0.0; i < float64(n); i += xStep {
		c.XTicks = append(c.XTicks, tick{x(int(i)), fmt.Sprintf("%.0f", i+1)})
	}
	var line strings.Builder
	for i, v := range values {
		cmd := "L"
		if i == 0 {
			cmd = "M"
		}
		fmt.Fprintf(&line, "%s%.1f %.1f ", cmd, x(i), y(v))
	}
	c.Line = strings.TrimSpace(line.String())
	c.Area = fmt.Sprintf("%s L%.1f %.1f L%.1f %.1f Z", c.Line, x(n-1), c.Bot, x(0), c.Bot)
	width := (c.Right - c.Left) / float64(max1(n-1))
	for i := range values {
		h := hit{X: x(i) - width/2, W: width, Title: title(i)}
		if calls[i].Rebuilt {
			h.Title += " · prompt cache rebuilt"
			c.Markers = append(c.Markers, marker{x(i), y(values[i]), "rebuild"})
			c.HasRebuild = true
		}
		if calls[i].Compacted {
			h.Title += " · first call after compaction"
			c.Markers = append(c.Markers, marker{x(i), y(values[i]), "compaction"})
			c.HasCompacted = true
		}
		c.Hits = append(c.Hits, h)
	}
	return c
}

func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}
