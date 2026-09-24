package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/digio/gwork-cli/internal/auth"
	"github.com/digio/gwork-cli/internal/buildinfo"
)

// WhoamiInput is the (empty) input of the whoami tool.
type WhoamiInput struct{}

// WhoamiOutput describes the current account.
type WhoamiOutput struct {
	Account         string   `json:"account" jsonschema:"email of the Google account in use"`
	GrantedServices []string `json:"granted_services" jsonschema:"services the account granted read access to"`
	EnabledServices []string `json:"enabled_services" jsonschema:"services whose tools are registered in this server"`
	Version         string   `json:"version" jsonschema:"gwork version"`
}

func registerWhoami(s *mcp.Server, deps Deps, enabled []auth.Service) {
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
