package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/digio/gwork-cli/internal/auth"
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
			"\"gwork gmail get <messageId>\". Existing files are only replaced with --force.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if out == "" {
				return errors.New("--out is required")
			}
			if !force {
				if _, err := os.Stat(out); err == nil {
					return fmt.Errorf("%s already exists (use --force to overwrite)", out)
				}
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
			if err := gmailWriteFileAtomic(out, data, force); err != nil {
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

// gmailWriteFileAtomic writes data to path through a temporary file in the same
// directory, so readers never see a partial file. Without overwrite it
// fails when path already exists.
func gmailWriteFileAtomic(path string, data []byte, overwrite bool) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".gwork-*.part")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err = os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	if overwrite {
		if err = os.Rename(tmpName, path); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		return nil
	}
	// A hard link fails atomically when path exists; fall back to a
	// check-then-rename on filesystems without hard links.
	if lerr := os.Link(tmpName, path); lerr != nil {
		if errors.Is(lerr, fs.ErrExist) {
			err = fmt.Errorf("%s already exists (use --force to overwrite)", path)
			return err
		}
		if _, serr := os.Lstat(path); serr == nil {
			err = fmt.Errorf("%s already exists (use --force to overwrite)", path)
			return err
		}
		if err = os.Rename(tmpName, path); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		return nil
	}
	_ = os.Remove(tmpName)
	return nil
}
