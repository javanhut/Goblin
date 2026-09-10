// Package ui holds terminal styling and the interactive init wizard.
package ui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"

	"goblin/internal/catalog"
	"goblin/internal/vcs"
)

var (
	accent  = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true)
	warnSty = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)
	failSty = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	dim     = lipgloss.NewStyle().Faint(true)
	bold    = lipgloss.NewStyle().Bold(true)
)

// Interactive reports whether stdin and stdout are terminals.
func Interactive() bool {
	return isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd())
}

// Bold renders s in bold.
func Bold(s string) string { return bold.Render(s) }

// Dim renders s faintly.
func Dim(s string) string { return dim.Render(s) }

// Step prints a headline for a phase of work.
func Step(format string, a ...any) {
	fmt.Println(accent.Render("▸ ") + fmt.Sprintf(format, a...))
}

// Info prints a neutral line.
func Info(format string, a ...any) {
	fmt.Println("  " + fmt.Sprintf(format, a...))
}

// Success prints a completed action.
func Success(format string, a ...any) {
	fmt.Println(accent.Render("✓ ") + fmt.Sprintf(format, a...))
}

// Warn prints a non-fatal problem.
func Warn(format string, a ...any) {
	fmt.Fprintln(os.Stderr, warnSty.Render("! ")+fmt.Sprintf(format, a...))
}

// Fail prints an error line.
func Fail(format string, a ...any) {
	fmt.Fprintln(os.Stderr, failSty.Render("✗ ")+fmt.Sprintf(format, a...))
}

// Selection is a manager the user chose in the wizard, with one or more paths.
type Selection struct {
	Kind  string
	Paths []string
}

// InitOptions seeds the wizard with detected defaults.
type InitOptions struct {
	Dir         string
	DefaultName string
	DetectedVCS vcs.Kind
	VCSFound    bool
	Detections  []catalog.Detection
}

// InitAnswers is what the wizard collected.
type InitAnswers struct {
	Name       string
	VCS        vcs.Kind
	Selections []Selection
	InitVCS    bool
	Build      bool
}

// ErrAborted is returned when the user leaves the wizard early.
var ErrAborted = errors.New("aborted")

// wizardState is shared by the terminal form and the plain-text fallback.
type wizardState struct {
	opts      InitOptions
	specs     []catalog.Spec
	name      string
	vcsChoice string
	kinds     []string
	paths     map[string]*string
	initVCS   bool
	build     bool
}

func newWizardState(opts InitOptions) *wizardState {
	detected := map[string][]string{}
	for _, d := range opts.Detections {
		detected[d.Kind] = append(detected[d.Kind], d.Path)
	}
	st := &wizardState{
		opts:      opts,
		specs:     catalog.All(),
		name:      opts.DefaultName,
		vcsChoice: string(defaultVCS(opts)),
		paths:     map[string]*string{},
		initVCS:   true,
		build:     true,
	}
	for k := range detected {
		st.kinds = append(st.kinds, k)
	}
	sort.Strings(st.kinds)
	for _, s := range st.specs {
		def := "."
		if p, ok := detected[s.Kind]; ok {
			def = strings.Join(p, ", ")
		}
		v := def
		st.paths[s.Kind] = &v
	}
	return st
}

func (st *wizardState) selected(kind string) bool {
	for _, k := range st.kinds {
		if k == kind {
			return true
		}
	}
	return false
}

func (st *wizardState) needVCSInit() bool {
	k, _ := vcs.Parse(st.vcsChoice)
	return k != vcs.None && !(st.opts.VCSFound && st.opts.DetectedVCS == k)
}

func (st *wizardState) summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "name: %s\nvcs:  %s\n", st.name, st.vcsChoice)
	if len(st.kinds) == 0 {
		b.WriteString("managers: none (add later with `goblin add <kind>`)\n")
	}
	for _, k := range st.kinds {
		fmt.Fprintf(&b, "  %-9s in %s\n", k, *st.paths[k])
	}
	return b.String()
}

func (st *wizardState) answers() InitAnswers {
	kind, _ := vcs.Parse(st.vcsChoice)
	ans := InitAnswers{
		Name:    strings.TrimSpace(st.name),
		VCS:     kind,
		InitVCS: st.initVCS && st.needVCSInit(),
		Build:   st.build,
	}
	sort.Strings(st.kinds)
	for _, k := range st.kinds {
		ans.Selections = append(ans.Selections, Selection{Kind: k, Paths: SplitPaths(*st.paths[k])})
	}
	return ans
}

func managerLabel(s catalog.Spec) string {
	label := fmt.Sprintf("%-9s %-11s %s", s.Kind, s.Language, s.Description)
	if !s.Available() {
		label += "  (" + s.Binary + " not installed)"
	}
	return label
}

func vcsLabel(opts InitOptions, k vcs.Kind) string {
	label := string(k)
	switch {
	case k == vcs.None:
		return "none      (no ignore file management)"
	case opts.VCSFound && opts.DetectedVCS == k:
		label += "  (repository detected)"
	case !k.Available():
		label += "  (not installed)"
	}
	return label
}

var vcsChoices = []vcs.Kind{vcs.Git, vcs.Ivaldi, vcs.None}

// RunInitWizard drives the interactive init flow. On a terminal it is a
// full-screen form; with piped stdin it falls back to line prompts.
func RunInitWizard(opts InitOptions) (InitAnswers, error) {
	st := newWizardState(opts)
	var err error
	if Interactive() {
		err = st.runForm()
	} else {
		err = st.runPlain(os.Stdin, os.Stdout)
	}
	if err != nil {
		return InitAnswers{}, err
	}
	return st.answers(), nil
}

// runForm renders the wizard as one navigable huh form; path groups for
// unselected managers are hidden dynamically.
func (st *wizardState) runForm() error {
	managerOpts := make([]huh.Option[string], 0, len(st.specs))
	for _, s := range st.specs {
		managerOpts = append(managerOpts, huh.NewOption(managerLabel(s), s.Kind).Selected(st.selected(s.Kind)))
	}
	vcsOpts := make([]huh.Option[string], 0, len(vcsChoices))
	for _, k := range vcsChoices {
		vcsOpts = append(vcsOpts, huh.NewOption(vcsLabel(st.opts, k), string(k)))
	}

	groups := []*huh.Group{
		huh.NewGroup(
			huh.NewNote().
				Title("Goblin").
				Description("Set up an isolated environment for this project.\nAnswers are written to goblin.toml so anyone can rebuild it with `goblin build`."),
			huh.NewInput().
				Title("Environment name").
				Description("Shown in $GOBLIN_ENV and the state file.").
				Value(&st.name).
				Validate(validateName),
			huh.NewSelect[string]().
				Title("Version control").
				Description("Goblin maintains the ignore file and untracks excluded paths.").
				Options(vcsOpts...).
				Value(&st.vcsChoice),
		),
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Package managers").
				Description("Space toggles, / filters, enter continues. Detected ones are pre-selected.").
				Options(managerOpts...).
				Height(14).
				Filterable(true).
				Value(&st.kinds),
		),
	}
	for _, s := range st.specs {
		s := s
		groups = append(groups, huh.NewGroup(
			huh.NewInput().
				Title(fmt.Sprintf("Where does %s run?", s.Name)).
				Description(fmt.Sprintf("Directory holding %s, relative to the project. Separate several with commas.", strings.Join(s.Detect, " / "))).
				Placeholder(".").
				Value(st.paths[s.Kind]).
				Validate(validatePaths(st.opts.Dir)),
		).WithHideFunc(func() bool { return !st.selected(s.Kind) }))
	}
	groups = append(groups,
		huh.NewGroup(
			huh.NewNote().Title("Summary").DescriptionFunc(st.summary, &st.kinds),
			huh.NewConfirm().
				TitleFunc(func() string { return fmt.Sprintf("Initialize a %s repository here?", st.vcsChoice) }, &st.vcsChoice).
				Description("No repository was found in this directory or above it.").
				Affirmative("Yes").Negative("No").
				Value(&st.initVCS),
		).WithHideFunc(func() bool { return !st.needVCSInit() }),
		huh.NewGroup(
			huh.NewNote().Title("Summary").DescriptionFunc(st.summary, &st.kinds),
			huh.NewConfirm().
				Title("Run `goblin build` now?").
				Description("Downloads dependencies into the isolated caches and runs each build step.").
				Affirmative("Yes").Negative("Later").
				Value(&st.build),
		),
	)
	err := huh.NewForm(groups...).WithTheme(huh.ThemeBase16()).Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return ErrAborted
	}
	return err
}

// runPlain asks the same questions as line prompts on a shared reader, so
// piped or scripted input works and EOF aborts instead of accepting defaults.
func (st *wizardState) runPlain(in io.Reader, out io.Writer) error {
	p := &prompter{r: bufio.NewReader(in), w: out}
	fmt.Fprintln(out, "Goblin: set up an isolated environment for this project.")
	fmt.Fprintln(out, "Answers are written to goblin.toml so anyone can rebuild it with `goblin build`.")
	fmt.Fprintln(out)

	var err error
	if st.name, err = p.line("Environment name", st.name, validateName); err != nil {
		return err
	}

	labels := make([]string, len(vcsChoices))
	def := 0
	for i, k := range vcsChoices {
		labels[i] = vcsLabel(st.opts, k)
		if string(k) == st.vcsChoice {
			def = i
		}
	}
	idx, err := p.pick("Version control", labels, def)
	if err != nil {
		return err
	}
	st.vcsChoice = string(vcsChoices[idx])

	if err := p.multi(st); err != nil {
		return err
	}

	for _, s := range st.specs {
		if !st.selected(s.Kind) {
			continue
		}
		title := fmt.Sprintf("Where does %s run? (dir holding %s; commas separate several)", s.Name, strings.Join(s.Detect, " / "))
		v, err := p.line(title, *st.paths[s.Kind], validatePaths(st.opts.Dir))
		if err != nil {
			return err
		}
		*st.paths[s.Kind] = v
	}

	fmt.Fprintln(out)
	fmt.Fprint(out, st.summary())
	if st.needVCSInit() {
		if st.initVCS, err = p.yesno(fmt.Sprintf("Initialize a %s repository here?", st.vcsChoice), true); err != nil {
			return err
		}
	}
	st.build, err = p.yesno("Run `goblin build` now?", true)
	return err
}

type prompter struct {
	r *bufio.Reader
	w io.Writer
}

// read returns one trimmed line; EOF with no pending input aborts.
func (p *prompter) read() (string, error) {
	s, err := p.r.ReadString('\n')
	if err != nil && s == "" {
		fmt.Fprintln(p.w)
		return "", ErrAborted
	}
	return strings.TrimSpace(s), nil
}

func (p *prompter) line(title, def string, validate func(string) error) (string, error) {
	for {
		if def != "" {
			fmt.Fprintf(p.w, "%s [%s]: ", title, def)
		} else {
			fmt.Fprintf(p.w, "%s: ", title)
		}
		s, err := p.read()
		if err != nil {
			return "", err
		}
		if s == "" {
			s = def
		}
		if validate != nil {
			if err := validate(s); err != nil {
				fmt.Fprintln(p.w, "  "+err.Error())
				continue
			}
		}
		return s, nil
	}
}

func (p *prompter) pick(title string, options []string, def int) (int, error) {
	fmt.Fprintln(p.w, title)
	for i, o := range options {
		fmt.Fprintf(p.w, "  %d. %s\n", i+1, o)
	}
	for {
		fmt.Fprintf(p.w, "Choose 1-%d [%d]: ", len(options), def+1)
		s, err := p.read()
		if err != nil {
			return 0, err
		}
		if s == "" {
			return def, nil
		}
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > len(options) {
			fmt.Fprintf(p.w, "  enter a number between 1 and %d\n", len(options))
			continue
		}
		return n - 1, nil
	}
}

// multi toggles managers by number or kind name until an empty line or 0.
func (p *prompter) multi(st *wizardState) error {
	fmt.Fprintln(p.w, "Package managers (detected ones are pre-selected)")
	show := func() {
		for i, s := range st.specs {
			mark := " "
			if st.selected(s.Kind) {
				mark = "✓"
			}
			fmt.Fprintf(p.w, "  %2d. %s %s\n", i+1, mark, managerLabel(s))
		}
	}
	show()
	for {
		fmt.Fprint(p.w, "Toggle by number or name, comma-separated; empty line or 0 confirms: ")
		s, err := p.read()
		if err != nil {
			return err
		}
		if s == "" || s == "0" {
			return nil
		}
		var bad []string
		for _, tok := range strings.Split(s, ",") {
			tok = strings.TrimSpace(tok)
			if tok == "" {
				continue
			}
			kind := ""
			if n, err := strconv.Atoi(tok); err == nil && n >= 1 && n <= len(st.specs) {
				kind = st.specs[n-1].Kind
			} else if spec, ok := catalog.Lookup(tok); ok {
				kind = spec.Kind
			}
			if kind == "" {
				bad = append(bad, tok)
				continue
			}
			if st.selected(kind) {
				var kept []string
				for _, k := range st.kinds {
					if k != kind {
						kept = append(kept, k)
					}
				}
				st.kinds = kept
			} else {
				st.kinds = append(st.kinds, kind)
			}
		}
		sort.Strings(st.kinds)
		if len(bad) > 0 {
			fmt.Fprintf(p.w, "  unknown: %s\n", strings.Join(bad, ", "))
		}
		show()
	}
}

func (p *prompter) yesno(title string, def bool) (bool, error) {
	hint := "Y/n"
	if !def {
		hint = "y/N"
	}
	for {
		fmt.Fprintf(p.w, "%s [%s]: ", title, hint)
		s, err := p.read()
		if err != nil {
			return false, err
		}
		switch strings.ToLower(s) {
		case "":
			return def, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		fmt.Fprintln(p.w, "  answer y or n")
	}
}

func defaultVCS(opts InitOptions) vcs.Kind {
	if opts.VCSFound {
		return opts.DetectedVCS
	}
	if vcs.Git.Available() {
		return vcs.Git
	}
	if vcs.Ivaldi.Available() {
		return vcs.Ivaldi
	}
	return vcs.None
}

func validateName(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("name cannot be empty")
	}
	return nil
}

// SplitPaths turns "a, b" into cleaned relative paths; empty means ".".
func SplitPaths(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		p = filepath.ToSlash(filepath.Clean(p))
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		out = []string{"."}
	}
	return out
}

func validatePaths(dir string) func(string) error {
	return func(s string) error {
		for _, p := range SplitPaths(s) {
			if filepath.IsAbs(p) || strings.HasPrefix(p, "..") {
				return fmt.Errorf("%q must be a relative path inside the project", p)
			}
			if st, err := os.Stat(filepath.Join(dir, p)); err != nil || !st.IsDir() {
				return fmt.Errorf("%q is not a directory", p)
			}
		}
		return nil
	}
}

// Confirm asks a yes/no question. Non-interactive sessions get def.
func Confirm(title, description string, def bool) (bool, error) {
	if !Interactive() {
		return def, nil
	}
	v := def
	err := huh.NewConfirm().Title(title).Description(description).Value(&v).
		WithTheme(huh.ThemeBase16()).Run()
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return false, ErrAborted
		}
		return false, err
	}
	return v, nil
}
