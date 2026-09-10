// Package envs resolves a goblin manifest into concrete environment
// variables and generated shell scripts, and runs commands inside it.
package envs

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"goblin/internal/catalog"
	"goblin/internal/config"
)

// Environment is a fully resolved goblin environment.
type Environment struct {
	Name string
	// Dir is the absolute project directory holding goblin.toml.
	Dir string
	// Root is the absolute isolation directory (Dir/.goblin by default).
	Root string
	// Bin is Root/bin, first on PATH.
	Bin string
	// Vars are the exported variables, excluding PATH.
	Vars map[string]string
	// PathPrepend are directories placed ahead of the inherited PATH.
	PathPrepend []string
	Config      *config.Config
}

// Resolve computes the environment for the manifest at dir.
func Resolve(dir string, cfg *config.Config) (*Environment, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	root := filepath.Join(abs, filepath.FromSlash(cfg.Goblin.Root))
	env := &Environment{
		Name:   cfg.Goblin.Name,
		Dir:    abs,
		Root:   root,
		Bin:    filepath.Join(root, "bin"),
		Vars:   map[string]string{},
		Config: cfg,
	}
	env.Vars["GOBLIN_ENV"] = cfg.Goblin.Name
	env.Vars["GOBLIN_DIR"] = abs
	env.Vars["GOBLIN_ROOT"] = root
	env.PathPrepend = append(env.PathPrepend, env.Bin)

	seenPath := map[string]bool{env.Bin: true}

	// Containment baseline: redirect every known manager's global caches and
	// install prefixes into the root, so ad-hoc tool use inside the
	// environment stays isolated even for managers not listed in goblin.toml.
	// Configured managers below override these with their full settings.
	baseVars := map[string]string{"root": root, "path": abs, "bin": env.Bin, "name": cfg.Goblin.Name}
	for k, v := range catalog.Containment() {
		env.Vars[k] = catalog.Expand(v, baseVars)
	}

	for _, m := range cfg.Managers {
		spec, ok := catalog.Lookup(m.Kind)
		if !ok {
			return nil, fmt.Errorf("unknown manager kind %q in goblin.toml (known: %s)", m.Kind, strings.Join(catalog.Kinds(), ", "))
		}
		tv := env.templateVars(m)
		for k, v := range spec.Env {
			env.Vars[k] = catalog.Expand(v, tv)
		}
		for k, v := range m.Env {
			env.Vars[k] = catalog.Expand(v, tv)
		}
		for _, p := range spec.PathPrepend {
			p = catalog.Expand(p, tv)
			if !seenPath[p] {
				seenPath[p] = true
				env.PathPrepend = append(env.PathPrepend, p)
			}
		}
		if spec.Provisionable() && env.IsProvisioned(spec.Kind) {
			for k, v := range spec.Provision.Env {
				env.Vars[k] = catalog.Expand(v, tv)
			}
			for _, p := range spec.Provision.PathPrepend {
				p = catalog.Expand(p, tv)
				if !seenPath[p] {
					seenPath[p] = true
					env.PathPrepend = append(env.PathPrepend, p)
				}
			}
		}
	}
	// Global overrides come last so users can pin anything.
	gtv := map[string]string{"root": root, "path": abs, "bin": env.Bin, "name": cfg.Goblin.Name}
	for k, v := range cfg.Env {
		env.Vars[k] = catalog.Expand(v, gtv)
	}
	return env, nil
}

func (e *Environment) templateVars(m config.Manager) map[string]string {
	return map[string]string{
		"root": e.Root,
		"path": e.ManagerDir(m),
		"bin":  e.Bin,
		"name": e.Name,
	}
}

// TemplateVars exposes the placeholder values for a manager.
func (e *Environment) TemplateVars(m config.Manager) map[string]string {
	return e.templateVars(m)
}

// ProvisionMarker is the file recording that goblin installed kind itself.
func (e *Environment) ProvisionMarker(kind string) string {
	return filepath.Join(e.Root, "provisioned", kind)
}

// IsProvisioned reports whether goblin installed kind into the root.
func (e *Environment) IsProvisioned(kind string) bool {
	_, err := os.Stat(e.ProvisionMarker(kind))
	return err == nil
}

// ProvisionedVersion returns the version recorded at provisioning time.
func (e *Environment) ProvisionedVersion(kind string) string {
	data, err := os.ReadFile(e.ProvisionMarker(kind))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// LookPath finds an executable on the environment's PATH: goblin
// directories first, then the inherited PATH.
func (e *Environment) LookPath(name string) (string, bool) {
	for _, dir := range e.PathPrepend {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, true
		}
	}
	p, err := exec.LookPath(name)
	return p, err == nil
}

// HasBinary reports whether the manager's executable is usable inside
// the environment.
func (e *Environment) HasBinary(spec catalog.Spec) bool {
	if spec.Binary == "" {
		return true
	}
	_, ok := e.LookPath(spec.Binary)
	return ok
}

// ManagerDir returns the absolute directory a manager runs in.
func (e *Environment) ManagerDir(m config.Manager) string {
	return filepath.Join(e.Dir, filepath.FromSlash(m.Path))
}

// Path returns the PATH value with goblin directories prepended.
func (e *Environment) Path() string {
	parts := append([]string{}, e.PathPrepend...)
	if cur := os.Getenv("PATH"); cur != "" {
		parts = append(parts, cur)
	}
	return strings.Join(parts, string(os.PathListSeparator))
}

// SortedKeys returns the variable names in deterministic order.
func (e *Environment) SortedKeys() []string {
	keys := make([]string, 0, len(e.Vars))
	for k := range e.Vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Environ returns the process environment with goblin variables applied.
func (e *Environment) Environ() []string {
	out := []string{}
	for _, kv := range os.Environ() {
		k := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			k = kv[:i]
		}
		if k == "PATH" {
			continue
		}
		if _, override := e.Vars[k]; override {
			continue
		}
		out = append(out, kv)
	}
	for _, k := range e.SortedKeys() {
		out = append(out, k+"="+e.Vars[k])
	}
	out = append(out, "PATH="+e.Path())
	return out
}

// Command builds an exec.Cmd running a shell line inside dir with the
// goblin environment. stdio is inherited.
func (e *Environment) Command(dir, line string) *exec.Cmd {
	cmd := exec.Command("/bin/sh", "-c", line)
	cmd.Dir = dir
	cmd.Env = e.Environ()
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

// Exec builds an exec.Cmd for argv (no shell) inside dir.
func (e *Environment) Exec(dir string, argv []string) *exec.Cmd {
	name := argv[0]
	if !strings.Contains(name, string(os.PathSeparator)) {
		if p, ok := e.LookPath(name); ok {
			name = p
		}
	}
	cmd := exec.Command(name, argv[1:]...)
	cmd.Dir = dir
	cmd.Env = e.Environ()
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

// EnsureDirs creates the root layout and writes the env scripts.
func (e *Environment) EnsureDirs() error {
	dirs := []string{e.Root, e.Bin}
	for _, m := range e.Config.Managers {
		spec, ok := catalog.Lookup(m.Kind)
		if !ok {
			continue
		}
		tv := e.templateVars(m)
		for _, v := range spec.Env {
			v = catalog.Expand(v, tv)
			if strings.HasPrefix(v, e.Root+string(os.PathSeparator)) {
				dirs = append(dirs, v)
			}
		}
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	// Belt and braces: git honours nested ignore files, so the root can never
	// leak into a commit even if the top-level .gitignore is edited.
	marker := filepath.Join(e.Root, ".gitignore")
	if _, err := os.Stat(marker); os.IsNotExist(err) {
		if err := os.WriteFile(marker, []byte("# generated by goblin\n*\n"), 0o644); err != nil {
			return err
		}
	}
	return e.WriteScripts()
}

// WriteScripts generates env.sh and env.fish inside the root.
func (e *Environment) WriteScripts() error {
	if err := os.WriteFile(filepath.Join(e.Root, "env.sh"), []byte(e.ShellScript("sh")), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(e.Root, "env.fish"), []byte(e.ShellScript("fish")), 0o644)
}

// ShellScript renders the environment as a sourceable script.
// shell is "sh" (POSIX / bash / zsh) or "fish".
func (e *Environment) ShellScript(shell string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# goblin environment %q. Generated; do not edit.\n", e.Name)
	switch shell {
	case "fish":
		fmt.Fprintf(&b, "# usage: source %s\n", filepath.Join(e.Root, "env.fish"))
		for _, k := range e.SortedKeys() {
			fmt.Fprintf(&b, "set -gx %s %s\n", k, fishQuote(e.Vars[k]))
		}
		for i := len(e.PathPrepend) - 1; i >= 0; i-- {
			fmt.Fprintf(&b, "fish_add_path --global --prepend --move %s\n", fishQuote(e.PathPrepend[i]))
		}
	default:
		fmt.Fprintf(&b, "# usage: . %s\n", filepath.Join(e.Root, "env.sh"))
		for _, k := range e.SortedKeys() {
			fmt.Fprintf(&b, "export %s=%s\n", k, shQuote(e.Vars[k]))
		}
		quoted := make([]string, len(e.PathPrepend))
		for i, p := range e.PathPrepend {
			quoted[i] = shQuote(p)
		}
		fmt.Fprintf(&b, "export PATH=%s:\"$PATH\"\n", strings.Join(quoted, ":"))
	}
	return b.String()
}

func shQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !(r == '/' || r == '.' || r == '-' || r == '_' || r == '=' || r == ':' || r == ',' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func fishQuote(s string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), "'", `\'`) + "'"
}

// State records what the last build did so status can report it.
type State struct {
	BuiltAt       time.Time `toml:"built_at"`
	ManifestHash  string    `toml:"manifest_hash"`
	Managers      []string  `toml:"managers"`
	GoblinVersion string    `toml:"goblin_version"`
}

func (e *Environment) statePath() string { return filepath.Join(e.Root, "state.toml") }

// SaveState writes the build state file.
func (e *Environment) SaveState(s State) error {
	f, err := os.Create(e.statePath())
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(s)
}

// LoadState reads the build state; ok is false if no build happened yet.
func (e *Environment) LoadState() (State, bool) {
	var s State
	data, err := os.ReadFile(e.statePath())
	if err != nil {
		return s, false
	}
	if _, err := toml.Decode(string(data), &s); err != nil {
		return s, false
	}
	return s, true
}

// ManifestHash hashes goblin.toml so status can flag a stale build.
func (e *Environment) ManifestHash() string {
	f, err := os.Open(filepath.Join(e.Dir, config.FileName))
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Steps returns the install and build command lines for a manager, with
// manifest overrides applied and templates expanded.
func (e *Environment) Steps(m config.Manager) (install, build []string) {
	spec, ok := catalog.Lookup(m.Kind)
	if !ok {
		return nil, nil
	}
	tv := e.templateVars(m)
	install = spec.Install
	if m.Install != nil {
		install = m.Install
	}
	build = spec.Build
	if m.Build != nil {
		build = m.Build
	}
	expand := func(in []string) []string {
		out := make([]string, 0, len(in))
		for _, s := range in {
			if strings.TrimSpace(s) != "" {
				out = append(out, catalog.Expand(s, tv))
			}
		}
		return out
	}
	return expand(install), expand(build)
}

// Artifacts returns a manager's generated paths as project-relative
// ignore patterns.
func Artifacts(m config.Manager) []string {
	spec, ok := catalog.Lookup(m.Kind)
	if !ok {
		return nil
	}
	list := spec.Artifacts
	if m.Artifacts != nil {
		list = m.Artifacts
	}
	out := make([]string, 0, len(list))
	for _, a := range list {
		out = append(out, PrefixPattern(m.Path, a))
	}
	return out
}

// PrefixPattern scopes an ignore pattern to a sub-path. Patterns for the
// project root are left unanchored so they match at any depth (gitignore
// semantics), which also covers nested workspaces.
func PrefixPattern(path, pattern string) string {
	path = filepath.ToSlash(filepath.Clean(path))
	if path == "." || path == "" {
		return config.NormalizePattern(pattern)
	}
	return config.NormalizePattern(path + "/" + pattern)
}
