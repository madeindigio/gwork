# Google Workspace admin guide for gwork

This guide is for the digio.es Google Workspace **super admin** (or an admin
with the Security settings privilege). `gwork` is an internal CLI and MCP
server that accesses users' Gmail, Drive, Calendar and Chat with their own
OAuth consent: read-only by default, with opt-in write access for Gmail,
Calendar and Chat that each user must grant explicitly. It uses an **Internal** OAuth app in a Google Cloud project
under the digio organization (see [setup-google-cloud.md](setup-google-cloud.md)).

What the admin needs from the maintainer: the **OAuth client ID** of the
`gwork` Desktop client (for example `1234567890-abc...apps.googleusercontent.com`),
one per project (prod, and dev if used).

Scopes requested by every login (read-only): `openid`, `userinfo.email`,
`gmail.readonly`, `calendar.readonly`, `drive.readonly`, `chat.spaces.readonly`,
`chat.messages.readonly`, `chat.memberships.readonly`.

Write scopes, requested only when a user runs `gwork auth login --write ...`:
`gmail.modify` (drafts, send, labels, trash), `calendar.events` (create, update,
delete events, RSVP) and `chat.messages.create` (post Chat messages as the
user). Drive has no write access.

Google classifies `gmail.readonly`, `gmail.modify` and `drive.readonly` as
restricted, high-risk scopes. To keep gwork read-only for your organization,
use **Specific Google data** (section 1) and allow only the read-only scopes:
a `--write` login is then refused with `admin_policy_enforced`.

Admin console paths below were checked against Google's documentation in
September 2026 (see [References](#references)).

---

## 1. Mark the gwork OAuth client as Trusted

Path: **Menu > Security > Access and data control > API controls** [R1].

1. Sign in to the [Google Admin console](https://admin.google.com) as super admin.
2. Go to **Menu > Security > Access and data control > API controls**.
3. Under **App access control**, click **Manage Third-Party App Access**.
4. Under **Configured apps**, click **Add app > OAuth App Name Or Client ID**
   (shown in some consoles as **Configure new app**).
5. Paste the `gwork` **OAuth client ID** and click **Search**; select the app
   (it appears as `gwork`, the name set in Branding) and the client ID.
6. **Scope**: keep the top-level organizational unit (all users) or pick the
   OUs/groups that may use gwork. Click **Continue**.
7. **Access to Google Data**: choose **Trusted** ("Can access all Google
   services (both restricted and unrestricted)").
   - Alternative, least privilege: **Specific Google data** ("Can request data
     access only to scopes that you specify") and allow only the scopes listed
     above: the read-only ones, plus the write scopes you want to permit.
   - Optional: **Exempt from having API access blocked by Context-Aware Access
     levels** if CAA would otherwise block CLI use.
8. Click **Continue**, then **Finish**.
9. Repeat for the dev client ID if developers use it.

### Alternative: trust all internal apps

In **API controls**, under **Settings > Internal apps**, the **Trust internal
apps** checkbox lets apps built by your organization (Internal OAuth apps owned
by digio projects) access restricted Google Workspace APIs without configuring
each one [R1]. Configuring the specific client ID (steps above) is more
explicit and auditable; prefer it.

### What happens if third-party access is restricted

API controls has two independent levers [R1]:

- **Unconfigured third-party apps** (App access control > Settings). Options
  include allowing all, **Allow users to access third-party apps that only
  request basic info needed for Sign in with Google**, and **Don't allow users
  to access any third-party apps**. With either restrictive option, an app that
  is not configured cannot get Gmail/Drive/Chat/Calendar data: users of an
  untrusted `gwork` would see `access_denied` / `admin_policy_enforced` at
  login.
- **Manage Google Services** (API controls > **Manage Google Services**). For
  each service (Gmail, Drive and Docs, Calendar, Chat, ...) access can be
  **Unrestricted** or **Restricted**. Restricted means only apps configured as
  **Trusted** (or **Specific Google data** covering that scope) may use its
  scopes. Google warns: "If you change access to Restricted, any previously
  installed apps that you haven't trusted stop working and tokens are
  revoked." Users then get `invalid_grant` and cannot log in again until the
  client is trusted.

Marking the `gwork` client ID as **Trusted** makes it keep working under both
restrictions.

## 2. Google Chat settings

For users to call the Chat API through `gwork` [R2][R3]:

1. Users need Google Chat turned on (Business/Enterprise edition with Chat
   access): **Menu > Apps > Google Workspace > Google Chat** must be **ON** for
   their OU.
2. Go to **Menu > Apps > Google Workspace > Google Chat**, click **Chat apps**.
3. Set **Allow users to install Chat apps** to **On** for the **top-level
   organizational unit**, then **Save**.

Google's help: "Apps must be turned on for the top organizational unit to work
with the Chat API, and to ensure apps work properly in spaces", and "If you
don't allow this, then Chat APIs may be prevented from working properly." Note
that turning the setting off "disables all app usage, including personal app
use." `gwork` does not install a Chat app or bot; it reads spaces and
messages the user can already see and, with write access, posts messages as
the user in spaces and DMs they already belong to. This setting still gates
Chat API access.

If **Chat** is set to **Restricted** under **Manage Google Services**, the
`gwork` client must be Trusted (section 1).

## 3. Google Cloud session control (reauthentication policy)

Path: **Menu > Security > Access and data control > Google Cloud session
control** (<https://admin.google.com/ac/security/reauth/admin-tools>),
Security Settings privilege required [R4].

1. Select the organizational unit.
2. Under **Reauthentication policy**, choose **Require reauthentication** or
   **Never require reauthentication**.
3. Set **Reauthentication frequency** (1 to 24 hours) and **Reauthentication
   method** (Password or Security key).
4. Optionally check **Exempt Trusted apps**.
5. Click **Save** (or **Override** for a child OU).

Scope of the policy: it applies to the Google Cloud console, the `gcloud` CLI,
and "any applications (including third-party applications, or your own
applications) that require user authorization for Google Cloud scopes" [R4].
`gwork` requests only Workspace scopes (Gmail, Drive, Calendar, Chat), not
Google Cloud scopes such as `cloud-platform`, so this policy is not expected to
expire its sessions. Google's refresh-token documentation lists the same
limit: "For Google Cloud Platform APIs - the session length set by the admin
could have been exceeded" [R5].

If a policy does end a session, the effect is the same as revoking the
refresh token: the next refresh fails with `invalid_grant`, `gwork` reports that
reauthentication is required, and the user runs:

```sh
gwork auth login                      # same account, requests a new token
gwork auth login --services gmail,drive,calendar,chat   # if specific services are needed
```

Checking **Exempt Trusted apps** together with marking `gwork` as Trusted
(section 1) avoids forced re-login if the policy ever covers it.

Other admin actions that cause `invalid_grant` for users [R5]: changing a
service to Restricted without trusting the app, user password change (tokens
with Gmail scopes), revoking the app for the user.

## 4. Revoking access

### For one user

Path: **Menu > Directory > Users** > select the user > **Security** >
**Connected applications** [R6].

1. Open the user, go to **Security**, click **Connected applications**.
2. Hover over `gwork`, click **Remove**, confirm **Remove**, then **Done**.

Caveat from Google: "Removing data access for an app doesn't prevent a user
from using the app in the future ... Once a user signs into the app again, data
access is restored." To stop the user from re-authorizing, also block the app
for that user's OU (below). To sign the user out everywhere, use **Security >
Sign-in cookies > Reset** in the same page.

Users can revoke their own grant at <https://myaccount.google.com/connections>
(or run `gwork auth logout`, which deletes the local token).

### Organization-wide (or per OU)

1. **Menu > Security > Access and data control > API controls > Manage
   Third-Party App Access**.
2. Select `gwork` under **Configured apps** (configure it first if needed).
3. Click **Change access** and choose **Blocked** ("Can't access any Google
   service"), for the whole organization or the chosen OUs/groups. **Save**.

Stronger options:

- Ask the maintainer to delete (or rotate) the OAuth client in the Cloud
  project (**Google Auth Platform > Clients**), which invalidates every token
  issued to it. Deleting the whole Cloud project has the same effect.
- Setting a service to **Restricted** without trusting `gwork` revokes its
  tokens for that service [R1].

## References

- [R1] Control which third-party & internal apps access Google Workspace data: <https://knowledge.workspace.google.com/admin/apps/control-which-third-party-and-internal-apps-access-google-workspace-data> (formerly <https://support.google.com/a/answer/7281227>)
- [R2] Allow users to install Chat apps: <https://knowledge.workspace.google.com/admin/chat/allow-users-to-install-chat-apps> (formerly <https://support.google.com/a/answer/7651360>)
- [R3] Authenticate and authorize as a Google Chat user: <https://developers.google.com/workspace/chat/authenticate-authorize-chat-user>
- [R4] Set session length for Google Cloud services: <https://knowledge.workspace.google.com/admin/security/set-session-length-for-google-cloud-services> (formerly <https://support.google.com/a/answer/9368756>)
- [R5] Using OAuth 2.0 to Access Google APIs (refresh token expiration): <https://developers.google.com/identity/protocols/oauth2>
- [R6] Manage a user's security settings: <https://knowledge.workspace.google.com/admin/security/manage-a-users-security-settings> (formerly <https://support.google.com/a/answer/2537800>)
- Configure the Google Chat API: <https://developers.google.com/workspace/chat/configure-chat-api>
