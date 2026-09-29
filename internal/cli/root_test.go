package cli

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"google.golang.org/api/googleapi"

	"github.com/madeindigio/gwork/internal/auth"
	"github.com/madeindigio/gwork/internal/buildinfo"
	"github.com/madeindigio/gwork/internal/testutil"
	"github.com/madeindigio/gwork/internal/workspace/gmail"
)

func TestVersion(t *testing.T) {
	out, _, code := runCLI(t, nil, "version")
	if code != 0 || !strings.HasPrefix(out, "gwork "+buildinfo.Version) {
		t.Fatalf("code=%d out=%q", code, out)
	}
	out, _, code = runCLI(t, nil, "version", "--json")
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	var v versionInfo
	if err := json.Unmarshal([]byte(out), &v); err != nil || v.Version != buildinfo.Version {
		t.Fatalf("json %q: %v", out, err)
	}
	out, _, _ = runCLI(t, nil, "version", "-o", "json")
	if !strings.HasPrefix(out, "{") {
		t.Fatalf("-o json not honored: %q", out)
	}
}

func TestInvalidOutputFormat(t *testing.T) {
	_, errOut, code := runCLI(t, nil, "version", "--output", "yaml")
	if code != 1 || !strings.Contains(errOut, "invalid output format") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}

func TestServiceGroupsRegistered(t *testing.T) {
	app, _, _ := newTestApp(t, nil)
	root := NewRootCmd(app)
	for _, svc := range auth.AllServices {
		cmd, _, err := root.Find([]string{string(svc)})
		if err != nil || cmd.Name() != string(svc) {
			t.Fatalf("group %s not registered: %v", svc, err)
		}
		if cmd.Annotations[annotationService] != string(svc) {
			t.Fatalf("group %s lacks service annotation", svc)
		}
	}
	if cmd, _, err := root.Find([]string{"mcp"}); err != nil || cmd.Name() != "mcp" {
		t.Fatal("mcp command not registered")
	}
}

// TestServiceCommandPattern shows how Phase 2 subcommands are written and
// tested: attach a subcommand to a group, use app.ClientOptions and
// app.Print, and fake Google with testutil.
func TestServiceCommandPattern(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /gmail/v1/users/me/labels", func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"labels": []any{map[string]any{"id": "INBOX", "name": "Inbox"}}})
	})
	provider := testutil.NewFakeProvider(t, mux, auth.Gmail)

	build := func(app *App) *cobra.Command {
		root := NewRootCmd(app)
		gmailCmd, _, _ := root.Find([]string{"gmail"})
		gmailCmd.AddCommand(&cobra.Command{
			Use: "labels-demo",
			RunE: func(cmd *cobra.Command, _ []string) error {
				opts, err := app.ClientOptions(cmd.Context(), auth.Gmail)
				if err != nil {
					return err
				}
				svc, err := gmail.New(cmd.Context(), opts...)
				if err != nil {
					return err
				}
				res, err := svc.Users.Labels.List("me").Context(cmd.Context()).Do()
				if err != nil {
					return err
				}
				type label struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				}
				out := []label{{ID: res.Labels[0].Id, Name: res.Labels[0].Name}}
				return app.Print(out, nil)
			},
		})
		return root
	}

	app, out, _ := newTestApp(t, provider)
	app.Flags.JSON = true
	root := build(app)
	root.SetArgs([]string{"gmail", "labels-demo", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"name": "Inbox"`) {
		t.Fatalf("out %q", out.String())
	}
}

func TestExplainUsesServiceAnnotation(t *testing.T) {
	app, _, _ := newTestApp(t, nil)
	root := NewRootCmd(app)
	chatCmd, _, _ := root.Find([]string{"chat"})
	sub := &cobra.Command{Use: "x"}
	chatCmd.AddCommand(sub)
	err := app.explain(sub, &googleapi.Error{Code: 403, Errors: []googleapi.ErrorItem{{Reason: "insufficientPermissions"}}})
	if !errors.Is(err, auth.ErrInsufficientScope) || !strings.Contains(err.Error(), "--services chat") {
		t.Fatalf("got %v", err)
	}
}

func TestClientOptionsScopeError(t *testing.T) {
	provider := testutil.NewFakeProvider(t, http.NotFoundHandler(), auth.Gmail)
	app, _, _ := newTestApp(t, provider)
	_, err := app.ClientOptions(t.Context(), auth.Drive)
	if !errors.Is(err, auth.ErrInsufficientScope) || !strings.Contains(err.Error(), "gwork auth login --services drive") {
		t.Fatalf("got %v", err)
	}
}

func TestMCPInvalidServices(t *testing.T) {
	_, errOut, code := runCLI(t, testutil.NewFakeProvider(t, http.NotFoundHandler()), "mcp", "--services", "photos")
	if code != 1 || !strings.Contains(errOut, "unknown service") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}
