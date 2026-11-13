package cmd

import (
	"fmt"
	"os"

	"github.com/Emil-02/gh-spruce/internal/tui"
	"github.com/cli/go-gh/v2/pkg/browser"
	"github.com/spf13/cobra"
)

func newTUICmd() *cobra.Command {
	var sf scanFlags
	c := &cobra.Command{
		Use:   "tui [owner]",
		Short: "Browse scores interactively and pick repositories to fix",
		Args:  ownerArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !isTTY(os.Stdout) {
				return fmt.Errorf("tui needs an interactive terminal; use gh spruce audit instead")
			}
			s, err := load(cmd, args, &sf)
			if err != nil {
				return err
			}
			m := tui.New(s.report)
			for {
				if err := tui.Run(m); err != nil {
					return err
				}
				if m.Open != "" {
					_ = browser.New("", os.Stdout, os.Stderr).Browse(m.Open)
					m.Open = ""
					continue
				}
				break
			}
			if !m.Fix {
				return nil
			}
			if !s.report.Profile.Authenticated {
				return fmt.Errorf("you can only fix repositories you own")
			}
			return runFix(cmd.Context(), cmd.OutOrStdout(), os.Stdin, s, m.Selected(), fixOptions{})
		},
	}
	sf.register(c)
	return c
}
