package auth

import (
	"slices"
	"testing"
)

func TestParseServices(t *testing.T) {
	got, err := ParseServices("chat, Gmail,gmail")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []Service{Gmail, Chat}) {
		t.Fatalf("got %v", got)
	}
	for _, in := range []string{"", "all", "gmail,all"} {
		got, err := ParseServices(in)
		if err != nil || !slices.Equal(got, AllServices) {
			t.Fatalf("ParseServices(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseServices("gmail,photos"); err == nil {
		t.Fatal("expected error for unknown service")
	}
	if _, err := ParseServices(" , "); err == nil {
		t.Fatal("expected error for empty selection")
	}
}

func TestScopesFor(t *testing.T) {
	got := ScopesFor([]Service{Chat, Gmail})
	want := []string{ScopeOpenID, ScopeUserinfoEmail, ScopeChatSpacesReadonly, ScopeChatMessagesRO, ScopeChatMembershipsRO,
		ScopeChatReadStateRO, ScopeChatSectionsRO, ScopeGmailReadonly}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestOptionalScopesAreNotRequired(t *testing.T) {
	granted := []string{ScopeChatSpacesReadonly, ScopeChatMessagesRO, ScopeChatMembershipsRO}
	if m := MissingScopes(granted, Chat); len(m) != 0 {
		t.Fatalf("chat missing %v", m)
	}
	if got := Chat.OptionalScopes(); !slices.Equal(got, []string{ScopeChatReadStateRO, ScopeChatSectionsRO}) {
		t.Fatalf("optional %v", got)
	}
	if Gmail.OptionalScopes() != nil {
		t.Fatal("gmail has no optional scopes")
	}
}

func TestMissingAndGranted(t *testing.T) {
	granted := []string{ScopeOpenID, ScopeGmailReadonly, ScopeChatSpacesReadonly}
	if m := MissingScopes(granted, Gmail); len(m) != 0 {
		t.Fatalf("gmail missing %v", m)
	}
	if m := MissingScopes(granted, Chat); len(m) != 2 {
		t.Fatalf("chat missing %v", m)
	}
	if g := GrantedServices(granted); !slices.Equal(g, []Service{Gmail}) {
		t.Fatalf("granted %v", g)
	}
	if !Drive.Valid() || Service("x").Valid() {
		t.Fatal("Valid mismatch")
	}
}
