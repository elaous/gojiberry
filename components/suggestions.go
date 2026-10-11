package components

import (
	"strconv"

	"github.com/elaous/gojiberry/xango"
)

// Suggestions: a list of words offered for the one being typed, as the WAI-ARIA combobox with a
// listbox popup. The list never takes the focus: the editing element keeps it and names the
// highlighted option through aria-activedescendant (see SuggestionsControl), so typing goes on.
//
// The keys, handled by SuggestState.Key: Down and Up move (and wrap), Home and End go to the ends,
// Tab or Enter accept the highlighted word, Escape closes, and the digits 1 to 9 accept the numbered
// row when Numbers is set.

// SuggestAction is what a key did to the list.
type SuggestAction int

const (
	SuggestNone    SuggestAction = iota // the key is not for the list; let it through
	SuggestMoved                        // the highlight moved
	SuggestAccept                       // accept the highlighted word (Index)
	SuggestDismiss                      // close the list
)

// SuggestState is the list and its highlight.
type SuggestState struct {
	Words   []string
	Active  int
	Open    bool
	Numbers bool // the digits 1 to 9 accept a row
}

// Show opens the list with words, the first highlighted; no words closes it.
func (s *SuggestState) Show(words []string) {
	s.Words, s.Active, s.Open = words, 0, len(words) > 0
}

// Close closes the list.
func (s *SuggestState) Close() { s.Open, s.Words, s.Active = false, nil, 0 }

// Key applies a key (as KeyboardEvent.key) and says what happened; for SuggestAccept the word is
// Words[Active] (or the numbered row, which Key moves the highlight to).
func (s *SuggestState) Key(key string) SuggestAction {
	if !s.Open || len(s.Words) == 0 {
		return SuggestNone
	}
	n := len(s.Words)
	switch key {
	case "ArrowDown":
		s.Active = (s.Active + 1) % n
		return SuggestMoved
	case "ArrowUp":
		s.Active = (s.Active + n - 1) % n
		return SuggestMoved
	case "Home":
		s.Active = 0
		return SuggestMoved
	case "End":
		s.Active = n - 1
		return SuggestMoved
	case "Tab", "Enter":
		return SuggestAccept
	case "Escape":
		return SuggestDismiss
	}
	if s.Numbers && len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
		if i := int(key[0] - '1'); i < n {
			s.Active = i
			return SuggestAccept
		}
	}
	return SuggestNone
}

// Selected is the highlighted word, "" when the list is closed.
func (s *SuggestState) Selected() string {
	if !s.Open || s.Active < 0 || s.Active >= len(s.Words) {
		return ""
	}
	return s.Words[s.Active]
}

// Announcement is what a polite live region says when the list opens or the highlight moves.
func (s *SuggestState) Announcement(moved bool) string {
	if !s.Open || len(s.Words) == 0 {
		return ""
	}
	w := s.Words[s.Active]
	pos := trf("suggest.position", "%s, %d of %d", w, s.Active+1, len(s.Words))
	if moved {
		return pos
	}
	return trn("suggest.count", "%d suggestion.", "%d suggestions.", len(s.Words)) + " " + pos
}

// SuggestionsControl is the attributes the editing element carries while it has the popup: role
// combobox, expanded, controlling the list, and naming the highlighted option.
func SuggestionsControl(id string, s SuggestState) []xango.Attr {
	attrs := []xango.Attr{
		xango.A("role", "combobox"),
		xango.A("aria-autocomplete", "list"),
		xango.A("aria-haspopup", "listbox"),
		xango.A("aria-controls", id),
		xango.A("aria-expanded", strconv.FormatBool(s.Open)),
	}
	if s.Open && len(s.Words) > 0 {
		attrs = append(attrs, xango.A("aria-activedescendant", suggestOptionID(id, s.Active)))
	}
	return attrs
}

func suggestOptionID(id string, i int) string { return id + "-opt-" + strconv.Itoa(i) }

// SuggestionsProps configures Suggestions.
type SuggestionsProps struct {
	// ID is the list's id (the editing element's aria-controls); "suggestions" when empty.
	ID string
	// Label names the list; "Suggestions" translated when empty.
	Label string
	State SuggestState
	// X and Y are the page position of the list's top left corner (fixed positioning), where the
	// caret is.
	X, Y float64
	// OnPick is called with the index of the row clicked or tapped.
	OnPick func(i int)
}

// Suggestions renders the popup list; nothing when the state is closed.
func Suggestions(p SuggestionsProps) xango.Node {
	if !p.State.Open || len(p.State.Words) == 0 {
		return xango.Frag()
	}
	id := p.ID
	if id == "" {
		id = "suggestions"
	}
	label := p.Label
	if label == "" {
		label = tr("suggest.label", "Suggestions")
	}
	var rows []xango.Node
	for i, w := range p.State.Words {
		i := i
		class := "gj-suggest-option"
		sel := "false"
		if i == p.State.Active {
			class += " gj-suggest-active"
			sel = "true"
		}
		attrs := []xango.Attr{
			xango.A("class", class), xango.A("role", "option"), xango.A("id", suggestOptionID(id, i)),
			xango.A("aria-selected", sel), xango.A("aria-setsize", strconv.Itoa(len(p.State.Words))), xango.A("aria-posinset", strconv.Itoa(i+1)),
			// The editing element keeps the focus: pressing a row must not move it.
			xango.On("mousedown", func(e xango.Event) { e.PreventDefault() }),
			xango.On("click", func(xango.Event) {
				if p.OnPick != nil {
					p.OnPick(i)
				}
			}),
		}
		kids := []xango.Node{xango.Text(w)}
		if p.State.Numbers && i < 9 {
			kids = append([]xango.Node{xango.El("span", []xango.Attr{xango.A("class", "gj-suggest-num"), xango.A("aria-hidden", "true")}, xango.Text(strconv.Itoa(i+1)))}, kids...)
		}
		rows = append(rows, xango.El("li", attrs, kids...))
	}
	style := "left:" + strconv.FormatFloat(p.X, 'f', 0, 64) + "px;top:" + strconv.FormatFloat(p.Y, 'f', 0, 64) + "px"
	return xango.El("ul", []xango.Attr{xango.A("class", "gj-suggest"), xango.A("id", id), xango.A("role", "listbox"), xango.A("aria-label", label), xango.A("style", style)}, rows...)
}
