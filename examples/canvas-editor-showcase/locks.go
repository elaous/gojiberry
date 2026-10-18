//go:build js && wasm

package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/elaous/gojiberry/canvas"
)

// Objects and shared editing: when one person is changing a picture, an equation or a form field, the others see it busy
// and wait; the text itself merges. The hold goes to the others with the cursor (wire.Presence.Holding) and is renewed
// while the person works, so it ends by itself a few seconds after they stop (or leave). It is advice to the page, not a
// lock the server keeps: the document merges whatever it says, and nothing here is a way to keep somebody out. Each person
// has one colour (the `color` of the cursor): the object they hold has an outline in it, and a busy object a badge in it.

// holdFor is how long a hold lasts without another sign of work.
const holdFor = 8 * time.Second

// lockKeys: the keys of the objects (what Presence.Holding says): "image:2", "equation:0", "field:name".
func imageKey(i int) string    { return "image:" + strconv.Itoa(i) }
func equationKey(i int) string { return "equation:" + strconv.Itoa(i) }
func tableKey(i int) string    { return "table:" + strconv.Itoa(i) }

func fieldKey(name string) string {
	return "field:" + name
}

// objectKeys maps every keyed object of the document to the object that shows it: a picture, an equation, a form field
// in the text, or, for a field in a table cell, the table.
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

// claim says this person is working on the object (or still is). It is false, and the person is told, when somebody else
// has it.
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

// holding is the object this person holds, "" when they hold none or the hold ran out.
func (u *ui) holding() string {
	if u.hold.key != "" && time.Now().After(u.hold.until) {
		u.hold.key = ""
		u.refreshBusy()
	}
	return u.hold.key
}

// busyBy says whether somebody else holds the object, and who. Two people who claim one object at the same moment both see
// the other's claim: the one whose replica id sorts first keeps it, the other lets go (see contend).
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
				continue // both claimed it: this replica's id sorts first, so this one keeps it
			}
			return n, p.Color, true
		}
	}
	return "", "", false
}

// contend lets go of an object another person holds with the better claim.
func (u *ui) contend() {
	if u.hold.key == "" {
		return
	}
	if name, _, busy := u.busyBy(u.hold.key); busy {
		u.announce(T("lock.lost", "Busy: %s got to it first.", name))
		u.hold.key = ""
	}
}

// refreshBusy puts the marks of who holds what on the objects of the document (canvas.TextFlow.ObjectBusy).
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

// tableKeyOf is the key of the table in the text that shows tv ("" for a table that is not in the text, which has no
// hold: nobody else is shown it).
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
