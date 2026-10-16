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

1. **DOCX export of cell styles (S). Done** (background, alignment, bold; HTML too). Write `w:shd` (background) and `w:jc`
   (alignment) for `TableCellStyle`, matching what `docximport` already reads.
   Test: export → import round trip keeps styles; XML element order checked.
2. **Table end-to-end in the browser (M).** Merge, split, set background and
   align through the cell menu; undo; collaboration of a styled table between
   two clients. Lives with the other e2e tests (see C).
3. **Columns e2e (S). Done:** every column command, the caret put back in the text between menu uses.
4. **i18n breadth (M). Done** (roadmap 83: packs for de, fr, es, pt, he, ar and a test that every `T()` id is in the packs). Move the remaining hard-coded strings (canvas context
   menus, collab/presence, saver errors, spell/language panels) into the
   catalog; add a test that fails on any `T()` id missing from `en`/`es`/`fr`/`de`.
5. **Right-to-left (L). Done** (roadmap 64 and 83: paragraph direction, mirrored alignment and caret movement, mirrored UI, the Arabic and Hebrew packs, the browser tests that type RTL text). Bidi text shaping already exists per run; add
   paragraph direction, mirrored alignment and caret movement, mirrored UI
   (`dir=rtl` already set on `<html>`; audit CSS for `left/right`). Needs an
   Arabic/Hebrew pack and an e2e that types RTL text.
6. **Accessibility depth (M). Done** (roadmap 48, 50, 51, 59, 65, 66, 69: one keyboard model per widget, focus return, live regions for collaboration, high-contrast and reduced-motion settings, keys-only run of the whole editor, axe-core as a release gate). Roving focus in menus and toolbars, focus
   return after dialogs, `aria-live` for collab join/leave, high-contrast and
   reduced-motion CSS, a keyboard path for every pointer-only action.
7. **Hunspell breadth (M). Done.** Dictionaries beyond embedded en_US load on demand, with the morphology flags
   beyond the single-level subset. `spell.LoadHunspell` reads the whole of what another language needs: the character
   set the `.aff` names (`SET`: UTF-8 and the usual ISO 8859, KOI8 and Windows ones), cross-product prefix+suffix
   forms, `FLAG num`, compounding (`COMPOUNDFLAG`, `COMPOUNDBEGIN/MIDDLE/END`, `COMPOUNDMIN`, `COMPOUNDRULE` with `*`
   and `?`), `NEEDAFFIX`, `FORBIDDENWORD`, and for suggestions the language's letters (`TRY`), its common mistakes
   (`REP`, with `^` and `$` anchors) and its keyboard (`KEY`); a word limit stops a hostile dictionary. `spell.Remote`
   lists, resolves ("de-AT" to `de_AT`, else the first `de_*`) and loads a dictionary once from a server;
   `spell/dictserver` is that server over a directory of the dictionaries the operator hosts
   (`GOJIBERRY_DOCSTORE_DICTIONARIES=dir` mounts it at `/dict/`; ETag, a day's cache, files kept as `.gz` sent
   compressed). The showcase's Spelling tab offers the dictionaries the server has (`?dictapi=` or `/dict/` on its own
   origin) and loads the one chosen. The embedded English stays single-level (it keeps the browser's memory down).
   Not done: `ICONV`/`OCONV`, `BREAK`, `COMPOUNDSYLLABLE` and the other compound checks (`CHECKCOMPOUNDDUP`, ...),
   affixes on compound parts, `KEEPCASE`; and no dictionaries are shipped, because their licences are their own
   (LibreOffice's dictionaries are GPL, LGPL, MPL and others by language): the operator chooses what to host.
8. **WS role edge cases (S). Done:** read limit four times `MaxBody`, at most 1 MiB over it, `Config.TrustForwardedHost` for proxies.

9. **Review leftovers.** Done: catalog updates are conditional (`docstore.ConditionalAdapter`, `SaveIf` with a content digest; two processes on a SQLite, file, memory or HTTP store lose nothing, others write as before), the WebSocket read limit, the proxy `Host`,
   `expandReplacement` with context (`\b`, `\B`), whole-table-less cells and media
   order in DOCX export. Left: `pdftext` cannot stop a looping parser (run it out
   of process on a server); version operations list the whole store; the trash checkbox handler can drop one of two quick ticks.

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

## E. Damage detection and recovery (done, with limits)

Checksums, copy-on-write generations, a previous copy, `Verify`, fsync, Web Locks and a
warning when a store is filling up are done; what they do not cover is listed in
[document-components.md](document-components.md#damaged-files-and-previous-copies)
("Limits that remain").

## Order

1. B1, B3, B8 (small), then C step 1.
2. B2, B4, B6, B9 while porting e2e to chromedp.
3. Port-plan rows 1–5 (doc manager, login, templates, margins, feature config).
4. B5, B7 last (largest, least coupled).
