package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizePattern(t *testing.T) {
	cases := map[string]string{
		"target/":            "target/",
		"./target/":          "target/",
		"  dist ":            "dist",
		"web//node_modules/": "web/node_modules/",
		".":                  "",
		"":                   "",
		"*.egg-info/":        "*.egg-info/",
	}
	for in, want := range cases {
		if got := NormalizePattern(in); got != want {
			t.Errorf("NormalizePattern(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAddRemoveExclude(t *testing.T) {
	c := Default("x", "git")
	added := c.AddExclude("target/", "./target/", "dist", "")
	if len(added) != 2 || added[0] != "target/" || added[1] != "dist" {
		t.Fatalf("AddExclude added %v", added)
	}
	if again := c.AddExclude("dist"); len(again) != 0 {
		t.Fatalf("duplicate exclude was added: %v", again)
	}
	removed := c.RemoveExclude("./target/", "nope")
	if len(removed) != 1 || removed[0] != "target/" {
		t.Fatalf("RemoveExclude = %v", removed)
	}
	if len(c.Exclude.Paths) != 1 || c.Exclude.Paths[0] != "dist" {
		t.Fatalf("remaining excludes = %v", c.Exclude.Paths)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c := Default("proj", "ivaldi")
	c.Managers = append(c.Managers,
		Manager{Kind: "cargo", Path: "api", Build: []string{"cargo build --release"}},
		Manager{Kind: "npm", Path: "web", Env: map[string]string{"NODE_ENV": "production"}},
	)
	c.AddExclude("api/target/", "web/node_modules/")
	c.Env = map[string]string{"FOO": "bar"}
	if err := Save(dir, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Goblin.Name != "proj" || got.Goblin.VCS != "ivaldi" || got.Goblin.Root != ".goblin" {
		t.Fatalf("goblin section = %+v", got.Goblin)
	}
	if len(got.Managers) != 2 || got.Managers[0].Build[0] != "cargo build --release" || got.Managers[1].Env["NODE_ENV"] != "production" {
		t.Fatalf("managers = %+v", got.Managers)
	}
	if len(got.Exclude.Paths) != 2 || got.Env["FOO"] != "bar" {
		t.Fatalf("exclude/env = %v %v", got.Exclude.Paths, got.Env)
	}
	if !got.HasManager("cargo", "./api") || got.HasManager("cargo", ".") {
		t.Fatal("HasManager mismatch")
	}
}

func TestLoadRejectsBadManifests(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("[goblin]\nname='x'\nvcs='svn'\n")
	if _, err := Load(dir); err == nil {
		t.Fatal("expected unknown vcs error")
	}
	write("[goblin]\nname='x'\n[[manager]]\nkind='cargo'\npath='../else'\n")
	if _, err := Load(dir); err == nil {
		t.Fatal("expected path escape error")
	}
	write("[goblin]\nname='x'\nbogus=1\n")
	if _, err := Load(dir); err == nil {
		t.Fatal("expected unknown key error")
	}
	write("[goblin]\nname='x'\nversion=99\n")
	if _, err := Load(dir); err == nil {
		t.Fatal("expected newer schema error")
	}
}

func TestFindWalksUp(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, Default("x", "none")); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := Find(nested)
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := filepath.EvalSymlinks(dir); filepath.Clean(root) != want && filepath.Clean(root) != dir {
		t.Fatalf("Find = %q, want %q", root, dir)
	}
	if _, err := Find(t.TempDir()); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestValidateAnchoredExclude(t *testing.T) {
	c := Default("x", "git")
	c.AddExclude("/goblin", "web/dist/")
	if err := c.Validate(); err != nil {
		t.Fatalf("anchored pattern rejected: %v", err)
	}
	c.Exclude.Paths = append(c.Exclude.Paths, "../escape")
	if err := c.Validate(); err == nil {
		t.Fatal("expected escape error")
	}
}
