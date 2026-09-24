// Package calendar implements read-only Google Calendar operations (list
// calendars, list events in a time window, get event) on top of
// google.golang.org/api/calendar/v3.
//
// It exposes plain Go types with snake_case JSON tags and knows nothing
// about cobra, MCP or output formats. Construct the API client with New
// using the options from auth.ClientProvider.ClientOptions(ctx, auth.Calendar).
package calendar
