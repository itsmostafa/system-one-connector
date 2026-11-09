// Command soctriage builds the SOC alert-triage eval set from SecAlertBench
// (https://github.com/Dxsssu/SecAlertBench): production web alerts from three
// enterprise SOCs, each labeled Attack or Non-Attack. Jev will decide whether
// an alert is Risky, an attack to escalate, or a False Positive to close.
//
//	go run ./soctriage fetch
//	go run ./soctriage build
//	go run ./soctriage run -profile jev
//	go run ./soctriage run -profile jev -protocol noul
//	go run ./soctriage run -profile jev -protocol inconclusive
//	go run ./soctriage score
package main

import (
	"bufio"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// SecAlertBench states no license, so the file is downloaded at a pinned
// commit into gitignored eval/data/ and never committed.
const (
	sourceURL    = "https://raw.githubusercontent.com/Dxsssu/SecAlertBench/42a84889fda912ca432c994924a1ccd4b9df6274/0x02.%20Processed%20SecAlertBench%20Dataset/secalertbench.json"
	sourceSHA256 = "33f95305d1c42f8e615e4f94066119570859dee7eb086dff7c2273536c932ea3"
	rawFile      = "secalertbench.json"
	alertsFile   = "alerts.jsonl"
)

//go:embed translations.json
var translationsJSON []byte

//go:embed criteria.json
var criteriaJSON []byte

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: soctriage fetch|build|run|score [flags]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "fetch":
		err = fetch(os.Args[2:])
	case "build":
		err = build(os.Args[2:])
	case "run":
		err = run(os.Args[2:])
	case "score":
		err = score(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "soctriage:", err)
		os.Exit(1)
	}
}

func fetch(args []string) error {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
	dir := fs.String("data", "data/soctriage", "dataset directory")
	fs.Parse(args)
	path := filepath.Join(*dir, rawFile)
	if b, err := os.ReadFile(path); err == nil && hash(b) == sourceSHA256 {
		return nil
	}
	resp, err := http.Get(sourceURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", sourceURL, resp.Status)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if got := hash(b); got != sourceSHA256 {
		return fmt.Errorf("%s: sha256 %s, want %s", rawFile, got, sourceSHA256)
	}
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func build(args []string) error {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	dir := fs.String("data", "data/soctriage", "dataset directory")
	size := fs.Int("size", 4000, "alerts in the eval set")
	fs.Parse(args)

	b, err := os.ReadFile(filepath.Join(*dir, rawFile))
	if err != nil {
		return fmt.Errorf("%w (run fetch first)", err)
	}
	if got := hash(b); got != sourceSHA256 {
		return fmt.Errorf("%s: sha256 %s, want %s (run fetch again)", rawFile, got, sourceSHA256)
	}
	var raw []alert
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	var tr translations
	if err := json.Unmarshal(translationsJSON, &tr); err != nil {
		return fmt.Errorf("translations.json: %w", err)
	}
	rows, st, err := selectAlerts(raw, tr, *size)
	if err != nil {
		return err
	}

	f, err := os.Create(filepath.Join(*dir, alertsFile))
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Fprint(os.Stderr, st)
	return nil
}

func readJSONL[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []T
	s := bufio.NewScanner(f)
	s.Buffer(nil, 1<<20)
	for s.Scan() {
		if strings.TrimSpace(s.Text()) == "" {
			continue
		}
		var v T
		if err := json.Unmarshal(s.Bytes(), &v); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out = append(out, v)
	}
	return out, s.Err()
}

func hash(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// keyHash orders alerts deterministically but unrelated to their content's
// sort order, so a capped fill and the final row order both look shuffled.
func keyHash(parts ...string) string {
	return hash([]byte(strings.Join(parts, "\x00")))
}
