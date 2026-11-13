package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Emil-02/gh-spruce/internal/checks"
	"github.com/Emil-02/gh-spruce/internal/config"
	"github.com/Emil-02/gh-spruce/internal/gh"
	"github.com/Emil-02/gh-spruce/internal/scan"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type scanFlags struct {
	repo            string
	public, private bool
	includeForks    bool
	includeArchived bool
}

func (f *scanFlags) register(c *cobra.Command) {
	c.Flags().StringVarP(&f.repo, "repo", "R", "", "only this repository (name or owner/name)")
	c.Flags().BoolVar(&f.public, "public", false, "only public repositories")
	c.Flags().BoolVar(&f.private, "private", false, "only private repositories")
	c.Flags().BoolVar(&f.includeForks, "include-forks", false, "include forked repositories")
	c.Flags().BoolVar(&f.includeArchived, "include-archived", false, "include archived repositories")
	c.MarkFlagsMutuallyExclusive("public", "private")
}

type session struct {
	client *gh.Client
	config *config.Config
	report *checks.Report
}

// newClient is replaced in tests.
var newClient = func() (*gh.Client, error) { return gh.New(gh.Options{}) }

var now = time.Now

func isTTY(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }

func load(cmd *cobra.Command, args []string, f *scanFlags) (*session, error) {
	cfgPath, _ := cmd.Flags().GetString("config")
	conc, _ := cmd.Flags().GetInt("concurrency")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	client, err := newClient()
	if err != nil {
		return nil, fmt.Errorf("%w\nlog in with: gh auth login", err)
	}
	opts := scan.Options{Concurrency: conc, Ignore: cfg.Ignored, IncludeForks: f.includeForks, IncludeArchived: f.includeArchived}
	if len(args) > 0 {
		opts.Owner = args[0]
		if o, r, ok := strings.Cut(args[0], "/"); ok {
			opts.Owner, opts.Repo = o, r
		}
	}
	if f.repo != "" {
		if o, r, ok := strings.Cut(f.repo, "/"); ok {
			opts.Owner, opts.Repo = o, r
		} else {
			opts.Repo = f.repo
		}
	}
	switch {
	case f.public:
		opts.Visibility = "public"
	case f.private:
		opts.Visibility = "private"
	}
	if isTTY(os.Stderr) {
		opts.Progress = func(done, total int) {
			fmt.Fprintf(os.Stderr, "\r\033[2KScanning repositories %d/%d…", done, total)
			if done == total {
				fmt.Fprint(os.Stderr, "\r\033[2K")
			}
		}
	}
	res, err := scan.Run(cmd.Context(), client, opts)
	if err != nil {
		return nil, err
	}
	return &session{client: client, config: cfg, report: checks.Build(res, cfg, now())}, nil
}

func ownerArgs(cmd *cobra.Command, args []string) error {
	return cobra.MaximumNArgs(1)(cmd, args)
}
