package main

import (
	"encoding/json"
	"errors"

	"github.com/elaous/gojiberry/components"
)

// prefs are the display settings; an empty field means "as the system has it".
type prefs struct {
	Motion   string `json:"motion,omitempty"`   // "reduce"
	Contrast string `json:"contrast,omitempty"` // "more"
	Text     string `json:"text,omitempty"`     // "large", "larger", "largest"
	Spacing  string `json:"spacing,omitempty"`  // "wide"
	Focus    string `json:"focus,omitempty"`    // "on": distraction-free
	Targets  string `json:"targets,omitempty"`  // "large"
	Suggest  string `json:"suggest,omitempty"`  // word suggestions: "auto" opens the list while typing
	Ghost    string `json:"ghost,omitempty"`    // "on": the best word is shown faintly after the caret, Tab accepts it
	Speed    string `json:"speed,omitempty"`    // reading aloud: "slow", "fast"
	Typing   string `json:"typing,omitempty"`   // what is said of typed text: "words", "characters"; "" nothing
	TypeFmt  string `json:"typefmt,omitempty"`  // "on": formatting changes at the caret are said
	TypeRead string `json:"typeread,omitempty"` // "on": the line is read when the caret moves
	Collab   string `json:"collab,omitempty"`   // what is announced of a shared session: "" everything, "important", "off"
	Memory   string `json:"memory,omitempty"`   // "low": fewer undo steps kept; "high": many; "normal": the person turned it off; "" automatic (low on a device that reports 2 GB or less)
	Where    string `json:"where,omitempty"`    // "on": the presence list has a "Where is <name>?" button for each collaborator
}

// sanitize drops every value that is not one the page knows: unknown or unreadable ones are as if unset.
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
	if p.Where != "on" {
		p.Where = ""
	}
	if p.Memory != "low" && p.Memory != "normal" && p.Memory != "high" {
		p.Memory = ""
	}
	return p
}

// maxPrefsFile is the largest preferences file that is read.
const maxPrefsFile = 16 << 10

// parsePrefs reads preferences from JSON (a kept copy, or a file the person brings): values the page does
// not know are dropped, so a file from another version or an edited one cannot set anything odd.
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

// encodePrefs is the preferences as a file: JSON, one setting to a line.
func encodePrefs(p prefs) []byte {
	b, _ := json.MarshalIndent(p.sanitize(), "", "  ")
	return append(b, '\n')
}
