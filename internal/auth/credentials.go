package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/digio/gwork-cli/internal/buildinfo"
)

// EnvCredentials is the environment variable holding a credentials.json path.
const EnvCredentials = "GWORK_CREDENTIALS"

// CredentialsFileName is the credentials file looked up in the config dir.
const CredentialsFileName = "credentials.json"

// Credential sources, reported by ClientCredentials.Source.
const (
	SourceFlag     = "flag"
	SourceEnv      = "env"
	SourceConfig   = "config"
	SourceEmbedded = "embedded"
)

// ClientCredentials is an OAuth Desktop client.
type ClientCredentials struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	// Source says where the client came from (flag, env, config, embedded).
	Source string `json:"source"`
	// Path is the file the client was read from, empty when embedded.
	Path string `json:"path,omitempty"`
}

// CredentialInputs are the inputs of ResolveCredentials.
type CredentialInputs struct {
	// FlagPath is the value of --credentials.
	FlagPath string
	// EnvPath is the value of GWORK_CREDENTIALS.
	EnvPath string
	// ConfigDir is the gwork config directory.
	ConfigDir string
	// EmbeddedID and EmbeddedSecret are the client embedded at build time.
	EmbeddedID, EmbeddedSecret string
}

// DefaultCredentialInputs fills CredentialInputs from the environment and
// the build-time embedded client.
func DefaultCredentialInputs(flagPath, configDir string) CredentialInputs {
	return CredentialInputs{
		FlagPath:       flagPath,
		EnvPath:        os.Getenv(EnvCredentials),
		ConfigDir:      configDir,
		EmbeddedID:     buildinfo.OAuthClientID,
		EmbeddedSecret: buildinfo.OAuthClientSecret,
	}
}

// ResolveCredentials finds the OAuth client. First match wins:
//
//  1. --credentials <path>
//  2. GWORK_CREDENTIALS
//  3. <configDir>/credentials.json
//  4. the client embedded at build time
//
// An explicitly given path (flag or env) that cannot be read is an error;
// it never silently falls through to the next source.
func ResolveCredentials(in CredentialInputs) (*ClientCredentials, error) {
	if in.FlagPath != "" {
		return readCredentialsFile(in.FlagPath, SourceFlag)
	}
	if in.EnvPath != "" {
		return readCredentialsFile(in.EnvPath, SourceEnv)
	}
	if in.ConfigDir != "" {
		p := filepath.Join(in.ConfigDir, CredentialsFileName)
		c, err := readCredentialsFile(p, SourceConfig)
		if err == nil {
			return c, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	if in.EmbeddedID != "" && in.EmbeddedSecret != "" {
		return &ClientCredentials{ClientID: in.EmbeddedID, ClientSecret: in.EmbeddedSecret, Source: SourceEmbedded}, nil
	}
	return nil, &Error{
		Kind:    ErrNoCredentials,
		Message: "no OAuth client configured",
		Hint: fmt.Sprintf("pass --credentials <credentials.json>, set %s, or copy it to %s; %s",
			EnvCredentials, filepath.Join(in.ConfigDir, CredentialsFileName), SetupDocHint),
	}
}

// credentialsFile mirrors the JSON downloaded from the Google Cloud console.
type credentialsFile struct {
	Installed *clientSection `json:"installed"`
	Web       *clientSection `json:"web"`
}

type clientSection struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// ParseCredentialsJSON parses a Google OAuth client JSON ("installed" or
// "web" section).
func ParseCredentialsJSON(data []byte) (*ClientCredentials, error) {
	var f credentialsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse OAuth client JSON: %w", err)
	}
	sec := f.Installed
	if sec == nil {
		sec = f.Web
	}
	if sec == nil || sec.ClientID == "" {
		return nil, errors.New(`OAuth client JSON has no "installed" (Desktop) section with a client_id`)
	}
	return &ClientCredentials{ClientID: sec.ClientID, ClientSecret: sec.ClientSecret}, nil
}

func readCredentialsFile(path, source string) (*ClientCredentials, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read OAuth client %s (%s): %w", path, source, err)
	}
	c, err := ParseCredentialsJSON(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	c.Source, c.Path = source, path
	return c, nil
}
