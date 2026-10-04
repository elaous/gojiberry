// hunspell.go reads Hunspell .aff/.dic dictionaries (the affix-compressed form
// LibreOffice and Firefox ship) and expands them into a plain set of words.
//
// It supports the part of the affix format needed for ordinary inflection:
// single-level PFX/SFX rules (strip, affix, condition) with any of the three
// flag encodings (one ASCII byte per flag by default, UTF-8 with FLAG UTF-8,
// two characters with FLAG long), and the ONLYINCOMPOUND flag. It does not
// support compounding (COMPOUNDRULE) or cross-product prefix+suffix forms, which
// affect only a narrow set of forms (like English ordinals "11th").
package spell

import "strings"

// hunspellFlagMode is how a .dic entry's own flags field is tokenized
// -- set by the .aff file's own FLAG directive (absent means
// hunspellFlagDefault).
type hunspellFlagMode int

const (
	// hunspellFlagDefault is Hunspell's own default: each flag is
	// exactly one ASCII byte, several concatenated with no separator
	// ("URTP" is four flags: U, R, T, P).
	//nolint:unused // the zero value, selected by not naming a FLAG mode
	hunspellFlagDefault hunspellFlagMode = iota
	// hunspellFlagUTF8 ("FLAG UTF-8"): each flag is one Unicode code
	// point, letting a flag be a non-ASCII character.
	hunspellFlagUTF8
	// hunspellFlagLong ("FLAG long"): each flag is exactly two ASCII
	// characters.
	hunspellFlagLong
)

// hunspellCondMatcher matches one rune position of a Hunspell affix
// condition -- ".", "[abc]", "[^abc]", or a single literal character.
// A condition is a slice of these, checked one-to-one against the
// tail (suffix rules) or head (prefix rules) of a candidate stem --
// deliberately not compiled as a regexp: Hunspell conditions are
// always a flat concatenation of single-position matchers, never
// quantifiers or groups, so a direct rune-by-rune matcher is both
// simpler and faster than involving the regexp engine at all.
type hunspellCondMatcher func(r rune) bool

// hunspellAffixRule is one PFX/SFX rule line: remove Strip characters
// (already normalized from Hunspell's own "0" to "" -- see
// parseHunspellAffixRule), then add Affix, provided Condition matches.
type hunspellAffixRule struct {
	Strip     string
	Affix     string
	Condition []hunspellCondMatcher
}

// hunspellAffixClass is every rule sharing one flag -- IsSuffix tells
// applyHunspellAffix which end of the stem Strip/Condition apply to.
// CrossProduct is parsed (from the class header's own Y/N field) but
// not acted on -- see this file's own top doc comment.
type hunspellAffixClass struct {
	IsSuffix     bool
	CrossProduct bool
	Rules        []hunspellAffixRule
}

// hunspellAffixFile is a fully-parsed .aff file: every PFX/SFX class,
// keyed by its own flag, plus the two special per-word flags this
// package acts on directly rather than through an affix class.
type hunspellAffixFile struct {
	Mode               hunspellFlagMode
	Classes            map[string]*hunspellAffixClass
	OnlyInCompoundFlag string // "" if the .aff file never declares one
}

// hunspellDicEntry is one .dic line: a stem plus its own raw,
// unsplit flags field (splitHunspellFlags tokenizes it once the
// governing .aff file's own flag mode is known).
type hunspellDicEntry struct {
	Stem     string
	RawFlags string
}

// parseHunspellAffix parses a .aff file's own FLAG/NOSUGGEST/
// ONLYINCOMPOUND directives and every PFX/SFX class -- every other
// directive (SET, TRY, ICONV, COMPOUNDMIN, COMPOUNDRULE, WORDCHARS,
// REP, ...) is recognized by name and skipped outright, along with
// blank lines and "#"-prefixed comments, rather than erroring on
// unrecognized input: a real .aff file is mostly these other
// directives, and this package only ever needs the subset above.
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
			case "long":
				aff.Mode = hunspellFlagLong
			// "num" (comma-separated decimal flags) isn't supported --
			// left at hunspellFlagDefault, which will simply fail to
			// find matching affix classes for a "num"-mode dictionary's
			// own multi-digit flags (a single byte/rune won't match a
			// class keyed by a whole number), degrading to "only bare
			// stems recognized, no inflected forms" for that
			// dictionary rather than misparsing it.
			default:
			}
		case "ONLYINCOMPOUND":
			if len(fields) >= 2 {
				aff.OnlyInCompoundFlag = fields[1]
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
					// A malformed or short rule block -- stop reading
					// rules for this class rather than misinterpreting
					// an unrelated line as one; parsing continues
					// normally from here (i isn't rewound, so whatever
					// this line actually was still gets its own turn
					// through the switch below on the next iteration
					// -- were rule count declarations ever wrong,
					// that's the safer failure than a stuck parser).
					i--
					break
				}
				rule := parseHunspellAffixRule(ruleFields[2], ruleFields[3], ruleFields[4])
				class.Rules = append(class.Rules, rule)
			}
			aff.Classes[flag] = class
		default:
			// Every other directive: not needed by this package.
		}
	}
	return aff
}

// parseHunspellAffixRule builds one rule from a PFX/SFX line's own
// strip/affix/condition fields, normalizing Hunspell's own "0"
// placeholder (meaning "nothing") to "" for Strip/Affix and to a nil
// Condition (matches unconditionally) for Condition.
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

// parseHunspellCondition turns a Hunspell condition string ("0" or ""
// for none, "." for any single character, "[abc]"/"[^abc]" for a
// character class, otherwise literal characters) into a slice of
// per-position matchers. Iterates by rune, not byte, so a non-ASCII
// literal condition character (a language's own accented letters, for
// instance) is treated as one matcher, not split across bytes.
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

// matchHunspellCondition checks cond (nil means "always matches")
// against the last len(cond) runes of stem (atEnd) or the first
// len(cond) runes (prefix side).
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

// applyHunspellAffix produces the derived word rule yields from stem
// (already split into runes by the caller, which expandHunspellEntry
// does once per stem rather than once per rule), or ok=false if
// Condition doesn't match, or (defensively -- a well-formed .aff file
// should never trigger this) Strip isn't actually present where the
// rule says to remove it from.
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

// stripHunspellComment removes a "# ..." trailing comment (and any
// carriage return) from one .aff/.dic line -- Fields still splits the
// remainder on whitespace normally either way, so this only matters
// for lines that are *entirely* a comment (Fields would otherwise
// treat "#" itself as fields[0], matching no directive name, which
// parseHunspellAffix's own default case already ignores harmlessly --
// this is a belt-and-suspenders trim, not load-bearing).
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

// splitHunspellFlags tokenizes a .dic entry's own raw flags field per
// mode -- see hunspellFlagMode's own doc comment for what each mode
// means. An odd-length string under hunspellFlagLong silently drops
// its own last, incomplete character rather than erroring, the same
// "degrade, don't crash on slightly-malformed input" posture the rest
// of this parser takes.
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

// parseHunspellDic parses a .dic file's own entries, skipping the
// first line (a plain word-count, per the format's own convention --
// its value is never checked, only its presence assumed) and any
// entirely-blank line. Splits each remaining line on the first "/"
// only (a real .dic file's own escaped-slash-within-a-word edge case,
// rare enough that no dictionary this package is actually used
// against exercises it, isn't handled).
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

// isHunspellWordish reports whether s is worth adding to the expanded
// dictionary at all: every rune a letter or an apostrophe (matching
// isWordRune plus the apostrophe SpellCheckRanges' own tokenizer
// treats as part of a word), and non-empty -- filters out the
// Hunspell ordinal-number scaffolding entries ("0", "1st", ...; see
// this file's own top doc comment on compounding) and any affix
// application that degenerated to an empty string.
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

// ExpandHunspellDictionary parses aff/dic (a real Hunspell affix file
// and word list, as distributed by essentially every desktop OS/
// LibreOffice/Firefox's own dictionary packages) and returns every
// word form they together describe -- each stem itself (unless
// flagged ONLYINCOMPOUND: see this file's own top doc comment on
// compounding) plus every single-affix inflection its own flags
// enable, lowercased to match how the checker already
// compare every other dictionary. The result is a plain
// map[string]struct{}, the same shape a flat word list already
// produces, so nothing downstream (isKnownWord, SpellCheckRanges,
// SpellSuggestions, CompletionFor, DictFor) needs to know its words
// came from affix expansion rather than a flat list.
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
