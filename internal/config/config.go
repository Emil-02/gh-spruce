// Package config loads the optional .spruce.yml file.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	// Ignore lists repository names or glob patterns to skip.
	Ignore []string `yaml:"ignore"`
	// Disable lists check IDs to skip.
	Disable []string `yaml:"disable"`
	// Weights overrides the weight of individual checks.
	Weights map[string]int `yaml:"weights"`
	// License is the SPDX key used by `fix` (mit, apache-2.0, gpl-3.0, ...).
	License string `yaml:"license"`
	// Holder is the copyright holder written into new LICENSE files.
	Holder       string `yaml:"holder"`
	MinTopics    int    `yaml:"min_topics"`
	LargeFileMB  int    `yaml:"large_file_mb"`
	StaleDays    int    `yaml:"stale_days"`
	MinReadmeLen int    `yaml:"min_readme_length"`

	Source string `yaml:"-"`
}

func Default() *Config {
	return &Config{License: "mit", MinTopics: 3, LargeFileMB: 10, StaleDays: 365, MinReadmeLen: 300}
}

// Load reads explicit, or ./.spruce.yml, or $XDG_CONFIG_HOME/gh-spruce/config.yml.
func Load(explicit string) (*Config, error) {
	c := Default()
	candidates := []string{explicit}
	if explicit == "" {
		candidates = []string{".spruce.yml", ".spruce.yaml"}
		if dir, err := os.UserConfigDir(); err == nil {
			candidates = append(candidates, filepath.Join(dir, "gh-spruce", "config.yml"))
		}
	}
	for _, p := range candidates {
		b, err := os.ReadFile(p)
		if errors.Is(err, fs.ErrNotExist) && explicit == "" {
			continue
		}
		if err != nil {
			return nil, err
		}
		if err := yaml.Unmarshal(b, c); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		c.Source = p
		break
	}
	d := Default()
	if c.License == "" {
		c.License = d.License
	}
	if c.MinTopics <= 0 {
		c.MinTopics = d.MinTopics
	}
	if c.LargeFileMB <= 0 {
		c.LargeFileMB = d.LargeFileMB
	}
	if c.StaleDays <= 0 {
		c.StaleDays = d.StaleDays
	}
	if c.MinReadmeLen <= 0 {
		c.MinReadmeLen = d.MinReadmeLen
	}
	return c, nil
}

func (c *Config) Ignored(name string) bool {
	for _, g := range c.Ignore {
		if ok, _ := path.Match(g, name); ok || g == name {
			return true
		}
	}
	return false
}

func (c *Config) Disabled(id string) bool {
	for _, d := range c.Disable {
		if d == id {
			return true
		}
	}
	return false
}
