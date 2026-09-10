package cli

import (
	"fmt"
	"strings"
	"time"

	"goblin/internal/catalog"
	"goblin/internal/config"
	"goblin/internal/envs"
	"goblin/internal/ui"
)

func runBuild(args []string) error {
	fs := newFlags("build", "[options]",
		"Recreates .goblin/, regenerates env scripts, then for every manager in",
		"goblin.toml runs its install step and build step inside the isolated environment.")
	only := fs.String("only", "", "comma-separated manager kinds to build (default: all)")
	installOnly := fs.Bool("install-only", false, "fetch dependencies but skip build steps")
	dryRun := fs.Bool("dry-run", false, "print the commands without running them")
	noSync := fs.Bool("no-sync", false, "skip updating the ignore file afterwards")
	noDownload := fs.Bool("no-download", false, "do not download missing package managers")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usage("build takes no positional arguments")
	}

	p, err := loadProject()
	if err != nil {
		return err
	}
	filter := map[string]bool{}
	for _, k := range strings.Split(*only, ",") {
		if k = strings.TrimSpace(k); k != "" {
			filter[k] = true
		}
	}

	ui.Step("environment %s at %s", ui.Bold(p.Cfg.Goblin.Name), p.Env.Root)
	if !*dryRun {
		if err := p.Env.EnsureDirs(); err != nil {
			return err
		}
	}

	var built []string
	var failed []string
	start := time.Now()
	for _, m := range p.Cfg.Managers {
		if len(filter) > 0 && !filter[m.Kind] {
			continue
		}
		label := m.Kind
		if m.Path != "." {
			label += " (" + m.Path + ")"
		}
		spec, _ := catalog.Lookup(m.Kind)
		if !*dryRun && !p.ensureManager(spec, label, !*noDownload) {
			failed = append(failed, label)
			continue
		}
		if !spec.DetectedIn(p.Env.ManagerDir(m)) {
			ui.Info("%s: no %s yet; tool is ready, create the project inside `goblin shell`", label, strings.Join(spec.Detect, " / "))
			built = append(built, label)
			continue
		}
		install, build := p.Env.Steps(m)
		steps := install
		if !*installOnly {
			steps = append(steps, build...)
		}
		if len(steps) == 0 {
			ui.Info("%s: nothing to run", label)
			built = append(built, label)
			continue
		}
		fmt.Println()
		ui.Step("%s", label)
		ok := true
		for _, line := range steps {
			fmt.Println(ui.Dim("  $ " + line))
			if *dryRun {
				continue
			}
			if err := runStep(p, m, line); err != nil {
				ui.Fail("%s: %v", label, err)
				ok = false
				break
			}
		}
		if ok {
			built = append(built, label)
		} else {
			failed = append(failed, label)
		}
	}

	if !*dryRun {
		state := envs.State{
			BuiltAt:       time.Now(),
			ManifestHash:  p.Env.ManifestHash(),
			Managers:      built,
			GoblinVersion: Version,
		}
		if err := p.Env.SaveState(state); err != nil {
			return err
		}
		if !*noSync {
			fmt.Println()
			if err := p.sync(syncOptions{quiet: true}); err != nil {
				ui.Warn("sync: %v", err)
			}
		}
	}

	fmt.Println()
	if len(failed) > 0 {
		ui.Fail("%s failed: %s", plural(len(failed), "manager"), strings.Join(failed, ", "))
		if len(built) > 0 {
			ui.Info("built: %s", strings.Join(built, ", "))
		}
		return buildError{}
	}
	if len(p.Cfg.Managers) == 0 {
		ui.Success("environment ready (no managers configured; `goblin add <kind>`)")
		return nil
	}
	ui.Success("built %s in %s", plural(len(built), "manager"), time.Since(start).Round(time.Millisecond))
	ui.Info("enter it with `goblin shell` or `eval \"$(goblin env)\"`")
	return nil
}

func runStep(p *project, m config.Manager, line string) error {
	cmd := p.Env.Command(p.Env.ManagerDir(m), line)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("`%s` %v", line, err)
	}
	return nil
}
