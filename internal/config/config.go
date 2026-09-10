// Package config defines the goblin.toml schema and load/save helpers.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// FileName is the manifest file that describes a goblin environment.
const FileName = "goblin.toml"

// SchemaVersion is bumped when the manifest format changes incompatibly.
const SchemaVersion = 1

// Config is the on-disk representation of goblin.toml.
type Config struct {
	Goblin   Goblin            `toml:"goblin"`
	Managers []Manager         `toml:"manager"`
	Exclude  Exclude           `toml:"exclude"`
	Env      map[string]string `toml:"env,omitempty"`
}

// Goblin holds environment-wide settings.
type Goblin struct {
	Name    string `toml:"name"`
	Version int    `toml:"version"`
	// VCS is one of "git", "ivaldi" or "none".
	VCS string `toml:"vcs"`
	// Root is the directory (relative to the manifest) that holds the
	// isolated caches, toolchain homes and generated env scripts.
	Root string `toml:"root"`
}

// Manager is one package manager instance bound to a path inside the project.
// A monorepo may declare the same kind several times with different paths.
type Manager struct {
	Kind string `toml:"kind"`
	// Path is the directory the manager operates in, relative to the manifest.
	Path string `toml:"path"`
	// Install overrides the catalog's dependency-install commands.
	Install []string `toml:"install,omitempty"`
	// Build overrides the catalog's build commands.
	Build []string `toml:"build,omitempty"`
	// Artifacts overrides the catalog's list of generated paths to exclude.
	Artifacts []string `toml:"artifacts,omitempty"`
	// Env adds manager-specific variables on top of the catalog defaults.
	Env map[string]string `toml:"env,omitempty"`
}

// Exclude lists paths that are ignored by the VCS and removed on sync.
type Exclude struct {
	Paths []string `toml:"paths"`
}

// Default returns a fresh config for a project called name.
func Default(name, vcs string) *Config {
	return &Config{
		Goblin: Goblin{
			Name:    name,
			Version: SchemaVersion,
			VCS:     vcs,
			Root:    ".goblin",
		},
		Exclude: Exclude{Paths: []string{}},
	}
}

// ErrNotFound is returned when no manifest exists in the directory tree.
var ErrNotFound = errors.New("no goblin.toml found (run `goblin init` first)")

// Find walks from dir upwards looking for goblin.toml and returns the
// directory containing it.
func Find(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if st, err := os.Stat(filepath.Join(abs, FileName)); err == nil && !st.IsDir() {
			return abs, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", ErrNotFound
		}
		abs = parent
	}
}

// Load reads and validates the manifest in root.
func Load(root string) (*Config, error) {
	path := filepath.Join(root, FileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var c Config
	md, err := toml.Decode(string(data), &c)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, k := range undecoded {
			keys = append(keys, k.String())
		}
		return nil, fmt.Errorf("parse %s: unknown keys: %s", path, strings.Join(keys, ", "))
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	c.normalize()
	return &c, nil
}

// Save writes the manifest to root atomically.
func Save(root string, c *Config) error {
	c.normalize()
	var buf bytes.Buffer
	buf.WriteString("# Goblin environment manifest.\n")
	buf.WriteString("# Rebuild this environment anywhere with `goblin build`.\n")
	buf.WriteString("# Docs: goblin help\n\n")
	enc := toml.NewEncoder(&buf)
	enc.Indent = ""
	if err := enc.Encode(c); err != nil {
		return err
	}
	path := filepath.Join(root, FileName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Validate checks the manifest for structural problems.
func (c *Config) Validate() error {
	if c.Goblin.Version > SchemaVersion {
		return fmt.Errorf("manifest schema version %d is newer than this goblin (%d)", c.Goblin.Version, SchemaVersion)
	}
	switch c.Goblin.VCS {
	case "", "git", "ivaldi", "none":
	default:
		return fmt.Errorf("unknown vcs %q (expected git, ivaldi or none)", c.Goblin.VCS)
	}
	if strings.Contains(c.Goblin.Root, "..") || filepath.IsAbs(c.Goblin.Root) {
		return fmt.Errorf("goblin.root must be a relative path inside the project, got %q", c.Goblin.Root)
	}
	for i, m := range c.Managers {
		if m.Kind == "" {
			return fmt.Errorf("manager #%d has no kind", i+1)
		}
		if filepath.IsAbs(m.Path) || strings.HasPrefix(filepath.Clean(m.Path), "..") {
			return fmt.Errorf("manager %s: path %q must stay inside the project", m.Kind, m.Path)
		}
	}
	for _, p := range c.Exclude.Paths {
		// A leading "/" anchors a gitignore pattern to the project root; it
		// is not an absolute path. Escaping the project is what we forbid.
		for _, seg := range strings.Split(strings.TrimPrefix(p, "/"), "/") {
			if seg == ".." {
				return fmt.Errorf("exclude path %q must stay inside the project", p)
			}
		}
	}
	return nil
}

func (c *Config) normalize() {
	if c.Goblin.Version == 0 {
		c.Goblin.Version = SchemaVersion
	}
	if c.Goblin.Root == "" {
		c.Goblin.Root = ".goblin"
	}
	if c.Goblin.VCS == "" {
		c.Goblin.VCS = "none"
	}
	for i := range c.Managers {
		if c.Managers[i].Path == "" {
			c.Managers[i].Path = "."
		}
		c.Managers[i].Path = filepath.ToSlash(filepath.Clean(c.Managers[i].Path))
	}
	if c.Exclude.Paths == nil {
		c.Exclude.Paths = []string{}
	}
	c.Exclude.Paths = dedupe(c.Exclude.Paths)
}

// AddExclude appends paths to the exclusion list. Returns the ones that were new.
func (c *Config) AddExclude(paths ...string) []string {
	var added []string
	seen := make(map[string]bool, len(c.Exclude.Paths))
	for _, p := range c.Exclude.Paths {
		seen[p] = true
	}
	for _, p := range paths {
		p = NormalizePattern(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		c.Exclude.Paths = append(c.Exclude.Paths, p)
		added = append(added, p)
	}
	return added
}

// RemoveExclude drops paths from the exclusion list. Returns the ones removed.
func (c *Config) RemoveExclude(paths ...string) []string {
	drop := make(map[string]bool, len(paths))
	for _, p := range paths {
		drop[NormalizePattern(p)] = true
	}
	var kept, removed []string
	for _, p := range c.Exclude.Paths {
		if drop[p] {
			removed = append(removed, p)
		} else {
			kept = append(kept, p)
		}
	}
	c.Exclude.Paths = kept
	return removed
}

// HasManager reports whether a manager of kind exists at path.
func (c *Config) HasManager(kind, path string) bool {
	path = filepath.ToSlash(filepath.Clean(path))
	for _, m := range c.Managers {
		if m.Kind == kind && filepath.ToSlash(filepath.Clean(m.Path)) == path {
			return true
		}
	}
	return false
}

// NormalizePattern cleans an ignore pattern while preserving a trailing
// slash, which marks directories in gitignore syntax.
func NormalizePattern(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	dir := strings.HasSuffix(p, "/")
	p = strings.TrimPrefix(p, "./")
	p = filepath.ToSlash(filepath.Clean(p))
	if p == "." || p == "" {
		return ""
	}
	if dir {
		p += "/"
	}
	return p
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = NormalizePattern(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// SortedEnv returns the extra env keys in deterministic order.
func (c *Config) SortedEnv() []string {
	keys := make([]string, 0, len(c.Env))
	for k := range c.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
