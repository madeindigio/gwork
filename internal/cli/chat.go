package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/digio/gwork-cli/internal/auth"
	"github.com/digio/gwork-cli/internal/output"
	"github.com/digio/gwork-cli/internal/timeutil"
	"github.com/digio/gwork-cli/internal/workspace/chat"
)

// newChatCmd returns the "gwork chat" command group.
func newChatCmd(app *App) *cobra.Command {
	cmd := serviceGroup(auth.Chat, "Read Google Chat spaces and messages")
	cmd.AddCommand(
		newChatSpacesCmd(app),
		newChatDMCmd(app),
		newChatMessagesCmd(app),
		newChatGetCmd(app),
		newChatSearchCmd(app),
	)
	return cmd
}

func newChatSpacesCmd(app *App) *cobra.Command {
	var typ string
	var limit int
	cmd := &cobra.Command{
		Use:   "spaces",
		Short: "List the Chat spaces, group chats and DMs you are a member of",
		Long: "List the Chat spaces you are a member of. Group chats and direct messages\n" +
			"only appear once they have at least one message.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			opts, err := app.ClientOptions(ctx, auth.Chat)
			if err != nil {
				return err
			}
			svc, err := chat.New(ctx, opts...)
			if err != nil {
				return err
			}
			spaces, err := chat.ListSpaces(ctx, svc, chat.ListSpacesOptions{Type: typ, Max: limit})
			if err != nil {
				return err
			}
			return app.Print(spaces, func(w io.Writer) error {
				rows := make([][]string, 0, len(spaces))
				for _, s := range spaces {
					members := ""
					if s.MemberCount > 0 {
						members = strconv.FormatInt(s.MemberCount, 10)
					}
					rows = append(rows, []string{s.Name, s.Type, output.Ellipsize(s.DisplayName, 50), members, output.DateTime(s.LastActiveTime, app.Location())})
				}
				return output.Table(w, []string{"NAME", "TYPE", "DISPLAY NAME", "MEMBERS", "LAST ACTIVE"}, rows)
			})
		},
	}
	cmd.Flags().StringVar(&typ, "type", "", "only list this type: space, group or dm")
	cmd.Flags().IntVar(&limit, "max", chat.DefaultMaxSpaces, "maximum number of spaces")
	return cmd
}

func newChatDMCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "dm <email>",
		Short: "Find the direct message space with a user",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			opts, err := app.ClientOptions(ctx, auth.Chat)
			if err != nil {
				return err
			}
			svc, err := chat.New(ctx, opts...)
			if err != nil {
				return err
			}
			space, err := chat.FindDirectMessage(ctx, svc, args[0])
			if err != nil {
				return err
			}
			return app.Print(space, func(w io.Writer) error {
				return output.KeyValues(w,
					"Name", space.Name,
					"Type", space.Type,
					"Last active", output.DateTime(space.LastActiveTime, app.Location()),
					"URI", space.URI,
				)
			})
		},
	}
}

func newChatMessagesCmd(app *App) *cobra.Command {
	var since, until, thread, order string
	var limit int
	var noResolve bool
	cmd := &cobra.Command{
		Use:   "messages <space>",
		Short: "List messages of a space (spaces/XXX or XXX)",
		Long: "List messages of a space, newest first by default.\n\n" +
			"--since/--until accept RFC 3339, YYYY-MM-DD, today, yesterday or relative\n" +
			"values such as 7d or 24h. Missing sender display names are looked up in\n" +
			"the space memberships; if unavailable, senders show as users/{id}.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			win, err := timeutil.ParseWindow(since, until, app.CurrentTime(), "", "")
			if err != nil {
				return err
			}
			opts, err := app.ClientOptions(ctx, auth.Chat)
			if err != nil {
				return err
			}
			svc, err := chat.New(ctx, opts...)
			if err != nil {
				return err
			}
			msgs, err := chat.ListMessages(ctx, svc, args[0], chat.ListMessagesOptions{
				Window: win, Thread: thread, Max: limit, Order: order,
			})
			if err != nil {
				return err
			}
			if !noResolve {
				chat.NewNameResolver(svc).Resolve(ctx, msgs)
			}
			return app.Print(msgs, func(w io.Writer) error {
				if len(msgs) == 0 {
					_, err := fmt.Fprintln(w, "no messages")
					return err
				}
				return writeChatMessages(w, app, msgs, false)
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&since, "since", "", "only messages created at or after this time (e.g. 7d, 2026-09-01)")
	f.StringVar(&until, "until", "", "only messages created before this time")
	f.StringVar(&thread, "thread", "", "only messages of this thread (spaces/S/threads/T or T)")
	f.IntVar(&limit, "max", chat.DefaultMaxMessages, "maximum number of messages")
	f.StringVar(&order, "order", chat.OrderDesc, "order by creation time: asc or desc")
	f.BoolVar(&noResolve, "no-resolve-names", false, "do not look up sender display names in space memberships")
	return cmd
}

func newChatGetCmd(app *App) *cobra.Command {
	var noResolve bool
	cmd := &cobra.Command{
		Use:   "get <messageName>",
		Short: "Show one message (spaces/{space}/messages/{message})",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			opts, err := app.ClientOptions(ctx, auth.Chat)
			if err != nil {
				return err
			}
			svc, err := chat.New(ctx, opts...)
			if err != nil {
				return err
			}
			m, err := chat.GetMessage(ctx, svc, args[0])
			if err != nil {
				return err
			}
			if !noResolve {
				one := []chat.Message{m}
				chat.NewNameResolver(svc).Resolve(ctx, one)
				m = one[0]
			}
			return app.Print(m, func(w io.Writer) error {
				if err := output.KeyValues(w,
					"Name", m.Name,
					"Space", m.Space,
					"Thread", m.Thread,
					"Created", output.DateTime(m.CreateTime, app.Location()),
					"Sender", chatSender(m.Sender),
				); err != nil {
					return err
				}
				for _, a := range m.Attachments {
					if _, err := fmt.Fprintf(w, "Attachment:  %s (%s) %s\n", a.ContentName, a.ContentType, a.Name); err != nil {
						return err
					}
				}
				_, err := fmt.Fprintf(w, "\n%s\n", m.Text)
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&noResolve, "no-resolve-names", false, "do not look up the sender display name in space memberships")
	return cmd
}

func newChatSearchCmd(app *App) *cobra.Command {
	var spaces []string
	var since string
	var limit, maxScan int
	var noResolve bool
	cmd := &cobra.Command{
		Use:   "search <text>",
		Short: "Search message text across spaces (client-side filter)",
		Long: "Search message text across Chat spaces.\n\n" +
			"Google Chat has no server-side full-text search for users. This command lists\n" +
			"the messages of every space you belong to (or the --space ones) created since\n" +
			"--since, newest first and 4 spaces at a time, and keeps those containing all\n" +
			"words of <text> (case-insensitive). It stops after --max-scan messages; when\n" +
			"that cap is hit results may be incomplete, so narrow --since or --space.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			win, err := timeutil.ParseWindow(since, "", app.CurrentTime(), "7d", "")
			if err != nil {
				return err
			}
			opts, err := app.ClientOptions(ctx, auth.Chat)
			if err != nil {
				return err
			}
			svc, err := chat.New(ctx, opts...)
			if err != nil {
				return err
			}
			res, err := chat.SearchMessages(ctx, svc, chat.SearchOptions{
				Text: args[0], Spaces: spaces, Window: win, Max: limit, MaxScan: maxScan,
			})
			if err != nil {
				return err
			}
			if !noResolve {
				chat.NewNameResolver(svc).Resolve(ctx, res.Matches)
			}
			for _, f := range res.FailedSpaces {
				_, _ = fmt.Fprintf(app.Err, "warning: could not read %s: %s\n", output.Sanitize(f.Space), output.Sanitize(f.Error))
			}
			if res.CapReached {
				_, _ = fmt.Fprintf(app.Err, "warning: stopped after scanning %d messages (--max-scan); results may be incomplete\n", res.Scanned)
			}
			return app.Print(res, func(w io.Writer) error {
				if len(res.Matches) > 0 {
					if err := writeChatMessages(w, app, res.Matches, true); err != nil {
						return err
					}
				}
				_, err := fmt.Fprintf(w, "%d of %d matches; scanned %d messages in %d/%d spaces\n",
					len(res.Matches), res.TotalMatches, res.Scanned, res.SpacesScanned, res.SpacesTotal)
				return err
			})
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&spaces, "space", nil, "limit to this space (repeatable; spaces/XXX or XXX)")
	f.StringVar(&since, "since", "7d", "only messages created at or after this time")
	f.IntVar(&limit, "max", chat.DefaultSearchMax, "maximum number of matches returned")
	f.IntVar(&maxScan, "max-scan", chat.DefaultMaxScan, "maximum number of messages scanned")
	f.BoolVar(&noResolve, "no-resolve-names", false, "do not look up sender display names in space memberships")
	return cmd
}

// writeChatMessages renders messages as blocks: a header line with time,
// sender and name, then the indented text and attachments.
func writeChatMessages(w io.Writer, app *App, msgs []chat.Message, withSpace bool) error {
	for i, m := range msgs {
		if i > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		header := output.DateTime(m.CreateTime, app.Location()) + "  " + chatSender(m.Sender)
		if withSpace {
			header += "  " + m.Space
		}
		if _, err := fmt.Fprintf(w, "%s  [%s]\n", header, m.Name); err != nil {
			return err
		}
		text := strings.TrimRight(m.Text, "\n")
		if text != "" {
			if _, err := fmt.Fprintf(w, "  %s\n", strings.ReplaceAll(text, "\n", "\n  ")); err != nil {
				return err
			}
		}
		for _, a := range m.Attachments {
			if _, err := fmt.Fprintf(w, "  [attachment] %s\n", a.ContentName); err != nil {
				return err
			}
		}
	}
	return nil
}

// chatSender formats a sender as "Display Name (users/1)" or "users/1".
func chatSender(u chat.User) string {
	switch {
	case u.DisplayName != "" && u.Name != "":
		return u.DisplayName + " (" + u.Name + ")"
	case u.DisplayName != "":
		return u.DisplayName
	case u.Name != "":
		return u.Name
	}
	return "unknown"
}
