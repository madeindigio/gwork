// Package mcpserver exposes gwork as a Model Context Protocol server over
// stdio. Tools are thin adapters over internal/workspace/*. They are
// read-only unless the operator opts in to write tools (Deps.Write, from
// gwork mcp --allow-write).
//
// Each service registers its tools from its own file (gmail.go,
// calendar.go, drive.go, chat.go) through a registerXxx(s, deps) function
// listed in registrars. Tool groups are registered only when requested with
// --services and granted by the account's OAuth scopes. Write tools live in
// <svc>_write.go files (registerXxxWrite, listed in writeRegistrars) and are
// registered only for services in Deps.Write that are also write-granted.
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

	"github.com/madeindigio/gwork/internal/auth"
	"github.com/madeindigio/gwork/internal/buildinfo"
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
	// Write configures the opt-in write tools; the zero value is read-only.
	Write WriteOptions
}

// WriteOptions selects the write tools to expose.
type WriteOptions struct {
	// Services are the services whose write tools may be registered
	// (gwork mcp --allow-write). They are registered only if the account
	// also granted the write scopes.
	Services []auth.Service
	// AllowSend permits tools that send email (gwork mcp --allow-send).
	// Gmail write registrars must check it before registering a send tool.
	AllowSend bool
}

// WriteClientOptions returns the API client options for write calls on svc,
// or the reason no write access is available.
func (d Deps) WriteClientOptions(ctx context.Context, svc auth.Service) ([]option.ClientOption, error) {
	if d.Provider == nil {
		return nil, d.notLoggedIn()
	}
	return d.Provider.WriteClientOptions(ctx, svc)
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

// writeRegistrars maps each writable service to the function registering its
// write tools. Service files only fill in their own registerXxxWrite.
var writeRegistrars = map[auth.Service]func(*mcp.Server, Deps){
	auth.Gmail:    registerGmailWrite,
	auth.Calendar: registerCalendarWrite,
	auth.Chat:     registerChatWrite,
}

// Server is a configured MCP server.
type Server struct {
	// MCP is the underlying go-sdk server.
	MCP *mcp.Server
	// Enabled lists the services whose tools were registered.
	Enabled []auth.Service
	// WriteEnabled lists the services whose write tools were registered.
	WriteEnabled []auth.Service
}

// New builds the MCP server. The whoami tool is always registered; the
// tools of each service in services are registered only when the account
// granted that service (skipped services are logged). Write tools are
// registered only for services that are enabled, listed in deps.Write and
// write-granted.
func New(deps Deps, services []auth.Service) *Server {
	log := deps.logger()

	var granted, writeGranted []auth.Service
	if deps.Provider != nil {
		granted = deps.Provider.GrantedServices()
		writeGranted = deps.Provider.WriteGrantedServices()
	} else {
		log.Warn("no account available; only whoami is registered", "err", deps.notLoggedIn())
	}

	var enabled, writeEnabled []auth.Service
	for _, svc := range auth.AllServices {
		if !slices.Contains(services, svc) || deps.Provider == nil {
			continue
		}
		if !slices.Contains(granted, svc) {
			log.Warn("service not granted; its tools are not registered", "service", svc, "hint", auth.LoginHint(svc))
			continue
		}
		if _, ok := registrars[svc]; !ok {
			continue
		}
		enabled = append(enabled, svc)
	}
	for _, svc := range enabled {
		if _, ok := writeRegistrars[svc]; !ok || !slices.Contains(deps.Write.Services, svc) {
			continue
		}
		if !slices.Contains(writeGranted, svc) {
			log.Warn("write access not granted; its write tools are not registered", "service", svc, "hint", auth.WriteLoginHint(svc))
			continue
		}
		writeEnabled = append(writeEnabled, svc)
	}
	for _, svc := range deps.Write.Services {
		if !slices.Contains(enabled, svc) {
			log.Warn("write tools requested for a service that is not enabled; not registered", "service", svc)
		}
	}

	title := "gwork (read-only Google Workspace)"
	instructions := "Read-only access to the user's Gmail, Google Calendar, Google Drive and Google Chat. " +
		"Call whoami to see the account and the enabled services. Long text fields are truncated; " +
		"check the truncated flag and raise max_chars if needed."
	if len(writeEnabled) > 0 {
		title = "gwork (Google Workspace, write tools enabled)"
		instructions = "Access to the user's Gmail, Google Calendar, Google Drive and Google Chat. " +
			"Call whoami to see the account, the enabled services and the services with write tools. " +
			"Long text fields are truncated; check the truncated flag and raise max_chars if needed. " +
			"Write tools can change the user's data (" + auth.JoinServices(writeEnabled) + "): prefer creating drafts " +
			"over sending, and ask the user for explicit confirmation before sending, deleting or changing anything."
	}
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "gwork",
		Title:   title,
		Version: buildinfo.Version,
	}, &mcp.ServerOptions{Logger: log, Instructions: instructions})

	for _, svc := range enabled {
		registrars[svc](s, deps)
	}
	for _, svc := range writeEnabled {
		writeRegistrars[svc](s, deps)
	}
	registerWhoami(s, deps, enabled, writeEnabled)
	log.Info("mcp server ready", "account", accountOf(deps), "services", auth.JoinServices(enabled),
		"write_services", auth.JoinServices(writeEnabled), "allow_send", deps.Write.AllowSend)
	return &Server{MCP: s, Enabled: enabled, WriteEnabled: writeEnabled}
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
