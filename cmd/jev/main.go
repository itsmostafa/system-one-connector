// Command jev is an MCP stdio server for running TypeSafe Jev prompts.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
- Batch independent questions over the same state into one call; they run in parallel and cannot see each other's answers.
- Include a no-match option in a choice when nothing may fit. Score levels must describe concrete situations.
- A noul near 0.5 means uncertain, not medium intensity. Confidence measures how concentrated the distribution is, not correctness.
Docs: https://docs.typesafe.ai/llms.txt`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := newRootCmd().ExecuteContext(ctx)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "jev:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:     "jev",
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
	setupCmd := &cobra.Command{Use: "setup", Short: "Register this binary with your agents"}
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
			Short: "Install the jev extension for pi",
			Args:  cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error {
				return runPiSetup()
			},
		},
	)
	root.AddCommand(
		mcpCmd,
		setupCmd,
		&cobra.Command{Use: "update", Short: "Update jev to the latest release", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
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
func route() (*Client, error) {
	switch {
	case os.Getenv("TYPESAFE_API_KEY") != "":
		return &Client{
			URL:    "https://api.typesafe.ai/v1/systemone",
			APIKey: os.Getenv("TYPESAFE_API_KEY"),
			Model:  "jev-latest",
		}, nil
	case os.Getenv("OPENROUTER_API_KEY") != "":
		return &Client{
			// ponytail: /api/alpha/ is OpenRouter's alpha path and may move.
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
	s := mcp.NewServer(&mcp.Implementation{Name: "jev", Version: version}, &mcp.ServerOptions{Instructions: instructions})
	registerTools(s, c)
	return s.Run(ctx, &mcp.StdioTransport{})
}
