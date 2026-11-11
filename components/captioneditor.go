package components

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/xango"
)

type CaptionDraft struct {
	Start, End, Text string
}

// CaptionEditorProps configures CaptionEditor.
type CaptionEditorProps struct {
	ID      string
	Track   string
	OnTrack func(string)
	Draft   CaptionDraft
	OnDraft func(CaptionDraft)
	OnAdd   func()
	// Message is what the last "Add" said ("" for nothing).
	Message  string
	Disabled bool
}

func captionProblem(p docfile.CueProblem) string {
	switch p.Kind {
	case "no-header":
		return tr("cap.p.noHeader", "The track does not start with the line WEBVTT.")
	case "no-cues":
		return tr("cap.p.noCues", "The track has no captions yet.")
	case "backwards":
		return trf("cap.p.backwards", "Caption %d ends before it starts.", p.Cue)
	case "overlap":
		return trf("cap.p.overlap", "Caption %d starts before the caption before it has ended.", p.Cue)
	}
	return trf("cap.p.noText", "Caption %d has no text or no readable times.", p.Cue)
}

func ParseCaptionTime(s string) (float64, bool) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", "."))
	if s == "" {
		return 0, false
	}
	if !strings.Contains(s, ":") {
		v, err := strconv.ParseFloat(s, 64)
		return v, err == nil && v >= 0
	}
	cues, _ := docfile.ParseCues("WEBVTT\n\n" + s + " --> 99:59:59.000\nx\n")
	if len(cues) != 1 {
		return 0, false
	}
	return cues[0].Start, true
}

func CaptionEditor(p CaptionEditorProps) xango.Node {
	cues, problems := docfile.ParseCues(p.Track)
	if strings.TrimSpace(p.Track) == "" {
		problems = nil // nothing written yet is not a fault of the track
	}
	field := func(suffix, label, value string, on func(string)) xango.Node {
		attrs := []xango.Attr{xango.A("id", p.ID+"-"+suffix), xango.A("type", "text"), xango.A("value", value), xango.A("aria-label", label),
			xango.A("placeholder", label), xango.A("spellcheck", "false")}
		if p.Disabled {
			attrs = append(attrs, xango.A("disabled", "disabled"))
		}
		if on != nil {
			attrs = append(attrs, xango.On("input", func(e xango.Event) { on(xango.EventValue[string](e)) }))
		}
		return xango.El("input", attrs)
	}
	track := []xango.Attr{xango.A("id", p.ID+"-track"), xango.A("aria-label", tr("cap.trackLabel", "Caption track (WebVTT)")), xango.A("rows", "6"),
		xango.A("spellcheck", "false"), xango.A("class", "gj-captions-track"), xango.A("style", "width:100%;box-sizing:border-box;font-family:monospace;font-size:12px")}
	if p.Disabled {
		track = append(track, xango.A("disabled", "disabled"))
	}
	if p.OnTrack != nil {
		track = append(track, xango.On("input", func(e xango.Event) { p.OnTrack(xango.EventValue[string](e)) }))
	}
	nodes := []xango.Node{
		xango.El("label", []xango.Attr{xango.A("for", p.ID+"-track")}, xango.Text(tr("cap.trackLabel", "Caption track (WebVTT)"))),
		xango.El("textarea", track, xango.Text(p.Track)),
	}
	if len(problems) > 0 {
		var items []xango.Node
		for _, pr := range problems {
			items = append(items, xango.El("li", nil, xango.Text(captionProblem(pr))))
		}
		nodes = append(nodes, xango.El("ul", []xango.Attr{xango.A("id", p.ID+"-problems"), xango.A("class", "gj-captions-problems"), xango.A("role", "status")}, items...))
	}
	if len(cues) > 0 {
		var rows []xango.Node
		for i, c := range cues {
			rows = append(rows, xango.El("tr", nil,
				xango.El("td", nil, xango.Text(strconv.Itoa(i+1))),
				xango.El("td", nil, xango.Text(docfile.FormatCueTime(c.Start))),
				xango.El("td", nil, xango.Text(docfile.FormatCueTime(c.End))),
				xango.El("td", nil, xango.Text(strings.ReplaceAll(c.Text, "\n", " ")))))
		}
		nodes = append(nodes, xango.El("table", []xango.Attr{xango.A("id", p.ID+"-cues"), xango.A("class", "gj-captions-cues")},
			xango.El("caption", []xango.Attr{xango.A("class", "gj-sr-only")}, xango.Text(trf("cap.count", "%d captions", len(cues)))),
			xango.El("thead", nil, xango.El("tr", nil,
				xango.El("th", []xango.Attr{xango.A("scope", "col")}, xango.Text("#")),
				xango.El("th", []xango.Attr{xango.A("scope", "col")}, xango.Text(tr("cap.start", "Start"))),
				xango.El("th", []xango.Attr{xango.A("scope", "col")}, xango.Text(tr("cap.end", "End"))),
				xango.El("th", []xango.Attr{xango.A("scope", "col")}, xango.Text(tr("cap.text", "Caption"))))),
			xango.El("tbody", nil, rows...)))
	}
	cur := p.Draft
	set := func(f func(*CaptionDraft, string)) func(string) {
		return func(v string) {
			f(&cur, v)
			if p.OnDraft != nil {
				p.OnDraft(cur)
			}
		}
	}
	add := []xango.Attr{xango.A("id", p.ID+"-add"), xango.A("type", "button")}
	if p.Disabled {
		add = append(add, xango.A("disabled", "disabled"))
	} else if p.OnAdd != nil {
		add = append(add, xango.On("click", func(xango.Event) { p.OnAdd() }))
	}
	nodes = append(nodes, xango.El("div", []xango.Attr{xango.A("class", "gj-captions-add"), xango.A("role", "group"), xango.A("aria-label", tr("cap.addGroup", "Add a caption"))},
		field("start", tr("cap.startLabel", "Start (for example 0:05.5)"), p.Draft.Start, set(func(d *CaptionDraft, v string) { d.Start = v })),
		field("end", tr("cap.endLabel", "End (for example 0:08)"), p.Draft.End, set(func(d *CaptionDraft, v string) { d.End = v })),
		field("text", tr("cap.textLabel", "What is said"), p.Draft.Text, set(func(d *CaptionDraft, v string) { d.Text = v })),
		xango.El("button", add, xango.Text(tr("cap.add", "Add the caption")))))
	if p.Message != "" {
		nodes = append(nodes, xango.El("p", []xango.Attr{xango.A("id", p.ID+"-message"), xango.A("role", "status")}, xango.Text(p.Message)))
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-captions"), xango.A("id", p.ID)}, nodes...)
}

func AddCaption(track string, d CaptionDraft) (string, string) {
	start, ok1 := ParseCaptionTime(d.Start)
	end, ok2 := ParseCaptionTime(d.End)
	switch {
	case !ok1 || !ok2:
		return track, tr("cap.badTime", "Write the times as seconds (12.5) or minutes and seconds (0:12.5).")
	case end <= start:
		return track, tr("cap.badOrder", "The caption must end after it starts.")
	case strings.TrimSpace(d.Text) == "":
		return track, tr("cap.noText", "Write what is said.")
	}
	cues, _ := docfile.ParseCues(track)
	if n := len(cues); n > 0 && start < cues[n-1].End {
		return track, trf("cap.overlapAdd", "The caption starts before the last one ends (at %s).", docfile.FormatCueTime(cues[n-1].End))
	}
	cues = append(cues, docfile.Cue{Start: start, End: end, Text: strings.TrimSpace(d.Text)})
	return docfile.BuildCues(cues), fmt.Sprintf(tr("cap.added", "Caption %d added."), len(cues))
}
