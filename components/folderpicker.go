package components

import (
	"github.com/elaous/xango"
)

// Folder states, for FolderPickerProps.State.
const (
	FolderNone      = "none"  // no folder chosen
	FolderReady     = "ready" // chosen, and the browser lets the page write it
	FolderReconnect = "reconnect"
)

// FolderPickerProps configures FolderPicker.
type FolderPickerProps struct {
	Supported                       bool
	State, Name                     string
	OnChoose, OnReconnect, OnForget func()
	Disabled                        bool
}

func FolderPicker(p FolderPickerProps) xango.Node {
	btn := func(class, label string, on func()) xango.Node {
		attrs := []xango.Attr{xango.A("type", "button"), xango.A("class", class)}
		if p.Disabled {
			attrs = append(attrs, xango.A("disabled", "disabled"))
		} else if on != nil {
			attrs = append(attrs, xango.On("click", func(xango.Event) { on() }))
		}
		return xango.El("button", attrs, xango.Text(label))
	}
	var children []xango.Node
	switch {
	case !p.Supported:
		children = append(children, xango.El("p", []xango.Attr{xango.A("class", "gj-folder-unsupported")},
			xango.Text(tr("folder.unsupported", "This browser cannot save to a folder you choose (Chrome, Edge and Opera can). Use This browser's storage, or download your documents."))))
	case p.State == FolderReady:
		children = append(children,
			xango.El("span", []xango.Attr{xango.A("class", "gj-folder-name")}, xango.Text(trf("folder.saving", "Saving to the folder %s", p.Name))),
			btn("gj-folder-change", tr("folder.change", "Choose another folder"), p.OnChoose),
			btn("gj-folder-forget", tr("folder.forget", "Stop using this folder"), p.OnForget))
	case p.State == FolderReconnect:
		children = append(children,
			xango.El("p", []xango.Attr{xango.A("class", "gj-folder-reconnect"), xango.A("role", "status")},
				xango.Text(trf("folder.reconnect", "The browser needs your permission again to write the folder %s. It is not used until you reconnect it.", p.Name))),
			btn("gj-folder-reconnect-button", tr("folder.reconnectButton", "Reconnect folder"), p.OnReconnect),
			btn("gj-folder-forget", tr("folder.forget", "Stop using this folder"), p.OnForget))
	default:
		children = append(children, btn("gj-folder-choose", tr("folder.choose", "Choose a folder…"), p.OnChoose))
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-folder")}, children...)
}
