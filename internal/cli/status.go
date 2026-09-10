package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"goblin/internal/catalog"
	"goblin/internal/ui"
	"goblin/internal/vcs"
)

func runStatus(args []string) error {
	fs := newFlags("status", "", "Shows the environment, its managers, VCS state and excluded paths.")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	p, err := loadProject()
	if err != nil {
		return err
	}

	fmt.Printf("%s %s\n", ui.Bold("environment"), p.Cfg.Goblin.Name)
	fmt.Printf("%s %s\n", ui.Bold("project    "), p.Root)
	rootState := ui.Dim("missing, run `goblin build`")
	if st, err := os.Stat(p.Env.Root); err == nil && st.IsDir() {
		rootState = humanBytes(diskUsage(p.Env.Root))
	}
	fmt.Printf("%s %s (%s)\n", ui.Bold("root       "), p.Cfg.Goblin.Root, rootState)

	kind, _ := vcs.Parse(p.Cfg.Goblin.VCS)
	detected, repoRoot, found := vcs.Detect(p.Root)
	vcsLine := string(kind)
	switch {
	case kind == vcs.None && found:
		vcsLine += fmt.Sprintf(" (%s repository detected but unmanaged)", detected)
	case kind == vcs.None:
	case !found:
		vcsLine += " (no repository yet)"
	case detected != kind:
		vcsLine += fmt.Sprintf(" (mismatch: inside a %s repository)", detected)
	case repoRoot != p.Root:
		vcsLine += " (repository at " + repoRoot + ")"
	}
	fmt.Printf("%s %s\n", ui.Bold("vcs        "), vcsLine)

	if state, ok := p.Env.LoadState(); ok {
		stale := ""
		if state.ManifestHash != p.Env.ManifestHash() {
			stale = ui.Dim("  goblin.toml changed since; run `goblin build`")
		}
		fmt.Printf("%s %s ago%s\n", ui.Bold("last build "), time.Since(state.BuiltAt).Round(time.Second), stale)
	} else {
		fmt.Printf("%s %s\n", ui.Bold("last build "), ui.Dim("never"))
	}
	if inside := os.Getenv("GOBLIN_ENV"); inside != "" {
		fmt.Printf("%s inside environment %s\n", ui.Bold("shell      "), inside)
	}

	fmt.Println()
	fmt.Println(ui.Bold("managers"))
	if len(p.Cfg.Managers) == 0 {
		fmt.Println("  " + ui.Dim("none; `goblin add <kind>`"))
	}
	for _, m := range p.Cfg.Managers {
		spec, _ := catalog.Lookup(m.Kind)
		bin := "system"
		switch {
		case p.Env.IsProvisioned(m.Kind):
			bin = "goblin " + p.Env.ProvisionedVersion(m.Kind)
		case !p.Env.HasBinary(spec):
			bin = spec.Binary + " missing"
		}
		lock := ui.Dim("no lockfile")
		for _, l := range spec.Lockfiles {
			if _, err := os.Stat(filepath.Join(p.Env.ManagerDir(m), l)); err == nil {
				lock = l
				break
			}
		}
		fmt.Printf("  %-9s %-14s %-22s %s\n", m.Kind, m.Path, bin, lock)
	}

	fmt.Println()
	fmt.Println(ui.Bold("excluded"))
	if len(p.Cfg.Exclude.Paths) == 0 {
		fmt.Println("  " + ui.Dim("none"))
	}
	matches := p.matchExcluded()
	sizes := map[string]int64{}
	counts := map[string]int{}
	pats := compilePatterns(p.Cfg.Exclude.Paths)
	for _, abs := range matches {
		rel, _ := filepath.Rel(p.Root, abs)
		rel = filepath.ToSlash(rel)
		isDir := false
		if st, err := os.Stat(abs); err == nil {
			isDir = st.IsDir()
		}
		for i, pt := range pats {
			if pt.match(rel, filepath.Base(abs), isDir) {
				key := p.Cfg.Exclude.Paths[i]
				sizes[key] += diskUsage(abs)
				counts[key]++
				break
			}
		}
	}
	for _, x := range p.Cfg.Exclude.Paths {
		present := ui.Dim("absent")
		if c := counts[x]; c > 0 {
			present = fmt.Sprintf("%s in %s", humanBytes(sizes[x]), plural(c, "match"))
		}
		fmt.Printf("  %-28s %s\n", x, present)
	}

	if kind != vcs.None {
		managed := vcs.ManagedPatterns(kind, p.Root)
		want := p.ignorePatterns()
		if len(managed) != len(want) {
			fmt.Println()
			ui.Warn("%s is out of date; run `goblin sync`", kind.IgnoreFile())
		} else {
			for i := range want {
				if managed[i] != want[i] {
					fmt.Println()
					ui.Warn("%s is out of date; run `goblin sync`", kind.IgnoreFile())
					break
				}
			}
		}
	}
	return nil
}
