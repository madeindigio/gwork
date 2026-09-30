package mcpserver

import (
	"context"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/madeindigio/gwork/internal/auth"
	"github.com/madeindigio/gwork/internal/buildinfo"
)

// WhoamiInput is the (empty) input of the whoami tool.
type WhoamiInput struct{}

// WhoamiOutput describes the current account.
type WhoamiOutput struct {
	Account         string   `json:"account" jsonschema:"email of the Google account in use"`
	GrantedServices []string `json:"granted_services" jsonschema:"services the account granted read access to"`
	EnabledServices []string `json:"enabled_services" jsonschema:"services whose tools are registered in this server"`
	WriteServices   []string `json:"write_services" jsonschema:"services whose write tools are registered in this server (empty when read-only)"`
	AllowSend       bool     `json:"allow_send" jsonschema:"whether tools that send email are available"`
	Version         string   `json:"version" jsonschema:"gwork version"`
}

func registerWhoami(s *mcp.Server, deps Deps, enabled, writeEnabled []auth.Service) {
	addReadOnlyTool(s, deps, "", &mcp.Tool{
		Name:        "whoami",
		Description: "Return the Google account gwork is using, the services it granted and the services whose tools are available.",
	}, func(_ context.Context, _ WhoamiInput) (WhoamiOutput, error) {
		if deps.Provider == nil {
			return WhoamiOutput{}, deps.notLoggedIn()
		}
		return WhoamiOutput{
			Account:         deps.Provider.Account(),
			GrantedServices: serviceNames(deps.Provider.GrantedServices()),
			EnabledServices: serviceNames(enabled),
			WriteServices:   serviceNames(writeEnabled),
			AllowSend:       deps.Write.AllowSend && slices.Contains(writeEnabled, auth.Gmail),
			Version:         buildinfo.Version,
		}, nil
	})
}

func serviceNames(svcs []auth.Service) []string {
	out := make([]string, 0, len(svcs))
	for _, s := range svcs {
		out = append(out, string(s))
	}
	return out
}
