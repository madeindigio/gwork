package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/madeindigio/gwork/internal/testutil"
)

type uploaded struct {
	space, uploadType, filename, contentType, data string
}

// attachMux fakes media.upload (multipart/related: JSON metadata + media)
// and messages.create, echoing the attachments in the created message.
func attachMux(t *testing.T, ups *[]uploaded, msgs *[]map[string]any, failUpload bool) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /upload/v1/spaces/{id}/attachments:upload", func(w http.ResponseWriter, r *http.Request) {
		if failUpload {
			testutil.WriteGoogleError(w, 400, "badRequest", "bad file")
			return
		}
		mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mt != "multipart/related" {
			t.Errorf("content type %q (%v)", r.Header.Get("Content-Type"), err)
			return
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		meta, err := mr.NextPart()
		if err != nil {
			t.Error(err)
			return
		}
		var req map[string]any
		if err := json.NewDecoder(meta).Decode(&req); err != nil {
			t.Error(err)
		}
		media, err := mr.NextPart()
		if err != nil {
			t.Error(err)
			return
		}
		data, _ := io.ReadAll(media)
		name, _ := req["filename"].(string)
		*ups = append(*ups, uploaded{space: r.PathValue("id"), uploadType: r.URL.Query().Get("uploadType"),
			filename: name, contentType: media.Header.Get("Content-Type"), data: string(data)})
		testutil.WriteJSON(t, w, map[string]any{"attachmentDataRef": map[string]any{
			"resourceName": "res-" + name, "attachmentUploadToken": "tok-" + name}})
	})
	mux.HandleFunc("POST /v1/spaces/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		*msgs = append(*msgs, body)
		space := "spaces/" + r.PathValue("id")
		atts := []any{}
		list, _ := body["attachment"].([]any)
		for i, a := range list {
			ref, _ := a.(map[string]any)["attachmentDataRef"].(map[string]any)
			tok, _ := ref["attachmentUploadToken"].(string)
			atts = append(atts, map[string]any{"name": fmt.Sprintf("%s/messages/M1/attachments/A%d", space, i),
				"contentName": strings.TrimPrefix(tok, "tok-"), "source": "UPLOADED_CONTENT"})
		}
		testutil.WriteJSON(t, w, map[string]any{"name": space + "/messages/M1", "text": body["text"],
			"space": map[string]any{"name": space}, "createTime": "2026-09-24T12:00:00Z", "attachment": atts})
	})
	mux.HandleFunc("GET /v1/spaces:findDirectMessage", func(w http.ResponseWriter, _ *http.Request) {
		testutil.WriteJSON(t, w, map[string]any{"name": "spaces/DM1", "spaceType": "DIRECT_MESSAGE"})
	})
	return mux
}

func TestSendMessageAttachments(t *testing.T) {
	a := Upload{Filename: "report.pdf", ContentType: "application/pdf", Data: []byte("%PDF-1.4 fake")}
	b := Upload{Filename: "notes.txt", ContentType: "text/plain; charset=utf-8", Data: []byte("hello notes")}
	img := Upload{Filename: "photo.png", ContentType: "image/png", Data: []byte("png data")}
	vid := Upload{Filename: "clip.mp4", ContentType: "video/mp4", Data: []byte("mp4 data")}
	tests := []struct {
		name      string
		in        SendInput
		wantSpace string
		wantText  any
	}{
		{name: "text and two media files", in: SendInput{Space: "AAA", Text: "see files", Attachments: []Upload{img, vid}},
			wantSpace: "AAA", wantText: "see files"},
		{name: "attachment only", in: SendInput{Space: "spaces/AAA", Text: "  ", Attachments: []Upload{a}},
			wantSpace: "AAA", wantText: nil},
		{name: "dm", in: SendInput{UserEmail: "bob@digio.es", Attachments: []Upload{b}},
			wantSpace: "DM1", wantText: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var ups []uploaded
			var msgs []map[string]any
			svc := newTestService(t, attachMux(t, &ups, &msgs, false))
			m, err := SendMessage(context.Background(), svc, tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if len(ups) != len(tc.in.Attachments) {
				t.Fatalf("uploads %+v", ups)
			}
			for i, u := range ups {
				want := tc.in.Attachments[i]
				if u.space != tc.wantSpace || u.filename != want.Filename || u.contentType != want.ContentType ||
					u.data != string(want.Data) || u.uploadType != "multipart" {
					t.Errorf("upload %d = %+v", i, u)
				}
			}
			if len(msgs) != 1 || msgs[0]["text"] != tc.wantText {
				t.Fatalf("messages %v", msgs)
			}
			list, _ := msgs[0]["attachment"].([]any)
			if len(list) != len(tc.in.Attachments) {
				t.Fatalf("attachment %v", msgs[0]["attachment"])
			}
			for i, x := range list {
				ref, _ := x.(map[string]any)["attachmentDataRef"].(map[string]any)
				name := tc.in.Attachments[i].Filename
				if ref["attachmentUploadToken"] != "tok-"+name || ref["resourceName"] != "res-"+name {
					t.Errorf("attachmentDataRef %d = %v", i, ref)
				}
			}
			if len(m.Attachments) != len(tc.in.Attachments) || m.Attachments[0].ContentName != tc.in.Attachments[0].Filename {
				t.Errorf("result attachments %+v", m.Attachments)
			}
		})
	}
}

func TestSendMessageUploadError(t *testing.T) {
	var ups []uploaded
	var msgs []map[string]any
	svc := newTestService(t, attachMux(t, &ups, &msgs, true))
	_, err := SendMessage(context.Background(), svc, SendInput{Space: "AAA", Text: "x",
		Attachments: []Upload{{Filename: "a.txt", Data: []byte("a")}}})
	if err == nil || !strings.Contains(err.Error(), "upload attachment a.txt to spaces/AAA") {
		t.Fatalf("err = %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("message posted after a failed upload: %v", msgs)
	}
}

func TestSendMessageDetectsContentType(t *testing.T) {
	var ups []uploaded
	var msgs []map[string]any
	svc := newTestService(t, attachMux(t, &ups, &msgs, false))
	_, err := SendMessage(context.Background(), svc, SendInput{Space: "AAA", Attachments: []Upload{
		{Filename: "pic.png", Data: []byte("not really")},
		{Filename: "noext", Data: []byte("\x89PNG\r\n\x1a\n")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(ups) != 2 || ups[0].contentType != "image/png" || ups[1].contentType != "image/png" {
		t.Errorf("uploads %+v", ups)
	}
	if ct := DetectContentType("noext", []byte("%PDF-1.4")); ct != "application/pdf" {
		t.Errorf("sniffed %q", ct)
	}
}

func TestSendInputValidateMultipleAttachments(t *testing.T) {
	img := Upload{Filename: "a.png", Data: []byte("x")}
	vid := Upload{Filename: "b.mp4", Data: []byte("x")}
	pdf := Upload{Filename: "c.pdf", Data: []byte("x")}
	if _, _, err := (SendInput{Space: "AAA", Attachments: []Upload{img, vid}}).Validate(); err != nil {
		t.Errorf("images and videos: %v", err)
	}
	if _, _, err := (SendInput{Space: "AAA", Attachments: []Upload{pdf}}).Validate(); err != nil {
		t.Errorf("one document: %v", err)
	}
	_, _, err := SendInput{Space: "AAA", Attachments: []Upload{img, pdf}}.Validate()
	if err == nil || !strings.Contains(err.Error(), "c.pdf") || !strings.Contains(err.Error(), "only when all are images or videos") {
		t.Errorf("mixed: %v", err)
	}
}

func TestSendInputValidateAttachments(t *testing.T) {
	ok := []byte("x")
	tests := []struct {
		name    string
		up      Upload
		wantErr string
	}{
		{name: "ok", up: Upload{Filename: "a b.txt", Data: ok}},
		{name: "empty file ok", up: Upload{Filename: "a.txt"}},
		{name: "no name", up: Upload{Filename: " ", Data: ok}, wantErr: "filename is required"},
		{name: "newline", up: Upload{Filename: "a\nb.txt", Data: ok}, wantErr: "line break"},
		{name: "cr", up: Upload{Filename: "a\rb.txt", Data: ok}, wantErr: "line break"},
		{name: "nul", up: Upload{Filename: "a\x00b.txt", Data: ok}, wantErr: "NUL"},
		{name: "dir", up: Upload{Filename: "dir/a.txt", Data: ok}, wantErr: "base name"},
		{name: "windows dir", up: Upload{Filename: `dir\a.txt`, Data: ok}, wantErr: "base name"},
		{name: "dotdot", up: Upload{Filename: "..", Data: ok}, wantErr: "base name"},
		{name: "content type", up: Upload{Filename: "a", ContentType: "text/plain\r\nX: y", Data: ok}, wantErr: "content type"},
		{name: "too big", up: Upload{Filename: "big.bin", Data: make([]byte, MaxAttachmentSize+1)}, wantErr: "limit is 209715200 bytes"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := SendInput{Space: "AAA", Attachments: []Upload{tc.up}}.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}
	if _, _, err := (SendInput{Space: "AAA", Text: " "}).Validate(); err == nil || !strings.Contains(err.Error(), "text is required") {
		t.Errorf("text-less message without attachments: %v", err)
	}
	long := SendInput{Space: "AAA", Text: strings.Repeat("a", MaxTextLength+1), Attachments: []Upload{{Filename: "a", Data: ok}}}
	if _, _, err := long.Validate(); err == nil || !strings.Contains(err.Error(), "limit is 4096") {
		t.Errorf("long text with attachment: %v", err)
	}
}
