package cli

import (
	"github.com/spf13/cobra"

	"github.com/digio/gwork-cli/internal/auth"
)

// newGmailCmd returns the "gwork gmail" command group.
func newGmailCmd(app *App) *cobra.Command {
	cmd := serviceGroup(auth.Gmail, "Read Gmail messages, threads, labels and attachments")
	_ = app // Phase 2: cmd.AddCommand(newGmailXxxCmd(app), ...)
	return cmd
}
