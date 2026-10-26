package main

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func near(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func row(n int, rule, label string) alert {
	uri := "/" + rule
	return alert{Row: n, RuleName: rule, Label: label, URI: &uri}
}

func TestScoreRun(t *testing.T) {
	rows := []alert{
		row(1, "a", escalate),
		row(2, "a", escalate),
		row(3, "b", falsePositive),
		row(4, "b", falsePositive),
		row(5, "b", falsePositive),
	}
	ans := []answer{
		{Row: 1, Choice: escalate, Confidence: 0.8, Probabilities: map[string]float64{escalate: 0.9}},
		// row 2 failed upstream and has no answer
		{Row: 3, Choice: escalate, Confidence: 0.6, Probabilities: map[string]float64{escalate: 0.8}},
		{Row: 4, Choice: falsePositive, Confidence: 0.4, Probabilities: map[string]float64{falsePositive: 0.7}},
		{Row: 5, Choice: falsePositive, Confidence: 0.2, Probabilities: map[string]float64{falsePositive: 0.6}},
	}
	r := scoreRun("t", rows, ans)
	near(t, "Acc", r.Acc, 3.0/5)
	near(t, "AttacksEscalated", r.AttacksEscalated, 0.5) // row 1 caught, row 2 errored
	near(t, "FPEscalated", r.FPEscalated, 1.0/3)         // row 3 escalated
	near(t, "FPClosed", r.FPClosed, 2.0/3)
	// The errored attack is in no cell.
	if want := (counts{AttacksEscalated: 1, FPEscalated: 1, FPClosed: 2}); r.Counts != want {
		t.Errorf("Counts = %+v, want %+v", r.Counts, want)
	}
	near(t, "Precision", r.Precision, 0.5)
	near(t, "F1", r.F1, 0.5)
	// false_positive: precision 2/2, recall 2/3, F1 0.8.
	near(t, "MacroF1", r.MacroF1, (0.5+0.8)/2)
	near(t, "AUC", r.AUC, 1) // row 1's 0.9 beats every false positive; errored row 2 is left out
	if len(r.Missed) != 2 || r.Missed[0].Row != 3 || r.Missed[1].Got != "(error)" {
		t.Errorf("Missed = %+v, want row 3 first, then the errored row 2", r.Missed)
	}
	// Coverage at 0.5 keeps rows 1 and 3, one right.
	if c := r.Coverage[1]; c.Threshold != 0.5 || math.Abs(c.Coverage-0.4) > 1e-9 || math.Abs(c.Acc-0.5) > 1e-9 {
		t.Errorf("Coverage[0.5] = %+v, want 40%% kept at 50%% accuracy", c)
	}
}

func TestAUC(t *testing.T) {
	rows := []alert{row(1, "a", escalate), row(2, "a", escalate), row(3, "b", falsePositive), row(4, "b", falsePositive)}
	p := func(n int, v float64) answer {
		return answer{Row: n, Probabilities: map[string]float64{escalate: v}}
	}
	got := map[int]answer{1: p(1, 0.9), 2: p(2, 0.4), 3: p(3, 0.4), 4: p(4, 0.1)}
	// Pairs: 0.9 beats both; 0.4 ties 0.4 (half) and beats 0.1. 3.5 of 4.
	near(t, "auc", auc(rows, got), 3.5/4)
}

func TestRuleBaseline(t *testing.T) {
	rows := []alert{
		row(1, "a", escalate),
		row(2, "a", escalate),
		row(3, "a", falsePositive),
		row(4, "solo", escalate),
	}
	got := map[int]string{}
	for _, a := range ruleBaseline(rows) {
		got[a.Row] = a.Choice
	}
	// Row 1 sees one escalate and one false positive: a tie, so false_positive.
	// Row 3 sees two escalates. Row 4 has no other alert under its rule.
	want := map[int]string{1: falsePositive, 2: falsePositive, 3: escalate, 4: falsePositive}
	for n, w := range want {
		if got[n] != w {
			t.Errorf("row %d = %s, want %s", n, got[n], w)
		}
	}
}

func TestFromNoul(t *testing.T) {
	c, conf, p := fromNoul(0.8)
	if c != escalate || math.Abs(conf-0.6) > 1e-9 || math.Abs(p[falsePositive]-0.2) > 1e-9 {
		t.Errorf("fromNoul(0.8) = %s, %v, %v", c, conf, p)
	}
	if c, _, _ := fromNoul(0.3); c != falsePositive {
		t.Errorf("fromNoul(0.3) = %s, want false_positive", c)
	}
}

func TestAverage(t *testing.T) {
	a := average("jev", []report{
		{AttacksEscalated: 0.6, FPEscalated: 0.2, Counts: counts{FPClosed: 3}, Missed: []missed{{Row: 1, Got: escalate, Conf: 0.4}}},
		{AttacksEscalated: 0.8, FPEscalated: 0.4, Counts: counts{FPClosed: 4}, Missed: []missed{{Row: 1, Got: escalate, Conf: 0.7}, {Row: 2, Got: falsePositive, Conf: 0.1}}},
	})
	near(t, "AttacksEscalated", a.AttacksEscalated, 0.7)
	near(t, "FPEscalated", a.FPEscalated, 0.3)
	near(t, "Counts.FPClosed", a.Counts.FPClosed, 3.5)
	near(t, "RecallMin", a.RecallMin, 0.6)
	near(t, "RecallMax", a.RecallMax, 0.8)
	if a.Passes != 2 || len(a.Missed) != 2 || a.Missed[0].Conf != 0.7 {
		t.Errorf("average = %+v, want 2 passes and row 1's miss kept once at 0.7", a)
	}
}

func TestNum(t *testing.T) {
	if num(977) != "977" || num(3.5) != "3.5" {
		t.Errorf("num = %q, %q", num(977), num(3.5))
	}
}

func TestGroupKey(t *testing.T) {
	if k := groupKey("jev.p2", runMeta{}, 4000); k != "jev" {
		t.Errorf("groupKey = %q, want jev", k)
	}
	if k := groupKey("jev", runMeta{Limit: 10}, 4000); k != "jev (first 10)" {
		t.Errorf("groupKey = %q, want a separate smoke group", k)
	}
}

func TestStateHidesLabelAndRandomIPs(t *testing.T) {
	uri := "/login"
	a := alert{Row: 7, Label: escalate, RuleName: "r", URI: &uri, Sip: "1.2.3.4", Dip: "5.6.7.8", Sport: 4444, Dport: 80}
	b, _ := json.Marshal(a.state())
	s := string(b)
	for _, leak := range []string{`"row"`, `"label"`, "1.2.3.4", "5.6.7.8", `"sport"`, `"xff"`} {
		if strings.Contains(s, leak) {
			t.Errorf("state %s contains %s", s, leak)
		}
	}
	if !strings.Contains(s, `"dport":80`) || !strings.Contains(s, `"uri":"/login"`) {
		t.Errorf("state %s lost the port or URI", s)
	}
}
