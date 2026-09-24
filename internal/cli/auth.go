package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/digio/gwork-cli/internal/auth"
	"github.com/digio/gwork-cli/internal/buildinfo"
	"github.com/digio/gwork-cli/internal/config"
	"github.com/digio/gwork-cli/internal/output"
)

func newAuthCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Log in, inspect and manage Google accounts",
	}
	cmd.AddCommand(
		newAuthLoginCmd(a),
		newAuthStatusCmd(a),
		newAuthListCmd(a),
		newAuthUseCmd(a),
		newAuthLogoutCmd(a),
	)
	return cmd
}

// accountInfo is the JSON shape of an account in auth commands.
type accountInfo struct {
	Account         string         `json:"account"`
	Default         bool           `json:"default"`
	Services        []auth.Service `json:"services"`
	Scopes          []string       `json:"scopes,omitempty"`
	Storage         string         `json:"storage,omitempty"`
	TokenExpiry     *time.Time     `json:"token_expiry,omitempty"`
	HasRefreshToken bool           `json:"has_refresh_token"`
	Created         *time.Time     `json:"created,omitempty"`
}

func newAccountInfo(st *auth.StoredToken, cfg *config.Config, storage string) accountInfo {
	info := accountInfo{
		Account:  config.NormalizeEmail(st.Email),
		Default:  cfg.DefaultAccount == config.NormalizeEmail(st.Email),
		Services: st.Services(),
		Scopes:   st.Scopes,
		Storage:  storage,
	}
	if info.Services == nil {
		info.Services = []auth.Service{}
	}
	if st.Token != nil {
		info.HasRefreshToken = st.Token.RefreshToken != ""
		if !st.Token.Expiry.IsZero() {
			exp := st.Token.Expiry
			info.TokenExpiry = &exp
		}
	}
	if !st.Created.IsZero() {
		c := st.Created
		info.Created = &c
	}
	return info
}

func hostedDomain() string {
	if d := os.Getenv(auth.EnvHostedDomain); d != "" {
		return d
	}
	return buildinfo.HostedDomain
}

func newAuthLoginCmd(a *App) *cobra.Command {
	var services string
	var noBrowser bool
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in with a Google account (opens the browser)",
		Long: "Log in with a Google account using OAuth (loopback redirect + PKCE) and store\n" +
			"the token in the OS keyring. Only read-only scopes are requested.\n\n" +
			"Run it again with --services to grant more services later.",
		Example:     "  gwork auth login\n  gwork auth login --services gmail,calendar",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{annotationNoTimeout: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			svcs, err := auth.ParseServices(services)
			if err != nil {
				return err
			}
			dir, err := a.ConfigDir()
			if err != nil {
				return err
			}
			creds, err := a.credentials(dir)
			if err != nil {
				return err
			}
			timeout := auth.DefaultLoginTimeout
			if f := cmd.Flags().Lookup("timeout"); f != nil && f.Changed && a.Flags.Timeout > 0 {
				timeout = a.Flags.Timeout
			}
			open := a.OpenBrowser
			if noBrowser {
				open = nil
			}
			st, err := auth.Login(cmd.Context(), auth.LoginOptions{
				Credentials:  creds,
				Services:     svcs,
				HostedDomain: hostedDomain(),
				LoginHint:    a.Flags.Account,
				Timeout:      timeout,
				OpenBrowser:  open,
				Prompt:       a.Err,
				Endpoint:     a.OAuthEndpoint,
				UserinfoURL:  a.UserinfoURL,
			})
			if err != nil {
				return err
			}
			store := a.tokenStore(dir)
			if err := store.Save(st); err != nil {
				return fmt.Errorf("save token: %w", err)
			}
			cfg, err := config.Load(dir)
			if err != nil {
				return err
			}
			cfg.AddAccount(st.Email)
			if err := config.Save(dir, cfg); err != nil {
				return err
			}
			info := newAccountInfo(st, cfg, store.Backend(st.Email))
			return a.Print(info, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "Logged in as %s (services: %s; stored in %s)\n",
					info.Account, joinOrNone(info.Services), info.Storage)
				if err == nil && !info.Default {
					_, err = fmt.Fprintf(w, "Default account is %s; switch with: gwork auth use %s\n", cfg.DefaultAccount, info.Account)
				}
				return err
			})
		},
	}
	cmd.Flags().StringVar(&services, "services", "all", "comma separated services to grant: gmail,calendar,drive,chat or all")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "do not open the browser; only print the URL")
	return cmd
}

type statusInfo struct {
	accountInfo
	CredentialsSource string `json:"credentials_source,omitempty"`
	CredentialsError  string `json:"credentials_error,omitempty"`
	ConfigDir         string `json:"config_dir"`
}

func newAuthStatusCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the current account, granted services and token storage",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			dir, err := a.ConfigDir()
			if err != nil {
				return err
			}
			cfg, err := config.Load(dir)
			if err != nil {
				return err
			}
			account, err := auth.ResolveAccount(a.Flags.Account, os.Getenv(auth.EnvAccount), cfg)
			if err != nil {
				return err
			}
			store := a.tokenStore(dir)
			st, err := store.Load(account)
			if errors.Is(err, auth.ErrTokenNotFound) {
				return &auth.Error{Kind: auth.ErrNotLoggedIn, Message: "no stored credentials for " + account, Hint: "run: gwork auth login"}
			}
			if err != nil {
				return err
			}
			info := statusInfo{accountInfo: newAccountInfo(st, cfg, store.Backend(account)), ConfigDir: dir}
			if creds, err := a.credentials(dir); err != nil {
				info.CredentialsError = err.Error()
			} else {
				info.CredentialsSource = creds.Source
				if creds.Path != "" {
					info.CredentialsSource += " (" + creds.Path + ")"
				}
			}
			return a.Print(info, func(w io.Writer) error {
				expiry := ""
				if info.TokenExpiry != nil {
					expiry = info.TokenExpiry.Local().Format(time.RFC3339)
				}
				creds := info.CredentialsSource
				if info.CredentialsError != "" {
					creds = "unavailable: " + strings.SplitN(info.CredentialsError, "\n", 2)[0]
				}
				return output.KeyValues(w,
					"Account", info.Account,
					"Default", fmt.Sprint(info.Default),
					"Services", joinOrNone(info.Services),
					"Storage", info.Storage,
					"Refresh token", fmt.Sprint(info.HasRefreshToken),
					"Access expiry", expiry,
					"OAuth client", creds,
					"Config dir", info.ConfigDir,
				)
			})
		},
	}
}

func newAuthListCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the accounts that have logged in",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			dir, err := a.ConfigDir()
			if err != nil {
				return err
			}
			cfg, err := config.Load(dir)
			if err != nil {
				return err
			}
			store := a.tokenStore(dir)
			accounts := make([]accountInfo, 0, len(cfg.Accounts))
			for _, email := range cfg.Accounts {
				st, err := store.Load(email)
				if err != nil {
					accounts = append(accounts, accountInfo{Account: email, Default: cfg.DefaultAccount == email, Services: []auth.Service{}})
					continue
				}
				info := newAccountInfo(st, cfg, store.Backend(email))
				info.Scopes = nil
				accounts = append(accounts, info)
			}
			return a.Print(accounts, func(w io.Writer) error {
				if len(accounts) == 0 {
					_, err := fmt.Fprintln(w, "No accounts. Run: gwork auth login")
					return err
				}
				rows := make([][]string, 0, len(accounts))
				for _, acc := range accounts {
					def := ""
					if acc.Default {
						def = "*"
					}
					storage := acc.Storage
					if storage == "" {
						storage = "missing"
					}
					rows = append(rows, []string{def, acc.Account, joinOrNone(acc.Services), storage})
				}
				return output.Table(w, []string{"DEFAULT", "ACCOUNT", "SERVICES", "STORAGE"}, rows)
			})
		},
	}
}

func newAuthUseCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "use <email>",
		Short: "Set the default account",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			dir, err := a.ConfigDir()
			if err != nil {
				return err
			}
			cfg, err := config.Load(dir)
			if err != nil {
				return err
			}
			email := config.NormalizeEmail(args[0])
			if !cfg.HasAccount(email) {
				return &auth.Error{Kind: auth.ErrNotLoggedIn, Message: "unknown account " + email, Hint: "run: gwork auth login --account " + email}
			}
			cfg.DefaultAccount = email
			if err := config.Save(dir, cfg); err != nil {
				return err
			}
			return a.Print(map[string]string{"default_account": email}, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "Default account set to %s\n", email)
				return err
			})
		},
	}
}

type logoutResult struct {
	Account string `json:"account"`
	Revoked bool   `json:"revoked"`
	Removed bool   `json:"removed"`
}

func newAuthLogoutCmd(a *App) *cobra.Command {
	var all, noRevoke bool
	cmd := &cobra.Command{
		Use:   "logout [email]",
		Short: "Revoke and delete the stored token of an account",
		Long: "Revoke the account's token at Google (best effort), delete it from the\n" +
			"keyring or token file, and forget the account. Without an email, the\n" +
			"current account is logged out.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := a.ConfigDir()
			if err != nil {
				return err
			}
			cfg, err := config.Load(dir)
			if err != nil {
				return err
			}
			var targets []string
			switch {
			case all:
				targets = append(targets, cfg.Accounts...)
			case len(args) == 1:
				targets = []string{config.NormalizeEmail(args[0])}
			default:
				acc, err := auth.ResolveAccount(a.Flags.Account, os.Getenv(auth.EnvAccount), cfg)
				if err != nil {
					return err
				}
				targets = []string{acc}
			}
			store := a.tokenStore(dir)
			results := make([]logoutResult, 0, len(targets))
			for _, email := range targets {
				res := logoutResult{Account: email}
				st, err := store.Load(email)
				if err == nil && !noRevoke && st.Token != nil {
					tok := st.Token.RefreshToken
					if tok == "" {
						tok = st.Token.AccessToken
					}
					if err := auth.Revoke(cmd.Context(), nil, a.RevokeURL, tok); err != nil {
						fmt.Fprintf(a.Err, "warning: could not revoke the token of %s: %v\n", email, err)
					} else {
						res.Revoked = true
					}
				}
				switch err := store.Delete(email); {
				case err == nil:
					res.Removed = true
				case !errors.Is(err, auth.ErrTokenNotFound):
					return fmt.Errorf("delete token of %s: %w", email, err)
				}
				wasKnown := cfg.HasAccount(email)
				cfg.RemoveAccount(email)
				if !res.Removed && !wasKnown {
					return &auth.Error{Kind: auth.ErrNotLoggedIn, Message: "unknown account " + email, Hint: "list accounts with: gwork auth list"}
				}
				results = append(results, res)
			}
			if err := config.Save(dir, cfg); err != nil {
				return err
			}
			return a.Print(results, func(w io.Writer) error {
				if len(results) == 0 {
					_, err := fmt.Fprintln(w, "No accounts to log out.")
					return err
				}
				for _, r := range results {
					if _, err := fmt.Fprintf(w, "Logged out %s\n", r.Account); err != nil {
						return err
					}
				}
				if cfg.DefaultAccount != "" {
					_, err := fmt.Fprintf(w, "Default account is now %s\n", cfg.DefaultAccount)
					return err
				}
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "log out every account")
	cmd.Flags().BoolVar(&noRevoke, "no-revoke", false, "only delete the local token, do not revoke it at Google")
	return cmd
}

func joinOrNone(svcs []auth.Service) string {
	if len(svcs) == 0 {
		return "none"
	}
	return auth.JoinServices(svcs)
}
