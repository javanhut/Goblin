package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"goblin/internal/catalog"
	"goblin/internal/vcs"
)

func testOpts(t *testing.T) InitOptions {
	dir := t.TempDir()
	for _, d := range []string{"api", "web"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return InitOptions{
		Dir:         dir,
		DefaultName: "demo",
		DetectedVCS: vcs.Git,
		VCSFound:    false,
		Detections:  []catalog.Detection{{Kind: "cargo", Path: "api"}},
	}
}

func TestPlainWizardFollowsInput(t *testing.T) {
	st := newWizardState(testOpts(t))
	// name, vcs=ivaldi, toggle npm on and cargo off then confirm, npm path,
	// don't init repo, don't build.
	in := "proj\n2\nnpm,cargo\n\nweb\nn\nn\n"
	var out strings.Builder
	if err := st.runPlain(strings.NewReader(in), &out); err != nil {
		t.Fatalf("runPlain: %v\n%s", err, out.String())
	}
	ans := st.answers()
	if ans.Name != "proj" || ans.VCS != vcs.Ivaldi || ans.InitVCS || ans.Build {
		t.Fatalf("answers = %+v", ans)
	}
	if len(ans.Selections) != 1 || ans.Selections[0].Kind != "npm" || ans.Selections[0].Paths[0] != "web" {
		t.Fatalf("selections = %+v", ans.Selections)
	}
}

func TestPlainWizardDefaults(t *testing.T) {
	st := newWizardState(testOpts(t))
	// Empty answers everywhere keep detected defaults.
	in := "\n\n\n\n\n\n"
	var out strings.Builder
	if err := st.runPlain(strings.NewReader(in), &out); err != nil {
		t.Fatalf("runPlain: %v", err)
	}
	ans := st.answers()
	if ans.Name != "demo" || ans.VCS != vcs.Git || !ans.InitVCS || !ans.Build {
		t.Fatalf("answers = %+v", ans)
	}
	if len(ans.Selections) != 1 || ans.Selections[0].Kind != "cargo" || ans.Selections[0].Paths[0] != "api" {
		t.Fatalf("selections = %+v", ans.Selections)
	}
}

func TestPlainWizardAbortsOnEOF(t *testing.T) {
	st := newWizardState(testOpts(t))
	var out strings.Builder
	if err := st.runPlain(strings.NewReader("proj\n1\n"), &out); err != ErrAborted {
		t.Fatalf("expected ErrAborted, got %v", err)
	}
	if err := st.runPlain(strings.NewReader(""), &out); err != ErrAborted {
		t.Fatalf("expected ErrAborted on empty input, got %v", err)
	}
}

func TestPlainWizardRepromptsOnBadInput(t *testing.T) {
	st := newWizardState(testOpts(t))
	in := "proj\n9\n1\nbogus\n\nnope\napi\nmaybe\ny\ny\n"
	var out strings.Builder
	if err := st.runPlain(strings.NewReader(in), &out); err != nil {
		t.Fatalf("runPlain: %v\n%s", err, out.String())
	}
	o := out.String()
	for _, want := range []string{"enter a number between 1 and 3", "unknown: bogus", `"nope" is not a directory`, "answer y or n"} {
		if !strings.Contains(o, want) {
			t.Errorf("missing reprompt %q in output:\n%s", want, o)
		}
	}
	ans := st.answers()
	if ans.VCS != vcs.Git || !ans.InitVCS || !ans.Build || ans.Selections[0].Paths[0] != "api" {
		t.Fatalf("answers = %+v", ans)
	}
}

func TestSplitPaths(t *testing.T) {
	got := SplitPaths(" ./api , web/, api ")
	if len(got) != 2 || got[0] != "api" || got[1] != "web" {
		t.Fatalf("SplitPaths = %v", got)
	}
	if got := SplitPaths(""); len(got) != 1 || got[0] != "." {
		t.Fatalf("SplitPaths(empty) = %v", got)
	}
}
