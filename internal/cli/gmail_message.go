package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/digio/gwork-cli/internal/auth"
	"github.com/digio/gwork-cli/internal/output"
	"github.com/digio/gwork-cli/internal/workspace/gmail"
)

func newGmailGetCmd(app *App) *cobra.Command {
	var rawHTML bool
	cmd := &cobra.Command{
		Use:   "get <messageId>",
		Short: "Show a message as text (headers, body and attachments)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			opts, err := app.ClientOptions(ctx, auth.Gmail)
			if err != nil {
				return err
			}
			msg, err := gmail.GetMessage(ctx, args[0], gmail.GetOptions{IncludeHTML: rawHTML}, opts...)
			if err != nil {
				return err
			}
			return app.Print(msg, func(w io.Writer) error {
				return writeGmailMessage(w, msg, rawHTML, app.Location())
			})
		},
	}
	cmd.Flags().BoolVar(&rawHTML, "raw-html", false, "print the raw HTML body instead of the text rendering (JSON: adds the html field)")
	return cmd
}

func newGmailThreadCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "thread <threadId>",
		Short: "Show every message of a thread as text",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			opts, err := app.ClientOptions(ctx, auth.Gmail)
			if err != nil {
				return err
			}
			th, err := gmail.GetThread(ctx, args[0], gmail.GetOptions{}, opts...)
			if err != nil {
				return err
			}
			return app.Print(th, func(w io.Writer) error {
				for i := range th.Messages {
					if _, err := fmt.Fprintf(w, "=== Message %d/%d ===\n", i+1, len(th.Messages)); err != nil {
						return err
					}
					if err := writeGmailMessage(w, &th.Messages[i], false, app.Location()); err != nil {
						return err
					}
					if i < len(th.Messages)-1 {
						if _, err := fmt.Fprintln(w); err != nil {
							return err
						}
					}
				}
				return nil
			})
		},
	}
}

// writeGmailMessage renders a message as headers, a blank line, the body
// and the attachment list. Dates are shown in loc.
func writeGmailMessage(w io.Writer, m *gmail.Message, rawHTML bool, loc *time.Location) error {
	if err := output.KeyValues(w,
		"ID", m.ID,
		"Thread", m.ThreadID,
		"From", m.From,
		"To", m.To,
		"Cc", m.Cc,
		"Subject", m.Subject,
		"Date", output.DateTime(m.Date, loc),
		"Labels", strings.Join(m.Labels, ", "),
	); err != nil {
		return err
	}
	body := m.Body
	if rawHTML && m.HTML != "" {
		body = m.HTML
	}
	if body == "" {
		body = "(no text body)"
	}
	if _, err := fmt.Fprintf(w, "\n%s\n", strings.TrimRight(body, "\n")); err != nil {
		return err
	}
	if len(m.Attachments) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(w, "\nAttachments (%d):\n", len(m.Attachments)); err != nil {
		return err
	}
	rows := make([][]string, 0, len(m.Attachments))
	for _, a := range m.Attachments {
		rows = append(rows, []string{a.Filename, a.MimeType, strconv.FormatInt(a.Size, 10), a.AttachmentID})
	}
	return output.Table(w, []string{"FILENAME", "TYPE", "SIZE", "ATTACHMENT_ID"}, rows)
}
