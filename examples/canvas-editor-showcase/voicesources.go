//go:build js && wasm

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"syscall/js"

	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/transfer"
	"github.com/elaous/gojiberry/voice/read"
	"github.com/elaous/gojiberry/voice/read/hub"
	"github.com/elaous/gojiberry/voice/read/piper"
	"github.com/elaous/gojiberry/xango"
)

// The sources of reader voices, in the Reader voices tab: the project's hub (four voices whose terms have been read, with
// the terms shown), Piper's own catalogue and the repositories the person adds (each voice has the terms its publisher
// wrote, shown before a download, and the person says they read them), voices from the person's own files (their own
// terms and responsibility), and the voices of the operating system and the browser (the "Voices on this device" reader).
// Nothing is asked of any of them until the person presses a button that says which host it contacts.

type voiceRepo struct {
	ID, Name, Address string
	loaded            bool
	err               string
}

var voiceSources struct {
	selected string // "hub" (the default), "piper", a repository's id, "files" or "device"
	repos    []*voiceRepo
	// the form to add a repository
	addName, addAddress string
	// the voices whose terms the person said they read, by voice id
	read map[string]bool
	// loading a repository
	loading string
	// own files
	ownName, ownModel, ownConfig string
	ownModelData, ownConfigData  []byte
	ownConfirmed                 bool
}

const reposKey = "gj-voice-repos"

// loadVoiceRepos reads the repositories the person added in this browser.
func loadVoiceRepos() {
	defer func() { _ = recover() }()
	raw := js.Global().Get("localStorage").Call("getItem", reposKey)
	if raw.IsNull() || raw.IsUndefined() {
		return
	}
	var list []struct{ ID, Name, Address string }
	if json.Unmarshal([]byte(raw.String()), &list) != nil {
		return
	}
	for _, r := range list {
		if piper.CheckAddress(r.Address) == nil && len(voiceSources.repos) < 8 {
			voiceSources.repos = append(voiceSources.repos, &voiceRepo{ID: r.ID, Name: r.Name, Address: r.Address})
		}
	}
}

func saveVoiceRepos() {
	defer func() { _ = recover() }()
	var list []map[string]string
	for _, r := range voiceSources.repos {
		list = append(list, map[string]string{"ID": r.ID, "Name": r.Name, "Address": r.Address})
	}
	b, _ := json.Marshal(list)
	js.Global().Get("localStorage").Call("setItem", reposKey, string(b))
}

func hostOf(address string) string {
	if u, err := url.Parse(address); err == nil && u.Host != "" {
		return u.Host
	}
	return T("voices.thisServer", "this server")
}

// voiceSourcesSection is the part of the tab that lists where voices can come from and what each source offers.
func voiceSourcesSection(u *ui, e interface {
	read.Engine
	read.Downloader
}, lang string, renderUI func()) []xango.Node {
	vs := &voiceSources
	if vs.selected == "" {
		vs.selected = "hub"
	}
	if vs.read == nil {
		vs.read = map[string]bool{}
	}
	pe := piperUI.engine
	pick := func(id string) func(xango.Event) {
		return func(xango.Event) { vs.selected = id; renderUI() }
	}
	button := func(id, label string) xango.Node {
		return components.Button(components.ButtonProps{Label: label, Variant: variantOf(vs.selected == id), OnClick: pick(id)})
	}
	buttons := []xango.Node{xango.El("span", []xango.Attr{xango.A("id", "vsrc-hub")}, button("hub", T("vsrc.hub", "The hub (voices with their terms read)"))),
		xango.El("span", []xango.Attr{xango.A("id", "vsrc-piper")}, button("piper", T("vsrc.piper", "Piper's catalogue")))}
	for i, r := range vs.repos {
		buttons = append(buttons, xango.El("span", []xango.Attr{xango.A("id", fmt.Sprintf("vsrc-repo-%d", i))}, button(r.ID, r.Name)))
	}
	buttons = append(buttons,
		xango.El("span", []xango.Attr{xango.A("id", "vsrc-add")}, button("add", T("vsrc.add", "Add a repository…"))),
		xango.El("span", []xango.Attr{xango.A("id", "vsrc-files")}, button("files", T("vsrc.files", "My own files"))),
		xango.El("span", []xango.Attr{xango.A("id", "vsrc-device")}, button("device", T("vsrc.device", "This device's voices"))))
	nodes := []xango.Node{
		xango.El("h5", nil, xango.Text(T("vsrc.title", "Where voices come from"))),
		xango.El("div", []xango.Attr{xango.A("role", "group"), xango.A("aria-label", T("vsrc.title", "Where voices come from")), xango.A("style", "display:flex;gap:6px;flex-wrap:wrap;margin-bottom:0.6em")}, buttons...),
	}
	switch vs.selected {
	case "hub":
		l, _ := hub.Load()
		nodes = append(nodes,
			para("vsrc-hub-about", fmt.Sprintf(T("vsrc.hubAbout", "Four voices, American and British, a woman's and a man's. Their terms were read on %s and are shown below; the files come from %s and are checked against their size and checksum."), l.Read, hostOf(l.Base)), false))
		nodes = append(nodes, listVoices(u, e, pe.SourceVoices(hub.ID, "*"), false, renderUI)...)
	case "piper":
		nodes = append(nodes, para("vsrc-piper-about", fmt.Sprintf(T("vsrc.piperAbout", "Every voice of Piper's catalogue for %s, from %s. Each has the terms its publisher wrote on its model card; read them before you download."), lang, hostOf(piperUI.cfg.Catalogue)), false))
		nodes = append(nodes, catalogueList(u, e, lang, renderUI)...)
	case "add":
		nodes = append(nodes, addRepositoryForm(renderUI)...)
	case "files":
		nodes = append(nodes, ownFilesForm(u, renderUI)...)
	case "device":
		nodes = append(nodes, deviceVoices()...)
	default:
		for _, r := range vs.repos {
			if r.ID == vs.selected {
				nodes = append(nodes, repoSection(u, e, r, lang, renderUI)...)
			}
		}
	}
	return nodes
}

// listVoices lists the voices with their terms and a Download button; a voice of a source whose terms the project has not
// read needs the person's word that they read them.
func listVoices(u *ui, e read.Downloader, list []read.Installable, needRead bool, renderUI func()) []xango.Node {
	if len(list) == 0 {
		return []xango.Node{para("vsrc-empty", T("vsrc.empty", "Nothing to list for this language."), false)}
	}
	var items []xango.Node
	for i, c := range list {
		i, c := i, c
		kids := []xango.Node{
			xango.El("strong", nil, xango.Text(read.Label(c.Voice))),
			xango.El("span", []xango.Attr{xango.A("style", "margin-left:6px;color:#6b7280")}, xango.Text(fmt.Sprintf("%s, %s, %s", c.Lang, c.Quality, megabytes(c.Size)))),
		}
		if c.Terms != "" {
			kids = append(kids, xango.El("div", []xango.Attr{xango.A("id", fmt.Sprintf("voices-terms-%d", i))}, xango.Text(T("vsrc.terms", "Terms: ")+c.Terms)))
		}
		if c.Credit != "" {
			kids = append(kids, xango.El("div", []xango.Attr{xango.A("id", fmt.Sprintf("voices-credit-%d", i))}, xango.Text(c.Credit)))
		}
		if c.Licence != "" {
			kids = append(kids, xango.El("a", []xango.Attr{xango.A("href", c.Licence), xango.A("target", "_blank"), xango.A("rel", "noopener noreferrer"), xango.A("id", fmt.Sprintf("voices-licence-%d", i)),
				xango.A("style", "margin-left:6px")}, xango.Text(T("voices.licence", "Whose voice and what terms"))))
		}
		switch {
		case c.Installed:
			kids = append(kids, para(fmt.Sprintf("voices-has-%d", i), T("voices.installed", "Downloaded."), false))
		case piperUI.busy == c.Key():
			pct := 0
			if piperUI.total > 0 {
				pct = int(piperUI.done * 100 / piperUI.total)
			}
			kids = append(kids,
				xango.El("span", []xango.Attr{xango.A("id", "voices-progress"), xango.A("role", "status")}, xango.Text(fmt.Sprintf(T("voices.progress", "Downloading… %d%%"), pct))),
				btn("voices-cancel", T("voices.cancel", "Cancel"), func() {
					if piperUI.cancel != nil {
						piperUI.cancel()
					}
				}, false))
		default:
			ack := !needRead || voiceSources.read[c.Key()]
			if needRead {
				kids = append(kids, xango.El("div", []xango.Attr{xango.A("id", fmt.Sprintf("voices-read-%d", i))}, components.Checkbox(components.CheckboxProps{
					Label: T("vsrc.iRead", "I have read the terms of this voice"), Checked: ack,
					OnChange: func(on bool) { voiceSources.read[c.Key()] = on; renderUI() }})))
			}
			kids = append(kids, btn(fmt.Sprintf("voices-download-%d", i), fmt.Sprintf(T("voices.download", "Download (%s)"), megabytes(c.Size)), func() {
				ctx, cancel := context.WithCancel(context.Background())
				piperUI.busy, piperUI.done, piperUI.total, piperUI.cancel, piperUI.msg = c.Key(), 0, c.Size, cancel, ""
				renderUI()
				e.Install(ctx, c.Key(), func(done, total int64) {
					piperUI.done, piperUI.total = done, total
					renderUI()
				}, func(err error) {
					cancel()
					piperUI.busy, piperUI.cancel = "", nil
					if err != nil {
						piperUI.msg = fmt.Sprintf(T("voices.failed", "The voice could not be downloaded: %s"), err.Error())
					} else {
						piperUI.msg = fmt.Sprintf(T("voices.done", "%s is downloaded and can be used."), c.Name)
						u.announce(piperUI.msg)
					}
					renderUI()
				})
			}, piperUI.busy != "" || !ack))
		}
		items = append(items, xango.El("li", []xango.Attr{xango.A("class", "gj-doc-item"), xango.A("id", fmt.Sprintf("voices-item-%d", i))}, kids...))
	}
	return []xango.Node{xango.El("ul", []xango.Attr{xango.A("class", "gj-doc-list"), xango.A("id", "voices-list")}, items...)}
}

// catalogueList is Piper's own catalogue: asked for when the person presses the button, then listed for the language.
func catalogueList(u *ui, e interface {
	read.Engine
	read.Downloader
}, lang string, renderUI func()) []xango.Node {
	var own []read.Installable
	for _, c := range e.Catalogue(lang) {
		if c.Source == "" {
			own = append(own, c)
		}
	}
	if len(own) == 0 {
		nodes := []xango.Node{
			para("voices-contact", fmt.Sprintf(T("voices.contact", "Showing the list asks %s for its catalogue of voices. Nothing else is sent."), hostOf(piperUI.cfg.Catalogue)), false),
			btn("voices-load", T("voices.load", "Show the voices to download"), func() {
				piperUI.catLoading, piperUI.catErr = true, ""
				renderUI()
				e.LoadCatalogue(context.Background(), func(err error) {
					piperUI.catLoading = false
					if err != nil {
						piperUI.catErr = err.Error()
					}
					renderUI()
				})
			}, piperUI.catLoading)}
		if piperUI.catErr != "" {
			nodes = append(nodes, para("voices-caterr", fmt.Sprintf(T("voices.catErr", "The list could not be fetched: %s"), piperUI.catErr), true))
		}
		return nodes
	}
	return listVoices(u, e, own, true, renderUI)
}

// addRepositoryForm adds a repository by the address of its catalogue.
func addRepositoryForm(renderUI func()) []xango.Node {
	vs := &voiceSources
	nodes := []xango.Node{
		para("vsrc-add-about", T("vsrc.addAbout", "A repository is a catalogue of voices (a voices.json like Piper's) on a server. Its voices have their own terms: the project has not read them, and each voice shows the model card its publisher wrote before you can download it. Nothing is asked of the server until you press Show its voices."), false),
		lineInput("vsrc-add-name", T("vsrc.addName", "A name for it"), vs.addName, "", func(v string) { vs.addName = v }),
		lineInput("vsrc-add-address", T("vsrc.addAddress", "Address of its catalogue (https://…/voices.json)"), vs.addAddress, "https://", func(v string) { vs.addAddress = strings.TrimSpace(v) }),
		btn("vsrc-add-go", T("vsrc.addGo", "Add the repository"), func() {
			if err := piper.CheckAddress(vs.addAddress); err != nil {
				piperUI.msg = T("vsrc.badAddress", "That is not the address of a catalogue: it is https://host/path/voices.json, with no spaces.")
			} else if len(vs.repos) >= 8 {
				piperUI.msg = T("vsrc.tooMany", "That is as many repositories as can be kept; remove one first.")
			} else {
				name := strings.TrimSpace(vs.addName)
				if name == "" {
					name = hostOf(vs.addAddress)
				}
				id := fmt.Sprintf("repo%d", len(vs.repos)+1)
				for _, r := range vs.repos {
					if r.ID == id {
						id += "x"
					}
				}
				vs.repos = append(vs.repos, &voiceRepo{ID: id, Name: name, Address: vs.addAddress})
				saveVoiceRepos()
				vs.addName, vs.addAddress, vs.selected, piperUI.msg = "", "", id, ""
			}
			renderUI()
		}, false),
	}
	return nodes
}

// repoSection is one repository the person added: its host, a button to show its voices, and, once shown, the voices
// with their model cards.
func repoSection(u *ui, e interface {
	read.Engine
	read.Downloader
}, r *voiceRepo, lang string, renderUI func()) []xango.Node {
	vs := &voiceSources
	nodes := []xango.Node{
		para("vsrc-repo-about", fmt.Sprintf(T("vsrc.repoAbout", "A repository you added, on %s. Its voices have its publisher's terms, which the project has not read."), hostOf(r.Address)), false),
		btn("vsrc-repo-remove", T("vsrc.repoRemove", "Forget this repository"), func() {
			for i, x := range vs.repos {
				if x == r {
					vs.repos = append(vs.repos[:i], vs.repos[i+1:]...)
					break
				}
			}
			saveVoiceRepos()
			vs.selected = "hub"
			renderUI()
		}, false),
	}
	list := piperUI.engine.SourceVoices(r.ID, "*")
	if len(list) == 0 || !r.loaded {
		nodes = append(nodes, btn("vsrc-repo-load", fmt.Sprintf(T("vsrc.repoLoad", "Show its voices (asks %s)"), hostOf(r.Address)), func() {
			vs.loading, r.err = r.ID, ""
			renderUI()
			(jsFetcher{}).Fetch(context.Background(), r.Address, nil, func(data []byte, err error) {
				vs.loading = ""
				if err == nil {
					var voices []piper.CatalogueVoice
					if voices, err = piper.ParseRepository(r.Address, r.ID, data); err == nil {
						piperUI.engine.AddVoices(voices)
						r.loaded = true
					}
				}
				if err != nil {
					r.err = err.Error()
				}
				renderUI()
			})
		}, vs.loading != ""))
		if r.err != "" {
			nodes = append(nodes, para("vsrc-repo-err", fmt.Sprintf(T("voices.catErr", "The list could not be fetched: %s"), r.err), true))
		}
		return nodes
	}
	return append(nodes, listVoices(u, e, list, true, renderUI)...)
}

// ownFilesForm keeps a voice the person brings: the model and its configuration from their own device.
func ownFilesForm(u *ui, renderUI func()) []xango.Node {
	vs := &voiceSources
	pickFile := func(model bool) func() {
		return func() {
			accept := ".json"
			if model {
				accept = ".onnx"
			}
			transfer.PickFile(accept, func(name string, data []byte, err error) {
				if err != nil {
					piperUI.msg = err.Error()
				} else if model {
					vs.ownModel, vs.ownModelData = name, data
				} else {
					vs.ownConfig, vs.ownConfigData = name, data
				}
				if vs.ownName == "" && err == nil {
					vs.ownName = strings.TrimSuffix(strings.TrimSuffix(name, ".json"), ".onnx")
				}
				renderUI()
			})
		}
	}
	chosen := func(name string) string {
		if name == "" {
			return T("vsrc.noFile", "none chosen")
		}
		return name
	}
	return []xango.Node{
		para("vsrc-files-about", T("vsrc.filesAbout", "Bring a Piper voice you have: its .onnx model and its .onnx.json configuration. They are kept in this browser and never sent anywhere. The terms of the voice are yours to know: the project does not check them."), false),
		btn("vsrc-files-model", T("vsrc.chooseModel", "Choose the model (.onnx)"), pickFile(true), false),
		xango.El("span", []xango.Attr{xango.A("id", "vsrc-files-model-name"), xango.A("style", "margin-left:6px")}, xango.Text(chosen(vs.ownModel))),
		xango.El("br", nil),
		btn("vsrc-files-config", T("vsrc.chooseConfig", "Choose the configuration (.onnx.json)"), pickFile(false), false),
		xango.El("span", []xango.Attr{xango.A("id", "vsrc-files-config-name"), xango.A("style", "margin-left:6px")}, xango.Text(chosen(vs.ownConfig))),
		lineInput("vsrc-files-name", T("vsrc.ownName", "A name for the voice"), vs.ownName, "", func(v string) { vs.ownName = v }),
		xango.El("div", []xango.Attr{xango.A("id", "vsrc-files-confirm")}, components.Checkbox(components.CheckboxProps{
			Label: T("vsrc.ownConfirm", "I made this voice, or I may use it as I mean to"), Checked: vs.ownConfirmed,
			OnChange: func(on bool) { vs.ownConfirmed = on; renderUI() }})),
		btn("vsrc-files-go", T("vsrc.ownGo", "Keep the voice"), func() {
			piperUI.engine.InstallFiles(context.Background(), vs.ownName, vs.ownConfigData, vs.ownModelData, func(id string, err error) {
				if err != nil {
					piperUI.msg = T("vsrc.ownBad", "Those files are not a Piper voice: choose the .onnx model and its .onnx.json configuration.")
				} else {
					vs.ownModel, vs.ownConfig, vs.ownName, vs.ownModelData, vs.ownConfigData, vs.ownConfirmed = "", "", "", nil, nil, false
					piperUI.msg = fmt.Sprintf(T("vsrc.ownDone", "%s is kept and can be used."), id)
					u.announce(piperUI.msg)
				}
				renderUI()
			})
		}, len(vs.ownModelData) == 0 || len(vs.ownConfigData) == 0 || !vs.ownConfirmed),
	}
}

// deviceVoices says what the operating system and the browser offer: the "Voices on this device" reader.
func deviceVoices() []xango.Node {
	nodes := []xango.Node{para("vsrc-device-about", T("vsrc.deviceAbout", "The operating system and the browser have voices of their own. Choose \"Voices on this device\" above to read with them; add more in the operating system's settings. Some of them send the text to a speech service; the page uses those only when you allow voices that use the internet."), false)}
	var items []xango.Node
	for i, v := range reader.device.Voices() {
		where := T("vsrc.local", "on this device")
		if !v.Local {
			where = T("vsrc.cloud", "uses the internet")
		}
		items = append(items, xango.El("li", []xango.Attr{xango.A("id", fmt.Sprintf("vsrc-device-%d", i))}, xango.Text(fmt.Sprintf("%s (%s, %s)", read.Label(v), v.Lang, where))))
	}
	if len(items) == 0 {
		return append(nodes, para("vsrc-device-none", T("vsrc.deviceNone", "This browser offers no voices."), false))
	}
	return append(nodes, xango.El("ul", []xango.Attr{xango.A("id", "vsrc-device-list"), xango.A("class", "gj-doc-list")}, items...))
}
