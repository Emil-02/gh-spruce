package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/Emil-02/gh-spruce/internal/card"
	"github.com/spf13/cobra"
)

func newCardCmd() *cobra.Command {
	var (
		theme  string
		output string
	)
	c := &cobra.Command{
		Use:   "card [owner]",
		Short: "Render an SVG health card for your profile README",
		Long: `Render an SVG card with the profile score and key statistics.

Only public repositories are counted, so the card is safe to publish.
Use the gh-spruce GitHub Action to refresh it on a schedule.`,
		Example: `  gh spruce card -o spruce.svg
  gh spruce card octocat --theme light -o -`,
		Args: ownerArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			sf := scanFlags{public: true}
			s, err := load(cmd, args, &sf)
			if err != nil {
				return err
			}
			svg, err := card.Render(s.report, theme)
			if err != nil {
				return err
			}
			if gho := os.Getenv("GITHUB_OUTPUT"); gho != "" {
				if err := appendOutput(gho, fmt.Sprintf("score=%d\ngrade=%s\n", s.report.Score, s.report.Grade)); err != nil {
					return err
				}
			}
			if output == "-" {
				_, err = fmt.Fprint(cmd.OutOrStdout(), svg)
				return err
			}
			if err := os.WriteFile(output, []byte(svg), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Wrote %s (score %d, grade %s)\nEmbed it with: ![repo health](%s)\n", output, s.report.Score, s.report.Grade, output)
			return nil
		},
	}
	c.Flags().StringVarP(&theme, "theme", "t", "dark", "card theme: "+strings.Join(card.ThemeNames(), ", "))
	c.Flags().StringVarP(&output, "output", "o", "spruce-card.svg", "output file, or - for stdout")
	return c
}

func appendOutput(path, data string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
