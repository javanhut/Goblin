package catalog

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Provision describes how goblin fetches a manager into the environment
// root when it is not installed on the host. Only official release
// channels are used, always over HTTPS.
//
// Templates may use {root}, {bin}, {path}, {name} plus {target} (the
// platform string from Targets) and {version}.
type Provision struct {
	// Method is "archive" (download and unpack), "binary" (download a
	// single executable) or "script" (download an official installer and
	// pipe it to Interpreter).
	Method string
	// Targets maps runtime GOOS/GOARCH ("linux/amd64") to the platform
	// string the project uses in its asset names. Missing = unsupported.
	Targets map[string]string
	// URL is the download location. "latest" redirects are used when the
	// project offers them, otherwise Resolve fills {version}.
	URL string
	// Resolve looks up the current release when URL needs a {version}.
	Resolve func(target string) (version string, err error)
	// Archive is "tar.gz" or "zip" for Method archive.
	Archive string
	// Strip removes this many leading path components while unpacking.
	Strip int
	// Dest is the directory (archive) or file (binary) to install into.
	Dest string
	// Interpreter runs the installer for Method script ("sh", "python3").
	Interpreter string
	// Args are passed to the interpreter after the script.
	Args []string
	// Post runs after installation inside the environment.
	Post []string
	// Env and PathPrepend apply only once the manager is provisioned.
	Env         map[string]string
	PathPrepend []string
	// VersionCmd prints the installed version, for the marker and status.
	VersionCmd string
	// Note explains manual installation when provisioning is unsupported.
	Note string
}

// Provisionable reports whether goblin can install the manager itself.
func (s Spec) Provisionable() bool {
	return s.Provision != nil && s.Provision.Method != ""
}

var (
	rustTargets = map[string]string{
		"linux/amd64":  "x86_64-unknown-linux-gnu",
		"linux/arm64":  "aarch64-unknown-linux-gnu",
		"darwin/amd64": "x86_64-apple-darwin",
		"darwin/arm64": "aarch64-apple-darwin",
	}
	nodeTargets = map[string]string{
		"linux/amd64":  "linux-x64",
		"linux/arm64":  "linux-arm64",
		"darwin/amd64": "darwin-x64",
		"darwin/arm64": "darwin-arm64",
	}
	bunTargets = map[string]string{
		"linux/amd64":  "linux-x64",
		"linux/arm64":  "linux-aarch64",
		"darwin/amd64": "darwin-x64",
		"darwin/arm64": "darwin-aarch64",
	}
	pnpmTargets = map[string]string{
		"linux/amd64":  "linux-x64",
		"linux/arm64":  "linux-arm64",
		"darwin/amd64": "darwin-x64",
		"darwin/arm64": "darwin-arm64",
	}
	goTargets = map[string]string{
		"linux/amd64":  "linux-amd64",
		"linux/arm64":  "linux-arm64",
		"darwin/amd64": "darwin-amd64",
		"darwin/arm64": "darwin-arm64",
	}
)

func init() {
	set := func(kind string, p *Provision) {
		for i := range specs {
			if specs[i].Kind == kind {
				specs[i].Provision = p
				return
			}
		}
		panic("provision for unknown kind " + kind)
	}

	set("uv", &Provision{
		Method:     "archive",
		Targets:    rustTargets,
		URL:        "https://github.com/astral-sh/uv/releases/latest/download/uv-{target}.tar.gz",
		Archive:    "tar.gz",
		Strip:      1,
		Dest:       "{bin}",
		VersionCmd: "uv --version",
	})
	set("bun", &Provision{
		Method:     "archive",
		Targets:    bunTargets,
		URL:        "https://github.com/oven-sh/bun/releases/latest/download/bun-{target}.zip",
		Archive:    "zip",
		Strip:      1,
		Dest:       "{root}/bun/bin",
		VersionCmd: "bun --version",
	})
	set("deno", &Provision{
		Method:     "archive",
		Targets:    rustTargets,
		URL:        "https://github.com/denoland/deno/releases/latest/download/deno-{target}.zip",
		Archive:    "zip",
		Dest:       "{bin}",
		VersionCmd: "deno --version",
	})
	set("pnpm", &Provision{
		Method:     "archive",
		Targets:    pnpmTargets,
		URL:        "https://github.com/pnpm/pnpm/releases/latest/download/pnpm-{target}.tar.gz",
		Archive:    "tar.gz",
		Dest:       "{root}/pnpm",
		VersionCmd: "pnpm --version",
	})
	set("npm", &Provision{
		Method:      "archive",
		Targets:     nodeTargets,
		URL:         "https://nodejs.org/dist/{version}/node-{version}-{target}.tar.gz",
		Resolve:     resolveNodeLTS,
		Archive:     "tar.gz",
		Strip:       1,
		Dest:        "{root}/node",
		PathPrepend: []string{"{root}/node/bin"},
		VersionCmd:  "node --version",
	})
	set("yarn", &Provision{
		Method:      "archive",
		Targets:     nodeTargets,
		URL:         "https://nodejs.org/dist/{version}/node-{version}-{target}.tar.gz",
		Resolve:     resolveNodeLTS,
		Archive:     "tar.gz",
		Strip:       1,
		Dest:        "{root}/node",
		Post:        []string{"npm install --silent --global --prefix {root}/npm yarn"},
		Env:         map[string]string{"npm_config_prefix": "{root}/npm"},
		PathPrepend: []string{"{root}/npm/bin", "{root}/node/bin"},
		VersionCmd:  "yarn --version",
	})
	set("go", &Provision{
		Method:      "archive",
		Targets:     goTargets,
		URL:         "https://go.dev/dl/{version}.{target}.tar.gz",
		Resolve:     resolveGoStable,
		Archive:     "tar.gz",
		Strip:       1,
		Dest:        "{root}/toolchains/go",
		Env:         map[string]string{"GOTOOLCHAIN": "local"},
		PathPrepend: []string{"{root}/toolchains/go/bin"},
		VersionCmd:  "go version",
	})
	set("cargo", &Provision{
		Method:      "script",
		Targets:     rustTargets,
		URL:         "https://sh.rustup.rs",
		Interpreter: "sh",
		Args:        []string{"-s", "--", "-y", "--no-modify-path", "--profile", "minimal"},
		Env:         map[string]string{"RUSTUP_HOME": "{root}/rustup"},
		VersionCmd:  "cargo --version",
	})
	set("poetry", &Provision{
		Method:      "script",
		Targets:     rustTargets,
		URL:         "https://install.python-poetry.org",
		Interpreter: "python3",
		Args:        []string{"-"},
		Env:         map[string]string{"POETRY_HOME": "{root}/poetry"},
		PathPrepend: []string{"{root}/poetry/bin"},
		VersionCmd:  "poetry --version",
	})

	set("zig", &Provision{Note: "ziglang.org ships .tar.xz archives; install zig with your system package manager"})
	set("pip", &Provision{Note: "needs a system python3"})
	set("bundler", &Provision{Note: "install ruby with your system package manager, then `gem install bundler`"})
	set("composer", &Provision{Note: "install php and composer with your system package manager"})
	set("maven", &Provision{Note: "install a JDK and maven with your system package manager"})
	set("gradle", &Provision{Note: "install a JDK and gradle with your system package manager"})
	set("dotnet", &Provision{Note: "install the .NET SDK from dotnet.microsoft.com"})
	set("mix", &Provision{Note: "install elixir with your system package manager"})
	set("swift", &Provision{Note: "install swift from swift.org"})
}

// resolveNodeLTS returns the newest LTS version string, e.g. "v22.11.0".
func resolveNodeLTS(_ string) (string, error) {
	var index []struct {
		Version string `json:"version"`
		LTS     any    `json:"lts"`
	}
	if err := fetchJSON("https://nodejs.org/dist/index.json", &index); err != nil {
		return "", err
	}
	for _, r := range index {
		if lts, ok := r.LTS.(string); ok && lts != "" {
			return r.Version, nil
		}
	}
	return "", fmt.Errorf("no LTS release found in nodejs.org index")
}

// resolveGoStable returns the newest stable Go version, e.g. "go1.23.4".
func resolveGoStable(target string) (string, error) {
	var releases []struct {
		Version string `json:"version"`
		Stable  bool   `json:"stable"`
		Files   []struct {
			OS   string `json:"os"`
			Arch string `json:"arch"`
			Kind string `json:"kind"`
		} `json:"files"`
	}
	if err := fetchJSON("https://go.dev/dl/?mode=json", &releases); err != nil {
		return "", err
	}
	osName, arch, _ := strings.Cut(target, "-")
	for _, r := range releases {
		if !r.Stable {
			continue
		}
		for _, f := range r.Files {
			if f.OS == osName && f.Arch == arch && f.Kind == "archive" {
				return r.Version, nil
			}
		}
	}
	return "", fmt.Errorf("no stable Go release for %s on go.dev", target)
}

func fetchJSON(url string, v any) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}
