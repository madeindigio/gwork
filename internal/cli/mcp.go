package cli

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"

	"github.com/digio/gwork-cli/internal/auth"
	"github.com/digio/gwork-cli/internal/mcpserver"
)

func newMCPCmd(a *App) *cobra.Command {
	var services, logLevel string
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run the read-only MCP server over stdio",
		Long: "Run a Model Context Protocol server over stdin/stdout for AI agents.\n\n" +
			"Only tools of the selected services that the account has granted are\n" +
			"registered. Logs go to stderr; stdout carries protocol messages only.\n" +
			"--timeout bounds each tool call.",
		Example: `  gwork mcp
  gwork mcp --services gmail,calendar --account me@digio.es

  # Claude Code
  claude mcp add gwork -- gwork mcp`,
		Args:        cobra.NoArgs,
		Annotations: map[string]string{annotationNoTimeout: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			svcs, err := auth.ParseServices(services)
			if err != nil {
				return err
			}
			var level slog.Level
			if err := level.UnmarshalText([]byte(logLevel)); err != nil {
				return fmt.Errorf("invalid --log-level %q: %w", logLevel, err)
			}
			logger := slog.New(slog.NewTextHandler(a.Err, &slog.HandlerOptions{Level: level}))
			slog.SetDefault(logger)

			deps := mcpserver.Deps{Logger: logger, Timeout: a.Flags.Timeout, Now: a.Now}
			if p, err := a.Provider(cmd.Context()); err != nil {
				deps.ProviderErr = err
			} else {
				deps.Provider = p
			}
			return mcpserver.New(deps, svcs).Run(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&services, "services", "all", "comma separated tool groups to enable: gmail,calendar,drive,chat or all")
	cmd.Flags().StringVar(&logLevel, "log-level", "info", "stderr log level: "+strings.Join([]string{"debug", "info", "warn", "error"}, ", "))
	return cmd
}
