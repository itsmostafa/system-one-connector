// Command evaluate is an MCP stdio server for running TypeSafe Jev prompts.
package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

// version is set by release builds via -ldflags "-X main.version=...".
var version = "dev"

func init() {
	if info, ok := debug.ReadBuildInfo(); ok && version == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		version = info.Main.Version
	}
}

const instructions = `The evaluate tool runs Jev, a TypeSafe System One model that returns typed judgments and probabilities, not generated text.
- Question types: noul (probability a yes/no condition holds), choice (one option from a criteria map), score (probability-weighted position on ordered criteria levels).
- Ask one narrow judgment per question. Question ids are NOT sent to the model, so instructions must carry the full meaning.
- Put everything the judgment needs in state; prefer a JSON object with named fields, and reference nested fields with backticked paths like ` + "`ticket.messages[0].text`" + `.
- State is what you observed — source records with their original field names and values, trimmed to the fields the question needs. Don't add derived or summary fields; keep the uncertainty and the counterevidence, and leave out your own verdict, which Jev reads as evidence.
- Background the judgment depends on (user goals, policies, priorities, identities) also belongs in state as named fields, e.g. {"user_profile": ..., "email": ...}; it is fact to weigh, unlike your verdict.
- Write instructions that name the condition to test, not the conclusion you expect. "Does the message report a failed payout?", not "Confirm this urgent payout failure."
- A conclusion asserted in state biases the answer toward it, and the confidence that comes back is then agreement with yourself, not independent corroboration.
- Jev reads literally and is not a calculator: state the exact condition, put boundary cases in criteria, and keep counting, arithmetic, and date comparison in code.
- Send only the state the question needs: unrelated detail costs accuracy, and instructions embedded in state can steer the answer.
- Batch independent questions over the same state into one call; they run in parallel and cannot see each other's answers.
- To ask the same questions of many records, pass them as items (id → record, up to 100 per call) instead of repeating each question per record; each item is judged independently.
- Include a no-match option in a choice when nothing may fit. Score levels must describe concrete situations.
- A noul near 0.5 means uncertain, not medium intensity. Confidence measures how concentrated the distribution is, not correctness.
- Jev returns no reasoning. To audit a low-confidence or near-0.5 answer, read its full probabilities, re-ask it as narrower nouls about the specific evidence, or escalate to a reasoning model or a human instead of acting.
- Score criteria are an ordered array; the answer is 0-indexed, so N levels score 0 to N-1. A 3.87 over 5 levels sits between levels 3 and 4, not 3.87/5. Report it with the labels from the response ` + "`legend`" + `, and read ` + "`probabilities`" + ` alongside it.
Docs: https://docs.typesafe.ai/llms.txt`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := newRootCmd().ExecuteContext(ctx)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "evaluate:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:     "evaluate",
		Short:   "MCP server for TypeSafe Jev prompts",
		Version: version,
		// Stdout carries the MCP protocol; main reports errors on stderr.
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetVersionTemplate("{{.Version}}\n")
	mcpCmd := &cobra.Command{Use: "mcp", Short: "Run the MCP server over stdio", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return serve(cmd.Context())
	}}
	// Args+RunE, not a bare parent: cobra checks Runnable before validating args,
	// so without both `evaluate setup typo` prints help and exits 0.
	setupCmd := &cobra.Command{
		Use:   "setup",
		Short: "Register this binary with your agents",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	setupCmd.AddCommand(
		&cobra.Command{
			Use:   "mcp",
			Short: "Register with Claude Code, Claude Desktop, and Codex",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return runMCPSetup(cmd.Context())
			},
		},
		&cobra.Command{
			Use:   "pi",
			Short: "Install the evaluate extension for pi",
			Args:  cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error {
				return runPiSetup()
			},
		},
	)
	root.AddCommand(
		mcpCmd,
		setupCmd,
		&cobra.Command{Use: "update", Short: "Update evaluate to the latest release", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			return runUpdate(cmd.Context())
		}},
		&cobra.Command{Use: "version", Short: "Print the version", Args: cobra.NoArgs, Run: func(*cobra.Command, []string) {
			fmt.Println(version)
		}},
	)
	return root
}

// route picks the evaluation endpoint from the environment: the TypeSafe API
// when TYPESAFE_API_KEY is set, otherwise OpenRouter's Decisions router.
// TypeSafe wins when both are set, so an OPENROUTER_API_KEY left in the shell
// by another tool cannot silently reroute and re-bill an existing setup.
// TYPESAFE_BASE_URL is validated here rather than in setup because route is the
// only place a *Client is built, so a hand-written client config cannot smuggle
// a dead endpoint past it either.
func route() (*Client, error) {
	switch {
	case os.Getenv("TYPESAFE_API_KEY") != "":
		base := cmp.Or(os.Getenv("TYPESAFE_BASE_URL"), "https://api.typesafe.ai")
		u, err := url.Parse(base)
		if err != nil {
			return nil, fmt.Errorf("TYPESAFE_BASE_URL: %w", err)
		}
		// Parse accepts a bare host as a relative URL, so the scheme and host
		// carry the check.
		if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("TYPESAFE_BASE_URL must be an absolute http(s) URL, got %q", base)
		}
		return &Client{
			URL:    u.JoinPath("v1", "systemone").String(),
			APIKey: os.Getenv("TYPESAFE_API_KEY"),
			Model:  "jev-latest",
		}, nil
	case os.Getenv("OPENROUTER_API_KEY") != "":
		return &Client{
			URL:    "https://openrouter.ai/api/alpha/decisions",
			APIKey: os.Getenv("OPENROUTER_API_KEY"),
			Model:  "~typesafe/jev-latest",
		}, nil
	}
	return nil, errors.New("set TYPESAFE_API_KEY (https://console.typesafe.ai/) or OPENROUTER_API_KEY (https://openrouter.ai/keys)")
}

func serve(ctx context.Context) error {
	c, err := route()
	if err != nil {
		return err
	}
	c.HTTP = &http.Client{Timeout: 60 * time.Second}
	c.Backoff = time.Second
	s := mcp.NewServer(&mcp.Implementation{Name: "evaluate", Version: version}, &mcp.ServerOptions{Instructions: instructions})
	registerTools(s, c)
	return s.Run(ctx, &mcp.StdioTransport{})
}
