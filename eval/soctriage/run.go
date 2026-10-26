package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/itsmostafa/system-one-connector/eval/internal/openrouter"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// labels lists the options in criteria order.
var labels = []string{escalate, falsePositive}

const (
	question     = "Should a SOC analyst escalate this web security alert, or close it as a false positive? `item.rule_name`, `item.attack_type` and `item.kill_chain_all` are what the detection rule reported; the other fields are the HTTP request and response that triggered it."
	noulQuestion = "This web security alert is a real attack attempt that a SOC analyst should escalate, not a false positive on harmless traffic. An attack attempt includes exploit or injection payloads, path traversal, malicious uploads, credential guessing, and probing for sensitive files or known vulnerabilities, whether or not it succeeded. `item.rule_name`, `item.attack_type` and `item.kill_chain_all` are what the detection rule reported; the other fields are the HTTP request and response that triggered it."
)

// apiAnswer is one question's answer as evaluate returns it.
type apiAnswer struct {
	Choice        string             `json:"choice"`
	Confidence    float64            `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
	Noul          float64            `json:"noul"`
}

// answer is one result row, written for both protocols so scoring is uniform.
type answer struct {
	Row           int                `json:"row"`
	Choice        string             `json:"choice"`
	Confidence    float64            `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
}

func run(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	profile := fs.String("profile", "", "evaluate profile to pin with TYPESAFE_PROFILE")
	protocol := fs.String("protocol", "choice", "choice, or noul (one yes/no escalate question, escalate at 0.5 or above)")
	name := fs.String("name", "", "results name (default: profile, plus -noul for the noul protocol)")
	limit := fs.Int("limit", 0, "only the first N alerts (smoke runs)")
	bin := fs.String("bin", "../evaluate", "evaluate binary")
	data := fs.String("data", "data/soctriage", "dataset directory")
	outDir := fs.String("out", "results/soctriage", "results directory")
	fs.Parse(args)
	if *profile == "" {
		return errors.New("-profile is required")
	}
	if *protocol != "choice" && *protocol != "noul" {
		return fmt.Errorf("-protocol must be choice or noul, got %q", *protocol)
	}
	if *name == "" {
		*name = *profile
		if *protocol == "noul" {
			*name += "-noul"
		}
	}

	rows, err := readJSONL[alert](filepath.Join(*data, alertsFile))
	if err != nil {
		return fmt.Errorf("%w (run build first)", err)
	}
	if *limit > 0 && *limit < len(rows) {
		rows = rows[:*limit]
	}
	dataset, err := fingerprint(*data)
	if err != nil {
		return err
	}

	ctx := context.Background()
	cmd := exec.Command(*bin, "mcp", "--no-update-check")
	cmd.Env = append(os.Environ(), "TYPESAFE_PROFILE="+*profile)
	cmd.Stderr = os.Stderr
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "soctriage", Version: "1"}, nil).Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return err
	}
	defer cs.Close()

	questions := map[string]any{}
	if *protocol == "choice" {
		questions["disposition"] = map[string]any{"type": "choice", "instructions": question, "criteria": json.RawMessage(criteriaJSON)}
	} else {
		questions["escalate"] = map[string]any{"type": "noul", "instructions": noulQuestion}
	}

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return err
	}
	out, err := os.Create(filepath.Join(*outDir, *name+".jsonl"))
	if err != nil {
		return err
	}
	defer out.Close()
	enc := json.NewEncoder(out)
	var metas []json.RawMessage
	var cost *openrouter.Price
	errs := map[string]string{}

	// The spawned server inherits TYPESAFE_MAX_ITEMS, so batch under it.
	size := 500
	if n, err := strconv.Atoi(os.Getenv("TYPESAFE_MAX_ITEMS")); err == nil && n > 0 {
		size = min(n, size)
	}
	for start := 0; start < len(rows); start += size {
		batch := rows[start:min(start+size, len(rows))]
		items := map[string]any{}
		for _, r := range batch {
			items[strconv.Itoa(r.Row)] = r.state()
		}
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "evaluate", Arguments: map[string]any{"questions": questions, "items": items}})
		if err != nil {
			return err
		}
		text := res.Content[0].(*mcp.TextContent).Text
		if res.IsError {
			return fmt.Errorf("rows %d-%d: %s", batch[0].Row, batch[len(batch)-1].Row, text)
		}
		var reply struct {
			Results map[string]struct {
				Answers map[string]apiAnswer `json:"answers"`
			} `json:"results"`
			Errors map[string]string `json:"errors"`
			Meta   json.RawMessage   `json:"meta"`
		}
		if err := json.Unmarshal([]byte(text), &reply); err != nil {
			return fmt.Errorf("decoding reply: %w", err)
		}
		maps.Copy(errs, reply.Errors)
		metas = append(metas, reply.Meta)
		// Price the run once, from the model the first reply names, and warn
		// right away so a long run does not finish unpriced by surprise.
		if start == 0 {
			var m struct{ Model string }
			json.Unmarshal(reply.Meta, &m)
			if cost, err = openrouter.Fetch(m.Model); err != nil {
				fmt.Fprintf(os.Stderr, "%s: no price saved, cost will show as —: %v\n", *name, err)
			}
		}
		for _, id := range slices.Sorted(maps.Keys(reply.Results)) {
			row, _ := strconv.Atoi(id)
			a := answer{Row: row}
			ans := reply.Results[id].Answers
			if *protocol == "choice" {
				q := ans["disposition"]
				a.Choice, a.Confidence, a.Probabilities = q.Choice, q.Confidence, q.Probabilities
			} else {
				a.Choice, a.Confidence, a.Probabilities = fromNoul(ans["escalate"].Noul)
			}
			if err := enc.Encode(a); err != nil {
				return err
			}
		}
		fmt.Fprintf(os.Stderr, "%s: rows %d-%d done, %d errors so far\n", *name, batch[0].Row, batch[len(batch)-1].Row, len(errs))
	}
	meta, _ := json.MarshalIndent(map[string]any{"profile": *profile, "protocol": *protocol, "dataset": dataset, "limit": *limit, "calls": metas, "errors": errs, "price": cost}, "", "  ")
	return os.WriteFile(filepath.Join(*outDir, *name+".meta.json"), append(meta, '\n'), 0o644)
}

// state is what the model sees: the detector's labels and the HTTP exchange,
// under their original field names. Row and label are left out, and so are
// the source and destination IPs and source port, which SecAlertBench
// replaced with random values.
func (a alert) state() alert {
	a.Row, a.Label, a.Sip, a.Dip, a.Sport = 0, "", "", "", 0
	return a
}

// fromNoul turns the escalate probability into the same shape a choice
// answer has. With two options, the choice confidence formula reduces to
// the gap between them.
func fromNoul(p float64) (string, float64, map[string]float64) {
	choice := falsePositive
	if p >= 0.5 {
		choice = escalate
	}
	probs := map[string]float64{escalate: p, falsePositive: 1 - p}
	return choice, max(p, 1-p) - min(p, 1-p), probs
}

// fingerprint hashes what a run was asked and graded on: the criteria, both
// questions and the alerts. score skips a pass whose fingerprint differs,
// since its row numbers may now name other alerts.
func fingerprint(dir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, alertsFile))
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write(criteriaJSON)
	h.Write([]byte(question))
	h.Write([]byte(noulQuestion))
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil)), nil
}
