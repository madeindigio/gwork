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

// flakyKeyring is a TokenStore whose Save (and optionally Delete) fails,
// holding whatever record it had before.
type flakyKeyring struct {
	rec        *StoredToken
	deleteErr  error
	deleteCall int
}

func (k *flakyKeyring) Load(string) (*StoredToken, error) {
	if k.rec == nil {
		return nil, ErrTokenNotFound
	}
	c := *k.rec
	return &c, nil
}

func (k *flakyKeyring) Save(*StoredToken) error { return errors.New("keyring locked") }

func (k *flakyKeyring) Delete(string) error {
	k.deleteCall++
	if k.deleteErr != nil {
		return k.deleteErr
	}
	if k.rec == nil {
		return ErrTokenNotFound
	}
	k.rec = nil
	return nil
}

func TestFallbackStoreSaveFailureDropsStaleKeyringEntry(t *testing.T) {
	old := sampleToken("a@digio.es")
	old.Token.RefreshToken = "rt-old"
	kr := &flakyKeyring{rec: old}
	s := &FallbackStore{Keyring: kr, File: FileStore{Dir: t.TempDir()}}

	fresh := sampleToken("a@digio.es")
	fresh.Token.RefreshToken = "rt-new"
	if err := s.Save(fresh); err != nil {
		t.Fatal(err)
	}
	if kr.deleteCall != 1 || kr.rec != nil {
		t.Fatalf("stale keyring entry not deleted (calls %d)", kr.deleteCall)
	}
	got, err := s.Load("a@digio.es")
	if err != nil {
		t.Fatal(err)
	}
	if got.Token.RefreshToken != "rt-new" || got.Saved.IsZero() {
		t.Fatalf("loaded %+v, want the file copy with a save stamp", got)
	}
	if s.Backend("a@digio.es") != "file" {
		t.Fatalf("backend %q", s.Backend("a@digio.es"))
	}
}

func TestFallbackStoreLoadPrefersNewerRecord(t *testing.T) {
	now := time.Now().UTC()
	old := sampleToken("a@digio.es")
	old.Token.RefreshToken = "rt-old"
	old.Saved = now.Add(-time.Hour)
	// The keyring cannot delete its entry either, so both copies remain.
	kr := &flakyKeyring{rec: old, deleteErr: errors.New("keyring locked")}
	s := &FallbackStore{Keyring: kr, File: FileStore{Dir: t.TempDir()}}

	fresh := sampleToken("a@digio.es")
	fresh.Token.RefreshToken = "rt-new"
	if err := s.Save(fresh); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load("a@digio.es")
	if err != nil {
		t.Fatal(err)
	}
	if got.Token.RefreshToken != "rt-new" {
		t.Fatalf("loaded %q, want the newer file record", got.Token.RefreshToken)
	}

	// A keyring record newer than the file wins.
	kr.rec.Saved = now.Add(time.Hour)
	if got, _ := s.Load("a@digio.es"); got.Token.RefreshToken != "rt-old" {
		t.Fatalf("loaded %q, want the newer keyring record", got.Token.RefreshToken)
	}
	if s.Backend("a@digio.es") != "keyring" {
		t.Fatalf("backend %q", s.Backend("a@digio.es"))
	}
}

func TestFallbackStoreKeyringErrorDeletesEntry(t *testing.T) {
	keyring.MockInit()
	if err := (KeyringStore{}).Save(sampleToken("a@digio.es")); err != nil {
		t.Fatal(err)
	}
	// With the mock failing every call, Save must still succeed via the
	// file and must have attempted to remove the keyring entry.
	keyring.MockInitWithError(errors.New("no dbus"))
	t.Cleanup(keyring.MockInit)
	s := &FallbackStore{Keyring: KeyringStore{}, File: FileStore{Dir: t.TempDir()}}
	if err := s.Save(sampleToken("a@digio.es")); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Load("a@digio.es"); err != nil || got.Saved.IsZero() {
		t.Fatalf("load: %+v, %v", got, err)
	}
}
