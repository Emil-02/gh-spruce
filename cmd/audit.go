package cmd

import (
	"bytes"
	"fmt"
	"os"

	"github.com/Emil-02/gh-spruce/internal/report"
	"github.com/spf13/cobra"
)

func newAuditCmd() *cobra.Command {
	var (
		sf        scanFlags
		asJSON    bool
		asMD      bool
		verbose   bool
		failUnder int
		output    string
	)
	c := &cobra.Command{
		Use:   "audit [owner | owner/repo]",
		Short: "Score every repository and list what to improve",
		Long: `Scan all repositories of a user or organization (default: you) and score them.

Your own private repositories are included when you are the owner.
Forks and archived repositories are skipped unless requested.`,
		Example: `  gh spruce audit
  gh spruce audit my-org --public --md -o HEALTH.md
  gh spruce audit --json | jq '.repos[] | {name: .repo.name, score}'
  gh spruce audit --fail-under 80   # non-zero exit for CI`,
		Args: ownerArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load(cmd, args, &sf)
			if err != nil {
				return err
			}
			var buf bytes.Buffer
			w := &buf
			switch {
			case asJSON:
				if err := report.JSON(w, s.report); err != nil {
					return err
				}
			case asMD:
				report.Markdown(w, s.report)
			default:
				report.Table(w, s.report, verbose)
			}
			if output != "" && output != "-" {
				if err := os.WriteFile(output, buf.Bytes(), 0o644); err != nil {
					return err
				}
			} else if _, err := cmd.OutOrStdout().Write(buf.Bytes()); err != nil {
				return err
			}
			if failUnder > 0 && s.report.Score < failUnder {
				return &exitError{code: 1, msg: fmt.Sprintf("profile score %d is below --fail-under %d", s.report.Score, failUnder)}
			}
			return nil
		},
	}
	sf.register(c)
	c.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	c.Flags().BoolVar(&asMD, "md", false, "output Markdown")
	c.Flags().BoolVarP(&verbose, "verbose", "v", false, "show the reason and a hint for every issue")
	c.Flags().IntVar(&failUnder, "fail-under", 0, "exit with status 1 when the profile score is below this value")
	c.Flags().StringVarP(&output, "output", "o", "", "write the report to a file")
	c.MarkFlagsMutuallyExclusive("json", "md")
	return c
}
