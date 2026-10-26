package main

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"
)

// alert is one SecAlertBench record with its original field names. Five SIP
// alerts carry no HTTP exchange, so those fields are pointers that drop out
// of the eval set when null.
type alert struct {
	Row          int     `json:"row,omitempty"`
	Label        string  `json:"label,omitempty"`
	AttackType   string  `json:"attack_type"`
	RuleName     string  `json:"rule_name"`
	KillChainAll string  `json:"kill_chain_all"`
	Proto        string  `json:"proto"`
	Method       *string `json:"method,omitempty"`
	Host         *string `json:"host,omitempty"`
	URI          *string `json:"uri,omitempty"`
	Parameter    *string `json:"parameter,omitempty"`
	ReqHeader    *string `json:"req_header,omitempty"`
	ReqBody      *string `json:"req_body,omitempty"`
	RspStatus    *int    `json:"rsp_status,omitempty"`
	RspHeader    *string `json:"rsp_header,omitempty"`
	RspBody      *string `json:"rsp_body,omitempty"`
	Sip          string  `json:"sip,omitempty"`
	Sport        int     `json:"sport,omitempty"`
	Dip          string  `json:"dip,omitempty"`
	Dport        int     `json:"dport"`
	Xff          string  `json:"xff,omitempty"`
	// SourceLabel is SecAlertBench's own label, Attack or Non-Attack. It is
	// read from the source and never written to the eval set.
	SourceLabel string `json:"Label,omitempty"`
}

const (
	escalate      = "escalate"
	falsePositive = "false_positive"
)

var labelMap = map[string]string{"Attack": escalate, "Non-Attack": falsePositive}

// translations maps the detector's Chinese labels to English. Kill-chain
// stages are translated by name, keeping each stage's hex code.
type translations struct {
	RuleName   map[string]string `json:"rule_name"`
	AttackType map[string]string `json:"attack_type"`
	KillChain  map[string]string `json:"kill_chain"`
}

type stats struct {
	Raw, Unique, Conflicting, ForeignTraffic, Pool, Selected int
	PoolLabels, Labels                                       map[string]int
}

func (s stats) String() string {
	return fmt.Sprintf(`raw alerts               %d
after dedupe             %d (dropped %d keys with conflicting labels)
non-English traffic      %d dropped
English pool             %d (%d escalate, %d false_positive)
eval set                 %d (%d escalate, %d false_positive)
`, s.Raw, s.Unique, s.Conflicting, s.ForeignTraffic, s.Pool, s.PoolLabels[escalate], s.PoolLabels[falsePositive], s.Selected, s.Labels[escalate], s.Labels[falsePositive])
}

func (a alert) key() string {
	return keyHash(a.RuleName, deref(a.URI), deref(a.ReqBody))
}

// selectAlerts turns raw SecAlertBench records into the eval set:
//
//  1. Dedupe on rule name, URI and request body, dropping any key that
//     appears with both labels.
//  2. Drop alerts whose traffic contains non-English text (see foreign),
//     since translating raw traffic would change the evidence.
//  3. Translate the detector's labels to English; a string missing from the
//     table is an error, never passed through.
//  4. Keep every escalate alert and fill to size with false positives taken
//     one per rule name per round, so no single noisy rule dominates.
func selectAlerts(raw []alert, tr translations, size int) ([]alert, stats, error) {
	st := stats{Raw: len(raw), PoolLabels: map[string]int{}, Labels: map[string]int{}}

	byKey := map[string][]alert{}
	var keys []string
	for _, a := range raw {
		k := a.key()
		if byKey[k] == nil {
			keys = append(keys, k)
		}
		byKey[k] = append(byKey[k], a)
	}
	var unique []alert
	for _, k := range keys {
		group := byKey[k]
		if slices.ContainsFunc(group, func(a alert) bool { return a.SourceLabel != group[0].SourceLabel }) {
			st.Conflicting++
			continue
		}
		unique = append(unique, group[0])
	}
	st.Unique = len(unique)

	var pool []alert
	missing := map[string]bool{}
	for _, a := range unique {
		if slices.ContainsFunc(a.traffic(), foreign) {
			st.ForeignTraffic++
			continue
		}
		label, ok := labelMap[a.SourceLabel]
		if !ok {
			return nil, st, fmt.Errorf("unknown source label %q", a.SourceLabel)
		}
		a.Label, a.SourceLabel = label, ""
		a.RuleName = lookup(tr.RuleName, a.RuleName, "rule_name", missing)
		a.AttackType = lookup(tr.AttackType, a.AttackType, "attack_type", missing)
		stages := strings.Split(a.KillChainAll, "|")
		for i, s := range stages {
			name, code, _ := strings.Cut(s, ":")
			stages[i] = lookup(tr.KillChain, name, "kill_chain", missing) + ":" + code
		}
		a.KillChainAll = strings.Join(stages, "|")
		pool = append(pool, a)
		st.PoolLabels[label]++
	}
	if len(missing) > 0 {
		return nil, st, fmt.Errorf("translations.json is missing:\n  %s", strings.Join(slices.Sorted(maps.Keys(missing)), "\n  "))
	}
	for _, a := range pool {
		if s := a.RuleName + a.AttackType + a.KillChainAll; foreign(s) {
			return nil, st, fmt.Errorf("translation left non-English text: %q", s)
		}
	}
	st.Pool = len(pool)

	var out []alert
	byRule := map[string][]alert{}
	for _, a := range pool {
		if a.Label == escalate {
			out = append(out, a)
		} else {
			byRule[a.RuleName] = append(byRule[a.RuleName], a)
		}
	}
	if len(out) > size {
		return nil, st, fmt.Errorf("%d escalate alerts do not fit in a set of %d", len(out), size)
	}
	if len(pool) < size {
		return nil, st, fmt.Errorf("only %d English alerts, fewer than the %d asked for", len(pool), size)
	}
	rules := slices.Sorted(maps.Keys(byRule))
	for _, r := range rules {
		slices.SortFunc(byRule[r], func(x, y alert) int { return cmp.Compare(x.key(), y.key()) })
	}
	for round := 0; len(out) < size; round++ {
		for _, r := range rules {
			if round < len(byRule[r]) && len(out) < size {
				out = append(out, byRule[r][round])
			}
		}
	}

	slices.SortFunc(out, func(x, y alert) int { return cmp.Compare(x.key(), y.key()) })
	for i := range out {
		out[i].Row = i + 1
		st.Labels[out[i].Label]++
	}
	st.Selected = len(out)
	return out, st, nil
}

// traffic is every field the detector did not write: the HTTP exchange.
func (a alert) traffic() []string {
	return []string{deref(a.Method), deref(a.Host), deref(a.URI), deref(a.Parameter), deref(a.ReqHeader), deref(a.ReqBody), deref(a.RspHeader), deref(a.RspBody), a.Xff}
}

func lookup(m map[string]string, s, field string, missing map[string]bool) string {
	if t, ok := m[s]; ok {
		return t
	}
	missing[field+": "+s] = true
	return s
}

// foreign reports text in a language other than English: any Chinese,
// Japanese or Korean (see cjk), or a run of three or more letters in another
// non-Latin script such as Cyrillic. Binary payloads (TLS handshakes,
// serialized Java objects) decode to stray non-Latin letters too, so a single
// one is not enough.
func foreign(s string) bool {
	if cjk(s) {
		return true
	}
	run := 0
	for _, r := range s {
		if unicode.IsLetter(r) && r > 0xff && !unicode.Is(unicode.Latin, r) {
			if run++; run >= 3 {
				return true
			}
		} else {
			run = 0
		}
	}
	return false
}

// cjk reports Chinese, Japanese or Korean script, or the full-width and CJK
// punctuation that comes with it.
func cjk(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool {
		return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) ||
			(r >= 0x3000 && r <= 0x303f) || (r >= 0xff00 && r <= 0xffef)
	})
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
