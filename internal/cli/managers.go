package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"goblin/internal/catalog"
	"goblin/internal/config"
	"goblin/internal/envs"
	"goblin/internal/ui"
)

func runAdd(args []string) error {
	fs := newFlags("add", "[options] <kind>...",
		"Adds package managers to goblin.toml, seeds their default artifacts into",
		"the exclusion list, regenerates env scripts and syncs the ignore file.",
		"Use kind=path to bind a manager to a subdirectory: goblin add npm=web")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return usage("add needs at least one manager kind (see `goblin list`)")
	}
	p, err := loadProject()
	if err != nil {
		return err
	}
	var added []string
	for _, arg := range fs.Args() {
		kind, path, _ := strings.Cut(arg, "=")
		spec, ok := catalog.Lookup(kind)
		if !ok {
			return usage("unknown manager %q (see `goblin list`)", kind)
		}
		if path == "" {
			path = "."
		}
		path = filepath.ToSlash(filepath.Clean(path))
		if filepath.IsAbs(path) || strings.HasPrefix(path, "..") {
			return usage("path %q must be relative to the project", path)
		}
		if st, err := os.Stat(filepath.Join(p.Root, path)); err != nil || !st.IsDir() {
			return usage("%q is not a directory in the project", path)
		}
		if p.Cfg.HasManager(spec.Kind, path) {
			ui.Info("%s already configured at %s", spec.Kind, path)
			continue
		}
		m := config.Manager{Kind: spec.Kind, Path: path}
		p.Cfg.Managers = append(p.Cfg.Managers, m)
		p.Cfg.AddExclude(envs.Artifacts(m)...)
		added = append(added, arg)
		if !spec.Available() {
			ui.Warn("%s is not installed (`%s` not on PATH); build will skip it until it is", spec.Name, spec.Binary)
		}
	}
	if len(added) == 0 {
		return nil
	}
	if err := p.save(); err != nil {
		return err
	}
	if err := p.refresh(); err != nil {
		return err
	}
	if err := p.Env.EnsureDirs(); err != nil {
		return err
	}
	ui.Success("added %s", strings.Join(added, ", "))
	return p.sync(syncOptions{quiet: true})
}

func runRemove(args []string) error {
	fs := newFlags("remove", "[options] <kind>[=path]...",
		"Removes package managers from goblin.toml. Their artifacts stay in the",
		"exclusion list; drop them with `goblin include` if wanted.")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return usage("remove needs at least one manager kind")
	}
	p, err := loadProject()
	if err != nil {
		return err
	}
	var removed []string
	for _, arg := range fs.Args() {
		kind, path, hasPath := strings.Cut(arg, "=")
		kind = strings.ToLower(strings.TrimSpace(kind))
		if hasPath {
			path = filepath.ToSlash(filepath.Clean(path))
		}
		var kept []config.Manager
		for _, m := range p.Cfg.Managers {
			if m.Kind == kind && (!hasPath || m.Path == path) {
				removed = append(removed, arg)
				continue
			}
			kept = append(kept, m)
		}
		if len(kept) == len(p.Cfg.Managers) {
			ui.Info("%s is not configured", arg)
		}
		p.Cfg.Managers = kept
	}
	if len(removed) == 0 {
		return nil
	}
	if err := p.save(); err != nil {
		return err
	}
	if err := p.refresh(); err != nil {
		return err
	}
	if err := p.Env.WriteScripts(); err != nil && !os.IsNotExist(err) {
		return err
	}
	ui.Success("removed %s", strings.Join(removed, ", "))
	return nil
}

func runList(args []string) error {
	fs := newFlags("list", "", "Lists every package manager goblin can isolate.")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	fmt.Printf("%-9s %-11s %-6s %s\n", ui.Bold("KIND"), ui.Bold("LANGUAGE"), ui.Bold("BIN"), ui.Bold("DESCRIPTION"))
	for _, s := range catalog.All() {
		avail := ui.Dim("no")
		if s.Available() {
			avail = "yes"
		}
		fmt.Printf("%-9s %-11s %-6s %s\n", s.Kind, s.Language, avail, s.Description)
	}
	return nil
}
