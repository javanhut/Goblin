// Package cli implements the goblin subcommands.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"goblin/internal/config"
	"goblin/internal/envs"
	"goblin/internal/ui"
)

// Version is the goblin release string; overridden at link time via
// -X goblin/internal/cli.Version=<v> (see lazy.toml).
var Version = "0.1.0"

type command struct {
	name    string
	summary string
	run     func(args []string) error
}

var commands []command

func init() {
	commands = []command{
		{"init", "Create goblin.toml and the isolated environment (interactive)", runInit},
		{"build", "Rebuild the environment: fetch dependencies and run build steps", runBuild},
		{"sync", "Update the ignore file from goblin.toml and untrack excluded paths", runSync},
		{"exclude", "Add paths to the exclusion list and sync", runExclude},
		{"include", "Remove paths from the exclusion list and sync", runInclude},
		{"clean", "Delete excluded build artifacts and packages from disk", runClean},
		{"install", "Download missing package managers into the environment", runInstall},
		{"add", "Add a package manager to the environment", runAdd},
		{"remove", "Remove a package manager from the environment", runRemove},
		{"list", "List package managers goblin knows about", runList},
		{"shell", "Start a shell inside the environment", runShell},
		{"run", "Run a command inside the environment", runRun},
		{"env", "Print the environment as shell exports (eval \"$(goblin env)\")", runEnv},
		{"status", "Show the environment, managers and excluded paths", runStatus},
		{"version", "Print the goblin version", runVersion},
		{"help", "Show this help", runHelp},
	}
}

// Main dispatches to a subcommand and returns the process exit code.
func Main(args []string) int {
	if len(args) == 0 {
		runHelp(nil)
		return 0
	}
	name := args[0]
	switch name {
	case "-h", "--help":
		name = "help"
	case "-V", "--version":
		name = "version"
	}
	for _, c := range commands {
		if c.name == name {
			if err := c.run(args[1:]); err != nil {
				if errors.Is(err, flag.ErrHelp) {
					return 0
				}
				if errors.Is(err, ui.ErrAborted) {
					ui.Warn("aborted")
					return 130
				}
				var ue usageError
				if errors.As(err, &ue) {
					ui.Fail("%v", err)
					return 2
				}
				var be buildError
				if errors.As(err, &be) {
					return 1
				}
				ui.Fail("%v", err)
				return 1
			}
			return 0
		}
	}
	ui.Fail("unknown command %q", name)
	fmt.Fprintln(os.Stderr, "run `goblin help` for the list of commands")
	return 2
}

type usageError struct{ msg string }

func (u usageError) Error() string { return u.msg }

func usage(format string, a ...any) error { return usageError{fmt.Sprintf(format, a...)} }

// buildError signals that failures were already reported.
type buildError struct{}

func (buildError) Error() string { return "build failed" }

func runHelp(_ []string) error {
	fmt.Println(ui.Bold("goblin") + " " + Version + " — isolated, per-project package environments")
	fmt.Println()
	fmt.Println("Usage: goblin <command> [options]")
	fmt.Println()
	fmt.Println("Commands:")
	for _, c := range commands {
		fmt.Printf("  %-9s %s\n", c.name, c.summary)
	}
	fmt.Println()
	fmt.Println("Typical flow:")
	fmt.Println("  goblin init                      pick managers and a VCS, writes goblin.toml")
	fmt.Println("  goblin build                     download missing managers, fetch + build inside .goblin/")
	fmt.Println("  goblin sync                      write .gitignore/.ivaldiignore, untrack artifacts")
	fmt.Println("  goblin exclude target/ dist/     add more paths to the exclusion list")
	fmt.Println("  goblin shell                     work inside the environment")
	fmt.Println()
	fmt.Println("Run `goblin <command> -h` for command options.")
	return nil
}

func runVersion(_ []string) error {
	fmt.Println("goblin " + Version)
	return nil
}

// newFlags creates a flag set that prints goblin-style usage.
func newFlags(name, synopsis string, lines ...string) *flag.FlagSet {
	fs := flag.NewFlagSet("goblin "+name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {
		fmt.Printf("Usage: goblin %s %s\n", name, synopsis)
		for _, l := range lines {
			fmt.Println("  " + l)
		}
		var any bool
		fs.VisitAll(func(*flag.Flag) { any = true })
		if any {
			fmt.Println()
			fmt.Println("Options:")
			fs.SetOutput(os.Stdout)
			fs.PrintDefaults()
			fs.SetOutput(io.Discard)
		}
	}
	return fs
}

func parseFlags(fs *flag.FlagSet, args []string) error {
	err := fs.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		fs.Usage()
		return flag.ErrHelp
	}
	if err != nil {
		return usage("%v", err)
	}
	return nil
}

// project bundles everything a command needs about the current environment.
type project struct {
	Root string
	Cfg  *config.Config
	Env  *envs.Environment
}

// loadProject finds goblin.toml from the working directory upwards.
func loadProject() (*project, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	root, err := config.Find(cwd)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	env, err := envs.Resolve(root, cfg)
	if err != nil {
		return nil, err
	}
	return &project{Root: root, Cfg: cfg, Env: env}, nil
}

func (p *project) save() error {
	return config.Save(p.Root, p.Cfg)
}

// refresh re-resolves the environment after the manifest changed.
func (p *project) refresh() error {
	env, err := envs.Resolve(p.Root, p.Cfg)
	if err != nil {
		return err
	}
	p.Env = env
	return nil
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func joinOrNone(list []string) string {
	if len(list) == 0 {
		return ui.Dim("none")
	}
	return strings.Join(list, ", ")
}
