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
	mcpCmd.AddCommand(&cobra.Command{
		Use:   "setup",
		Short: "Register this binary with Claude Code, Claude Desktop, and Codex",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMCPSetup(cmd.Context())
		},
	})
	root.AddCommand(
		mcpCmd,
		&cobra.Command{Use: "version", Short: "Print the version", Args: cobra.NoArgs, Run: func(*cobra.Command, []string) {
			fmt.Println(version)
		}},
	)
	return root
}

// apiKey returns TYPESAFE_API_KEY from the environment.
func apiKey() (string, error) {
	key := os.Getenv("TYPESAFE_API_KEY")
	if key == "" {
		return "", errors.New("TYPESAFE_API_KEY must be set (create one at https://console.typesafe.ai/)")
	}
	return key, nil
}

func serve(ctx context.Context) error {
	key, err := apiKey()
	if err != nil {
		return err
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "jev", Version: version}, &mcp.ServerOptions{Instructions: instructions})
	registerTools(s, &Client{
		BaseURL: "https://api.typesafe.ai",
		APIKey:  key,
		HTTP:    &http.Client{Timeout: 60 * time.Second},
		Backoff: time.Second,
	})
	return s.Run(ctx, &mcp.StdioTransport{})
}
