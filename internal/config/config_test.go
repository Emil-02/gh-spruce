package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	p := filepath.Join(t.TempDir(), "spruce.yml")
	_ = os.WriteFile(p, []byte("ignore: [\"SEM_*\", Python]\ndisable: [stale]\nlicense: apache-2.0\nholder: Octo\nweights: {ci: 20}\n"), 0o644)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Ignored("SEM_1") || !c.Ignored("Python") || c.Ignored("app") || !c.Disabled("stale") {
		t.Errorf("ignore/disable not applied: %+v", c)
	}
	if c.License != "apache-2.0" || c.Holder != "Octo" || c.Weights["ci"] != 20 || c.MinTopics != 3 || c.LargeFileMB != 10 {
		t.Errorf("unexpected config %+v", c)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yml")); err == nil {
		t.Error("explicit missing config should fail")
	}
}
