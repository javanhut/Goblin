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
	"goblin/internal/vcs"
)

func runInit(args []string) error {
	fs := newFlags("init", "[options]",
		"Creates goblin.toml, the .goblin/ isolation directory and the ignore file.",
		"Without options an interactive wizard asks for everything.")
	name := fs.String("name", "", "environment name (default: directory name)")
	vcsFlag := fs.String("vcs", "", "version control: git, ivaldi or none (default: detected)")
	with := fs.String("with", "", "managers to enable, non-interactive: cargo,npm=web,uv=py")
	yes := fs.Bool("y", false, "non-interactive: accept detected managers and VCS")
	noVCSInit := fs.Bool("no-vcs-init", false, "do not create a repository when none exists")
	build := fs.Bool("build", false, "run `goblin build` after init (non-interactive modes)")
	force := fs.Bool("force", false, "overwrite an existing goblin.toml")
	noDownload := fs.Bool("no-download", false, "do not download missing package managers")
	dir := fs.String("dir", ".", "project directory")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usage("init takes no positional arguments")
	}

	root, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return fmt.Errorf("%s is not a directory", root)
	}
	if _, err := os.Stat(filepath.Join(root, config.FileName)); err == nil && !*force {
		return fmt.Errorf("%s already exists in %s (use --force to overwrite)", config.FileName, root)
	}

	detectedVCS, repoRoot, vcsFound := vcs.Detect(root)
	detections := catalog.Scan(root)
	defaultName := *name
	if defaultName == "" {
		defaultName = filepath.Base(root)
	}

	var ans ui.InitAnswers
	switch {
	case *with != "" || *yes:
		ans, err = answersFromFlags(defaultName, *vcsFlag, *with, *yes, detectedVCS, vcsFound, detections)
		if err != nil {
			return err
		}
		ans.InitVCS = !*noVCSInit
		ans.Build = *build
	default:
		ans, err = ui.RunInitWizard(ui.InitOptions{
			Dir:         root,
			DefaultName: defaultName,
			DetectedVCS: detectedVCS,
			VCSFound:    vcsFound,
			Detections:  detections,
		})
		if err != nil {
			return err
		}
		if *vcsFlag != "" {
			if ans.VCS, err = vcs.Parse(*vcsFlag); err != nil {
				return usage("%v", err)
			}
		}
		if *noVCSInit {
			ans.InitVCS = false
		}
		if *build {
			ans.Build = true
		}
	}

	cfg := config.Default(ans.Name, string(ans.VCS))
	for _, sel := range ans.Selections {
		spec, ok := catalog.Lookup(sel.Kind)
		if !ok {
			return usage("unknown manager %q (see `goblin list`)", sel.Kind)
		}
		for _, p := range sel.Paths {
			m := config.Manager{Kind: spec.Kind, Path: p}
			cfg.Managers = append(cfg.Managers, m)
			cfg.AddExclude(envs.Artifacts(m)...)
		}
	}

	ui.Step("writing %s", config.FileName)
	if err := config.Save(root, cfg); err != nil {
		return err
	}
	env, err := envs.Resolve(root, cfg)
	if err != nil {
		return err
	}
	ui.Step("creating %s", cfg.Goblin.Root)
	if err := env.EnsureDirs(); err != nil {
		return err
	}

	if ans.VCS != vcs.None {
		sameRepo := vcsFound && detectedVCS == ans.VCS
		switch {
		case sameRepo && repoRoot != root:
			ui.Info("using the %s repository at %s", ans.VCS, repoRoot)
		case sameRepo:
			ui.Info("using the existing %s repository", ans.VCS)
		case ans.InitVCS:
			if !ans.VCS.Available() {
				ui.Warn("%s is not installed; skipping repository creation", ans.VCS)
			} else {
				ui.Step("initializing %s repository", ans.VCS)
				if err := vcs.Init(ans.VCS, root); err != nil {
					return err
				}
			}
		default:
			ui.Info("no %s repository here; ignore file will still be written", ans.VCS)
		}
	}

	p := &project{Root: root, Cfg: cfg, Env: env}
	if err := p.sync(syncOptions{}); err != nil {
		return err
	}

	fmt.Println()
	ui.Success("environment %s ready", ui.Bold(cfg.Goblin.Name))
	if len(cfg.Managers) == 0 {
		ui.Info("no managers enabled yet: `goblin add <kind>`")
	} else {
		names := make([]string, 0, len(cfg.Managers))
		for _, m := range cfg.Managers {
			if m.Path == "." {
				names = append(names, m.Kind)
			} else {
				names = append(names, m.Kind+" ("+m.Path+")")
			}
		}
		ui.Info("managers: %s", strings.Join(names, ", "))
	}
	ui.Info("excluded:  %s", joinOrNone(cfg.Exclude.Paths))

	seen := map[string]bool{}
	var unavailable []string
	for _, m := range cfg.Managers {
		spec, _ := catalog.Lookup(m.Kind)
		if seen[m.Kind] || p.Env.HasBinary(spec) {
			continue
		}
		seen[m.Kind] = true
		if *noDownload {
			ui.Warn("%s", availabilityNote(spec))
			continue
		}
		fmt.Println()
		if !p.ensureManager(spec, spec.Kind, true) {
			unavailable = append(unavailable, spec.Kind)
		}
	}
	if len(unavailable) > 0 {
		ui.Warn("not available yet: %s (build will skip them)", strings.Join(unavailable, ", "))
	}

	if ans.Build {
		fmt.Println()
		if err := os.Chdir(root); err != nil {
			return err
		}
		return runBuild(nil)
	}
	ui.Info("next: `goblin build`, then `goblin shell`")
	return nil
}

// answersFromFlags builds wizard answers for non-interactive init.
func answersFromFlags(name, vcsFlag, with string, useDetected bool, detected vcs.Kind, vcsFound bool, detections []catalog.Detection) (ui.InitAnswers, error) {
	ans := ui.InitAnswers{Name: name}
	var err error
	switch {
	case vcsFlag != "":
		if ans.VCS, err = vcs.Parse(vcsFlag); err != nil {
			return ans, usage("%v", err)
		}
	case vcsFound:
		ans.VCS = detected
	case vcs.Git.Available():
		ans.VCS = vcs.Git
	case vcs.Ivaldi.Available():
		ans.VCS = vcs.Ivaldi
	default:
		ans.VCS = vcs.None
	}

	if with != "" {
		byKind := map[string]*ui.Selection{}
		var order []string
		for _, item := range strings.Split(with, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			kind, path, _ := strings.Cut(item, "=")
			kind = strings.ToLower(strings.TrimSpace(kind))
			if _, ok := catalog.Lookup(kind); !ok {
				return ans, usage("unknown manager %q in --with (see `goblin list`)", kind)
			}
			if path == "" {
				path = "."
			}
			sel, ok := byKind[kind]
			if !ok {
				sel = &ui.Selection{Kind: kind}
				byKind[kind] = sel
				order = append(order, kind)
			}
			sel.Paths = append(sel.Paths, ui.SplitPaths(path)...)
		}
		for _, k := range order {
			ans.Selections = append(ans.Selections, *byKind[k])
		}
		return ans, nil
	}

	if useDetected {
		byKind := map[string]*ui.Selection{}
		var order []string
		for _, d := range detections {
			sel, ok := byKind[d.Kind]
			if !ok {
				sel = &ui.Selection{Kind: d.Kind}
				byKind[d.Kind] = sel
				order = append(order, d.Kind)
			}
			sel.Paths = append(sel.Paths, d.Path)
		}
		for _, k := range order {
			ans.Selections = append(ans.Selections, *byKind[k])
		}
	}
	return ans, nil
}
