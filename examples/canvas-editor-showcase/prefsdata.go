package main

import (
	"encoding/json"
	"errors"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/components"
)

type prefs struct {
	Motion      string `json:"motion,omitempty"`   // "reduce"
	Contrast    string `json:"contrast,omitempty"` // "more"
	Text        string `json:"text,omitempty"`     // "large", "larger", "largest"
	Spacing     string `json:"spacing,omitempty"`  // "wide"
	Focus       string `json:"focus,omitempty"`    // "on": distraction-free
	Targets     string `json:"targets,omitempty"`  // "large"
	Suggest     string `json:"suggest,omitempty"`  // word suggestions: "auto" opens the list while typing
	Ghost       string `json:"ghost,omitempty"`
	Speed       string `json:"speed,omitempty"`    // reading aloud: "slow", "fast"
	Typing      string `json:"typing,omitempty"`   // what is said of typed text: "words", "characters"; "" nothing
	TypeFmt     string `json:"typefmt,omitempty"`  // "on": formatting changes at the caret are said
	TypeRead    string `json:"typeread,omitempty"` // "on": the line is read when the caret moves
	Collab      string `json:"collab,omitempty"`
	Memory      string `json:"memory,omitempty"`
	Lang        string `json:"lang,omitempty"` // the interface language chosen by the person ("" the browser's)
	Theme       string `json:"theme,omitempty"`
	Typewriter  string `json:"typewriter,omitempty"`
	TypewriterX string `json:"typewriterx,omitempty"`
	Typed       string `json:"typed,omitempty"`
	Close       string `json:"close,omitempty"`
	Colours     string `json:"colours,omitempty"`
	Dwell       string `json:"dwell,omitempty"` // click by resting the pointer: "fast", "medium", "slow" (the pause)
	Scan        string `json:"scan,omitempty"`  // single-switch scanning: "fast", "medium", "slow" (the step)
	Hint        string `json:"hint,omitempty"`
	Voice       string `json:"voice,omitempty"`
	Reader      string `json:"reader,omitempty"`
	HVoice      string `json:"hvoice,omitempty"`
	PVoice      string `json:"pvoice,omitempty"`
	Voices      string `json:"voices,omitempty"`
	Where       string `json:"where,omitempty"`
}

func (p prefs) sanitize() prefs {
	if p.Motion != "reduce" {
		p.Motion = ""
	}
	if p.Contrast != "more" {
		p.Contrast = ""
	}
	if p.Text != "large" && p.Text != "larger" && p.Text != "largest" {
		p.Text = ""
	}
	if p.Spacing != "wide" {
		p.Spacing = ""
	}
	if p.Typewriter != "on" {
		p.Typewriter = ""
	}
	if p.TypewriterX != "on" {
		p.TypewriterX = ""
	}
	if p.Focus != "on" {
		p.Focus = ""
	}
	if p.Targets != "large" {
		p.Targets = ""
	}
	if p.Suggest != "auto" {
		p.Suggest = ""
	}
	if p.Ghost != "on" {
		p.Ghost = ""
	}
	if p.Voices != "net" {
		p.Voices = ""
	}
	if p.Hint != "on" {
		p.Hint = ""
	}
	if p.Reader != "piper" && p.Reader != "hosted" {
		p.Reader = ""
	}
	if len(p.HVoice) > 120 {
		p.HVoice = ""
	}
	if len(p.PVoice) > 120 {
		p.PVoice = ""
	}
	if len(p.Voice) > 120 {
		p.Voice = ""
	}
	if p.Speed != "slow" && p.Speed != "fast" {
		p.Speed = ""
	}
	if p.Typing != components.TypingWords && p.Typing != components.TypingCharacters {
		p.Typing = ""
	}
	if p.TypeFmt != "on" {
		p.TypeFmt = ""
	}
	if p.TypeRead != "on" {
		p.TypeRead = ""
	}
	if p.Collab != components.CollabImportant && p.Collab != components.CollabOff {
		p.Collab = ""
	}
	if p.Theme != "dark" {
		p.Theme = ""
	}
	if p.Colours != "safe" {
		p.Colours = ""
	}
	if p.Close != "off" {
		p.Close = ""
	}
	if p.Typed != "off" {
		p.Typed = ""
	}
	if !validLang(p.Lang) {
		p.Lang = ""
	}
	if p.Dwell != "fast" && p.Dwell != "medium" && p.Dwell != "slow" {
		p.Dwell = ""
	}
	if p.Scan != "fast" && p.Scan != "medium" && p.Scan != "slow" {
		p.Scan = ""
	}
	if p.Where != "on" {
		p.Where = ""
	}
	if p.Memory != "low" && p.Memory != "normal" && p.Memory != "high" {
		p.Memory = ""
	}
	return p
}

func validLang(s string) bool {
	if s == "" || len(s) > 12 {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9' && i > 0, r == '-' && i > 0 && i < len(s)-1:
		default:
			return false
		}
	}
	return true
}

const maxPrefsFile = 16 << 10

func parsePrefs(data []byte) (prefs, error) {
	if len(data) > maxPrefsFile {
		return prefs{}, errors.New("preferences: the file is too large")
	}
	var p prefs
	if err := json.Unmarshal(data, &p); err != nil {
		return prefs{}, errors.New("preferences: not a preferences file")
	}
	return p.sanitize(), nil
}

func encodePrefs(p prefs) []byte {
	b, _ := json.MarshalIndent(p.sanitize(), "", "  ")
	return append(b, '\n')
}

func (p prefs) textScale() float64 {
	switch p.Text {
	case "large":
		return 1.25
	case "larger":
		return 1.5
	case "largest":
		return 2
	}
	return 1
}

func (p prefs) peerPalette() []string {
	if p.Colours == "safe" {
		return canvas.ColourBlindPalette
	}
	return nil
}
