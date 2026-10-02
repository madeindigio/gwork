package chat

import (
	"context"
	"fmt"
	"strings"

	chatapi "google.golang.org/api/chat/v1"
)

// Section types as returned by the Chat API (Section.type).
const (
	// SectionCustom is a section created by the user (e.g. "Favorites").
	SectionCustom = "CUSTOM_SECTION"
	// SectionDefaultDirectMessages holds DMs and group chats outside custom
	// sections.
	SectionDefaultDirectMessages = "DEFAULT_DIRECT_MESSAGES"
	// SectionDefaultSpaces holds spaces outside custom sections.
	SectionDefaultSpaces = "DEFAULT_SPACES"
	// SectionDefaultApps holds the user's installed apps.
	SectionDefaultApps = "DEFAULT_APPS"
)

// sectionsParent is the collection of the calling user's sections.
const sectionsParent = "users/me"

// sectionsPageSize is the largest pageSize accepted by the sections methods.
const sectionsPageSize = 100

// Section is a group of conversations in the user's Chat sidebar: either a
// system section or a custom one the user created to organize spaces.
type Section struct {
	// Name is the resource name, e.g. "users/123/sections/abc".
	Name string `json:"name"`
	// DisplayName is only set for custom sections.
	DisplayName string `json:"display_name,omitempty"`
	// Type is CUSTOM_SECTION, DEFAULT_DIRECT_MESSAGES, DEFAULT_SPACES or
	// DEFAULT_APPS.
	Type string `json:"type"`
	// SortOrder is the position in the sidebar; lower values come first.
	SortOrder int64 `json:"sort_order"`
}

// ListSections lists the sections of the user's Chat sidebar in the order
// returned by the API. It needs the chat.users.sections.readonly scope.
func ListSections(ctx context.Context, svc *chatapi.Service) ([]Section, error) {
	out := make([]Section, 0)
	token := ""
	for {
		call := svc.Users.Sections.List(sectionsParent).PageSize(sectionsPageSize).Context(ctx)
		if token != "" {
			call = call.PageToken(token)
		}
		resp, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("list chat sections: %w", err)
		}
		for _, s := range resp.Sections {
			if s != nil {
				out = append(out, Section{Name: s.Name, DisplayName: s.DisplayName, Type: s.Type, SortOrder: s.SortOrder})
			}
		}
		if resp.NextPageToken == "" {
			return out, nil
		}
		token = resp.NextPageToken
	}
}

// FindSection resolves section, given as a resource name
// ("users/{user}/sections/{id}"), a section id ("default-spaces") or the
// display name of a custom section (case-insensitive).
func FindSection(ctx context.Context, svc *chatapi.Service, section string) (Section, error) {
	want := strings.TrimSpace(section)
	if want == "" {
		return Section{}, fmt.Errorf("invalid section %q: want a section name, id or display name", section)
	}
	sections, err := ListSections(ctx, svc)
	if err != nil {
		return Section{}, err
	}
	var byName []Section
	for _, s := range sections {
		if s.Name == want || sectionID(s.Name) == want {
			return s, nil
		}
		if s.DisplayName != "" && strings.EqualFold(s.DisplayName, want) {
			byName = append(byName, s)
		}
	}
	switch len(byName) {
	case 1:
		return byName[0], nil
	case 0:
		return Section{}, fmt.Errorf("no chat section matches %q: list them with the sections command", want)
	}
	return Section{}, fmt.Errorf("%d chat sections are named %q: use the section resource name", len(byName), want)
}

// sectionID returns the last segment of a section resource name.
func sectionID(name string) string {
	return name[strings.LastIndex(name, "/")+1:]
}

// listSectionSpaceNames returns the space resource names of the items in
// section (a section resource name), in the order returned by the API.
func listSectionSpaceNames(ctx context.Context, svc *chatapi.Service, section string) ([]string, error) {
	out := make([]string, 0)
	token := ""
	for {
		call := svc.Users.Sections.Items.List(section).PageSize(sectionsPageSize).Context(ctx)
		if token != "" {
			call = call.PageToken(token)
		}
		resp, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("list items of chat section %s: %w", section, err)
		}
		for _, it := range resp.SectionItems {
			if it != nil && it.Space != "" {
				out = append(out, it.Space)
			}
		}
		if resp.NextPageToken == "" {
			return out, nil
		}
		token = resp.NextPageToken
	}
}
