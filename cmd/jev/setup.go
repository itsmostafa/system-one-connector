package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// goRunDir matches the temp directory `go run` builds into, deleted on exit.
var goRunDir = regexp.MustCompile(`/go-build\d+/`)

// runMCPSetup registers this binary as the "jev" MCP server with Claude Code
// and Codex, via their own CLIs, baking in the TYPESAFE_* variables and
// OPENROUTER_API_KEY from the current environment: clients launch the server
// without the user's shell env.
func runMCPSetup(ctx context.Context) error {
	if _, err := route(); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding current executable: %w", err)
	}
	if goRunDir.MatchString(exe) {
		return fmt.Errorf("refusing to configure %s: `go run` binaries are deleted on exit; build or install jev first", exe)
	}

	env := setupEnv(os.Environ())

	var errs []error
	fail := func(name string, err error) {
		fmt.Printf("❌ %s setup failed\n", name)
		errs = append(errs, fmt.Errorf("%s: %w", name, err))
	}
	for _, c := range setupCommands(exe, env) {
		if _, err := exec.LookPath(c.cli); err != nil {
			fmt.Printf("➖ %s not found, skipped (`%s` not on PATH; see README to configure by hand)\n", c.name, c.cli)
			continue
		}
		fmt.Printf("🔎 %s detected\n", c.name)
		var prev []byte
		if c.reset != nil {
			prev = claudeUserEntry()
			// Fails when there is no entry yet; nothing to report either way.
			exec.CommandContext(ctx, c.cli, c.reset...).Run()
		}
		// Captured so the CLIs' own chatter stays out of the list; shown on failure.
		if out, err := exec.CommandContext(ctx, c.cli, c.add...).CombinedOutput(); err != nil {
			fail(c.name, fmt.Errorf("%w\n%s", err, bytes.TrimSpace(out)))
			if prev != nil {
				// Not ctx: an interrupted add must still put the old entry back.
				if err := exec.Command(c.cli, "mcp", "add-json", "jev", string(prev), "-s", "user").Run(); err != nil {
					errs = append(errs, fmt.Errorf("%s: restoring previous entry: %w", c.name, err))
				}
			}
		}
	}

	// Claude Desktop has no CLI; edit its config file if the app is installed.
	desktop := false
	if dir, err := os.UserConfigDir(); err == nil {
		if _, err := os.Stat(filepath.Join(dir, "Claude")); err != nil {
			fmt.Println("➖ Claude Desktop not found, skipped (see README to configure by hand)")
		} else {
			fmt.Println("🔎 Claude Desktop detected")
			if err := setupClaudeDesktop(filepath.Join(dir, "Claude", "claude_desktop_config.json"), exe, env); err != nil {
				fail("Claude Desktop", err)
			} else {
				desktop = true
			}
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	fmt.Println("\n✅ Setup complete!")
	if desktop {
		fmt.Println("Restart Claude Desktop to load the jev server.")
	}
	return nil
}

// setupClaudeDesktop sets the jev entry in Claude Desktop's config at path,
// keeping every other key and server intact.
func setupClaudeDesktop(path, exe string, env []string) error {
	cfg := map[string]json.RawMessage{}
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &cfg); err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
	}
	servers := map[string]json.RawMessage{}
	if raw, ok := cfg["mcpServers"]; ok {
		if err := json.Unmarshal(raw, &servers); err != nil {
			return fmt.Errorf("parsing mcpServers in %s: %w", path, err)
		}
	}
	// A JSON null unmarshals to a nil map, which panics on assignment.
	if cfg == nil {
		cfg = map[string]json.RawMessage{}
	}
	if servers == nil {
		servers = map[string]json.RawMessage{}
	}

	entry := struct {
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env,omitempty"`
	}{Command: exe, Args: []string{"mcp"}}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if entry.Env == nil {
			entry.Env = map[string]string{}
		}
		entry.Env[k] = v
	}
	if servers["jev"], err = json.Marshal(entry); err != nil {
		return err
	}
	if cfg["mcpServers"], err = json.Marshal(servers); err != nil {
		return err
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	// Write to a temp file and rename, so a failed write can't truncate the
	// app's config (it also holds the user's preferences).
	tmp, err := os.CreateTemp(filepath.Dir(path), ".claude_desktop_config-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(out, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// claudeUserEntry returns Claude Code's current user-scope jev entry, or nil
// if there is none or the config cannot be read.
func claudeUserEntry() []byte {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		dir, _ = os.UserHomeDir()
	}
	b, err := os.ReadFile(filepath.Join(dir, ".claude.json"))
	if err != nil {
		return nil
	}
	var cfg struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return nil
	}
	return cfg.MCPServers["jev"]
}

// setupEnv picks the variables to bake into the client configs: every
// TYPESAFE_* knob, plus the OpenRouter key for that route. The "=" anchors the
// name, so OPENROUTER_API_KEY_OTHER is left behind.
func setupEnv(environ []string) []string {
	var env []string
	for _, kv := range environ {
		if strings.HasPrefix(kv, "TYPESAFE_") || strings.HasPrefix(kv, "OPENROUTER_API_KEY=") {
			env = append(env, kv)
		}
	}
	return env
}

type setupCommand struct {
	name, cli  string
	reset, add []string
}

func setupCommands(exe string, env []string) []setupCommand {
	claude := []string{"mcp", "add", "jev", "-s", "user"}
	codex := []string{"mcp", "add", "jev"}
	for _, kv := range env {
		// One flag per pair: claude's -e is variadic and would swallow the name.
		claude = append(claude, "-e", kv)
		codex = append(codex, "--env", kv)
	}
	return []setupCommand{
		// `claude mcp add` refuses an existing name; `codex mcp add` overwrites.
		{"Claude Code", "claude", []string{"mcp", "remove", "jev", "-s", "user"}, append(claude, "--", exe, "mcp")},
		{"Codex", "codex", nil, append(codex, "--", exe, "mcp")},
	}
}
