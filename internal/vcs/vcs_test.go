package vcs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteIgnorePreservesUserContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(path, []byte("*.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := WriteIgnore(Git, dir, []string{".goblin/", "target/"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Created || len(res.Added) != 2 {
		t.Fatalf("unexpected result %+v", res)
	}
	data, _ := os.ReadFile(path)
	got := string(data)
	if !strings.HasPrefix(got, "*.log\n") || !strings.Contains(got, blockStart) || !strings.Contains(got, "\ntarget/\n") {
		t.Fatalf("bad content:\n%s", got)
	}

	// Append user content after the block, then rewrite with fewer patterns.
	if err := os.WriteFile(path, []byte(got+"\n# mine\n.env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = WriteIgnore(Git, dir, []string{".goblin/"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 1 || res.Removed[0] != "target/" {
		t.Fatalf("expected target/ removed, got %+v", res)
	}
	data, _ = os.ReadFile(path)
	got = string(data)
	if !strings.HasPrefix(got, "*.log\n") || !strings.HasSuffix(got, "# mine\n.env\n") || strings.Contains(got, "target/") {
		t.Fatalf("user content lost:\n%s", got)
	}
	if strings.Count(got, blockStart) != 1 {
		t.Fatalf("managed block duplicated:\n%s", got)
	}
	if got := ManagedPatterns(Git, dir); len(got) != 1 || got[0] != ".goblin/" {
		t.Fatalf("ManagedPatterns = %v", got)
	}
	// Idempotent.
	res, err = WriteIgnore(Git, dir, []string{".goblin/"})
	if err != nil || len(res.Added)+len(res.Removed) != 0 {
		t.Fatalf("rewrite not idempotent: %+v %v", res, err)
	}
}

func TestWriteIgnoreCreates(t *testing.T) {
	dir := t.TempDir()
	res, err := WriteIgnore(Ivaldi, dir, []string{".goblin/"})
	if err != nil || !res.Created || res.File != ".ivaldiignore" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".ivaldiignore")); err != nil {
		t.Fatal(err)
	}
	if res, err := WriteIgnore(None, dir, []string{"x"}); err != nil || res.File != "" {
		t.Fatalf("none should be a no-op: %+v %v", res, err)
	}
}

func TestDetectPrefersNearest(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(filepath.Join(sub, ".ivaldi"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, ".ivaldi", "HEAD"), []byte("main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if k, root, ok := Detect(sub); !ok || k != Ivaldi || root != sub {
		t.Fatalf("Detect(sub) = %v %q %v", k, root, ok)
	}
	if k, root, ok := Detect(dir); !ok || k != Git || root != dir {
		t.Fatalf("Detect(dir) = %v %q %v", k, root, ok)
	}
	if _, _, ok := Detect(t.TempDir()); ok {
		t.Fatal("expected no repository")
	}
}

func TestDetectIgnoresIvaldiGlobalConfigDir(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".ivaldi"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ivaldi", "config"), []byte("[user]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(home, "proj")
	if err := os.Mkdir(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := Detect(proj); ok {
		t.Fatal("global ~/.ivaldi config dir was mistaken for a repository")
	}
}
