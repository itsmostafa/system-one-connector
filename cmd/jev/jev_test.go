package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestEvaluate(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/systemone" || r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("bad request: %s %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var in evaluateIn
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Fatal(err)
		}
		switch in.State {
		case "busy":
			if calls == 1 {
				w.WriteHeader(529)
				return
			}
		case "bad":
			w.WriteHeader(http.StatusUnprocessableEntity)
			w.Write([]byte(`{"detail":"criteria required"}`))
			return
		}
		w.Write([]byte(`{"model":"` + in.Model + `","answers":{"q":{"type":"noul","noul":0.9}}}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, APIKey: "k", HTTP: srv.Client()}
	req := evaluateIn{Model: "jev-latest", Questions: map[string]question{"q": {Type: "noul", Instructions: "urgent?"}}}

	req.State = "busy"
	b, err := c.Evaluate(context.Background(), req)
	if err != nil || !strings.Contains(string(b), `"noul":0.9`) || calls != 2 {
		t.Fatalf("retry: calls=%d b=%s err=%v", calls, b, err)
	}

	req.State = "bad"
	if _, err := c.Evaluate(context.Background(), req); err == nil || !strings.Contains(err.Error(), "criteria required") {
		t.Fatalf("422: err=%v", err)
	}
}

func TestSetupCommands(t *testing.T) {
	cmds := setupCommands("/bin/jev", []string{"TYPESAFE_API_KEY=k", "TYPESAFE_OTHER=s"})
	want := [][]string{
		{"mcp", "remove", "jev", "-s", "user"},
		{"mcp", "add", "jev", "-s", "user", "-e", "TYPESAFE_API_KEY=k", "-e", "TYPESAFE_OTHER=s", "--", "/bin/jev", "mcp"},
		nil,
		{"mcp", "add", "jev", "--env", "TYPESAFE_API_KEY=k", "--env", "TYPESAFE_OTHER=s", "--", "/bin/jev", "mcp"},
	}
	got := [][]string{cmds[0].reset, cmds[0].add, cmds[1].reset, cmds[1].add}
	if !slices.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestSetupClaudeDesktop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude_desktop_config.json")
	seed := `{"mcpServers":{"lumi":{"command":"/bin/lumi"},"jev":{"command":"/old"}},"preferences":{"sidebarMode":"chat"}}`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := setupClaudeDesktop(path, "/bin/jev", []string{"TYPESAFE_API_KEY=k"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	var got struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
		Preferences map[string]string `json:"preferences"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	s := got.MCPServers["jev"]
	if s.Command != "/bin/jev" || !slices.Equal(s.Args, []string{"mcp"}) || s.Env["TYPESAFE_API_KEY"] != "k" {
		t.Fatalf("jev entry = %+v", s)
	}
	if got.MCPServers["lumi"].Command != "/bin/lumi" || got.Preferences["sidebarMode"] != "chat" {
		t.Fatalf("other keys lost: %s", b)
	}

	for _, seed := range []string{`null`, `{"mcpServers":null}`} {
		if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := setupClaudeDesktop(path, "/bin/jev", nil); err != nil {
			t.Fatalf("seed %s: %v", seed, err)
		}
	}
}
