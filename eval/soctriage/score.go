package main

import (
	"cmp"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/itsmostafa/system-one-connector/eval/internal/openrouter"
)

var thresholds = []float64{0.3, 0.5, 0.7}

// baselineName labels the rule-name baseline in the report.
const baselineName = "baseline: rule name"

// report holds one run's scores, or the mean over several passes of a run.
// AttacksEscalated, FPClosed and FPEscalated are shares of each labeled
// class; precision is the share of escalations that were attacks.
type report struct {
	Name                  string
	N, Passes             int
	Errors                float64
	AttacksEscalated      float64
	FPClosed, FPEscalated float64
	Precision, F1         float64
	Acc, MacroF1, ECE     float64
	AUC                   float64 // how well p(escalate) ranks attacks above false positives, whatever the cutoff
	RecallMin, RecallMax  float64 // spread of AttacksEscalated across passes
	// Counts are per-pass means of the 2x2 of labels against answers.
	// Alerts that errored upstream are in no cell; Errors counts them.
	Counts   counts
	Coverage []coverage
	Missed   []missed
	Meta     runMeta
	Cost100  float64 // USD per 100 alerts, each pass at its own saved rate
	Costed   bool    // false when no pass saved a rate
}

type counts struct {
	AttacksEscalated, AttacksClosed float64
	FPEscalated, FPClosed           float64
}

type coverage struct {
	Threshold, Coverage, Acc float64
}

type missed struct {
	Row       int
	Rule, URI string
	Want, Got string
	Conf      float64
}

type runMeta struct {
	Profile  string            `json:"profile"`
	Protocol string            `json:"protocol"`
	Dataset  string            `json:"dataset"`
	Limit    int               `json:"limit"`
	Calls    []callMeta        `json:"calls"`
	Errors   map[string]string `json:"errors"`
	Price    *openrouter.Price `json:"price"`
}

// callMeta is one evaluate call's model and usage, summed over its items.
type callMeta struct {
	Model        string `json:"model"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	ItemCount    int    `json:"item_count"`
	LatencyMS    int64  `json:"latency_ms"`
}

func score(args []string) error {
	fs := flag.NewFlagSet("score", flag.ExitOnError)
	data := fs.String("data", "data/soctriage", "dataset directory")
	dir := fs.String("results", "results/soctriage", "results directory")
	fs.Parse(args)
	rows, err := readJSONL[alert](filepath.Join(*data, alertsFile))
	if err != nil {
		return err
	}
	fp, err := fingerprint(*data)
	if err != nil {
		return err
	}
	reps := []report{scoreRun(baselineName, rows, ruleBaseline(rows))}
	files, _ := filepath.Glob(filepath.Join(*dir, "*.jsonl"))
	var order []string
	groups := map[string][]report{}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".jsonl")
		var meta runMeta
		if b, err := os.ReadFile(strings.TrimSuffix(f, ".jsonl") + ".meta.json"); err == nil {
			json.Unmarshal(b, &meta)
		}
		if meta.Dataset != fp {
			fmt.Fprintf(os.Stderr, "soctriage: skipping %s: run on dataset %.7s, current dataset is %.7s\n", name, cmp.Or(meta.Dataset, "(none)"), fp)
			continue
		}
		ans, err := readJSONL[answer](f)
		if err != nil {
			return err
		}
		// Score a -limit run on the rows it was given, not the whole set.
		scored := rows
		if meta.Limit > 0 && meta.Limit < len(rows) {
			scored = rows[:meta.Limit]
		}
		r := scoreRun(name, scored, ans)
		r.Meta = meta
		r.Errors = float64(len(meta.Errors))
		key := groupKey(name, meta, len(rows))
		if groups[key] == nil {
			order = append(order, key)
		}
		groups[key] = append(groups[key], r)
	}
	for _, k := range order {
		reps = append(reps, average(k, groups[k]))
	}
	writeMarkdown(os.Stdout, reps)
	return nil
}

// passSuffix marks repeat passes of one run: jev.p1, jev.p2 and jev.p3 score as jev.
var passSuffix = regexp.MustCompile(`\.p\d+$`)

// groupKey folds repeat passes of one run together. A -limit run gets its own
// group, so a smoke run never averages with full passes of the same name.
func groupKey(name string, meta runMeta, rows int) string {
	key := passSuffix.ReplaceAllString(name, "")
	if meta.Limit > 0 && meta.Limit < rows {
		key += fmt.Sprintf(" (first %d)", meta.Limit)
	}
	return key
}

// ruleBaseline answers each alert with the label most other alerts sharing
// its rule name carry, leaving the alert itself out so it cannot vote for
// its own label. With no other alert under the rule, or a tie, it answers
// false_positive, the larger class. A model that cannot beat this is
// reacting to the rule name, not reading the alert.
func ruleBaseline(rows []alert) []answer {
	count := map[string]map[string]int{}
	for _, r := range rows {
		if count[r.RuleName] == nil {
			count[r.RuleName] = map[string]int{}
		}
		count[r.RuleName][r.Label]++
	}
	out := make([]answer, len(rows))
	for i, r := range rows {
		esc, fp := count[r.RuleName][escalate], count[r.RuleName][falsePositive]
		if r.Label == escalate {
			esc--
		} else {
			fp--
		}
		p := 0.5
		if esc+fp > 0 {
			p = float64(esc) / float64(esc+fp)
		}
		choice := falsePositive
		if p > 0.5 {
			choice = escalate
		}
		out[i] = answer{Row: r.Row, Choice: choice, Confidence: math.Abs(2*p - 1), Probabilities: map[string]float64{escalate: p, falsePositive: 1 - p}}
	}
	return out
}

// scoreRun scores one run. Rows with no answer (upstream errors) count as
// wrong, so a flaky host cannot raise its scores by failing hard alerts.
func scoreRun(name string, rows []alert, ans []answer) report {
	got := map[int]answer{}
	for _, a := range ans {
		got[a.Row] = a
	}
	r := report{Name: name, N: len(rows), Passes: 1}
	tp, fp := map[string]int{}, map[string]int{}
	support := map[string]int{}
	var right int
	type pt struct {
		p  float64
		ok bool
	}
	var pts []pt
	for _, row := range rows {
		a, ok := got[row.Row]
		pred := a.Choice
		if !ok {
			pred = "(error)"
		}
		hit := pred == row.Label
		support[row.Label]++
		if hit {
			right++
			tp[row.Label]++
		} else {
			fp[pred]++
			r.Missed = append(r.Missed, missed{row.Row, row.RuleName, deref(row.URI), row.Label, pred, a.Confidence})
		}
		if ok {
			pts = append(pts, pt{a.Probabilities[pred], hit})
		}
	}
	r.Acc = ratio(right, len(rows))
	r.AttacksEscalated = ratio(tp[escalate], support[escalate])
	r.FPClosed = ratio(tp[falsePositive], support[falsePositive])
	r.FPEscalated = ratio(fp[escalate], support[falsePositive])
	r.Precision = ratio(tp[escalate], tp[escalate]+fp[escalate])
	r.RecallMin, r.RecallMax = r.AttacksEscalated, r.AttacksEscalated
	r.Counts = counts{float64(tp[escalate]), float64(fp[falsePositive]), float64(fp[escalate]), float64(tp[falsePositive])}
	var f1s []float64
	for _, l := range labels {
		if support[l] == 0 {
			continue
		}
		f := f1(ratio(tp[l], tp[l]+fp[l]), ratio(tp[l], support[l]))
		f1s = append(f1s, f)
		if l == escalate {
			r.F1 = f
		}
	}
	r.MacroF1 = mean(f1s)
	r.AUC = auc(rows, got)
	slices.SortFunc(r.Missed, func(a, b missed) int { return cmp.Compare(b.Conf, a.Conf) })

	// ECE over ten equal-width bins of the chosen option's probability.
	var bins [10]struct {
		n, ok int
		p     float64
	}
	for _, x := range pts {
		i := min(int(x.p*10), 9)
		bins[i].n++
		bins[i].p += x.p
		if x.ok {
			bins[i].ok++
		}
	}
	for _, b := range bins {
		if b.n > 0 {
			r.ECE += float64(b.n) / float64(len(pts)) * math.Abs(b.p/float64(b.n)-float64(b.ok)/float64(b.n))
		}
	}

	for _, t := range thresholds {
		var kept, keptRight int
		for _, row := range rows {
			a, ok := got[row.Row]
			if !ok || a.Confidence < t {
				continue
			}
			kept++
			if a.Choice == row.Label {
				keptRight++
			}
		}
		r.Coverage = append(r.Coverage, coverage{t, ratio(kept, len(rows)), ratio(keptRight, kept)})
	}
	return r
}

// average folds repeat passes of one run into a single report: rates and
// coverage become per-pass means, and the misses list keeps each alert and
// prediction once at its highest confidence.
func average(name string, ps []report) report {
	n := float64(len(ps))
	a := report{Name: name, N: ps[0].N, Passes: len(ps), Meta: ps[0].Meta, RecallMin: ps[0].AttacksEscalated, RecallMax: ps[0].AttacksEscalated}
	a.Meta.Calls = nil
	seen := map[[2]string]int{}
	metas := make([]runMeta, len(ps))
	for i, p := range ps {
		metas[i] = p.Meta
		a.Errors += p.Errors / n
		a.AttacksEscalated += p.AttacksEscalated / n
		a.FPClosed += p.FPClosed / n
		a.FPEscalated += p.FPEscalated / n
		a.Counts.AttacksEscalated += p.Counts.AttacksEscalated / n
		a.Counts.AttacksClosed += p.Counts.AttacksClosed / n
		a.Counts.FPEscalated += p.Counts.FPEscalated / n
		a.Counts.FPClosed += p.Counts.FPClosed / n
		a.Precision += p.Precision / n
		a.F1 += p.F1 / n
		a.Acc += p.Acc / n
		a.MacroF1 += p.MacroF1 / n
		a.ECE += p.ECE / n
		a.AUC += p.AUC / n
		a.RecallMin, a.RecallMax = min(a.RecallMin, p.AttacksEscalated), max(a.RecallMax, p.AttacksEscalated)
		for j, c := range p.Coverage {
			if i == 0 {
				a.Coverage = append(a.Coverage, coverage{Threshold: c.Threshold})
			}
			a.Coverage[j].Coverage += c.Coverage / n
			a.Coverage[j].Acc += c.Acc / n
		}
		for _, m := range p.Missed {
			k := [2]string{fmt.Sprint(m.Row), m.Got}
			if j, ok := seen[k]; ok {
				a.Missed[j].Conf = max(a.Missed[j].Conf, m.Conf)
				continue
			}
			seen[k] = len(a.Missed)
			a.Missed = append(a.Missed, m)
		}
		a.Meta.Calls = append(a.Meta.Calls, p.Meta.Calls...)
	}
	a.Cost100, a.Costed = costPer100(metas)
	slices.SortFunc(a.Missed, func(x, y missed) int { return cmp.Compare(y.Conf, x.Conf) })
	return a
}

// costPer100 prices each pass's calls at the rate that pass saved and scales
// the total to 100 alerts. Passes with no saved rate are left out; it reports
// false when no pass has a rate or the priced passes have no items.
func costPer100(passes []runMeta) (float64, bool) {
	var usd float64
	var items int
	for _, m := range passes {
		if m.Price == nil {
			continue
		}
		for _, c := range m.Calls {
			usd += float64(c.InputTokens)*m.Price.Prompt + float64(c.OutputTokens)*m.Price.Completion
			items += c.ItemCount
		}
	}
	if items == 0 {
		return 0, false
	}
	return usd * 100 / float64(items), true
}

// auc is the area under the ROC curve of p(escalate): the chance that a
// random attack scores above a random false positive, ties counting half.
// Rows with no answer are left out.
func auc(rows []alert, got map[int]answer) float64 {
	var pos, neg []float64
	for _, row := range rows {
		a, ok := got[row.Row]
		if !ok {
			continue
		}
		if row.Label == escalate {
			pos = append(pos, a.Probabilities[escalate])
		} else {
			neg = append(neg, a.Probabilities[escalate])
		}
	}
	if len(pos) == 0 || len(neg) == 0 {
		return 0
	}
	slices.Sort(neg)
	var wins float64
	for _, p := range pos {
		below, _ := slices.BinarySearch(neg, p)
		end := below
		for end < len(neg) && neg[end] == p {
			end++
		}
		wins += float64(below) + float64(end-below)/2
	}
	return wins / float64(len(pos)*len(neg))
}

func f1(p, r float64) float64 {
	if p+r == 0 {
		return 0
	}
	return 2 * p * r / (p + r)
}

func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

func mean(xs []float64) float64 {
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(max(len(xs), 1))
}

// num prints a per-pass count, with a decimal only when passes disagree.
func num(x float64) string {
	if x == math.Trunc(x) {
		return fmt.Sprintf("%.0f", x)
	}
	return fmt.Sprintf("%.1f", x)
}

func pct(x float64) string { return fmt.Sprintf("%.1f%%", 100*x) }

func writeMarkdown(w io.Writer, reps []report) {
	fmt.Fprintln(w, "## Headline")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Attacks escalated is the share of real attacks the model escalated (higher is better). False positives closed is the share of harmless alerts it correctly closed (higher is better), and false positives escalated is the rest, which an analyst would have to look at for nothing (lower is better). Precision is the share of escalations that were real attacks. AUC is the chance an attack gets a higher escalate probability than a false positive, so it shows how well a model reads the alerts whatever cutoff it answers at. Each figure is the mean over the run's passes. The rule-name baseline guesses each alert's label from the other alerts under the same rule.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Run | Model | Passes | Alerts | Attacks escalated | Attacks escalated, range across passes | False positives closed | False positives escalated | Precision | Escalate F1 | Accuracy | Macro-F1 | AUC | ECE | Errors |")
	fmt.Fprintln(w, "|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|")
	for _, r := range reps {
		model := ""
		if len(r.Meta.Calls) > 0 {
			model = "`" + r.Meta.Calls[0].Model + "`"
		}
		fmt.Fprintf(w, "| `%s` | %s | %d | %d | %s | %s–%s | %s | %s | %s | %.3f | %s | %.3f | %.3f | %.3f | %.1f |\n",
			r.Name, model, r.Passes, r.N, pct(r.AttacksEscalated), pct(r.RecallMin), pct(r.RecallMax), pct(r.FPClosed), pct(r.FPEscalated), pct(r.Precision), r.F1, pct(r.Acc), r.MacroF1, r.AUC, r.ECE, r.Errors)
	}

	fmt.Fprintln(w, "\n## Answers against labels")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Alert counts per pass. Alerts that errored upstream are left out.")
	for _, r := range reps {
		c := r.Counts
		fmt.Fprintf(w, "\n`%s`\n\n| Labeled | Model said escalate | Model said false positive |\n|---|---|---|\n", r.Name)
		fmt.Fprintf(w, "| Attack | %s attacks escalated | %s attacks closed (missed) |\n", num(c.AttacksEscalated), num(c.AttacksClosed))
		fmt.Fprintf(w, "| False positive | %s false positives escalated | %s false positives closed |\n", num(c.FPEscalated), num(c.FPClosed))
	}

	fmt.Fprintln(w, "\n## Coverage when abstaining below a confidence threshold")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Run | ≥0.3 coverage / accuracy | ≥0.5 | ≥0.7 |")
	fmt.Fprintln(w, "|---|---|---|---|")
	for _, r := range reps {
		fmt.Fprintf(w, "| `%s` |", r.Name)
		for _, c := range r.Coverage {
			fmt.Fprintf(w, " %s / %s |", pct(c.Coverage), pct(c.Acc))
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintln(w, "\n## Cost and speed")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Cost is USD per 100 alerts at the OpenRouter rate saved when the run was made; `—` means the run saved no rate.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Run | Input tokens / pass | Output tokens / pass | Wall clock / pass | Cost / 100 |")
	fmt.Fprintln(w, "|---|---|---|---|---|")
	for _, r := range reps {
		if r.Name == baselineName {
			continue
		}
		var in, out int
		var ms int64
		for _, c := range r.Meta.Calls {
			in, out, ms = in+c.InputTokens, out+c.OutputTokens, ms+c.LatencyMS
		}
		p := max(r.Passes, 1)
		cost := "—"
		if r.Costed {
			cost = fmt.Sprintf("$%.6f", r.Cost100)
		}
		fmt.Fprintf(w, "| `%s` | %d | %d | %.0fs | %s |\n", r.Name, in/p, out/p, float64(ms)/1000/float64(p), cost)
	}

	for _, r := range reps {
		if r.Name == baselineName {
			continue
		}
		fmt.Fprintf(w, "\n## `%s`: most confident misses\n\n| Row | Rule | URI | Labeled | Predicted | Confidence |\n|---|---|---|---|---|---|\n", r.Name)
		for _, m := range r.Missed[:min(10, len(r.Missed))] {
			fmt.Fprintf(w, "| %d | %s | `%s` | `%s` | `%s` | %.2f |\n", m.Row, m.Rule, cell(m.URI, 60), m.Want, m.Got, m.Conf)
		}
	}
}

// cell shortens s and keeps it from breaking a Markdown table.
func cell(s string, n int) string {
	s = strings.NewReplacer("|", "\\|", "`", "'", "\n", " ", "\r", "").Replace(s)
	if r := []rune(s); len(r) > n {
		s = string(r[:n]) + "…"
	}
	return s
}
