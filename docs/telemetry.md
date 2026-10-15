# Usage statistics (telemetry)

Gojiberry can send the operator of a docstore server a small, anonymous count of how the editor is used. It is
**off by default**, and it is built so that personal data has nowhere to go. Package `telemetry` is the code;
roadmap item 57 is the plan.

## Policy

- Opt-in, default "send nothing". While it is off the browser does not even keep a count.
- Do Not Track and Global Privacy Control are respected: with either set, the option cannot be turned on.
- Only the events of the allowlist below can be sent. A name or value outside it is refused by the client and,
  if one is sent anyway, by the server, which refuses the whole request and counts the refusal.
- What is sent is shown first as the exact JSON, in the Privacy tab of the showcase.
- No identifier of any kind: no user id, address, document id, key, name, text, file path, error message,
  user agent, IP address, cookie or token. The request carries no cookie and no `Authorization` header. The
  receiver does not read or store the source address.
- Time is a day (UTC) and nothing finer. Counts are summed on arrival; nothing is kept per request, so no
  sequence of one visitor's actions can be rebuilt, and nothing links two visits or two devices.
- Sent only to the docstore server the person confirmed in the People tab, never to a third party.
- The server keeps the totals for 90 days and reports only totals of at least 5 (`GET /admin/telemetry`).
- The operator can turn the receiver on with `GOJIBERRY_DOCSTORE_TELEMETRY=on` (it is off, and the route does
  not exist, without it); unsetting it is the kill switch.

## What can be sent

A request is `{"day":"2030-05-01","counts":[{"name":"feature","value":"find","n":3}]}`: a day, and per entry a
name, a value and how many times. Nothing else is accepted.

| Name | Values | Why |
| --- | --- | --- |
| `browser` | `chromium`, `firefox`, `safari`, `other` | which browsers to test on (family only, no version) |
| `crash` | `editor_panic`, `editor_trap`, `editor_error`, `editor_rejection`, `files_panic`, `files_trap`, `files_error`, `files_rejection`, `save_panic`, `save_trap`, `save_error`, `save_rejection`, `collab_panic`, `collab_trap`, `collab_error`, `collab_rejection`, `speech_panic`, `speech_trap`, `speech_error`, `speech_rejection`, `worker_panic`, `worker_trap`, `worker_error`, `worker_rejection`, `page_panic`, `page_trap`, `page_error`, `page_rejection` | which part of the page crashes and how, by two fixed words; a separate opt-in (see "Crash codes") |
| `error` | `load_failed`, `save_failed`, `send_failed`, `unlock_failed` | which failures are common, by fixed code only |
| `feature` | `format`, `styles`, `symbols`, `keys`, `comments`, `review`, `outline`, `check`, `images`, `find`, `spell`, `language`, `stats`, `share`, `people`, `migrate`, `page`, `save`, `transfer`, `privacy` | which parts of the editor matter, counted when a side panel is opened |
| `load` | `under_1s`, `1_to_3s`, `3_to_10s`, `over_10s` | where the app is slow to start |
| `locale` | `en`, `de`, `fr`, `es`, `other` | which translations matter |

A test keeps this table in step with the allowlist in `telemetry/telemetry.go`.

## Sinks: sharing the layer with operator observability

The usage counts are one of two producers that write to the same `Sink` abstraction (package `observe`); the other is
the operator's own metrics and traces about the server ([observability.md](observability.md)). They are separate: this
one comes from the browser with the person's opt-in, that one from the server with the operator's. The receiver keeps
its totals and the admin pages above exactly as they were (the *builtin* sink), and, only if the operator turned on
observability and named a sink in `GOJIBERRY_OBS_USAGE_SINKS`, also hands each batch it accepted to that sink as
aggregate counts (`usage_events_total{fact,choice}`). The browser may send exactly what it could before.

## For the operator

- `POST /telemetry` takes a batch (at most 8 KiB, rate limited per client with a hashed key that is not kept in
  any log). It answers 204 or a status without a body; it cannot read the store.
- `GET /admin/telemetry` (the admin guard of `/admin/usage`) lists the totals of at least 5 per day.
  `GET /admin/usage` shows how many batches were taken and how many requests were refused.
- A refused request is written to the audit log as `telemetry.refused`, with no content.
- The totals are kept in the server's store, under the reserved key `telemetry:totals` (not reachable through
  `/docs`, never expires by itself), written a few seconds after a change so a burst is one write. A restart
  starts from them. The record holds only what the counters hold: a number per day, name and value from the
  allowlist, the two request totals and the random seed of the noise below. It is bounded (90 days times the
  allowlist, 1 MiB at most) and read strictly: a record with a field, name, value or day outside the schema is
  refused whole, the server counts from zero and the next write replaces it. Several server processes on one
  store each write their own totals and the last write wins; run one counting process.
- `GOJIBERRY_DOCSTORE_TELEMETRY_NOISE=<step>` (off by default; a whole number from 2 to 1000, anything else
  stops the server) rounds every total in `GET /admin/telemetry` at random to a multiple of the step before the
  k threshold is applied: a total n becomes the multiple just below or just above it, above with probability
  (n mod step)/step, so it is right on average and a small total is a coarse figure. The draw comes from a keyed
  hash of day, event and total with a secret seed that is stored with the totals, so asking again gives the
  same answer and asking often does not average the noise away. This is differential-privacy-style rounding, not
  a proven (epsilon, delta) guarantee, and it does not change the exact totals in the store, which only the
  operator can reach. The request totals in `/admin/usage` are not rounded.

## Crash codes

A crash is counted as two fixed words, the part of the page it happened in (`editor`, `files`, `save`, `collab`,
`speech`, `worker`, `page`) and its kind (`panic` of the Go program, `trap` of the wasm, `error`, `rejection`): never
the message, the stack, a file name, a line, a URL or a document. They are chosen apart from the usage counts, in the
same Privacy tab: "Send nothing" is the default; "Send a code when the page crashes" turns on only these, and each
choice can be on while the other is off (`Collector.SetCrashConsent`). With neither, nothing is recorded; Do Not
Track and Global Privacy Control turn both off. A crash code travels in the same batch, to the same confirmed server,
under the same limits as a usage count, and shows in the same preview before it is sent. The page keeps the codes
of a crash that ended the program in the browser until the next visit (a program that has crashed cannot send), only
if the person agreed, and clears them whether they were sent or not.

## Legal notes (not legal advice)

The data is aggregated counts without an identifier, so it is not personal data in itself, but this project
still treats the sending as something the person chooses: the lawful basis is consent (GDPR Art. 6(1)(a)),
given by choosing "Send anonymous counts", withdrawn by choosing "Send nothing" at any time. The consent text is the
text of the Privacy tab. A deployer should review it against their own jurisdiction.

## On the client

- **Sampling.** The Privacy tab has "Send from": every visit (default), about 1 visit in 10, or 1 in 100. A visit
  is in the sample or not, drawn when the person opts in and when they change the rate, so a visit sends all of
  its counts or none. A visit outside the sample keeps and shows nothing and sends nothing, and the tab says so.
  The rate is kept in this browser.
- **Local debug log.** Off by default, and only available to someone who opted in. When on, the page keeps (in
  memory, at most 100 entries) a line for each batch that would be or was sent: the minute, an outcome (`sent`,
  `failed`, `not-sampled`, `no-server`), the day and the allowlisted counts, so it can say no more than the
  request would have. It is never sent anywhere, it goes when the page closes or the person chooses to send
  nothing, and "Download the log" saves it as `gojiberry-usage-log.json` to look at or attach to a report.
- The settings are reached from View > Display preferences > Privacy settings, as well as the tab.

