package a11ycheck

import (
	"strings"

	"github.com/elaous/gojiberry/docfile"
)

// EquationFindings checks one equation for a text alternative (roadmap 78): the words a person would read aloud.
// Check calls it for every equation in a document (offset and item say where it is); a host that inserts an
// equation calls it with offset and item 0 on the one about to be inserted and refuses it while an error comes
// back, so an equation is never added with nothing for a reader who cannot see the symbols.
//
// It judges that words exist, not that they are right: an alternative that says nothing true passes.
func EquationFindings(e docfile.EquationItem, offset, item int) []Finding {
	alt := strings.TrimSpace(e.Alt)
	formula := strings.TrimSpace(e.Formula)
	switch alt {
	case "":
		return []Finding{{Rule: RuleEquationAlt, Severity: Error, Offset: offset, Item: item, Text: excerpt(formula),
			Message: "An equation has no text alternative, so a reader who cannot see it meets symbols with no words.",
			Fix:     "Write the equation in words, as you would read it aloud.",
			Action:  ActionDescribe}}
	case formula:
		return []Finding{{Rule: RuleEquationAlt, Severity: Warning, Offset: offset, Item: item, Text: excerpt(formula),
			Message: "The text alternative of an equation is the formula typed again, which a screen reader reads symbol by symbol.",
			Fix:     "Write the equation in words, as you would read it aloud.",
			Action:  ActionDescribe}}
	}
	return nil
}
