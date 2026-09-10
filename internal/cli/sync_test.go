package cli

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"goblin/internal/config"
	"goblin/internal/envs"
)

func TestPatternMatching(t *testing.T) {
	pats := compilePatterns([]string{"target/", "web/dist/", "*.log", "/root-only", "**/__pycache__/", "!ignored", "# comment"})
	cases := []struct {
		rel   string
		isDir bool
		want  bool
	}{
		{"target", true, true},
		{"api/target", true, true},
		{"target", false, false},
		{"web/dist", true, true},
		{"other/web/dist", true, false},
		{"a/b/c.log", false, true},
		{"root-only", false, true},
		{"x/root-only", false, false},
		{"deep/__pycache__", true, true},
	}
	for _, c := range cases {
		got := false
		for _, p := range pats {
			if p.match(c.rel, filepath.Base(c.rel), c.isDir) {
				got = true
				break
			}
		}
		if got != c.want {
			t.Errorf("match(%q, dir=%v) = %v, want %v", c.rel, c.isDir, got, c.want)
		}
	}
}

func TestMatchExcludedSkipsRootAndVCS(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"target", "api/target", ".goblin/target", ".git/target", "src"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "x.log"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default("m", "git")
	cfg.AddExclude("target/", "*.log")
	env, err := envs.Resolve(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	p := &project{Root: dir, Cfg: cfg, Env: env}
	var got []string
	for _, m := range p.matchExcluded() {
		rel, _ := filepath.Rel(dir, m)
		got = append(got, filepath.ToSlash(rel))
	}
	sort.Strings(got)
	want := []string{"api/target", "src/x.log", "target"}
	if len(got) != len(want) {
		t.Fatalf("matchExcluded = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("matchExcluded = %v, want %v", got, want)
		}
	}
	if got := p.ignorePatterns(); got[0] != ".goblin/" || len(got) != 3 {
		t.Fatalf("ignorePatterns = %v", got)
	}
}

func TestHumanBytes(t *testing.T) {
	if humanBytes(512) != "512 B" || humanBytes(2048) != "2.0 KiB" || humanBytes(3*1024*1024) != "3.0 MiB" {
		t.Fatalf("humanBytes wrong: %s %s %s", humanBytes(512), humanBytes(2048), humanBytes(3*1024*1024))
	}
}
