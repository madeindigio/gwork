# Releasing gwork

Pushing a tag `v*` to `madeindigio/gwork` runs
[`.github/workflows/release.yml`](../.github/workflows/release.yml). The
pipeline mirrors `digiogithub/git-in-track`:

| Job | Runner | Output |
|---|---|---|
| `check` | ubuntu | `go test -race`, fails fast if a required secret is missing |
| `linux` | ubuntu | `gwork_<v>_linux_{amd64,arm64}.tar.gz` (unsigned) |
| `windows` | windows, environment `release` | `gwork_<v>_windows_{amd64,arm64}.zip`, Authenticode via Azure Trusted Signing (OIDC) |
| `macos` | macos | `gwork_<v>_darwin_{arm64,amd64}.zip`, Developer ID signed (hardened runtime) and notarized |
| `release` | ubuntu | GitHub release with all archives and `checksums.txt` |

Every build embeds the OAuth Desktop client with `-X` ldflags.

## Repository configuration

Secrets (Settings > Secrets and variables > Actions > Secrets):

| Secret | Value |
|---|---|
| `GWORK_OAUTH_CLIENT_ID` | `client_id` of the Internal Desktop OAuth client ([setup](setup-google-cloud.md)) |
| `GWORK_OAUTH_CLIENT_SECRET` | its `client_secret` |
| `GWORK_HOSTED_DOMAIN` | `digio.es` (login `hd` hint) |
| `MACOS_SIGNING_BUNDLE` | base64 `.tar.gz` of `~/DIGIO_Software_Signing_Keys` (Developer ID `.p12` files + `kvagerc` with passwords and notary credentials), same bundle as git-in-track |

```sh
tar -C ~ -czf - DIGIO_Software_Signing_Keys | base64 -w0 | gh secret set MACOS_SIGNING_BUNDLE -R madeindigio/gwork
```

Variables (same values as git-in-track):

| Variable | Meaning |
|---|---|
| `AZURE_CLIENT_ID` | Entra app used for signing (federated identity) |
| `AZURE_TENANT_ID` | Entra tenant |
| `AZURE_SUBSCRIPTION_ID` | subscription holding the Trusted Signing account |
| `AZURE_SIGNING_ENDPOINT` | `https://weu.codesigning.azure.net/` |
| `AZURE_SIGNING_ACCOUNT` | Trusted Signing account |
| `AZURE_SIGNING_CERT_PROFILE` | certificate profile |

Environment `release` must exist; the Windows job runs in it.

Azure: the Entra app `AZURE_CLIENT_ID` needs federated credentials for this
repository (issuer `https://token.actions.githubusercontent.com`, audience
`api://AzureADTokenExchange`):

- `repo:madeindigio/gwork:environment:release`
- `repo:madeindigio@143500854/gwork@1396628993:environment:release`
  (immutable-ID form, used when the org has immutable subject claims)

## Cutting a release

1. `make test lint` and the [smoke test](smoke-test.md) on `main`.
2. `git tag -a v0.1.0 -m "gwork v0.1.0" && git push origin v0.1.0`.
3. Watch `gh run watch -R madeindigio/gwork`. To rebuild an existing tag:
   `gh workflow run release.yml -R madeindigio/gwork -f tag=v0.1.0`.
