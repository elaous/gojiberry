package spell

import "strings"

type hunspellFlagMode int

const (
	// hunspellFlagDefault: each flag is exactly one ASCII byte.
	//nolint:unused // the zero value, selected by not naming a FLAG mode
	hunspellFlagDefault hunspellFlagMode = iota
	hunspellFlagUTF8
	hunspellFlagLong
	hunspellFlagNum
)

type hunspellCondMatcher func(r rune) bool

type hunspellAffixRule struct {
	Strip     string
	Affix     string
	Condition []hunspellCondMatcher
}

type hunspellAffixClass struct {
	IsSuffix     bool
	CrossProduct bool
	Rules        []hunspellAffixRule
}

type hunspellAffixFile struct {
	Mode               hunspellFlagMode
	Classes            map[string]*hunspellAffixClass
	OnlyInCompoundFlag string // "" if the .aff file never declares one

	Set                string // SET: the file's character encoding ("" is UTF-8)
	Try                string // TRY: the letters of the language, most common first (suggestions)
	Rep                []hunspellRep
	Key                [][]rune // KEY: keyboard rows ("qwertyuiop|asdfghjkl|zxcvbnm"), neighbours are the typos
	CompoundFlag       string
	CompoundBegin      string
	CompoundMiddle     string
	CompoundEnd        string
	CompoundMin        int
	CompoundRules      [][]compoundElem
	NeedAffix          string
	ForbiddenWord      string
	CompoundPermitFlag string
}

type hunspellRep struct{ From, To string }

type compoundElem struct {
	Flag string
	Mod  byte
}

type hunspellDicEntry struct {
	Stem     string
	RawFlags string
}

func parseHunspellAffix(data string) *hunspellAffixFile {
	aff := &hunspellAffixFile{Classes: map[string]*hunspellAffixClass{}}

	lines := strings.Split(data, "\n")
	for i := 0; i < len(lines); i++ {
		fields := strings.Fields(stripHunspellComment(lines[i]))
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "FLAG":
			if len(fields) < 2 {
				continue
			}
			switch fields[1] {
			case "UTF-8":
				aff.Mode = hunspellFlagUTF8
			case "num":
				aff.Mode = hunspellFlagNum
			case "long":
				aff.Mode = hunspellFlagLong

			default:
			}
		case "ONLYINCOMPOUND":
			if len(fields) >= 2 {
				aff.OnlyInCompoundFlag = fields[1]
			}
		case "SET":
			if len(fields) >= 2 {
				aff.Set = fields[1]
			}
		case "TRY":
			if len(fields) >= 2 {
				aff.Try = fields[1]
			}
		case "REP":
			if len(fields) >= 3 {
				aff.Rep = append(aff.Rep, hunspellRep{From: fields[1], To: strings.ReplaceAll(fields[2], "_", " ")})
			}
		case "KEY":
			if len(fields) >= 2 {
				for _, row := range strings.Split(fields[1], "|") {
					aff.Key = append(aff.Key, []rune(row))
				}
			}
		case "COMPOUNDFLAG":
			if len(fields) >= 2 {
				aff.CompoundFlag = fields[1]
			}
		case "COMPOUNDBEGIN":
			if len(fields) >= 2 {
				aff.CompoundBegin = fields[1]
			}
		case "COMPOUNDMIDDLE":
			if len(fields) >= 2 {
				aff.CompoundMiddle = fields[1]
			}
		case "COMPOUNDEND":
			if len(fields) >= 2 {
				aff.CompoundEnd = fields[1]
			}
		case "COMPOUNDMIN":
			if len(fields) >= 2 {
				aff.CompoundMin = atoiOr(fields[1], 0)
			}
		case "COMPOUNDPERMITFLAG":
			if len(fields) >= 2 {
				aff.CompoundPermitFlag = fields[1]
			}
		case "NEEDAFFIX", "PSEUDOROOT":
			if len(fields) >= 2 {
				aff.NeedAffix = fields[1]
			}
		case "FORBIDDENWORD":
			if len(fields) >= 2 {
				aff.ForbiddenWord = fields[1]
			}
		case "COMPOUNDRULE":
			if len(fields) >= 2 && atoiOr(fields[1], -1) < 0 {
				if rule := parseCompoundRule(fields[1], aff.Mode); len(rule) > 0 {
					aff.CompoundRules = append(aff.CompoundRules, rule)
				}
			}
		case "PFX", "SFX":
			if len(fields) < 4 {
				continue
			}
			flag := fields[1]
			crossProduct := fields[2] == "Y"
			numRules := atoiOr(fields[3], 0)
			class := &hunspellAffixClass{IsSuffix: fields[0] == "SFX", CrossProduct: crossProduct}
			for n := 0; n < numRules && i+1 < len(lines); n++ {
				i++
				ruleFields := strings.Fields(stripHunspellComment(lines[i]))
				if len(ruleFields) < 5 || ruleFields[0] != fields[0] || ruleFields[1] != flag {
					i--
					break
				}
				rule := parseHunspellAffixRule(ruleFields[2], ruleFields[3], ruleFields[4])
				class.Rules = append(class.Rules, rule)
			}
			aff.Classes[flag] = class
		default:
		}
	}
	return aff
}

func parseHunspellAffixRule(strip, affix, condition string) hunspellAffixRule {
	if strip == "0" {
		strip = ""
	}
	if affix == "0" {
		affix = ""
	}
	return hunspellAffixRule{
		Strip:     strip,
		Affix:     affix,
		Condition: parseHunspellCondition(condition),
	}
}

func parseHunspellCondition(condition string) []hunspellCondMatcher {
	if condition == "" || condition == "0" {
		return nil
	}
	runes := []rune(condition)
	var out []hunspellCondMatcher
	for i := 0; i < len(runes); i++ {
		switch runes[i] {
		case '.':
			out = append(out, func(rune) bool { return true })
		case '[':
			j := i + 1
			negate := j < len(runes) && runes[j] == '^'
			if negate {
				j++
			}
			start := j
			for j < len(runes) && runes[j] != ']' {
				j++
			}
			set := make(map[rune]bool, j-start)
			for _, r := range runes[start:j] {
				set[r] = true
			}
			out = append(out, func(r rune) bool { return set[r] != negate })
			i = j // the loop's own i++ advances past the closing ']'
		default:
			lit := runes[i]
			out = append(out, func(r rune) bool { return r == lit })
		}
	}
	return out
}

func matchHunspellCondition(stem []rune, cond []hunspellCondMatcher, atEnd bool) bool {
	if len(cond) == 0 {
		return true
	}
	if len(stem) < len(cond) {
		return false
	}
	offset := 0
	if atEnd {
		offset = len(stem) - len(cond)
	}
	for i, m := range cond {
		if !m(stem[offset+i]) {
			return false
		}
	}
	return true
}

func applyHunspellAffix(stem []rune, rule hunspellAffixRule, isSuffix bool) (string, bool) {
	if !matchHunspellCondition(stem, rule.Condition, isSuffix) {
		return "", false
	}
	strip := []rune(rule.Strip)
	if len(stem) < len(strip) {
		return "", false
	}
	affix := []rune(rule.Affix)
	if isSuffix {
		if !runesHaveSuffix(stem, strip) {
			return "", false
		}
		base := stem[:len(stem)-len(strip)]
		return string(base) + string(affix), true
	}
	if !runesHavePrefix(stem, strip) {
		return "", false
	}
	base := stem[len(strip):]
	return string(affix) + string(base), true
}

func runesHaveSuffix(s, suffix []rune) bool {
	if len(suffix) == 0 {
		return true
	}
	off := len(s) - len(suffix)
	for i, r := range suffix {
		if s[off+i] != r {
			return false
		}
	}
	return true
}

func runesHavePrefix(s, prefix []rune) bool {
	if len(prefix) > len(s) {
		return false
	}
	for i, r := range prefix {
		if s[i] != r {
			return false
		}
	}
	return true
}

func stripHunspellComment(line string) string {
	line = strings.TrimSuffix(line, "\r")
	if i := strings.IndexByte(line, '#'); i >= 0 {
		line = line[:i]
	}
	return line
}

func atoiOr(s string, fallback int) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return fallback
		}
		n = n*10 + int(r-'0')
	}
	if s == "" {
		return fallback
	}
	return n
}

func splitHunspellFlags(raw string, mode hunspellFlagMode) []string {
	if raw == "" {
		return nil
	}
	switch mode {
	case hunspellFlagUTF8:
		runes := []rune(raw)
		out := make([]string, len(runes))
		for i, r := range runes {
			out[i] = string(r)
		}
		return out
	case hunspellFlagNum:
		return strings.Split(raw, ",")
	case hunspellFlagLong:
		var out []string
		for i := 0; i+1 < len(raw); i += 2 {
			out = append(out, raw[i:i+2])
		}
		return out
	default: // hunspellFlagDefault
		out := make([]string, len(raw))
		for i := 0; i < len(raw); i++ {
			out[i] = string(raw[i])
		}
		return out
	}
}

// the first line's word count is never checked
func parseHunspellDic(data string) []hunspellDicEntry {
	lines := strings.Split(data, "\n")
	var entries []hunspellDicEntry
	for i, line := range lines {
		if i == 0 {
			continue // word-count header line
		}
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		stem, flags, _ := strings.Cut(line, "/")
		if stem == "" {
			continue
		}
		entries = append(entries, hunspellDicEntry{Stem: stem, RawFlags: flags})
	}
	return entries
}

func isHunspellWordish(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !isWordRune(r) && r != '\'' {
			return false
		}
	}
	return true
}

func ExpandHunspellDictionary(dicText, affText string) map[string]struct{} {
	aff := parseHunspellAffix(affText)
	entries := parseHunspellDic(dicText)

	set := make(map[string]struct{}, len(entries)*2)
	for _, e := range entries {
		flags := splitHunspellFlags(e.RawFlags, aff.Mode)
		onlyInCompound := false
		if aff.OnlyInCompoundFlag != "" {
			for _, f := range flags {
				if f == aff.OnlyInCompoundFlag {
					onlyInCompound = true
					break
				}
			}
		}
		stemRunes := []rune(e.Stem)
		if !onlyInCompound && isHunspellWordish(e.Stem) {
			set[strings.ToLower(e.Stem)] = struct{}{}
		}
		for _, f := range flags {
			class, ok := aff.Classes[f]
			if !ok {
				continue
			}
			for _, rule := range class.Rules {
				derived, ok := applyHunspellAffix(stemRunes, rule, class.IsSuffix)
				if ok && isHunspellWordish(derived) {
					set[strings.ToLower(derived)] = struct{}{}
				}
			}
		}
	}
	return set
}
