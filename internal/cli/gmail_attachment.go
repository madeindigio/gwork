package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/digio/gwork-cli/internal/auth"
	"github.com/digio/gwork-cli/internal/fsutil"
	"github.com/digio/gwork-cli/internal/workspace/gmail"
)

// gmailAttachmentResult is the result of "gwork gmail attachment".
type gmailAttachmentResult struct {
	MessageID    string `json:"message_id"`
	AttachmentID string `json:"attachment_id"`
	Path         string `json:"path"`
	Size         int    `json:"size"`
}

func newGmailAttachmentCmd(app *App) *cobra.Command {
	var (
		out   string
		force bool
	)
	cmd := &cobra.Command{
		Use:   "attachment <messageId> <attachmentId> --out <path>",
		Short: "Download a message attachment to a file",
		Long: "Download a message attachment to a file. Attachment ids are listed by\n" +
			"\"gwork gmail get <messageId>\". Existing files are only replaced with --force.\n" +
			"The file is created with mode 0600.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if out == "" {
				return errors.New("--out is required")
			}
			if err := fsutil.CheckDest(out, force); err != nil {
				return err
			}
			ctx := cmd.Context()
			opts, err := app.ClientOptions(ctx, auth.Gmail)
			if err != nil {
				return err
			}
			data, err := gmail.GetAttachment(ctx, args[0], args[1], opts...)
			if err != nil {
				return err
			}
			if _, err := fsutil.WriteFile(out, bytes.NewReader(data), force); err != nil {
				return err
			}
			res := gmailAttachmentResult{MessageID: args[0], AttachmentID: args[1], Path: out, Size: len(data)}
			return app.Print(res, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "wrote %d bytes to %s\n", res.Size, res.Path)
				return err
			})
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "destination file path (required)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite the destination file if it exists")
	return cmd
}
