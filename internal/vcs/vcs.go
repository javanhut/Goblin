// Package vcs abstracts the version control systems goblin cooperates with:
// git and ivaldi. It owns the managed block inside the ignore file and
// knows how to stop tracking paths that became excluded.
package vcs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Kind identifies a VCS.
type Kind string

const (
	Git    Kind = "git"
	Ivaldi Kind = "ivaldi"
	None   Kind = "none"
)

const (
	blockStart = "# >>> goblin managed: edit goblin.toml and run `goblin sync` instead >>>"
	blockEnd   = "# <<< goblin managed <<<"
)

// IgnoreFile returns the ignore file name for the VCS ("" for none).
func (k Kind) IgnoreFile() string {
	switch k {
	case Git:
		return ".gitignore"
	case Ivaldi:
		return ".ivaldiignore"
	}
	return ""
}

// Available reports whether the VCS binary is installed.
func (k Kind) Available() bool {
	switch k {
	case Git, Ivaldi:
		_, err := exec.LookPath(string(k))
		return err == nil
	}
	return true
}

// Parse validates a user-supplied VCS name.
func Parse(s string) (Kind, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "git":
		return Git, nil
	case "ivaldi":
		return Ivaldi, nil
	case "none", "":
		return None, nil
	}
	return None, fmt.Errorf("unknown vcs %q (expected git, ivaldi or none)", s)
}

// Detect finds the VCS whose repository contains dir. When both exist the
// one rooted closest to dir wins; ties go to ivaldi since it is the more
// deliberate choice. found is false if dir is not inside any repository.
func Detect(dir string) (kind Kind, repoRoot string, found bool) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return None, "", false
	}
	cur := abs
	for {
		if IsRepoRoot(Ivaldi, cur) {
			return Ivaldi, cur, true
		}
		if IsRepoRoot(Git, cur) {
			return Git, cur, true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return None, "", false
		}
		cur = parent
	}
}

// IsRepoRoot reports whether dir itself is the top of a repository of kind.
// Ivaldi keeps its global configuration in ~/.ivaldi, which holds only a
// config file, so a repository is recognised by the HEAD file it also has.
func IsRepoRoot(kind Kind, dir string) bool {
	switch kind {
	case Git:
		return exists(filepath.Join(dir, ".git"))
	case Ivaldi:
		return exists(filepath.Join(dir, ".ivaldi", "HEAD"))
	}
	return false
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// Init creates a repository of kind in dir.
func Init(kind Kind, dir string) error {
	var cmd *exec.Cmd
	switch kind {
	case Git:
		cmd = exec.Command("git", "init", "-q")
	case Ivaldi:
		cmd = exec.Command("ivaldi", "forge", "-q")
	default:
		return nil
	}
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w\n%s", strings.Join(cmd.Args, " "), err, bytes.TrimSpace(out))
	}
	return nil
}

// IgnoreResult describes what WriteIgnore changed.
type IgnoreResult struct {
	File    string
	Added   []string
	Removed []string
	Created bool
}

// WriteIgnore replaces the goblin-managed block in the ignore file with
// patterns, preserving everything the user wrote outside the block.
func WriteIgnore(kind Kind, dir string, patterns []string) (IgnoreResult, error) {
	name := kind.IgnoreFile()
	if name == "" {
		return IgnoreResult{}, nil
	}
	path := filepath.Join(dir, name)
	res := IgnoreResult{File: name}

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return res, err
	}
	res.Created = os.IsNotExist(err)

	before, old, after := splitBlock(string(existing))
	oldSet := toSet(old)
	newSet := toSet(patterns)
	for _, p := range patterns {
		if !oldSet[p] {
			res.Added = append(res.Added, p)
		}
	}
	for _, p := range old {
		if !newSet[p] {
			res.Removed = append(res.Removed, p)
		}
	}

	var b strings.Builder
	b.WriteString(before)
	if before != "" && !strings.HasSuffix(before, "\n") {
		b.WriteString("\n")
	}
	if before != "" && !strings.HasSuffix(before, "\n\n") {
		b.WriteString("\n")
	}
	b.WriteString(blockStart + "\n")
	for _, p := range patterns {
		b.WriteString(p + "\n")
	}
	b.WriteString(blockEnd + "\n")
	if after != "" {
		if !strings.HasPrefix(after, "\n") {
			b.WriteString("\n")
		}
		b.WriteString(after)
	}
	content := b.String()
	if string(existing) == content {
		return res, nil
	}
	return res, os.WriteFile(path, []byte(content), 0o644)
}

// ManagedPatterns returns the patterns currently inside the managed block.
func ManagedPatterns(kind Kind, dir string) []string {
	name := kind.IgnoreFile()
	if name == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return nil
	}
	_, block, _ := splitBlock(string(data))
	return block
}

func splitBlock(content string) (before string, block []string, after string) {
	start := strings.Index(content, blockStart)
	if start < 0 {
		return content, nil, ""
	}
	end := strings.Index(content[start:], blockEnd)
	if end < 0 {
		// Corrupt block: keep everything before it, drop the rest of the block.
		return content[:start], nil, ""
	}
	end += start
	body := content[start+len(blockStart) : end]
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			block = append(block, line)
		}
	}
	after = content[end+len(blockEnd):]
	after = strings.TrimPrefix(after, "\n")
	before = strings.TrimRight(content[:start], "\n")
	if before != "" {
		before += "\n"
	}
	return before, block, after
}

func toSet(in []string) map[string]bool {
	m := make(map[string]bool, len(in))
	for _, s := range in {
		m[s] = true
	}
	return m
}

// Untrack stops tracking files that the ignore file now excludes. It
// returns the affected paths. Files stay on disk.
//
// git: tracked-but-ignored files are removed from the index.
// ivaldi: the ignore file alone is authoritative; ivaldi reports the
// files as deleted and drops them at the next seal, so this only lists them.
func Untrack(kind Kind, dir string) ([]string, error) {
	switch kind {
	case Git:
		return gitUntrack(dir)
	case Ivaldi:
		return ivaldiExcluded(dir)
	}
	return nil, nil
}

func gitUntrack(dir string) ([]string, error) {
	if !IsRepoRoot(Git, dir) {
		if _, _, ok := Detect(dir); !ok {
			return nil, nil
		}
	}
	ls := exec.Command("git", "ls-files", "-z", "--cached", "--ignored", "--exclude-standard", "--", ".")
	ls.Dir = dir
	out, err := ls.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("git ls-files: %s", bytes.TrimSpace(ee.Stderr))
		}
		return nil, err
	}
	var files []string
	for _, f := range bytes.Split(out, []byte{0}) {
		if len(f) > 0 {
			files = append(files, string(f))
		}
	}
	if len(files) == 0 {
		return nil, nil
	}
	// Batch to stay under argv limits.
	const batch = 500
	for i := 0; i < len(files); i += batch {
		j := i + batch
		if j > len(files) {
			j = len(files)
		}
		args := append([]string{"rm", "-q", "--cached", "--"}, files[i:j]...)
		rm := exec.Command("git", args...)
		rm.Dir = dir
		if out, err := rm.CombinedOutput(); err != nil {
			return files[:i], fmt.Errorf("git rm --cached: %s", bytes.TrimSpace(out))
		}
	}
	return files, nil
}

type ivaldiStatus struct {
	Files []struct {
		Path  string `json:"path"`
		State string `json:"state"`
	} `json:"files"`
}

func ivaldiExcluded(dir string) ([]string, error) {
	cmd := exec.Command("ivaldi", "status", "--json")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("ivaldi status: %s", bytes.TrimSpace(ee.Stderr))
		}
		return nil, err
	}
	var st ivaldiStatus
	if err := json.Unmarshal(out, &st); err != nil {
		return nil, fmt.Errorf("ivaldi status --json: %w", err)
	}
	var files []string
	for _, f := range st.Files {
		// A file that exists on disk but ivaldi now reports as deleted is one
		// the ignore file just pushed out of the tracked set.
		if f.State == "deleted" && exists(filepath.Join(dir, filepath.FromSlash(f.Path))) {
			files = append(files, f.Path)
		}
	}
	return files, nil
}
