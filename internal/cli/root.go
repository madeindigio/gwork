package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"

	"github.com/pkg/browser"
	"github.com/spf13/cobra"

	"github.com/madeindigio/gwork/internal/auth"
	"github.com/madeindigio/gwork/internal/output"
)

// Command annotations understood by the root command.
const (
	// annotationService marks a service group command ("gmail", ...). Errors
	// from its subcommands are classified with that service.
	annotationService = "gwork.service"
	// annotationNoTimeout disables the --timeout context for long-running
	// commands (mcp, auth login).
	annotationNoTimeout = "gwork.no-timeout"
)

// Execute runs gwork with os.Args and returns the process exit code.
func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	app := NewApp(os.Stdout, os.Stderr)
	app.In = os.Stdin
	app.IsTerminal = stdinIsTerminal
	app.OpenBrowser = func(url string) error {
		// pkg/browser writes the launcher's output to stdout by default,
		// which would corrupt --json output.
		browser.Stdout, browser.Stderr = os.Stderr, os.Stderr
		return browser.OpenURL(url)
	}
	return app.Run(ctx, os.Args[1:])
}

// Run executes the command line args and returns the exit code. Errors
// are classified and printed to Err with their hint.
func (a *App) Run(ctx context.Context, args []string) int {
	root := NewRootCmd(a)
	root.SetArgs(args)
	root.SetOut(a.Out)
	root.SetErr(a.Err)
	cmd, err := root.ExecuteContextC(ctx)
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	if err != nil {
		// Error messages can quote Google responses and user data.
		fmt.Fprintln(a.Err, "error:", output.Sanitize(a.explain(cmd, err).Error()))
		return 1
	}
	return 0
}

// explain classifies err using the service of cmd's group, if any.
func (a *App) explain(cmd *cobra.Command, err error) error {
	var svc auth.Service
	for c := cmd; c != nil; c = c.Parent() {
		if s, ok := c.Annotations[annotationService]; ok {
			svc = auth.Service(s)
			break
		}
	}
	err = auth.ClassifyService(err, svc)
	if errors.Is(err, context.DeadlineExceeded) && auth.HintFor(err) == "" {
		return fmt.Errorf("%w\nhint: the command exceeded --timeout (%s); raise it with --timeout", err, a.Flags.Timeout)
	}
	return err
}

// NewRootCmd builds the command tree bound to app.
func NewRootCmd(a *App) *cobra.Command {
	root := &cobra.Command{
		Use:   "gwork",
		Short: "Access Gmail, Drive, Chat and Calendar (read-only by default)",
		Long: "gwork gives humans and AI agents access to Google Workspace (Gmail, Google\n" +
			"Drive, Google Chat and Google Calendar), as a CLI and as an MCP server.\n" +
			"It is read-only unless write access is explicitly granted (gwork auth login\n" +
			"--write) and, for MCP, enabled (gwork mcp --allow-write).",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := a.Format(); err != nil {
				return err
			}
			if a.Flags.Timeout > 0 && !hasAnnotation(cmd, annotationNoTimeout) {
				ctx, cancel := context.WithTimeout(cmd.Context(), a.Flags.Timeout)
				a.cancel = cancel
				cmd.SetContext(ctx)
			}
			return nil
		},
	}
	root.CompletionOptions.HiddenDefaultCmd = true

	f := root.PersistentFlags()
	f.StringVar(&a.Flags.Account, "account", "", "account email to use (default: $GWORK_ACCOUNT or the default account)")
	f.StringVar(&a.Flags.Credentials, "credentials", "", "path to an OAuth client credentials.json (default: $GWORK_CREDENTIALS, <config>/credentials.json)")
	f.StringVarP(&a.Flags.Output, "output", "o", "text", "output format: text or json")
	f.BoolVar(&a.Flags.JSON, "json", false, "shortcut for --output json")
	f.DurationVar(&a.Flags.Timeout, "timeout", DefaultTimeout, "timeout for API commands (0 disables)")

	root.AddCommand(
		newVersionCmd(a),
		newAuthCmd(a),
		newGmailCmd(a),
		newCalendarCmd(a),
		newDriveCmd(a),
		newChatCmd(a),
		newMCPCmd(a),
	)
	return root
}

func hasAnnotation(cmd *cobra.Command, key string) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if _, ok := c.Annotations[key]; ok {
			return true
		}
	}
	return false
}

// serviceGroup returns a service group command annotated with svc.
func serviceGroup(svc auth.Service, short string) *cobra.Command {
	return &cobra.Command{
		Use:         string(svc),
		Short:       short,
		Annotations: map[string]string{annotationService: string(svc)},
	}
}
