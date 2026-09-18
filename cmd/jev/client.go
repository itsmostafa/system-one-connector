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
		b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode/100 == 2 {
			return b, nil
		}
		retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == 529
		if !retryable || attempt == 3 {
			return nil, fmt.Errorf("jev: %s: %s", resp.Status, b)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		delay *= 2
	}
}
