package spell

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
)

// HunspellOptions say how much of the format LoadHunspell uses.
type HunspellOptions struct {
	NoCrossProducts bool
	// a hostile dictionary cannot use all the memory
	MaxWords        int
}

type compoundData struct {
	stems map[string]map[string]bool
	aff   *hunspellAffixFile
	min   int
}

func LoadHunspell(dic, aff []byte, o HunspellOptions) *Dictionary {
	affText := string(aff)
	set := parseHunspellAffix(affText).Set
	dicText := string(dic)
	if enc := encodingOf(set); enc != nil && !utf8.Valid(aff) {
		affText = decodeWith(enc, aff)
	}
	if enc := encodingOf(set); enc != nil && !utf8.Valid(dic) {
		dicText = decodeWith(enc, dic)
	}
	return loadHunspell(dicText, affText, o)
}

func decodeWith(enc encoding.Encoding, b []byte) string {
	out, err := enc.NewDecoder().Bytes(b)
	if err != nil {
		return string(b)
	}
	return string(out)
}

func encodingOf(name string) encoding.Encoding {
	switch strings.ToUpper(strings.ReplaceAll(name, "_", "-")) {
	case "ISO8859-1", "ISO-8859-1", "LATIN1":
		return charmap.ISO8859_1
	case "ISO8859-2", "ISO-8859-2":
		return charmap.ISO8859_2
	case "ISO8859-3", "ISO-8859-3":
		return charmap.ISO8859_3
	case "ISO8859-4", "ISO-8859-4":
		return charmap.ISO8859_4
	case "ISO8859-5", "ISO-8859-5":
		return charmap.ISO8859_5
	case "ISO8859-7", "ISO-8859-7":
		return charmap.ISO8859_7
	case "ISO8859-9", "ISO-8859-9":
		return charmap.ISO8859_9
	case "ISO8859-13", "ISO-8859-13":
		return charmap.ISO8859_13
	case "ISO8859-15", "ISO-8859-15":
		return charmap.ISO8859_15
	case "KOI8-R":
		return charmap.KOI8R
	case "KOI8-U":
		return charmap.KOI8U
	case "MICROSOFT-CP1250", "CP1250", "WINDOWS-1250":
		return charmap.Windows1250
	case "MICROSOFT-CP1251", "CP1251", "WINDOWS-1251":
		return charmap.Windows1251
	case "MICROSOFT-CP1252", "CP1252", "WINDOWS-1252":
		return charmap.Windows1252
	}
	return nil
}

func loadHunspell(dicText, affText string, o HunspellOptions) *Dictionary {
	aff := parseHunspellAffix(affText)
	entries := parseHunspellDic(dicText)
	limit := o.MaxWords
	if limit <= 0 {
		limit = 4_000_000
	}
	d := &Dictionary{words: make(map[string]struct{}, len(entries)*3)}
	if aff.Try != "" {
		d.try = []rune(strings.ToLower(aff.Try))
	}
	d.rep = aff.Rep
	d.keys = aff.Key
	compounds := aff.CompoundFlag != "" || aff.CompoundBegin != "" || aff.CompoundMiddle != "" || aff.CompoundEnd != "" || len(aff.CompoundRules) > 0
	ruleFlags := map[string]bool{}
	for _, r := range aff.CompoundRules {
		for _, e := range r {
			ruleFlags[e.Flag] = true
		}
	}
	var cd *compoundData
	if compounds {
		cd = &compoundData{stems: map[string]map[string]bool{}, aff: aff, min: aff.CompoundMin}
		if cd.min <= 0 {
			cd.min = 3
		}
	}
	add := func(w string) {
		if isHunspellWordish(w) && len(d.words) < limit {
			d.words[strings.ToLower(w)] = struct{}{}
		}
	}
	for _, e := range entries {
		flags := splitHunspellFlags(e.RawFlags, aff.Mode)
		has := map[string]bool{}
		for _, f := range flags {
			has[f] = true
		}
		stem := strings.ToLower(e.Stem)
		if aff.ForbiddenWord != "" && has[aff.ForbiddenWord] {
			if d.forbidden == nil {
				d.forbidden = map[string]struct{}{}
			}
			d.forbidden[stem] = struct{}{}
			continue
		}
		bare := (aff.OnlyInCompoundFlag == "" || !has[aff.OnlyInCompoundFlag]) && (aff.NeedAffix == "" || !has[aff.NeedAffix])
		if bare {
			add(e.Stem)
		}
		if cd != nil && isHunspellWordish(e.Stem) {
			fl := map[string]bool{}
			for _, f := range []string{aff.CompoundFlag, aff.CompoundBegin, aff.CompoundMiddle, aff.CompoundEnd} {
				if f != "" && has[f] {
					fl[f] = true
				}
			}
			for f := range ruleFlags {
				if has[f] {
					fl[f] = true
				}
			}
			if len(fl) > 0 {
				cd.stems[stem] = fl
			}
		}
		stemRunes := []rune(e.Stem)
		var prefixed, suffixed []appliedAffix
		for _, f := range flags {
			class, ok := aff.Classes[f]
			if !ok {
				continue
			}
			for _, rule := range class.Rules {
				if derived, ok := applyHunspellAffix(stemRunes, rule, class.IsSuffix); ok {
					add(derived)
					if class.CrossProduct {
						a := appliedAffix{word: derived}
						if class.IsSuffix {
							suffixed = append(suffixed, a)
						} else {
							prefixed = append(prefixed, a)
						}
					}
				}
			}
		}
		if !o.NoCrossProducts && len(prefixed) > 0 && len(suffixed) > 0 {
			for _, f := range flags {
				class, ok := aff.Classes[f]
				if !ok || class.IsSuffix || !class.CrossProduct {
					continue
				}
				for _, s := range suffixed {
					sr := []rune(s.word)
					for _, rule := range class.Rules {
						if !matchHunspellCondition(stemRunes, rule.Condition, false) {
							continue
						}
						if derived, ok := applyPrefixTo(sr, rule); ok {
							add(derived)
						}
					}
				}
			}
		}
	}
	if cd != nil && len(cd.stems) > 0 {
		d.compound = cd
	}
	return d
}

type appliedAffix struct{ word string }

func applyPrefixTo(word []rune, rule hunspellAffixRule) (string, bool) {
	strip := []rune(rule.Strip)
	if !runesHavePrefix(word, strip) {
		return "", false
	}
	return rule.Affix + string(word[len(strip):]), true
}

func parseCompoundRule(pat string, mode hunspellFlagMode) []compoundElem {
	var out []compoundElem
	rs := []rune(pat)
	for i := 0; i < len(rs); i++ {
		var flag string
		switch {
		case mode == hunspellFlagLong && rs[i] == '(':
			j := i + 1
			for j < len(rs) && rs[j] != ')' {
				j++
			}
			flag = string(rs[i+1 : j])
			i = j
		case mode == hunspellFlagNum && rs[i] == '(':
			j := i + 1
			for j < len(rs) && rs[j] != ')' {
				j++
			}
			flag = string(rs[i+1 : j])
			i = j
		case rs[i] == '*' || rs[i] == '?':
			if len(out) > 0 {
				out[len(out)-1].Mod = byte(rs[i])
			}
			continue
		default:
			flag = string(rs[i])
		}
		out = append(out, compoundElem{Flag: flag})
	}
	return out
}

func (c *compoundData) isCompound(word string) bool {
	rs := []rune(word)
	if len(rs) < 2*c.min {
		return false
	}
	if c.aff.CompoundFlag != "" || c.aff.CompoundBegin != "" {
		memo := map[int]bool{}
		var split func(pos, parts int) bool
		split = func(pos, parts int) bool {
			if pos == len(rs) {
				return parts >= 2
			}
			key := pos*8 + min(parts, 3)
			if v, ok := memo[key]; ok {
				return v
			}
			res := false
			for end := pos + c.min; end <= len(rs) && !res; end++ {
				last := end == len(rs)
				fl, ok := c.stems[string(rs[pos:end])]
				if !ok {
					continue
				}
				first := pos == 0
				allowed := fl[c.aff.CompoundFlag] && c.aff.CompoundFlag != ""
				switch {
				case first && fl[c.aff.CompoundBegin] && c.aff.CompoundBegin != "":
					allowed = true
				case last && fl[c.aff.CompoundEnd] && c.aff.CompoundEnd != "":
					allowed = true
				case !first && !last && fl[c.aff.CompoundMiddle] && c.aff.CompoundMiddle != "":
					allowed = true
				}
				if allowed && (last || len(rs)-end >= c.min) {
					res = split(end, parts+1)
				}
			}
			memo[key] = res
			return res
		}
		if split(0, 0) {
			return true
		}
	}
	for _, rule := range c.aff.CompoundRules {
		memo := map[[3]int]bool{}
		var match func(pos, el, parts int) bool
		match = func(pos, el, parts int) bool {
			if el == len(rule) {
				return pos == len(rs) && parts >= 2
			}
			k := [3]int{pos, el, min(parts, 3)}
			if v, ok := memo[k]; ok {
				return v
			}
			e := rule[el]
			res := false
			if e.Mod == '*' || e.Mod == '?' {
				res = match(pos, el+1, parts)
			}
			for end := pos + c.min; end <= len(rs) && !res; end++ {
				fl, ok := c.stems[string(rs[pos:end])]
				if !ok || !fl[e.Flag] {
					continue
				}
				if e.Mod == '*' {
					res = match(end, el, parts+1) || match(end, el+1, parts+1)
				} else {
					res = match(end, el+1, parts+1)
				}
			}
			memo[k] = res
			return res
		}
		if match(0, 0, 0) {
			return true
		}
	}
	return false
}
