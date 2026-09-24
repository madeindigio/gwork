package cli

import (
	"github.com/spf13/cobra"

	"github.com/digio/gwork-cli/internal/auth"
)

// newChatCmd returns the "gwork chat" command group.
func newChatCmd(app *App) *cobra.Command {
	cmd := serviceGroup(auth.Chat, "Read Google Chat spaces and messages")
	_ = app // Phase 2: cmd.AddCommand(newChatXxxCmd(app), ...)
	return cmd
}
