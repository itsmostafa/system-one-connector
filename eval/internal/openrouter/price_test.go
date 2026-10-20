package openrouter

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMatch(t *testing.T) {
	models := []Model{
		{ID: "liquid/d1", CanonicalSlug: "liquid/d1-20260930"},
		{ID: "typesafe/jev-router", CanonicalSlug: "typesafe/jev-router"},
	}
	models[0].Pricing.Prompt, models[0].Pricing.Completion = "0.00000004", "0"
	models[1].Pricing.Prompt, models[1].Pricing.Completion = "-1", "-1"
	for _, model := range []string{"liquid/d1", "liquid/d1-20260930"} {
		if p, err := Match(models, model); err != nil || p.ID != "liquid/d1" || p.Prompt != 4e-8 {
			t.Errorf("Match(%q) = %+v, %v; want liquid/d1 at 4e-8", model, p, err)
		}
	}
	for _, model := range []string{"jev-1.13.0", "typesafe/jev-router"} {
		if p, err := Match(models, model); err == nil {
			t.Errorf("Match(%q) = %+v, want an error", model, p)
		}
	}
}

func TestFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"data":[{"id":"cloudflare/clef","canonical_slug":"cloudflare/clef","pricing":{"prompt":"0.00000024","completion":"0"}}]}`))
	}))
	defer srv.Close()
	defer func(u string) { ModelsURL = u }(ModelsURL)
	ModelsURL = srv.URL
	p, err := Fetch("cloudflare/clef")
	if err != nil || p.Prompt != 2.4e-7 || p.FetchedAt == "" {
		t.Errorf("Fetch = %+v, %v", p, err)
	}
}
