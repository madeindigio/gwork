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
	"github.com/madeindigio/gwork/internal/workspace/gmail"
)

// maxBodyFileBytes bounds --body-file input.
const maxBodyFileBytes = 25 << 20

// composeFlags are the flags shared by "gmail draft create" and "gmail send".
type composeFlags struct {
	to, cc, bcc []string
	subject     string
	body        string
	bodyFile    string
	replyTo     string
	replyAll    bool
}

func (f *composeFlags) add(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringSliceVar(&f.to, "to", nil, "recipient address (repeatable or comma-separated)")
	fl.StringSliceVar(&f.cc, "cc", nil, "Cc address (repeatable or comma-separated)")
	fl.StringSliceVar(&f.bcc, "bcc", nil, "Bcc address (repeatable or comma-separated)")
	fl.StringVar(&f.subject, "subject", "", "subject (default on replies: \"Re: <original>\")")
	fl.StringVar(&f.body, "body", "", "plain-text body")
	fl.StringVar(&f.bodyFile, "body-file", "", "read the body from this file, or from stdin with -")
	fl.StringVar(&f.replyTo, "reply-to", "", "ID of the message to reply to (same thread, recipient defaults to its Reply-To/From)")
	fl.BoolVar(&f.replyAll, "reply-all", false, "with --reply-to, also address the original To and Cc recipients")
}

// input validates the flags and reads the body.
func (f *composeFlags) input(app *App) (gmail.ComposeInput, error) {
	if f.body != "" && f.bodyFile != "" {
		return gmail.ComposeInput{}, errors.New("--body and --body-file are mutually exclusive")
	}
	if f.replyAll && f.replyTo == "" {
		return gmail.ComposeInput{}, errors.New("--reply-all requires --reply-to")
	}
	body := f.body
	if f.bodyFile != "" {
		b, err := readBodyFile(app, f.bodyFile)
		if err != nil {
			return gmail.ComposeInput{}, err
		}
		body = b
	}
	return gmail.ComposeInput{
		To: f.to, Cc: f.cc, Bcc: f.bcc,
		Subject: f.subject, Body: body,
		ReplyToMessageID: f.replyTo, ReplyAll: f.replyAll,
	}, nil
}

func readBodyFile(app *App, path string) (string, error) {
	var r io.Reader
	if path == "-" {
		if app.In == nil {
			return "", errors.New("no standard input available for --body-file -")
		}
		r = app.In
	} else {
		f, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("read body file: %w", err)
		}
		defer f.Close()
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, maxBodyFileBytes+1))
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}
	if len(b) > maxBodyFileBytes {
		return "", fmt.Errorf("body exceeds %s", output.Bytes(maxBodyFileBytes))
	}
	return string(b), nil
}

// recipients lists every explicit recipient, for the confirmation prompt.
func recipients(in gmail.ComposeInput) string {
	all := append(append(append([]string{}, in.To...), in.Cc...), in.Bcc...)
	if len(all) == 0 {
		if in.ReplyToMessageID != "" {
			return "the recipients of message " + in.ReplyToMessageID
		}
		return "nobody"
	}
	return strings.Join(all, ", ")
}

func newGmailDraftCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "draft",
		Short: "Create and send Gmail drafts (needs write access)",
	}
	cmd.AddCommand(newGmailDraftCreateCmd(app), newGmailDraftSendCmd(app))
	return cmd
}

func newGmailDraftCreateCmd(app *App) *cobra.Command {
	var (
		cf composeFlags
		wf writeFlags
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a draft (nothing is sent)",
		Long: "Create a draft, optionally as a reply. Nothing is sent; send it later with\n" +
			"'gwork gmail draft send', or from Gmail.\n" +
			"  gwork gmail draft create --to ana@example.com --subject Hi --body 'Hello'\n" +
			"  gwork gmail draft create --reply-to 18a1b2 --body-file reply.txt",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			in, err := cf.input(app)
			if err != nil {
				return err
			}
			if wf.DryRun {
				return app.printDryRun(map[string]any{"action": "create_draft", "message": in})
			}
			ctx := cmd.Context()
			opts, err := app.WriteClientOptions(ctx, auth.Gmail)
			if err != nil {
				return err
			}
			res, err := gmail.CreateDraft(ctx, in, opts...)
			if err != nil {
				return classifyWrite(err, auth.Gmail)
			}
			return app.Print(res, func(w io.Writer) error {
				return output.KeyValues(w, "Draft", res.DraftID, "Message", res.MessageID, "Thread", res.ThreadID)
			})
		},
	}
	cf.add(cmd)
	addWriteFlags(cmd, &wf)
	return cmd
}

func newGmailDraftSendCmd(app *App) *cobra.Command {
	var wf writeFlags
	cmd := &cobra.Command{
		Use:   "send <draft-id>",
		Short: "Send an existing draft (asks for confirmation)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if wf.DryRun {
				return app.printDryRun(map[string]any{"action": "send_draft", "draft_id": id})
			}
			if err := app.confirmWrite(wf, fmt.Sprintf("Send draft %s to its recipients.", id)); err != nil {
				return err
			}
			ctx := cmd.Context()
			opts, err := app.WriteClientOptions(ctx, auth.Gmail)
			if err != nil {
				return err
			}
			res, err := gmail.SendDraft(ctx, id, opts...)
			if err != nil {
				return classifyWrite(err, auth.Gmail)
			}
			return printSent(app, res)
		},
	}
	addWriteFlags(cmd, &wf)
	return cmd
}

func printSent(app *App, res *gmail.SendResult) error {
	return app.Print(res, func(w io.Writer) error {
		return output.KeyValues(w, "Sent", res.MessageID, "Thread", res.ThreadID)
	})
}

func newGmailSendCmd(app *App) *cobra.Command {
	var (
		cf composeFlags
		wf writeFlags
	)
	cmd := &cobra.Command{
		Use:   "send",
		Short: "Send an email (asks for confirmation)",
		Long: "Send a plain-text email, optionally as a reply. Sending cannot be undone, so\n" +
			"you are asked to confirm unless you pass --yes.\n" +
			"  gwork gmail send --to ana@example.com --subject Hi --body 'Hello'\n" +
			"  gwork gmail send --reply-to 18a1b2 --reply-all --body-file - < reply.txt --yes",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cf.bodyFile == "-" && !wf.Yes && !wf.DryRun {
				return errors.New("--body-file - reads stdin, which the confirmation prompt needs: pass --yes or use a file")
			}
			in, err := cf.input(app)
			if err != nil {
				return err
			}
			if len(in.To)+len(in.Cc)+len(in.Bcc) == 0 && in.ReplyToMessageID == "" {
				return errors.New("no recipients: use --to, --cc, --bcc or --reply-to")
			}
			if wf.DryRun {
				return app.printDryRun(map[string]any{"action": "send_message", "message": in})
			}
			summary := fmt.Sprintf("Send email %q to %s.", output.Ellipsize(in.Subject, 60), recipients(in))
			if err := app.confirmWrite(wf, summary); err != nil {
				return err
			}
			ctx := cmd.Context()
			opts, err := app.WriteClientOptions(ctx, auth.Gmail)
			if err != nil {
				return err
			}
			res, err := gmail.SendMessage(ctx, in, opts...)
			if err != nil {
				return classifyWrite(err, auth.Gmail)
			}
			return printSent(app, res)
		},
	}
	cf.add(cmd)
	addWriteFlags(cmd, &wf)
	return cmd
}

// changeSpec describes a label/trash command over a message or thread.
type changeSpec struct {
	use, short string
	long       string
	// add and remove are the labels applied (label-like commands).
	add, remove []string
	// trash: "trash", "untrash" or "" (label change).
	trash string
	// confirm requires confirmation.
	confirm bool
}

// newGmailChangeCmd builds a command <use> <id> [--thread] that runs spec.
func newGmailChangeCmd(app *App, spec changeSpec) *cobra.Command {
	var (
		thread bool
		wf     writeFlags
	)
	cmd := &cobra.Command{
		Use:   spec.use + " <id>",
		Short: spec.short,
		Long:  spec.long,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGmailChange(app, cmd, args[0], thread, wf, spec)
		},
	}
	cmd.Flags().BoolVar(&thread, "thread", false, "treat <id> as a thread id and apply to the whole thread")
	addWriteFlags(cmd, &wf)
	return cmd
}

func newGmailLabelCmd(app *App) *cobra.Command {
	var (
		add, remove []string
		thread      bool
		wf          writeFlags
	)
	cmd := &cobra.Command{
		Use:   "label <id>",
		Short: "Add or remove labels on a message or thread",
		Long: "Add or remove labels given by name (case-insensitive) or id, e.g.\n" +
			"  gwork gmail label 18a1b2 --add Customers --remove INBOX\n" +
			"  gwork gmail label 18a1b2 --thread --add STARRED",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(add)+len(remove) == 0 {
				return errors.New("nothing to do: use --add and/or --remove")
			}
			return runGmailChange(app, cmd, args[0], thread, wf, changeSpec{add: add, remove: remove})
		},
	}
	cmd.Flags().StringSliceVar(&add, "add", nil, "label to add (name or id; repeatable or comma-separated)")
	cmd.Flags().StringSliceVar(&remove, "remove", nil, "label to remove (name or id; repeatable or comma-separated)")
	cmd.Flags().BoolVar(&thread, "thread", false, "treat <id> as a thread id and apply to the whole thread")
	addWriteFlags(cmd, &wf)
	return cmd
}

func runGmailChange(app *App, cmd *cobra.Command, id string, thread bool, wf writeFlags, spec changeSpec) error {
	target := gmail.Target{MessageID: id}
	if thread {
		target = gmail.Target{ThreadID: id}
	}
	action := "modify_labels"
	switch spec.trash {
	case "trash", "untrash":
		action = spec.trash
	}
	if wf.DryRun {
		req := map[string]any{"action": action, "kind": target.Kind(), "id": id}
		if spec.trash == "" {
			req["add_labels"], req["remove_labels"] = nonNilStrings(spec.add), nonNilStrings(spec.remove)
		}
		return app.printDryRun(req)
	}
	if spec.confirm {
		if err := app.confirmWrite(wf, fmt.Sprintf("Move %s %s to the Trash.", target.Kind(), id)); err != nil {
			return err
		}
	}
	ctx := cmd.Context()
	opts, err := app.WriteClientOptions(ctx, auth.Gmail)
	if err != nil {
		return err
	}
	var res *gmail.ModifyResult
	switch spec.trash {
	case "trash":
		res, err = gmail.Trash(ctx, target, opts...)
	case "untrash":
		res, err = gmail.Untrash(ctx, target, opts...)
	default:
		res, err = gmail.ModifyLabels(ctx, target, spec.add, spec.remove, opts...)
	}
	if err != nil {
		return classifyWrite(err, auth.Gmail)
	}
	return app.Print(res, func(w io.Writer) error {
		pairs := []string{"Done", action, "Kind", res.Kind, "ID", res.ID}
		if res.ThreadID != "" {
			pairs = append(pairs, "Thread", res.ThreadID)
		}
		if len(res.Added) > 0 {
			pairs = append(pairs, "Added", strings.Join(res.Added, ", "))
		}
		if len(res.Removed) > 0 {
			pairs = append(pairs, "Removed", strings.Join(res.Removed, ", "))
		}
		if len(res.LabelIDs) > 0 {
			pairs = append(pairs, "Labels", strings.Join(res.LabelIDs, ", "))
		}
		return output.KeyValues(w, pairs...)
	})
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// gmailWriteCommands returns the write commands added to "gwork gmail".
func gmailWriteCommands(app *App) []*cobra.Command {
	return []*cobra.Command{
		newGmailDraftCmd(app),
		newGmailSendCmd(app),
		newGmailLabelCmd(app),
		newGmailChangeCmd(app, changeSpec{use: "archive", short: "Archive a message or thread (remove INBOX)", remove: []string{"INBOX"}}),
		newGmailChangeCmd(app, changeSpec{use: "mark-read", short: "Mark a message or thread as read (remove UNREAD)", remove: []string{"UNREAD"}}),
		newGmailChangeCmd(app, changeSpec{use: "mark-unread", short: "Mark a message or thread as unread (add UNREAD)", add: []string{"UNREAD"}}),
		newGmailChangeCmd(app, changeSpec{use: "star", short: "Star a message or thread (add STARRED)", add: []string{"STARRED"}}),
		newGmailChangeCmd(app, changeSpec{use: "unstar", short: "Remove the star from a message or thread", remove: []string{"STARRED"}}),
		newGmailChangeCmd(app, changeSpec{use: "trash", short: "Move a message or thread to the Trash (asks for confirmation)", trash: "trash", confirm: true}),
		newGmailChangeCmd(app, changeSpec{use: "untrash", short: "Restore a message or thread from the Trash", trash: "untrash"}),
	}
}
