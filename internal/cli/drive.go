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
	"github.com/digio/gwork-cli/internal/workspace/drive"
)

// newDriveCmd returns the "gwork drive" command group.
func newDriveCmd(app *App) *cobra.Command {
	cmd := serviceGroup(auth.Drive, "Search, read and download Google Drive files")
	cmd.AddCommand(
		newDriveSearchCmd(app),
		newDriveGetCmd(app),
		newDriveReadCmd(app),
		newDriveDownloadCmd(app),
	)
	return cmd
}

// newDriveSearchCmd returns "gwork drive search".
func newDriveSearchCmd(app *App) *cobra.Command {
	var opts drive.SearchOptions
	cmd := &cobra.Command{
		Use:   "search [text]",
		Short: "Search files in My Drive and shared drives",
		Long: "Search Drive files, including shared drives. All filters are combined with AND\n" +
			"and trashed files are excluded. [text] matches names and content; --query adds a\n" +
			"raw Drive query (https://developers.google.com/drive/api/guides/search-files).\n" +
			"Results are sorted by modification time, or by relevance when [text] is given.",
		Example: "  gwork drive search budget --type sheet\n" +
			"  gwork drive search --name \"Q3 plan\" --modified-after 30d\n" +
			"  gwork drive search --folder <folderId> --max 100 --json",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				opts.Text = args[0]
			}
			opts.Now = app.CurrentTime()
			ctx := cmd.Context()
			copts, err := app.ClientOptions(ctx, auth.Drive)
			if err != nil {
				return err
			}
			files, err := drive.Search(ctx, opts, copts...)
			if err != nil {
				return err
			}
			return app.Print(files, func(w io.Writer) error {
				if len(files) == 0 {
					_, err := fmt.Fprintln(w, "No files found.")
					return err
				}
				rows := make([][]string, 0, len(files))
				for _, f := range files {
					rows = append(rows, []string{f.ID, f.Type, formatDriveTime(f.ModifiedTime), formatDriveSize(f.Size), output.Ellipsize(f.Name, 60)})
				}
				return output.Table(w, []string{"ID", "TYPE", "MODIFIED", "SIZE", "NAME"}, rows)
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.Name, "name", "", "file name contains this text")
	f.StringVar(&opts.Type, "type", "", "file type: "+strings.Join(drive.TypeShortcuts(), ", "))
	f.StringVar(&opts.MimeType, "mime", "", "exact MIME type (e.g. application/pdf)")
	f.StringVar(&opts.Owner, "owner", "", "owner email address")
	f.StringVar(&opts.FolderID, "folder", "", "only direct children of this folder ID")
	f.StringVar(&opts.ModifiedAfter, "modified-after", "", "modified after this time (2026-09-01, 7d, RFC 3339)")
	f.StringVar(&opts.RawQuery, "query", "", "raw Drive query ANDed with the other filters")
	f.IntVar(&opts.Max, "max", drive.DefaultMaxResults, "maximum number of results (up to 1000)")
	f.StringVar(&opts.OrderBy, "order-by", "", `sort order, e.g. "name" or "modifiedTime desc" (default "modifiedTime desc", relevance for text searches)`)
	return cmd
}

// newDriveGetCmd returns "gwork drive get".
func newDriveGetCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "get <fileId>",
		Short: "Show the metadata of a file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			copts, err := app.ClientOptions(ctx, auth.Drive)
			if err != nil {
				return err
			}
			f, err := drive.GetFile(ctx, args[0], copts...)
			if err != nil {
				return err
			}
			return app.Print(f, func(w io.Writer) error {
				pairs := []string{
					"ID", f.ID,
					"Name", f.Name,
					"Type", f.Type,
					"MIME type", f.MimeType,
					"Size", formatDriveSize(f.Size),
					"Created", formatDriveTime(f.CreatedTime),
					"Modified", formatDriveTime(f.ModifiedTime),
					"Last modified by", f.LastModifyingUser,
					"Owners", strings.Join(f.Owners, ", "),
					"Shared", strconv.FormatBool(f.Shared),
					"Parents", strings.Join(f.Parents, ", "),
					"Shared drive", f.DriveID,
					"Link", f.WebViewLink,
					"Description", f.Description,
				}
				if f.ShortcutTargetID != "" {
					pairs = append(pairs, "Shortcut target", f.ShortcutTargetID)
				}
				if len(f.ExportLinks) > 0 {
					pairs = append(pairs, "Export formats", strings.Join(f.ExportLinks, ", "))
				}
				return output.KeyValues(w, pairs...)
			})
		},
	}
}

// newDriveReadCmd returns "gwork drive read".
func newDriveReadCmd(app *App) *cobra.Command {
	var opts drive.ReadOptions
	cmd := &cobra.Command{
		Use:   "read <fileId>",
		Short: "Print the text content of a file",
		Long: "Print the text content of a file. Google Docs are exported as Markdown (or plain\n" +
			"text with --format txt), Sheets as CSV (first sheet only) and Slides as plain text.\n" +
			"Text-like files (text/*, JSON, XML, YAML...) are printed as-is. Binary files (PDF,\n" +
			"images, Office documents) are rejected: use 'gwork drive download' instead.\n" +
			"Content beyond --max-bytes is cut; notes and truncation warnings go to stderr.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			copts, err := app.ClientOptions(ctx, auth.Drive)
			if err != nil {
				return err
			}
			c, err := drive.Read(ctx, args[0], opts, copts...)
			if err != nil {
				return err
			}
			return app.Print(c, func(w io.Writer) error {
				for _, n := range c.Notes {
					fmt.Fprintln(app.Err, "note:", n)
				}
				if c.Truncated {
					fmt.Fprintf(app.Err, "warning: content truncated to %d bytes (raise --max-bytes)\n", c.MaxBytes)
				}
				if _, err := io.WriteString(w, c.Text); err != nil {
					return err
				}
				if c.Text != "" && !strings.HasSuffix(c.Text, "\n") {
					_, err := io.WriteString(w, "\n")
					return err
				}
				return nil
			})
		},
	}
	f := cmd.Flags()
	f.Int64Var(&opts.MaxBytes, "max-bytes", drive.DefaultMaxBytes, "maximum bytes of content to return")
	f.StringVar(&opts.Format, "format", "", "export format of Google files: md|txt (Docs), csv (Sheets), txt (Slides)")
	return cmd
}

// newDriveDownloadCmd returns "gwork drive download".
func newDriveDownloadCmd(app *App) *cobra.Command {
	var opts drive.DownloadOptions
	cmd := &cobra.Command{
		Use:   "download <fileId> --out <path>",
		Short: "Download a file to disk",
		Long: "Download a file to disk. Regular files are saved as-is. Google files are exported:\n" +
			"by default Docs as docx, Sheets as xlsx, Slides as pptx and Drawings as pdf; choose\n" +
			"another format with --export-format. Forms cannot be exported. The file is written\n" +
			"atomically (temporary file + rename) with mode 0600; existing files are only\n" +
			"replaced with --force. Raise --timeout for very large files.",
		Example: "  gwork drive download <fileId> --out report.pdf\n" +
			"  gwork drive download <docId> --out plan.pdf --export-format pdf",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			copts, err := app.ClientOptions(ctx, auth.Drive)
			if err != nil {
				return err
			}
			res, err := drive.Download(ctx, args[0], opts, copts...)
			if err != nil {
				return err
			}
			return app.Print(res, func(w io.Writer) error {
				how := res.ContentMimeType
				if res.Exported {
					how = "exported as " + res.ExportFormat
				}
				_, err := fmt.Fprintf(w, "Saved %s (%d bytes, %s)\n", res.Path, res.Bytes, how)
				return err
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.Out, "out", "", "destination file path (required)")
	f.StringVar(&opts.ExportFormat, "export-format", "", "export format of Google files: "+strings.Join(drive.ExportFormatNames(), ", "))
	f.BoolVar(&opts.Force, "force", false, "overwrite the destination if it exists")
	_ = cmd.MarkFlagRequired("out")
	return cmd
}

// formatDriveTime formats a timestamp for tables, or "" for the zero time.
func formatDriveTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04")
}

// formatDriveSize formats a byte count in human units, or "" for 0 (Google
// files have no size).
func formatDriveSize(n int64) string {
	const unit = 1024
	if n <= 0 {
		return ""
	}
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
