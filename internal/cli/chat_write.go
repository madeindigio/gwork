package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/madeindigio/gwork/internal/auth"
	"github.com/madeindigio/gwork/internal/fsutil"
	"github.com/madeindigio/gwork/internal/output"
	"github.com/madeindigio/gwork/internal/workspace/chat"
)

// maxTextFileBytes bounds how much of --text-file is read (the API limit is
// 4096 characters, at most 4 bytes each in UTF-8, plus slack).
const maxTextFileBytes = 64 * 1024

// chatSendPreview is the request printed by --dry-run.
type chatSendPreview struct {
	Space  string `json:"space,omitempty"`
	To     string `json:"to,omitempty"`
	Thread string `json:"thread,omitempty"`
	Text   string `json:"text"`
	// Attachments lists the files that would be uploaded (no content).
	Attachments []uploadPreview `json:"attachments,omitempty"`
}

// uploadPreview describes a file to upload in --dry-run output.
type uploadPreview struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
}

func newChatSendCmd(app *App) *cobra.Command {
	var space, to, text, textFile, thread string
	var attach []string
	var wf writeFlags
	cmd := &cobra.Command{
		Use:   "send (--space SPACE | --to EMAIL) [--text TEXT | --text-file PATH|-] [--attach PATH]... [--thread THREAD]",
		Short: "Send a Chat message as you to a space, a DM or a thread",
		Long: "Send a Google Chat message posted as you. The message goes to other\n" +
			"people, so confirmation is always required: pass --yes to skip the prompt\n" +
			"or --dry-run to preview.\n\n" +
			"--to sends to the existing direct message with that user; it never creates\n" +
			"a space, so start the conversation from Chat first. --thread replies in a\n" +
			"thread of the target space (spaces/X/threads/Y). --text-file - reads the\n" +
			"text from stdin, which then requires --yes or --dry-run.\n\n" +
			"--attach uploads a local file as an attachment (repeatable, at most 200 MB\n" +
			"each); the text is optional when at least one file is attached. Several\n" +
			"files in one message must all be images or videos.",
		Example: "  gwork chat send --space spaces/AAAA --text 'Deploy done' --yes\n" +
			"  gwork chat send --to ana@example.com --text-file msg.txt\n" +
			"  gwork chat send --space spaces/AAAA --text 'Report' --attach photo.png --attach chart.jpg",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			flags := cmd.Flags()
			if flags.Changed("text") && flags.Changed("text-file") {
				return errors.New("specify exactly one of --text or --text-file")
			}
			if !flags.Changed("text") && !flags.Changed("text-file") && len(attach) == 0 {
				return errors.New("specify exactly one of --text or --text-file, or attach a file with --attach")
			}
			if textFile == "-" && !wf.Yes && !wf.DryRun {
				return errors.New("--text-file - consumes stdin, so the confirmation prompt cannot be shown: pass --yes (or --dry-run to preview)")
			}
			if flags.Changed("text-file") {
				var err error
				if text, err = readTextFile(app, textFile); err != nil {
					return err
				}
			}
			// Read attachments before validation, the dry run, the prompt and
			// any network call, so a bad path never half-sends.
			files, err := fsutil.ReadFiles(attach, chat.MaxAttachmentSize, 0)
			if err != nil {
				return fmt.Errorf("attach: %w", err)
			}
			in := chat.SendInput{Space: space, UserEmail: to, Text: text, Thread: thread}
			for _, f := range files {
				in.Attachments = append(in.Attachments, chat.Upload{Filename: f.Name, ContentType: chat.DetectContentType(f.Name, f.Data), Data: f.Data})
			}
			target, threadName, err := in.Validate()
			if err != nil {
				return err
			}
			if target == "" {
				target = strings.TrimSpace(to)
			}
			if wf.DryRun {
				p := chatSendPreview{Thread: threadName, Text: text}
				for _, f := range files {
					p.Attachments = append(p.Attachments, uploadPreview{Filename: f.Name, ContentType: chat.DetectContentType(f.Name, f.Data), Size: int64(len(f.Data))})
				}
				if strings.TrimSpace(to) != "" {
					p.To = target
				} else {
					p.Space = target
				}
				return app.printDryRun(p)
			}
			summary := "Send to " + target
			if threadName != "" {
				summary += " (reply in " + threadName + ")"
			}
			if strings.TrimSpace(text) != "" {
				summary += fmt.Sprintf(": %q", output.Ellipsize(strings.Join(strings.Fields(text), " "), 80))
			}
			if len(files) > 0 {
				summary += " with attachments " + describeUploads(files)
			}
			summary += "."
			if err := app.confirmWrite(wf, summary); err != nil {
				return err
			}
			opts, err := app.WriteClientOptions(ctx, auth.Chat)
			if err != nil {
				return err
			}
			svc, err := chat.New(ctx, opts...)
			if err != nil {
				return err
			}
			msg, err := chat.SendMessage(ctx, svc, in)
			if err != nil {
				return classifyWrite(err, auth.Chat)
			}
			return app.Print(msg, func(w io.Writer) error {
				names := make([]string, 0, len(msg.Attachments))
				for _, a := range msg.Attachments {
					names = append(names, a.ContentName)
				}
				return output.KeyValues(w,
					"Sent", msg.Name,
					"Space", msg.Space,
					"Thread", msg.Thread,
					"Time", output.DateTime(msg.CreateTime, app.Location()),
					"Text", output.Ellipsize(strings.Join(strings.Fields(msg.Text), " "), 80),
					"Attachments", strings.Join(names, ", "),
				)
			})
		},
	}
	cmd.Flags().StringVar(&space, "space", "", "target space (spaces/XXX or XXX)")
	cmd.Flags().StringVar(&to, "to", "", "email of the user whose existing DM to post in")
	cmd.Flags().StringVar(&text, "text", "", "message text")
	cmd.Flags().StringVar(&textFile, "text-file", "", "read the message text from this file, or - for stdin")
	cmd.Flags().StringArrayVar(&attach, "attach", nil, "attach this local file (repeatable)")
	cmd.Flags().StringVar(&thread, "thread", "", "reply in this thread (spaces/XXX/threads/YYY)")
	addWriteFlags(cmd, &wf)
	return cmd
}

// readTextFile reads the message text from path, or from the App's stdin
// when path is "-".
func readTextFile(app *App, path string) (string, error) {
	var r io.Reader
	if path == "-" {
		if app.In == nil {
			return "", errors.New("no stdin available for --text-file -")
		}
		r = app.In
	} else {
		f, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("read text file: %w", err)
		}
		defer f.Close()
		r = f
	}
	data, err := io.ReadAll(io.LimitReader(r, maxTextFileBytes+1))
	if err != nil {
		return "", fmt.Errorf("read text file: %w", err)
	}
	if len(data) > maxTextFileBytes {
		return "", fmt.Errorf("text file is larger than %d bytes; a Chat message is limited to %d characters", maxTextFileBytes, chat.MaxTextLength)
	}
	return strings.TrimRight(string(data), "\r\n"), nil
}

// describeUploads renders "a.pdf (1.2 MB), b.txt (12 B)" for prompts.
func describeUploads(files []fsutil.File) string {
	parts := make([]string, 0, len(files))
	for _, f := range files {
		parts = append(parts, fmt.Sprintf("%s (%s)", f.Name, output.Bytes(int64(len(f.Data)))))
	}
	return strings.Join(parts, ", ")
}
