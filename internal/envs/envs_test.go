package envs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"goblin/internal/config"
)

func TestResolveIsolatesIntoRoot(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default("demo", "git")
	cfg.Managers = []config.Manager{
		{Kind: "cargo", Path: "api"},
		{Kind: "npm", Path: "web", Env: map[string]string{"NODE_ENV": "test"}},
	}
	cfg.Env = map[string]string{"CUSTOM": "{root}/custom"}
	env, err := Resolve(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, ".goblin")
	if env.Root != root || env.Vars["GOBLIN_ENV"] != "demo" {
		t.Fatalf("root/name wrong: %+v", env)
	}
	if env.Vars["CARGO_HOME"] != filepath.Join(root, "cargo") {
		t.Errorf("CARGO_HOME = %q", env.Vars["CARGO_HOME"])
	}
	if env.Vars["NODE_ENV"] != "test" || env.Vars["CUSTOM"] != filepath.Join(root, "custom") {
		t.Errorf("overrides not applied: %v", env.Vars)
	}
	if env.PathPrepend[0] != env.Bin || !contains(env.PathPrepend, filepath.Join(dir, "web", "node_modules", ".bin")) {
		t.Errorf("PATH prepend = %v", env.PathPrepend)
	}
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("CARGO_HOME", "/home/me/.cargo")
	environ := env.Environ()
	var sawPath, sawCargo bool
	for _, kv := range environ {
		if kv == "CARGO_HOME=/home/me/.cargo" {
			t.Fatal("inherited CARGO_HOME leaked through")
		}
		if strings.HasPrefix(kv, "CARGO_HOME=") {
			sawCargo = true
		}
		if strings.HasPrefix(kv, "PATH="+env.Bin+string(os.PathListSeparator)) && strings.HasSuffix(kv, "/usr/bin") {
			sawPath = true
		}
	}
	if !sawPath || !sawCargo {
		t.Fatalf("Environ missing PATH/CARGO_HOME: %v", environ)
	}
}

func TestResolveRejectsUnknownKind(t *testing.T) {
	cfg := config.Default("x", "none")
	cfg.Managers = []config.Manager{{Kind: "what", Path: "."}}
	if _, err := Resolve(t.TempDir(), cfg); err == nil {
		t.Fatal("expected error")
	}
}

func TestShellScriptQuoting(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "with space")
	cfg := config.Default("q", "none")
	env, err := Resolve(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	sh := env.ShellScript("sh")
	if !strings.Contains(sh, "export GOBLIN_DIR='"+dir+"'") {
		t.Errorf("sh quoting wrong:\n%s", sh)
	}
	if !strings.Contains(sh, `:"$PATH"`) {
		t.Errorf("sh PATH not preserved:\n%s", sh)
	}
	fish := env.ShellScript("fish")
	if !strings.Contains(fish, "set -gx GOBLIN_ENV 'q'") || !strings.Contains(fish, "fish_add_path") {
		t.Errorf("fish script wrong:\n%s", fish)
	}
}

func TestStepsAndArtifacts(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default("s", "none")
	m := config.Manager{Kind: "pip", Path: "py", Build: []string{"echo {root}"}}
	cfg.Managers = []config.Manager{m}
	env, _ := Resolve(dir, cfg)
	install, build := env.Steps(m)
	if len(install) != 2 || !strings.Contains(install[0], filepath.Join(dir, "py", ".venv")) {
		t.Errorf("install steps = %v", install)
	}
	if len(build) != 1 || build[0] != "echo "+env.Root {
		t.Errorf("build steps = %v", build)
	}
	arts := Artifacts(m)
	if arts[0] != "py/.venv/" || !contains(arts, "py/*.egg-info/") {
		t.Errorf("artifacts = %v", arts)
	}
	if got := PrefixPattern(".", "target/"); got != "target/" {
		t.Errorf("root pattern = %q", got)
	}
}

func TestEnsureDirsWritesScriptsAndMarker(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default("e", "git")
	cfg.Managers = []config.Manager{{Kind: "go", Path: "."}}
	env, _ := Resolve(dir, cfg)
	if err := env.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"env.sh", "env.fish", ".gitignore", "bin", filepath.Join("go", "pkg", "mod")} {
		if _, err := os.Stat(filepath.Join(env.Root, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
	if _, ok := env.LoadState(); ok {
		t.Fatal("state should not exist before a build")
	}
	if err := env.SaveState(State{Managers: []string{"go"}}); err != nil {
		t.Fatal(err)
	}
	if st, ok := env.LoadState(); !ok || st.Managers[0] != "go" {
		t.Fatalf("state round trip failed: %+v %v", st, ok)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
