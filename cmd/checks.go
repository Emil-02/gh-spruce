package cmd

import (
	"fmt"
	"text/tabwriter"

	"github.com/Emil-02/gh-spruce/internal/checks"
	"github.com/spf13/cobra"
)

func newChecksCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "checks",
		Short: "List the checks, their weights and whether they can be fixed",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tWEIGHT\tSEVERITY\tSCOPE\tFIX\tTITLE")
			for _, c := range checks.All() {
				scope, fix := "all", "-"
				if c.PublicOnly {
					scope = "public"
				}
				if c.Fixable {
					fix = "auto"
					if c.OptIn {
						fix = "opt-in"
					}
				}
				weight := fmt.Sprint(c.Weight)
				if c.ID == "empty" {
					weight = "gate"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", c.ID, weight, c.Severity, scope, fix, c.Title)
			}
			w.Flush()
			fmt.Fprintln(cmd.OutOrStdout(), "\nScore = earned weight / applicable weight (warnings earn half).")
			fmt.Fprintln(cmd.OutOrStdout(), "Empty repositories score 0; a critical failure caps the score at 40.")
		},
	}
}
