// Command taxcat runs the tax-categorization eval: it drives a built evaluate
// binary over MCP stdio, one profile per run, and scores the saved answers.
//
//	go run ./taxcat run -profile jev
//	go run ./taxcat run -profile span-01 -protocol noul
//	go run ./taxcat score
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"maps"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/itsmostafa/system-one-connector/eval/internal/openrouter"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

//go:embed criteria.json
var criteriaJSON []byte

// labels lists the options in criteria order; a Go map would sort them.
var labels = func() []string {
	d := json.NewDecoder(strings.NewReader(string(criteriaJSON)))
	d.Token()
	var out []string
	for d.More() {
		t, _ := d.Token()
		out = append(out, t.(string))
		var skip json.RawMessage
		d.Decode(&skip)
	}
	return out
}()

const question = "Which US personal income tax category does this bank transaction belong to? `item.payee` is the payee name as it appears on the statement; `item.approx_amount` is the amount rounded to the nearest $5 and whether money went out (charge) or came in (deposit)."

// payee is one committed dataset row. The amount lives in a gitignored file.
type payee struct {
	Row            int    `json:"row"`
	Payee          string `json:"payee"`
	SourceCategory string `json:"source_category"`
	Label          string `json:"label"`
	Origin         string `json:"origin"`
	Hard           bool   `json:"hard"`
}

type amount struct {
	Row    int     `json:"row"`
	Amount float64 `json:"amount_rounded"`
}

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

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: taxcat run|score [flags]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "run":
		err = run(os.Args[2:])
	case "score":
		err = score(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "taxcat:", err)
		os.Exit(1)
	}
}

func readJSONL[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []T
	s := bufio.NewScanner(f)
	s.Buffer(nil, 1<<20)
	for s.Scan() {
		if strings.TrimSpace(s.Text()) == "" {
			continue
		}
		var v T
		if err := json.Unmarshal(s.Bytes(), &v); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out = append(out, v)
	}
	return out, s.Err()
}

// approx renders a rounded amount the way the models see it.
func approx(a float64) string {
	dir := "charge"
	if a > 0 {
		dir = "deposit"
	}
	a = math.Abs(a)
	if a < 5 {
		return "under $5 " + dir
	}
	return "$" + addCommas(int(a)) + " " + dir
}

func addCommas(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func run(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	profile := fs.String("profile", "", "evaluate profile to pin with TYPESAFE_PROFILE")
	protocol := fs.String("protocol", "choice", "choice, or noul (one yes/no question per category, argmax)")
	name := fs.String("name", "", "results name (default: profile, plus -noul for the noul protocol)")
	limit := fs.Int("limit", 0, "only the first N rows (smoke runs)")
	bin := fs.String("bin", "../evaluate", "evaluate binary")
	data := fs.String("data", "data", "dataset directory")
	outDir := fs.String("out", "results", "results directory")
	noAmount := fs.Bool("no-amount", false, "send payee names only")
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
		if *noAmount {
			*name += "-no-amount"
		}
	}

	rows, err := readJSONL[payee](filepath.Join(*data, "payees.jsonl"))
	if err != nil {
		return err
	}
	amounts := map[int]float64{}
	if !*noAmount {
		as, err := readJSONL[amount](filepath.Join(*data, "amounts.jsonl"))
		if err != nil {
			return fmt.Errorf("%w (pass -no-amount to run on payee names only)", err)
		}
		for _, a := range as {
			amounts[a.Row] = a.Amount
		}
	}
	if *limit > 0 && *limit < len(rows) {
		rows = rows[:*limit]
	}
	dataset, err := fingerprint(*data, !*noAmount)
	if err != nil {
		return err
	}

	ctx := context.Background()
	cmd := exec.Command(*bin, "mcp", "--no-update-check")
	cmd.Env = append(os.Environ(), "TYPESAFE_PROFILE="+*profile)
	cmd.Stderr = os.Stderr
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "taxcat", Version: "1"}, nil).Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return err
	}
	defer cs.Close()

	var crit map[string]string
	if err := json.Unmarshal(criteriaJSON, &crit); err != nil {
		return err
	}
	questions := map[string]any{}
	if *protocol == "choice" {
		questions["tax_category"] = map[string]any{"type": "choice", "instructions": question, "criteria": json.RawMessage(criteriaJSON)}
	} else {
		// No dataset row is labeled none_of_these, so noul asks only the real
		// categories; a "none of the others" noul would be a different question.
		for _, l := range labels {
			if l == "none_of_these" {
				continue
			}
			questions[l] = map[string]any{"type": "noul", "instructions": "This bank transaction belongs in this US personal income tax category: " + crit[l]}
		}
	}

	os.MkdirAll(*outDir, 0o755)
	out, err := os.Create(filepath.Join(*outDir, *name+".jsonl"))
	if err != nil {
		return err
	}
	defer out.Close()
	enc := json.NewEncoder(out)
	var metas []json.RawMessage
	var cost *price
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
			id := strconv.Itoa(r.Row)
			rec := map[string]string{"payee": r.Payee}
			text := r.Payee
			if a, ok := amounts[r.Row]; ok {
				rec["approx_amount"] = approx(a)
				text += ", a " + approx(a)
			}
			if *protocol == "noul" {
				items[id] = "Bank transaction: " + text
			} else {
				items[id] = rec
			}
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
				q := ans["tax_category"]
				a.Choice, a.Confidence, a.Probabilities = q.Choice, q.Confidence, q.Probabilities
			} else {
				a.Probabilities, a.Choice, a.Confidence = argmax(ans)
			}
			if err := enc.Encode(a); err != nil {
				return err
			}
		}
		fmt.Fprintf(os.Stderr, "%s: rows %d-%d done, %d errors so far\n", *name, batch[0].Row, batch[len(batch)-1].Row, len(errs))
	}
	meta, _ := json.MarshalIndent(map[string]any{"profile": *profile, "protocol": *protocol, "with_amount": !*noAmount, "dataset": dataset, "limit": *limit, "calls": metas, "errors": errs, "price": cost}, "", "  ")
	return os.WriteFile(filepath.Join(*outDir, *name+".meta.json"), append(meta, '\n'), 0o644)
}

// fingerprint hashes what a run was asked and graded on: the criteria, the
// question, the rows and, when sent, the amounts. score skips a pass whose
// fingerprint differs, since its row numbers may now name other payees.
func fingerprint(dir string, withAmount bool) (string, error) {
	h := sha256.New()
	h.Write(criteriaJSON)
	h.Write([]byte(question))
	files := []string{"payees.jsonl"}
	if withAmount {
		files = append(files, "amounts.jsonl")
	}
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			return "", err
		}
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// argmax turns per-category noul answers into a normalized distribution, the
// top category, and the choice confidence formula over it, so noul runs score
// like choice runs.
func argmax(ans map[string]apiAnswer) (map[string]float64, string, float64) {
	p := map[string]float64{}
	var sum float64
	for k, v := range ans {
		p[k] = v.Noul
		sum += v.Noul
	}
	best := ""
	for _, l := range labels {
		if _, ok := p[l]; ok && (best == "" || p[l] > p[best]) {
			best = l
		}
	}
	if sum > 0 {
		for k := range p {
			p[k] /= sum
		}
	}
	n := float64(len(p))
	conf := 0.0
	if n > 1 {
		conf = (n*p[best] - 1) / (n - 1)
	}
	return p, best, conf
}
