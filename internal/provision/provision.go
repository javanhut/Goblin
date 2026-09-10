// Package provision downloads package managers into a goblin root when the
// host does not provide them, using each project's official releases.
package provision

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"goblin/internal/catalog"
	"goblin/internal/config"
	"goblin/internal/envs"
)

// ErrUnsupported means goblin has no download recipe for this manager
// or platform.
var ErrUnsupported = errors.New("not provisionable")

// Reason explains why kind cannot be provisioned here, or "" if it can.
func Reason(spec catalog.Spec) string {
	p := spec.Provision
	switch {
	case p == nil:
		return "no installer known; install " + spec.Binary + " with your system package manager"
	case p.Method == "":
		return p.Note
	}
	if _, ok := p.Targets[Platform()]; !ok {
		return fmt.Sprintf("no %s build for %s", spec.Name, Platform())
	}
	return ""
}

// Platform is the GOOS/GOARCH key used in Provision.Targets.
func Platform() string { return runtime.GOOS + "/" + runtime.GOARCH }

// Logger receives progress lines.
type Logger func(format string, a ...any)

// Install fetches spec into env. It returns the version string printed by
// the tool. Existing provisioned copies are replaced when force is set.
func Install(env *envs.Environment, spec catalog.Spec, force bool, log Logger) (string, error) {
	if log == nil {
		log = func(string, ...any) {}
	}
	if reason := Reason(spec); reason != "" {
		return "", fmt.Errorf("%w: %s", ErrUnsupported, reason)
	}
	if env.IsProvisioned(spec.Kind) && !force {
		return env.ProvisionedVersion(spec.Kind), nil
	}
	p := spec.Provision
	target := p.Targets[Platform()]
	tv := env.TemplateVars(config.Manager{Kind: spec.Kind, Path: "."})
	tv["target"] = target

	if p.Resolve != nil {
		version, err := p.Resolve(target)
		if err != nil {
			return "", fmt.Errorf("resolve %s release: %w", spec.Name, err)
		}
		tv["version"] = version
	}
	url := expand(p.URL, tv)

	if err := os.MkdirAll(filepath.Join(env.Root, "tmp"), 0o755); err != nil {
		return "", err
	}
	var err error
	switch p.Method {
	case "archive":
		dest := expand(p.Dest, tv)
		log("downloading %s", url)
		var tmp string
		tmp, err = download(env, url)
		if err == nil {
			defer os.Remove(tmp)
			log("unpacking into %s", rel(env, dest))
			if force {
				_ = os.RemoveAll(dest)
			}
			err = unpack(tmp, p.Archive, dest, p.Strip)
		}
	case "binary":
		dest := expand(p.Dest, tv)
		log("downloading %s", url)
		var tmp string
		tmp, err = download(env, url)
		if err == nil {
			if err = os.MkdirAll(filepath.Dir(dest), 0o755); err == nil {
				err = os.Rename(tmp, dest)
			}
			if err == nil {
				err = os.Chmod(dest, 0o755)
			}
		}
	case "script":
		log("downloading installer %s", url)
		var body []byte
		body, err = fetch(url)
		if err == nil {
			log("running %s installer", spec.Name)
			err = runScript(env, spec, body, p, tv)
		}
	default:
		return "", fmt.Errorf("unknown provision method %q", p.Method)
	}
	if err != nil {
		return "", err
	}

	// Record the marker first so provisioned env/PATH apply to post steps.
	if err := writeMarker(env, spec.Kind, "installing"); err != nil {
		return "", err
	}
	fresh, err := envs.Resolve(env.Dir, env.Config)
	if err != nil {
		return "", err
	}
	for _, line := range p.Post {
		line = expand(line, tv)
		log("$ %s", line)
		if err := fresh.Command(env.Dir, line).Run(); err != nil {
			_ = os.Remove(env.ProvisionMarker(spec.Kind))
			return "", fmt.Errorf("`%s`: %w", line, err)
		}
	}
	version := probeVersion(fresh, p.VersionCmd)
	if version == "" {
		_ = os.Remove(env.ProvisionMarker(spec.Kind))
		return "", fmt.Errorf("%s was installed but `%s` did not run", spec.Name, p.VersionCmd)
	}
	if err := writeMarker(env, spec.Kind, version); err != nil {
		return "", err
	}
	return version, nil
}

// Remove deletes the provisioning marker; files stay until `goblin clean --env`.
func Remove(env *envs.Environment, kind string) error {
	err := os.Remove(env.ProvisionMarker(kind))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func writeMarker(env *envs.Environment, kind, content string) error {
	m := env.ProvisionMarker(kind)
	if err := os.MkdirAll(filepath.Dir(m), 0o755); err != nil {
		return err
	}
	return os.WriteFile(m, []byte(content+"\n"), 0o644)
}

func probeVersion(env *envs.Environment, line string) string {
	if line == "" {
		return "ok"
	}
	cmd := exec.Command("/bin/sh", "-c", line)
	cmd.Dir = env.Dir
	cmd.Env = env.Environ()
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	first, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	if m := versionRe.FindString(first); m != "" {
		return m
	}
	return strings.TrimSpace(first)
}

var versionRe = regexp.MustCompile(`v?\d+(\.\d+)+`)

func expand(s string, vars map[string]string) string {
	s = catalog.Expand(s, vars)
	return strings.NewReplacer("{target}", vars["target"], "{version}", vars["version"]).Replace(s)
}

func rel(env *envs.Environment, p string) string {
	if r, err := filepath.Rel(env.Dir, p); err == nil {
		return r
	}
	return p
}

var client = &http.Client{}

func fetch(url string) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func download(env *envs.Environment, url string) (string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	f, err := os.CreateTemp(filepath.Join(env.Root, "tmp"), "download-*")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func runScript(env *envs.Environment, spec catalog.Spec, body []byte, p *catalog.Provision, tv map[string]string) error {
	// The installer must see the provisioned variables (RUSTUP_HOME,
	// POETRY_HOME) so it lands inside the root. CARGO_HOME is already set.
	cmd := exec.Command(p.Interpreter, p.Args...)
	cmd.Dir = env.Dir
	cmd.Env = env.Environ()
	for k, v := range p.Env {
		cmd.Env = append(cmd.Env, k+"="+expand(v, tv))
	}
	cmd.Stdin = bytes.NewReader(body)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s installer: %w", spec.Name, err)
	}
	return nil
}

// unpack extracts a tar.gz or zip archive into dest, dropping strip
// leading path components. Entries that would escape dest are rejected.
func unpack(archive, kind, dest string, strip int) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	switch kind {
	case "tar.gz":
		return untarGz(archive, dest, strip)
	case "zip":
		return unzip(archive, dest, strip)
	}
	return fmt.Errorf("unknown archive type %q", kind)
}

func stripPath(name string, strip int) (string, bool) {
	name = filepath.ToSlash(filepath.Clean(name))
	if strings.HasPrefix(name, "..") || strings.HasPrefix(name, "/") {
		return "", false
	}
	parts := strings.Split(strings.TrimPrefix(name, "./"), "/")
	if len(parts) <= strip {
		return "", false
	}
	rel := filepath.Join(parts[strip:]...)
	if rel == "." || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return rel, true
}

func safeJoin(dest, rel string) (string, error) {
	p := filepath.Join(dest, rel)
	if !strings.HasPrefix(p, filepath.Clean(dest)+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive entry %q escapes %s", rel, dest)
	}
	return p, nil
}

func untarGz(archive, dest string, strip int) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		rel, ok := stripPath(h.Name, strip)
		if !ok {
			continue
		}
		p, err := safeJoin(dest, rel)
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeFile(p, tr, os.FileMode(h.Mode)); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			os.Remove(p)
			if err := os.Symlink(h.Linkname, p); err != nil {
				return err
			}
		}
	}
}

func unzip(archive, dest string, strip int) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, zf := range zr.File {
		rel, ok := stripPath(zf.Name, strip)
		if !ok {
			continue
		}
		p, err := safeJoin(dest, rel)
		if err != nil {
			return err
		}
		if zf.FileInfo().IsDir() {
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		err = writeFile(p, rc, zf.Mode())
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func writeFile(p string, r io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	perm := mode.Perm()
	if perm == 0 {
		perm = 0o644
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chmod(p, perm)
}
