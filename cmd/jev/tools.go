package main

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// add registers a tool whose handler returns raw text (usually TypeSafe JSON).
// A returned error becomes a tool result with IsError set.
func add[In any](s *mcp.Server, t *mcp.Tool, fn func(ctx context.Context, in In) ([]byte, error)) {
	mcp.AddTool(s, t, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		b, err := fn(ctx, in)
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil, nil
	})
}

type question struct {
	Type         string `json:"type" jsonschema:"noul (probability a yes/no condition holds), choice (one option from the criteria map), or score (probability-weighted position on ordered criteria levels)"`
	Instructions any    `json:"instructions" jsonschema:"the judgment to make, with its full meaning; a string, or an object/array for definitions, contrasts, and examples"`
	Criteria     any    `json:"criteria,omitempty" jsonschema:"noul: optional {\"true\": ..., \"false\": ...} descriptions; choice (required): map of option to description or null; score (required): ordered array of at least 2 level descriptions"`
}

type evaluateIn struct {
	State     any                 `json:"state" jsonschema:"content to judge: plain text, or a JSON object/array with named fields"`
	Questions map[string]question `json:"questions" jsonschema:"map of question id to question; answers come back under the same ids, which are not sent to the model"`
	Model     string              `json:"model,omitempty" jsonschema:"model to use; default jev-latest"`
}

func registerTools(s *mcp.Server, c *Client) {
	add(s, &mcp.Tool{
		Name: "evaluate",
		Description: "Run a Jev prompt: evaluate state against one or more typed questions (noul, choice, score) " +
			"and return typed answers with probabilities and confidence.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, in evaluateIn) ([]byte, error) {
		if in.State == nil {
			return nil, errors.New("state is required")
		}
		if len(in.Questions) == 0 {
			return nil, errors.New("questions must not be empty")
		}
		if in.Model == "" {
			in.Model = "jev-latest"
		}
		return c.Evaluate(ctx, in)
	})
}
