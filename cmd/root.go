// Package cmd implements the gh-spruce command line.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"

	"github.com/spf13/cobra"
)

var version = ""

// exitError carries a specific exit code without printing usage.
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }

func Version() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "spruce",
		Short: "Audit, score and fix every repository on your GitHub profile",
		Long: `gh spruce audits all repositories of a user or organization, scores their
health from 0 to 100, opens pull requests that fix what can be fixed
automatically, and renders a shareable SVG card for your profile README.`,
		Example: `  gh spruce audit                 # your own repositories
  gh spruce audit octocat -v      # someone else's public repositories
  gh spruce fix --dry-run         # preview fixes
  gh spruce fix --only license,topics
  gh spruce card -o spruce.svg`,
		Version:       Version(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().String("config", "", "path to a .spruce.yml config file")
	root.PersistentFlags().Int("concurrency", 8, "number of repositories scanned in parallel")
	root.AddCommand(newAuditCmd(), newFixCmd(), newCardCmd(), newChecksCmd(), newTUICmd())
	return root
}

func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err := newRoot().ExecuteContext(ctx)
	if err == nil {
		return 0
	}
	var ee *exitError
	if errors.As(err, &ee) {
		if ee.msg != "" {
			fmt.Fprintln(os.Stderr, ee.msg)
		}
		return ee.code
	}
	fmt.Fprintln(os.Stderr, "error:", err)
	return 1
}
