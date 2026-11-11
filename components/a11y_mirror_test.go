package components

import (
	"strings"
	"testing"
)

func TestAccessibleTextWritesLinksAsRealAnchorsAndDropsUnsafeOnes(t *testing.T) {
	html := render(t, AccessibleText(AccessibleTextProps{Blocks: []A11yBlock{
		{Kind: A11yParagraph, Text: "See the docs or this.", Runs: []A11yRun{
			{Text: "See the "}, {Text: "docs", Href: "https://example.com/docs"}, {Text: " or "},
			{Text: "this", Href: "javascript:alert(1)"}, {Text: "."}}},
	}}))
	if !strings.Contains(html, `<a href="https://example.com/docs" target="_blank" rel="noopener noreferrer">docs</a>`) {
		t.Errorf("a link is a real anchor that opens safely:\n%s", html)
	}
	if strings.Contains(html, "javascript:") || strings.Count(html, "<a ") != 1 {
		t.Errorf("a script address never becomes a link:\n%s", html)
	}
	if !strings.Contains(html, "this") {
		t.Errorf("the text of a dropped link stays:\n%s", html)
	}
}

func TestAccessibleTextTableHeaderScopeAndSections(t *testing.T) {
	html := render(t, AccessibleText(AccessibleTextProps{Blocks: []A11yBlock{
		{Kind: A11yTableRow, Cells: []string{"Name", "Points"}},
		{Kind: A11yTableRow, Cells: []string{"Ana", "3"}},
		{Kind: A11yTableRow, Cells: []string{"Bo", "4"}},
	}}))
	for _, want := range []string{`<thead><tr><th scope="col">Name</th><th scope="col">Points</th></tr></thead>`, `<tbody><tr><td>Ana</td><td>3</td></tr><tr><td>Bo</td><td>4</td></tr></tbody>`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in\n%s", want, html)
		}
	}
	if strings.Contains(html, "<td>Name") {
		t.Errorf("the header row is not data:\n%s", html)
	}
}

func TestAccessibleTextOutlineIsANavigationLandmarkLinkingToTheHeadings(t *testing.T) {
	html := render(t, AccessibleText(AccessibleTextProps{
		Blocks: []A11yBlock{
			{Kind: A11yHeading, Level: 1, Text: "Title", Mapped: true, Start: 0},
			{Kind: A11yParagraph, Text: "Body."},
			{Kind: A11yHeading, Level: 2, Text: "First", Mapped: true, Start: 20},
			{Kind: A11yHeading, Level: 2, Text: "Second", Mapped: true, Start: 40},
			{Kind: A11yHeading, Level: 1, Text: "Other", Mapped: true, Start: 60},
		},
		OnGoto: func(string, int, bool) {},
	}))
	want := `<nav id="a11y-text-outline" aria-label="Document outline"><ul>` +
		`<li><a href="#a11y-text-h0">Title</a><ul><li><a href="#a11y-text-h1">First</a></li><li><a href="#a11y-text-h2">Second</a></li></ul></li>` +
		`<li><a href="#a11y-text-h3">Other</a></li></ul></nav>`
	if !strings.Contains(html, want) {
		t.Errorf("the outline nests by level:\n%s", html)
	}
	for _, id := range []string{"a11y-text-h0", "a11y-text-h1", "a11y-text-h2", "a11y-text-h3"} {
		if !strings.Contains(html, `id="`+id+`"`) {
			t.Errorf("heading %s has its id", id)
		}
	}
	if strings.Index(html, "<nav") > strings.Index(html, "<h1") {
		t.Error("the outline comes before the text")
	}
	none := render(t, AccessibleText(AccessibleTextProps{Blocks: []A11yBlock{{Kind: A11yParagraph, Text: "No headings."}}}))
	if strings.Contains(none, "<nav") {
		t.Errorf("no headings, no outline:\n%s", none)
	}
}

func TestAccessibleTextMarksCommentedTextAndListsTheComments(t *testing.T) {
	html := render(t, AccessibleText(AccessibleTextProps{
		Blocks: []A11yBlock{{Kind: A11yParagraph, Text: "Check this wording please.", Runs: []A11yRun{
			{Text: "Check "}, {Text: "this wording", Comments: "c1,gone"}, {Text: " please."}}}},
		Comments: []A11yComment{{ID: "c1", Author: "Ana", Excerpt: "this wording", Text: "Too vague.", Replies: []A11yReply{{Author: "Bo", Text: "Agreed."}}},
			{ID: "c2", Author: "Cy", Text: "Done.", Resolved: true}},
	}))
	for _, want := range []string{
		`<mark aria-details="a11y-text-c-c1"><span class="gj-sr-only">Commented text: </span>this wording</mark>`,
		`<aside id="a11y-text-comments" aria-label="2 comments">`,
		`<article id="a11y-text-c-c1"><p>Comment by Ana: Too vague.</p><p>On the text: this wording</p><p>Reply by Bo: Agreed.</p></article>`,
		`<article id="a11y-text-c-c2"><p>Comment by Cy (resolved): Done.</p></article>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in\n%s", want, html)
		}
	}
	if strings.Contains(html, "a11y-text-c-gone") {
		t.Errorf("a comment that is not listed is not pointed to:\n%s", html)
	}
}

func TestAccessibleTextCarriesTheMappingToTheEditor(t *testing.T) {
	html := render(t, AccessibleText(AccessibleTextProps{Blocks: []A11yBlock{
		{Kind: A11yParagraph, Text: "One", Mapped: true, Start: 0},
		{Kind: A11yListItem, Text: "☐ todo", Mapped: true, Start: 4, Lead: 2, Caret: true},
		{Kind: A11yParagraph, Text: "Image: a berry", Mapped: true, Start: 11, Object: true},
		{Kind: A11yParagraph, Text: "Unmapped"},
	}}))
	for _, want := range []string{`<p data-gj-start="0">One</p>`, `data-gj-start="4" data-gj-lead="2" aria-current="location"`, `data-gj-start="11" data-gj-object="true"`, `<p>Unmapped</p>`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in\n%s", want, html)
		}
	}
	if strings.Count(html, "aria-current") != 1 {
		t.Errorf("only the block with the caret is marked:\n%s", html)
	}
}

func TestAccessibleTextMarksRightToLeftBlocks(t *testing.T) {
	html := render(t, AccessibleText(AccessibleTextProps{Blocks: []A11yBlock{
		{Kind: A11yParagraph, Text: "Plain"},
		{Kind: A11yParagraph, Text: "שלום עולם"},
		{Kind: A11yParagraph, Runs: []A11yRun{{Text: "mixed "}, {Text: "مرحبا", Lang: "ar"}}},
	}}))
	for _, want := range []string{`<p>Plain</p>`, `<p dir="auto">שלום עולם</p>`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in\n%s", want, html)
		}
	}
	if strings.Count(html, `dir="auto"`) != 2 {
		t.Errorf("two blocks are right to left:\n%s", html)
	}
}
