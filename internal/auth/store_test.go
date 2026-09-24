package auth

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
	"golang.org/x/oauth2"
)

func sampleToken(email string) *StoredToken {
	return &StoredToken{
		Token:   &oauth2.Token{AccessToken: "at", RefreshToken: "rt", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour).UTC()},
		Scopes:  []string{ScopeGmailReadonly},
		Email:   email,
		Created: time.Now().UTC(),
	}
}

func TestKeyringStore(t *testing.T) {
	keyring.MockInit()
	s := KeyringStore{}
	if _, err := s.Load("a@digio.es"); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
	if err := s.Save(sampleToken("A@digio.es")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load("a@digio.es")
	if err != nil {
		t.Fatal(err)
	}
	if got.Token.RefreshToken != "rt" || got.Email != "A@digio.es" {
		t.Fatalf("got %+v", got)
	}
	if err := s.Delete("a@digio.es"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("a@digio.es"); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestFileStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tokens")
	s := FileStore{Dir: dir}
	if err := s.Save(sampleToken("a@digio.es")); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(filepath.Join(dir, "a@digio.es.json"))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", fi.Mode().Perm())
		}
	}
	if _, err := s.Load("a@digio.es"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load("../etc/passwd"); err == nil {
		t.Fatal("expected invalid email error")
	}
	if err := s.Delete("a@digio.es"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load("a@digio.es"); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestFallbackStoreUsesKeyring(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	var warn bytes.Buffer
	s := &FallbackStore{Keyring: KeyringStore{}, File: FileStore{Dir: dir}, Warn: &warn}
	if err := s.Save(sampleToken("a@digio.es")); err != nil {
		t.Fatal(err)
	}
	if s.Backend("a@digio.es") != "keyring" {
		t.Fatalf("backend %q", s.Backend("a@digio.es"))
	}
	if _, err := os.Stat(filepath.Join(dir, "a@digio.es.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("no file should be written when the keyring works")
	}
	if warn.Len() != 0 {
		t.Fatalf("unexpected warning %q", warn.String())
	}
	if err := s.Delete("a@digio.es"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("a@digio.es"); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestFallbackStoreFallsBackToFile(t *testing.T) {
	keyring.MockInitWithError(errors.New("no dbus"))
	t.Cleanup(keyring.MockInit)
	dir := t.TempDir()
	var warn bytes.Buffer
	s := &FallbackStore{Keyring: KeyringStore{}, File: FileStore{Dir: dir}, Warn: &warn}
	if err := s.Save(sampleToken("a@digio.es")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load("a@digio.es"); err != nil {
		t.Fatal(err)
	}
	if s.Backend("a@digio.es") != "file" {
		t.Fatalf("backend %q", s.Backend("a@digio.es"))
	}
	if n := strings.Count(warn.String(), "warning:"); n != 1 {
		t.Fatalf("expected exactly one warning, got %d: %q", n, warn.String())
	}
}

func TestNewTokenStoreForceFile(t *testing.T) {
	keyring.MockInit()
	t.Setenv(EnvKeyring, "file")
	dir := t.TempDir()
	s := NewTokenStore(dir, nil)
	if !s.ForceFile {
		t.Fatal("GWORK_KEYRING=file should force the file backend")
	}
	if err := s.Save(sampleToken("a@digio.es")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "tokens", "a@digio.es.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := (KeyringStore{}).Load("a@digio.es"); !errors.Is(err, ErrTokenNotFound) {
		t.Fatal("keyring must not be used when forced to file")
	}
}
