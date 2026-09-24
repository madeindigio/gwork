// Package config locates the gwork configuration directory and reads and
// writes config.json, which records the known accounts and the default one.
//
// The directory is $GWORK_CONFIG_DIR when set, otherwise
// os.UserConfigDir()/gwork (XDG_CONFIG_HOME/gwork on Linux).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// EnvConfigDir is the environment variable overriding the config directory.
const EnvConfigDir = "GWORK_CONFIG_DIR"

// FileName is the name of the configuration file inside the config directory.
const FileName = "config.json"

// Dir returns the gwork configuration directory. It does not create it.
func Dir() (string, error) {
	if d := os.Getenv(EnvConfigDir); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user config dir (set %s to override): %w", EnvConfigDir, err)
	}
	return filepath.Join(base, "gwork"), nil
}

// EnsureDir creates dir (and parents) with mode 0700 if it does not exist.
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir %s: %w", dir, err)
	}
	return nil
}

// Config is the content of config.json.
type Config struct {
	// DefaultAccount is the account used when neither --account nor
	// GWORK_ACCOUNT is set.
	DefaultAccount string `json:"default_account,omitempty"`
	// Accounts lists the email addresses that have logged in.
	Accounts []string `json:"accounts,omitempty"`
}

// Load reads config.json from dir. A missing file yields an empty Config.
func Load(dir string) (*Config, error) {
	path := filepath.Join(dir, FileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &c, nil
}

// Save writes c to dir/config.json atomically with mode 0600.
func Save(dir string, c *Config) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	return WriteFileAtomic(filepath.Join(dir, FileName), append(data, '\n'), 0o600)
}

// WriteFileAtomic writes data to path through a temporary file in the same
// directory followed by a rename, creating the parent directory with 0700.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := EnsureDir(dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op after a successful rename
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename to %s: %w", path, err)
	}
	return nil
}

// NormalizeEmail lower-cases and trims an email address.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// HasAccount reports whether email is a known account.
func (c *Config) HasAccount(email string) bool {
	return slices.Contains(c.Accounts, NormalizeEmail(email))
}

// AddAccount records email as a known account and makes it the default
// when there is none yet.
func (c *Config) AddAccount(email string) {
	email = NormalizeEmail(email)
	if !slices.Contains(c.Accounts, email) {
		c.Accounts = append(c.Accounts, email)
		slices.Sort(c.Accounts)
	}
	if c.DefaultAccount == "" {
		c.DefaultAccount = email
	}
}

// RemoveAccount forgets email. If it was the default, the first remaining
// account (if any) becomes the default.
func (c *Config) RemoveAccount(email string) {
	email = NormalizeEmail(email)
	c.Accounts = slices.DeleteFunc(c.Accounts, func(a string) bool { return a == email })
	if c.DefaultAccount == email {
		c.DefaultAccount = ""
		if len(c.Accounts) > 0 {
			c.DefaultAccount = c.Accounts[0]
		}
	}
}
