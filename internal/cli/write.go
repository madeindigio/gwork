package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/api/option"

	"github.com/madeindigio/gwork/internal/auth"
	"github.com/madeindigio/gwork/internal/output"
)

// writeFlags are the flags shared by every command that changes data.
type writeFlags struct {
	// Yes skips the interactive confirmation.
	Yes bool
	// DryRun prints what would be done without calling Google.
	DryRun bool
}

// addWriteFlags registers --yes/-y and --dry-run on cmd.
func addWriteFlags(cmd *cobra.Command, f *writeFlags) {
	cmd.Flags().BoolVarP(&f.Yes, "yes", "y", false, "do not ask for confirmation")
	cmd.Flags().BoolVar(&f.DryRun, "dry-run", false, "print what would be done and exit without calling Google")
}

// WriteClientOptions returns client options for write operations on svc,
// failing with an actionable error (hint: gwork auth login --services <svc>
// --write <svc>) when the account has not granted write access.
//
//	opts, err := app.WriteClientOptions(cmd.Context(), auth.Gmail)
func (a *App) WriteClientOptions(ctx context.Context, svc auth.Service) ([]option.ClientOption, error) {
	p, err := a.Provider(ctx)
	if err != nil {
		return nil, err
	}
	opts, err := p.WriteClientOptions(ctx, svc)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", svc, err)
	}
	return opts, nil
}

// classifyWrite classifies an error returned by a write call so that a
// missing-scope error carries the write login hint. Return it from write
// commands instead of the raw error; the root leaves classified errors alone.
func classifyWrite(err error, svc auth.Service) error {
	return auth.ClassifyWrite(err, svc)
}

// confirmWrite asks the user to confirm a change described by summary. It
// returns nil when f.Yes is set or the user answers y/yes on an interactive
// terminal; otherwise it returns an error (declined, or non-interactive
// without --yes). The prompt goes to stderr.
func (a *App) confirmWrite(f writeFlags, summary string) error {
	if f.Yes {
		return nil
	}
	if a.IsTerminal == nil || !a.IsTerminal() || a.In == nil {
		return errors.New("confirmation required but stdin is not a terminal: pass --yes to proceed (or --dry-run to preview)")
	}
	fmt.Fprintf(a.Err, "%s Proceed? [y/N]: ", output.Sanitize(summary))
	line, err := bufio.NewReader(a.In).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read confirmation: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	}
	return errors.New("aborted: nothing was changed")
}

// printDryRun prints the request a write command would send: JSON with
// --json, indented JSON text otherwise, preceded by a note on stderr.
func (a *App) printDryRun(v any) error {
	if !a.JSON() {
		fmt.Fprintln(a.Err, "dry run: nothing was changed")
	}
	return a.Print(v, func(w io.Writer) error {
		return output.WriteJSON(w, v)
	})
}
