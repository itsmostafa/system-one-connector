package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
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
		nil,
		{"mcp", "add", "evaluate", "--env", "TYPESAFE_API_KEY=k", "OPENROUTER_API_KEY=o", "--command", "/bin/evaluate", "--args", "mcp"},
	}
	got := [][]string{cmds[0].reset, cmds[0].add, cmds[1].reset, cmds[1].add, cmds[2].reset, cmds[2].add}
	if !slices.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestSetupHermesAnswers(t *testing.T) {
	var hermes setupCommand
	for _, c := range setupCommands("/bin/evaluate", nil) {
		if c.cli == "hermes" {
			hermes = c
		}
	}
	if hermes.answers != "Y\nY\n" || hermes.confirm == "" {
		t.Fatalf("hermes answers = %q, confirm = %q", hermes.answers, hermes.confirm)
	}

	// `cat` echoes what run pipes to stdin, standing in for the CLI's output.
	echo := setupCommand{cli: "cat", answers: "saved (1/1 tools enabled)\n", confirm: "tools enabled)"}
	if out, err := echo.install(context.Background()); err != nil || string(out) != echo.answers {
		t.Fatalf("install = %q, %v; want stdin echoed, nil", out, err)
	}
	echo.answers = "Saved 'evaluate' to config (disabled)\n"
	if _, err := echo.install(context.Background()); err == nil {
		t.Fatal("install accepted output without the confirmation")
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

// pi.ts hand-copies the tool and field descriptions, so a change to one side
// must show up on the other. Joining `" + "` undoes the TS line wrapping.
func TestPiDescriptionsMatch(t *testing.T) {
	src := regexp.MustCompile(`"\s*\+\s*"`).ReplaceAllString(piExtension, "")
	want := []string{toolDescription}
	for _, typ := range []reflect.Type{reflect.TypeFor[evaluateIn](), reflect.TypeFor[question]()} {
		for f := range typ.Fields() {
			want = append(want, f.Tag.Get("jsonschema"))
		}
	}
	for _, w := range want {
		if !strings.Contains(src, w) {
			t.Errorf("pi.ts is missing description %q", w)
		}
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
		{"noul true/false", question{Type: "noul", Criteria: map[string]any{"true": "y", "false": "n"}}, ""},
		{"noul omitted", question{Type: "noul"}, ""},
		// The API silently drops these keys, so the criteria would do nothing.
		{"noul yes/no", question{Type: "noul", Criteria: map[string]any{"yes": "y"}}, `noul criteria keys must be "true" or "false", got "yes"`},
		// The API answers these with a bare "Invalid request.".
		{"unknown type", question{Type: "yesno"}, `questions["q"].type: must be noul, choice, or score, got "yesno"`},
		{"bool type", question{Type: "bool"}, `got "bool"; use "noul" for yes/no questions`},
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

	call := connectEvaluate(t, srv)

	args := `{"state":{"a":9007199254740993,"b":9007199254740992,"c":1.50,"d":-0.1},` +
		`"questions":{"q":{"type":"noul","instructions":"i"}}}`
	if text, isErr := call(args); isErr {
		t.Fatalf("tool error: %s", text)
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
// types validate type-switches on, and that validate runs *before* the HTTP call.
func TestValidateBlocksRequest(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Write([]byte(`{"answers":{}}`))
	}))
	defer srv.Close()

	call := connectEvaluate(t, srv)

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
		{"unknown type", `{"0":"low"}`, "bounding_box", `questions["q"].type`, 0},
	} {
		calls = 0
		args := `{"state":"s","questions":{"q":{"type":"` + tc.qtype +
			`","instructions":"i","criteria":` + tc.criteria + `}}}`
		got, isErr := call(args)
		if (tc.wantErr != "") != isErr {
			t.Errorf("%s: IsError=%v, got %q", tc.name, isErr, got)
		}
		if tc.wantErr != "" && !strings.Contains(got, tc.wantErr) {
			t.Errorf("%s: got %q, want it to contain %q", tc.name, got, tc.wantErr)
		}
		if calls != tc.wantCalls {
			t.Errorf("%s: %d HTTP calls, want %d", tc.name, calls, tc.wantCalls)
		}
	}
}

// TestItems drives items mode end to end: one request per item, each carrying
// only its own item (plus shared state as context), results keyed by item id,
// and a failed item reported beside its siblings rather than failing the call.
func TestItems(t *testing.T) {
	var (
		mu     sync.Mutex
		states = map[string]map[string]any{}
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			State     map[string]any  `json:"state"`
			Questions json.RawMessage `json:"questions"`
			Items     json.RawMessage `json:"items"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Items != nil || body.Questions == nil {
			t.Errorf("upstream body: items=%s questions=%s", body.Items, body.Questions)
		}
		item, _ := body.State["item"].(map[string]any)
		subject, _ := item["subject"].(string)
		mu.Lock()
		states[subject] = body.State
		mu.Unlock()
		if strings.HasPrefix(subject, "bad") {
			http.Error(w, `{"error":"boom"}`, http.StatusBadRequest)
			return
		}
		if subject == "garbage" {
			w.Write([]byte("not json"))
			return
		}
		w.Write([]byte(`{"answers":{"q":{"type":"noul","noul":0.9}},"subject":"` + subject + `"}`))
	}))
	defer srv.Close()

	call := connectEvaluate(t, srv)

	q := `"questions":{"q":{"type":"noul","instructions":"Is ` + "`item.subject`" + ` urgent?"}}`

	t.Run("fan out with context", func(t *testing.T) {
		text, isErr := call(`{"state":{"goal":"house hunting"},` + q +
			`,"items":{"e1":{"subject":"a"},"e2":{"subject":"b"},"e3":{"subject":"bad"}}}`)
		if isErr {
			t.Fatalf("tool error: %s", text)
		}
		var out struct {
			Results map[string]map[string]any
			Errors  map[string]string
		}
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			t.Fatal(err)
		}
		if len(out.Results) != 2 || out.Results["e1"]["subject"] != "a" || out.Results["e2"]["subject"] != "b" {
			t.Errorf("results not keyed by item: %s", text)
		}
		if !strings.Contains(out.Errors["e3"], "boom") {
			t.Errorf("errors[e3] = %q, want the API error", out.Errors["e3"])
		}
		if len(states) != 3 {
			t.Fatalf("%d upstream requests, want 3", len(states))
		}
		for subject, st := range states {
			if ctx, _ := st["context"].(map[string]any); ctx["goal"] != "house hunting" {
				t.Errorf("item %s: context = %v", subject, st["context"])
			}
			if len(st) != 2 {
				t.Errorf("item %s: state has %d keys, want item and context only: %v", subject, len(st), st)
			}
		}
	})

	// The documented result shape carries errors even when nothing failed, so
	// callers can read it without a presence check.
	t.Run("errors present on success", func(t *testing.T) {
		text, isErr := call(`{` + q + `,"items":{"e1":{"subject":"a"},"e2":{"subject":"b"}}}`)
		if isErr {
			t.Fatalf("tool error: %s", text)
		}
		if !strings.Contains(text, `"errors":{}`) {
			t.Errorf("want \"errors\":{} in %s", text)
		}
	})

	t.Run("no shared state", func(t *testing.T) {
		clear(states)
		text, isErr := call(`{` + q + `,"items":{"e1":{"subject":"a"}}}`)
		if isErr {
			t.Fatalf("tool error: %s", text)
		}
		if _, ok := states["a"]["context"]; ok || len(states["a"]) != 1 {
			t.Errorf("state = %v, want only item", states["a"])
		}
	})

	// A 2xx body that is not JSON must fail only its own item: stored as raw
	// JSON, it would otherwise break the final Marshal and drop every sibling.
	t.Run("non-JSON success body", func(t *testing.T) {
		text, isErr := call(`{` + q + `,"items":{"e1":{"subject":"a"},"e2":{"subject":"garbage"}}}`)
		if isErr {
			t.Fatalf("tool error: %s", text)
		}
		var out struct {
			Results map[string]json.RawMessage
			Errors  map[string]string
		}
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			t.Fatal(err)
		}
		if _, ok := out.Results["e1"]; !ok || len(out.Results) != 1 {
			t.Errorf("results = %s, want only e1", text)
		}
		if !strings.Contains(out.Errors["e2"], "not valid JSON") {
			t.Errorf("errors[e2] = %q", out.Errors["e2"])
		}
	})

	t.Run("all fail", func(t *testing.T) {
		text, isErr := call(`{` + q + `,"items":{"e1":{"subject":"bad1"},"e2":{"subject":"bad2"}}}`)
		if !isErr || !strings.Contains(text, "all 2 items failed") {
			t.Errorf("IsError=%v, text=%q", isErr, text)
		}
	})

	ids := make([]string, maxItems+1)
	for i := range ids {
		ids[i] = fmt.Sprintf(`"e%d":{}`, i)
	}
	tooMany := strings.Join(ids, ",")
	for _, tc := range []struct{ name, args, want string }{
		{"neither state nor items", `{` + q + `}`, "state or items is required"},
		{"empty items", `{` + q + `,"items":{}}`, "items must not be empty"},
		{"too many items", `{` + q + `,"items":{` + tooMany + `}}`, fmt.Sprintf("exceeds the limit of %d", maxItems)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, isErr := call(tc.args)
			if !isErr || !strings.Contains(text, tc.want) {
				t.Errorf("IsError=%v, text=%q, want %q", isErr, text, tc.want)
			}
		})
	}
}

// The per-request cap bounds one reply, not a batch: without an aggregate cap
// two replies just under it would both be kept, and 100 would hold gigabytes.
func TestItemsAggregateCap(t *testing.T) {
	big := `{"answers":{"q":"` + strings.Repeat("x", maxBody*2/3) + `"}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(big))
	}))
	defer srv.Close()
	text, isErr := connectEvaluate(t, srv)(`{"questions":{"q":{"type":"noul","instructions":"i"}},` +
		`"items":{"a":{"x":1},"b":{"x":2}}}`)
	if isErr {
		t.Fatalf("tool error: %.200s", text)
	}
	var out struct {
		Results map[string]json.RawMessage
		Errors  map[string]string
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 1 || len(out.Errors) != 1 {
		t.Fatalf("got %d results and %d errors, want one of each", len(out.Results), len(out.Errors))
	}
	for _, msg := range out.Errors {
		if !strings.Contains(msg, "batch responses exceed") {
			t.Errorf("error = %q", msg)
		}
	}
}

// The API lists choice probabilities in no fixed order, so the same question
// over many items came back with its options shuffled. Every result must list
// them in the order the caller wrote the criteria, and the criteria must reach
// the API in that order too, not sorted alphabetically.
func TestProbabilityOrder(t *testing.T) {
	var (
		mu     sync.Mutex
		bodies []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		shuffled := func(pairs ...string) string {
			rand.Shuffle(len(pairs), func(i, j int) { pairs[i], pairs[j] = pairs[j], pairs[i] })
			return "{" + strings.Join(pairs, ",") + "}"
		}
		fmt.Fprintf(w, `{"model":"m","answers":{`+
			`"c":{"type":"choice","choice":"zeta","confidence":0.4,"probabilities":%s},`+
			`"s":{"type":"score","score":1.1,"confidence":0.6,"legend":%s,"probabilities":%s}},`+
			`"usage":{"input_tokens":1,"output_tokens":1}}`,
			shuffled(`"zeta":0.6`, `"alpha":0.3`, `"mid":0.1`),
			shuffled(`"2":"high"`, `"0":"a < b"`, `"1":"mid"`, `"10":"top"`),
			shuffled(`"2":0.2`, `"0":0.1`, `"1":0.6`, `"10":0.1`))
	}))
	defer srv.Close()

	items := make([]string, 25)
	for i := range items {
		items[i] = fmt.Sprintf(`"i%d":{"n":%d}`, i, i)
	}
	text, isErr := connectEvaluate(t, srv)(`{"questions":{` +
		`"c":{"type":"choice","instructions":"i","criteria":{"zeta":null,"alpha":"a","mid":null}},` +
		`"s":{"type":"score","instructions":"i","criteria":["low","mid","high"]}},` +
		`"items":{` + strings.Join(items, ",") + `}}`)
	if isErr {
		t.Fatalf("tool error: %s", text)
	}
	var out struct{ Results map[string]json.RawMessage }
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 25 {
		t.Fatalf("%d results, want 25", len(out.Results))
	}
	for id, r := range out.Results {
		var res struct {
			Answers map[string]map[string]json.RawMessage
		}
		if err := json.Unmarshal(r, &res); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			q, field string
			want     []string
		}{
			{"c", "probabilities", []string{"zeta", "alpha", "mid"}},
			{"s", "probabilities", []string{"0", "1", "2", "10"}},
			{"s", "legend", []string{"0", "1", "2", "10"}},
		} {
			if got := keyOrder(res.Answers[tc.q][tc.field]); !slices.Equal(got, tc.want) {
				t.Errorf("%s: %s.%s keys = %v, want %v", id, tc.q, tc.field, got, tc.want)
			}
		}
		if !strings.Contains(string(r), `"a < b"`) {
			t.Errorf("%s: legend text re-escaped: %s", id, r)
		}
	}
	for _, b := range bodies {
		if !strings.Contains(b, `"criteria":{"zeta":null,"alpha":"a","mid":null}`) {
			t.Fatalf("criteria not forwarded in caller order: %s", b)
		}
	}
}

// Model and usage are the same on every item, so items mode reports them once
// in meta and strips them per item unless the caller opts back in.
func TestItemsMeta(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"model":"jev-x","answers":{"q":{"type":"noul","noul":0.9}},"usage":{"input_tokens":10,"output_tokens":2}}`))
	}))
	defer srv.Close()
	call := connectEvaluate(t, srv)
	args := `{"questions":{"q":{"type":"noul","instructions":"i"}},"items":{"a":{},"b":{},"c":{}}`
	for _, keep := range []bool{false, true} {
		extra := ""
		if keep {
			extra = `,"include_item_usage":true`
		}
		text, isErr := call(args + extra + `}`)
		if isErr {
			t.Fatalf("tool error: %s", text)
		}
		var out struct {
			Results map[string]map[string]json.RawMessage
			Meta    map[string]any
		}
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			t.Fatal(err)
		}
		m := out.Meta
		if m["model"] != "jev-x" || m["input_tokens"] != 30.0 || m["output_tokens"] != 6.0 || m["item_count"] != 3.0 {
			t.Errorf("keep=%v: meta = %v", keep, m)
		}
		if _, ok := m["latency_ms"].(float64); !ok {
			t.Errorf("keep=%v: latency_ms missing: %v", keep, m)
		}
		for id, r := range out.Results {
			_, hasUsage := r["usage"]
			_, hasModel := r["model"]
			if hasUsage != keep || hasModel != keep || r["answers"] == nil {
				t.Errorf("keep=%v: results[%s] = %v", keep, id, r)
			}
		}
	}
}

// min_confidence is applied here, never sent upstream: below it a choice
// becomes the abstain sentinel and a noul is flagged, with probabilities kept.
func TestMinConfidence(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.Write([]byte(`{"answers":{` +
			`"lo":{"type":"choice","choice":"a","confidence":0.28,"probabilities":{"a":0.46,"b":0.54}},` +
			`"hi":{"type":"choice","choice":"a","confidence":0.94,"probabilities":{"a":0.97,"b":0.03}},` +
			`"n":{"type":"noul","noul":0.6},` +
			`"plain":{"type":"choice","choice":"a","confidence":0.1,"probabilities":{"a":0.55,"b":0.45}}}}`))
	}))
	defer srv.Close()
	call := connectEvaluate(t, srv)
	choice := func(min string) string {
		return `{"type":"choice","instructions":"i","criteria":{"a":null,"b":null}` + min + `}`
	}
	text, isErr := call(`{"state":"s","questions":{` +
		`"lo":` + choice(`,"min_confidence":0.5`) + `,"hi":` + choice(`,"min_confidence":0.5`) +
		`,"n":{"type":"noul","instructions":"i","min_confidence":0.3},"plain":` + choice("") + `}}`)
	if isErr {
		t.Fatalf("tool error: %s", text)
	}
	if strings.Contains(body, "min_confidence") {
		t.Errorf("min_confidence forwarded upstream: %s", body)
	}
	var out struct{ Answers map[string]map[string]any }
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	a := out.Answers
	if a["lo"]["choice"] != "__uncertain__" || a["lo"]["uncertain"] != true || a["lo"]["probabilities"] == nil {
		t.Errorf("lo = %v, want abstention with probabilities", a["lo"])
	}
	// |2*0.6-1| = 0.2 < 0.3
	if a["n"]["uncertain"] != true || a["n"]["noul"] != 0.6 {
		t.Errorf("n = %v, want flagged with noul kept", a["n"])
	}
	for _, id := range []string{"hi", "plain"} {
		if a[id]["choice"] != "a" || a[id]["uncertain"] != nil {
			t.Errorf("%s = %v, want untouched", id, a[id])
		}
	}

	for _, tc := range []struct{ q, want string }{
		{`{"type":"score","instructions":"i","criteria":["x","y"],"min_confidence":0.5}`, "only noul and choice"},
		{choice(`,"min_confidence":1.5`), "between 0 and 1"},
		{`{"type":"choice","instructions":"i","criteria":{"__uncertain__":null},"min_confidence":0.5}`, "reserved"},
	} {
		if text, isErr := call(`{"state":"s","questions":{"q":` + tc.q + `}}`); !isErr || !strings.Contains(text, tc.want) {
			t.Errorf("IsError=%v, text=%q, want %q", isErr, text, tc.want)
		}
	}
}

// A full batch fans out and comes back as one response.
func TestItemsAtLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"answers":{"q":{"type":"noul","noul":0.9}}}`))
	}))
	defer srv.Close()
	ids := make([]string, maxItems)
	for i := range ids {
		ids[i] = fmt.Sprintf(`"e%d":{}`, i)
	}
	text, isErr := connectEvaluate(t, srv)(`{"questions":{"q":{"type":"noul","instructions":"i"}},"items":{` + strings.Join(ids, ",") + `}}`)
	var out struct{ Results map[string]json.RawMessage }
	if isErr || json.Unmarshal([]byte(text), &out) != nil || len(out.Results) != maxItems {
		t.Fatalf("IsError=%v, %d results, want %d: %.200s", isErr, len(out.Results), maxItems, text)
	}
}

// connectEvaluate registers the evaluate tool against srv and connects an MCP
// client to it in memory, returning a call that yields the tool's text and
// whether it was an error result.
func connectEvaluate(t *testing.T, srv *httptest.Server) func(args string) (string, bool) {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "evaluate", Version: "test"}, nil)
	registerTools(s, &Client{URL: srv.URL, APIKey: "k", HTTP: srv.Client(), Model: "m"})
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return func(args string) (string, bool) {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "evaluate", Arguments: json.RawMessage(args)})
		if err != nil {
			t.Fatal(err)
		}
		return res.Content[0].(*mcp.TextContent).Text, res.IsError
	}
}

// A 2xx that is not JSON (a proxy's HTML page, say) must not come back as a
// successful answer on the single-request path either.
func TestNonJSONSuccessIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("<html>gateway</html>"))
	}))
	defer srv.Close()
	text, isErr := connectEvaluate(t, srv)(`{"state":"s","questions":{"q":{"type":"noul","instructions":"i"}}}`)
	if !isErr || !strings.Contains(text, "not valid JSON") {
		t.Errorf("IsError=%v, text=%q", isErr, text)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUpdateNotice(t *testing.T) {
	origClient, origVersion := updateClient, version
	t.Cleanup(func() { updateClient, version = origClient, origVersion })
	updateClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"tag_name":"v9.9.9"}`))}, nil
	})}

	for _, tc := range []struct{ version, wantSub string }{
		{"dev", ""},
		{"v9.9.9", ""},
		{"v10.0.0", ""},
		{"v9.9.10-0.20260101000000-abcdef123456", ""},
		{"v9.9.9+dirty", ""},
		{"v9.9.9-0.20260101000000-abcdef123456", "evaluate v9.9.9 is available (running v9.9.9-0.20260101000000-abcdef123456)"},
		{"v9.9.8", "evaluate v9.9.9 is available (running v9.9.8)"},
		{"v0.1.0", "evaluate v9.9.9 is available (running v0.1.0)"},
	} {
		version = tc.version
		got := updateNotice(context.Background())
		if (tc.wantSub == "") != (got == "") || !strings.Contains(got, tc.wantSub) {
			t.Errorf("version %s: updateNotice() = %q, want containing %q", tc.version, got, tc.wantSub)
		}
	}
}
