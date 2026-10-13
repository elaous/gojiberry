package export

import (
	"github.com/elaous/gojiberry/docximport"
	"github.com/elaous/gojiberry/htmlimport"
)

// Features that only an import can lose: what a file written by another program holds and the editor has no
// place for. They are not in Features (no exporter writes them).
const (
	FeatHeaderFooter Feature = "header-footer"
	FeatTextBox      Feature = "text-box"
	FeatDrawing      Feature = "drawing"
	FeatEndnote      Feature = "endnote"
	FeatField        Feature = "field"
	// FeatLayout is everything but the text of a file that holds only drawn text (a PDF): fonts, styles,
	// pictures, tables and the structure.
	FeatLayout Feature = "layout"
	// What a web page holds that a document has no place for.
	FeatScript      Feature = "script"
	FeatStyleSheet  Feature = "stylesheet"
	FeatForm        Feature = "form"
	FeatFrame       Feature = "frame"
	FeatRemoteImage Feature = "remote-image"
)

// ImportFormats are the formats ImportFidelity can report on.
var ImportFormats = []string{"docx", "html", "pdf"}

// ImportFidelity reports what importing a file of this format would leave out of the document, from the file
// itself: for a Word file the elements Import has no place for (counted by docximport.Inspect), for a PDF
// everything but the text. The report is Known only for those two formats. It never fails: a file that cannot
// be inspected gives an unknown report, and the import itself says why it fails.
func ImportFidelity(format string, data []byte) Report {
	r := Report{Format: format, Complete: completeFormats[format]}
	switch format {
	case "pdf":
		r.Known = true
		r.Losses = []Loss{{Feature: FeatLayout, Treatment: Dropped, Note: NoteText}}
	case "html":
		s, err := htmlimport.Inspect(data)
		if err != nil {
			return r
		}
		r.Known = true
		for _, l := range []Loss{
			{Feature: FeatScript, Count: s.Scripts},
			{Feature: FeatStyleSheet, Count: s.Styles},
			{Feature: FeatForm, Count: s.Forms},
			{Feature: FeatFrame, Count: s.Frames},
			{Feature: FeatDrawing, Count: s.Graphics},
			{Feature: FeatMedia, Count: s.Media},
			{Feature: FeatRemoteImage, Count: s.Images, Note: "pictures on a server are not fetched"},
			{Feature: FeatLink, Count: s.Links, Note: NoteText},
			{Feature: FeatLayout, Count: s.Layout},
		} {
			if l.Count > 0 {
				l.Treatment = Dropped
				r.Losses = append(r.Losses, l)
			}
		}
	case "docx":
		s, err := docximport.Inspect(data)
		if err != nil {
			return r
		}
		r.Known = true
		for _, l := range []Loss{
			{Feature: FeatComment, Count: s.Comments},
			{Feature: FeatHeaderFooter, Count: s.HeaderFoots},
			{Feature: FeatTextBox, Count: s.TextBoxes},
			{Feature: FeatDrawing, Count: s.Drawings},
			{Feature: FeatEquation, Count: s.Equations},
			{Feature: FeatEndnote, Count: s.Endnotes},
			{Feature: FeatField, Count: s.Fields, Note: NoteText},
			{Feature: FeatBookmark, Count: s.Bookmarks},
			{Feature: FeatColumns, Count: s.Columns},
			{Feature: FeatParagraphLayout, Count: s.Layout},
		} {
			if l.Count > 0 {
				l.Treatment = Dropped
				r.Losses = append(r.Losses, l)
			}
		}
	}
	return r
}
