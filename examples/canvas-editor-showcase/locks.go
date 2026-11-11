//go:build js && wasm

package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/elaous/gojiberry/canvas"
)

const holdFor = 8 * time.Second

func imageKey(i int) string    { return "image:" + strconv.Itoa(i) }
func equationKey(i int) string { return "equation:" + strconv.Itoa(i) }
func tableKey(i int) string    { return "table:" + strconv.Itoa(i) }

func fieldKey(name string) string {
	return "field:" + name
}

func objectKeys(fl *canvas.TextFlow) map[string]canvas.InlineObject {
	out := map[string]canvas.InlineObject{}
	if fl == nil {
		return out
	}
	for i, r := range documentImages(fl) {
		out[imageKey(i)] = r.img
	}
	for i, r := range documentEquations(fl) {
		out[equationKey(i)] = r.eq
	}
	tableIndex := 0
	for _, it := range fl.Items {
		in, ok := it.(canvas.Inline)
		if !ok {
			continue
		}
		switch o := in.Object.(type) {
		case *canvas.FormFieldObject:
			out[fieldKey(o.Name)] = o
		case *canvas.TableObject:
			out[tableKey(tableIndex)] = o
			tableIndex++
			for _, f := range o.View.Table.FieldList() {
				out[fieldKey(f.Name)] = o
			}
		}
	}
	return out
}

func (u *ui) claim(key, what string, renderUI func()) bool {
	if name, _, busy := u.busyBy(key); busy {
		u.announce(T("lock.busy", "Busy: %s is changing this %s.", name, what))
		renderUI() // what was typed is put back
		return false
	}
	if u.hold.key != key {
		u.hold.key = key
		u.refreshBusy()
	}
	u.hold.until = time.Now().Add(holdFor)
	return true
}

func (u *ui) holding() string {
	if u.hold.key != "" && time.Now().After(u.hold.until) {
		u.hold.key = ""
		u.refreshBusy()
	}
	return u.hold.key
}

func (u *ui) busyBy(key string) (name, color string, busy bool) {
	if u.live == nil || key == "" {
		return "", "", false
	}
	for _, p := range u.live.Peers {
		if p.Holding == key && !p.Gone {
			n := p.Name
			if n == "" {
				n = p.User
			}
			if n == "" {
				n = T("lock.someone", "someone")
			}
			if u.hold.key == key && strings.Compare(u.live.Replica, p.Replica) < 0 {
				continue
			}
			return n, p.Color, true
		}
	}
	return "", "", false
}

func (u *ui) contend() {
	if u.hold.key == "" {
		return
	}
	if name, _, busy := u.busyBy(u.hold.key); busy {
		u.announce(T("lock.lost", "Busy: %s got to it first.", name))
		u.hold.key = ""
	}
}

func (u *ui) refreshBusy() {
	if u.flowNow == nil {
		return
	}
	fl := u.flowNow()
	if fl == nil {
		return
	}
	marks := map[canvas.InlineObject]canvas.Busy{}
	if u.live != nil {
		keys := objectKeys(fl)
		for _, p := range u.live.Peers {
			if o, ok := keys[p.Holding]; ok && p.Holding != "" && !p.Gone {
				if _, taken := marks[o]; !taken {
					n := p.Name
					if n == "" {
						n = p.User
					}
					marks[o] = canvas.Busy{Name: n, Color: p.Color}
				}
			}
		}
		if o, ok := keys[u.hold.key]; ok && u.hold.key != "" {
			if _, taken := marks[o]; !taken {
				marks[o] = canvas.Busy{Name: u.liveName, Color: u.liveColor, Mine: true}
			}
		}
	}
	if fmt.Sprint(marks) != fmt.Sprint(fl.ObjectBusy) {
		fl.ObjectBusy = marks
		if u.redrawCanvas != nil {
			u.redrawCanvas()
		}
	}
}

func (u *ui) tableKeyOf(fl *canvas.TextFlow, tv *canvas.TableView) string {
	if fl == nil {
		return ""
	}
	for i, t := range fl.Tables() {
		if t.View == tv {
			return tableKey(i)
		}
	}
	return ""
}
