package cli

import (
	"github.com/spf13/cobra"

	"github.com/digio/gwork-cli/internal/auth"
)

// newDriveCmd returns the "gwork drive" command group.
func newDriveCmd(app *App) *cobra.Command {
	cmd := serviceGroup(auth.Drive, "Search, read and download Google Drive files")
	_ = app // Phase 2: cmd.AddCommand(newDriveXxxCmd(app), ...)
	return cmd
}
