package provision

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestStripPath(t *testing.T) {
	cases := []struct {
		name  string
		strip int
		want  string
		ok    bool
	}{
		{"uv-x86_64/uv", 1, "uv", true},
		{"uv-x86_64/", 1, "", false},
		{"./go/bin/go", 1, "bin/go", true},
		{"deno", 0, "deno", true},
		{"../evil", 0, "", false},
		{"a/../../evil", 1, "", false},
	}
	for _, c := range cases {
		got, ok := stripPath(c.name, c.strip)
		if ok != c.ok || got != c.want {
			t.Errorf("stripPath(%q,%d) = %q,%v want %q,%v", c.name, c.strip, got, ok, c.want, c.ok)
		}
	}
}

func TestUntarGzStripsAndKeepsMode(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(name string, mode int64, body string, typ byte) {
		h := &tar.Header{Name: name, Mode: mode, Size: int64(len(body)), Typeflag: typ}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if body != "" {
			tw.Write([]byte(body))
		}
	}
	add("tool-1.0/", 0o755, "", tar.TypeDir)
	add("tool-1.0/bin/", 0o755, "", tar.TypeDir)
	add("tool-1.0/bin/tool", 0o755, "#!/bin/sh\necho hi\n", tar.TypeReg)
	add("tool-1.0/README", 0o644, "docs", tar.TypeReg)
	tw.Close()
	gz.Close()

	dir := t.TempDir()
	archive := filepath.Join(dir, "a.tar.gz")
	if err := os.WriteFile(archive, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "out")
	if err := unpack(archive, "tar.gz", dest, 1); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dest, "bin", "tool"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o111 == 0 {
		t.Fatalf("executable bit lost: %v", st.Mode())
	}
	if _, err := os.Stat(filepath.Join(dest, "README")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "tool-1.0")); err == nil {
		t.Fatal("top-level directory was not stripped")
	}
}

func TestUnzipRejectsEscape(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("../escape")
	w.Write([]byte("x"))
	w, _ = zw.Create("bun-linux/bun")
	w.Write([]byte("bin"))
	zw.Close()
	dir := t.TempDir()
	archive := filepath.Join(dir, "a.zip")
	os.WriteFile(archive, buf.Bytes(), 0o644)
	dest := filepath.Join(dir, "out")
	if err := unpack(archive, "zip", dest, 1); err != nil {
		t.Fatalf("escaping entries should be skipped, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "bun")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "escape")); err == nil {
		t.Fatal("zip slip: file written outside dest")
	}
}
