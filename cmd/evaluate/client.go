package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// maxBody caps response size so an oversized reply cannot exhaust memory.
const maxBody = 16 << 20

// Client calls a Jev evaluation endpoint: the TypeSafe API directly, or
// OpenRouter's Decisions router. Both take the same request body.
type Client struct {
	// URL is the full endpoint, not a base. Model is the route's default.
	URL, APIKey, Model string
	HTTP               *http.Client
	// Backoff is the first retry delay for 429/529; it doubles each attempt.
	Backoff time.Duration
}

// Evaluate posts a System One request and returns the raw response JSON.
// 429 and 529 are retried with exponential backoff; other non-2xx statuses
// become errors carrying the API's error body.
func (c *Client) Evaluate(ctx context.Context, req any) ([]byte, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	delay := c.Backoff
	for attempt := 0; ; attempt++ {
		r, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		r.Header.Set("Authorization", "Bearer "+c.APIKey)
		r.Header.Set("Content-Type", "application/json")
		resp, err := c.HTTP.Do(r)
		if err != nil {
			return nil, err
		}
		// maxBody+1 so a body that fills the cap exactly is told apart from one
		// that overruns it; without that, an overlong reply came back truncated
		// mid-JSON and, on a 2xx, was returned as a successful answer.
		b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if len(b) > maxBody {
			return nil, fmt.Errorf("evaluate: %s: response exceeds %d bytes", resp.Status, maxBody)
		}
		if resp.StatusCode/100 == 2 {
			// Callers hand the body on as the API's JSON (the items path embeds
			// it raw), so a non-JSON 2xx such as a proxy's HTML page is an error,
			// not an answer.
			if !json.Valid(b) {
				return nil, fmt.Errorf("evaluate: %s: response is not valid JSON: %.200q", resp.Status, b)
			}
			return b, nil
		}
		retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == 529
		if !retryable || attempt == 3 {
			return nil, fmt.Errorf("evaluate: %s: %s", resp.Status, b)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		delay *= 2
	}
}
