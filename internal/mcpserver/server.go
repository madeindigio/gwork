// Package mcpserver exposes gwork as a Model Context Protocol server over
// stdio. Tools are thin adapters over internal/workspace/* and are all
// read-only.
//
// Each service registers its tools from its own file (gmail.go,
// calendar.go, drive.go, chat.go) through a registerXxx(s, deps) function
// listed in registrars. Tool groups are registered only when requested with
// --services and granted by the account's OAuth scopes.
//
// Nothing but protocol messages may be written to stdout: log through
// Deps.Logger, which writes to stderr.
package mcpserver

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/api/option"

	"github.com/digio/gwork-cli/internal/auth"
	"github.com/digio/gwork-cli/internal/buildinfo"
)

// DefaultToolTimeout bounds each tool call when Deps.Timeout is zero.
const DefaultToolTimeout = 2 * time.Minute

// NoToolTimeout, as Deps.Timeout, disables the per-call timeout.
const NoToolTimeout time.Duration = -1

// Deps are the dependencies shared by every tool handler.
type Deps struct {
	// Provider builds API client options for the current account. It is nil
	// when no account is logged in; ProviderErr then says why.
	Provider auth.ClientProvider
	// ProviderErr is the error obtained while building Provider, if any.
	ProviderErr error
	// Logger writes to stderr. Nil discards logs.
	Logger *slog.Logger
	// Timeout bounds each tool call; zero means DefaultToolTimeout and a
	// negative value (NoToolTimeout) disables the per-call timeout.
	Timeout time.Duration
	// Now returns the current time; nil means time.Now. Inject it in tests
	// that resolve relative times.
	Now func() time.Time
}

// ClientOptions returns the API client options for svc, or the reason no
// account is available.
func (d Deps) ClientOptions(ctx context.Context, svc auth.Service) ([]option.ClientOption, error) {
	if d.Provider == nil {
		return nil, d.notLoggedIn()
	}
	return d.Provider.ClientOptions(ctx, svc)
}

// CurrentTime returns Now() or time.Now().
func (d Deps) CurrentTime() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d Deps) logger() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return slog.New(slog.DiscardHandler)
}

// timeout returns the per-call timeout; 0 means none.
func (d Deps) timeout() time.Duration {
	switch {
	case d.Timeout > 0:
		return d.Timeout
	case d.Timeout < 0:
		return 0
	}
	return DefaultToolTimeout
}

func (d Deps) notLoggedIn() error {
	if d.ProviderErr != nil {
		return d.ProviderErr
	}
	return &auth.Error{Kind: auth.ErrNotLoggedIn, Message: "no account logged in", Hint: "run: gwork auth login"}
}

// registrars maps each service to the function registering its tools.
// Phase 2 service files only fill in their own registerXxx function.
var registrars = map[auth.Service]func(*mcp.Server, Deps){
	auth.Gmail:    registerGmail,
	auth.Calendar: registerCalendar,
	auth.Drive:    registerDrive,
	auth.Chat:     registerChat,
}

// Server is a configured MCP server.
type Server struct {
	// MCP is the underlying go-sdk server.
	MCP *mcp.Server
	// Enabled lists the services whose tools were registered.
	Enabled []auth.Service
}

// New builds the MCP server. The whoami tool is always registered; the
// tools of each service in services are registered only when the account
// granted that service (skipped services are logged).
func New(deps Deps, services []auth.Service) *Server {
	log := deps.logger()
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "gwork",
		Title:   "gwork (read-only Google Workspace)",
		Version: buildinfo.Version,
	}, &mcp.ServerOptions{
		Logger: log,
		Instructions: "Read-only access to the user's Gmail, Google Calendar, Google Drive and Google Chat. " +
			"Call whoami to see the account and the enabled services. Long text fields are truncated; " +
			"check the truncated flag and raise max_chars if needed.",
	})

	var granted []auth.Service
	if deps.Provider != nil {
		granted = deps.Provider.GrantedServices()
	} else {
		log.Warn("no account available; only whoami is registered", "err", deps.notLoggedIn())
	}

	var enabled []auth.Service
	for _, svc := range auth.AllServices {
		if !slices.Contains(services, svc) || deps.Provider == nil {
			continue
		}
		if !slices.Contains(granted, svc) {
			log.Warn("service not granted; its tools are not registered", "service", svc, "hint", auth.LoginHint(svc))
			continue
		}
		register, ok := registrars[svc]
		if !ok {
			continue
		}
		register(s, deps)
		enabled = append(enabled, svc)
	}
	registerWhoami(s, deps, enabled)
	log.Info("mcp server ready", "account", accountOf(deps), "services", auth.JoinServices(enabled))
	return &Server{MCP: s, Enabled: enabled}
}

// Run serves the MCP protocol over stdin/stdout until the client
// disconnects or ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	if err := s.MCP.Run(ctx, &mcp.StdioTransport{}); err != nil {
		return fmt.Errorf("mcp server: %w", err)
	}
	return nil
}

func accountOf(d Deps) string {
	if d.Provider == nil {
		return ""
	}
	return d.Provider.Account()
}
