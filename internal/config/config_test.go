package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDirOverride(t *testing.T) {
	t.Setenv(EnvConfigDir, "/tmp/custom-gwork")
	d, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if d != "/tmp/custom-gwork" {
		t.Fatalf("Dir() = %q", d)
	}
}

func TestDirDefault(t *testing.T) {
	t.Setenv(EnvConfigDir, "")
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg")
	t.Setenv("HOME", "/tmp/home")
	t.Setenv("AppData", "/tmp/appdata")
	d, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(d) != "gwork" {
		t.Fatalf("Dir() = %q, want .../gwork", d)
	}
}

func TestLoadMissing(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c.DefaultAccount != "" || len(c.Accounts) != 0 {
		t.Fatalf("expected empty config, got %+v", c)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested")
	c := &Config{}
	c.AddAccount(" Bob@Digio.es ")
	c.AddAccount("alice@digio.es")
	if err := Save(dir, c); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(filepath.Join(dir, FileName))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %v, want 0600", fi.Mode().Perm())
		}
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultAccount != "bob@digio.es" {
		t.Fatalf("default = %q", got.DefaultAccount)
	}
	if len(got.Accounts) != 2 || got.Accounts[0] != "alice@digio.es" {
		t.Fatalf("accounts = %v", got.Accounts)
	}
	if !got.HasAccount("ALICE@digio.es") {
		t.Fatal("HasAccount should be case-insensitive")
	}
}

func TestRemoveAccountPromotesNext(t *testing.T) {
	c := &Config{}
	c.AddAccount("b@digio.es")
	c.AddAccount("a@digio.es")
	c.RemoveAccount("b@digio.es")
	if c.DefaultAccount != "a@digio.es" {
		t.Fatalf("default = %q", c.DefaultAccount)
	}
	c.RemoveAccount("a@digio.es")
	if c.DefaultAccount != "" || len(c.Accounts) != 0 {
		t.Fatalf("expected empty, got %+v", c)
	}
}

func TestLoadInvalid(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("expected parse error")
	}
}
