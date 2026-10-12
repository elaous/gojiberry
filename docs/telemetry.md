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
| `error` | `load_failed`, `save_failed`, `send_failed`, `unlock_failed` | which failures are common, by fixed code only |
| `feature` | `format`, `styles`, `symbols`, `keys`, `comments`, `review`, `outline`, `check`, `images`, `find`, `spell`, `language`, `stats`, `share`, `people`, `migrate`, `page`, `save`, `transfer`, `privacy` | which parts of the editor matter, counted when a side panel is opened |
| `load` | `under_1s`, `1_to_3s`, `3_to_10s`, `over_10s` | where the app is slow to start |
| `locale` | `en`, `de`, `fr`, `es`, `other` | which translations matter |

A test keeps this table in step with the allowlist in `telemetry/telemetry.go`.

## For the operator

- `POST /telemetry` takes a batch (at most 8 KiB, rate limited per client with a hashed key that is not kept in
  any log). It answers 204 or a status without a body; it cannot read the store.
- `GET /admin/telemetry` (the admin guard of `/admin/usage`) lists the totals of at least 5 per day.
  `GET /admin/usage` shows how many batches were taken and how many requests were refused.
- A refused request is written to the audit log as `telemetry.refused`, with no content.
- Totals are kept in memory: a restart starts them again. That is a choice of this first version, not a promise.

## Legal notes (not legal advice)

The data is aggregated counts without an identifier, so it is not personal data in itself, but this project
still treats the sending as something the person chooses: the lawful basis is consent (GDPR Art. 6(1)(a)),
given by choosing "Send anonymous counts", withdrawn by choosing "Send nothing" at any time. The consent text is the
text of the Privacy tab. A deployer should review it against their own jurisdiction.

## Not done yet

Sampling on the client, a local debug log to export, and a separate opt-in for scrubbed crash reports (roadmap 57,
steps 5 and 7) are not built.
