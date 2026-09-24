package drive

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	driveapi "google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

// DefaultMaxResults is the default number of results returned by Search.
const DefaultMaxResults = 25

// MaxResultsLimit caps the number of results Search returns.
const MaxResultsLimit = 1000

// summaryFields are the Drive fields needed to build a FileSummary.
const summaryFields = "id,name,mimeType,modifiedTime,size,owners(emailAddress),webViewLink,parents,driveId"

// fileFields are the Drive fields needed to build a File.
const fileFields = summaryFields + ",description,createdTime,lastModifyingUser(emailAddress,displayName),shared,exportLinks,shortcutDetails(targetId,targetMimeType)"

// FileSummary is the compact representation of a Drive file returned by
// Search.
type FileSummary struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	MimeType     string    `json:"mime_type"`
	Type         string    `json:"type"`
	ModifiedTime time.Time `json:"modified_time,omitzero"`
	Size         int64     `json:"size,omitempty"`
	Owners       []string  `json:"owners"`
	WebViewLink  string    `json:"web_view_link,omitempty"`
	Parents      []string  `json:"parents"`
	DriveID      string    `json:"drive_id,omitempty"`
}

// File is the full metadata of a Drive file returned by GetFile.
type File struct {
	FileSummary
	Description       string    `json:"description,omitempty"`
	CreatedTime       time.Time `json:"created_time,omitzero"`
	LastModifyingUser string    `json:"last_modifying_user,omitempty"`
	Shared            bool      `json:"shared"`
	// ExportLinks lists the MIME types a Google-native file can be exported
	// to (the keys of Drive's exportLinks).
	ExportLinks []string `json:"export_links"`
	// ShortcutTargetID is the target file of a shortcut.
	ShortcutTargetID string `json:"shortcut_target_id,omitempty"`
}

// Search lists files matching opts across My Drive and shared drives,
// following pages until opts.Max results are collected.
func Search(ctx context.Context, opts SearchOptions, clientOpts ...option.ClientOption) ([]FileSummary, error) {
	q, err := BuildQuery(opts)
	if err != nil {
		return nil, err
	}
	svc, err := New(ctx, clientOpts...)
	if err != nil {
		return nil, err
	}
	limit := opts.Max
	if limit <= 0 {
		limit = DefaultMaxResults
	}
	limit = min(limit, MaxResultsLimit)
	orderBy := opts.OrderBy
	// Drive rejects orderBy on full-text searches (they are ordered by
	// relevance), whether fullText comes from Text or from RawQuery.
	if orderBy == "" && !strings.Contains(q, "fullText") {
		orderBy = "modifiedTime desc"
	}

	out := make([]FileSummary, 0, min(limit, 100))
	pageToken := ""
	for len(out) < limit {
		call := svc.Files.List().
			Q(q).
			Corpora("allDrives").
			SupportsAllDrives(true).
			IncludeItemsFromAllDrives(true).
			PageSize(int64(min(limit-len(out), MaxResultsLimit))).
			Fields("nextPageToken", "files("+summaryFields+")").
			Context(ctx)
		if orderBy != "" {
			call = call.OrderBy(orderBy)
		}
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}
		res, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("search drive files: %w", err)
		}
		for _, f := range res.Files {
			if len(out) == limit {
				break
			}
			out = append(out, summaryFromAPI(f))
		}
		if res.NextPageToken == "" || len(res.Files) == 0 {
			break
		}
		pageToken = res.NextPageToken
	}
	return out, nil
}

// GetFile returns the metadata of a file (shared drives supported).
func GetFile(ctx context.Context, fileID string, clientOpts ...option.ClientOption) (*File, error) {
	svc, err := New(ctx, clientOpts...)
	if err != nil {
		return nil, err
	}
	f, err := getAPIFile(ctx, svc, fileID)
	if err != nil {
		return nil, err
	}
	return fileFromAPI(f), nil
}

// getAPIFile fetches the raw metadata of a file with fileFields.
func getAPIFile(ctx context.Context, svc *driveapi.Service, fileID string) (*driveapi.File, error) {
	if fileID == "" {
		return nil, fmt.Errorf("file id is required")
	}
	f, err := svc.Files.Get(fileID).SupportsAllDrives(true).Fields(fileFields).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("get drive file %s: %w", fileID, err)
	}
	return f, nil
}

// summaryFromAPI converts an API file to a FileSummary.
func summaryFromAPI(f *driveapi.File) FileSummary {
	s := FileSummary{
		ID:           f.Id,
		Name:         f.Name,
		MimeType:     f.MimeType,
		Type:         TypeLabel(f.MimeType),
		ModifiedTime: parseTime(f.ModifiedTime),
		Size:         f.Size,
		Owners:       make([]string, 0, len(f.Owners)),
		WebViewLink:  f.WebViewLink,
		Parents:      make([]string, 0, len(f.Parents)),
		DriveID:      f.DriveId,
	}
	for _, o := range f.Owners {
		if o != nil && o.EmailAddress != "" {
			s.Owners = append(s.Owners, o.EmailAddress)
		}
	}
	s.Parents = append(s.Parents, f.Parents...)
	return s
}

// fileFromAPI converts an API file to a File.
func fileFromAPI(f *driveapi.File) *File {
	out := &File{
		FileSummary: summaryFromAPI(f),
		Description: f.Description,
		CreatedTime: parseTime(f.CreatedTime),
		Shared:      f.Shared,
		ExportLinks: make([]string, 0, len(f.ExportLinks)),
	}
	if u := f.LastModifyingUser; u != nil {
		out.LastModifyingUser = u.EmailAddress
		if out.LastModifyingUser == "" {
			out.LastModifyingUser = u.DisplayName
		}
	}
	for k := range f.ExportLinks {
		out.ExportLinks = append(out.ExportLinks, k)
	}
	sort.Strings(out.ExportLinks)
	if f.ShortcutDetails != nil {
		out.ShortcutTargetID = f.ShortcutDetails.TargetId
	}
	return out
}

// parseTime parses an RFC 3339 timestamp, returning the zero time when s is
// empty or invalid.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
