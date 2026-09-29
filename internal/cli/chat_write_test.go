package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/madeindigio/gwork/internal/auth"
	"github.com/madeindigio/gwork/internal/testutil"
)

type chatSendFake struct {
	mu     sync.Mutex
	calls  int
	bodies []map[string]any
}

func (f *chatSendFake) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func newChatSendFake(t *testing.T) (*chatSendFake, *http.ServeMux) {
	t.Helper()
	f := &chatSendFake{}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls++
		f.mu.Unlock()
		testutil.WriteGoogleError(w, 500, "backendError", "unexpected "+r.URL.Path)
	})
	mux.HandleFunc("POST /v1/spaces/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.calls++
		f.bodies = append(f.bodies, body)
		f.mu.Unlock()
		space := "spaces/" + r.PathValue("id")
		testutil.WriteJSON(t, w, map[string]any{"name": space + "/messages/M1", "text": body["text"],
			"space": map[string]any{"name": space}, "thread": map[string]any{"name": space + "/threads/T1"},
			"createTime": "2026-09-24T12:00:00Z"})
	})
	mux.HandleFunc("GET /v1/spaces:findDirectMessage", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls++
		f.mu.Unlock()
		testutil.WriteJSON(t, w, map[string]any{"name": "spaces/DM1", "spaceType": "DIRECT_MESSAGE"})
	})
	return f, mux
}

func TestChatSendDryRun(t *testing.T) {
	f, mux := newChatSendFake(t)
	p := testutil.NewFakeProvider(t, mux)
	stdout, stderr, code := runCLI(t, p, "chat", "send", "--space", "AAA", "--text", "hello", "--dry-run", "--json")
	if code != 0 {
		t.Fatalf("code %d: %s", code, stderr)
	}
	if f.count() != 0 {
		t.Errorf("dry run made %d HTTP calls", f.count())
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(stdout), &got); err != nil || got["space"] != "spaces/AAA" || got["text"] != "hello" {
		t.Errorf("stdout %q (%v)", stdout, err)
	}
}

func TestChatSendConfirmation(t *testing.T) {
	tests := []struct {
		name     string
		tty      bool
		stdin    string
		yes      bool
		wantCode int
		wantErr  string
		wantSent bool
	}{
		{name: "non tty without yes", stdin: "", wantCode: 1, wantErr: "pass --yes"},
		{name: "tty yes answer", tty: true, stdin: "y\n", wantCode: 0, wantSent: true},
		{name: "tty no answer", tty: true, stdin: "n\n", wantCode: 1, wantErr: "aborted"},
		{name: "yes flag", yes: true, wantCode: 0, wantSent: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, mux := newChatSendFake(t)
			app, stdout, stderr := newTestApp(t, testutil.NewFakeProvider(t, mux))
			app.In = strings.NewReader(tc.stdin)
			app.IsTerminal = func() bool { return tc.tty }
			args := []string{"chat", "send", "--to", "ana@digio.es", "--text", "hello there", "--thread", "spaces/DM1/threads/T"}
			if tc.yes {
				args = append(args, "--yes")
			}
			code := app.Run(context.Background(), args)
			if code != tc.wantCode {
				t.Fatalf("code %d, stderr %s", code, stderr)
			}
			if tc.wantErr != "" && !strings.Contains(stderr.String(), tc.wantErr) {
				t.Errorf("stderr %q lacks %q", stderr, tc.wantErr)
			}
			if tc.wantSent {
				if len(f.bodies) != 1 || f.bodies[0]["text"] != "hello there" {
					t.Errorf("bodies %+v", f.bodies)
				}
				if !strings.Contains(stdout.String(), "spaces/DM1/messages/M1") {
					t.Errorf("stdout %q", stdout)
				}
			} else if f.count() != 0 {
				t.Errorf("made %d calls without confirmation", f.count())
			}
			if tc.tty && !strings.Contains(stderr.String(), "ana@digio.es") {
				t.Errorf("prompt lacks target: %s", stderr)
			}
		})
	}
}

func TestChatSendTextSources(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "m.txt")
	if err := os.WriteFile(path, []byte("from file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		args    []string
		stdin   string
		want    string
		wantErr string
	}{
		{name: "file", args: []string{"--text-file", path, "--yes"}, want: "from file"},
		{name: "stdin", args: []string{"--text-file", "-", "--yes"}, stdin: "from stdin\n", want: "from stdin"},
		{name: "stdin needs yes", args: []string{"--text-file", "-"}, stdin: "x", wantErr: "consumes stdin"},
		{name: "both", args: []string{"--text", "a", "--text-file", path, "--yes"}, wantErr: "exactly one of --text or --text-file"},
		{name: "neither", args: []string{"--yes"}, wantErr: "exactly one of --text or --text-file"},
		{name: "missing file", args: []string{"--text-file", filepath.Join(dir, "nope"), "--yes"}, wantErr: "read text file"},
		{name: "empty text", args: []string{"--text", " ", "--yes"}, wantErr: "text is required"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, mux := newChatSendFake(t)
			app, _, stderr := newTestApp(t, testutil.NewFakeProvider(t, mux))
			app.In = strings.NewReader(tc.stdin)
			app.IsTerminal = func() bool { return false }
			code := app.Run(context.Background(), append([]string{"chat", "send", "--space", "AAA"}, tc.args...))
			if tc.wantErr != "" {
				if code == 0 || !strings.Contains(stderr.String(), tc.wantErr) || f.count() != 0 {
					t.Fatalf("code %d calls %d stderr %s", code, f.count(), stderr)
				}
				return
			}
			if code != 0 || len(f.bodies) != 1 || f.bodies[0]["text"] != tc.want {
				t.Fatalf("code %d bodies %+v stderr %s", code, f.bodies, stderr)
			}
		})
	}
}

func TestChatSendJSON(t *testing.T) {
	_, mux := newChatSendFake(t)
	stdout, stderr, code := runCLI(t, testutil.NewFakeProvider(t, mux), "chat", "send", "--space", "spaces/AAA", "--text", "hi", "--yes", "--json")
	if code != 0 {
		t.Fatalf("code %d: %s", code, stderr)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		t.Fatal(err)
	}
	if raw["name"] != "spaces/AAA/messages/M1" || raw["space"] != "spaces/AAA" || raw["text"] != "hi" || raw["thread"] != "spaces/AAA/threads/T1" {
		t.Errorf("json %v", raw)
	}
}

func TestChatSendMissingWriteScope(t *testing.T) {
	f, mux := newChatSendFake(t)
	p := testutil.NewFakeProvider(t, mux).GrantWrite(auth.Gmail)
	_, stderr, code := runCLI(t, p, "chat", "send", "--space", "AAA", "--text", "hi", "--yes")
	if code == 0 || !strings.Contains(stderr, "write access") || !strings.Contains(stderr, "--write chat") {
		t.Fatalf("code %d stderr %s", code, stderr)
	}
	if f.count() != 0 {
		t.Errorf("made %d calls", f.count())
	}
}
