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
)

// BaseScopes are requested on every login to discover the account email.
var BaseScopes = []string{ScopeOpenID, ScopeUserinfoEmail}

var serviceScopes = map[Service][]string{
	Gmail:    {ScopeGmailReadonly},
	Calendar: {ScopeCalendarReadonly},
	Drive:    {ScopeDriveReadonly},
	Chat:     {ScopeChatSpacesReadonly, ScopeChatMessagesRO, ScopeChatMembershipsRO},
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

// ScopesFor returns the base scopes plus the scopes of every service,
// without duplicates.
func ScopesFor(svcs []Service) []string {
	out := slices.Clone(BaseScopes)
	for _, s := range svcs {
		for _, sc := range serviceScopes[s] {
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
