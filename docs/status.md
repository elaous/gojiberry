# Status of the roadmap

A one-page view of [roadmap.md](roadmap.md), read against `main` on 2026-10-13 (after #331). The roadmap keeps the long
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
| A veraPDF or PAC run on the PDFs (pdfa11y, a Go checker, now passes in `make pdfa11y-test`) | veraPDF is Java; PAC is Windows | 63 |
| Test the Redis-style shared stores, S3-compatible services that ignore `If-Match` on delete, nginx and Caddy recipes | Live endpoints and daemons | 3, 9, 82, 89 |
| Test atypical-speech support with people who have atypical speech | Volunteers and consent | 56 |
| An outside security audit before anyone relies on the keys for secrets | A reviewer | 89 |
| Delete the old remote branches `ccr-f005be71-wip-*`, `-w57-telemetry`, `-w75-sharing` | The repository owner | chore |

## Product: documents and accessibility (items 48 to 84)

| # | Item | Type | Status | Present | Missing |
| --- | --- | --- | --- | --- | --- |
| 48 | ARIA across the canvas editor and components | enhancement | partial | roles, names, live regions, axe-core in the browser tests over 21 tabs with every rule on, heading levels in the panels, fixes from four slices | one arrow-key model for every picker |
| 49 | Speech while typing | feature | partial | polite live region for words, characters, line and formatting; off by default | judgement of pace and double echo by real readers (blocked) |
| 50 | Accessibility configs | enhancement | partial | reduced motion, high contrast (with a strong caret and selection on the canvas), font size and line spacing, the canvas text following the text size, key remapping kept between visits, colour-blind safe colours for other people\'s cursors | dyslexia-friendly font (needs a font asset: blocked on choosing and licensing one) |
| 51 | Preference menu | feature | done | one list of settings drives both the Display preferences menu and a Preferences tab with search and groups; language, theme and an editor behaviour in the same schema; import and export as a file; Privacy entry | none (a theme that also dresses the document sheets would be new work) |
| 52 | Central config store (preferences, styles, templates, fonts) | feature | partial | `userconfig` store and client (local copy, offline queue, merge), fonts as assets with limits, shareable bundles | wiring the client to the showcase's preferences and a share dialog; loading a stored font into the editor |
| 56 | Atypical speech (Project Euphonia style) | feature | partial | [euphonia-port.md](euphonia-port.md); `speechcoach` package; prompt and language bias in `cmd/voiceserver`; showcase Speech tab (record phrases, download, delete, dictate through a recogniser address); `speechmodel` (model/adapter contract, digest-pinned loader, federated averaging with clip and noise, stub and http backends; [speech-model.md](speech-model.md)) | keeping recordings between visits; learning from corrections and the contribution flow in the page; an ONNX backend and the model repository; trials with users (blocked) |
| 59 | Keyboard-only completeness | enhancement | partial | dropdowns, colour picker and pickers operable; skip links to the document and the side panel; no focus traps found | inventory of every action; shortcut cheat sheet; pointer-free end-to-end run |
| 60 | Accessible text mirror of the canvas | feature | partial | mirror with links, outline landmark, comments, tables, lists, caret mapping both ways (browser-tested) | block-level diff of its own; caret mapping counts runes; manual reader run (blocked) |
| 61 | Alt text and structure checks | feature | partial | prompt on insert, description editor, checker rules, reading order of typed lists and tables | on-device suggestions; reading order of floating objects; heading levels beyond 3 |
| 62 | Accessibility checker panel | feature | partial | panel with jump, one-click fixes (including typed bullets and numbers to a list), export gate (page side) and `docstore/exportgate` server floor | server floor for an encrypted store needs the host's decrypting job; floating objects; no fix for the table-header rules (only the person can name a column) |
| 63 | Accessible exports | feature | partial | tagged PDF (structure tree, `/Lang`, links, nested lists, run languages), semantic HTML with a main landmark, checked with axe in the browser tests, DOCX metadata, fidelity report | table-cell language spans and links in PDF, code links; a veraPDF / PAC run (pdfa11y passes; see `make pdfa11y-test`) |
| 64 | Language and direction | feature | partial | per-run language, document language (set by hand or from detection), RTL and direction handling, IME composition inserted once at its end | bidi/vertical layout checks; the preedit drawn at the caret and the candidate window placed there; a manual run with real IMEs (blocked) |
| 65 | Cognitive and motor options | feature | partial | distraction-free mode, forgiving targets, larger size step, a longer undo history (1000 steps) in the preferences | size scale beyond one step, switch scanning and dwell click, plain-language label set |
| 66 | Collaboration accessibility | feature | partial | joins, leaves, merged and refused changes, remote comments announced; "Where is X" as a per-person setting; colour-blind safe cursor colours | true conflict wording; browser test of refused count |
| 67 | Read aloud | feature | done (with leftovers) | selection or document aloud with pause, speed, highlighted sentence in the bar | sentence highlight on the canvas, word-level highlight, choosing a network voice on purpose |
| 68 | Captions and transcripts | feature | partial | media model, caption/transcript fields, checker rules, exports (HTML embeds the clip with a caption track and the transcript; the other formats write the link and the transcript) | insert-clip UI in the showcase and a caption track editor (the canvas has no media object, so a clip opens as text) |
| 69 | Accessibility as a release gate | chore | partial | axe checks in the browser tests, test screen reader on the accessibility tree, manual script, five more examples under the gate (style editor, components gallery, editor app, passkey, people keys) | first manual run (blocked) |
| 70 | Version history UI | feature | partial | timeline, names, diff, restore | different author per person in a live session; permission and encryption checks |
| 71 | Comments, suggestions, tracked changes | feature | partial | threads, resolve and reopen, suggestions with accept and reject, in the mirror | mentions that do not leak addresses; tracked changes in DOCX; two people proposing in one place |
| 72 | Import and export fidelity | feature | partial | DOCX, Markdown, RTF, EPUB, HTML and PDF exporters; DOCX, HTML and ODT importers; ODT exporter (with its contents index); fidelity report per format shown in the Files tab | a corpus of real documents; the report kept with the document; the ODT files opened in LibreOffice |
| 73 | Search and replace | feature | partial | `docsearch` (sealed index), `PlanReplace`, search across documents in the showcase | sealed index is rewritten whole, not incrementally |
| 74 | Offline-first with conflict visibility | feature | partial | sync state, queued changes kept (`client.Options.Queue`) | pre-merge view; same for a document saved to a store; e2e with a real network cut |
| 75 | Sharing UX | feature | partial | share links with role, expiry, revoke, who-can-see list, per-person expiry, open from the address, plain wording | a dialog that reaches a real server for the access list; comment-only role |
| 76 | Key and recovery UX | feature | done (with leftovers) | recovery sheet, test-my-recovery-key, one-key nudge, device list with last use, guided re-key, plain limit wording | none named; the limit itself is inherent |
| 77 | Templates, styles, design tokens | feature | partial | named styles, tokens, themed HTML export | storing sets in the central store; applying them to the canvas and other exports; a theme editor |
| 78 | Equations, diagrams, code, footnotes | feature | partial | MathML with text alternative, code language, diagram descriptions, footnote links both ways in HTML, Word, RTF and Markdown | footnote links in PDF; equations as OMML in Word |
| 79 | Autocomplete word picker | feature | partial | ranked suggestion list as an ARIA listbox, ghost text, de/fr/es word lists | ghost text drawn on the canvas (it is a DOM element), run font, mid-line; larger lists; reader run (blocked) |
| 80 | Print layout | feature | partial | page model, headers and footers, page numbers, contents, widow control in the exports (table rows too in PDF), contents with page references in PDF, Word and RTF; the same running header and footer on the screen sheets | odd and even headers; contents page on the screen (needs a decision: a real contents object in the document would add a second list next to the one the exports write; a display-only sheet would not); sheets that are none of the six papers |
| 81 | Admin side | feature | partial | audit event schema, readiness route, backup and restore, `/admin/usage` and history page, observability core and PostHog sink | hub and sync/font/translation servers instrumented; persistent builtin totals; usage series across restarts |
| 82 | Performance and size | enhancement | done (with leftovers) | wasm size budgets per program (main 26.8 MB raw, 6.6 MB gzip; files 9.8 MB, 2.8 MB) checked in CI, stripped build, precompressed wasm, `wasmdist`, deploy recipe, service worker that keeps the app for repeat visits and offline start, low-memory mode (fewer undo steps), the file formats in a second wasm loaded on first use (main 26.1 MB, was 29.4) | memory saving of the low-memory mode not measured on a real small device; splitting the worker by format: `net/http` is out of it now (9.5 MB, was 11.0), the rest of the floor is to be measured before judging the split (roadmap 82); recipes untested on nginx/Caddy (blocked) |
| 83 | i18n depth | enhancement | partial | de, fr, es packs, a test that every message id is in a pack, plurals and dates | time zones, languages beyond four, RTL packs, ids built at run time |
| 84 | Lint the examples | chore | done | no path exclusions in `.golangci.yml` | none |

## Telemetry and operator observability (57, 81)

| Part | Type | Status | Present | Missing |
| --- | --- | --- | --- | --- |
| 57 Usage counts from browsers (no personal data) | feature | partial | `telemetry` package, opt-in, allowlist, sampling, local log, persistent totals, noise, Privacy menu, [telemetry.md](telemetry.md) | step 7: scrubbed crash reports (separate opt-in); one counting process per store |
| 81 Operator metrics and traces (`observe`) | feature | partial | off by default; closed allowlists; builtin sink and `GET /admin/observe`; **PostHog** and **OpenTelemetry (OTLP/HTTP JSON)** sinks; [observability.md](observability.md) | the other servers count requests only (no metrics for their own work); persistent builtin totals |

## Server, keys and security

| # | Item | Type | Status | Present | Missing |
| --- | --- | --- | --- | --- | --- |
| Passkey backlog 1, 2 | Shared rate limiter and challenge store | enhancement | done | through the store; sessions shared too | Redis behind the same interfaces (decided not to build a client) |
| Passkey backlog 3 | Account stores with a conditional write | enhancement | partial | `AdapterStore` over S3, Mongo and Postgres | a SQL-table store behind the same interfaces |
| Passkey backlog 4 | Per-account failed-sign-in budget | enhancement | open | per-address budgets | key by account |
| Passkey backlog 5 | Shared lock for stores without conditional writes | enhancement | open | conditional-write stores covered | `docstore.Locker` |
| Limits 1 to 11, 13 to 19, 22 to 26 | Review findings (WatchLog anchoring, RepairAll, signer tooling, SSH tags, challenge store, double first passkey, S3 `DeleteIf`, expiry on digest, Mongo CI, pseudonyms, tombstones, byte budgets, history rewrite) | fix | done | see roadmap entries | none |
| Limits 12 | Log witnesses (co-signed heads) | feature | open | auditors compare heads by hand | witness protocol |
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
| Button icons | enhancement | partial | `components.Material` | a toolbar of icon buttons |
| Download submenu | enhancement | done | grouped, `.open` | none |
| `.opendoc` packs | feature | done | `docfile/pack`, `cmd/opendoc` | none |
| Encryption and passkeys | feature | partial | SSH keys, passkeys, envelope encryption, directory, sharing, recovery | see the security table |
| Public docstore | feature | done (server) | anonymous offer with expiry and abuse controls | see 55, 86 |
| Console mode | feature | partial | `console`, `cmd/docsh` | the editor panel |
| Terminal demo | feature | done | `examples/terminal-demo` | none |
| Backends with conditional saves | feature | done | PostgreSQL, MongoDB, S3 | blob backend cannot, by design |
| Known limits plan tail | enhancement | open | partly in 64 and 83 | RTL, Hunspell dictionaries on demand |

## Counts

Product table (items 48 to 84, 37 rows): 1 done, 2 done with leftovers, 34 partial. None is untouched. The server and
security table: the review findings are done; still open are the per-account sign-in budget and the shared lock
(passkey backlog 4 and 5) and log witnesses (limits 12); 41 is blocked on a provider.

## Suggested order for the next work

1. 81: metrics for the other servers' own work; persistent builtin totals.
2. 56: keep recordings between visits and learn from corrections.
3. 68: an insert-clip UI and caption track editor.
4. Then the small leftovers of 59, 64, 65, 50 and 51, and 80's odd and even headers.
5. 82: measure what the file worker's remaining ~7 MB holds before judging a split by format.

Closed since the previous version of this page: 58 (IPv6 /64 budgets), the table-row widow control, the sheet size
restore, contents in RTF, footnote links in RTF, the long undo history, heading levels in the panels, all CSS
colour names, and the file worker without `net/http` (11.0 to 9.5 MB).
