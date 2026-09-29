package auth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCreds(t *testing.T, dir, name, id string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	data := `{"installed":{"client_id":"` + id + `","client_secret":"s-` + id + `","redirect_uris":["http://localhost"]}}`
	if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestResolveCredentialsPrecedence(t *testing.T) {
	dir := t.TempDir()
	flag := writeCreds(t, dir, "flag.json", "flag")
	env := writeCreds(t, dir, "env.json", "env")
	cfgDir := filepath.Join(dir, "cfg")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeCreds(t, cfgDir, CredentialsFileName, "config")

	in := CredentialInputs{FlagPath: flag, EnvPath: env, ConfigDir: cfgDir}
	steps := []struct {
		wantID, wantSource string
		drop               func()
	}{
		{"flag", SourceFlag, func() { in.FlagPath = "" }},
		{"env", SourceEnv, func() { in.EnvPath = "" }},
		{"config", SourceConfig, func() { in.ConfigDir = t.TempDir() }},
	}
	for _, s := range steps {
		c, err := ResolveCredentials(in)
		if err != nil {
			t.Fatalf("want %s: %v", s.wantSource, err)
		}
		if c.ClientID != s.wantID || c.Source != s.wantSource {
			t.Fatalf("got %+v, want id=%s source=%s", c, s.wantID, s.wantSource)
		}
		s.drop()
	}
	_, err := ResolveCredentials(in)
	if !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("expected ErrNoCredentials, got %v", err)
	}
	hint := HintFor(err)
	for _, want := range []string{"--credentials", EnvCredentials, filepath.Join(in.ConfigDir, CredentialsFileName), "docs/setup-google-cloud.md"} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint %q does not mention %q", hint, want)
		}
	}
}

func TestResolveCredentialsExplicitPathMustExist(t *testing.T) {
	cfgDir := t.TempDir()
	writeCreds(t, cfgDir, CredentialsFileName, "config")
	_, err := ResolveCredentials(CredentialInputs{FlagPath: "/nonexistent/creds.json", ConfigDir: cfgDir})
	if err == nil {
		t.Fatal("an explicit missing path must not fall through to the config dir")
	}
}

func TestParseCredentialsJSON(t *testing.T) {
	c, err := ParseCredentialsJSON([]byte(`{"web":{"client_id":"w","client_secret":"x"}}`))
	if err != nil || c.ClientID != "w" || c.ClientSecret != "x" {
		t.Fatalf("got %+v, %v", c, err)
	}
	if _, err := ParseCredentialsJSON([]byte(`{"other":{}}`)); err == nil {
		t.Fatal("expected error")
	}
	if _, err := ParseCredentialsJSON([]byte(`nope`)); err == nil {
		t.Fatal("expected error")
	}
}
