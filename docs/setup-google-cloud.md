# Google Cloud setup for gwork (maintainer guide)

This guide is for the maintainer who creates and owns the OAuth client that
`gwork` uses to access Gmail, Google Drive, Google Chat and Google Calendar
(read-only) in digio's Google Workspace (`digio.es`).

Result of this guide: a Google Cloud project under the digio organization with
the four APIs enabled, an **Internal** OAuth app, and a **Desktop app** OAuth
client whose ID and secret are used by `gwork`.

For the Workspace super admin tasks (trusting the client, Chat settings, session
control, revocation) see [workspace-admin.md](workspace-admin.md).

Facts below were checked against Google's official documentation in September
2026 (see [References](#references)).

---

## 1. Create the Google Cloud project

The app must use the **Internal** audience (no Google verification, only
`digio.es` accounts can authorize it). Internal is only available to projects
that belong to a Google Cloud organization: "Projects associated with a Google
Cloud Organization can configure Internal users to limit authorization requests
to members of the organization" [R1]. A project created without an organization
(no parent / blank Location) cannot use Internal.

1. Sign in to the [Google Cloud console](https://console.cloud.google.com/) with
   your `@digio.es` account.
2. In the project picker, click **New project**.
3. Set **Organization** to `digio.es` and **Location** to the organization (or a
   folder inside it).
4. Choose a project ID (6-30 chars, lowercase letters, digits and hyphens, must
   start with a letter, cannot end with a hyphen, globally unique and never
   reused) [R2]. Suggested: `digio-gwork-prod`.
5. Click **Create**.

CLI equivalent:

```sh
gcloud organizations list                       # find the digio.es ORGANIZATION_ID
gcloud projects create digio-gwork-prod --organization=ORGANIZATION_ID
gcloud config set project digio-gwork-prod
```

### Separate dev and prod projects

Use two projects, for example `digio-gwork-dev` and `digio-gwork-prod`:

- The **prod** client is the one embedded in released binaries (GoReleaser/CI)
  and trusted by the Workspace admin. Change it rarely.
- The **dev** client is for local development and experiments (new scopes,
  testing error paths, deleting/recreating clients) without breaking users.
- Each project has its own Google Auth Platform config, clients and API quotas.
  Revoking or rotating the dev client never logs out real users.
- Both must be Internal and both client IDs must be trusted by the admin if
  third-party access is restricted (see workspace-admin.md).

Repeat sections 2-5 for each project.

## 2. Enable the APIs

Console: **Menu > APIs & Services > Library**, search each API and click
**Enable** [R3]:

| API | Service name | Console link |
|---|---|---|
| Gmail API | `gmail.googleapis.com` | <https://console.cloud.google.com/apis/library/gmail.googleapis.com> |
| Google Calendar API | `calendar-json.googleapis.com` | <https://console.cloud.google.com/apis/library/calendar-json.googleapis.com> |
| Google Drive API | `drive.googleapis.com` | <https://console.cloud.google.com/apis/library/drive.googleapis.com> |
| Google Chat API | `chat.googleapis.com` | <https://console.cloud.google.com/apis/library/chat.googleapis.com> |

Note: the Calendar service name is `calendar-json.googleapis.com`, not
`calendar.googleapis.com`.

CLI:

```sh
gcloud services enable \
  gmail.googleapis.com \
  calendar-json.googleapis.com \
  drive.googleapis.com \
  chat.googleapis.com \
  --project=digio-gwork-prod

gcloud services list --enabled --project=digio-gwork-prod   # verify
```

## 3. Configure Google Auth Platform

Console: **Menu > Google Auth Platform**
(<https://console.cloud.google.com/auth/overview>). If the project has never
been configured, click **Get started**, which walks through Branding and
Audience [R4][R5].

### 3.1 Branding (minimal)

**Google Auth Platform > Branding**:

1. **App name**: `gwork`.
2. **User support email**: your address or a group such as a support alias.
3. **Developer contact information**: the maintainers' email(s).
4. Logo, home page, privacy policy and terms links are optional for an Internal
   app; leave them empty.
5. Click **Save**.

### 3.2 Audience = Internal

**Google Auth Platform > Audience**:

1. Set **User type** to **Internal** ("only available to users within an
   organization") [R4].
2. Save.

Consequences:

- Only `digio.es` accounts can authorize; others get `org_internal`.
- No test users, no publishing status, no Google verification. "For apps used
  only internally by your Google Workspace organization, scopes aren't listed on
  the consent screen and use of restricted or sensitive scopes doesn't require
  further review by Google" [R4].
- The 7-day refresh token expiry does **not** apply (it only affects External
  apps in Testing status, see Troubleshooting).

### 3.3 Data Access (scopes)

**Google Auth Platform > Data Access > Add or remove scopes**. Add exactly these
read-only scopes (from `docs/architecture.md`), then **Update** and **Save**:

| Service | Scope | Classification |
|---|---|---|
| base | `openid` | non-sensitive |
| base | `https://www.googleapis.com/auth/userinfo.email` | non-sensitive |
| gmail | `https://www.googleapis.com/auth/gmail.readonly` | restricted [R6] |
| calendar | `https://www.googleapis.com/auth/calendar.readonly` | sensitive [R14] |
| drive | `https://www.googleapis.com/auth/drive.readonly` | restricted [R7] |
| chat | `https://www.googleapis.com/auth/chat.spaces.readonly` | sensitive [R8] |
| chat | `https://www.googleapis.com/auth/chat.messages.readonly` | sensitive [R8] |
| chat | `https://www.googleapis.com/auth/chat.memberships.readonly` | sensitive [R8] |

If a scope is not listed in the picker, paste it into **Manually add scopes**.
For an Internal app, restricted/sensitive scopes need no verification, but a
Workspace admin may still restrict Gmail/Drive access org-wide; that is why the
client ID should be marked **Trusted** (see workspace-admin.md). Do not add
write scopes: `gwork` v1 is read-only by design.

## 4. Create the OAuth client (Desktop app)

**Google Auth Platform > Clients** [R9]:

1. Click **Create client**.
2. **Application type**: **Desktop app**.
3. **Name**: e.g. `gwork desktop (prod)` (visible only in the console).
4. Click **Create**.
5. Download the JSON from the confirmation dialog (**Download JSON**) or later
   from the client's row/details page in **Clients**. Save it outside the repo,
   e.g. `~/.config/gwork/credentials.json`.

No redirect URIs are configured: Desktop clients accept loopback redirects
(`http://127.0.0.1:<port>`), which is what `gwork auth login` uses together with
PKCE (S256) [R10].

Give the client ID to the Workspace super admin so it can be marked Trusted.

### 4.1 Using the client with gwork

Resolution order (first match wins):

1. Flag: `gwork --credentials /path/to/credentials.json auth login`
2. Env var: `export GWORK_CREDENTIALS=/path/to/credentials.json`
3. File: `<configDir>/credentials.json` (e.g. `~/.config/gwork/credentials.json`
   on Linux, where `<configDir>` follows XDG / `os.UserConfigDir`)
4. Embedded at build time via ldflags (used for internal/released builds).

Build-time embedding:

```make
# Makefile (values come from the environment / CI secrets, never from git)
PKG := github.com/madeindigio/gwork/internal/buildinfo
LDFLAGS := -s -w \
  -X $(PKG).OAuthClientID=$(GWORK_OAUTH_CLIENT_ID) \
  -X $(PKG).OAuthClientSecret=$(GWORK_OAUTH_CLIENT_SECRET) \
  -X $(PKG).HostedDomain=digio.es

build:
	go build -ldflags "$(LDFLAGS)" -o bin/gwork ./cmd/gwork
```

```sh
export GWORK_OAUTH_CLIENT_ID=$(jq -r .installed.client_id ~/.config/gwork/credentials.json)
export GWORK_OAUTH_CLIENT_SECRET=$(jq -r .installed.client_secret ~/.config/gwork/credentials.json)
make build
```

For GoReleaser, set the same `-X` flags in `builds[].ldflags` reading
`{{ .Env.GWORK_OAUTH_CLIENT_ID }}` / `{{ .Env.GWORK_OAUTH_CLIENT_SECRET }}` from
CI secrets. `HostedDomain` only adds the `hd=digio.es` hint to the login URL
(it pre-selects the account); enforcement comes from the Internal audience.
It can be overridden with `GWORK_HOSTED_DOMAIN`.

### 4.2 Is the client secret secret?

Google's docs for installed apps: "the client secret is obviously not treated
as a secret" [R11], and installed apps "cannot keep secrets" [R10]. Embedding it
in the internal binary is therefore acceptable. Still:

- **Never commit** `credentials.json`, the client ID/secret, or tokens to the
  repo (keep `credentials*.json` in `.gitignore`; inject via CI secrets).
- If it leaks, anyone could impersonate the `gwork` app in a consent screen
  (only to `digio.es` users, since the app is Internal). Rotate by adding a new
  secret / new client in **Clients**, rebuilding, then deleting the old one.

## 5. Google Chat API "Configuration" page: required?

**Verified answer: No, not for gwork.** Google's "Configure the Google Chat
API" page states [R12]:

> "To perform read-only API calls with user authentication, like getting spaces
> and listing messages, you only need to enable the API and create an OAuth
> client."
>
> "To perform create, update, and delete API calls, you must also configure the
> Chat API."

`gwork` only uses user authentication with `chat.spaces.readonly`,
`chat.messages.readonly` and `chat.memberships.readonly`, so the Chat app
(App name, Avatar URL, Description on **Chat API > Configuration**) does not
need to be configured.

Caveat: the generic "Authenticate and authorize as a Google Chat user" page
still lists "Enable and configure the Google Chat API with a name, icon, and
description for your Chat app" as a prerequisite for its samples [R13]. The
more specific configuration page above is authoritative for read-only calls.
If Chat calls fail with an error mentioning a missing Chat app configuration,
fill in the page as a harmless fallback: open
<https://console.cloud.google.com/apis/api/chat.googleapis.com/hangouts-chat>,
set **App name** `gwork` (up to 25 chars), **Avatar URL** (HTTPS, square
PNG/JPEG, 256 px+), **Description** (up to 40 chars), turn **Enable
interactive features** off, and **Save** [R12]. It is required anyway if
`gwork` ever adds Chat write operations.

Also required for Chat: the user needs a Business or Enterprise Workspace
account with access to Google Chat [R13], and Chat apps must be allowed by the
admin (see workspace-admin.md).

## 6. Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `Error 403: org_internal` | "The OAuth client ID in the request is part of a project limiting access to Google Accounts in a specific Google Cloud Organization" [R10]: a non-`digio.es` account (e.g. personal Gmail) tried to log in. | Log in with the `@digio.es` account. `gwork` reports that only digio accounts can log in. |
| `access_denied` | User clicked Cancel/Deny on the consent screen, or the admin blocked the app. | Rerun `gwork auth login` and accept; if it persists, ask the admin (app Blocked or not trusted). |
| `admin_policy_enforced` | "The Google Account is unable to authorize one or more scopes requested due to the policies of their Google Workspace administrator" [R10], e.g. Gmail/Drive set to Restricted and the client not Trusted. | Admin marks the client ID as **Trusted** or enables **Trust internal apps** (workspace-admin.md). |
| `invalid_grant` on refresh | Refresh token no longer valid. Reasons per Google [R11]: user revoked access; unused for six months; password change (tokens with Gmail scopes); too many live refresh tokens for the account; time-based access expired; admin set a requested service to Restricted; Google Cloud session length exceeded (only for apps using Google Cloud scopes). Also a bad PKCE verifier during the initial exchange [R10]. | `gwork auth login` again (with the same `--services`). If it keeps happening, check admin restrictions. |
| `403 SERVICE_DISABLED` / "... API has not been used in project N before or it is disabled" | The API is not enabled in the project that owns the OAuth client. | Enable it (section 2) in that project; wait a few minutes and retry. |
| `403 insufficientPermissions` / `ACCESS_TOKEN_SCOPE_INSUFFICIENT` | Token lacks the service's scope (logged in without that service). | `gwork auth login --services <service>` (e.g. `chat`). Also check the scope is listed under Data Access. |
| Refresh token expires after 7 days | Only for projects with **External** user type and publishing status **Testing** [R11][R1]. | Should not happen with Internal. If it does, the project audience is wrong: set Audience to Internal. |
| Internal option unavailable in Audience | Project has no organization parent. | Create the project under the `digio.es` organization (section 1) or migrate it. |

## References

- [R1] Manage App Audience (Google Cloud Console Help): <https://support.google.com/cloud/answer/15549945>
- [R2] Creating and managing projects (Resource Manager): <https://docs.cloud.google.com/resource-manager/docs/creating-managing-projects>
- [R3] Enable Google Workspace APIs: <https://developers.google.com/workspace/guides/enable-apis>
- [R4] Configure the OAuth consent screen and choose scopes: <https://developers.google.com/workspace/guides/configure-oauth-consent>
- [R5] Get started with the Google Auth Platform: <https://support.google.com/cloud/answer/15544987>
- [R6] Gmail API scopes: <https://developers.google.com/workspace/gmail/api/auth/scopes>
- [R7] Drive API scopes: <https://developers.google.com/workspace/drive/api/guides/api-specific-auth>
- [R8] Authenticate and authorize Chat apps and Google Chat API requests (scopes): <https://developers.google.com/workspace/chat/authenticate-authorize>
- [R9] Create access credentials: <https://developers.google.com/workspace/guides/create-credentials>
- [R10] OAuth 2.0 for iOS & Desktop Apps: <https://developers.google.com/identity/protocols/oauth2/native-app>
- [R11] Using OAuth 2.0 to Access Google APIs (refresh token expiration, installed apps): <https://developers.google.com/identity/protocols/oauth2>
- [R12] Configure the Google Chat API: <https://developers.google.com/workspace/chat/configure-chat-api>
- [R13] Authenticate and authorize as a Google Chat user: <https://developers.google.com/workspace/chat/authenticate-authorize-chat-user>
- [R14] Choose Google Calendar API scopes: <https://developers.google.com/workspace/calendar/api/auth>
