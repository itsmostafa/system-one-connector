# Tool reference

`evaluate` exposes one MCP tool, also named `evaluate`. It sends state and typed questions to Jev and returns the API's response JSON unchanged. For the full API contract, see https://docs.typesafe.ai/api.

## Input

| Field | Required | Description |
|---|---|---|
| `state` | yes, unless `items` is set | What to judge: plain text, or a JSON object or array with named fields. Include the observed evidence and the background it is judged against (user goals, policies). Leave out your own conclusion about it. With `items`, `state` is sent to every item as `context`. |
| `questions` | yes | Map of question ID to `{type, instructions, criteria?}`. Answers come back under the same IDs. |
| `items` | no | Map of item ID to record. Each record is judged separately. See [Many records](#many-records-items). |
| `model` | no | Defaults to `jev-latest`, or `~typesafe/jev-latest` on OpenRouter. |

Question IDs are not sent to the model, so `instructions` must state the full question on its own. `instructions` can be a string, or an object or array when definitions, contrasts or examples make the question clearer. To refer to a nested field in `state`, use a backticked path such as `` `ticket.messages[0].text` ``.

## Question types

| Type | Answers | `criteria` |
|---|---|---|
| `noul` | Probability that a yes/no condition holds | Optional: `{"true": ..., "false": ...}` descriptions |
| `choice` | One option from a set, with a probability for each | Required: map of option to description (or `null`) |
| `score` | Probability-weighted position on ordered levels | Required: array of at least 2 level descriptions, lowest first |

A `choice` or `score` question can have at most 255 options.

`evaluate` checks criteria locally and rejects malformed ones before sending a request. The error names the field path you sent. It also rejects two inputs that the API mishandles:

- An unknown question type. The API answers only "Invalid request."
- `noul` criteria keys other than `true` and `false`. The API drops them without an error.

### Reading answers

A `noul` answer near 0.5 means Jev is unsure. It does not mean "somewhat true".

`choice` and `score` answers include a `confidence` value. It measures how concentrated the probability distribution is. It does not measure whether the answer is correct.

Score answers are 0-indexed: N levels score from `0` to `N-1`. So `3.87` over 5 levels sits between levels 3 and 4; it is not 3.87 out of 5. The response includes a `legend` that maps each index to its level and a `probabilities` entry for each level.

### Example response

```json
{
  "model": "jev-1.13.0",
  "answers": {
    "is_urgent": {"type": "noul", "noul": 0.95},
    "department": {
      "type": "choice",
      "choice": "billing",
      "confidence": 0.79,
      "probabilities": {"billing": 0.86, "technical": 0.14, "sales": 0.0}
    }
  },
  "usage": {"input_tokens": 349, "output_tokens": 58}
}
```

## Many records (`items`)

`items` has no counterpart in the TypeSafe API. It lets one tool call ask the same questions about many records:

- The tool sends one request per item, with state `{"item": <record>, "context": <state>}`. `context` is included only when `state` is set.
- A call accepts at most 100 items, and at most 8 requests run at once.
- The result is `{"results": {id: response}, "errors": {id: message}}`.
- If one item fails, it appears in `errors` and the other items still complete. The tool call itself fails only when every item fails.
- All responses in a batch together must stay under 16 MiB. Once that limit is reached, the remaining results are dropped and appear in `errors`. Split large batches across several calls.

Every item is a separately billed request.

## Limits and errors

| Behavior | Value |
|---|---|
| Request timeout | 60 seconds |
| Retries | Up to 3 retries with exponential backoff on HTTP 429 and 529 |
| Maximum response size | 16 MiB. A larger response is rejected, not truncated |
| Non-JSON success body | Rejected |

Other API errors come back to the agent as tool errors, which it can read and act on. The tool is read-only: it changes nothing on your machine.

## Guidance sent to agents

When a client connects, the server sends it usage guidance as MCP server instructions. The guidance asks agents to ask narrow questions, use JSON state, include a no-match option, and pass evidence instead of verdicts. The pi extension includes the same guidance. The text is the `instructions` constant in [`cmd/evaluate/main.go`](../cmd/evaluate/main.go).
