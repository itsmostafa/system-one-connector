package main

import "github.com/itsmostafa/system-one-connector/eval/internal/openrouter"

// price is the OpenRouter rate a run was billed at, saved in its meta.json.
type price = openrouter.Price

// costPer100 prices each pass's calls at the rate that pass saved and scales
// the total to 100 categorized items. Passes with no saved rate are left out;
// it reports false when no pass has a rate or the priced passes have no items.
func costPer100(passes []runMeta) (float64, bool) {
	var usd float64
	var items int
	for _, m := range passes {
		if m.Price == nil {
			continue
		}
		for _, c := range m.Calls {
			usd += float64(c.InputTokens)*m.Price.Prompt + float64(c.OutputTokens)*m.Price.Completion
			items += c.ItemCount
		}
	}
	if items == 0 {
		return 0, false
	}
	return usd * 100 / float64(items), true
}
