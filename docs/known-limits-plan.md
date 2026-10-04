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

## C. Test tooling: follow opendocs (chromedp)

opendocs drives the browser with **chromedp** under a `cdp` build tag
(`editor/cdptestkit_test.go`, `*_cdp_test.go`), not Node/Playwright. Gojiberry's
`internal/e2etest` currently shells out to Playwright with JS scripts.

- **Step 1 (M).** Add a chromedp-based `internal/e2etest` driver exposing the
  same helpers (`Run`, page, `#state`, errors). Keep the JS scripts running
  until each is ported.
- **Step 2 (L).** Port the scripts one at a time (verify, share, find, spell,
  lang, columns, lifecycle, a11y), deleting the JS and the Node dependency at
  the end. Reuse opendocs' helpers (`typeTextCDP`, `canvasBoxCDP`, `docStateCDP`)
  where they are generic.
- Risk: `go.mod` gains chromedp/cdproto; both are already vetted in opendocs.

## D. Still to do from earlier

1. **Path-style links (M).** `…/{doc}` for the document, optional settings in
   the query string (`?mode=view&lang=es`). Server routes `GET /{doc}` and the
   WS/SSE endpoints under `/{doc}/…`; `share` builds and parses them; the
   showcase reads the doc id from `location.pathname`. Old `?doc=` links keep
   working through a redirect for one release.
2. **opendocs port plan (S, writing).** `docs/opendocs-port-plan.md`: for each
   clean opendocs package, port as a component (which gojiberry package, what
   changes, what tests come with it), cleanest first; the rest listed as
   "rewrite" or "drop". Uses `opendocs-scan.md` and `opendocs-missing-features.md`.
3. **Refresh the two opendocs docs (S).** Mark what now exists (columns, export/
   import, find, spell, tables, lifecycle, a11y/i18n) and what remains.

## Order

1. Merge PR #48 after review (this step).
2. D3, D2 (documents), then D1 (path links) so share links are stable early.
3. B1, B3, B8 (small), then C step 1.
4. B2, B4, B6 while porting e2e to chromedp.
5. B5, B7 last (largest, least coupled).
