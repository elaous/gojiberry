# Plan: known limits, fixes and next features

Status after PR #48 (rename, stable object keys, features 1–5). Each item has a
size (S ≤ half a day, M ≈ 1–2 days, L > 2 days), what "done" means, and the
order it should land in.

## A. Naming

| Item | Size | Done when |
|---|---|---|
| Rename finished inside gojiberry (`xango`, `xgc`, `.xg`, `vscode-xango`) | S | No `gox` outside the README origin note; empty `gox/` dirs removed. **Done in this PR.** |
| Own module `github.com/elaous/xango` | — | Dropped: Xangô stays in this repository (import path `github.com/elaous/gojiberry/xango`). |
| Required CI check name | S | Branch protection uses "vscode-xango grammar suite". Needs a repo admin. |

## B. Known limits from PR #48

1. **DOCX export of cell styles (S).** Write `w:shd` (background) and `w:jc`
   (alignment) for `TableCellStyle`, matching what `docximport` already reads.
   Test: export → import round trip keeps styles; XML element order checked.
2. **Table end-to-end in the browser (M).** Merge, split, set background and
   align through the cell menu; undo; collaboration of a styled table between
   two clients. Lives with the other e2e tests (see C).
3. **Columns e2e (S).** Cover every column command, not one (the second menu
   command failed because the menu steals the caret; drive them from the
   keyboard shortcuts / `RunCommand` instead of the menu).
4. **i18n breadth (M).** Move the remaining hard-coded strings (canvas context
   menus, collab/presence, saver errors, spell/language panels) into the
   catalog; add a test that fails on any `T()` id missing from `en`/`es`/`fr`/`de`.
5. **Right-to-left (L).** Bidi text shaping already exists per run; add
   paragraph direction, mirrored alignment and caret movement, mirrored UI
   (`dir=rtl` already set on `<html>`; audit CSS for `left/right`). Needs an
   Arabic/Hebrew pack and an e2e that types RTL text.
6. **Accessibility depth (M).** Roving focus in menus and toolbars, focus
   return after dialogs, `aria-live` for collab join/leave, high-contrast and
   reduced-motion CSS, a keyboard path for every pointer-only action.
7. **Hunspell breadth (M).** Dictionaries beyond embedded en_US: load `.aff`/
   `.dic` on demand (fontserver-style), with morphology flags beyond the
   supported subset (compound rules, `REP`, `KEY`).
8. **WS role edge cases (S).** The remaining items under "Known limits" in
   `multiuser-design.md`.

9. **Review leftovers (S each).** WebSocket read limit is
   `4 * MaxBody` before the 413 check; `cookieSocketAllowed` ignores scheme and
   needs `CookieOrigins` behind a Host-rewriting proxy; `expandReplacement`
   re-runs the regexp on the isolated match (fails for `\b`/`\B` patterns);
   DOCX export drops a table whose cell count mismatches its grid and writes
   media in map order; `Duplicate`/`Restore`/`SetTags` are check-then-write
   (now serialised per process by the catalog lock; across processes they need a conditional save);
   `pdftext` cannot stop a
   looping parser (run it out of process on a server).

## C. Test tooling: chromedp (done)

opendocs drives the browser with **chromedp** (`cdptestkit_test.go`); gojiberry now
does too. `internal/e2etest` is a Go driver over chromedp (`Site` builds and serves
a wasm example, `Tab` is a browser on it: selectors with `:has-text`/`:text-is`,
real mouse and keyboard events, waits on the page's `#state`, dialogs, downloads,
file choosers, routes). Every Playwright script was ported to a Go test and the
Node dependency is gone; tests skip when no Chromium is found. Left to do: run
the e2e tests in CI (it has Chrome) once they are known stable there.

## D. Still to do from earlier

1. **Path-style links. Done.** A shared page is `…/d/{doc}`; hub/transport/
   selection stay in the query; `?doc=` is still read. `share.SplitPath`,
   `share.DocPath`; the hub already used `/docs/{id}/…`.
2. **opendocs port plan. Done:** [opendocs-port-plan.md](opendocs-port-plan.md).
3. **Refresh the two opendocs docs. Done.**

## Order

1. B1, B3, B8 (small), then C step 1.
2. B2, B4, B6, B9 while porting e2e to chromedp.
3. Port-plan rows 1–5 (doc manager, login, templates, margins, feature config).
4. B5, B7 last (largest, least coupled).
