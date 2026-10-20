package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// pass is one saved run of the eval.
type pass struct {
	Name    string
	Meta    runMeta
	Results []result
}

// summary folds a run's passes into the demo's headline figures, as per-pass
// means.
type summary struct {
	Name, Model       string
	Passes, N         int
	Right, RightFile  float64
	RightMin, RightMx int
	Errors            float64
	SecPerQ           float64
	Cost1000          float64 // USD per 1,000 questions, each pass at its own saved rate
	Costed            bool
	ByQuestion        map[string][]result
}

// passSuffix marks repeat passes of one run: d1.p1 and d1.p2 score as d1.
var passSuffix = regexp.MustCompile(`\.p\d+$`)

func score(args []string) error {
	fs := flag.NewFlagSet("score", flag.ExitOnError)
	suiteName := fs.String("suite", "transformers", "suite to score")
	baseline := fs.String("baseline", "d1", "run the cost multiple is measured against")
	fs.Parse(args)
	su, err := loadSuite(*suiteName)
	if err != nil {
		return err
	}
	qs := su.Questions
	files, _ := filepath.Glob(filepath.Join(resultsDir(su.Name), "*.jsonl"))
	var order []string
	groups := map[string][]pass{}
	for _, f := range files {
		p, err := readPass(f)
		if err != nil {
			return err
		}
		base := passSuffix.ReplaceAllString(p.Name, "")
		if groups[base] == nil {
			order = append(order, base)
		}
		groups[base] = append(groups[base], p)
	}
	// Grade against the current keys, not the ones saved with the run.
	byID := map[string]question{}
	for _, q := range qs {
		byID[q.ID] = q
	}
	for _, ps := range groups {
		for _, p := range ps {
			for i, r := range p.Results {
				if r.Error == "" {
					p.Results[i].RightFile, p.Results[i].Right = right(byID[r.ID], r.Path, r.Symbol)
				}
			}
		}
	}
	var sums []summary
	for _, base := range order {
		sums = append(sums, summarize(base, groups[base]))
	}
	writeMarkdown(os.Stdout, su, sums, *baseline)
	return nil
}

func readPass(path string) (pass, error) {
	p := pass{Name: strings.TrimSuffix(filepath.Base(path), ".jsonl")}
	if b, err := os.ReadFile(strings.TrimSuffix(path, ".jsonl") + ".meta.json"); err == nil {
		json.Unmarshal(b, &p.Meta)
	}
	f, err := os.Open(path)
	if err != nil {
		return p, err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(nil, 1<<20)
	for s.Scan() {
		var r result
		if err := json.Unmarshal(s.Bytes(), &r); err != nil {
			return p, fmt.Errorf("%s: %w", path, err)
		}
		p.Results = append(p.Results, r)
	}
	return p, s.Err()
}

func summarize(name string, ps []pass) summary {
	s := summary{Name: name, Model: ps[0].Meta.Model, Passes: len(ps), N: len(ps[0].Results), RightMin: -1, ByQuestion: map[string][]result{}}
	var usd float64
	var priced, ms int64
	var n int
	for _, p := range ps {
		right := 0
		var cost float64
		for _, r := range p.Results {
			s.ByQuestion[r.ID] = append(s.ByQuestion[r.ID], r)
			if r.Right {
				right++
			}
			if r.RightFile {
				s.RightFile++
			}
			if r.Error != "" {
				s.Errors++
			}
			ms += r.MS
			n++
			if p.Meta.Price != nil {
				cost += float64(r.InputTokens)*p.Meta.Price.Prompt + float64(r.OutputTokens)*p.Meta.Price.Completion
			}
		}
		s.Right += float64(right)
		if s.RightMin < 0 || right < s.RightMin {
			s.RightMin = right
		}
		s.RightMx = max(s.RightMx, right)
		// A pass with no saved rate is left out rather than blanking the run.
		if p.Meta.Price != nil {
			usd += cost
			priced += int64(len(p.Results))
		}
	}
	k := float64(len(ps))
	s.Right /= k
	s.RightFile /= k
	s.Errors /= k
	if n > 0 {
		s.SecPerQ = float64(ms) / 1000 / float64(n)
	}
	if priced > 0 {
		s.Cost1000, s.Costed = usd*1000/float64(priced), true
	}
	return s
}

func writeMarkdown(w io.Writer, su *suite, sums []summary, baseline string) {
	qs := su.Questions
	var base *summary
	for i := range sums {
		if sums[i].Name == baseline && sums[i].Costed {
			base = &sums[i]
		}
	}
	fmt.Fprintln(w, "## Headline")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Code search over %s at %s, after https://d1.liquid.ai/#/code. Figures are per-pass means; cost is USD at the OpenRouter rate each pass saved, and time is the harness's wall clock from the root to the answer, questions run one at a time.\n\n", strings.TrimPrefix(su.Repo, "https://github.com/"), su.Commit[:7])
	fmt.Fprintln(w, "| Run | Model | Passes | Right place | Right file | Cost / 1,000 questions | Time / question | Cost vs `"+baseline+"` | Errors |")
	fmt.Fprintln(w, "|---|---|---|---|---|---|---|---|---|")
	for _, s := range sums {
		rng := ""
		if s.Passes > 1 && s.RightMin != s.RightMx {
			rng = fmt.Sprintf(" (%d–%d)", s.RightMin, s.RightMx)
		}
		cost, vs := "—", "—"
		if s.Costed {
			cost = fmt.Sprintf("$%.4f", s.Cost1000)
			if base != nil && base.Cost1000 > 0 {
				vs = fmt.Sprintf("%.1f×", s.Cost1000/base.Cost1000)
			}
		}
		fmt.Fprintf(w, "| `%s` | `%s` | %d | %s of %d%s | %s of %d | %s | %.1f s | %s | %s |\n",
			s.Name, s.Model, s.Passes, num(s.Right), s.N, rng, num(s.RightFile), s.N, cost, s.SecPerQ, vs, num(s.Errors))
	}

	fmt.Fprintln(w, "\n## By question")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "✓ right place in every pass; otherwise the passes that were right, and where the first wrong pass ended.")
	fmt.Fprintln(w)
	fmt.Fprint(w, "| Question |")
	for _, s := range sums {
		fmt.Fprintf(w, " `%s` |", s.Name)
	}
	fmt.Fprint(w, "\n|---|")
	for range sums {
		fmt.Fprint(w, "---|")
	}
	fmt.Fprintln(w)
	for _, q := range qs {
		fmt.Fprintf(w, "| %s |", q.Question)
		for _, s := range sums {
			fmt.Fprintf(w, " %s |", cell(s.ByQuestion[q.ID]))
		}
		fmt.Fprintln(w)
	}
}

// cell renders one run's passes on one question.
func cell(rs []result) string {
	if len(rs) == 0 {
		return ""
	}
	right := 0
	var miss *result
	for i, r := range rs {
		if r.Right {
			right++
		} else if miss == nil {
			miss = &rs[i]
		}
	}
	if miss == nil {
		return "✓"
	}
	where := "error"
	if miss.Error == "" {
		where = "`" + miss.Path
		if miss.Symbol != "" {
			where += ":" + miss.Symbol
		}
		where += "`"
		if miss.RightFile {
			where += " (right file)"
		}
	}
	if len(rs) == 1 {
		return "✗ " + where
	}
	return fmt.Sprintf("%d/%d, ✗ %s", right, len(rs), where)
}

// num prints a per-pass mean without a needless decimal.
func num(x float64) string {
	if x == float64(int(x)) {
		return fmt.Sprint(int(x))
	}
	return fmt.Sprintf("%.1f", x)
}
