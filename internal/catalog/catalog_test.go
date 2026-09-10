package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanResolvesOverlapsAndSubdirs(t *testing.T) {
	dir := t.TempDir()
	touch := func(rel string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	touch("Cargo.toml")
	touch("web/package.json")
	touch("web/pnpm-lock.yaml")
	touch("py/pyproject.toml")
	touch("py/poetry.lock")
	touch("node_modules/pkg/package.json") // must be skipped
	touch(".hidden/go.mod")                // must be skipped
	touch("dotnet/App.csproj")

	got := map[string]string{}
	for _, d := range Scan(dir) {
		got[d.Kind+"@"+d.Path] = d.Path
	}
	for _, want := range []string{"cargo@.", "pnpm@web", "poetry@py", "dotnet@dotnet"} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing detection %s in %v", want, got)
		}
	}
	for _, bad := range []string{"npm@web", "uv@py", "pip@py", "npm@node_modules", "go@.hidden"} {
		if _, ok := got[bad]; ok {
			t.Errorf("unexpected detection %s", bad)
		}
	}
}

func TestLookupAndExpand(t *testing.T) {
	if _, ok := Lookup("CARGO "); !ok {
		t.Fatal("lookup should be case-insensitive and trimmed")
	}
	if _, ok := Lookup("nope"); ok {
		t.Fatal("unknown kind matched")
	}
	got := Expand("{root}/x:{path}/y:{bin}:{name}", map[string]string{"root": "/r", "path": "/p", "bin": "/b", "name": "n"})
	if got != "/r/x:/p/y:/b:n" {
		t.Fatalf("Expand = %q", got)
	}
	seen := map[string]bool{}
	for _, s := range All() {
		if seen[s.Kind] {
			t.Fatalf("duplicate kind %s", s.Kind)
		}
		seen[s.Kind] = true
		if s.Binary == "" || len(s.Detect) == 0 || len(s.Install) == 0 {
			t.Errorf("%s: incomplete spec", s.Kind)
		}
	}
}
