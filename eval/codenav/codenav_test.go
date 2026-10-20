package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/itsmostafa/system-one-connector/eval/internal/openrouter"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func keys(opts []option) []string {
	var out []string
	for _, o := range opts {
		out = append(out, o.Key)
	}
	return out
}

func TestSymbolsPython(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.py")
	write(t, p, `import x

@overload
def pipeline(task: int) -> int: ...
def pipeline(task):
    def inner():
        pass

class Trainer(
    Base,
):
    """Doc."""

    def create_scheduler(
        self,
    ):
        def nested():
            pass

    async def run(self):
        pass

if TYPE_CHECKING:
    def not_a_method():
        pass
`)
	syms, err := symbols(p)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"pipeline", "Trainer", "Trainer.create_scheduler", "Trainer.run"}
	if got := keys(syms); !slices.Equal(got, want) {
		t.Fatalf("symbols = %v, want %v", got, want)
	}
	// The overload keeps its first slot and the implementation's line.
	if syms[0].Line != 5 || syms[2].Line != 14 || syms[2].Desc != "def create_scheduler(" {
		t.Errorf("lines/desc: %+v", syms)
	}
}

func TestSymbolsMarkdown(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d.md")
	write(t, p, "# Bitsandbytes\n\n## Examples\n\n```py\n# not a heading\n```\n\n## Examples\n### NF4 ##\n")
	syms, err := symbols(p)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Bitsandbytes", "Examples", "Examples (2)", "NF4"}
	if got := keys(syms); !slices.Equal(got, want) {
		t.Errorf("symbols = %v, want %v", got, want)
	}
	if syms, _ := symbols(filepath.Join(t.TempDir(), "x.json")); syms != nil {
		t.Errorf("non-code file got symbols %v", syms)
	}
}

func TestChildren(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "src/a/x.py"), "")
	write(t, filepath.Join(root, "src/b.py"), "")
	write(t, filepath.Join(root, ".github/ci.yml"), "")
	write(t, filepath.Join(root, ".env"), "")
	write(t, filepath.Join(root, "README.md"), "")
	counts, err := fileCounts(root)
	if err != nil {
		t.Fatal(err)
	}
	if counts["."] != 3 || counts["src"] != 2 || counts["src/a"] != 1 {
		t.Errorf("counts = %v", counts)
	}
	opts, err := children(root, ".", counts)
	if err != nil {
		t.Fatal(err)
	}
	if got := keys(opts); !slices.Equal(got, []string{"src/", "README.md"}) {
		t.Errorf("children = %v", got)
	}
	if opts[0].Desc != "src/: folder with 2 files" {
		t.Errorf("desc = %q", opts[0].Desc)
	}
}

func TestRight(t *testing.T) {
	q := question{Answers: []accept{
		{Path: "docs/source/*/quantization/bitsandbytes.md"},
		{Path: "src/q.py", Symbols: []string{"Config"}},
	}}
	for _, tc := range []struct {
		path, sym   string
		file, place bool
	}{
		{"docs/source/ko/quantization/bitsandbytes.md", "QLoRA", true, true},
		{"src/q.py", "Config", true, true},
		{"src/q.py", "Other", true, false},
		{"src/other.py", "Config", false, false},
	} {
		if f, p := right(q, tc.path, tc.sym); f != tc.file || p != tc.place {
			t.Errorf("right(%s, %s) = %v, %v", tc.path, tc.sym, f, p)
		}
	}
}

// stubCaller answers each chunk with the highest-numbered option on offer,
// or noMatch for chunks with no option in skip's complement.
func stubCaller(rounds *[]int, skip func(string) bool) caller {
	return func(args map[string]any) (*reply, error) {
		qs := args["questions"].(map[string]any)
		*rounds = append(*rounds, len(qs))
		r := &reply{Model: "m", Answers: map[string]answer{}}
		for id, q := range qs {
			var crit map[string]string
			json.Unmarshal(q.(map[string]any)["criteria"].(json.RawMessage), &crit)
			best := ""
			probs := map[string]float64{}
			for k := range crit {
				if k != noMatch && !skip(k) {
					best = max(best, k)
				}
			}
			for k := range crit {
				probs[k] = 0.01
			}
			if best == "" {
				// Nothing fits: the model says so, and o00-style options tie.
				probs[noMatch] = 0.9
				r.Answers[id] = answer{Choice: noMatch, Probabilities: probs}
				continue
			}
			probs[best] = 0.9
			r.Answers[id] = answer{Choice: best, Probabilities: probs}
		}
		r.Usage.InputTokens = 10
		return r, nil
	}
}

// TestTournament checks that a long list is split into even chunks judged in
// one call, and that the winners go to a final round.
func TestTournament(t *testing.T) {
	var opts []option
	for i := range 40 {
		opts = append(opts, option{Key: fmt.Sprintf("o%02d", i)})
	}
	var rounds []int
	s := &searcher{call: stubCaller(&rounds, func(string) bool { return false })}
	pick, conf, err := s.tournament(nil, "", opts)
	if err != nil || pick.Key != "o39" || conf != 0.9 {
		t.Fatalf("tournament = %v, %v, %v", pick, conf, err)
	}
	// 40 options: 3 chunks of 13–14 in one call, then 3 winners in a second.
	if !slices.Equal(rounds, []int{3, 1}) || s.calls != 2 || s.in != 20 || s.model != "m" {
		t.Errorf("rounds = %v, calls = %d, in = %d, model = %q", rounds, s.calls, s.in, s.model)
	}

	// Only o05 fits: the two chunks that answer noMatch sit out, so o05 wins
	// without a final round.
	rounds = nil
	s = &searcher{call: stubCaller(&rounds, func(k string) bool { return k != "o05" })}
	if pick, _, err := s.tournament(nil, "", opts); err != nil || pick.Key != "o05" || !slices.Equal(rounds, []int{3}) {
		t.Errorf("tournament = %v, %v, rounds %v; want o05 in one round", pick, err, rounds)
	}

	// Nothing fits anywhere: every chunk's best real option goes on, the first
	// on a tie, and the search still picks one rather than abstaining.
	rounds = nil
	s = &searcher{call: stubCaller(&rounds, func(string) bool { return true })}
	if pick, _, err := s.tournament(nil, "", opts); err != nil || pick.Key != "o00" || !slices.Equal(rounds, []int{3, 1}) {
		t.Errorf("tournament = %v, %v, rounds %v; want o00 over two rounds", pick, err, rounds)
	}
}

func TestCriteriaKeepsOrder(t *testing.T) {
	got := string(criteria([]option{{Key: "z/", Desc: "z"}, {Key: "a.py", Desc: "a"}}))
	if got != `{"z/":"z","a.py":"a","none_of_these":"None of these entries leads to the answer."}` {
		t.Errorf("criteria = %s", got)
	}
}

func TestSummarize(t *testing.T) {
	rate := &openrouter.Price{Prompt: 1e-6}
	ps := []pass{
		{Meta: runMeta{Model: "m", Price: rate}, Results: []result{{ID: "a", Right: true, RightFile: true, MS: 1000, InputTokens: 1000}, {ID: "b", Path: "x.py", Symbol: "f", RightFile: true, MS: 3000, InputTokens: 1000}}},
		{Meta: runMeta{Model: "m"}, Results: []result{{ID: "a", Right: true, RightFile: true, MS: 1000}, {ID: "b", Right: true, RightFile: true, MS: 3000}}},
	}
	s := summarize("m", ps)
	// The unpriced pass is left out of cost: 2,000 tokens at $1/M over 2 questions.
	if s.Right != 1.5 || s.RightMin != 1 || s.RightMx != 2 || s.SecPerQ != 2 || !s.Costed || s.Cost1000 != 1 {
		t.Errorf("summary = %+v", s)
	}
	if got := cell(s.ByQuestion["b"]); got != "1/2, ✗ `x.py:f` (right file)" {
		t.Errorf("cell = %q", got)
	}
}

// TestAnswerKeys checks every suite's accepted files and symbols exist in its
// pinned checkout, so a key cannot silently name a place no search could
// reach. Suites without a checkout are skipped.
func TestAnswerKeys(t *testing.T) {
	names, err := fs.Glob(suitesFS, "suites/*.json")
	if err != nil || len(names) == 0 {
		t.Fatalf("no suites: %v", err)
	}
	for _, n := range names {
		name := strings.TrimSuffix(filepath.Base(n), ".json")
		t.Run(name, func(t *testing.T) {
			su, err := loadSuite(name)
			if err != nil {
				t.Fatal(err)
			}
			ids := map[string]bool{}
			for _, q := range su.Questions {
				if ids[q.ID] {
					t.Errorf("duplicate question id %s", q.ID)
				}
				ids[q.ID] = true
			}
			repo := filepath.Join("repos", name)
			if !checkedOut(repo, su.Commit) {
				t.Skipf("no %s checkout at %s; run task eval:codenav:fetch SUITE=%s", name, su.Commit[:7], name)
			}
			for _, q := range su.Questions {
				for _, a := range q.Answers {
					files, _ := filepath.Glob(filepath.Join(repo, a.Path))
					if len(files) == 0 {
						t.Errorf("%s: no file matches %s", q.ID, a.Path)
					}
					for _, f := range files {
						syms, err := symbols(f)
						if err != nil {
							t.Fatal(err)
						}
						for _, want := range a.Symbols {
							if !slices.Contains(keys(syms), want) {
								t.Errorf("%s: %s has no symbol %s", q.ID, a.Path, want)
							}
						}
					}
				}
			}
		})
	}
}
