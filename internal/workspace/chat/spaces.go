package chat

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	chatapi "google.golang.org/api/chat/v1"
	"google.golang.org/api/googleapi"
)

// DefaultMaxSpaces is the default number of spaces returned by ListSpaces.
const DefaultMaxSpaces = 100

// maxPageSize is the largest pageSize accepted by the Chat list methods.
const maxPageSize = 1000

// ListSpacesOptions configures ListSpaces.
type ListSpacesOptions struct {
	// Type limits the result to one space type: "space", "group", "dm" (or
	// the API enum value). Empty lists every type.
	Type string
	// Section limits the result to the spaces of one sidebar section (see
	// FindSection for the accepted forms), in the section's order. Empty
	// lists every space.
	Section string
	// Max caps the number of spaces returned; <= 0 means DefaultMaxSpaces.
	Max int
}

// ListSpaces lists the spaces the user is a member of. Group chats and DMs
// only appear once they have at least one message.
func ListSpaces(ctx context.Context, svc *chatapi.Service, o ListSpacesOptions) ([]Space, error) {
	limit := o.Max
	if limit <= 0 {
		limit = DefaultMaxSpaces
	}
	if strings.TrimSpace(o.Section) == "" {
		return listSpaces(ctx, svc, o.Type, limit)
	}
	spaces, err := listSectionSpaces(ctx, svc, o.Section, o.Type)
	if err != nil {
		return nil, err
	}
	return spaces[:min(limit, len(spaces))], nil
}

// listSectionSpaces returns the spaces of a section, in the section's order
// and optionally limited to one space type. Section items only carry the
// space name, so details come from spaces.list; an item missing there (a
// conversation without messages) is kept with its name only, unless a type
// was requested.
func listSectionSpaces(ctx context.Context, svc *chatapi.Service, section, typ string) ([]Space, error) {
	if _, err := SpaceTypeFilter(typ); err != nil {
		return nil, err
	}
	sec, err := FindSection(ctx, svc, section)
	if err != nil {
		return nil, err
	}
	names, err := listSectionSpaceNames(ctx, svc, sec.Name)
	if err != nil {
		return nil, err
	}
	out := make([]Space, 0, len(names))
	if len(names) == 0 {
		return out, nil
	}
	all, err := listSpaces(ctx, svc, typ, 0)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]Space, len(all))
	for _, sp := range all {
		byName[sp.Name] = sp
	}
	for _, name := range names {
		if sp, ok := byName[name]; ok {
			out = append(out, sp)
		} else if typ == "" {
			out = append(out, Space{Name: name})
		}
	}
	return out, nil
}

// listSpaces pages through spaces.list; limit <= 0 means no limit.
func listSpaces(ctx context.Context, svc *chatapi.Service, typ string, limit int) ([]Space, error) {
	enum, err := SpaceTypeFilter(typ)
	if err != nil {
		return nil, err
	}
	out := make([]Space, 0)
	token := ""
	for {
		size := maxPageSize
		if limit > 0 {
			size = min(size, limit-len(out))
		}
		call := svc.Spaces.List().PageSize(int64(size)).Context(ctx)
		if enum != "" {
			call = call.Filter(fmt.Sprintf("spaceType = %q", enum))
		}
		if token != "" {
			call = call.PageToken(token)
		}
		resp, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("list chat spaces: %w", err)
		}
		for _, s := range resp.Spaces {
			if s == nil {
				continue
			}
			out = append(out, convertSpace(s))
			if limit > 0 && len(out) >= limit {
				return out, nil
			}
		}
		if resp.NextPageToken == "" {
			return out, nil
		}
		token = resp.NextPageToken
	}
}

// GetSpace returns one space by name ("spaces/X" or bare "X").
func GetSpace(ctx context.Context, svc *chatapi.Service, space string) (Space, error) {
	name, err := NormalizeSpace(space)
	if err != nil {
		return Space{}, err
	}
	s, err := svc.Spaces.Get(name).Context(ctx).Do()
	if err != nil {
		return Space{}, fmt.Errorf("get chat space %s: %w", name, err)
	}
	return convertSpace(s), nil
}

// FindDirectMessage returns the direct message space between the
// authenticated user and user, given as an email, "users/{email}" or
// "users/{id}". When there is no such DM it returns an error wrapping
// ErrDirectMessageNotFound.
func FindDirectMessage(ctx context.Context, svc *chatapi.Service, user string) (Space, error) {
	user = strings.TrimSpace(user)
	if strings.TrimPrefix(user, "users/") == "" {
		return Space{}, errors.New("find direct message: empty user")
	}
	name := user
	if !strings.HasPrefix(name, "users/") {
		name = "users/" + name
	}
	s, err := svc.Spaces.FindDirectMessage().Name(name).Context(ctx).Do()
	if err != nil {
		var ge *googleapi.Error
		if errors.As(err, &ge) && ge.Code == http.StatusNotFound {
			return Space{}, fmt.Errorf("no direct message with %s: the conversation may not exist yet, or the address is not a Google Chat user: %w",
				strings.TrimPrefix(user, "users/"), ErrDirectMessageNotFound)
		}
		return Space{}, fmt.Errorf("find direct message with %s: %w", name, err)
	}
	return convertSpace(s), nil
}
