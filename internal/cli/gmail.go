package cli

import (
	"errors"
	"io"

	"github.com/spf13/cobra"

	"github.com/madeindigio/gwork/internal/auth"
	"github.com/madeindigio/gwork/internal/output"
	"github.com/madeindigio/gwork/internal/workspace/gmail"
)

// newGmailCmd returns the "gwork gmail" command group.
func newGmailCmd(app *App) *cobra.Command {
	cmd := serviceGroup(auth.Gmail, "Read Gmail messages, threads, labels and attachments; drafts, send, labels and trash (needs auth login --write)")
	cmd.AddCommand(
		newGmailSearchCmd(app),
		newGmailGetCmd(app),
		newGmailThreadCmd(app),
		newGmailLabelsCmd(app),
		newGmailAttachmentCmd(app),
	)
	cmd.AddCommand(gmailWriteCommands(app)...)
	return cmd
}

func newGmailSearchCmd(app *App) *cobra.Command {
	var (
		maxResults       int
		includeSpamTrash bool
	)
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search messages with Gmail query syntax",
		Long: "Search messages with Gmail query syntax, e.g.\n" +
			"  gwork gmail search 'from:alice subject:report after:2026/09/01'\n" +
			"  gwork gmail search 'has:attachment is:unread' --max 50",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if maxResults <= 0 {
				return errors.New("--max must be positive")
			}
			ctx := cmd.Context()
			opts, err := app.ClientOptions(ctx, auth.Gmail)
			if err != nil {
				return err
			}
			msgs, err := gmail.Search(ctx, args[0], gmail.SearchOptions{
				MaxResults:       maxResults,
				IncludeSpamTrash: includeSpamTrash,
			}, opts...)
			if err != nil {
				return err
			}
			return app.Print(msgs, func(w io.Writer) error {
				rows := make([][]string, 0, len(msgs))
				for _, m := range msgs {
					rows = append(rows, []string{
						output.DateTime(m.Date, app.Location()),
						output.Ellipsize(m.From, 30),
						output.Ellipsize(m.Subject, 60),
						m.ID,
					})
				}
				return output.Table(w, []string{"DATE", "FROM", "SUBJECT", "ID"}, rows)
			})
		},
	}
	cmd.Flags().IntVar(&maxResults, "max", gmail.DefaultMaxResults, "maximum number of messages")
	cmd.Flags().BoolVar(&includeSpamTrash, "include-spam-trash", false, "also search Spam and Trash")
	return cmd
}

func newGmailLabelsCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "labels",
		Short: "List Gmail labels",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			opts, err := app.ClientOptions(ctx, auth.Gmail)
			if err != nil {
				return err
			}
			labels, err := gmail.ListLabels(ctx, opts...)
			if err != nil {
				return err
			}
			return app.Print(labels, func(w io.Writer) error {
				rows := make([][]string, 0, len(labels))
				for _, l := range labels {
					rows = append(rows, []string{l.ID, l.Name, l.Type})
				}
				return output.Table(w, []string{"ID", "NAME", "TYPE"}, rows)
			})
		},
	}
}
