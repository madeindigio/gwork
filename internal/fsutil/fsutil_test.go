package fsutil

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteFile(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.bin")

	n, err := WriteFile(dest, strings.NewReader("hello"), false)
	if err != nil || n != 5 {
		t.Fatalf("WriteFile = %d, %v", n, err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "hello" {
		t.Errorf("content = %q", b)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(dest)
		if err != nil || fi.Mode().Perm() != FileMode {
			t.Errorf("mode = %v, %v; want %v", fi.Mode().Perm(), err, FileMode)
		}
	}

	// No overwrite without force.
	_, err = WriteFile(dest, strings.NewReader("other"), false)
	if !errors.Is(err, ErrExists) || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v, want ErrExists", err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "hello" {
		t.Errorf("content changed to %q", b)
	}

	// Force replaces and keeps the private mode.
	if err := os.Chmod(dest, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteFile(dest, strings.NewReader("new"), true); err != nil {
		t.Fatalf("force: %v", err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "new" {
		t.Errorf("content = %q after force", b)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(dest); fi.Mode().Perm() != FileMode {
			t.Errorf("mode after force = %v", fi.Mode().Perm())
		}
	}
	assertOnlyFiles(t, dir, "out.bin")
}

// appearingReader creates path when it is first read, simulating a file
// that shows up while a download is in progress.
type appearingReader struct {
	path string
	r    io.Reader
	done bool
}

func (a *appearingReader) Read(p []byte) (int, error) {
	if !a.done {
		a.done = true
		if err := os.WriteFile(a.path, []byte("racer"), 0o600); err != nil {
			return 0, err
		}
	}
	return a.r.Read(p)
}

func TestWriteFileRechecksBeforeRename(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "f")
	_, err := WriteFile(dest, &appearingReader{path: dest, r: strings.NewReader("data")}, false)
	if !errors.Is(err, ErrExists) {
		t.Fatalf("err = %v, want ErrExists", err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "racer" {
		t.Errorf("existing file replaced: %q", b)
	}
	assertOnlyFiles(t, dir, "f")
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("network down") }

func TestWriteFileCleansUpOnError(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "f")
	if _, err := WriteFile(dest, failingReader{}, false); err == nil || !strings.Contains(err.Error(), "network down") {
		t.Fatalf("err = %v", err)
	}
	assertOnlyFiles(t, dir)
}

func TestCheckDest(t *testing.T) {
	dir := t.TempDir()
	if err := CheckDest(filepath.Join(dir, "missing"), false); err != nil {
		t.Errorf("missing file: %v", err)
	}
	if err := CheckDest(dir, true); err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("directory: %v", err)
	}
	f := filepath.Join(dir, "x")
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckDest(f, false); !errors.Is(err, ErrExists) {
		t.Errorf("existing: %v", err)
	}
	if err := CheckDest(f, true); err != nil {
		t.Errorf("existing with force: %v", err)
	}
	if _, err := WriteFile(filepath.Join(dir, "nodir", "f"), strings.NewReader("x"), false); err == nil {
		t.Error("missing parent directory: want error")
	}
}

func assertOnlyFiles(t *testing.T, dir string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("files in dir = %v, want %v", got, want)
	}
}
