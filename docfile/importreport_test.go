package docfile

import "testing"

func TestImportReportStaysWithTheDocument(t *testing.T) {
	var d Document
	if _, ok := d.ImportReport(); ok {
		t.Fatal("a new document has no report")
	}
	want := ImportReport{Name: "memo.docx", Known: true, Losses: []ImportLoss{{Feature: "comment", Treatment: "dropped", Count: 2}, {Feature: "columns", Treatment: "simplified", Note: "one column"}}}
	d.SetImportReport(&want)
	b, err := JSON.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	back, err := JSON.Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := back.ImportReport()
	if !ok || got.Name != want.Name || !got.Known || len(got.Losses) != 2 || got.Losses[0] != want.Losses[0] || got.Losses[1] != want.Losses[1] {
		t.Errorf("report after saving = %+v, %v", got, ok)
	}
	d.Set(ImportKey, "not json")
	if _, ok := d.ImportReport(); ok {
		t.Error("an unreadable report is ignored")
	}
	d.SetImportReport(nil)
	if _, ok := d.Get(ImportKey); ok {
		t.Error("the report is removed")
	}
}

func TestVerticalTextIsPartOfThePageLayout(t *testing.T) {
	var d Document
	if d.PageLayout().Vertical {
		t.Fatal("new documents are horizontal")
	}
	d.SetPageLayout(PageLayout{Vertical: true})
	b, err := JSON.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	back, err := JSON.Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if !back.PageLayout().Vertical {
		t.Error("vertical text did not survive saving")
	}
	back.SetPageLayout(PageLayout{})
	if back.PageLayout().Vertical {
		t.Error("horizontal again")
	}
}
