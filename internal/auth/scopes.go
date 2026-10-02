package auth

import (
	"fmt"
	"slices"
	"strings"
)

// Service identifies a Google Workspace API that gwork can read.
type Service string

// Supported services.
const (
	Gmail    Service = "gmail"
	Calendar Service = "calendar"
	Drive    Service = "drive"
	Chat     Service = "chat"
)

// AllServices lists every supported service in display order.
var AllServices = []Service{Gmail, Calendar, Drive, Chat}

// Scope URLs.
const (
	ScopeOpenID             = "openid"
	ScopeUserinfoEmail      = "https://www.googleapis.com/auth/userinfo.email"
	ScopeGmailReadonly      = "https://www.googleapis.com/auth/gmail.readonly"
	ScopeCalendarReadonly   = "https://www.googleapis.com/auth/calendar.readonly"
	ScopeDriveReadonly      = "https://www.googleapis.com/auth/drive.readonly"
	ScopeChatSpacesReadonly = "https://www.googleapis.com/auth/chat.spaces.readonly"
	ScopeChatMessagesRO     = "https://www.googleapis.com/auth/chat.messages.readonly"
	ScopeChatMembershipsRO  = "https://www.googleapis.com/auth/chat.memberships.readonly"

	// Optional read scopes: requested at login, not required to use the
	// service (see optionalScopes).
	ScopeChatReadStateRO = "https://www.googleapis.com/auth/chat.users.readstate.readonly"
	ScopeChatSectionsRO  = "https://www.googleapis.com/auth/chat.users.sections.readonly"

	// Write scopes, requested only with "gwork auth login --write".
	ScopeGmailModify        = "https://www.googleapis.com/auth/gmail.modify"
	ScopeCalendarEvents     = "https://www.googleapis.com/auth/calendar.events"
	ScopeChatMessagesCreate = "https://www.googleapis.com/auth/chat.messages.create"
)

// BaseScopes are requested on every login to discover the account email.
var BaseScopes = []string{ScopeOpenID, ScopeUserinfoEmail}

var serviceScopes = map[Service][]string{
	Gmail:    {ScopeGmailReadonly},
	Calendar: {ScopeCalendarReadonly},
	Drive:    {ScopeDriveReadonly},
	Chat:     {ScopeChatSpacesReadonly, ScopeChatMessagesRO, ScopeChatMembershipsRO},
}

// optionalScopes holds read-only scopes that only some features of a service
// need (Chat unread messages and sections). Login requests them, but a token
// without them still grants the service, so logins made before a scope was
// added keep working; the features that need it fail with a login hint.
var optionalScopes = map[Service][]string{
	Chat: {ScopeChatReadStateRO, ScopeChatSectionsRO},
}

// WritableServices lists the services that support write operations.
var WritableServices = []Service{Gmail, Calendar, Chat}

// writeScopes holds the scopes needed in addition to the read scopes.
var writeScopes = map[Service][]string{
	Gmail:    {ScopeGmailModify},
	Calendar: {ScopeCalendarEvents},
	Chat:     {ScopeChatMessagesCreate},
}

// Writable reports whether the service supports write operations.
func (s Service) Writable() bool {
	_, ok := writeScopes[s]
	return ok
}

// WriteScopes returns the write OAuth scopes of the service (in addition to
// its read scopes), or nil when the service is not writable.
func (s Service) WriteScopes() []string {
	return slices.Clone(writeScopes[s])
}

// Valid reports whether s is a supported service.
func (s Service) Valid() bool {
	_, ok := serviceScopes[s]
	return ok
}

// Scopes returns the read-only OAuth scopes required by the service.
func (s Service) Scopes() []string {
	return slices.Clone(serviceScopes[s])
}

// OptionalScopes returns the read-only OAuth scopes that login requests for
// the service but that are not required to use it.
func (s Service) OptionalScopes() []string {
	return slices.Clone(optionalScopes[s])
}

// ParseServices parses a comma separated list such as "gmail,chat".
// "all" or an empty string select every service. Duplicates are removed and
// the result keeps the AllServices order.
func ParseServices(s string) ([]Service, error) {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "all") {
		return slices.Clone(AllServices), nil
	}
	seen := map[Service]bool{}
	for _, part := range strings.Split(s, ",") {
		name := Service(strings.ToLower(strings.TrimSpace(part)))
		if name == "" {
			continue
		}
		if name == "all" {
			return slices.Clone(AllServices), nil
		}
		if !name.Valid() {
			return nil, fmt.Errorf("unknown service %q (valid: %s)", name, JoinServices(AllServices))
		}
		seen[name] = true
	}
	var out []Service
	for _, svc := range AllServices {
		if seen[svc] {
			out = append(out, svc)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no services selected (valid: %s)", JoinServices(AllServices))
	}
	return out, nil
}

// JoinServices renders services as "a,b,c".
func JoinServices(svcs []Service) string {
	parts := make([]string, len(svcs))
	for i, s := range svcs {
		parts[i] = string(s)
	}
	return strings.Join(parts, ",")
}

// ScopesFor returns the base scopes plus the required and optional scopes of
// every service, without duplicates.
func ScopesFor(svcs []Service) []string {
	out := slices.Clone(BaseScopes)
	for _, s := range svcs {
		for _, sc := range slices.Concat(serviceScopes[s], optionalScopes[s]) {
			if !slices.Contains(out, sc) {
				out = append(out, sc)
			}
		}
	}
	return out
}

// MissingScopes returns the scopes required by svc that are not in granted.
func MissingScopes(granted []string, svc Service) []string {
	var missing []string
	for _, sc := range serviceScopes[svc] {
		if !slices.Contains(granted, sc) {
			missing = append(missing, sc)
		}
	}
	return missing
}

// GrantedServices returns the services whose scopes are all in granted.
func GrantedServices(granted []string) []Service {
	var out []Service
	for _, svc := range AllServices {
		if len(MissingScopes(granted, svc)) == 0 {
			out = append(out, svc)
		}
	}
	return out
}

// ParseWriteServices parses the value of --write / --allow-write: a comma
// separated list of writable services. "" and "none" select nothing, "all"
// selects WritableServices. Duplicates are removed and the result keeps the
// WritableServices order. Non-writable services (drive) are an error.
func ParseWriteServices(s string) ([]Service, error) {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "none") {
		return []Service{}, nil
	}
	seen := map[Service]bool{}
	for _, part := range strings.Split(s, ",") {
		name := Service(strings.ToLower(strings.TrimSpace(part)))
		switch {
		case name == "":
			continue
		case name == "all":
			return slices.Clone(WritableServices), nil
		case name == "none":
			continue
		case !name.Valid():
			return nil, fmt.Errorf("unknown service %q (valid for write: %s)", name, JoinServices(WritableServices))
		case !name.Writable():
			return nil, fmt.Errorf("service %q does not support write operations (valid for write: %s)", name, JoinServices(WritableServices))
		}
		seen[name] = true
	}
	out := []Service{}
	for _, svc := range WritableServices {
		if seen[svc] {
			out = append(out, svc)
		}
	}
	return out, nil
}

// ScopesForWrite returns the base scopes, the read scopes of read and write
// services, and the write scopes of write services, without duplicates.
func ScopesForWrite(read, write []Service) []string {
	svcs := slices.Clone(read)
	for _, w := range write {
		if !slices.Contains(svcs, w) {
			svcs = append(svcs, w)
		}
	}
	out := ScopesFor(svcs)
	for _, w := range write {
		for _, sc := range writeScopes[w] {
			if !slices.Contains(out, sc) {
				out = append(out, sc)
			}
		}
	}
	return out
}

// MissingWriteScopes returns the scopes required to write to svc (its read
// scopes plus its write scopes) that are not in granted. A non-writable
// service yields its read scopes only.
func MissingWriteScopes(granted []string, svc Service) []string {
	missing := MissingScopes(granted, svc)
	for _, sc := range writeScopes[svc] {
		if !slices.Contains(granted, sc) {
			missing = append(missing, sc)
		}
	}
	return missing
}

// WriteGrantedServices returns the writable services whose read and write
// scopes are all in granted.
func WriteGrantedServices(granted []string) []Service {
	out := []Service{}
	for _, svc := range WritableServices {
		if len(MissingWriteScopes(granted, svc)) == 0 {
			out = append(out, svc)
		}
	}
	return out
}
