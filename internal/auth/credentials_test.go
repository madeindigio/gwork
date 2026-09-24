package auth

import (
	"errors"
	"os"
	"path/filepath"
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

	in := CredentialInputs{FlagPath: flag, EnvPath: env, ConfigDir: cfgDir, EmbeddedID: "emb", EmbeddedSecret: "embs"}
	steps := []struct {
		wantID, wantSource string
		drop               func()
	}{
		{"flag", SourceFlag, func() { in.FlagPath = "" }},
		{"env", SourceEnv, func() { in.EnvPath = "" }},
		{"config", SourceConfig, func() { in.ConfigDir = t.TempDir() }},
		{"emb", SourceEmbedded, func() { in.EmbeddedID = "" }},
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
	if HintFor(err) == "" {
		t.Fatal("expected a hint")
	}
}

func TestResolveCredentialsExplicitPathMustExist(t *testing.T) {
	_, err := ResolveCredentials(CredentialInputs{FlagPath: "/nonexistent/creds.json", EmbeddedID: "a", EmbeddedSecret: "b"})
	if err == nil {
		t.Fatal("an explicit missing path must not fall through to the embedded client")
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
