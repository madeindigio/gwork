package cli

import (
	"fmt"
	"io"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/madeindigio/gwork/internal/auth"
	"github.com/madeindigio/gwork/internal/output"
	"github.com/madeindigio/gwork/internal/workspace/chat"
)

func newChatSectionsCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "sections",
		Short: "List the sections of your Chat sidebar",
		Long: "List the sections that organize your Chat sidebar: the default ones and the\n" +
			"custom sections you created (for example \"Favorites\"). List the spaces of a\n" +
			"section with \"gwork chat spaces --section <name>\".\n\n" +
			"Logins made before this command existed lack its scope: run\n" +
			"\"gwork auth login --services chat\" again.",
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
			sections, err := chat.ListSections(ctx, svc)
			if err != nil {
				return err
			}
			return app.Print(sections, func(w io.Writer) error {
				rows := make([][]string, 0, len(sections))
				for _, s := range sections {
					rows = append(rows, []string{s.Name, s.Type, output.Ellipsize(s.DisplayName, 50)})
				}
				return output.Table(w, []string{"NAME", "TYPE", "DISPLAY NAME"}, rows)
			})
		},
	}
}

func newChatUnreadCmd(app *App) *cobra.Command {
	var spaces []string
	var typ, section string
	var perSpace int
	var noResolve bool
	cmd := &cobra.Command{
		Use:   "unread",
		Short: "List unread messages across spaces",
		Long: "List the messages created after your read position in each space, most\n" +
			"recently active space first.\n\n" +
			"Google Chat has no unread flag or counter: this command reads your read state\n" +
			"in every space you belong to (or the --space, --type or --section ones), 4\n" +
			"spaces at a time, and lists the messages created after it. Read positions of\n" +
			"individual threads are not considered.\n\n" +
			"Logins made before this command existed lack its scope: run\n" +
			"\"gwork auth login --services chat\" again.",
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
			res, err := chat.ListUnread(ctx, svc, chat.UnreadOptions{
				Spaces: spaces, Type: typ, Section: section, MaxPerSpace: perSpace,
			})
			if err != nil {
				return err
			}
			if !noResolve {
				resolver := chat.NewNameResolver(svc)
				for _, u := range res.Spaces {
					resolver.Resolve(ctx, u.Messages)
				}
			}
			for _, f := range res.FailedSpaces {
				_, _ = fmt.Fprintf(app.Err, "warning: could not check %s: %s\n", output.Sanitize(f.Space), output.Sanitize(f.Error))
			}
			return app.Print(res, func(w io.Writer) error {
				for _, u := range res.Spaces {
					if err := writeChatUnreadSpace(w, app, u); err != nil {
						return err
					}
				}
				_, err := fmt.Fprintf(w, "%d spaces with unread messages; checked %d/%d spaces\n",
					len(res.Spaces), res.SpacesChecked, res.SpacesTotal)
				return err
			})
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&spaces, "space", nil, "only check this space (repeatable; spaces/XXX or XXX)")
	f.StringVar(&typ, "type", "", "only check this type: space, group or dm")
	f.StringVar(&section, "section", "", "only check the spaces of this sidebar section (display name, id or resource name)")
	f.IntVar(&perSpace, "max-per-space", chat.DefaultMaxUnreadPerSpace, "maximum number of unread messages per space")
	f.BoolVar(&noResolve, "no-resolve-names", false, "do not look up sender display names in space memberships")
	return cmd
}

// writeChatUnreadSpace renders one space: a header line with the space, the
// unread count and the read position, then its messages and a blank line.
func writeChatUnreadSpace(w io.Writer, app *App, u chat.UnreadSpace) error {
	title := u.Space.Name
	if u.Space.DisplayName != "" {
		title = u.Space.DisplayName + " (" + u.Space.Name + ")"
	}
	count := strconv.Itoa(len(u.Messages))
	if u.More {
		count += "+"
	}
	lastRead := "never read"
	if !u.LastReadTime.IsZero() {
		lastRead = "last read " + output.DateTime(u.LastReadTime, app.Location())
	}
	if _, err := fmt.Fprintf(w, "== %s  %s unread, %s\n", title, count, lastRead); err != nil {
		return err
	}
	if err := writeChatMessages(w, app, u.Messages, false); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}
