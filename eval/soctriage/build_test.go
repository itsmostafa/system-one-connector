package main

import (
	"encoding/json"
	"strings"
	"testing"
)

var testTr = translations{
	RuleName:   map[string]string{"SQL注入攻击": "SQL injection attack", "目录穿越": "Directory traversal", "命令执行": "Command execution"},
	AttackType: map[string]string{"SQL注入": "SQL injection", "目录遍历": "Directory traversal"},
	KillChain:  map[string]string{"入侵": "Intrusion", "漏洞利用": "Exploitation"},
}

func mk(rule, uri, label string) alert {
	return alert{RuleName: rule, AttackType: "SQL注入", KillChainAll: "入侵:0x02000000|漏洞利用:0x02020000", URI: &uri, SourceLabel: label}
}

func TestSelectAlerts(t *testing.T) {
	zhBody := "<title>登录</title>"
	cjkAlert := mk("SQL注入攻击", "/zh", "Non-Attack")
	cjkAlert.RspBody = &zhBody
	raw := []alert{
		mk("命令执行", "/a", "Attack"),
		mk("命令执行", "/a", "Attack"), // duplicate
		mk("目录穿越", "/b", "Attack"),
		mk("目录穿越", "/b", "Non-Attack"), // conflicts with the row above
		cjkAlert,
	}
	for i := range 6 {
		raw = append(raw, mk("SQL注入攻击", "/sql"+string(rune('0'+i)), "Non-Attack"))
	}
	raw = append(raw, mk("目录穿越", "/c", "Non-Attack"), mk("目录穿越", "/d", "Non-Attack"))

	out, st, err := selectAlerts(raw, testTr, 4)
	if err != nil {
		t.Fatal(err)
	}
	if st.Unique != 10 || st.Conflicting != 1 || st.ForeignTraffic != 1 || st.Pool != 9 {
		t.Errorf("stats = %+v, want unique 10, conflicting 1, foreign 1, pool 9", st)
	}
	if st.Labels[escalate] != 1 || st.Labels[falsePositive] != 3 {
		t.Errorf("labels = %v, want 1 escalate, 3 false_positive", st.Labels)
	}
	// The fill takes one false positive per rule per round, so the two
	// directory-traversal rows get in beside the six SQL-injection rows.
	rules := map[string]int{}
	for i, a := range out {
		if a.Row != i+1 {
			t.Errorf("row %d numbered %d", i+1, a.Row)
		}
		rules[a.RuleName]++
		if a.SourceLabel != "" {
			t.Errorf("source label leaked: %+v", a)
		}
		if a.KillChainAll != "Intrusion:0x02000000|Exploitation:0x02020000" {
			t.Errorf("kill chain = %q", a.KillChainAll)
		}
		if a.AttackType != "SQL injection" {
			t.Errorf("attack type = %q", a.AttackType)
		}
	}
	if rules["Directory traversal"] != 2 || rules["SQL injection attack"] != 1 || rules["Command execution"] != 1 {
		t.Errorf("rules = %v, want 2 directory traversal, 1 SQL injection, 1 command execution", rules)
	}

	again, _, _ := selectAlerts(raw, testTr, 4)
	a, _ := json.Marshal(out)
	b, _ := json.Marshal(again)
	if string(a) != string(b) {
		t.Error("selection is not deterministic")
	}
	if strings.Contains(string(a), `"Label"`) || foreign(string(a)) {
		t.Errorf("output keeps the source label or non-English text: %s", a)
	}
}

func TestSelectAlertsMissingTranslation(t *testing.T) {
	raw := []alert{mk("未知规则", "/x", "Attack")}
	_, _, err := selectAlerts(raw, testTr, 1)
	if err == nil || !strings.Contains(err.Error(), "rule_name: 未知规则") {
		t.Errorf("err = %v, want missing rule_name", err)
	}
}

func TestSelectAlertsTooSmall(t *testing.T) {
	raw := []alert{mk("命令执行", "/a", "Attack"), mk("目录穿越", "/b", "Attack")}
	if _, _, err := selectAlerts(raw, testTr, 1); err == nil {
		t.Error("want an error when escalate alerts exceed the set size")
	}
	if _, _, err := selectAlerts(raw, testTr, 3); err == nil {
		t.Error("want an error when the pool is smaller than the set size")
	}
}

func TestForeign(t *testing.T) {
	for s, want := range map[string]bool{
		`{"name1":"КАРГОАЭРОБРОКЕР"}`:   true,
		"\x16\x03\x03h2Q.\rÃe[Ί%k/?o3":  false, // TLS bytes with one stray Greek letter
		"BeanComparatorώ\x01N~":         false,
		"HTTP Status 400 – Bad Request": false,
		"GET /index.php?id=1 HTTP/1.1":  false,
		"<title>登录</title>":             true,
		"（CVE-2017-3066）":               true, // full-width parentheses
		"ログイン":                          true,
		"café ü":                        false,
	} {
		if got := foreign(s); got != want {
			t.Errorf("foreign(%q) = %v, want %v", s, got, want)
		}
	}
}
