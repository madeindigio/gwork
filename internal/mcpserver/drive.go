package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/madeindigio/gwork/internal/auth"
	"github.com/madeindigio/gwork/internal/workspace/drive"
)

type driveSearchInput struct {
	QueryText     string `json:"query_text,omitempty" jsonschema:"full-text search over file names and content"`
	Name          string `json:"name,omitempty" jsonschema:"file name contains this text"`
	Type          string `json:"type,omitempty" jsonschema:"file type shortcut: doc, sheet, slides, pdf, folder, image, form or drawing (do not combine with mime_type)"`
	MimeType      string `json:"mime_type,omitempty" jsonschema:"exact MIME type, e.g. application/pdf"`
	Owner         string `json:"owner,omitempty" jsonschema:"owner email address"`
	FolderID      string `json:"folder_id,omitempty" jsonschema:"only direct children of this folder ID"`
	ModifiedAfter string `json:"modified_after,omitempty" jsonschema:"only files modified after this time: RFC 3339, YYYY-MM-DD, or relative like 7d / 24h"`
	RawQuery      string `json:"raw_query,omitempty" jsonschema:"raw Google Drive query (q syntax) ANDed with the other filters, e.g. \"starred = true\""`
	MaxResults    int    `json:"max_results,omitempty" jsonschema:"maximum number of files to return (default 25, max 1000)"`
}

type driveSearchOutput struct {
	Files []drive.FileSummary `json:"files" jsonschema:"matching files, newest first (by relevance for query_text searches)"`
}

type driveGetFileInput struct {
	FileID string `json:"file_id" jsonschema:"the Drive file ID"`
}

type driveGetFileOutput struct {
	File *drive.File `json:"file" jsonschema:"file metadata"`
}

type driveReadFileInput struct {
	FileID   string `json:"file_id" jsonschema:"the Drive file ID"`
	MaxChars int    `json:"max_chars,omitempty" jsonschema:"maximum characters of text to return (default 20000)"`
}

type driveReadFileOutput struct {
	File            drive.FileSummary `json:"file" jsonschema:"the file that was read"`
	ContentMimeType string            `json:"content_mime_type" jsonschema:"format of text: text/markdown, text/csv, text/plain or the file's own type"`
	Exported        bool              `json:"exported" jsonschema:"true when the text was exported from a Google Doc, Sheet or Slides file"`
	Text            string            `json:"text" jsonschema:"the text content"`
	Truncated       bool              `json:"truncated" jsonschema:"true when text was cut at max_chars or at the size limit"`
	Notes           []string          `json:"notes" jsonschema:"caveats about the content, e.g. only the first sheet of a spreadsheet is exported"`
}

// registerDrive registers the drive_* tools. It is called by New only when the
// drive service is requested and granted. There is deliberately no download
// tool: it would write to the local disk.
func registerDrive(s *mcp.Server, deps Deps) {
	addReadOnlyTool(s, deps, auth.Drive, &mcp.Tool{
		Name: "drive_search",
		Description: "Search Google Drive files (My Drive and shared drives, trashed files excluded). " +
			"All filters are combined with AND; with no filters it returns the most recently modified files. " +
			"Returns file IDs for drive_get_file and drive_read_file.",
	}, func(ctx context.Context, in driveSearchInput) (driveSearchOutput, error) {
		opts, err := deps.ClientOptions(ctx, auth.Drive)
		if err != nil {
			return driveSearchOutput{}, err
		}
		files, err := drive.Search(ctx, drive.SearchOptions{
			Text:          in.QueryText,
			Name:          in.Name,
			Type:          in.Type,
			MimeType:      in.MimeType,
			Owner:         in.Owner,
			FolderID:      in.FolderID,
			ModifiedAfter: in.ModifiedAfter,
			Now:           deps.CurrentTime(),
			RawQuery:      in.RawQuery,
			Max:           in.MaxResults,
		}, opts...)
		if err != nil {
			return driveSearchOutput{}, err
		}
		return driveSearchOutput{Files: files}, nil
	})

	addReadOnlyTool(s, deps, auth.Drive, &mcp.Tool{
		Name: "drive_get_file",
		Description: "Get the metadata of a Google Drive file: name, type, owners, dates, size, description, " +
			"link, parents and the formats a Google file can be exported to. Does not return the content.",
	}, func(ctx context.Context, in driveGetFileInput) (driveGetFileOutput, error) {
		opts, err := deps.ClientOptions(ctx, auth.Drive)
		if err != nil {
			return driveGetFileOutput{}, err
		}
		f, err := drive.GetFile(ctx, in.FileID, opts...)
		if err != nil {
			return driveGetFileOutput{}, err
		}
		return driveGetFileOutput{File: f}, nil
	})

	addReadOnlyTool(s, deps, auth.Drive, &mcp.Tool{
		Name: "drive_read_file",
		Description: "Read the text content of a Google Drive file. Google Docs are returned as Markdown, " +
			"Sheets as CSV (first sheet only) and Slides as plain text; text files (txt, md, csv, JSON, XML, YAML...) as-is. " +
			"Binary files (PDF, images, Office documents), Drawings and Forms are not supported and return an error.",
	}, func(ctx context.Context, in driveReadFileInput) (driveReadFileOutput, error) {
		opts, err := deps.ClientOptions(ctx, auth.Drive)
		if err != nil {
			return driveReadFileOutput{}, err
		}
		c, err := drive.Read(ctx, in.FileID, drive.ReadOptions{}, opts...)
		if err != nil {
			return driveReadFileOutput{}, err
		}
		text, cut := TruncateText(c.Text, effectiveMaxChars(in.MaxChars))
		return driveReadFileOutput{
			File:            c.File,
			ContentMimeType: c.ContentMimeType,
			Exported:        c.Exported,
			Text:            text,
			Truncated:       cut || c.Truncated,
			Notes:           c.Notes,
		}, nil
	})
}
