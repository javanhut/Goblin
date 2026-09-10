package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"goblin/internal/config"
	"goblin/internal/envs"
	"goblin/internal/ui"
	"goblin/internal/vcs"
)

type syncOptions struct {
	clean bool
	quiet bool
}

// ignorePatterns is the full list goblin manages in the ignore file.
func (p *project) ignorePatterns() []string {
	patterns := []string{config.NormalizePattern(p.Cfg.Goblin.Root + "/")}
	for _, x := range p.Cfg.Exclude.Paths {
		if x != patterns[0] {
			patterns = append(patterns, x)
		}
	}
	return patterns
}

// sync writes the ignore file, untracks excluded files and optionally
// deletes them from disk.
func (p *project) sync(o syncOptions) error {
	kind, _ := vcs.Parse(p.Cfg.Goblin.VCS)
	patterns := p.ignorePatterns()

	if kind == vcs.None {
		if detected, _, found := vcs.Detect(p.Root); found && !o.quiet {
			ui.Info("vcs is \"none\" in goblin.toml but a %s repository was found; set goblin.vcs to manage its ignore file", detected)
		}
	} else {
		res, err := vcs.WriteIgnore(kind, p.Root, patterns)
		if err != nil {
			return err
		}
		switch {
		case res.Created:
			ui.Success("created %s with %s", res.File, plural(len(patterns), "pattern"))
		case len(res.Added)+len(res.Removed) > 0:
			ui.Success("updated %s (+%d −%d)", res.File, len(res.Added), len(res.Removed))
		case !o.quiet:
			ui.Success("%s is up to date (%s)", res.File, plural(len(patterns), "pattern"))
		}
		if !o.quiet {
			for _, a := range res.Added {
				ui.Info("+ %s", a)
			}
			for _, r := range res.Removed {
				ui.Info("− %s", r)
			}
		}

		detected, _, found := vcs.Detect(p.Root)
		switch {
		case !found:
			if !o.quiet {
				ui.Info("no %s repository yet; nothing to untrack", kind)
			}
		case detected != kind:
			ui.Warn("goblin.toml says %s but this directory is inside a %s repository; not untracking", kind, detected)
		case !kind.Available():
			ui.Warn("%s is not installed; skipping untrack", kind)
		default:
			files, err := vcs.Untrack(kind, p.Root)
			if err != nil {
				return err
			}
			if len(files) > 0 {
				verb := "untracked"
				if kind == vcs.Ivaldi {
					verb = "dropped from the next seal:"
				}
				ui.Success("%s %s", verb, plural(len(files), "excluded file"))
				if o.quiet {
					files = nil
				}
				for i, f := range files {
					if i == 8 {
						ui.Info("… and %d more", len(files)-i)
						break
					}
					ui.Info("  %s", f)
				}
			}
		}
	}

	if o.clean {
		return p.clean(false)
	}
	return nil
}

func runSync(args []string) error {
	fs := newFlags("sync", "[options]",
		"Rewrites the goblin-managed block of .gitignore / .ivaldiignore from",
		"goblin.toml and removes newly excluded files from version control.")
	clean := fs.Bool("clean", false, "also delete excluded paths from disk")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usage("sync takes no positional arguments (use `goblin exclude <path>`)")
	}
	p, err := loadProject()
	if err != nil {
		return err
	}
	// Keep env scripts in step with the manifest when the root exists;
	// build creates it otherwise.
	if st, err := os.Stat(p.Env.Root); err == nil && st.IsDir() {
		if err := p.Env.WriteScripts(); err != nil {
			return err
		}
	}
	return p.sync(syncOptions{clean: *clean})
}

func runExclude(args []string) error {
	fs := newFlags("exclude", "[options] <path>...",
		"Adds paths (gitignore syntax; trailing / means directory) to [exclude].paths",
		"in goblin.toml, then syncs the ignore file and untracks matching files.")
	clean := fs.Bool("clean", false, "also delete the paths from disk")
	defaults := fs.Bool("defaults", false, "re-add the default artifacts of every configured manager")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() == 0 && !*defaults {
		fs.Usage()
		return usage("exclude needs at least one path")
	}
	p, err := loadProject()
	if err != nil {
		return err
	}
	paths := fs.Args()
	if *defaults {
		for _, m := range p.Cfg.Managers {
			paths = append(paths, envs.Artifacts(m)...)
		}
	}
	added := p.Cfg.AddExclude(paths...)
	if len(added) == 0 {
		ui.Info("nothing new to exclude")
	} else {
		if err := p.save(); err != nil {
			return err
		}
		ui.Success("excluded %s in %s: %s", plural(len(added), "path"), config.FileName, strings.Join(added, ", "))
	}
	return p.sync(syncOptions{clean: *clean})
}

func runInclude(args []string) error {
	fs := newFlags("include", "<path>...",
		"Removes paths from [exclude].paths in goblin.toml and syncs the ignore file.",
		"Files are not re-added to version control automatically.")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return usage("include needs at least one path")
	}
	p, err := loadProject()
	if err != nil {
		return err
	}
	removed := p.Cfg.RemoveExclude(fs.Args()...)
	if len(removed) == 0 {
		ui.Info("none of those paths were excluded")
	} else {
		if err := p.save(); err != nil {
			return err
		}
		ui.Success("included %s: %s", plural(len(removed), "path"), strings.Join(removed, ", "))
	}
	return p.sync(syncOptions{})
}

func runClean(args []string) error {
	fs := newFlags("clean", "[options]",
		"Deletes every path matched by [exclude].paths (build outputs, node_modules, …).",
		"Rebuild afterwards with `goblin build`.")
	yes := fs.Bool("y", false, "do not ask for confirmation")
	env := fs.Bool("env", false, "also delete the .goblin/ isolation directory (caches, toolchains)")
	dryRun := fs.Bool("dry-run", false, "list what would be deleted")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	p, err := loadProject()
	if err != nil {
		return err
	}
	matches := p.matchExcluded()
	if *env {
		matches = append(matches, p.Env.Root)
	}
	if len(matches) == 0 {
		ui.Info("nothing to clean")
		return nil
	}
	var total int64
	for _, m := range matches {
		total += diskUsage(m)
	}
	ui.Step("%s to delete (%s)", plural(len(matches), "path"), humanBytes(total))
	for _, m := range matches {
		rel, _ := filepath.Rel(p.Root, m)
		ui.Info("%s", rel)
	}
	if *dryRun {
		return nil
	}
	if !*yes {
		ok, err := ui.Confirm("Delete these paths?", "They are excluded artifacts and can be rebuilt with `goblin build`.", false)
		if err != nil {
			return err
		}
		if !ok {
			return ui.ErrAborted
		}
	}
	return p.clean(*env)
}

// clean removes matched artifacts from disk.
func (p *project) clean(includeEnv bool) error {
	matches := p.matchExcluded()
	if includeEnv {
		matches = append(matches, p.Env.Root)
	}
	if len(matches) == 0 {
		ui.Info("nothing to clean")
		return nil
	}
	var total int64
	for _, m := range matches {
		total += diskUsage(m)
		if err := os.RemoveAll(m); err != nil {
			return fmt.Errorf("remove %s: %w", m, err)
		}
	}
	ui.Success("deleted %s, freed %s", plural(len(matches), "path"), humanBytes(total))
	return nil
}

// matchExcluded walks the project and returns absolute paths matched by
// the exclusion patterns. The goblin root and VCS directories are skipped.
func (p *project) matchExcluded() []string {
	pats := compilePatterns(p.Cfg.Exclude.Paths)
	var out []string
	_ = filepath.WalkDir(p.Root, func(path string, d os.DirEntry, err error) error {
		if err != nil || path == p.Root {
			return nil
		}
		if path == p.Env.Root {
			return filepath.SkipDir
		}
		name := d.Name()
		if d.IsDir() && (name == ".git" || name == ".ivaldi") {
			return filepath.SkipDir
		}
		rel, _ := filepath.Rel(p.Root, path)
		rel = filepath.ToSlash(rel)
		for _, pat := range pats {
			if pat.match(rel, name, d.IsDir()) {
				out = append(out, path)
				if d.IsDir() {
					return filepath.SkipDir
				}
				break
			}
		}
		return nil
	})
	return out
}

type pattern struct {
	glob     string
	dirOnly  bool
	anchored bool
}

// compilePatterns implements the useful subset of gitignore syntax:
// trailing "/" (directories only), leading "/" or an inner "/" (anchored
// to the project root), "**/" prefix (any depth) and shell globs.
func compilePatterns(in []string) []pattern {
	var out []pattern
	for _, raw := range in {
		if raw == "" || strings.HasPrefix(raw, "!") || strings.HasPrefix(raw, "#") {
			continue
		}
		pt := pattern{}
		s := raw
		if strings.HasSuffix(s, "/") {
			pt.dirOnly = true
			s = strings.TrimSuffix(s, "/")
		}
		s = strings.TrimPrefix(s, "**/")
		if strings.HasPrefix(s, "/") {
			pt.anchored = true
			s = strings.TrimPrefix(s, "/")
		} else if strings.Contains(s, "/") {
			pt.anchored = true
		}
		pt.glob = s
		out = append(out, pt)
	}
	return out
}

func (pt pattern) match(rel, base string, isDir bool) bool {
	if pt.dirOnly && !isDir {
		return false
	}
	subject := base
	if pt.anchored {
		subject = rel
	}
	ok, err := filepath.Match(pt.glob, subject)
	return err == nil && ok
}

func diskUsage(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if info, err := d.Info(); err == nil && !d.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
