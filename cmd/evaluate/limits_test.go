package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The cap is 16 MiB, so a 2 MiB reply is an answer, not an overrun.
// TestEvaluateBodyLimit sizes its bodies from maxBody, so it can't tell.
func TestEvaluateBodyCap(t *testing.T) {
	const size = 2 << 20
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		pad := size - len(`{"answers":{"q":""}}`)
		w.Write([]byte(`{"answers":{"q":"` + strings.Repeat("x", pad) + `"}}`))
	}))
	defer srv.Close()
	c := &Client{URL: srv.URL, APIKey: "k", HTTP: srv.Client()}

	b, err := c.Evaluate(context.Background(), evaluateIn{State: "s"})
	if err != nil || len(b) != size {
		t.Fatalf("2 MiB: len=%d err=%v", len(b), err)
	}
}
