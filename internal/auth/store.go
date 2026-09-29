package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zalando/go-keyring"
	"golang.org/x/oauth2"

	"github.com/madeindigio/gwork/internal/config"
)

// KeyringService is the service name used in the OS keyring.
const KeyringService = "gwork"

// EnvKeyring selects the token backend; "file" forces the 0600 file store.
const EnvKeyring = "GWORK_KEYRING"

// ErrTokenNotFound is returned by TokenStore.Load when nothing is stored.
var ErrTokenNotFound = errors.New("token not found")

// StoredToken is what gwork persists per account.
type StoredToken struct {
	Token   *oauth2.Token `json:"token"`
	Scopes  []string      `json:"scopes"`
	Email   string        `json:"email"`
	Created time.Time     `json:"created"`
	// Saved is when this record was last written by FallbackStore.Save;
	// it decides which copy wins when both backends hold one.
	Saved time.Time `json:"saved,omitzero"`
}

// stamp returns the time the record was last written: Saved, or Created
// for records written before Saved existed.
func (s *StoredToken) stamp() time.Time {
	if !s.Saved.IsZero() {
		return s.Saved
	}
	return s.Created
}

// Services returns the services fully covered by the stored scopes.
func (s *StoredToken) Services() []Service { return GrantedServices(s.Scopes) }

// WriteServices returns the services for which both the read and the write
// scopes are stored.
func (s *StoredToken) WriteServices() []Service { return WriteGrantedServices(s.Scopes) }

// TokenStore persists tokens keyed by account email.
type TokenStore interface {
	Load(email string) (*StoredToken, error)
	Save(t *StoredToken) error
	Delete(email string) error
}

// KeyringStore stores tokens in the OS keyring.
type KeyringStore struct{}

// Load implements TokenStore.
func (KeyringStore) Load(email string) (*StoredToken, error) {
	v, err := keyring.Get(KeyringService, config.NormalizeEmail(email))
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, ErrTokenNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("keyring get: %w", err)
	}
	return decodeToken([]byte(v))
}

// Save implements TokenStore.
func (KeyringStore) Save(t *StoredToken) error {
	data, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("encode token: %w", err)
	}
	if err := keyring.Set(KeyringService, config.NormalizeEmail(t.Email), string(data)); err != nil {
		return fmt.Errorf("keyring set: %w", err)
	}
	return nil
}

// Delete implements TokenStore.
func (KeyringStore) Delete(email string) error {
	err := keyring.Delete(KeyringService, config.NormalizeEmail(email))
	if errors.Is(err, keyring.ErrNotFound) {
		return ErrTokenNotFound
	}
	if err != nil {
		return fmt.Errorf("keyring delete: %w", err)
	}
	return nil
}

// FileStore stores tokens as <Dir>/<email>.json with mode 0600.
type FileStore struct {
	Dir string
}

func (f FileStore) path(email string) (string, error) {
	email = config.NormalizeEmail(email)
	if email == "" || strings.ContainsAny(email, `/\`) || strings.Contains(email, "..") {
		return "", fmt.Errorf("invalid account email %q", email)
	}
	return filepath.Join(f.Dir, email+".json"), nil
}

// Load implements TokenStore.
func (f FileStore) Load(email string) (*StoredToken, error) {
	p, err := f.path(email)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrTokenNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read token file: %w", err)
	}
	return decodeToken(data)
}

// Save implements TokenStore.
func (f FileStore) Save(t *StoredToken) error {
	p, err := f.path(t.Email)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("encode token: %w", err)
	}
	return config.WriteFileAtomic(p, data)
}

// Delete implements TokenStore.
func (f FileStore) Delete(email string) error {
	p, err := f.path(email)
	if err != nil {
		return err
	}
	err = os.Remove(p)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrTokenNotFound
	}
	if err != nil {
		return fmt.Errorf("remove token file: %w", err)
	}
	return nil
}

func decodeToken(data []byte) (*StoredToken, error) {
	var t StoredToken
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("decode stored token: %w", err)
	}
	if t.Token == nil {
		return nil, errors.New("decode stored token: missing token")
	}
	return &t, nil
}

// FallbackStore uses the OS keyring and falls back to a FileStore when the
// keyring is unavailable (or when forced with GWORK_KEYRING=file). The
// fallback is announced once on Warn.
type FallbackStore struct {
	Keyring   TokenStore
	File      FileStore
	ForceFile bool
	Warn      io.Writer

	once sync.Once
}

// NewTokenStore returns the default store for configDir: keyring with a
// <configDir>/tokens file fallback. GWORK_KEYRING=file forces the file
// backend. Warnings go to warn (usually stderr).
func NewTokenStore(configDir string, warn io.Writer) *FallbackStore {
	return &FallbackStore{
		Keyring:   KeyringStore{},
		File:      FileStore{Dir: filepath.Join(configDir, "tokens")},
		ForceFile: strings.EqualFold(os.Getenv(EnvKeyring), "file"),
		Warn:      warn,
	}
}

func (s *FallbackStore) warn(err error) {
	s.once.Do(func() {
		if s.Warn != nil {
			fmt.Fprintf(s.Warn, "warning: OS keyring unavailable (%v); storing tokens in %s with mode 0600\n", err, s.File.Dir)
		}
	})
}

// Load implements TokenStore. When both the keyring and the file hold a
// record (e.g. a keyring write failed after an earlier successful one), the
// most recently saved record wins.
func (s *FallbackStore) Load(email string) (*StoredToken, error) {
	t, _, err := s.load(email)
	return t, err
}

// load returns the current record for email and the backend holding it.
func (s *FallbackStore) load(email string) (*StoredToken, string, error) {
	var kt *StoredToken
	if !s.ForceFile {
		t, err := s.Keyring.Load(email)
		switch {
		case err == nil:
			kt = t
		case !errors.Is(err, ErrTokenNotFound):
			s.warn(err)
		}
	}
	ft, err := s.File.Load(email)
	switch {
	case kt == nil && err != nil:
		return nil, "", err
	case kt == nil:
		return ft, "file", nil
	case err == nil && ft.stamp().After(kt.stamp()):
		return ft, "file", nil
	}
	return kt, "keyring", nil
}

// Save implements TokenStore. It stores a copy of st stamped with the save
// time (Saved). When the keyring write fails, it removes any older keyring
// entry (best effort) so it cannot shadow the file copy, and writes the file.
func (s *FallbackStore) Save(st *StoredToken) error {
	t := *st
	t.Saved = time.Now().UTC()
	if !t.Saved.After(st.Saved) {
		// Keep stamps strictly increasing even with a coarse clock.
		t.Saved = st.Saved.Add(time.Nanosecond)
	}
	if !s.ForceFile {
		err := s.Keyring.Save(&t)
		if err == nil {
			// Drop a stale file copy from an earlier fallback, if any.
			_ = s.File.Delete(t.Email)
			return nil
		}
		s.warn(err)
		_ = s.Keyring.Delete(t.Email)
	}
	return s.File.Save(&t)
}

// Delete implements TokenStore. It removes the token from both backends and
// returns ErrTokenNotFound only when neither had it.
func (s *FallbackStore) Delete(email string) error {
	found := false
	var errs []error
	if !s.ForceFile {
		switch err := s.Keyring.Delete(email); {
		case err == nil:
			found = true
		case !errors.Is(err, ErrTokenNotFound):
			errs = append(errs, err)
		}
	}
	switch err := s.File.Delete(email); {
	case err == nil:
		found = true
	case !errors.Is(err, ErrTokenNotFound):
		errs = append(errs, err)
	}
	if found {
		return nil
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return ErrTokenNotFound
}

// Backend reports where the token for email currently lives: "keyring",
// "file" or "" when not stored.
func (s *FallbackStore) Backend(email string) string {
	_, backend, _ := s.load(email)
	return backend
}
