# opendocs features the showcase does not have

From opendocs' `docs/ROADMAP.md` and `docs/architecture/` (what it says it
shipped; not re-tested here) against what the OpenDocs showcase editor (moved out of this
repository)
and the library do today. **Missing** = nothing equivalent; **Partial** =
something smaller exists. See [opendocs-scan.md](opendocs-scan.md) for how the
two engines differ and what moves cleanly.

## Writing and formatting

| Feature (opendocs) | Here |
|---|---|
| Find and replace (panel, case, whole word, regex) | Have (`canvas` find/replace, `components.FindReplace`) |
| Spell check with suggestions, Tab autocomplete, Hunspell dictionaries (English embedded, others downloaded), custom words | Done (squiggles, suggestions and custom words; English embedded; other languages' Hunspell dictionaries load on demand from a dictionary server, with compounds, cross-product forms, TRY, REP and KEY: [known-limits-plan.md](known-limits-plan.md) B7) |
| Grammar check (LLM backend), language detection, translation through a backend | Have (`grammar`, `lang`, `translate` clients; a language panel) |
| Hyphenation (soft, on request, pattern based) | Have (`hyphen`, a layouter hook, a View toggle) |
| Special-characters picker | Have (`components` symbol picker) |
| Typography: size, family, colour, highlight; named styles and a style editor | Have |
| Custom list symbols, nested bullet lists, numbered lists, check lists | Partial (lists and check lists yes; custom symbols and nesting options no) |
| Horizontal rule and other text elements | Missing |
| Snippets (quotes, references, bibliography entries), templates | Missing |
| @mentions, free-text tags on documents | Missing |
| Formula cells and inserted formulas | Missing |
| Form fields | Missing |
| Index marks and an index; table of contents | Partial (table of contents yes, index no) |
| Footnotes, bookmarks, outline | Have |
| Links with hover preview | Have |
| Margins editor and margin guides | Partial (page margins are props; no editor) |
| Dictation and voice-over | Missing (gojiberry has `voiceserver`, no editor UI) |

## Layout and design

| Feature | Here |
|---|---|
| Multi-column text, up to 4, per block, column gap, 2+1 and 1+2 widths, column break | Have (per-block `ColumnConfig`, Edit-menu commands; column break not checked) |
| Linked text frames you can move, resize, reshape, explode | Missing |
| Images: insert, float, wrap by bounding box and by the picture's shape, clip, rotate, metadata, edit externally, paste or drop from the clipboard | Partial (insert, float, wrap by box, alpha; no shape wrap, rotate, metadata or paste) |
| Tables: word wrap, alignment, merge and split cells, per-cell styling, schemes, sort, formulas | Partial (grid, rows and columns, move, sort, wrap-around, merge and split, per-cell background, alignment and bold; no formulas, schemes) |
| Headers, footers, page numbers, watermark, background, page elements | Have |
| Background images (fixed, random) | Partial (a background picture per page with opacity and fit; no random picker) |
| Right-to-left and non-Latin scripts (CJK, Cyrillic, Arabic) | Missing (not tested; `dir` is set on the page only) |

## Documents, files and sharing

| Feature | Here |
|---|---|
| Export: HTML, RTF, EPUB, PDF, DOCX, braille (BRF), Markdown, text | Have (HTML, RTF, EPUB, PDF, DOCX, BRF, Markdown, text; DOCX cell colours not written) |
| Import: Word and Google Docs (docx), PDF text, import CLI and long-running import job | Partial (docx and PDF text through `transfer`, with input limits; no CLI or background job) |
| Document manager: list, open, duplicate, tags, trash and restore | Have (`saver`: list, open, save, delete, duplicate, trash and restore, tags) |
| Version history (save a version, list, restore) | Have (`saver` versions; a Save-tab list) |
| Storage adapters with quota, MinIO, local cache fallback on a failed save | Partial (`docstore` has several adapters and an ACL; no quota readout) |
| Share by link (view only or edit) and a People panel with server-enforced access | Partial (share link to a selection, roles and ACL on the hub; no People panel) |
| Signed documents | Missing |
| Review comments with replies, resolve; track changes | Partial (comments yes, track changes no) |
| Live collaboration | Have, and ahead (per-user undo, roles, WebSockets) |
| Login page, identity, auth gate | Partial (`auth`, `guard`; no login UI in the showcase) |

## Platform

| Feature | Here |
|---|---|
| Accessibility: a navigable accessible text surface, screen-reader announcements, braille display support | Partial (DOM mirror, live announcer, labelled canvas; no braille display, menus not roving-focus) |
| UI internationalization and language packs | Partial (catalog with es, fr, de for stock components, tabs, menus, status; no RTL layout) |
| Plugins and sandboxed scripting (Worker-isolated, several at once), example plugins | Missing |
| Build-time feature config to compile subsystems out | Missing |
| Keyboard shortcut reference and editor | Have |
| Context menus on the canvas | Have (`components` context menu; the showcase wires comments and links) |
| Opt-in telemetry for debugging, `?debug=1` state log | Partial (`#state` JSON, always on) |
| Electron desktop app, Docker and compose, Vercel config, CI with local pre-push hooks, manual-testing checklist, user guide | Missing (the repo has its own CI) |
| Performance and memory-leak tests | Missing |

## Status

Updated after the five "biggest gaps" below were built (export/import, find and text tools, tables and columns, document lifecycle, accessibility and i18n). The rows above show what is left. What still stands between the showcase and a V1 is in [known-limits-plan.md](known-limits-plan.md) and [opendocs-port-plan.md](opendocs-port-plan.md).

## The five gaps that were the biggest (built)

1. Export and import (DOCX, PDF, EPUB, HTML, RTF, BRF): users expect to leave and arrive with files.
2. Find and replace, spell check, hyphenation, language tools.
3. Tables beyond a grid (merge, per-cell styles) and per-block columns.
4. Document lifecycle: version history, trash, duplicate, tags.
5. Accessibility and i18n, which are far cheaper to build in early than to retrofit.
