package canvas

import "strings"

type MediaObject struct {
	// Kind is "audio" or "video" ("" is video).
	Kind string
	URL  string
	// Title names the clip.
	Title                             string
	Captions, CaptionsURL, Transcript string
	// Width and Height are the size of a video's picture.
	Width, Height float64
	Needs         bool
	w, h          float64
}

const mediaHeight = 52.0

// IsAudio says the clip is sound only.
func (m *MediaObject) IsAudio() bool { return strings.EqualFold(strings.TrimSpace(m.Kind), "audio") }

func (m *MediaObject) label() string {
	if t := strings.TrimSpace(m.Title); t != "" {
		return t
	}
	if u := strings.TrimSpace(m.URL); u != "" {
		return u
	}
	return "(clip with no name)"
}

func (m *MediaObject) Measure(p Painter, maxW, _ float64) (float64, float64) {
	m.w, m.h = maxW, mediaHeight
	if m.w <= 0 {
		p.SetFont("15px sans-serif")
		m.w = p.MeasureTextWidth(m.label()) + 96
	}
	return m.w, m.h
}

func (m *MediaObject) DrawAt(p Painter, x, y float64) {
	p.SetFillStyle("#eef0f4")
	p.FillRect(x, y+2, m.w, m.h-4)
	p.SetFillStyle("#4b5563")
	p.FillRect(x, y+2, 4, m.h-4)
	p.SetTextAlign("left")
	p.SetTextBaseline("middle")
	p.SetFont("22px sans-serif")
	p.SetFillStyle("#1f2328")
	icon := "▶"
	if m.IsAudio() {
		icon = "♪"
	}
	p.FillText(icon, x+14, y+m.h/2)
	p.SetFont("bold 13px sans-serif")
	kind := "Video"
	if m.IsAudio() {
		kind = "Audio"
	}
	p.SetFillStyle("#4b5563")
	p.FillText(kind, x+48, y+m.h/2-9)
	p.SetFont("15px sans-serif")
	p.SetFillStyle("#1f2328")
	p.FillText(m.label(), x+48, y+m.h/2+9)
	if m.Needs {
		p.SetFillStyle("#b42318")
		p.FillRect(x+m.w-28, y+8, 20, 20)
		p.SetFillStyle("#ffffff")
		p.SetFont("bold 14px sans-serif")
		p.SetTextAlign("center")
		p.FillText("!", x+m.w-18, y+18)
		p.SetTextAlign("left")
	}
}
