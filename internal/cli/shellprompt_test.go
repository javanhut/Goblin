package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"myproj":    "myproj",
		`ev"il'`:    "evil",
		"a\nb`c\\d": "abcd",
		"":          "env",
	}
	for in, want := range cases {
		if got := sanitizeName(in); got != want {
			t.Errorf("sanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGenRavenInitFallback(t *testing.T) {
	t.Setenv("RAVEN_INIT_SCRIPT", "")
	out := genRavenInit("demo")
	for _, want := range []string{"fn prompt(status)", "(goblin:demo)", "$(cwd)", "\x1b[1;32m"} {
		if !strings.Contains(out, want) {
			t.Errorf("fallback init missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "__raven_detect_vcs") {
		t.Error("fallback should not call terminal helpers")
	}
}

func TestGenRavenInitReusesHelpers(t *testing.T) {
	dir := t.TempDir()
	prev := filepath.Join(dir, "init.rsh")
	body := "fn __raven_cwd() { return \"/x\" }\n" +
		"fn __raven_detect_lang() { return \"Go\" }\n" +
		"fn __raven_detect_vcs() { return \"Git\" }\n" +
		"fn prompt(status) { return \"old\" }\n"
	if err := os.WriteFile(prev, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RAVEN_INIT_SCRIPT", prev)
	out := genRavenInit("proj")
	for _, want := range []string{
		"begin inherited init",
		"__raven_cwd()",
		"__raven_detect_lang()",
		"__raven_detect_vcs()",
		"(goblin:proj)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("reuse init missing %q:\n%s", want, out)
		}
	}
	// Our prompt must be defined after the inherited one so it wins.
	if strings.LastIndex(out, "fn prompt(status)") < strings.Index(out, "end inherited init") {
		t.Error("goblin prompt must come after the inherited init block")
	}
}
