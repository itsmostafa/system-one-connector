package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
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
	c := &Client{URL: srv.URL + "/v1/systemone", APIKey: "k", HTTP: srv.Client()}
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

// The endpoint is taken verbatim, so a route's full URL reaches the server.
func TestEvaluatePostsToURL(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := &Client{URL: srv.URL + "/api/alpha/decisions", APIKey: "k", HTTP: srv.Client()}
	if _, err := c.Evaluate(context.Background(), evaluateIn{State: "x"}); err != nil {
		t.Fatal(err)
	}
	if got != "/api/alpha/decisions" {
		t.Fatalf("path = %q", got)
	}
}

func TestRoute(t *testing.T) {
	for _, tc := range []struct{ typesafe, openrouter, url, model, key string }{
		{"t", "", "https://api.typesafe.ai/v1/systemone", "jev-latest", "t"},
		{"", "o", "https://openrouter.ai/api/alpha/decisions", "~typesafe/jev-latest", "o"},
		// TypeSafe wins so a stray OpenRouter key cannot reroute an existing setup.
		{"t", "o", "https://api.typesafe.ai/v1/systemone", "jev-latest", "t"},
	} {
		t.Setenv("TYPESAFE_API_KEY", tc.typesafe)
		t.Setenv("OPENROUTER_API_KEY", tc.openrouter)
		c, err := route()
		if err != nil {
			t.Fatalf("%+v: %v", tc, err)
		}
		// APIKey too: each route must send the key that selected it.
		if c.URL != tc.url || c.Model != tc.model || c.APIKey != tc.key {
			t.Errorf("%+v: got %s %s %s", tc, c.URL, c.Model, c.APIKey)
		}
	}

	t.Setenv("TYPESAFE_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "")
	if _, err := route(); err == nil {
		t.Fatal("no keys: want error")
	}
}

func TestSetupEnv(t *testing.T) {
	got := setupEnv([]string{
		"PATH=/bin", "TYPESAFE_API_KEY=k", "OPENROUTER_API_KEY_OTHER=no",
		"TYPESAFE_OTHER=s", "OPENROUTER_BASE_URL=no", "OPENROUTER_API_KEY=o=o",
	})
	want := []string{"TYPESAFE_API_KEY=k", "TYPESAFE_OTHER=s", "OPENROUTER_API_KEY=o=o"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestSetupCommands(t *testing.T) {
	cmds := setupCommands("/bin/jev", []string{"TYPESAFE_API_KEY=k", "OPENROUTER_API_KEY=o"})
	want := [][]string{
		{"mcp", "remove", "jev", "-s", "user"},
		{"mcp", "add", "jev", "-s", "user", "-e", "TYPESAFE_API_KEY=k", "-e", "OPENROUTER_API_KEY=o", "--", "/bin/jev", "mcp"},
		nil,
		{"mcp", "add", "jev", "--env", "TYPESAFE_API_KEY=k", "--env", "OPENROUTER_API_KEY=o", "--", "/bin/jev", "mcp"},
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

// A tar member named "jev" that is a symlink (or any other non-regular entry)
// must not be extracted and installed over the running binary.
func TestExtractBinaryRejectsNonRegularMember(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{
		Name:     "jev",
		Typeflag: tar.TypeSymlink,
		Linkname: "/etc/passwd",
		Mode:     0o777,
	}); err != nil {
		t.Fatal(err)
	}
	for _, closer := range []func() error{tw.Close, gw.Close} {
		if err := closer(); err != nil {
			t.Fatal(err)
		}
	}

	if err := extractBinaryFromTar(buf.Bytes(), "jev", io.Discard); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("expected non-regular member to be rejected, got %v", err)
	}
}

func TestWritePiExtension(t *testing.T) {
	dir := t.TempDir()
	path, err := writePiExtension(dir, `/bin/je"v`)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "extensions", "jev.ts"); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	// The quote in the path must come back escaped, not as a broken literal.
	if !strings.Contains(src, `const JEV = "/bin/je\"v"`) {
		t.Fatalf("binary path not rendered: %s", src)
	}
	if !strings.Contains(src, "A noul near 0.5 means uncertain") {
		t.Fatal("instructions not rendered")
	}
	if strings.Contains(src, "__JEV_") {
		t.Fatal("placeholder left behind")
	}
}

func TestPiDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ env, want string }{
		{"", filepath.Join(home, ".pi", "agent")},
		// pi expands "~" itself, so taking it literally would miss a real install.
		{"~", home},
		{"~/.pi/agent", filepath.Join(home, ".pi", "agent")},
		{"/tmp/pi", "/tmp/pi"},
	} {
		t.Setenv("PI_CODING_AGENT_DIR", tc.env)
		got, err := piDir()
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("piDir() with %q = %q, want %q", tc.env, got, tc.want)
		}
	}
	// An absolute override must not need a home directory: sanitized
	// environments (env -i PI_CODING_AGENT_DIR=...) have none.
	t.Setenv("HOME", "")
	t.Setenv("PI_CODING_AGENT_DIR", "/tmp/pi")
	if got, err := piDir(); err != nil || got != "/tmp/pi" {
		t.Errorf("piDir() without HOME = %q, %v, want %q, nil", got, err, "/tmp/pi")
	}
}
