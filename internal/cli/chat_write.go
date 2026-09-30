package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/madeindigio/gwork/internal/auth"
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
}

func newChatSendCmd(app *App) *cobra.Command {
	var space, to, text, textFile, thread string
	var wf writeFlags
	cmd := &cobra.Command{
		Use:   "send (--space SPACE | --to EMAIL) (--text TEXT | --text-file PATH|-) [--thread THREAD]",
		Short: "Send a Chat message as you to a space, a DM or a thread",
		Long: "Send a Google Chat message posted as you. The message goes to other\n" +
			"people, so confirmation is always required: pass --yes to skip the prompt\n" +
			"or --dry-run to preview.\n\n" +
			"--to sends to the existing direct message with that user; it never creates\n" +
			"a space, so start the conversation from Chat first. --thread replies in a\n" +
			"thread of the target space (spaces/X/threads/Y). --text-file - reads the\n" +
			"text from stdin, which then requires --yes or --dry-run.",
		Example: "  gwork chat send --space spaces/AAAA --text 'Deploy done' --yes\n" +
			"  gwork chat send --to ana@example.com --text-file msg.txt",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			flags := cmd.Flags()
			if flags.Changed("text") == flags.Changed("text-file") {
				return errors.New("specify exactly one of --text or --text-file")
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
			in := chat.SendInput{Space: space, UserEmail: to, Text: text, Thread: thread}
			target, threadName, err := in.Validate()
			if err != nil {
				return err
			}
			if target == "" {
				target = strings.TrimSpace(to)
			}
			if wf.DryRun {
				p := chatSendPreview{Thread: threadName, Text: text}
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
			summary += fmt.Sprintf(": %q.", output.Ellipsize(strings.Join(strings.Fields(text), " "), 80))
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
				return output.KeyValues(w,
					"Sent", msg.Name,
					"Space", msg.Space,
					"Thread", msg.Thread,
					"Time", output.DateTime(msg.CreateTime, app.Location()),
					"Text", output.Ellipsize(strings.Join(strings.Fields(msg.Text), " "), 80),
				)
			})
		},
	}
	cmd.Flags().StringVar(&space, "space", "", "target space (spaces/XXX or XXX)")
	cmd.Flags().StringVar(&to, "to", "", "email of the user whose existing DM to post in")
	cmd.Flags().StringVar(&text, "text", "", "message text")
	cmd.Flags().StringVar(&textFile, "text-file", "", "read the message text from this file, or - for stdin")
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
