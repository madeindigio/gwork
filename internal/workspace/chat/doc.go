// Package chat implements read-only Google Chat operations (list spaces,
// find a direct message space, list and get messages, client-side search)
// on top of google.golang.org/api/chat/v1.
//
// It exposes plain Go types with snake_case JSON tags and knows nothing
// about cobra, MCP or output formats. Construct the API client with New
// using the options from auth.ClientProvider.ClientOptions(ctx, auth.Chat).
package chat
