package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/Emil-02/gh-spruce/internal/checks"
	"github.com/Emil-02/gh-spruce/internal/fix"
	"github.com/Emil-02/gh-spruce/internal/report"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

type fixOptions struct {
	only    []string
	license string
	direct  bool
	yes     bool
	dryRun  bool
	show    bool
}

func newFixCmd() *cobra.Command {
	var (
		sf   scanFlags
		opts fixOptions
	)
	c := &cobra.Command{
		Use:   "fix [owner | owner/repo]",
		Short: "Fix what can be fixed automatically (via pull requests)",
		Long: `Fix failing checks across all repositories.

By default every repository gets one pull request from the branch "spruce/fixes"
containing all file changes (LICENSE, README, CI workflow, .gitignore,
SECURITY.md). Repository settings (description, topics, homepage) cannot go
through a pull request and are updated directly.

Nothing is changed before you confirm. Use --dry-run to only preview.
Adding workflow files requires the "workflow" token scope:
  gh auth refresh -s workflow`,
		Example: `  gh spruce fix --dry-run
  gh spruce fix --only license,topics --yes
  gh spruce fix -R my-repo --direct
  gh spruce fix --only empty      # archive empty repositories (opt-in)`,
		Args: ownerArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOnly(opts.only); err != nil {
				return err
			}
			s, err := load(cmd, args, &sf)
			if err != nil {
				return err
			}
			if !s.report.Profile.Authenticated && !opts.dryRun {
				return fmt.Errorf("you can only fix repositories you are logged in as the owner of (%s); use --dry-run to preview", s.report.Profile.Login)
			}
			return runFix(cmd.Context(), cmd.OutOrStdout(), os.Stdin, s, s.report.Repos, opts)
		},
	}
	sf.register(c)
	c.Flags().StringSliceVar(&opts.only, "only", nil, "comma-separated check IDs to fix (see gh spruce checks)")
	c.Flags().StringVar(&opts.license, "license", "", "license to add: mit, apache-2.0, gpl-3.0, bsd-3-clause, mpl-2.0, unlicense (default from config, mit)")
	c.Flags().BoolVar(&opts.direct, "direct", false, "commit straight to the default branch instead of opening pull requests")
	c.Flags().BoolVarP(&opts.yes, "yes", "y", false, "apply without asking for confirmation")
	c.Flags().BoolVar(&opts.dryRun, "dry-run", false, "only show what would change")
	c.Flags().BoolVar(&opts.show, "show", false, "print the content of every file that would be written")
	return c
}

func validateOnly(only []string) error {
	for _, id := range only {
		c, ok := checks.ByID(id)
		if !ok {
			return fmt.Errorf("unknown check %q (see gh spruce checks)", id)
		}
		if !c.Fixable {
			return fmt.Errorf("check %q cannot be fixed automatically", id)
		}
	}
	return nil
}

var (
	hdr   = lipgloss.NewStyle().Bold(true)
	muted = lipgloss.NewStyle().Faint(true)
	okC   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	errC  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	addC  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	modC  = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
)

func runFix(ctx context.Context, out io.Writer, in io.Reader, s *session, repos []checks.RepoReport, opts fixOptions) error {
	if opts.license != "" {
		s.config.License = opts.license
	}
	only := map[string]bool{}
	for _, id := range opts.only {
		only[strings.TrimSpace(id)] = true
	}
	holder := s.report.Profile.Name
	if holder == "" {
		holder = s.report.Profile.Login
	}
	planner := &fix.Planner{Client: s.client, Config: s.config, Holder: holder, Now: now()}

	patches := make([]*fix.Patch, len(repos))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(6)
	for i, rr := range repos {
		g.Go(func() error {
			p, err := planner.Plan(gctx, rr, only)
			patches[i] = p
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}

	var todo []*fix.Patch
	files, meta := 0, 0
	for _, p := range patches {
		if p == nil || (p.Empty() && len(p.Skipped) == 0) {
			continue
		}
		fmt.Fprintln(out, hdr.Render(p.FullName))
		for _, l := range p.Lines() {
			switch l[0] {
			case '+':
				l = addC.Render(l)
			case '~':
				l = modC.Render(l)
			default:
				l = muted.Render(l)
			}
			fmt.Fprintln(out, "  "+l)
		}
		if opts.show {
			for _, f := range p.Files {
				fmt.Fprintln(out, muted.Render("  ┌── "+f.Path))
				for _, l := range strings.Split(strings.TrimRight(f.Content, "\n"), "\n") {
					fmt.Fprintln(out, muted.Render("  │ ")+l)
				}
				fmt.Fprintln(out, muted.Render("  └──"))
			}
		}
		if !p.Empty() {
			todo = append(todo, p)
			files += len(p.Files)
			if p.Description != nil || p.Homepage != nil || p.Topics != nil || p.Archive {
				meta++
			}
		}
	}
	if len(todo) == 0 {
		fmt.Fprintln(out, okC.Render("Nothing to fix. ✨"))
		return nil
	}
	mode := "one pull request per repository (branch " + fix.Branch + ")"
	if opts.direct {
		mode = "direct commits to the default branch"
	}
	fmt.Fprintf(out, "\n%s · %s · %s · %s\n", plural(len(todo), "repository", "repositories"), plural(files, "file", "files"), plural(meta, "metadata update", "metadata updates"), mode)
	if opts.dryRun {
		fmt.Fprintln(out, muted.Render("Dry run: nothing was changed."))
		return nil
	}
	if !opts.yes {
		f, ok := in.(*os.File)
		if !ok || !isTTY(f) {
			return fmt.Errorf("refusing to change repositories without confirmation; pass --yes")
		}
		fmt.Fprint(out, "Apply these changes? [y/N] ")
		line, _ := bufio.NewReader(in).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Fprintln(out, "Aborted.")
			return nil
		}
	}

	applier := &fix.Applier{Client: s.client, Direct: opts.direct}
	results := make([]fix.Outcome, len(todo))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, p := range todo {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = applier.Apply(ctx, p)
		}()
	}
	wg.Wait()
	sort.Slice(results, func(i, j int) bool { return results[i].Repo < results[j].Repo })
	failed := 0
	fmt.Fprintln(out)
	for _, r := range results {
		if r.Error != "" {
			failed++
			fmt.Fprintf(out, "%s %s %s\n", errC.Render("✗"), r.Repo, errC.Render(r.Error))
			continue
		}
		fmt.Fprintf(out, "%s %s %s\n", okC.Render("✓"), r.Repo, muted.Render(r.URL))
	}
	if failed > 0 {
		return &exitError{code: 1, msg: fmt.Sprintf("%d of %d repositories failed", failed, len(results))}
	}
	fmt.Fprintln(out, muted.Render("\nRe-run "+report.ScoreStyle(100).Render("gh spruce audit")+" after merging to see the new score."))
	return nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
