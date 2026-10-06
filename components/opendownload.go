package components

import (
	"fmt"
	"strconv"

	"github.com/elaous/gojiberry/xango"
)

// OpenDownload is the question asked before a document is downloaded as an opendocs
// (.open) file: whether to keep its version history in the file, and how much of the
// file each part is.
type OpenDownload struct {
	// Name is the document's name.
	Name string
	// Current is the size in bytes of the current version in the file, History the size
	// of all the versions together, and Versions how many there are.
	Current, History int64
	Versions         int
	// Drop is set when the history is to be left out of the file.
	Drop bool
	// OnDrop sets Drop; OnConfirm downloads; OnCancel closes the dialog.
	OnDrop    func(drop bool)
	OnConfirm func()
	OnCancel  func()
}

// SizeText writes a size as the nearest whole KiB, MiB or GiB, with the exact bytes:
// "340 KiB (348,160 bytes)". Under 1 KiB it is just the bytes.
func SizeText(n int64) string {
	exact := groupDigits(n) + " " + tr("size.bytes", "bytes")
	if n == 1 {
		exact = "1 " + tr("size.byte", "byte")
	}
	unit, name := int64(0), ""
	switch {
	case n >= 1<<30:
		unit, name = 1<<30, "GiB"
	case n >= 1<<20:
		unit, name = 1<<20, "MiB"
	case n >= 1<<10:
		unit, name = 1<<10, "KiB"
	default:
		return exact
	}
	return fmt.Sprintf("%d %s (%s)", (n+unit/2)/unit, name, exact)
}

func groupDigits(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := ""
	if n < 0 {
		neg, s = "-", s[1:]
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return neg + string(out)
}

// OpenDownloadDialog is the dialog of an OpenDownload: the size of the current version
// and of the history, a checkbox to drop the history, and what the file will weigh.
func OpenDownloadDialog(d OpenDownload) xango.Node {
	line := func(class, text string) xango.Node {
		return xango.El("p", []xango.Attr{xango.A("class", class)}, xango.Text(text))
	}
	history := tr("opendl.noHistory", "No saved versions.")
	if d.Versions > 0 {
		history = trf("opendl.history", "Version history: %d versions, %s.", d.Versions, SizeText(d.History))
	}
	total := d.Current
	if !d.Drop {
		total += d.History
	}
	body := []xango.Node{
		line("gj-opendl-current", trf("opendl.current", "Current version: %s.", SizeText(d.Current))),
		line("gj-opendl-history", history),
	}
	if d.Versions > 0 {
		body = append(body, Checkbox(CheckboxProps{
			Label:    tr("opendl.drop", "Drop the version history (download only the current version)"),
			Checked:  d.Drop,
			OnChange: d.OnDrop,
		}))
	}
	body = append(body, line("gj-opendl-total", trf("opendl.total", "The file will be about %s.", SizeText(total))))
	cancel := Button(ButtonProps{Label: tr("confirm.no", "Cancel"), Variant: ButtonSecondary, OnClick: func(xango.Event) {
		if d.OnCancel != nil {
			d.OnCancel()
		}
	}})
	confirm := Button(ButtonProps{Label: tr("docs.download", "Download"), Variant: ButtonPrimary, OnClick: func(xango.Event) {
		if d.OnConfirm != nil {
			d.OnConfirm()
		}
	}})
	return Dialog(DialogProps{Open: true, Title: trf("opendl.title", "Download %s as .open", d.Name), Body: body,
		Actions: []xango.Node{cancel, confirm}, OnClose: d.OnCancel})
}
