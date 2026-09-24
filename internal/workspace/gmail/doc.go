// Package gmail implements read-only Gmail operations (search, get message,
// get thread, labels, attachments) on top of google.golang.org/api/gmail/v1.
//
// It exposes plain Go types with snake_case JSON tags and knows nothing
// about cobra, MCP or output formats. Construct the API client with New
// using the options from auth.ClientProvider.ClientOptions(ctx, auth.Gmail).
package gmail
