//go:build js && wasm

package main

import (
	"fmt"
	"strings"
	"syscall/js"

	"github.com/elaous/gojiberry/addons"
	"github.com/elaous/gojiberry/domrt"
	"github.com/elaous/gojiberry/xango"
)

func addon(name string) js.Value {
	all := js.Global().Get("addons")
	if !all.Truthy() {
		return js.Undefined()
	}
	return all.Get(name)
}

func hasAddon(name, method string) bool {
	a := addon(name)
	return a.Truthy() && a.Get(method).Type() == js.TypeFunction
}

var addonsState struct {
	models  map[string][]addonsModel // by add-on name, once asked
	loading map[string]bool
	errs    map[string]string
	busy    string // "name/id" being installed
	done    int64
	total   int64
	cancel  func()
	msg     string
}

type addonsModel = addons.Model

func addonModels(name string, done func([]addons.Model, error)) {
	go func() {
		a := addon(name)
		if !a.Truthy() || a.Get("models").Type() != js.TypeFunction {
			done(nil, addons.ErrNoAddon)
			return
		}
		res, err := domrt.Await(a.Call("models"))
		if err != nil {
			done(nil, err)
			return
		}
		ms, err := addons.ParseModels([]byte(js.Global().Get("JSON").Call("stringify", res).String()))
		done(ms, err)
	}()
}

func addonInstall(name, id string, progress func(done, total int64), done func(error)) {
	go func() {
		a := addon(name)
		if !a.Truthy() || a.Get("install").Type() != js.TypeFunction {
			done(addons.ErrNoAddon)
			return
		}
		on := js.FuncOf(func(_ js.Value, args []js.Value) any {
			if len(args) >= 2 && progress != nil {
				progress(int64(args[0].Float()), int64(args[1].Float()))
			}
			return nil
		})
		defer on.Release()
		_, err := domrt.Await(a.Call("install", id, on))
		done(err)
	}()
}

func addonRemove(name, id string, done func(error)) {
	go func() {
		a := addon(name)
		if !a.Truthy() || a.Get("remove").Type() != js.TypeFunction {
			done(addons.ErrNoAddon)
			return
		}
		_, err := domrt.Await(a.Call("remove", id))
		done(err)
	}()
}

func installedModel(name, lang string) string {
	base := strings.ToLower(strings.SplitN(lang, "-", 2)[0])
	for _, m := range addonsState.models[name] {
		if !m.Installed {
			continue
		}
		if mb := strings.ToLower(strings.SplitN(m.Lang, "-", 2)[0]); m.Lang == "" || base == "" || mb == base {
			return m.ID
		}
	}
	return ""
}

func addonLabel(name string) string {
	switch name {
	case "piper":
		return T("addons.piper", "Reader voices (piper)")
	case "whisper":
		return T("addons.whisper", "Dictation (whisper)")
	case "describe":
		return T("addons.describe", "Picture descriptions (describe)")
	case "ocr":
		return T("addons.ocr", "Text from scans (ocr)")
	case "translate":
		return T("addons.translate", "Translation (translate)")
	case "llm":
		return T("addons.llm", "Writing help (llm)")
	case "diagram":
		return T("addons.diagram", "Diagrams (diagram)")
	case "spell":
		return T("addons.spell", "Offline spelling (spell)")
	}
	return name
}

func addonsPanel(u *ui, renderUI func()) xango.Node {
	if addonsState.models == nil {
		addonsState.models, addonsState.loading, addonsState.errs = map[string][]addons.Model{}, map[string]bool{}, map[string]string{}
	}
	nodes := []xango.Node{
		xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(T("addons.title", "Add-ons"))),
		para("addons-about", T("addons.about", "Add-ons are optional scripts that the person who runs this page adds, for things that need a large program or model: speech, vision, translation. Nothing is downloaded until you ask, and they run on this device."), false),
	}
	if addonsState.msg != "" {
		nodes = append(nodes, xango.El("p", []xango.Attr{xango.A("id", "addons-message"), xango.A("role", "status")}, xango.Text(addonsState.msg)))
	}
	var items []xango.Node
	for _, s := range addons.Known {
		s := s
		present := hasAddon(s.Name, s.Method)
		kids := []xango.Node{xango.El("strong", nil, xango.Text(addonLabel(s.Name)))}
		if !present {
			kids = append(kids, para("addons-absent-"+s.Name, T("addons.absent", "Not on this page."), false))
			items = append(items, xango.El("li", []xango.Attr{xango.A("class", "gj-doc-item"), xango.A("id", "addons-item-"+s.Name)}, kids...))
			continue
		}
		kids = append(kids, para("addons-present-"+s.Name, T("addons.present", "On this page."), false))
		if s.Name == "piper" {
			kids = append(kids, btn("addons-open-piper", T("addons.openVoices", "Reader voices…"), func() { u.tab = "voices"; renderUI() }, false))
		}
		if s.Models && a(s.Name).Get("models").Type() == js.TypeFunction {
			ms, loaded := addonsState.models[s.Name]
			if !loaded {
				kids = append(kids, btn("addons-models-"+s.Name, T("addons.showModels", "Show the models"), func() {
					addonsState.loading[s.Name], addonsState.errs[s.Name] = true, ""
					renderUI()
					addonModels(s.Name, func(list []addons.Model, err error) {
						addonsState.loading[s.Name] = false
						if err != nil {
							addonsState.errs[s.Name] = err.Error()
						} else {
							addonsState.models[s.Name] = append([]addons.Model{}, list...)
							if list == nil {
								addonsState.models[s.Name] = []addons.Model{}
							}
						}
						renderUI()
					})
				}, addonsState.loading[s.Name]))
				if e := addonsState.errs[s.Name]; e != "" {
					kids = append(kids, para("addons-err-"+s.Name, fmt.Sprintf(T("addons.modelsErr", "The models could not be listed: %s"), e), true))
				}
			} else if len(ms) == 0 {
				kids = append(kids, para("addons-nomodels-"+s.Name, T("addons.noModels", "It offers no models."), false))
			}
			var rows []xango.Node
			for i, m := range ms {
				i, m := i, m
				key := s.Name + "/" + m.ID
				meta := strings.TrimSpace(strings.Join(nonEmpty(m.Lang, addons.Size(m.Size)), ", "))
				row := []xango.Node{xango.El("span", nil, xango.Text(m.Label)), xango.El("span", []xango.Attr{xango.A("style", "margin-left:6px;color:#6b7280")}, xango.Text(meta))}
				if link, ok := m.LicenceLink(); ok {
					row = append(row, xango.El("a", []xango.Attr{xango.A("href", link), xango.A("target", "_blank"), xango.A("rel", "noopener noreferrer"), xango.A("style", "margin-left:6px")}, xango.Text(T("addons.licence", "Terms"))))
				} else if m.Licence != "" {
					row = append(row, xango.El("span", []xango.Attr{xango.A("style", "margin-left:6px")}, xango.Text(m.Licence)))
				}
				id := fmt.Sprintf("addons-%s-%d", s.Name, i)
				switch {
				case addonsState.busy == key:
					pct := 0
					if addonsState.total > 0 {
						pct = int(addonsState.done * 100 / addonsState.total)
					}
					row = append(row, xango.El("span", []xango.Attr{xango.A("id", id+"-progress"), xango.A("role", "status"), xango.A("style", "margin-left:6px")}, xango.Text(fmt.Sprintf(T("addons.progress", "Downloading… %d%%"), pct))))
				case m.Installed:
					row = append(row, btn(id+"-remove", T("addons.remove", "Remove"), func() {
						addonRemove(s.Name, m.ID, func(err error) {
							if err != nil {
								addonsState.msg = fmt.Sprintf(T("addons.removeErr", "It could not be removed: %s"), err.Error())
							} else {
								for j := range addonsState.models[s.Name] {
									if addonsState.models[s.Name][j].ID == m.ID {
										addonsState.models[s.Name][j].Installed = false
									}
								}
								addonsState.msg = fmt.Sprintf(T("addons.removed", "%s was removed."), m.Label)
							}
							renderUI()
						})
					}, false))
				default:
					row = append(row, btn(id+"-install", fmt.Sprintf(T("addons.install", "Download %s"), addons.Size(m.Size)), func() {
						addonsState.busy, addonsState.done, addonsState.total, addonsState.msg = key, 0, m.Size, ""
						renderUI()
						addonInstall(s.Name, m.ID, func(d, t int64) { addonsState.done, addonsState.total = d, t; renderUI() }, func(err error) {
							addonsState.busy = ""
							if err != nil {
								addonsState.msg = fmt.Sprintf(T("addons.installErr", "It could not be downloaded: %s"), err.Error())
							} else {
								for j := range addonsState.models[s.Name] {
									if addonsState.models[s.Name][j].ID == m.ID {
										addonsState.models[s.Name][j].Installed = true
									}
								}
								addonsState.msg = fmt.Sprintf(T("addons.installed", "%s is downloaded and can be used."), m.Label)
								u.announce(addonsState.msg)
							}
							renderUI()
						})
					}, addonsState.busy != ""))
				}
				rows = append(rows, xango.El("li", []xango.Attr{xango.A("id", id)}, row...))
			}
			if len(rows) > 0 {
				kids = append(kids, xango.El("ul", []xango.Attr{xango.A("id", "addons-models-list-"+s.Name)}, rows...))
			}
		}
		items = append(items, xango.El("li", []xango.Attr{xango.A("class", "gj-doc-item"), xango.A("id", "addons-item-"+s.Name)}, kids...))
	}
	nodes = append(nodes, xango.El("ul", []xango.Attr{xango.A("class", "gj-doc-list"), xango.A("id", "addons-list")}, items...))
	return xango.El("div", []xango.Attr{xango.A("id", "addons-panel")}, nodes...)
}

func a(name string) js.Value { return addon(name) }

func nonEmpty(in ...string) []string {
	var out []string
	for _, s := range in {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
