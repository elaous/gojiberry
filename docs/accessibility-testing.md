# Accessibility testing

Two parts: a gate that runs on every change, and a script a person runs before every release. The
gate finds what a tool can; only the script finds what it cannot (whether the page can be *used* with
a screen reader, a keyboard, a magnifier).

## 1. The automated gate: `make a11y-test`

Run it on this machine with `make a11y-test` (or `make ci-local STEPS=a11y-test`, which also writes a short
report to `local-ci-report.txt`; see [testing-workflow.md](testing-workflow.md#running-the-ci-steps-locally)). The hosted
workflow is manual (choose `a11y` under "Run workflow") and not needed for it. It needs a Chromium or Chrome and **fails without one**
(`GOJIBERRY_REQUIRE_BROWSER`), so it cannot pass by skipping.

What it checks, in the canvas editor showcase (`examples/canvas-editor-showcase`):

| Test | What it covers |
| --- | --- |
| `TestNoAccessibilityViolationsAxe` | axe-core (4.10, vendored in `internal/e2etest/axe`) over the document, every tab of the side panel, every menu open, and the find bar |
| `TestNoAccessibilityViolationsAxeInTheKeyDirectory` | axe-core over the encryption panels and the key directory (owner key, backup, pinned log keys, owner keys to confirm) |
| `TestAccessibilityAndTranslation`, `TestScreenReaderCopyFollowsStructure`, `TestImageDescriptionIsInTheReadingCopy`, `TestSelectionLanguageReachesTheReadingCopy` | the screen-reader copy of the canvas: headings, lists, tables, images with their description, the language of a run, the live region |
| `TestMenuEntryChosenWithTheKeyboard`, `TestMenusAreOperatedWithTheArrowKeys`, `TestSidePanelTabsAreOperatedWithTheArrowKeys`, `TestDialogFocusIsTrappedAndReturned`, `TestFontSelectorKeyboard` | operating menus, tabs, dialogs and option lists with the keyboard alone, focus trapped in a dialog and returned |
| `TestDisplayPreferencesAreApplied`, `TestDisplayPreferencesMenu` | text size, spacing, contrast and motion preferences |

### The baseline

A new violation fails the build. The rules that are switched off are listed in `axeDisabled`
(`e2e_axe_test.go`), each with the reason and the roadmap entry that tracks fixing it. **Adding a rule
there needs a review and a roadmap entry**; removing one is always welcome. Today: `heading-order`
(the panels' titles are `h4`, the components' own level, under a page whose headings are in the
reading copy; roadmap item 48, "heading levels").

### What the gate does not cover

axe finds roles, names, labels, contrast, landmarks and duplicate ids. It does not tell whether the
reading order makes sense, whether a live announcement is too chatty, whether a dialog is understood,
or how the canvas feels to a person using a screen reader. That is what part 2 is for. Pages other than
the showcase (the other examples) are not under the gate yet.

## 2. The manual script, before each release

Run with a screen reader on the build that will be released, on each of: **NVDA with Firefox** or
Chrome (Windows), **JAWS with Chrome** (Windows) and **VoiceOver with Safari** (macOS). Use the keyboard
only. Record the result for each step in the table below, with the build and the date, and file an
issue for each failure. A failure of a step marked ★ stops the release.

| # | Step | Expect |
| --- | --- | --- |
| 1 ★ | Open the editor. Move to the document. | The reader says it is a document, with its title; the outline is reachable as headings. |
| 2 ★ | Switch to Edit mode and type a sentence. | The mode change is announced; typed text is read back; no repeated or missing announcements. |
| 3 ★ | Move by heading, by list item and by table cell; read a table with its header row. | Levels, list positions and the header of each cell are spoken. |
| 4 | Move to an image. | Its description is read; an image marked decorative is skipped; one without a description is named as lacking one. |
| 5 | Move to text in another language. | The reader switches voice (the run has a `lang`). |
| 6 ★ | Open each menu with the keyboard, move through it, choose an entry, Escape. | Names and states (checked, submenu) are spoken; Escape returns the focus to the menu button. |
| 7 ★ | Open a dialog (Page setup, a confirmation). Tab round it. Close it. | The focus stays inside it, the title is spoken, and the focus returns to where it was. |
| 8 | Open Find, search, step through matches. | The number of matches is announced; the current match is spoken. |
| 9 | Add and read a comment; resolve it. | The comment's author and text are spoken; the change is announced. |
| 10 | Use an option list (font selector, dropdown) with the arrow keys. | The options and the selected one are spoken; Enter chooses; Escape closes with the focus back on the button. |
| 11 | Export (HTML) and open the result with the reader. | Headings, lists, tables and image descriptions are in the exported file. |
| 12 | Turn on the display preferences (larger text, wide spacing, more contrast, reduce motion), and the operating system's high contrast. | Nothing is cut off or lost; the focus ring stays visible; no animation with reduced motion. |
| 13 | Zoom the browser to 200 % and 400 %. | The page reflows or scrolls without hiding a control. |
| 14 ★ | In View mode, open the landmark list and go to "Document outline"; activate an entry. | The outline is a navigation landmark; activating an entry puts the reader (and the canvas caret) on that heading and the heading is read. |
| 15 | In View mode, read the first paragraph with the reader's own cursor (arrow keys by line and word), then click another place on the canvas. | The reader's cursor and the canvas caret stay together in both directions (it does not jump back), and the block with the caret is marked as the current location. |
| 16 | Move to a link, to commented text and to a suggested change. | The link is a link and opens in a new tab; commented text is introduced ("Commented text:") and its comment (author, text, replies, resolved) is in the comments region; a suggested change says "Suggested addition by ..." or "removal". |
| 17 | Select text with Shift+arrows in the reader's own cursor in View mode. | The canvas shows the same selection and "Selected: ..." is announced. |

### Results

Copy this table into the release notes (or the release issue) for each release.

| Build | Date | Reader and browser | Steps passed | Failures (issue links) | Tester |
| --- | --- | --- | --- | --- | --- |
| | | NVDA + Firefox | | | |
| | | JAWS + Chrome | | | |
| | | VoiceOver + Safari | | | |

## 3. The test screen reader: `internal/screenreader`

`internal/screenreader` reads a page the way a screen reader's browse mode does, from the browser's
accessibility tree (the same tree NVDA, JAWS and VoiceOver consume), through chromedp. A test can
move by heading, landmark, link, button, table or form field, read the whole page, press Tab and hear
what gets focus, and watch live regions. Each stop is a phrase such as `Name, edit text, Ana, required`.
It also reports unlabelled controls and skipped heading levels. It finds missing names, wrong roles and
bad reading order early; it does not replace the manual run in section 2, since real screen readers
differ in what they say.

## 4. The document accessibility checker (`a11ycheck`)

This is not a test of the editor but a check *of the person's document*, in the showcase's Check tab
(roadmap items 61 and 62). `a11ycheck.Check(doc, Options{})` reads the saved document model and needs no
browser. Its rules:

| Rule | Severity | Finds | One-click fix |
| --- | --- | --- | --- |
| `image-alt` | error | a picture with no description that is not marked decorative | go and describe it (nothing is invented) |
| `heading-skip` | warning | a heading level that jumps (level 3 straight after level 1) | make it the next level |
| `heading-empty` | error | a heading with no text (also blank text at heading size) | make it a plain paragraph |
| `contrast` | warning | text under 4.5:1 (3:1 for large text) against its background or the page | return to the default colour |
| `table-header-missing` | error | a table of two rows or more whose first row has no text | none (write the column names) |
| `table-header-empty` | warning | some cells of the first row are empty (cells covered by a merge are not counted) | none |
| `list-fake` | warning | two or more paragraphs in a row that start with a typed bullet (`•`, `-`, `*`) or number (`1.`, `2)`) and are not list items | none (use the list button, then delete the markers) |

Limits, stated so nobody reads a pass as a proof. The model has no per-table header flag: the first row *is*
the header in every export (a `<th>` row in HTML, a header row in Word and PDF), so a header that is "not
marked" cannot occur and the rule looks for a header row with no text. The model has no text boxes or
floating objects, so reading order here means the heading outline, tables and lists; the order of floating
objects will need a rule when they exist. A single typed dash is left alone (it is often just punctuation).
The messages of the findings are English; the interface around them is translated.

### The export gate and the operator's minimum

The Check tab sets what exporting does about errors: nothing, warn, or block (with an explicit "allow the
next export anyway"). The person's choice is kept in the browser. The **operator** (whoever serves the page)
can set a minimum that the person cannot lower, in the served `index.html`:

```html
<meta name="gojiberry-min-export-gate" content="block">  <!-- or "warn"; empty or absent: no minimum -->
```

The gate in force is the stricter of the two (`GateMode.AtLeast`). Below the minimum the choices are
disabled and a note says an administrator set it. With `block` the override is gone as well
(`a11ycheck.OverrideAllowed`): an export with errors is refused until the errors are fixed. A value that is
not a gate (a typo) is read as `block`, so a mistake cannot open the gate. The setting is read from the page
the host delivers and deliberately not from the query string or local storage, which a link or the person
can set. It governs the page's own export paths; someone who edits the page in their own browser can still
change it, so a host that must enforce the rule against that needs to run `a11ycheck.Check` and
`GateMode.Decide` where the document is stored or sent. Tests: `TestOperatorMinimumOfTheGate`,
`TestCheckPanelGateFloorCannotBeLowered`, `TestOperatorMinimumExportGate` (block, warn, a typo).
