package export

import (
	"github.com/elaous/gojiberry/docximport"
	"github.com/elaous/gojiberry/htmlimport"
	"github.com/elaous/gojiberry/odtimport"
	"github.com/elaous/gojiberry/transfer"
)

// Features only an import can lose.
const (
	FeatHeaderFooter Feature = "header-footer"
	FeatTextBox      Feature = "text-box"
	FeatDrawing      Feature = "drawing"
	FeatEndnote      Feature = "endnote"
	FeatField        Feature = "field"
	// FeatLayout is the layout of a file that holds only drawn text.
	FeatLayout Feature = "layout"
	// FeatScript is script that a web page holds.
	FeatScript      Feature = "script"
	FeatStyleSheet  Feature = "stylesheet"
	FeatForm        Feature = "form"
	FeatFrame       Feature = "frame"
	FeatRemoteImage Feature = "remote-image"
	// FeatCellFormat is cell formatting that Markdown holds.
	FeatCellFormat Feature = "cell-format"
	FeatRawHTML    Feature = "raw-html"
)

// ImportFormats are the formats ImportFidelity can report on.
var ImportFormats = []string{"docx", "html", "odt", "pdf", "markdown"}

// ImportFidelity reports what importing a file would leave out.
func ImportFidelity(format string, data []byte) Report {
	r := Report{Format: format, Complete: completeFormats[format]}
	switch format {
	case "pdf":
		r.Known = true
		r.Losses = []Loss{{Feature: FeatLayout, Treatment: Dropped, Note: NoteText}}
	case "markdown":
		s := transfer.InspectMarkdown(data)
		r.Known = true
		for _, l := range []Loss{
			{Feature: FeatRemoteImage, Count: s.RemoteImages, Note: "pictures on a server are not fetched"},
			{Feature: FeatCellFormat, Count: s.CellMarks, Note: NoteText},
			{Feature: FeatRawHTML, Count: s.HTML, Note: NoteText},
		} {
			if l.Count > 0 {
				l.Treatment = Dropped
				r.Losses = append(r.Losses, l)
			}
		}
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
			{Feature: FeatRemoteImage, Count: s.Images, Note: "pictures on a server are not fetched"},
			{Feature: FeatLink, Count: s.Links, Note: NoteText},
			{Feature: FeatLayout, Count: s.Layout},
		} {
			if l.Count > 0 {
				l.Treatment = Dropped
				r.Losses = append(r.Losses, l)
			}
		}
	case "odt":
		s, err := odtimport.Inspect(data)
		if err != nil {
			return r
		}
		r.Known = true
		for _, l := range []Loss{
			{Feature: FeatComment, Count: s.Comments},
			{Feature: FeatHeaderFooter, Count: s.HeaderFoots},
			{Feature: FeatTextBox, Count: s.TextBoxes},
			{Feature: FeatDrawing, Count: s.Drawings},
			{Feature: FeatSuggestion, Count: s.TrackedChanges, Note: NoteAccepted},
			{Feature: FeatEndnote, Count: s.Endnotes},
			{Feature: FeatField, Count: s.Fields, Note: NoteText},
			{Feature: FeatBookmark, Count: s.Bookmarks},
			{Feature: FeatLink, Count: s.Links, Note: NoteText},
			{Feature: FeatColumns, Count: s.Columns},
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
			{Feature: FeatHeaderFooter, Count: s.HeaderFoots},
			{Feature: FeatTextBox, Count: s.TextBoxes},
			{Feature: FeatDrawing, Count: s.Drawings},
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
