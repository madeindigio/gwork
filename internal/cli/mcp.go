package cli

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/madeindigio/gwork/internal/auth"
	"github.com/madeindigio/gwork/internal/mcpserver"
)

func newMCPCmd(a *App) *cobra.Command {
	var services, allowWrite, logLevel string
	var allowSend bool
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run the MCP server over stdio (read-only unless --allow-write)",
		Long: "Run a Model Context Protocol server over stdin/stdout for AI agents.\n\n" +
			"Only tools of the selected services that the account has granted are\n" +
			"registered. The server is read-only by default; --allow-write registers\n" +
			"write tools (drafts, labels, events, chat messages...) for the listed\n" +
			"services, which also need write scopes granted with\n" +
			"gwork auth login --write. Sending email additionally needs --allow-send.\n" +
			"Logs go to stderr; stdout carries protocol messages only.\n" +
			"Each tool call is bounded by 2m unless --timeout is given explicitly\n" +
			"(--timeout 0 disables the per-call timeout).",
		Example: `  gwork mcp
  gwork mcp --services gmail,calendar --account me@digio.es
  gwork mcp --allow-write gmail          # drafts, labels, trash; no sending
  gwork mcp --allow-write gmail,calendar --allow-send

  # Claude Code
  claude mcp add gwork -- gwork mcp`,
		Args:        cobra.NoArgs,
		Annotations: map[string]string{annotationNoTimeout: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			svcs, err := auth.ParseServices(services)
			if err != nil {
				return err
			}
			writeSvcs, err := auth.ParseWriteServices(allowWrite)
			if err != nil {
				return fmt.Errorf("--allow-write: %w", err)
			}
			if allowSend && !slices.Contains(writeSvcs, auth.Gmail) {
				return errors.New("--allow-send requires gmail in --allow-write (e.g. --allow-write gmail --allow-send)")
			}
			var level slog.Level
			if err := level.UnmarshalText([]byte(logLevel)); err != nil {
				return fmt.Errorf("invalid --log-level %q: %w", logLevel, err)
			}
			logger := slog.New(slog.NewTextHandler(a.Err, &slog.HandlerOptions{Level: level}))
			slog.SetDefault(logger)

			deps := mcpserver.Deps{Logger: logger, Timeout: mcpToolTimeout(cmd.Flags().Changed("timeout"), a.Flags.Timeout), Now: a.Now,
				Write: mcpserver.WriteOptions{Services: writeSvcs, AllowSend: allowSend}}
			if p, err := a.Provider(cmd.Context()); err != nil {
				deps.ProviderErr = err
			} else {
				deps.Provider = p
			}
			return mcpserver.New(deps, svcs).Run(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&services, "services", "all", "comma separated tool groups to enable: gmail,calendar,drive,chat or all")
	cmd.Flags().StringVar(&allowWrite, "allow-write", "", "comma separated services whose write tools are registered: gmail,calendar,chat or all (default none: read-only)")
	cmd.Flags().BoolVar(&allowSend, "allow-send", false, "allow sending email (requires gmail in --allow-write)")
	cmd.Flags().StringVar(&logLevel, "log-level", "info", "stderr log level: "+strings.Join([]string{"debug", "info", "warn", "error"}, ", "))
	return cmd
}

// mcpToolTimeout maps --timeout to mcpserver.Deps.Timeout. The CLI default
// (1m) is meant for single commands, so unless the flag was set explicitly
// the server uses its own default (mcpserver.DefaultToolTimeout). An
// explicit 0 disables the per-call timeout.
func mcpToolTimeout(explicit bool, flag time.Duration) time.Duration {
	switch {
	case !explicit:
		return 0
	case flag <= 0:
		return mcpserver.NoToolTimeout
	}
	return flag
}
