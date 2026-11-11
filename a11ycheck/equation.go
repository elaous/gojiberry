package a11ycheck

import (
	"strings"

	"github.com/elaous/gojiberry/docfile"
)

// EquationFindings checks one equation's text alternative.
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
