package main

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestScoreRun(t *testing.T) {
	rows := []payee{
		{Row: 1, Payee: "Kaiser", Label: "medical", Origin: "real"},
		{Row: 2, Payee: "Red Cross", Label: "charitable", Origin: "added", Hard: true},
		{Row: 3, Payee: "Starbucks", Label: "personal", Origin: "real"},
		{Row: 4, Payee: "IRS", Label: "federal_tax", Origin: "added"},
	}
	ans := []answer{
		{Row: 1, Choice: "medical", Confidence: 0.9, Probabilities: map[string]float64{"medical": 0.95}},
		{Row: 2, Choice: "personal", Confidence: 0.4, Probabilities: map[string]float64{"personal": 0.6}},
		{Row: 3, Choice: "personal", Confidence: 0.8, Probabilities: map[string]float64{"personal": 0.85}},
		// row 4 failed upstream and has no answer
	}
	r := scoreRun("t", rows, ans)
	near := func(name string, got, want float64) {
		t.Helper()
		if math.Abs(got-want) > 1e-9 {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	near("Acc", r.Acc, 0.5)
	near("AccReal", r.AccReal, 1)
	near("AccAdd", r.AccAdd, 0)
	near("AccHard", r.AccHard, 0)
	near("DeductibleRecall", r.DeductibleRecall, 0.5) // medical hit, charitable missed
	near("Precision[personal]", r.Precision["personal"], 0.5)
	// F1: medical 1, charitable 0, personal 2/3, federal_tax 0.
	near("MacroF1", r.MacroF1, (1+0+2.0/3+0)/4)
	if len(r.Missed) != 2 || r.Missed[0].Payee != "Red Cross" {
		t.Errorf("Missed = %+v, want Red Cross first, then IRS", r.Missed)
	}
	// ≥0.5 keeps rows 1 and 3, both right; row 2 is under, row 4 has no answer.
	near("coverage@0.5", r.Coverage[1].Coverage, 0.5)
	near("acc@0.5", r.Coverage[1].Acc, 1)
}

func TestArgmax(t *testing.T) {
	p, best, conf := argmax(map[string]apiAnswer{"medical": {Noul: 0.6}, "personal": {Noul: 0.2}})
	if best != "medical" || math.Abs(p["medical"]-0.75) > 1e-9 || math.Abs(conf-0.5) > 1e-9 {
		t.Errorf("argmax = %v %q %v, want medical at 0.75, confidence 0.5", p, best, conf)
	}
}

func TestApprox(t *testing.T) {
	for in, want := range map[float64]string{-2: "under $5 charge", -20: "$20 charge", 2800: "$2,800 deposit"} {
		if got := approx(in); got != want {
			t.Errorf("approx(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestLabelsInCriteriaOrder(t *testing.T) {
	if len(labels) != 11 || labels[0] != "wage_income" || labels[10] != "none_of_these" {
		t.Errorf("labels = %v", labels)
	}
}

func TestAverage(t *testing.T) {
	a := average("jev", []report{
		{Acc: 0.9, Abstained: 2, Recall: map[string]float64{"medical": 1}, Coverage: []coverage{{0.5, 0.8, 1}},
			Confusions: []confusion{{"medical", "personal", 2}}, Missed: []missed{{"CVS", "medical", "personal", 0.4}}},
		{Acc: 0.8, Abstained: 4, Recall: map[string]float64{"medical": 0.5}, Coverage: []coverage{{0.5, 0.6, 0.5}},
			Missed: []missed{{"CVS", "medical", "personal", 0.7}}},
	})
	if a.Passes != 2 || math.Abs(a.Acc-0.85) > 1e-9 || a.AccMin != 0.8 || a.AccMax != 0.9 || a.Abstained != 3 {
		t.Errorf("average = passes %d acc %v [%v, %v] abstained %v", a.Passes, a.Acc, a.AccMin, a.AccMax, a.Abstained)
	}
	if math.Abs(a.Recall["medical"]-0.75) > 1e-9 || math.Abs(a.Coverage[0].Coverage-0.7) > 1e-9 || a.Confusions[0].N != 1 {
		t.Errorf("average recall %v coverage %+v confusions %+v", a.Recall, a.Coverage, a.Confusions)
	}
	if len(a.Missed) != 1 || a.Missed[0].Conf != 0.7 {
		t.Errorf("Missed = %+v, want CVS once at 0.7", a.Missed)
	}
}

func TestCostPer100(t *testing.T) {
	p := &price{Prompt: 0.04e-6, Completion: 1e-6}
	calls := []callMeta{
		{InputTokens: 1_000_000, OutputTokens: 10_000, ItemCount: 500},
		{InputTokens: 1_000_000, OutputTokens: 10_000, ItemCount: 500},
	}
	// 2M × $0.04/M + 20k × $1/M = $0.10 for 1,000 items, so $0.01 per 100.
	if got, ok := costPer100([]runMeta{{Price: p, Calls: calls}}); !ok || math.Abs(got-0.01) > 1e-12 {
		t.Errorf("costPer100 = %v, %v; want 0.01, true", got, ok)
	}
	if _, ok := costPer100([]runMeta{{Calls: calls}}); ok {
		t.Error("a run with no saved price got a cost")
	}
	if _, ok := costPer100([]runMeta{{Price: p}}); ok {
		t.Error("a run with no calls got a cost")
	}
	// Each pass is priced at its own rate: pass 2 at double the rate makes
	// $0.10 + $0.20 for 2,000 items, so $0.015 per 100. A pass with no saved
	// rate is left out rather than blanking the group.
	p2 := &price{Prompt: 0.08e-6, Completion: 2e-6}
	passes := []runMeta{{Calls: calls}, {Price: p, Calls: calls}, {Price: p2, Calls: calls}}
	if got, ok := costPer100(passes); !ok || math.Abs(got-0.015) > 1e-12 {
		t.Errorf("costPer100 over passes = %v, %v; want 0.015, true", got, ok)
	}
}

func TestGroupKey(t *testing.T) {
	for _, c := range []struct {
		name  string
		limit int
		want  string
	}{
		{"jev.p1", 0, "jev"},
		{"jev.p2", 1000, "jev"},
		{"jev.p3", 10, "jev (first 10)"},
		{"jev-noul", 0, "jev-noul"},
	} {
		if got := groupKey(c.name, runMeta{Limit: c.limit}, 1000); got != c.want {
			t.Errorf("groupKey(%q, limit %d) = %q, want %q", c.name, c.limit, got, c.want)
		}
	}
}

func TestFingerprint(t *testing.T) {
	dir := t.TempDir()
	put := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fp := func(withAmount bool) string {
		t.Helper()
		s, err := fingerprint(dir, withAmount)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	put("payees.jsonl", `{"row": 1, "payee": "IRS", "label": "federal_tax"}`+"\n")
	put("amounts.jsonl", `{"row": 1, "amount_rounded": -500}`+"\n")
	names, both := fp(false), fp(true)
	if names == both {
		t.Error("sending amounts did not change the fingerprint")
	}
	put("amounts.jsonl", `{"row": 1, "amount_rounded": -600}`+"\n")
	if fp(false) != names {
		t.Error("an amount changed the fingerprint of a run that sent none")
	}
	if fp(true) == both {
		t.Error("an amount change kept the fingerprint")
	}
	put("payees.jsonl", `{"row": 1, "payee": "IRS", "label": "state_local_tax"}`+"\n")
	if fp(false) == names {
		t.Error("a relabeled row kept the fingerprint")
	}
}
