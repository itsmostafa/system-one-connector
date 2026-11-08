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
var labels = []string{risky, falsePositive}

const (
	question = "What is the verdict on this web security alert: Risky, a real attack attempt a SOC analyst should look at, or False Positive, the rule firing on harmless traffic? `item.rule_name`, `item.attack_type` and `item.kill_chain_all` are what the detection rule reported; the other fields are the HTTP request and response that triggered it."
	// The inconclusive protocol asks the same choice with a third option.
	question3             = "What is the verdict on this web security alert: Risky, a real attack attempt a SOC analyst should look at; False Positive, the rule firing on harmless traffic; or Inconclusive, the alert alone cannot tell which? `item.rule_name`, `item.attack_type` and `item.kill_chain_all` are what the detection rule reported; the other fields are the HTTP request and response that triggered it."
	inconclusiveCriterion = "The alert does not show whether the traffic is an attack or harmless: the request and response fit either reading, and telling them apart needs context the alert does not include."
	noulQuestion          = "This web security alert is Risky: a real attack attempt that a SOC analyst should look at, not a false positive on harmless traffic. An attack attempt includes exploit or injection payloads, path traversal, malicious uploads, automated or default-credential login attempts, and requests for files and paths a normal client has no reason to reach, whether or not it succeeded. A user logging in to their own account with a mistyped or empty password, or a client downloading files that are part of its normal work, is not an attack attempt. `item.rule_name`, `item.attack_type` and `item.kill_chain_all` are what the detection rule reported; the other fields are the HTTP request and response that triggered it."
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
	Verdict       string             `json:"verdict"`
	Confidence    float64            `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
}

func run(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	profile := fs.String("profile", "", "evaluate profile to pin with TYPESAFE_PROFILE")
	protocol := fs.String("protocol", "choice", "choice, inconclusive (choice with a third Inconclusive verdict), or noul (one yes/no Risky question, Risky at 0.5 or above)")
	name := fs.String("name", "", "results name (default: profile, plus -noul or -inconclusive for those protocols)")
	limit := fs.Int("limit", 0, "only the first N alerts (smoke runs)")
	bin := fs.String("bin", "../evaluate", "evaluate binary")
	data := fs.String("data", "data/soctriage", "dataset directory")
	outDir := fs.String("out", "results/soctriage", "results directory")
	fs.Parse(args)
	if *profile == "" {
		return errors.New("-profile is required")
	}
	if !slices.Contains([]string{"choice", "inconclusive", "noul"}, *protocol) {
		return fmt.Errorf("-protocol must be choice, inconclusive or noul, got %q", *protocol)
	}
	if *name == "" {
		*name = *profile
		if *protocol != "choice" {
			*name += "-" + *protocol
		}
	}

	rows, err := readJSONL[alert](filepath.Join(*data, alertsFile))
	if err != nil {
		return fmt.Errorf("%w (run build first)", err)
	}
	if *limit > 0 && *limit < len(rows) {
		rows = rows[:*limit]
	}
	dataset, err := fingerprint(*data, *protocol)
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
	switch *protocol {
	case "choice":
		questions["verdict"] = map[string]any{"type": "choice", "instructions": question, "criteria": json.RawMessage(criteriaJSON)}
	case "inconclusive":
		criteria, err := criteria3()
		if err != nil {
			return err
		}
		questions["verdict"] = map[string]any{"type": "choice", "instructions": question3, "criteria": criteria}
	default:
		questions["risky"] = map[string]any{"type": "noul", "instructions": noulQuestion}
	}

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return err
	}
	// Answers and metadata go to .partial files that replace the named run
	// only once every batch succeeds, so a failed rerun can't leave new
	// answers beside the old run's metadata. score's *.jsonl glob skips them.
	base := filepath.Join(*outDir, *name)
	out, err := os.Create(base + ".jsonl.partial")
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
			if *protocol != "noul" {
				q := ans["verdict"]
				a.Verdict, a.Confidence, a.Probabilities = q.Choice, q.Confidence, q.Probabilities
			} else {
				a.Verdict, a.Confidence, a.Probabilities = fromNoul(ans["risky"].Noul)
			}
			if err := enc.Encode(a); err != nil {
				return err
			}
		}
		fmt.Fprintf(os.Stderr, "%s: rows %d-%d done, %d errors so far\n", *name, batch[0].Row, batch[len(batch)-1].Row, len(errs))
	}
	meta, _ := json.MarshalIndent(map[string]any{"profile": *profile, "protocol": *protocol, "dataset": dataset, "limit": *limit, "calls": metas, "errors": errs, "price": cost}, "", "  ")
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.WriteFile(base+".meta.json.partial", append(meta, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(base+".jsonl.partial", base+".jsonl"); err != nil {
		return err
	}
	return os.Rename(base+".meta.json.partial", base+".meta.json")
}

// state is what the model sees: the detector's labels and the HTTP exchange,
// under their original field names. Row and label are left out, and so are
// the source and destination IPs and source port, which SecAlertBench
// replaced with random values.
func (a alert) state() alert {
	a.Row, a.Label, a.Sip, a.Dip, a.Sport = 0, "", "", "", 0
	return a
}

// fromNoul turns the Risky probability into the same shape a choice
// answer has. With two options, the choice confidence formula reduces to
// the gap between them.
func fromNoul(p float64) (string, float64, map[string]float64) {
	choice := falsePositive
	if p >= 0.5 {
		choice = risky
	}
	probs := map[string]float64{risky: p, falsePositive: 1 - p}
	return choice, max(p, 1-p) - min(p, 1-p), probs
}

// criteria3 is criteria.json with the Inconclusive option added.
func criteria3() (json.RawMessage, error) {
	var c map[string]string
	if err := json.Unmarshal(criteriaJSON, &c); err != nil {
		return nil, err
	}
	c[inconclusive] = inconclusiveCriterion
	return json.Marshal(c)
}

// fingerprint hashes what a run was asked and graded on: the criteria, both
// two-way questions and the alerts, plus the three-way question for the
// inconclusive protocol. score skips a pass whose fingerprint differs, since
// its row numbers may now name other alerts.
func fingerprint(dir, protocol string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, alertsFile))
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write(criteriaJSON)
	h.Write([]byte(question))
	h.Write([]byte(noulQuestion))
	if protocol == "inconclusive" {
		h.Write([]byte(question3))
		h.Write([]byte(inconclusiveCriterion))
	}
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil)), nil
}
