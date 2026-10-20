// Command codenav runs the code-search eval after Liquid's d1 playground
// (https://d1.liquid.ai/#/code): a decision model finds where a plain-English
// question is answered in a large repository by walking the tree from the
// root, one choice per level, with nothing indexed beforehand. Each suite in
// suites/ pins one repository at one commit with its questions.
//
//	go run ./codenav fetch -suite vllm
//	go run ./codenav run -suite vllm -profile d1
//	go run ./codenav score -suite vllm
package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/itsmostafa/system-one-connector/eval/internal/openrouter"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

//go:embed suites/*.json
var suitesFS embed.FS

// suite is one repository pinned at the commit its answer keys were checked
// against, with its questions.
type suite struct {
	Name      string     `json:"-"`
	Repo      string     `json:"repo"`
	Commit    string     `json:"commit"`
	Source    string     `json:"source,omitempty"` // where the questions came from, if not written here
	Questions []question `json:"questions"`
}

func loadSuite(name string) (*suite, error) {
	b, err := suitesFS.ReadFile("suites/" + name + ".json")
	if err != nil {
		names, _ := fs.Glob(suitesFS, "suites/*.json")
		return nil, fmt.Errorf("no suite %q (have %s)", name, strings.Join(names, ", "))
	}
	s := &suite{Name: name}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, fmt.Errorf("suite %s: %w", name, err)
	}
	return s, nil
}

// Short is the repository name the model sees as the root folder.
func (s *suite) Short() string { return s.Repo[strings.LastIndexByte(s.Repo, '/')+1:] }

// Checkout is where fetch puts the repository, relative to eval/.
func (s *suite) Checkout() string { return filepath.Join("codenav", "repos", s.Name) }

type question struct {
	ID       string   `json:"id"`
	Question string   `json:"question"`
	Answers  []accept `json:"answers"`
}

// accept is one right place: a file (a glob, for translated docs) and the
// symbols in it that count, or any symbol when Symbols is empty.
type accept struct {
	Path    string   `json:"path"`
	Symbols []string `json:"symbols,omitempty"`
}

// Up to maxSingle options go to the model in one choice, as the demo's 27-entry
// root does; more are split into chunks of chunkSize, judged in parallel, and
// the chunk winners go to a final round.
const (
	maxSingle = 32
	chunkSize = 19
)

// Every choice also offers noMatch: GPT-6 Luna refuses a choice where nothing
// fits, which chunks of a long listing often are. A chunk that picks it sits
// out the final round, but the search never abstains: each round still opens
// its most probable real option, as the demo does.
const (
	noMatch     = "none_of_these"
	noMatchDesc = "None of these entries leads to the answer."
)

const (
	folderInstructions = "Which entry inside the folder at `path` is most likely to lead to the code or documentation that answers `question`? The options are that folder's subfolders and files."
	fileInstructions   = "Which function, class, or section of the file at `path` most likely holds the code or documentation that answers `question`?"
)

// step is one level of a search: where it was, how many entries it weighed,
// and what it opened. Auto marks a level with a single entry, opened without
// asking.
type step struct {
	Path       string  `json:"path"`
	Options    int     `json:"options"`
	Calls      int     `json:"calls,omitempty"`
	Pick       string  `json:"pick"`
	Confidence float64 `json:"confidence,omitempty"`
	Auto       bool    `json:"auto,omitempty"`
}

// result is one question's search, written one per line.
type result struct {
	ID           string `json:"id"`
	Path         string `json:"path,omitempty"`
	Symbol       string `json:"symbol,omitempty"`
	Line         int    `json:"line,omitempty"`
	RightFile    bool   `json:"right_file"`
	Right        bool   `json:"right"`
	MS           int64  `json:"ms"`
	Calls        int    `json:"calls"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	Steps        []step `json:"steps"`
	Error        string `json:"error,omitempty"`
}

type runMeta struct {
	Profile string            `json:"profile"`
	Model   string            `json:"model"`
	Commit  string            `json:"commit"`
	Limit   int               `json:"limit"`
	Price   *openrouter.Price `json:"price"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: codenav fetch|run|score [flags]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "fetch":
		err = fetch(os.Args[2:])
	case "run":
		err = run(os.Args[2:])
	case "score":
		err = score(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "codenav:", err)
		os.Exit(1)
	}
}

// fetch shallow-clones a suite's repository at its pinned commit, unless the
// checkout is already there.
func fetch(args []string) error {
	flags := flag.NewFlagSet("fetch", flag.ExitOnError)
	name := flags.String("suite", "transformers", "suite to fetch")
	flags.Parse(args)
	s, err := loadSuite(*name)
	if err != nil {
		return err
	}
	dir := s.Checkout()
	if checkedOut(dir, s.Commit) {
		return nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"init", "-q", dir},
		{"-C", dir, "fetch", "-q", "--depth", "1", s.Repo, s.Commit},
		{"-C", dir, "checkout", "-q", "FETCH_HEAD"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
	}
	return nil
}

// resultsDir holds one suite's runs, relative to eval/.
func resultsDir(suite string) string { return filepath.Join("results", "codenav", suite) }

func checkedOut(dir, commit string) bool {
	head, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	return err == nil && strings.TrimSpace(string(head)) == commit
}

func run(args []string) error {
	flags := flag.NewFlagSet("run", flag.ExitOnError)
	suiteName := flags.String("suite", "transformers", "suite to run")
	profile := flags.String("profile", "", "evaluate profile to pin with TYPESAFE_PROFILE")
	name := flags.String("name", "", "results name (default: profile)")
	limit := flags.Int("limit", 0, "only the first N questions (smoke runs)")
	bin := flags.String("bin", "../evaluate", "evaluate binary")
	flags.Parse(args)
	if *profile == "" {
		return errors.New("-profile is required")
	}
	if *name == "" {
		*name = *profile
	}
	su, err := loadSuite(*suiteName)
	if err != nil {
		return err
	}
	repo := su.Checkout()
	if !checkedOut(repo, su.Commit) {
		return fmt.Errorf("%s is not %s at %s; run task eval:codenav:fetch SUITE=%s", repo, su.Short(), su.Commit[:7], su.Name)
	}
	qs := su.Questions
	if *limit > 0 && *limit < len(qs) {
		qs = qs[:*limit]
	}
	counts, err := fileCounts(repo)
	if err != nil {
		return err
	}

	ctx := context.Background()
	cmd := exec.Command(*bin, "mcp", "--no-update-check")
	cmd.Env = append(os.Environ(), "TYPESAFE_PROFILE="+*profile)
	cmd.Stderr = os.Stderr
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "codenav", Version: "1"}, nil).Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return err
	}
	defer cs.Close()

	outDir := resultsDir(su.Name)
	os.MkdirAll(outDir, 0o755)
	out, err := os.Create(filepath.Join(outDir, *name+".jsonl"))
	if err != nil {
		return err
	}
	defer out.Close()
	enc := json.NewEncoder(out)
	meta := runMeta{Profile: *profile, Commit: su.Commit, Limit: *limit}
	s := &searcher{root: repo, name: su.Short(), counts: counts, call: mcpCaller(ctx, cs)}
	for _, q := range qs {
		r := s.search(q)
		// Price the run once, from the model the first reply names, and warn
		// right away so a run does not finish unpriced by surprise.
		if meta.Model == "" && s.model != "" {
			meta.Model = s.model
			if meta.Price, err = openrouter.Fetch(s.model); err != nil {
				fmt.Fprintf(os.Stderr, "%s: no price saved, cost will show as —: %v\n", *name, err)
			}
		}
		if err := enc.Encode(r); err != nil {
			return err
		}
		mark := "✗"
		if r.Right {
			mark = "✓"
		}
		if r.Error != "" {
			mark = "error: " + r.Error
		}
		fmt.Fprintf(os.Stderr, "%s: %s %s:%s %.1fs %s\n", *name, q.ID, r.Path, r.Symbol, float64(r.MS)/1000, mark)
	}
	b, _ := json.MarshalIndent(meta, "", "  ")
	return os.WriteFile(filepath.Join(outDir, *name+".meta.json"), append(b, '\n'), 0o644)
}

// reply is one evaluate call's answer to a set of choice questions.
type reply struct {
	Model   string            `json:"model"`
	Answers map[string]answer `json:"answers"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type answer struct {
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities"`
}

// best returns the most probable real option, first in listing order on a
// tie, and whether the model's own pick was noMatch.
func (a answer) best(opts []option) (int, bool) {
	j := -1
	for i, o := range opts {
		if p, ok := a.Probabilities[o.Key]; ok && (j < 0 || p > a.Probabilities[opts[j].Key]) {
			j = i
		}
	}
	if j < 0 {
		j = indexOf(opts, a.Choice)
	}
	return j, a.Choice == noMatch
}

type caller func(args map[string]any) (*reply, error)

func mcpCaller(ctx context.Context, cs *mcp.ClientSession) caller {
	return func(args map[string]any) (*reply, error) {
		var text string
		// Retry upstream hiccups twice; a refusal is an answer, not a hiccup.
		for try := 0; ; try++ {
			res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "evaluate", Arguments: args})
			if err != nil {
				return nil, err
			}
			text = res.Content[0].(*mcp.TextContent).Text
			if !res.IsError {
				break
			}
			if try == 2 || !transient.MatchString(text) || strings.Contains(text, "refused") {
				return nil, errors.New(text)
			}
			time.Sleep(time.Duration(try+1) * time.Second)
		}
		var r reply
		if err := json.Unmarshal([]byte(text), &r); err != nil {
			return nil, fmt.Errorf("decoding reply: %w", err)
		}
		return &r, nil
	}
}

var transient = regexp.MustCompile(`\b50[234]\b`)

// searcher walks one checkout for one model, question by question.
type searcher struct {
	root   string
	name   string // repository name shown as the root folder
	counts map[string]int
	call   caller
	model  string

	// per question
	calls, in, out int
}

func (s *searcher) search(q question) result {
	start := time.Now()
	s.calls, s.in, s.out = 0, 0, 0
	r := result{ID: q.ID}
	err := s.descend(q, &r)
	if err != nil {
		r.Error = err.Error()
	} else {
		r.RightFile, r.Right = right(q, r.Path, r.Symbol)
	}
	r.MS = time.Since(start).Milliseconds()
	r.Calls, r.InputTokens, r.OutputTokens = s.calls, s.in, s.out
	return r
}

func (s *searcher) descend(q question, r *result) error {
	repoName := s.name
	for dir := "."; ; {
		shown := repoName
		if dir != "." {
			shown += "/" + dir
		}
		opts, err := children(s.root, dir, s.counts)
		if err != nil {
			return err
		}
		if len(opts) == 0 {
			return fmt.Errorf("%s is empty", shown)
		}
		pick, st, err := s.choose(q, shown, folderInstructions, opts)
		r.Steps = append(r.Steps, st)
		if err != nil {
			return err
		}
		if pick.Dir {
			dir = pathJoin(dir, pick.Name)
			continue
		}
		r.Path = pathJoin(dir, pick.Name)
		syms, err := symbols(filepath.Join(s.root, r.Path))
		if err != nil {
			return err
		}
		if len(syms) == 0 {
			return nil
		}
		sym, st, err := s.choose(q, repoName+"/"+r.Path, fileInstructions, syms)
		r.Steps = append(r.Steps, st)
		if err != nil {
			return err
		}
		r.Symbol, r.Line = sym.Key, sym.Line
		return nil
	}
}

// choose picks one option, asking the model only when there is a choice.
func (s *searcher) choose(q question, path, instructions string, opts []option) (option, step, error) {
	st := step{Path: path, Options: len(opts)}
	if len(opts) == 1 {
		st.Pick, st.Auto = opts[0].Key, true
		return opts[0], st, nil
	}
	before := s.calls
	pick, conf, err := s.tournament(map[string]any{"question": q.Question, "path": path}, instructions, opts)
	st.Calls = s.calls - before
	st.Pick, st.Confidence = pick.Key, conf
	return pick, st, err
}

// tournament asks one choice over opts, or, past maxSingle, one choice per
// chunk in a single call and then a final round over the chunk winners.
func (s *searcher) tournament(state map[string]any, instructions string, opts []option) (option, float64, error) {
	chunks := [][]option{opts}
	if len(opts) > maxSingle {
		chunks = nil
		// Even chunks of at most chunkSize, so no chunk is a lone straggler.
		n := (len(opts) + chunkSize - 1) / chunkSize
		for i := range n {
			chunks = append(chunks, opts[i*len(opts)/n:(i+1)*len(opts)/n])
		}
	}
	qs := map[string]any{}
	for i, c := range chunks {
		qs[fmt.Sprintf("c%d", i)] = map[string]any{"type": "choice", "instructions": instructions, "criteria": criteria(c)}
	}
	rep, err := s.call(map[string]any{"state": state, "questions": qs})
	if err != nil {
		return option{}, 0, err
	}
	s.calls++
	s.in += rep.Usage.InputTokens
	s.out += rep.Usage.OutputTokens
	if s.model == "" {
		s.model = rep.Model
	}
	var winners, fallback []option
	var conf float64
	for i, c := range chunks {
		a, ok := rep.Answers[fmt.Sprintf("c%d", i)]
		if !ok {
			return option{}, 0, fmt.Errorf("no answer for chunk %d", i)
		}
		j, none := a.best(c)
		if j < 0 {
			return option{}, 0, fmt.Errorf("model chose %q, not an option", a.Choice)
		}
		fallback = append(fallback, c[j])
		if !none {
			winners = append(winners, c[j])
		}
		conf = a.Probabilities[c[j].Key]
	}
	if len(winners) == 0 {
		winners = fallback
	}
	if len(winners) == 1 {
		return winners[0], conf, nil
	}
	return s.tournament(state, instructions, winners)
}

func indexOf(opts []option, key string) int {
	for i, o := range opts {
		if o.Key == key {
			return i
		}
	}
	return -1
}

// criteria encodes options as a choice criteria object in their own order;
// a Go map would sort them.
func criteria(opts []option) json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, o := range opts {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(o.Key)
		v, _ := json.Marshal(o.Desc)
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	k, _ := json.Marshal(noMatch)
	v, _ := json.Marshal(noMatchDesc)
	fmt.Fprintf(&b, ",%s:%s}", k, v)
	return b.Bytes()
}
