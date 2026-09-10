package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"goblin/internal/ui"
)

func runShell(args []string) error {
	fs := newFlags("shell", "[options]",
		"Starts $SHELL with the isolated environment applied. Exit to leave.",
		"$GOBLIN_ENV holds the environment name for prompts.")
	shell := fs.String("shell", "", "shell to start (default: $SHELL, then /bin/sh)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	p, err := loadProject()
	if err != nil {
		return err
	}
	if os.Getenv("GOBLIN_ENV") == p.Cfg.Goblin.Name && os.Getenv("GOBLIN_DIR") == p.Root {
		ui.Warn("already inside environment %s", p.Cfg.Goblin.Name)
	}
	if err := p.Env.EnsureDirs(); err != nil {
		return err
	}
	sh := *shell
	if sh == "" {
		sh = os.Getenv("SHELL")
	}
	if sh == "" {
		sh = "/bin/sh"
	}
	ui.Step("entering environment %s (%s); type `exit` to leave", ui.Bold(p.Cfg.Goblin.Name), filepath.Base(sh))
	ui.Info("the prompt is prefixed with %s while you are inside", ui.Bold("(goblin:"+p.Cfg.Goblin.Name+")"))
	launch := prepareShell(p.Env, sh)
	if launch.cleanup != nil {
		defer launch.cleanup()
	}
	cmd := p.Env.Exec(p.Root, launch.argv)
	cmd.Env = append(cmd.Env, "GOBLIN_SHELL=1")
	cmd.Env = append(cmd.Env, launch.extraEnv...)
	err = cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		os.Exit(ee.ExitCode())
	}
	return err
}

func runRun(args []string) error {
	fs := newFlags("run", "[options] [--] <command> [args...]",
		"Runs a command inside the isolated environment.",
		"With --in, runs from that manager path instead of the project root.")
	in := fs.String("in", "", "run from this project-relative directory")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	argv := fs.Args()
	if len(argv) == 0 {
		fs.Usage()
		return usage("run needs a command")
	}
	p, err := loadProject()
	if err != nil {
		return err
	}
	if err := p.Env.EnsureDirs(); err != nil {
		return err
	}
	dir := p.Root
	if *in != "" {
		dir = filepath.Join(p.Root, filepath.FromSlash(*in))
	}
	cmd := p.Env.Exec(dir, argv)
	err = cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		os.Exit(ee.ExitCode())
	}
	if err != nil {
		return fmt.Errorf("%s: %w", argv[0], err)
	}
	return nil
}

func runEnv(args []string) error {
	fs := newFlags("env", "[options]",
		"Prints the environment as shell statements.",
		`  eval "$(goblin env)"          # bash / zsh / sh`,
		`  goblin env --fish | source    # fish`)
	fish := fs.Bool("fish", false, "emit fish syntax")
	shellFlag := fs.String("shell", "", "sh or fish (default: from $SHELL)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	p, err := loadProject()
	if err != nil {
		return err
	}
	shell := "sh"
	if *fish || strings.HasSuffix(os.Getenv("SHELL"), "fish") {
		shell = "fish"
	}
	if *shellFlag != "" {
		shell = *shellFlag
	}
	fmt.Print(p.Env.ShellScript(shell))
	return nil
}
