// Package buildinfo holds values injected at build time through -ldflags:
// the release version, the commit, the build date and the embedded OAuth
// client used when no credentials.json is provided.
//
// Example:
//
//	go build -ldflags "-X github.com/digio/gwork-cli/internal/buildinfo.Version=v1.0.0 \
//	  -X github.com/digio/gwork-cli/internal/buildinfo.OAuthClientID=... \
//	  -X github.com/digio/gwork-cli/internal/buildinfo.OAuthClientSecret=..."
//
// OAuth client secrets must never be committed to the repository.
package buildinfo

import (
	"fmt"
	"runtime"
)

// Values overridden with -ldflags "-X ...". They are variables (not
// constants) precisely so the linker can set them.
var (
	// Version is the semantic version of the build, "dev" for local builds.
	Version = "dev"
	// Commit is the short git commit the binary was built from.
	Commit = ""
	// Date is the build date in RFC 3339 format.
	Date = ""
	// OAuthClientID is the embedded Desktop OAuth client ID, if any.
	OAuthClientID = ""
	// OAuthClientSecret is the embedded Desktop OAuth client secret, if any.
	// Google considers Desktop client secrets non-confidential.
	OAuthClientSecret = ""
	// HostedDomain is the default Google Workspace domain used as the "hd"
	// hint during login. GWORK_HOSTED_DOMAIN overrides it at runtime.
	HostedDomain = ""
)

// HasEmbeddedClient reports whether an OAuth client was embedded at build time.
func HasEmbeddedClient() bool {
	return OAuthClientID != "" && OAuthClientSecret != ""
}

// String returns a one-line human readable description of the build.
func String() string {
	s := "gwork " + Version
	if Commit != "" {
		s += " (" + Commit
		if Date != "" {
			s += ", " + Date
		}
		s += ")"
	}
	return fmt.Sprintf("%s %s/%s %s", s, runtime.GOOS, runtime.GOARCH, runtime.Version())
}

// UserAgent returns the User-Agent product token sent to Google APIs.
func UserAgent() string {
	return "gwork/" + Version
}
