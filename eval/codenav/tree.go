package main

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// option is one entry a model can pick: a folder, a file, or a symbol in a file.
type option struct {
	Key  string // what the model sees and picks: "src/", "trainer.py", "Trainer.create_scheduler"
	Desc string
	Name string // folder or file name; empty for symbols
	Dir  bool
	Line int // symbols only
}

// fileCounts maps every folder under root, by slash path relative to root, to
// the number of files beneath it. Dot-entries are skipped, as in the demo.
func fileCounts(root string) (map[string]int, error) {
	counts := map[string]int{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != root && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, filepath.Dir(p))
		for dir := filepath.ToSlash(rel); ; dir = parent(dir) {
			counts[dir]++
			if dir == "." {
				break
			}
		}
		return nil
	})
	return counts, err
}

func parent(dir string) string {
	if i := strings.LastIndexByte(dir, '/'); i >= 0 {
		return dir[:i]
	}
	return "."
}

// children lists a folder's entries as options, folders first, by name.
func children(root, dir string, counts map[string]int) ([]option, error) {
	ents, err := os.ReadDir(filepath.Join(root, dir))
	if err != nil {
		return nil, err
	}
	var dirs, files []option
	for _, e := range ents {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if e.IsDir() {
			n := counts[pathJoin(dir, name)]
			if n == 0 {
				continue
			}
			dirs = append(dirs, option{Key: name + "/", Desc: fmt.Sprintf("%s/: folder with %s", name, plural(n, "file")), Name: name, Dir: true})
			continue
		}
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		files = append(files, option{Key: name, Desc: fmt.Sprintf("%s: file, %d KB", name, (info.Size()+512)/1024), Name: name})
	}
	return append(dirs, files...), nil
}

func pathJoin(dir, name string) string {
	if dir == "." {
		return name
	}
	return dir + "/" + name
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

var (
	pyTop    = regexp.MustCompile(`^(?:async\s+)?(def|class)\s+(\w+)`)
	pyMethod = regexp.MustCompile(`^    (?:async\s+)?def\s+(\w+)`)
	mdHead   = regexp.MustCompile(`^#{1,6}\s+(.+?)\s*#*\s*$`)
)

// symbols lists what a model picks from inside a file: for Python, top-level
// functions and classes plus the methods directly under those classes
// (Trainer.create_scheduler); for Markdown, headings outside code fences.
// Other files have none, so the file itself is the answer.
func symbols(path string) ([]option, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".py" && ext != ".md" && ext != ".mdx" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []option
	at := map[string]int{}
	add := func(key, desc string, line int) {
		if len(desc) > 160 {
			desc = desc[:160] + "…"
		}
		// @overload stubs repeat a name; keep its first slot and the last line,
		// which is the implementation.
		if i, ok := at[key]; ok {
			out[i].Desc, out[i].Line = desc, line
			return
		}
		at[key] = len(out)
		out = append(out, option{Key: key, Desc: desc, Line: line})
	}
	s := bufio.NewScanner(f)
	s.Buffer(nil, 1<<20)
	class, fence := "", false
	for n := 1; s.Scan(); n++ {
		line := s.Text()
		if ext != ".py" {
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				fence = !fence
			} else if m := mdHead.FindStringSubmatch(line); m != nil && !fence {
				key := m[1]
				for i := 2; ; i++ {
					if _, dup := at[key]; !dup {
						break
					}
					key = fmt.Sprintf("%s (%d)", m[1], i)
				}
				add(key, "section: "+m[1], n)
			}
			continue
		}
		if m := pyTop.FindStringSubmatch(line); m != nil {
			class = ""
			if m[1] == "class" {
				class = m[2]
			}
			add(m[2], strings.TrimSpace(line), n)
			continue
		}
		if m := pyMethod.FindStringSubmatch(line); m != nil && class != "" {
			add(class+"."+m[1], strings.TrimSpace(line), n)
			continue
		}
		// Any other code at column 0 ends the class, so a def under a
		// module-level if is not taken for a method.
		if line != "" && !strings.ContainsAny(line[:1], " \t#@)]}\"'") {
			class = ""
		}
	}
	return out, s.Err()
}

// matches reports whether path (slash-separated, relative to the repo root)
// fits an answer's path pattern.
func matches(pattern, path string) bool {
	ok, _ := filepath.Match(pattern, path)
	return ok
}

// right scores an answer: the file must match one accepted path, and the
// symbol must be one that path accepts, or anything when it lists none.
func right(q question, path, symbol string) (file, place bool) {
	for _, a := range q.Answers {
		if !matches(a.Path, path) {
			continue
		}
		file = true
		if len(a.Symbols) == 0 || slices.Contains(a.Symbols, symbol) {
			return true, true
		}
	}
	return file, false
}
