// Package catalog describes every package manager goblin knows how to isolate.
//
// Each Spec explains how to detect a manager in a directory, which
// environment variables redirect its global caches/homes into the goblin
// root, how to install and build, and which generated paths should never be
// committed.
//
// Template placeholders usable in Env, PathPrepend and commands:
//
//	{root}  absolute goblin root (e.g. /proj/.goblin)
//	{path}  absolute directory the manager runs in
//	{bin}   {root}/bin, the shared bin directory placed first on PATH
//	{name}  environment name
package catalog

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Spec is the static description of one package manager.
type Spec struct {
	Kind        string
	Name        string
	Language    string
	Description string
	// Binary is the executable that must exist for build to run.
	Binary string
	// Detect lists marker files (globs allowed) that indicate the manager.
	Detect []string
	// Env holds the isolation variables. Values may use templates.
	Env map[string]string
	// PathPrepend lists directories (templated) to put on PATH.
	PathPrepend []string
	// Install fetches dependencies into the isolated caches.
	Install []string
	// Build compiles the project. Empty means "nothing to build".
	Build []string
	// Artifacts are generated paths, relative to the manager path, that
	// should be excluded from version control.
	Artifacts []string
	// Lockfiles are files worth committing; used only for status output.
	Lockfiles []string
}

var specs = []Spec{
	{
		Kind: "cargo", Name: "Cargo", Language: "Rust",
		Description: "Rust crates; isolates CARGO_HOME (registry, git checkouts, installed bins)",
		Binary:      "cargo",
		Detect:      []string{"Cargo.toml"},
		Env: map[string]string{
			"CARGO_HOME":         "{root}/cargo",
			"CARGO_INSTALL_ROOT": "{root}",
		},
		PathPrepend: []string{"{root}/cargo/bin"},
		Install:     []string{"cargo fetch"},
		Build:       []string{"cargo build"},
		Artifacts:   []string{"target/"},
		Lockfiles:   []string{"Cargo.lock"},
	},
	{
		Kind: "go", Name: "Go modules", Language: "Go",
		Description: "Go modules; isolates GOPATH, module and build caches; binaries go to .goblin/bin",
		Binary:      "go",
		Detect:      []string{"go.mod"},
		Env: map[string]string{
			"GOPATH":     "{root}/go",
			"GOMODCACHE": "{root}/go/pkg/mod",
			"GOCACHE":    "{root}/go/build-cache",
			"GOBIN":      "{bin}",
			"GOFLAGS":    "-modcacherw",
		},
		Install:   []string{"go mod download"},
		Build:     []string{"go install ./..."},
		Artifacts: []string{},
		Lockfiles: []string{"go.sum"},
	},
	{
		Kind: "npm", Name: "npm", Language: "JavaScript",
		Description: "Node packages via npm; isolates the npm cache and global prefix",
		Binary:      "npm",
		Detect:      []string{"package-lock.json", "package.json"},
		Env: map[string]string{
			"npm_config_cache":           "{root}/npm/cache",
			"npm_config_prefix":          "{root}/npm",
			"npm_config_update_notifier": "false",
			"NPM_CONFIG_USERCONFIG":      "{root}/npm/npmrc",
		},
		PathPrepend: []string{"{root}/npm/bin", "{path}/node_modules/.bin"},
		Install:     []string{"npm install"},
		Build:       []string{"npm run build --if-present"},
		Artifacts:   []string{"node_modules/", "dist/"},
		Lockfiles:   []string{"package-lock.json"},
	},
	{
		Kind: "pnpm", Name: "pnpm", Language: "JavaScript",
		Description: "Node packages via pnpm; isolates the content-addressable store",
		Binary:      "pnpm",
		Detect:      []string{"pnpm-lock.yaml", "pnpm-workspace.yaml"},
		Env: map[string]string{
			"PNPM_HOME":            "{root}/pnpm",
			"npm_config_store_dir": "{root}/pnpm/store",
			"npm_config_cache":     "{root}/pnpm/cache",
		},
		PathPrepend: []string{"{root}/pnpm", "{path}/node_modules/.bin"},
		Install:     []string{"pnpm install"},
		Build:       []string{"pnpm run --if-present build"},
		Artifacts:   []string{"node_modules/", "dist/"},
		Lockfiles:   []string{"pnpm-lock.yaml"},
	},
	{
		Kind: "yarn", Name: "Yarn", Language: "JavaScript",
		Description: "Node packages via Yarn; isolates the cache and global folder",
		Binary:      "yarn",
		Detect:      []string{"yarn.lock"},
		Env: map[string]string{
			"YARN_CACHE_FOLDER":  "{root}/yarn/cache",
			"YARN_GLOBAL_FOLDER": "{root}/yarn/global",
		},
		PathPrepend: []string{"{path}/node_modules/.bin"},
		Install:     []string{"yarn install"},
		Build:       []string{},
		Artifacts:   []string{"node_modules/", "dist/", ".yarn/cache/"},
		Lockfiles:   []string{"yarn.lock"},
	},
	{
		Kind: "bun", Name: "Bun", Language: "JavaScript",
		Description: "Bun runtime and package manager; isolates BUN_INSTALL and its cache",
		Binary:      "bun",
		Detect:      []string{"bun.lock", "bun.lockb"},
		Env: map[string]string{
			"BUN_INSTALL":           "{root}/bun",
			"BUN_INSTALL_CACHE_DIR": "{root}/bun/install/cache",
		},
		PathPrepend: []string{"{root}/bun/bin", "{path}/node_modules/.bin"},
		Install:     []string{"bun install"},
		Build:       []string{"bun run --if-present build"},
		Artifacts:   []string{"node_modules/", "dist/"},
		Lockfiles:   []string{"bun.lock", "bun.lockb"},
	},
	{
		Kind: "deno", Name: "Deno", Language: "JavaScript",
		Description: "Deno runtime; isolates DENO_DIR (module cache) and install root",
		Binary:      "deno",
		Detect:      []string{"deno.json", "deno.jsonc", "deno.lock"},
		Env: map[string]string{
			"DENO_DIR":          "{root}/deno",
			"DENO_INSTALL_ROOT": "{root}/deno/install",
		},
		PathPrepend: []string{"{root}/deno/install/bin"},
		Install:     []string{"deno install"},
		Build:       []string{},
		Artifacts:   []string{"node_modules/", "vendor/"},
		Lockfiles:   []string{"deno.lock"},
	},
	{
		Kind: "uv", Name: "uv", Language: "Python",
		Description: "Python via uv; isolates the uv cache, managed Pythons and tools; venv in .venv",
		Binary:      "uv",
		Detect:      []string{"uv.lock", "pyproject.toml"},
		Env: map[string]string{
			"UV_CACHE_DIR":           "{root}/uv/cache",
			"UV_PYTHON_INSTALL_DIR":  "{root}/uv/python",
			"UV_TOOL_DIR":            "{root}/uv/tools",
			"UV_TOOL_BIN_DIR":        "{bin}",
			"UV_PROJECT_ENVIRONMENT": "{path}/.venv",
			"VIRTUAL_ENV":            "{path}/.venv",
		},
		PathPrepend: []string{"{path}/.venv/bin"},
		Install:     []string{"uv sync"},
		Build:       []string{},
		Artifacts:   []string{".venv/", "__pycache__/", ".pytest_cache/", ".ruff_cache/", ".mypy_cache/", "dist/", "*.egg-info/"},
		Lockfiles:   []string{"uv.lock"},
	},
	{
		Kind: "pip", Name: "pip + venv", Language: "Python",
		Description: "Python via pip in a project .venv; isolates the pip cache",
		Binary:      "python3",
		Detect:      []string{"requirements.txt"},
		Env: map[string]string{
			"PIP_CACHE_DIR":                 "{root}/pip/cache",
			"PIP_DISABLE_PIP_VERSION_CHECK": "1",
			"VIRTUAL_ENV":                   "{path}/.venv",
			"PIP_REQUIRE_VIRTUALENV":        "true",
			"PYTHONDONTWRITEBYTECODE":       "",
		},
		PathPrepend: []string{"{path}/.venv/bin"},
		Install: []string{
			"python3 -m venv {path}/.venv",
			"{path}/.venv/bin/python -m pip install -r requirements.txt",
		},
		Build:     []string{},
		Artifacts: []string{".venv/", "__pycache__/", ".pytest_cache/", "dist/", "*.egg-info/"},
		Lockfiles: []string{"requirements.txt"},
	},
	{
		Kind: "poetry", Name: "Poetry", Language: "Python",
		Description: "Python via Poetry; isolates the Poetry cache, venv kept in project",
		Binary:      "poetry",
		Detect:      []string{"poetry.lock"},
		Env: map[string]string{
			"POETRY_CACHE_DIR":              "{root}/poetry/cache",
			"POETRY_VIRTUALENVS_IN_PROJECT": "true",
		},
		PathPrepend: []string{"{path}/.venv/bin"},
		Install:     []string{"poetry install"},
		Build:       []string{},
		Artifacts:   []string{".venv/", "__pycache__/", ".pytest_cache/", "dist/"},
		Lockfiles:   []string{"poetry.lock"},
	},
	{
		Kind: "zig", Name: "Zig", Language: "Zig",
		Description: "Zig build system; isolates the global package cache",
		Binary:      "zig",
		Detect:      []string{"build.zig"},
		Env: map[string]string{
			"ZIG_GLOBAL_CACHE_DIR": "{root}/zig/global-cache",
			"ZIG_LOCAL_CACHE_DIR":  "{path}/.zig-cache",
		},
		Install:   []string{"zig build --fetch"},
		Build:     []string{"zig build"},
		Artifacts: []string{".zig-cache/", "zig-out/"},
		Lockfiles: []string{"build.zig.zon"},
	},
	{
		Kind: "bundler", Name: "Bundler", Language: "Ruby",
		Description: "Ruby gems via Bundler; isolates GEM_HOME and the bundle path",
		Binary:      "bundle",
		Detect:      []string{"Gemfile"},
		Env: map[string]string{
			"GEM_HOME":    "{root}/gem",
			"BUNDLE_PATH": "{root}/bundle",
		},
		PathPrepend: []string{"{root}/gem/bin", "{root}/bundle/bin"},
		Install:     []string{"bundle install"},
		Build:       []string{},
		Artifacts:   []string{".bundle/", "vendor/bundle/"},
		Lockfiles:   []string{"Gemfile.lock"},
	},
	{
		Kind: "composer", Name: "Composer", Language: "PHP",
		Description: "PHP packages via Composer; isolates COMPOSER_HOME and its cache",
		Binary:      "composer",
		Detect:      []string{"composer.json"},
		Env: map[string]string{
			"COMPOSER_HOME":      "{root}/composer",
			"COMPOSER_CACHE_DIR": "{root}/composer/cache",
		},
		PathPrepend: []string{"{path}/vendor/bin"},
		Install:     []string{"composer install"},
		Build:       []string{},
		Artifacts:   []string{"vendor/"},
		Lockfiles:   []string{"composer.lock"},
	},
	{
		Kind: "maven", Name: "Maven", Language: "Java",
		Description: "JVM builds via Maven; isolates the local repository",
		Binary:      "mvn",
		Detect:      []string{"pom.xml"},
		Env: map[string]string{
			"MAVEN_OPTS": "-Dmaven.repo.local={root}/maven/repository",
		},
		Install:   []string{"mvn -q dependency:resolve"},
		Build:     []string{"mvn -q package"},
		Artifacts: []string{"target/"},
	},
	{
		Kind: "gradle", Name: "Gradle", Language: "Java",
		Description: "JVM builds via Gradle; isolates GRADLE_USER_HOME",
		Binary:      "gradle",
		Detect:      []string{"build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts"},
		Env: map[string]string{
			"GRADLE_USER_HOME": "{root}/gradle",
		},
		Install:   []string{"gradle --quiet dependencies"},
		Build:     []string{"gradle --quiet build"},
		Artifacts: []string{"build/", ".gradle/"},
	},
	{
		Kind: "dotnet", Name: ".NET", Language: "C#",
		Description: ".NET projects; isolates the NuGet package folder and CLI home",
		Binary:      "dotnet",
		Detect:      []string{"*.sln", "*.csproj", "*.fsproj"},
		Env: map[string]string{
			"NUGET_PACKAGES":              "{root}/nuget/packages",
			"DOTNET_CLI_HOME":             "{root}/dotnet",
			"DOTNET_CLI_TELEMETRY_OPTOUT": "1",
		},
		PathPrepend: []string{"{root}/dotnet/.dotnet/tools"},
		Install:     []string{"dotnet restore"},
		Build:       []string{"dotnet build --no-restore"},
		Artifacts:   []string{"bin/", "obj/"},
	},
	{
		Kind: "mix", Name: "Mix", Language: "Elixir",
		Description: "Elixir projects via Mix; isolates MIX_HOME and the Hex cache",
		Binary:      "mix",
		Detect:      []string{"mix.exs"},
		Env: map[string]string{
			"MIX_HOME": "{root}/mix",
			"HEX_HOME": "{root}/hex",
		},
		Install:   []string{"mix deps.get"},
		Build:     []string{"mix compile"},
		Artifacts: []string{"_build/", "deps/"},
		Lockfiles: []string{"mix.lock"},
	},
	{
		Kind: "swift", Name: "Swift PM", Language: "Swift",
		Description: "Swift packages; keeps the build directory in the project",
		Binary:      "swift",
		Detect:      []string{"Package.swift"},
		Env: map[string]string{
			"SWIFTPM_CACHE_DIR": "{root}/swiftpm/cache",
		},
		Install:   []string{"swift package resolve"},
		Build:     []string{"swift build"},
		Artifacts: []string{".build/"},
		Lockfiles: []string{"Package.resolved"},
	},
}

// All returns every known spec in display order.
func All() []Spec {
	out := make([]Spec, len(specs))
	copy(out, specs)
	return out
}

// Kinds returns the sorted list of manager identifiers.
func Kinds() []string {
	kinds := make([]string, 0, len(specs))
	for _, s := range specs {
		kinds = append(kinds, s.Kind)
	}
	sort.Strings(kinds)
	return kinds
}

// Lookup finds a spec by kind (case-insensitive). ok is false if unknown.
func Lookup(kind string) (Spec, bool) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	for _, s := range specs {
		if s.Kind == kind {
			return s, true
		}
	}
	return Spec{}, false
}

// Available reports whether the spec's binary is on PATH.
func (s Spec) Available() bool {
	if s.Binary == "" {
		return true
	}
	_, err := exec.LookPath(s.Binary)
	return err == nil
}

// DetectedIn reports whether any marker file for the spec exists in dir.
func (s Spec) DetectedIn(dir string) bool {
	for _, marker := range s.Detect {
		matches, err := filepath.Glob(filepath.Join(dir, marker))
		if err == nil && len(matches) > 0 {
			return true
		}
	}
	return false
}

// Detection is a manager found while scanning a project.
type Detection struct {
	Kind string
	// Path is relative to the scanned root, "." for the root itself.
	Path string
}

// skipDirs are never scanned for nested projects.
var skipDirs = map[string]bool{
	"node_modules": true, "target": true, "dist": true, "build": true,
	"vendor": true, "_build": true, "deps": true, "zig-out": true,
	"bin": true, "obj": true, ".venv": true, "__pycache__": true,
}

// Scan looks for managers in root and its immediate subdirectories.
// Lockfile-specific managers (pnpm, yarn, bun) win over plain npm when
// both match in the same directory.
func Scan(root string) []Detection {
	var found []Detection
	dirs := []string{"."}
	entries, err := os.ReadDir(root)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || skipDirs[e.Name()] {
				continue
			}
			dirs = append(dirs, e.Name())
		}
	}
	for _, rel := range dirs {
		abs := filepath.Join(root, rel)
		var here []string
		for _, s := range specs {
			if s.DetectedIn(abs) {
				here = append(here, s.Kind)
			}
		}
		here = resolveOverlaps(here)
		for _, k := range here {
			found = append(found, Detection{Kind: k, Path: filepath.ToSlash(rel)})
		}
	}
	return found
}

// resolveOverlaps drops generic managers when a more specific one matched.
func resolveOverlaps(kinds []string) []string {
	has := func(k string) bool {
		for _, x := range kinds {
			if x == k {
				return true
			}
		}
		return false
	}
	var out []string
	for _, k := range kinds {
		switch k {
		case "npm":
			if has("pnpm") || has("yarn") || has("bun") {
				continue
			}
		case "uv":
			if has("poetry") {
				continue
			}
		case "pip":
			if has("uv") || has("poetry") {
				continue
			}
		}
		out = append(out, k)
	}
	return out
}

// Expand substitutes the template placeholders in s.
func Expand(s string, vars map[string]string) string {
	r := strings.NewReplacer(
		"{root}", vars["root"],
		"{path}", vars["path"],
		"{bin}", vars["bin"],
		"{name}", vars["name"],
	)
	return r.Replace(s)
}
