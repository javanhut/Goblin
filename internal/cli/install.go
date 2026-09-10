package cli

import (
	"errors"
	"fmt"
	"strings"

	"goblin/internal/catalog"
	"goblin/internal/provision"
	"goblin/internal/ui"
)

func runInstall(args []string) error {
	fs := newFlags("install", "[options] [kind...]",
		"Downloads package managers into .goblin/ from their official releases",
		"when the host does not provide them. Without arguments every configured",
		"manager that is missing is installed.")
	force := fs.Bool("force", false, "reinstall even if already provisioned")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	p, err := loadProject()
	if err != nil {
		return err
	}
	if err := p.Env.EnsureDirs(); err != nil {
		return err
	}
	kinds := fs.Args()
	if len(kinds) == 0 {
		seen := map[string]bool{}
		for _, m := range p.Cfg.Managers {
			spec, _ := catalog.Lookup(m.Kind)
			if !seen[m.Kind] && (!p.Env.HasBinary(spec) || p.Env.IsProvisioned(m.Kind)) {
				seen[m.Kind] = true
				kinds = append(kinds, m.Kind)
			}
		}
		if len(kinds) == 0 {
			ui.Success("every configured manager is already available")
			return nil
		}
	}
	var failed []string
	for _, k := range kinds {
		spec, ok := catalog.Lookup(k)
		if !ok {
			return usage("unknown manager %q (see `goblin list`)", k)
		}
		if err := p.provision(spec, *force); err != nil {
			ui.Fail("%s: %v", spec.Kind, err)
			failed = append(failed, spec.Kind)
		}
	}
	if len(failed) > 0 {
		return buildError{}
	}
	return nil
}

// provision installs spec into the environment and refreshes the resolved
// environment so its PATH and variables take effect immediately.
func (p *project) provision(spec catalog.Spec, force bool) error {
	if p.Env.IsProvisioned(spec.Kind) && !force {
		ui.Info("%s already provisioned (%s)", spec.Kind, p.Env.ProvisionedVersion(spec.Kind))
		return nil
	}
	if reason := provision.Reason(spec); reason != "" {
		return errors.New(reason)
	}
	ui.Step("installing %s into %s", spec.Name, p.Cfg.Goblin.Root)
	version, err := provision.Install(p.Env, spec, force, func(f string, a ...any) { ui.Info(f, a...) })
	if err != nil {
		return err
	}
	if err := p.refresh(); err != nil {
		return err
	}
	if err := p.Env.WriteScripts(); err != nil {
		return err
	}
	ui.Success("%s ready: %s", spec.Kind, version)
	return nil
}

// ensureManager makes spec usable, downloading it when allowed. It returns
// false with a printed explanation when the manager stays unavailable.
func (p *project) ensureManager(spec catalog.Spec, label string, download bool) bool {
	if p.Env.HasBinary(spec) {
		return true
	}
	if !download {
		ui.Warn("%s: %s is not installed (run `goblin install %s`); skipping", label, spec.Binary, spec.Kind)
		return false
	}
	if err := p.provision(spec, false); err != nil {
		if errors.Is(err, provision.ErrUnsupported) || !spec.Provisionable() {
			ui.Warn("%s: %s is not installed and %v; skipping", label, spec.Binary, strings.TrimPrefix(err.Error(), provision.ErrUnsupported.Error()+": "))
		} else {
			ui.Fail("%s: could not install %s: %v", label, spec.Name, err)
		}
		return false
	}
	return p.Env.HasBinary(spec)
}

func availabilityNote(spec catalog.Spec) string {
	if reason := provision.Reason(spec); reason != "" {
		return fmt.Sprintf("%s is not installed; %s", spec.Binary, reason)
	}
	return fmt.Sprintf("%s is not installed; `goblin install %s` downloads it into the environment", spec.Binary, spec.Kind)
}
