package main

import "strings"

// Prices for estimating Claude Code session costs, in USD per million tokens.
//
// HOW TO UPDATE THE PRICES ("Claude - update the prices!")
//
//  1. Fetch the official table: https://platform.claude.com/docs/en/about-claude/pricing.md
//     (the "Model pricing" table: base input, 5m cache writes, 1h cache writes,
//     cache hits, output).
//  2. Make priceTable match it: change any rates that moved and add every new
//     model with its API model ID. Keep models that are still listed (they
//     price older sessions and back the recorded-cost check); drop rows the
//     page no longer lists. Include limited-availability models only if they
//     appear in local sessions.
//  3. Point defaultPriceModel at the newest model the user works with, and
//     smallPriceModel at the newest Haiku.
//  4. Set NewTokenizer from the page's tokenizer note (which models use the
//     newer tokenizer).
//  5. Set pricesUpdated to today's date (YYYY-MM-DD).
//  6. Check against real sessions: run `go test ./...`, then ./run.sh and open a
//     recent Claude Code chat. Its header must not warn that the price table
//     disagrees with what Claude Code recorded.
//  7. Commit and push (see AGENTS.md).
//
// Sessions are estimated as if run on a current model (see Pricing), so
// only current models matter. There is deliberately no history of past prices.
const pricesUpdated = "2026-10-02"
const defaultPriceModel = "claude-opus-5-5"
const smallPriceModel = "claude-haiku-4-5"
const webSearchPrice = 10.0 / 1000 // USD per search

type Price struct {
	Model, Name                           string
	Input, Write5m, Write1h, Read, Output float64
	NewTokenizer                          bool // Claude 4.7 and later count ~30% more tokens for the same text
}

var priceTable = []Price{
	{"claude-fable-5-1", "Fable 5.1", 10, 12.5, 20, 0.25, 50, true},
	{"claude-fable-5", "Fable 5", 10, 12.5, 20, 1, 50, true},
	{"claude-opus-5-5", "Opus 5.5", 4, 5, 8, 0.20, 20, true},
	{"claude-opus-5", "Opus 5", 5, 6.25, 10, 0.50, 25, true},
	{"claude-opus-4-8", "Opus 4.8", 5, 6.25, 10, 0.50, 25, true},
	{"claude-opus-4-7", "Opus 4.7", 5, 6.25, 10, 0.50, 25, true},
	{"claude-opus-4-6", "Opus 4.6", 5, 6.25, 10, 0.50, 25, false},
	{"claude-opus-4-5", "Opus 4.5", 5, 6.25, 10, 0.50, 25, false},
	{"claude-sonnet-5-5", "Sonnet 5.5", 2, 2.5, 4, 0.20, 10, true},
	{"claude-sonnet-5", "Sonnet 5", 2, 2.5, 4, 0.20, 10, true},
	{"claude-sonnet-4-6", "Sonnet 4.6", 3, 3.75, 6, 0.30, 15, false},
	{"claude-sonnet-4-5", "Sonnet 4.5", 3, 3.75, 6, 0.30, 15, false},
	{"claude-haiku-4-5", "Haiku 4.5", 1, 1.25, 2, 0.10, 5, false},
}

// priceFor finds a model's prices. Recorded IDs may carry a date suffix
// (claude-haiku-4-5-20251001); anything else must match exactly, so an unknown
// claude-opus-5-6 is reported as missing rather than priced as Opus 5.
func priceFor(model string) (Price, bool) {
	if i := strings.LastIndex(model, "-"); i > 0 && len(model)-i-1 == 8 && strings.Trim(model[i+1:], "0123456789") == "" {
		model = model[:i]
	}
	for _, p := range priceTable {
		if p.Model == model {
			return p, true
		}
	}
	return Price{}, false
}
