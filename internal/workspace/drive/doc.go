// Package drive implements read-only Google Drive operations (search, file
// metadata, text read with export of Google Docs formats, download) on top
// of google.golang.org/api/drive/v3. Shared drives are supported.
//
// It exposes plain Go types with snake_case JSON tags and knows nothing
// about cobra, MCP or output formats. Construct the API client with New
// using the options from auth.ClientProvider.ClientOptions(ctx, auth.Drive).
package drive
