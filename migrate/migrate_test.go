package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/export"
	"github.com/elaous/gojiberry/saver"
)

// wordFile is a real .docx with the text in it.
func wordFile(t testing.TB, text string) []byte {
	t.Helper()
	doc := docfile.Document{Title: text, Content: []docfile.Item{{Chars: &docfile.Chars{Text: text}}}}
	data, err := export.DOCX{}.Export(export.FromDocument(doc))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// fakeDrive serves a tiny Google Drive: root has the folder "Work" and the doc
// "Plan"; Work has "Budget" (and a second page of results).
func fakeDrive(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/drive/v3/files":
			q := r.URL.Query().Get("q")
			if !strings.Contains(q, "trashed = false") {
				t.Errorf("trashed files are listed: %q", q)
			}
			switch {
			case strings.HasPrefix(q, "'root' in parents"):
				_ = json.NewEncoder(w).Encode(map[string]any{"files": []map[string]any{
					{"id": "f1", "name": "Work", "mimeType": googleFolderMIM},
					{"id": "d1", "name": "Plan", "mimeType": googleDocMIME, "modifiedTime": "2026-01-02T03:04:05Z"},
				}})
			case strings.HasPrefix(q, "'f1' in parents") && r.URL.Query().Get("pageToken") == "":
				_ = json.NewEncoder(w).Encode(map[string]any{"nextPageToken": "p2", "files": []map[string]any{{"id": "d2", "name": "Budget", "mimeType": googleDocMIME}}})
			case strings.HasPrefix(q, "'f1' in parents"):
				_ = json.NewEncoder(w).Encode(map[string]any{"files": []map[string]any{{"id": "d3", "name": "Notes", "mimeType": googleDocMIME}}})
			default:
				t.Errorf("unexpected query %q", q)
			}
		case strings.HasSuffix(r.URL.Path, "/export"):
			if r.URL.Query().Get("mimeType") != docxMIME {
				t.Errorf("exports as %q", r.URL.Query().Get("mimeType"))
			}
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/drive/v3/files/"), "/export")
			if id == "d3" {
				http.Error(w, "export failed", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write(wordFile(t, "text of "+id))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newSink(t *testing.T) (*SaverSink, *saver.Saver) {
	t.Helper()
	tg, err := saver.Build(context.Background(), "mem", "Memory", docstoreConfig())
	if err != nil {
		t.Fatal(err)
	}
	s := &saver.Saver{Targets: []saver.Target{tg}}
	return &SaverSink{Saver: s, Target: "mem"}, s
}

func TestMigrateFromGoogleDocsKeepsFoldersAndReportsFailures(t *testing.T) {
	ctx := context.Background()
	srv := fakeDrive(t)
	src := &GoogleDocs{Token: "tok", BaseURL: srv.URL}
	top, err := src.List(ctx, "")
	if err != nil || len(top) != 2 || !top[0].Folder || top[1].Name != "Plan" || top[1].Modified.IsZero() {
		t.Fatalf("%+v %v", top, err)
	}
	if _, err := (&GoogleDocs{Token: "wrong", BaseURL: srv.URL}).List(ctx, ""); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("a bad token: %v", err)
	}
	kids, err := src.List(ctx, "f1") // two pages
	if err != nil || len(kids) != 2 {
		t.Fatalf("%+v %v", kids, err)
	}

	sink, s := newSink(t)
	var progress []string
	rep, err := Run(ctx, src, top, sink, Options{Tag: "google-docs", OnProgress: func(done, total int, name string) {
		progress = append(progress, name)
		if total != 3 {
			t.Errorf("total %d", total)
		}
	}})
	if err != nil || rep.Imported != 2 || len(rep.Failed) != 1 || rep.Failed[0].Name != "Notes" || !strings.Contains(rep.Failed[0].Err.Error(), "500") {
		t.Fatalf("%+v %v", rep, err)
	}
	if len(progress) != 4 || progress[3] != "" {
		t.Errorf("progress %v", progress)
	}
	live, _, folders, err := s.Overview(ctx, "mem")
	if err != nil || len(folders) != 1 || folders[0].Name != "Work" || len(live) != 2 {
		t.Fatalf("%+v %+v %v", live, folders, err)
	}
	for _, e := range live {
		inWork := e.Folder == folders[0].ID
		if e.Name == "Budget" != inWork || e.Name != "Budget" && e.Name != "Plan" {
			t.Errorf("%+v", e)
		}
		if len(e.Tags) != 1 || e.Tags[0] != "google-docs" {
			t.Errorf("tags %v", e.Tags)
		}
		d, _ := s.Load(ctx, "mem", e.ID)
		if !strings.Contains(docText(d), "text of") {
			t.Errorf("%s holds %q", e.Name, docText(d))
		}
	}
	// Migrating again into the same place reuses the folder and makes new documents.
	sink2 := &SaverSink{Saver: s, Target: "mem"}
	if _, err := Run(ctx, src, top[:1], sink2, Options{}); err != nil {
		t.Fatal(err)
	}
	if _, _, folders, _ := s.Overview(ctx, "mem"); len(folders) != 1 {
		t.Errorf("a second run made another Work folder: %+v", folders)
	}
}

func docText(d docfile.Document) string {
	var b strings.Builder
	for _, it := range d.Content {
		if it.Chars != nil {
			b.WriteString(it.Chars.Text)
		}
	}
	return b.String()
}

func TestMigrateFromOneDriveFollowsPagesAndSkipsOtherFiles(t *testing.T) {
	ctx := context.Background()
	var srv *httptest.Server
	var mu sync.Mutex
	var seen []string
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Path)
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/me/drive/root/children" && r.URL.Query().Get("page") == "":
			_ = json.NewEncoder(w).Encode(map[string]any{"@odata.nextLink": srv.URL + "/me/drive/root/children?page=2", "value": []map[string]any{
				{"id": "a", "name": "Letter.docx", "size": 10, "file": map[string]any{}},
				{"id": "b", "name": "Sheet.xlsx", "file": map[string]any{}},
				{"id": "c", "name": "Reports", "folder": map[string]any{}},
			}})
		case r.URL.Path == "/me/drive/root/children":
			_ = json.NewEncoder(w).Encode(map[string]any{"value": []map[string]any{{"id": "e", "name": "Memo.DOCX", "file": map[string]any{}}}})
		case r.URL.Path == "/me/drive/items/c/children":
			_ = json.NewEncoder(w).Encode(map[string]any{"value": []map[string]any{{"id": "d", "name": "Q1.docx", "file": map[string]any{}}}})
		case strings.HasSuffix(r.URL.Path, "/content"):
			_, _ = w.Write(wordFile(t, "word "+strings.Split(r.URL.Path, "/")[4]))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	src := &OneDrive{Token: "tok", BaseURL: srv.URL}
	items, err := src.List(ctx, "")
	if err != nil || len(items) != 3 { // Letter, Reports, Memo; not the spreadsheet
		t.Fatalf("%+v %v", items, err)
	}
	sink, s := newSink(t)
	rep, err := Run(ctx, src, items, sink, Options{})
	if err != nil || rep.Imported != 3 || len(rep.Failed) != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	live, _, folders, _ := s.Overview(ctx, "mem")
	names := map[string]bool{}
	for _, e := range live {
		names[e.Name] = true
	}
	if !names["Letter"] || !names["Memo"] || !names["Q1"] || len(folders) != 1 || folders[0].Name != "Reports" {
		t.Errorf("%v %+v", names, folders)
	}
}

func TestMigrateWordFilesFromTheComputerAndLimits(t *testing.T) {
	ctx := context.Background()
	files := Files{{"One.docx", wordFile(t, "one")}, {"Broken.docx", []byte("not a zip")}}
	items, _ := files.List(ctx, "")
	sink, _ := newSink(t)
	rep, err := Run(ctx, files, items, sink, Options{})
	if err != nil || rep.Imported != 1 || len(rep.Failed) != 1 || rep.Failed[0].Name != "Broken.docx" {
		t.Fatalf("%+v %v", rep, err)
	}
	if DocName("Report.DOCX") != "Report" || DocName(".docx") != "Untitled" || DocName("a.b") != "a.b" {
		t.Error("names")
	}

	// Cancelling stops the run.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Run(cctx, files, items, sink, Options{}); !errors.Is(err, context.Canceled) {
		t.Errorf("%v", err)
	}
	// Too many files: truncated.
	many := make(Files, MaxFiles+5)
	for i := range many {
		many[i].Name = "x.docx"
	}
	mi, _ := many.List(ctx, "")
	rep, _ = Run(ctx, many, mi, discard{}, Options{Import: func([]byte) (docfile.Document, error) { return docfile.Document{}, nil }})
	if !rep.Truncated || rep.Imported != MaxFiles {
		t.Errorf("%+v", rep)
	}
}

type discard struct{}

func (discard) Save(context.Context, []string, string, string, docfile.Document) error { return nil }

func docstoreConfig() docstore.Config { return docstore.Config{Backend: "memory"} }

func TestSessionBrowsesSelectsAndMigrates(t *testing.T) {
	srv := fakeDrive(t)
	sink, saverUnder := newSink(t)
	var done []Report
	s := NewSession([]SourceChoice{{Name: "google", Label: "Google Docs", New: func(tok string) Source { return &GoogleDocs{Token: tok, BaseURL: srv.URL} }}}, func() Sink { return sink })
	s.Pending.Inline = true
	s.OnDone = func(r Report) { done = append(done, r) }

	p := s.Props(nil)
	if p.Connected || len(p.Sources) != 1 || p.OnPickFiles != nil {
		t.Fatalf("%+v", p)
	}
	s.Props(nil).OnConnect() // no platform yet
	if s.Props(nil).Error == "" {
		t.Error("connecting with no platform should say so")
	}
	s.SetSource("google")
	s.SetToken("wrong")
	s.Props(nil).OnConnect()
	if e := s.Props(nil).Error; !strings.Contains(e, "401") {
		t.Errorf("a bad token: %q", e)
	}
	s.SetToken("tok")
	s.Props(nil).OnConnect()
	p = s.Props(nil)
	if !p.Connected || p.Error != "" || len(p.Items) != 2 || p.Items[0].Name != "Work" || len(p.Path) != 0 {
		t.Fatalf("%+v", p)
	}
	// Into the folder and back up.
	p.OnOpenFolder("f1")
	p = s.Props(nil)
	if len(p.Path) != 1 || p.Path[0].Name != "Work" || len(p.Items) != 2 {
		t.Fatalf("%+v", p)
	}
	p.OnOpenFolder("")
	if p = s.Props(nil); len(p.Path) != 0 || len(p.Items) != 2 {
		t.Fatalf("%+v", p)
	}
	// Select only the document at the top and migrate it.
	p.OnToggle("d1")
	if p = s.Props(nil); !p.Items[1].Selected || p.Items[0].Selected {
		t.Fatalf("%+v", p.Items)
	}
	p.OnStart()
	p = s.Props(nil)
	if !p.Reported || p.Imported != 1 || len(p.Failed) != 0 || p.Busy || len(done) != 1 {
		t.Fatalf("%+v", p)
	}
	live, _, _, _ := saverUnder.Overview(context.Background(), "mem")
	if len(live) != 1 || live[0].Name != "Plan" || live[0].Tags[0] != "google-docs" {
		t.Errorf("%+v", live)
	}
	// Nothing selected: everything listed, folders walked.
	s.Props(nil).OnToggle("d1") // unselect it
	s.Props(nil).OnStart()      // nothing selected: Work and Plan, so Budget and Plan come, Notes fails
	if p = s.Props(nil); p.Imported != 2 || len(p.Failed) != 1 {
		t.Errorf("%+v", p)
	}
	s.Props(nil).OnSelectAll()
	s.Props(nil).OnToggle("d1")
	s.Props(nil).OnStart() // only the folder: Budget comes, Notes fails
	if p = s.Props(nil); p.Imported != 1 || len(p.Failed) != 1 {
		t.Errorf("%+v", p)
	}
	// Word files from the computer.
	if err := s.ImportFiles(context.Background(), Files{{"Mine.docx", wordFile(t, "mine")}}); err != nil {
		t.Fatal(err)
	}
	if p = s.Props(nil); p.Imported != 1 {
		t.Errorf("%+v", p)
	}
	if err := s.OpenFolder(context.Background(), "nope"); err == nil {
		t.Error("an unknown folder")
	}
}
