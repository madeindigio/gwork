package cli

import (
	"github.com/spf13/cobra"

	"github.com/digio/gwork-cli/internal/auth"
)

// newCalendarCmd returns the "gwork calendar" command group.
func newCalendarCmd(app *App) *cobra.Command {
	cmd := serviceGroup(auth.Calendar, "Read Google Calendar calendars and events")
	_ = app // Phase 2: cmd.AddCommand(newCalendarXxxCmd(app), ...)
	return cmd
}
