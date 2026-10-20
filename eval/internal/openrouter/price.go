// Package openrouter prices eval runs from OpenRouter's decision-model catalog.
package openrouter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// ModelsURL lists OpenRouter's decision models with their prices. It needs no
// key. A var so tests can point it at a stub.
var ModelsURL = "https://openrouter.ai/api/v1/models?output_modalities=decisions"

// Price is the OpenRouter rate a run was billed at, saved in its meta.json so
// scoring never needs the network and old runs keep their old rates.
type Price struct {
	ID         string  `json:"id"`
	Prompt     float64 `json:"prompt"`     // USD per input token
	Completion float64 `json:"completion"` // USD per output token
	FetchedAt  string  `json:"fetched_at"`
}

// Model is one catalog entry.
type Model struct {
	ID            string `json:"id"`
	CanonicalSlug string `json:"canonical_slug"`
	Pricing       struct {
		Prompt     string `json:"prompt"`
		Completion string `json:"completion"`
	} `json:"pricing"`
}

// Fetch looks up the model a reply named in OpenRouter's catalog.
func Fetch(model string) (*Price, error) {
	c := http.Client{Timeout: 30 * time.Second}
	resp, err := c.Get(ModelsURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", ModelsURL, resp.Status)
	}
	var body struct {
		Data []Model `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decoding OpenRouter models: %w", err)
	}
	p, err := Match(body.Data, model)
	if err != nil {
		return nil, err
	}
	p.FetchedAt = time.Now().UTC().Format(time.RFC3339)
	return p, nil
}

// Match finds model by id, else by canonical slug, which is what replies name
// (liquid/d1-20260930 for liquid/d1). A slug shared with a :free variant
// resolves to whichever comes first; no model in these evals has one.
func Match(models []Model, model string) (*Price, error) {
	var hit *Model
	for i, m := range models {
		if m.ID == model {
			hit = &models[i]
			break
		}
		if hit == nil && m.CanonicalSlug == model {
			hit = &models[i]
		}
	}
	if hit == nil {
		return nil, fmt.Errorf("model %q is not in OpenRouter's decision models", model)
	}
	in, err1 := strconv.ParseFloat(hit.Pricing.Prompt, 64)
	out, err2 := strconv.ParseFloat(hit.Pricing.Completion, 64)
	// OpenRouter marks routers, whose price depends on the model they pick, as -1.
	if err1 != nil || err2 != nil || in < 0 || out < 0 {
		return nil, fmt.Errorf("model %q has no fixed price (%q in, %q out)", model, hit.Pricing.Prompt, hit.Pricing.Completion)
	}
	return &Price{ID: hit.ID, Prompt: in, Completion: out}, nil
}
