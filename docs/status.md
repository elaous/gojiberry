# Status of the roadmap

A one-page view of [roadmap.md](roadmap.md), read against `main` on 2026-10-14 (after #375). The roadmap keeps the long
history of each item (what was built, tests, decisions); this page says only **what is present**, **what is
missing**, and where the item stands. The item numbers are the roadmap's. When the two disagree, the roadmap's
entry for the item is the detail and this page is the summary: update both in the same change.

**Type:** feature (new capability), enhancement (better what exists), fix (a defect or a gap in what is promised),
chore (housekeeping, tooling, tests), security (keys, ceremonies, abuse limits).

**Status:**

- **done**: nothing left that the item set out to do.
- **partial**: usable and merged, with named parts missing.
- **open**: not started, or only designed.
- **blocked**: waiting for a person, a provider, a tool or a device that this project cannot supply (named).
- **inherent**: a limit of the design, documented in the [limits register](limits-register.md); the work is to
  reduce the exposure, not to remove it.
- **closed**: decided not to build, with the reason in the roadmap.

## What is blocked on a person

| What | Needs | Items |
| --- | --- | --- |
| Run the manual screen-reader script on NVDA, JAWS and VoiceOver (steps 14 to 21 of [accessibility-testing.md](accessibility-testing.md)) | A person with those readers | 49, 60, 69, 79 |
| E-mail verification through a real provider (SMTP or an API) | The operator picks the provider and gives credentials | 41 |
| A PAC run on the PDFs (veraPDF, the PDF/UA-1 reference validator, passes: `TestVeraPDFAcceptsTheTaggedFiles`; pdfa11y passes in `make pdfa11y-test`) | PAC is a Windows program | 63 |
| Test the Redis-style shared stores, S3-compatible services that ignore `If-Match` on delete, nginx and Caddy recipes | Live endpoints and daemons | 3, 9, 82, 89 |
| Test atypical-speech support with people who have atypical speech | Volunteers and consent | 56 |
| An outside security audit before anyone relies on the keys for secrets | A reviewer | 89 |
| Read the pt, ar and he interface packs (model-drafted) as a native speaker | A native speaker of each | 83 |
| Choose and license a dyslexia-friendly font (an asset), and judge the pace and double echo of speech while typing | The owner; people who use screen readers | 50, 49 |
| Review "English, plain words" with the readers it is for, and extend it to the rest of the page | A plain-language editor or readers who need it | 65 |
| Delete the old remote branches `ccr-f005be71-wip-*`, `-w57-telemetry`, `-w75-sharing` | The repository owner | chore |

## Product: documents and accessibility (items 48 to 84)

| # | Item | Type | Status | Present | Missing |
| --- | --- | --- | --- | --- | --- |
| 48 | ARIA across the canvas editor and components | enhancement | done | roles, names, live regions, axe-core in the browser tests over 21 tabs with every rule on, heading levels in the panels, fixes from four slices; one keyboard model per kind of widget (list, grid, menu, tabs, combobox; the colour and date pickers are the browser's) | none |
| 49 | Speech while typing | feature | blocked | polite live region for words, characters, line and formatting; off by default | judgement of pace and double echo by real readers (blocked) |
| 50 | Accessibility configs | enhancement | blocked | reduced motion, high contrast (with a strong caret and selection on the canvas), font size and line spacing, the canvas text following the text size, key remapping kept between visits, colour-blind safe colours for other people\'s cursors | dyslexia-friendly font (needs a font asset: blocked on choosing and licensing one) |
| 51 | Preference menu | feature | done | one list of settings drives both the Display preferences menu and a Preferences tab with search and groups; language, theme and an editor behaviour in the same schema; import and export as a file; Privacy entry | none (a theme that also dresses the document sheets would be new work) |
| 52 | Central config store (preferences, styles, templates, fonts) | feature | done | `userconfig` store and client (local copy, offline queue, merge), fonts as assets with limits, shareable bundles; in the showcase: preferences that follow the person (opt-in), a share dialog for themes, My fonts (licence note, font list, sync) | none named; the showcase has no templates or style sets to share (bundles for them are for hosts) |
| 56 | Atypical speech (Project Euphonia style) | feature | blocked | [euphonia-port.md](euphonia-port.md); `speechcoach` package; prompt and language bias in `cmd/voiceserver`; showcase Speech tab (record phrases, download, delete, dictate through a recogniser address; recordings and corrections kept sealed between visits when asked; learning from corrections with fixes offered on the next dictation; the optional research contribution shown, confirmed and withdrawable); `speechmodel` (contract, digest-pinned loader, federated averaging, stub and http backends; [speech-model.md](speech-model.md)) | an ONNX backend and a trained model to run in it (nothing to build or test against here); trials with people who have atypical speech |
| 59 | Keyboard-only completeness | enhancement | done | dropdowns, colour picker and pickers operable; skip links; one keyboard model per widget; every menu entry reached by keys; a clean Tab cycle; keys-only run of the whole editor; [keyboard.md](keyboard.md) inventory; cheat sheet and remapping (Keys tab, kept) | none |
| 60 | Accessible text mirror of the canvas | feature | blocked | mirror with links, outline landmark, comments, tables, lists; caret mapping both ways by the editor's stream (emoji, footnote marks), block updates in place (browser-tested) | the manual run with NVDA, JAWS and VoiceOver (a person) |
| 61 | Alt text and structure checks | feature | done | prompt on insert, description editor, checker rules, reading order of typed lists, tables and floating objects, heading levels 1 to 6 in every format, a draft description made on the device (off by default, never automatic) | none (a captioning model for the draft is the operator's choice) |
| 62 | Accessibility checker panel | feature | done | panel with jump, one-click fixes (including typed bullets and numbers to a list), export gate (page side), `docstore/exportgate` server floor and `DecideSealed` for the host that holds an encrypted store's keys, floating pictures and tables checked | none (the server cannot judge ciphertext, and a column's name is the person's to write) |
| 63 | Accessible exports | feature | done | tagged PDF (structure tree, `/Lang`, links, nested lists, run languages), semantic HTML with a main landmark, checked with axe in the browser tests, DOCX metadata, fidelity report | table-cell language spans and links in PDF, veraPDF PDF/UA-1 passes on the test documents | none (code blocks are drawn without links on purpose; a PAC run needs Windows and a person) |
| 64 | Language and direction | feature | blocked | per-run language, document language (set by hand or from detection), IME composition inserted once with the preedit drawn at the caret and the candidate window moved there; right-to-left and bidirectional layout (levels, visual order, caret, click, selection, arrow keys), `dir=auto` in the mirror; vertical text (columns right to left, upright ideographs, saved and exported in HTML, EPUB, Word and OpenDocument) | a manual run with real IMEs (a person) |
| 65 | Cognitive and motor options | feature | done | distraction-free mode, forgiving targets, text size up to 200 %, a longer undo history (1000 steps), dwell click and single-switch scanning (one flat scan), "English, plain words" for the hardest messages (sign-in, keys, encryption, share links) | none (the plain-language wording is the model's draft: a review and the rest of the page need a person, see above) |
| 66 | Collaboration accessibility | feature | done | joins, leaves, merged and refused changes, remote comments announced; "Where is X" as a per-person setting; colour-blind safe cursor colours, refused changes announced (browser test), changed-under-you sentence naming who | none (screen-reader run is item 69) |
| 67 | Read aloud | feature | done | selection or document aloud with pause, speed, skip; the sentence and the word marked in the document and the bar; voices on the device by default, network voices only when allowed, the bar says which is speaking | none |
| 68 | Captions and transcripts | feature | done | media model, caption cues (parse, build, edit), clip card in the editor, insert form that requires the text, caption editor, checker rules, HTML export and import of the clip, text in seven languages | none named; no player in the canvas and no media part in Word, ODT or PDF are inherent ([limits register](limits-register.md) 16) |
| 69 | Accessibility as a release gate | chore | blocked | axe checks in the browser tests, test screen reader on the accessibility tree, manual script, five more examples under the gate (style editor, components gallery, editor app, passkey, people keys) | first manual run (blocked) |
| 70 | Version history UI | feature | done | timeline, names, diff, restore, access follows the document's list, snapshots as sealed as the document | none (names and authors in `meta.json` are plain metadata, as the document's own name is) |
| 71 | Comments, suggestions, tracked changes | feature | done | threads, resolve and reopen, suggestions with accept and reject, in the mirror, tracked changes in DOCX (export and import), two people proposing converge in the model and in two real browsers, mentions by handle that never write down an address of someone without access | none named |
| 72 | Import and export fidelity | feature | done | DOCX, Markdown, RTF, EPUB, HTML, ODT and PDF exporters; DOCX, HTML and ODT importers; fidelity report per format in the Files tab, and the import report kept with the document; a corpus written by LibreOffice read back; DOCX, ODT and RTF opened in LibreOffice | none |
| 73 | Search and replace | feature | done | `docsearch` (sealed index, kept incrementally as sealed changes after a base), `PlanReplace`, search across documents in the showcase | none |
| 74 | Offline-first with conflict visibility | feature | done | sync state, queued changes kept (`client.Options.Queue`), e2e with the browser offline, "While you were offline" list of what others changed, a store copy saved elsewhere is kept as a version and compared | none |
| 75 | Sharing UX | feature | done | share links with role, expiry, revoke, who-can-see list, per-person expiry, open from the address, plain wording, the People tab on the real server (list, people, links) | none (comment-only is enforced on live shared documents; a stored one cannot tell a comment from an edit, and the tab says so) |
| 76 | Key and recovery UX | feature | done | recovery sheet, test-my-recovery-key, one-key nudge, device list with last use, guided re-key, plain limit wording | none named; the limit itself is inherent |
| 77 | Templates, styles, design tokens | feature | done | named styles, tokens, themed HTML export, a theme editor with live contrast checks (the theme goes with the document, into HTML, EPUB, Word, OpenDocument, RTF and PDF) | none |
| 78 | Equations, diagrams, code, footnotes | feature | done | MathML with text alternative (and Office Math in Word, read back by the import), code language, diagram descriptions, footnote links both ways in HTML, Word, RTF, Markdown and PDF | none |
| 79 | Autocomplete word picker | feature | done | ranked suggestion list as an ARIA listbox, ghost text drawn by the canvas in the run's font and language (a tag under the caret mid-line), de/fr/es/pt/ar/he word lists | none (the lists hold common words only, by design; the screen-reader run is item 69) |
| 80 | Print layout | feature | done | page model, headers and footers, page numbers, different first page and odd/even pages, any sheet size (custom paper), a real clickable table of contents (page numbers, two looks, depth) that the exports write where it is, the outline as the navigation panel, widow control and kept table rows in every paged format, the same running header and footer on the screen sheets | small: the even-page rule assumes left-to-right page order |
| 81 | Admin side | feature | done | audit log written by the server (documents, access, keys, rate limits), readiness route, backup and restore with dated backups and rotation, `/admin/usage` and history page (series kept across restarts), observability core with PostHog and OTLP sinks, the other servers' own work, builtin totals kept across restarts | none (nothing else changes a store: the other servers keep no documents; a key rotation is logged as an enrollment, and `config.changed` has no source yet) |
| 82 | Performance and size | enhancement | blocked | wasm size budgets per program checked in CI, stripped build, precompressed wasm, `wasmdist`, service worker that keeps the app for repeat visits and offline start, the file formats in a second wasm loaded on first use, low-memory mode (measured: less than half the heap on a long document), typing in a long document 34 times faster (one layout for the reading copy, one style lookup per run), the worker split by format decided against with numbers, the nginx and Caddy recipes run against the real servers (and a Caddy defect fixed) | the Docker image (no Docker daemon here), nginx's `brotli_static` (needs the module), and the low-memory mode and typing speed on a real small phone |
| 83 | i18n depth | enhancement | blocked | de, fr, es packs, a test that every message id is in a pack, plurals, dates, time zones as offsets, pt/ar/he packs fetched on demand (model-drafted), page turned around for RTL | a native review of the pt, ar and he packs; ids built at run time |
| 84 | Lint the examples | chore | done | no path exclusions in `.golangci.yml` | none |

## Telemetry and operator observability (57, 81)

| Part | Type | Status | Present | Missing |
| --- | --- | --- | --- | --- |
| 57 Usage counts from browsers (no personal data) | feature | done | `telemetry` package, opt-in, allowlist, sampling, local log, persistent totals, noise, Privacy menu, [telemetry.md](telemetry.md); crash codes as a separate opt-in (two fixed words, kept in the browser until the next visit); several server processes on one store add their counts (conditional writes) | none |
| 81 Operator metrics and traces (`observe`) | feature | done | off by default; closed allowlists; builtin sink and `GET /admin/observe`; **PostHog** and **OpenTelemetry (OTLP/HTTP JSON)** sinks; builtin totals kept across restarts with `GOJIBERRY_OBS_STATE`; [observability.md](observability.md) | none (font downloads, the Google font proxy and hub presence have request counts only) |

## Server, keys and security

| # | Item | Type | Status | Present | Missing |
| --- | --- | --- | --- | --- | --- |
| Passkey backlog 1, 2 | Shared rate limiter and challenge store | enhancement | done | through the store; sessions shared too | Redis behind the same interfaces (decided not to build a client) |
| Passkey backlog 3 | Account stores with a conditional write | enhancement | done | `AdapterStore` over S3, Mongo and Postgres; `SQLStore` (a SQL table, any `database/sql` driver) | |
| Passkey backlog 4 | Per-account sign-in budget | enhancement | done | `Config.AccountLimiter`, shared through the store | |
| Passkey backlog 5 | Shared lock for stores without conditional writes | enhancement | done | `KeyLocker` and `LockedAdapter` make a plain store conditional | |
| Limits 1 to 11, 13 to 19, 22 to 26 | Review findings (WatchLog anchoring, RepairAll, signer tooling, SSH tags, challenge store, double first passkey, S3 `DeleteIf`, expiry on digest, Mongo CI, pseudonyms, tombstones, byte budgets, history rewrite) | fix | done | see roadmap entries | none |
| Limits 12 | Log witnesses (co-signed heads) | feature | done | witness run (`Client.Witness`), cosignature routes, quorum check in `WatchLog` (`ErrSplitView`) | none in code; independent parties to run witnesses are the operator's to find |
| Limits 20 | Showcase originals on disk | chore | closed | in memory | not worth a second copy of localStorage |
| Limits 21 | Directory publish and pick UI in the showcase | feature | done | four slices merged | none |
| 27 to 40 | Lint, clock note, sweep order, RepairAll, blob backend named, log read budgets, accept rotated owner key, encrypt errors in words, unmapped addresses, store-mode wording | fix | done | each in the roadmap | none |
| 41 | Same-e-mail passkey registration makes two accounts | security | blocked | never merge on a label (rule and test) | verification flow and notice need a provider and credentials |
| 42 | Revoked signer keeps writing | security | partial | frame v4, `Sharing.RevokeSigner`, design doc | manifest of the old key's frames; removal-dialog wording |
| 43 | Link replay past the 32-link window | security | inherent | documented | none |
| 44 | SSH-only store with no signed file undetected | security | inherent | first-unlock owner-list fingerprint | digest in the key log or a second store |
| 45 | Unenrolled-address numbering by the directory key holder | security | partial | `RequireOwner`, notice, WatchLog reports | none beyond enrolling |
| 53 | Session store read per request | security | inherent | miss cache, miss budget, optional bounded cache | a positive cache is deliberately not default |
| 54 | Login routes without rate limit | fix | done | limiter | none |
| 55 | Public offer sessions shared through the store | enhancement | partial | sessions and counts shared | see 86 |
| 58 | Leftovers of the #99 and #100 reviews | chore | done | seven of seven, the last being per-/64 IPv6 budgets for the login and passkey routes | none |
| 85 | Findings of the outside review (#90 to #123) | fix | done | all fixed | none |
| 86 | Public offer report counters per process | enhancement | partial | priority allowance shared (`store`, `store-required`) | the other caps |
| 87 | Owner key rotate and reset in the showcase | feature | done | rotate, reset, watch names a reset | clock moved by the page, not the tests |
| 88 | API dumps regenerated; `make check` fails when stale | chore | done | yes | none |
| 89 | Security review of keys and directory | security | partial | challenge-oracle fixed; owner key non-extractable in IndexedDB; fingerprints for log keys | outside audit (blocked); S3-compatible `If-Match` on delete untested; PRF/age/SSH/frame format unreviewed by others |

## Earlier plan ("Next" and "Later" in the roadmap)

| Item | Type | Status | Present | Missing |
| --- | --- | --- | --- | --- |
| Migrate | feature | partial | from other editors | skipping what an earlier run brought; Google Docs comments and history |
| Button icons | enhancement | done | `components.Material`; `components.IconToolbar` (toolbar pattern: one tab stop, arrow keys, toggles with `aria-pressed`, tooltips with the shortcut) and `CommandToolbarItems` (name, icon and shortcut from the registry the Edit menu uses); the showcase's formatting toolbar follows the caret | none |
| Download submenu | enhancement | done | grouped, `.open` | none |
| `.opendoc` packs | feature | done | `docfile/pack`, `cmd/opendoc` | none |
| Encryption and passkeys | feature | partial | SSH keys, passkeys, envelope encryption, directory, sharing, recovery | see the security table |
| Public docstore | feature | done (server) | anonymous offer with expiry and abuse controls | see 55, 86 |
| Console mode | feature | partial | `console`, `cmd/docsh` | the editor panel |
| Terminal demo | feature | done | `examples/terminal-demo` | none |
| Backends with conditional saves | feature | done | PostgreSQL, MongoDB, S3 | blob backend cannot, by design |
| Known limits plan tail | enhancement | open | partly in 64 and 83 | RTL, Hunspell dictionaries on demand |

## Counts

Product table (items 48 to 84, 32 rows): 24 done, 0 partial, 8 blocked (49, 50, 56, 60, 64, 69, 82, 83: all that is left is a person's work) ("done with leftovers" is not a status: a part that is left makes an item partial, and a limit that cannot be removed makes it inherent). None is untouched. The server and
security table: the review findings are done; still open are the per-account sign-in budget and the shared lock
(passkey backlog 4 and 5) and log witnesses (limits 12); 41 is blocked on a provider.

## Suggested order for the next work

1. 57: scrubbed crash reports (a separate opt-in) and one counting process per store.
2. Passkey backlog 3 (a SQL-table account store), 55 and 86 (the other caps shared through the store).
3. Migrate: skipping what an earlier run brought, Google Docs comments and history.
4. Console mode: the editor panel.
5. 42, 45, 89 and Encryption and passkeys: what is left is mostly an outside review or a person's work; the code parts first.

Closed since the previous version of this page: 80 (print layout: odd/even pages, a different first page, any sheet size, a clickable table of contents written where it is, kept table rows), 74, 75, 77, 73 and 66 (their gaps), the audit log written by the server with scheduled backups (81), the builtin observability totals kept across restarts and the other servers' own work reported (81), pt/ar/he completion lists (79), 58 (IPv6 /64 budgets), the table-row widow control, the sheet size
restore, contents in RTF, footnote links in RTF, the long undo history, heading levels in the panels, all CSS
colour names, and the file worker without `net/http` (11.0 to 9.5 MB), typing in long documents (34 times faster), the formatting toolbar of icon buttons.
