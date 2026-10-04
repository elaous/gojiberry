# opendocs features the showcase does not have

From opendocs' `docs/ROADMAP.md` and `docs/architecture/` (what it says it
shipped; not re-tested here) against what `examples/canvas-editor-showcase`
and the library do today. **Missing** = nothing equivalent; **Partial** =
something smaller exists. See [opendocs-scan.md](opendocs-scan.md) for how the
two engines differ and what moves cleanly.

## Writing and formatting

| Feature (opendocs) | Here |
|---|---|
| Find and replace (panel, case, whole word, regex) | Missing |
| Spell check with suggestions, Tab autocomplete, Hunspell dictionaries (English embedded, others downloaded), custom words | Missing (only word completion from the text and a vocabulary list) |
| Grammar check (LLM backend), language detection, translation through a backend | Missing |
| Hyphenation (soft, on request, pattern based) | Missing |
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
| Multi-column text, up to 4, per block, column gap, 2+1 and 1+2 widths, column break | Partial (equal columns for the whole flow; no per-block counts, gaps, widths or column break) |
| Linked text frames you can move, resize, reshape, explode | Missing |
| Images: insert, float, wrap by bounding box and by the picture's shape, clip, rotate, metadata, edit externally, paste or drop from the clipboard | Partial (insert, float, wrap by box, alpha; no shape wrap, rotate, metadata or paste) |
| Tables: word wrap, alignment, merge and split cells, per-cell styling, schemes, sort, formulas | Partial (grid, rows and columns, move, sort, wrap-around; no merge, per-cell styles or formulas) |
| Headers, footers, page numbers, watermark, background, page elements | Have |
| Background images (fixed, random) | Partial (a background picture per page with opacity and fit; no random picker) |
| Right-to-left and non-Latin scripts (CJK, Cyrillic, Arabic) | Missing (not tested) |

## Documents, files and sharing

| Feature | Here |
|---|---|
| Export: HTML, RTF, EPUB, PDF, DOCX, braille (BRF), Markdown, text | Partial (Markdown, text) |
| Import: Word and Google Docs (docx), PDF text, import CLI and long-running import job | Missing |
| Document manager: list, open, duplicate, tags, trash and restore | Partial (`saver`: list, open, save, delete; no trash, duplicate or tags) |
| Version history (save a version, list, restore) | Missing |
| Storage adapters with quota, MinIO, local cache fallback on a failed save | Partial (`docstore` has several adapters and an ACL; no quota readout) |
| Share by link (view only or edit) and a People panel with server-enforced access | Partial (share link to a selection, roles and ACL on the hub; no People panel) |
| Signed documents | Missing |
| Review comments with replies, resolve; track changes | Partial (comments yes, track changes no) |
| Live collaboration | Have, and ahead (per-user undo, roles, WebSockets) |
| Login page, identity, auth gate | Partial (`auth`, `guard`; no login UI in the showcase) |

## Platform

| Feature | Here |
|---|---|
| Accessibility: a navigable accessible text surface, screen-reader announcements, braille display support | Missing |
| UI internationalization and language packs | Missing |
| Plugins and sandboxed scripting (Worker-isolated, several at once), example plugins | Missing |
| Build-time feature config to compile subsystems out | Missing |
| Keyboard shortcut reference and editor | Have |
| Context menus on the canvas | Have (`components` context menu; the showcase wires comments and links) |
| Opt-in telemetry for debugging, `?debug=1` state log | Partial (`#state` JSON, always on) |
| Electron desktop app, Docker and compose, Vercel config, CI with local pre-push hooks, manual-testing checklist, user guide | Missing (the repo has its own CI) |
| Performance and memory-leak tests | Missing |

## Biggest gaps for an opendocs V1

1. Export and import (DOCX, PDF, EPUB, HTML, RTF, BRF): users expect to leave and arrive with files.
2. Find and replace, spell check, hyphenation, language tools.
3. Tables beyond a grid (merge, per-cell styles) and per-block columns.
4. Document lifecycle: version history, trash, duplicate, tags.
5. Accessibility and i18n, which are far cheaper to build in early than to retrofit.
