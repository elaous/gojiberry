//go:build !(js && wasm)

package e2etest

import (
	_ "embed"
	"fmt"
	"strings"
)

// axe-core, vendored (axe/README.md), is injected into the page on demand.
//
//go:embed axe/axe.min.js
var axeSource string

// AxeNode is one element a rule failed on.
type AxeNode struct {
	Target  []any  `json:"target"`
	HTML    string `json:"html"`
	Summary string `json:"failureSummary"`
}

// AxeViolation is one failed accessibility rule with the elements it failed on.
type AxeViolation struct {
	ID     string    `json:"id"`
	Impact string    `json:"impact"`
	Help   string    `json:"help"`
	Nodes  []AxeNode `json:"nodes"`
}

// String is the violation as a test failure shows it.
func (v AxeViolation) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%s): %s", v.ID, v.Impact, v.Help)
	for i, n := range v.Nodes {
		if i == 5 {
			fmt.Fprintf(&b, "\n    ... and %d more", len(v.Nodes)-5)
			break
		}
		html := n.HTML
		if len(html) > 160 {
			html = html[:160] + "..."
		}
		fmt.Fprintf(&b, "\n    %v  %s", n.Target, html)
	}
	return b.String()
}

// Axe runs axe-core over the page as it is now (open menus and dialogs included) and returns what
// failed. context is a CSS selector to limit the run to, or "" for the whole document. It checks
// what a tool can: roles, names, labels, contrast, landmarks and ids. It does not replace using the
// page with a screen reader or the keyboard. Rules in disable are left out (name the reason where
// the test lists them).
func (d *Tab) Axe(context string, disable ...string) []AxeViolation {
	d.t.Helper()
	d.Eval(`(function(){ if (!window.axe) { `+axeSource+` } return true })()`, nil)
	// #state is the harness's JSON dump of the page's state, not part of the page.
	ctx := `{exclude: [['#state']]}`
	if context != "" {
		ctx = fmt.Sprintf(`{include: [[%q]], exclude: [['#state']]}`, context)
	}
	rules := "{}"
	if len(disable) > 0 {
		var parts []string
		for _, r := range disable {
			parts = append(parts, fmt.Sprintf("%q: {enabled: false}", r))
		}
		rules = "{" + strings.Join(parts, ",") + "}"
	}
	var out []AxeViolation
	d.Eval(`axe.run(`+ctx+`, {rules: `+rules+`, resultTypes: ['violations']}).then(r => r.violations.map(v => ({id: v.id, impact: v.impact || '', help: v.help,
		nodes: v.nodes.map(n => ({target: n.target, html: n.html, failureSummary: n.failureSummary || ''}))})))`, &out)
	return out
}
