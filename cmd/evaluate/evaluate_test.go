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

	"github.com/modelcontextprotocol/go-sdk/mcp"
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

// A reply that fills the 16 MiB cap exactly is still a whole answer; one byte
// more is a body that was cut mid-JSON, and returning that as a success handed
// the caller truncated JSON with no error.
func TestEvaluateBodyLimit(t *testing.T) {
	size := maxBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		pad := size - len(`{"answers":{"q":""}}`)
		w.Write([]byte(`{"answers":{"q":"` + strings.Repeat("x", pad) + `"}}`))
	}))
	defer srv.Close()
	c := &Client{URL: srv.URL, APIKey: "k", HTTP: srv.Client()}

	b, err := c.Evaluate(context.Background(), evaluateIn{State: "s"})
	if err != nil || len(b) != maxBody || !json.Valid(b) {
		t.Fatalf("at limit: len=%d valid=%v err=%v", len(b), json.Valid(b), err)
	}

	size = maxBody + 1
	b, err = c.Evaluate(context.Background(), evaluateIn{State: "s"})
	if err == nil || !strings.Contains(err.Error(), "response exceeds") {
		t.Fatalf("over limit: len=%d err=%v", len(b), err)
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
	for _, tc := range []struct{ typesafe, openrouter, base, url, model, key string }{
		{"t", "", "", "https://api.typesafe.ai/v1/systemone", "jev-latest", "t"},
		{"", "o", "", "https://openrouter.ai/api/alpha/decisions", "~typesafe/jev-latest", "o"},
		// TypeSafe wins so a stray OpenRouter key cannot reroute an existing setup.
		{"t", "o", "", "https://api.typesafe.ai/v1/systemone", "jev-latest", "t"},
		// TYPESAFE_BASE_URL retargets the TypeSafe route, tolerating trailing slashes.
		{"t", "", "https://jev.internal", "https://jev.internal/v1/systemone", "jev-latest", "t"},
		{"t", "", "https://jev.internal/", "https://jev.internal/v1/systemone", "jev-latest", "t"},
		{"t", "", "https://jev.internal//", "https://jev.internal/v1/systemone", "jev-latest", "t"},
		// A local gateway over plain http is the point of the variable.
		{"t", "", "http://localhost:8080", "http://localhost:8080/v1/systemone", "jev-latest", "t"},
		// ...and it leaves the OpenRouter route alone.
		{"", "o", "https://jev.internal", "https://openrouter.ai/api/alpha/decisions", "~typesafe/jev-latest", "o"},
	} {
		t.Setenv("TYPESAFE_API_KEY", tc.typesafe)
		t.Setenv("OPENROUTER_API_KEY", tc.openrouter)
		t.Setenv("TYPESAFE_BASE_URL", tc.base)
		c, err := route()
		if err != nil {
			t.Errorf("%+v: %v", tc, err)
			continue
		}
		// APIKey too: each route must send the key that selected it.
		if c.URL != tc.url || c.Model != tc.model || c.APIKey != tc.key {
			t.Errorf("%+v: got %s %s %s", tc, c.URL, c.Model, c.APIKey)
		}
	}

	// A malformed base fails here, before setup bakes it into every client config.
	t.Setenv("TYPESAFE_API_KEY", "t")
	t.Setenv("OPENROUTER_API_KEY", "")
	for _, base := range []string{"jev.internal", "localhost:8080", "https://jev.internal ", "/v1", "https://"} {
		t.Setenv("TYPESAFE_BASE_URL", base)
		if _, err := route(); err == nil {
			t.Errorf("base %q: want error", base)
		}
	}

	// No keys at all: route fails before it ever looks at the base.
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
	cmds := setupCommands("/bin/evaluate", []string{"TYPESAFE_API_KEY=k", "OPENROUTER_API_KEY=o"})
	want := [][]string{
		{"mcp", "remove", "evaluate", "-s", "user"},
		{"mcp", "add", "evaluate", "-s", "user", "-e", "TYPESAFE_API_KEY=k", "-e", "OPENROUTER_API_KEY=o", "--", "/bin/evaluate", "mcp"},
		nil,
		{"mcp", "add", "evaluate", "--env", "TYPESAFE_API_KEY=k", "--env", "OPENROUTER_API_KEY=o", "--", "/bin/evaluate", "mcp"},
	}
	got := [][]string{cmds[0].reset, cmds[0].add, cmds[1].reset, cmds[1].add}
	if !slices.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestSetupClaudeDesktop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude_desktop_config.json")
	seed := `{"mcpServers":{"lumi":{"command":"/bin/lumi"},"evaluate":{"command":"/old"}},"preferences":{"sidebarMode":"chat"}}`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := setupClaudeDesktop(path, "/bin/evaluate", []string{"TYPESAFE_API_KEY=k"}); err != nil {
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
	s := got.MCPServers["evaluate"]
	if s.Command != "/bin/evaluate" || !slices.Equal(s.Args, []string{"mcp"}) || s.Env["TYPESAFE_API_KEY"] != "k" {
		t.Fatalf("evaluate entry = %+v", s)
	}
	if got.MCPServers["lumi"].Command != "/bin/lumi" || got.Preferences["sidebarMode"] != "chat" {
		t.Fatalf("other keys lost: %s", b)
	}

	for _, seed := range []string{`null`, `{"mcpServers":null}`} {
		if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := setupClaudeDesktop(path, "/bin/evaluate", nil); err != nil {
			t.Fatalf("seed %s: %v", seed, err)
		}
	}
}

// A tar member named "evaluate" that is a symlink (or any other non-regular entry)
// must not be extracted and installed over the running binary.
func TestExtractBinaryRejectsNonRegularMember(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{
		Name:     "evaluate",
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

	if err := extractBinaryFromTar(buf.Bytes(), "evaluate", io.Discard); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("expected non-regular member to be rejected, got %v", err)
	}
}

func TestWritePiExtension(t *testing.T) {
	dir := t.TempDir()
	path, err := writePiExtension(dir, `/bin/je"v`)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "extensions", "evaluate.ts"); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	// The quote in the path must come back escaped, not as a broken literal.
	if !strings.Contains(src, `const BINARY = "/bin/je\"v"`) {
		t.Fatalf("binary path not rendered: %s", src)
	}
	if !strings.Contains(src, "A noul near 0.5 means uncertain") {
		t.Fatal("instructions not rendered")
	}
	if strings.Contains(src, "__EVALUATE_") {
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

// Malformed criteria must be caught here, with the path the caller wrote. The
// API reports this as "questions.<id>.score.criteria" — a union branch, not a
// property the request ever had.
func TestValidate(t *testing.T) {
	obj := map[string]any{"0": "low", "1": "high"}
	arr := []any{"low", "high"}
	for _, tc := range []struct {
		name string
		q    question
		want string
	}{
		{"score object", question{Type: "score", Criteria: obj}, `questions["q"].criteria: score criteria must be an array of level descriptions, ordered low to high, got an object`},
		{"score missing", question{Type: "score"}, "got nothing"},
		{"score string", question{Type: "score", Criteria: "high"}, "got a string"},
		{"choice array", question{Type: "choice", Criteria: arr}, `questions["q"].criteria: choice criteria must be an object`},
		{"noul array", question{Type: "noul", Criteria: arr}, `questions["q"].criteria: noul criteria must be an object`},

		{"score array", question{Type: "score", Criteria: arr}, ""},
		// One level is accepted by the API, so it must not be rejected here.
		{"score one level", question{Type: "score", Criteria: []any{"only"}}, ""},
		{"choice object", question{Type: "choice", Criteria: obj}, ""},
		{"noul object", question{Type: "noul", Criteria: obj}, ""},
		{"noul omitted", question{Type: "noul"}, ""},
		// The server enumerates types this tool does not document; rejecting an
		// unknown type here would break every one of them.
		{"unknown type", question{Type: "bounding_box", Criteria: obj}, ""},
	} {
		tc.q.Instructions = "x"
		err := validate(evaluateIn{State: "s", Questions: map[string]question{"q": tc.q}})
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: want nil, got %v", tc.name, err)
		case tc.want != "" && err == nil:
			t.Errorf("%s: want %q, got nil", tc.name, tc.want)
		case tc.want != "" && err != nil && !strings.Contains(err.Error(), tc.want):
			t.Errorf("%s: got %q, want it to contain %q", tc.name, err, tc.want)
		}
	}
}

// State is evidence, so it must reach the model as the caller wrote it. Decoded
// into `any` the plain way, a JSON number becomes a float64, and anything past
// 2^53 rounds — collapsing two distinct ids into one before the model ever sees
// them. This drives the tool end to end and reads the body that goes out.
func TestBigNumbersSurviveForwarding(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		w.Write([]byte(`{"answers":{}}`))
	}))
	defer srv.Close()

	s := mcp.NewServer(&mcp.Implementation{Name: "evaluate", Version: "test"}, nil)
	registerTools(s, &Client{URL: srv.URL, APIKey: "k", HTTP: srv.Client(), Model: "m"})
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	args := `{"state":{"a":9007199254740993,"b":9007199254740992,"c":1.50,"d":-0.1},` +
		`"questions":{"q":{"type":"noul","instructions":"i"}}}`
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "evaluate", Arguments: json.RawMessage(args)})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	for _, want := range []string{
		`"a":9007199254740993`, // not rounded down to ...992
		`"b":9007199254740992`, // and still distinct from a
		`"c":1.50`,             // fractions keep the caller's digits too
		`"d":-0.1`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("outgoing body missing %s: %s", want, got)
		}
	}
}

// TestValidateBlocksRequest drives the registered tool with real JSON arguments,
// so it covers what TestValidate cannot: that the SDK decodes criteria into the
// types validate type-switches on, that validate runs *before* the HTTP call,
// and that a type this tool does not know still reaches the API.
func TestValidateBlocksRequest(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Write([]byte(`{"answers":{}}`))
	}))
	defer srv.Close()

	s := mcp.NewServer(&mcp.Implementation{Name: "evaluate", Version: "test"}, nil)
	registerTools(s, &Client{URL: srv.URL, APIKey: "k", HTTP: srv.Client(), Model: "m"})
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	for _, tc := range []struct {
		name, criteria, qtype, wantErr string
		wantCalls                      int
	}{
		// The reported bug: an index-keyed object, the shape the response legend
		// comes back as. It must never reach the API.
		{"score object", `{"0":"low","1":"high"}`, "score", `questions["q"].criteria`, 0},
		{"score array", `["low","high"]`, "score", "", 1},
		// The API accepts one level, so this must not be rejected locally.
		{"score one level", `["only"]`, "score", "", 1},
		// The API enumerates types this tool does not document; they pass through.
		{"unknown type", `{"0":"low"}`, "bounding_box", "", 1},
	} {
		calls = 0
		args := `{"state":"s","questions":{"q":{"type":"` + tc.qtype +
			`","instructions":"i","criteria":` + tc.criteria + `}}}`
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "evaluate", Arguments: json.RawMessage(args)})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		got := res.Content[0].(*mcp.TextContent).Text
		if (tc.wantErr != "") != res.IsError {
			t.Errorf("%s: IsError=%v, got %q", tc.name, res.IsError, got)
		}
		if tc.wantErr != "" && !strings.Contains(got, tc.wantErr) {
			t.Errorf("%s: got %q, want it to contain %q", tc.name, got, tc.wantErr)
		}
		if calls != tc.wantCalls {
			t.Errorf("%s: %d HTTP calls, want %d", tc.name, calls, tc.wantCalls)
		}
	}
}
