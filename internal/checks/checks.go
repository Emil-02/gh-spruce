// Package checks defines the repository health checks and the scoring model.
package checks

import (
	"time"

	"github.com/Emil-02/gh-spruce/internal/config"
	"github.com/Emil-02/gh-spruce/internal/scan"
	"github.com/Emil-02/gh-spruce/internal/stack"
)

type Status string

const (
	Pass Status = "pass"
	Warn Status = "warn"
	Fail Status = "fail"
	Skip Status = "skip" // not applicable
)

type Severity string

const (
	Low      Severity = "low"
	Medium   Severity = "medium"
	High     Severity = "high"
	Critical Severity = "critical"
)

type Context struct {
	Repo   *scan.Repo
	Stack  stack.Info
	Config *config.Config
	Now    time.Time
}

type Check struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Weight   int      `json:"weight"`
	Severity Severity `json:"severity"`
	// PublicOnly checks only matter for repositories visitors can see.
	PublicOnly bool `json:"public_only"`
	// Fixable means `gh spruce fix` can repair it.
	Fixable bool `json:"fixable"`
	// OptIn fixes only run when requested explicitly with --only.
	OptIn bool `json:"opt_in"`
	// Run returns the status and a short human message. Skip means not applicable.
	Run func(*Context) (Status, string, string) `json:"-"`
}

type Result struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Status   Status   `json:"status"`
	Severity Severity `json:"severity"`
	Weight   int      `json:"weight"`
	Message  string   `json:"message,omitempty"`
	Hint     string   `json:"hint,omitempty"`
	Fixable  bool     `json:"fixable"`
}

// All returns the built-in checks in display order.
func All() []Check {
	return []Check{
		emptyCheck, readmeCheck, licenseCheck, descriptionCheck, topicsCheck, homepageCheck,
		ciCheck, ciGreenCheck, screenshotCheck, gitignoreCheck, junkCheck, largeFilesCheck,
		secretsCheck, staleCheck, securityCheck,
	}
}

func ByID(id string) (Check, bool) {
	for _, c := range All() {
		if c.ID == id {
			return c, true
		}
	}
	return Check{}, false
}
