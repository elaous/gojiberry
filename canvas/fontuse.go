package canvas

import (
	"sort"
	"strings"
)

type FontUse struct {
	Family string
	Weight int // CSS weight; 400 when nothing says
	Italic bool
}

// FontUser is implemented by components that list their fonts.
type FontUser interface {
	UsedFonts() []FontUse
}

func firstFamily(list string) string {
	name, _, _ := strings.Cut(list, ",")
	return strings.Trim(strings.TrimSpace(name), `"'`)
}

type fontUses struct {
	base TextStyle
	set  map[FontUse]bool
}

func newFontUses(base TextStyle) *fontUses {
	u := &fontUses{base: base, set: map[FontUse]bool{}}
	u.add(Format{})
	return u
}

func (u *fontUses) add(fm Format) {
	family := firstFamily(fm.FontFamily)
	if family == "" {
		family = firstFamily(u.base.FontFamily)
	}
	if family == "" || genericFamily(family) {
		return
	}
	weight := fm.FontWeight
	if weight == 0 {
		weight = u.base.FontWeight
	}
	if weight == 0 {
		weight = 400
	}
	style := fm.FontStyle
	if style == "" {
		style = u.base.FontStyle
	}
	u.set[FontUse{Family: family, Weight: weight, Italic: style == StyleItalic || style == StyleOblique}] = true
}

func (u *fontUses) list() []FontUse {
	out := make([]FontUse, 0, len(u.set))
	for k := range u.set {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Family != b.Family {
			return a.Family < b.Family
		}
		if a.Weight != b.Weight {
			return a.Weight < b.Weight
		}
		return !a.Italic && b.Italic
	})
	return out
}

func genericFamily(name string) bool {
	switch strings.ToLower(name) {
	case "serif", "sans-serif", "monospace", "cursive", "fantasy", "system-ui", "ui-serif", "ui-sans-serif", "ui-monospace":
		return true
	}
	return false
}

// UsedFonts lists the fonts the text uses, including named styles.
func (f *TextFlow) UsedFonts() []FontUse {
	u := newFontUses(f.Style)
	for _, it := range f.items() {
		switch v := it.(type) {
		case Chars:
			if v.Text != "" {
				u.add(f.expand(Format{}, v.Format))
			}
		case ParaFormat:
			if def, ok := f.StyleByName(v.Style); ok && def.Paragraph {
				u.add(def.Format)
			}
		}
	}
	return u.list()
}

func (ft *FloatingText) UsedFonts() []FontUse {
	u := newFontUses(ft.Style)
	for _, r := range ft.runes {
		u.add(r.f)
	}
	return u.list()
}

func (s *Stage) UsedFonts() []FontUse {
	set := map[FontUse]bool{}
	var out []FontUse
	for _, c := range s.Components {
		if fu, ok := c.(FontUser); ok {
			for _, u := range fu.UsedFonts() {
				if !set[u] {
					set[u] = true
					out = append(out, u)
				}
			}
		}
	}
	return out
}

func (sv *ScrollView) UsedFonts() []FontUse { return sv.content.UsedFonts() }

func (s *Stage) reportFonts() {
	if s.OnFontUse == nil {
		return
	}
	for _, u := range s.UsedFonts() {
		if !s.fontsSeen[u] {
			if s.fontsSeen == nil {
				s.fontsSeen = map[FontUse]bool{}
			}
			s.fontsSeen[u] = true
			s.OnFontUse(u)
		}
	}
}

func (s *Stage) ForgetFonts() { s.fontsSeen = nil }
