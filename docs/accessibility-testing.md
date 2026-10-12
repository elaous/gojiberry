# Accessibility testing

Two parts: a gate that runs on every change, and a script a person runs before every release. The
gate finds what a tool can; only the script finds what it cannot (whether the page can be *used* with
a screen reader, a keyboard, a magnifier).

## 1. The automated gate: `make a11y-test`

Run it on this machine with `make a11y-test` (or `make ci-local STEPS=a11y-test`, which also writes a short
report to `local-ci-report.txt`; see [testing-workflow.md](testing-workflow.md#running-the-ci-steps-locally)). The hosted
workflow is manual and not needed for it. It needs a Chromium or Chrome and **fails without one**
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

### Results

Copy this table into the release notes (or the release issue) for each release.

| Build | Date | Reader and browser | Steps passed | Failures (issue links) | Tester |
| --- | --- | --- | --- | --- | --- |
| | | NVDA + Firefox | | | |
| | | JAWS + Chrome | | | |
| | | VoiceOver + Safari | | | |
