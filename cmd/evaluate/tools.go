package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// add registers a tool whose handler returns raw text (usually TypeSafe JSON).
// A returned error becomes a tool result with IsError set.
//
// The SDK's own decode of the arguments is discarded: it lands JSON numbers in
// `any` fields as float64, which silently rounds anything past 2^53, so two
// distinct ids in state would reach the model as one number. Re-decoding with
// UseNumber keeps every number as the digits the caller wrote, and re-marshals
// them verbatim. The SDK has already validated the arguments against the input
// schema by this point, so this only changes how they are read.
func add[In any](s *mcp.Server, t *mcp.Tool, fn func(ctx context.Context, in In) ([]byte, error)) {
	mcp.AddTool(s, t, func(ctx context.Context, req *mcp.CallToolRequest, _ In) (*mcp.CallToolResult, any, error) {
		var in In
		d := json.NewDecoder(bytes.NewReader(req.Params.Arguments))
		d.UseNumber()
		if err := d.Decode(&in); err != nil {
			return nil, nil, err
		}
		b, err := fn(ctx, in)
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil, nil
	})
}

type question struct {
	Type         string `json:"type" jsonschema:"noul (probability a yes/no condition holds), choice (one option from the criteria map), or score (probability-weighted position on ordered criteria levels)"`
	Instructions any    `json:"instructions" jsonschema:"the judgment to make, with its full meaning; a string, or an object/array for definitions, contrasts, and examples; name the condition to test, not the conclusion you expect"`
	Criteria     any    `json:"criteria,omitempty" jsonschema:"noul: optional {\"true\": ..., \"false\": ...} descriptions; choice (required): map of option to description or null; score (required): ordered array of at least 2 level descriptions, e.g. [\"poor\", \"fair\", \"good\"] — an array, not the index-keyed object the response legend comes back as"`
}

type evaluateIn struct {
	State     any                 `json:"state" jsonschema:"content to judge: plain text, or a JSON object/array with named fields; observed evidence or a faithful condensation of it, not your verdict about it"`
	Questions map[string]question `json:"questions" jsonschema:"map of question id to question; answers come back under the same ids, which are not sent to the model"`
	Model     string              `json:"model,omitempty" jsonschema:"model to use; defaults to the latest Jev on whichever endpoint is configured"`
}

const toolDescription = "Jev is a fast structured-decision model: unstructured state in, typed answers " +
	"(noul, choice, score) with calibrated confidence out; 70-500ms, schema-enforced. " +
	"Use for classification, routing, scoring, extraction, branching, guardrails/judging, " +
	"and map-reduce over large data — wherever hand-written logic is too brittle or latency matters. " +
	"Not for prose, code, or free-form text: the answer space must be enumerable up front (max 255 options). " +
	"Pass raw evidence as state, not your read of it — a conclusion asserted in state biases the answer toward it, and the confidence is then not independent corroboration."

func registerTools(s *mcp.Server, c *Client) {
	add(s, &mcp.Tool{
		Name:        "evaluate",
		Description: toolDescription,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, in evaluateIn) ([]byte, error) {
		if in.State == nil {
			return nil, errors.New("state is required")
		}
		if len(in.Questions) == 0 {
			return nil, errors.New("questions must not be empty")
		}
		if err := validate(in); err != nil {
			return nil, err
		}
		if in.Model == "" {
			in.Model = c.Model
		}
		return c.Evaluate(ctx, in)
	})
}

// validate rejects the criteria shapes the API is known to refuse, so the caller
// sees its own JSON path instead of the union-branch path the API reports
// ("questions.<id>.score.criteria" for input that has no score property at all).
//
// It also catches what the API gets wrong for the caller: an unknown type comes
// back as a bare "Invalid request.", and a noul criteria key other than true or
// false is silently dropped. The two-level score minimum stays guidance, not a
// rule, because the API accepts one level.
func validate(in evaluateIn) error {
	for id, q := range in.Questions {
		var want string
		switch q.Type {
		case "score":
			if v, ok := q.Criteria.([]any); ok && len(v) > 0 {
				continue
			}
			want = "an array of level descriptions, ordered low to high"
		case "choice":
			if v, ok := q.Criteria.(map[string]any); ok && len(v) > 0 {
				continue
			}
			want = "an object mapping each option to a description or null"
		case "noul":
			if q.Criteria == nil {
				continue
			}
			if m, ok := q.Criteria.(map[string]any); ok {
				for k := range m {
					if k != "true" && k != "false" {
						return fmt.Errorf(`questions[%q].criteria: noul criteria keys must be "true" or "false", got %q`, id, k)
					}
				}
				continue
			}
			want = `an object with "true" and "false" descriptions, or omitted`
		default:
			return fmt.Errorf("questions[%q].type: must be noul, choice, or score, got %q", id, q.Type)
		}
		// Bracket-quoted, not questions.%s.criteria: an id containing a dot
		// would otherwise read as nesting that the request never had, which is
		// the exact ambiguity this check exists to remove.
		return fmt.Errorf("questions[%q].criteria: %s criteria must be %s, got %s",
			id, q.Type, want, jsonKind(q.Criteria))
	}
	return nil
}

// jsonKind names a decoded JSON value the way the caller wrote it, so the error
// says "got object" rather than a Go type the caller never typed.
func jsonKind(v any) string {
	switch v := v.(type) {
	case nil:
		return "nothing"
	case []any:
		if len(v) == 0 {
			return "an empty array"
		}
		return "an array"
	case map[string]any:
		if len(v) == 0 {
			return "an empty object"
		}
		return "an object"
	case string:
		return "a string"
	case json.Number, float64:
		return "a number"
	case bool:
		return "a boolean"
	}
	return "an unsupported value"
}
