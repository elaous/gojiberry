# Operator observability

Gojiberry can report **how the server behaves** to the person who runs it: how many requests each route gets and how
long they take, how the store and the background sweeps perform, whether sign-in and the rate limits work, how many
errors of each fixed kind happen. It is **off by default**, the operator turns it on with an environment variable, and
it is built so that nothing about a person can be in it. Package `observe` is the code; the usage statistics of
[telemetry.md](telemetry.md) are a different thing (see the next section).

## Two producers, one sink layer

The owner's question was whether the browser usage counts (package `telemetry`) and this overlap. They do not: they are
**two separate producers** with different data and different consent, and they share only the **sink** abstraction.

| | User usage counts (`telemetry`) | Operator metrics and traces (`observe`) |
| --- | --- | --- |
| Comes from | the browser | the server itself |
| About | which parts of the editor are used, load speed, browser family, locale, failures | requests per route template, store timings, sweeps, sign-in and limiter outcomes, errors by code |
| Consent | the person's opt-in in the Privacy tab (off by default, Do Not Track respected) | the operator's opt-in, `GOJIBERRY_OBS=on` (off by default, unset is the kill switch) |
| Receiver | `POST /telemetry`, turned on by `GOJIBERRY_DOCSTORE_TELEMETRY=on` | none: the server reports on itself |
| Always available to the operator in | `GET /admin/telemetry`, `GET /admin/usage` (the *builtin* sink) | `GET /admin/observe` (the *builtin* sink) |
| Can also go to | the sinks named in `GOJIBERRY_OBS_USAGE_SINKS` | the sinks named in `GOJIBERRY_OBS_SINKS` |

Both producers write to a `Sink` (`observe.Sink`: `Metric`, `Span`, `Flush`, `Close`). A sink is the builtin admin pages
or an adapter for an external system, and each producer is routed to one sink, another, or several, without the
producer knowing which. **Nothing about what the browser may send changed**: the allowlist, the strict decoder and the
rate limit of `telemetry` are as they were; the receiver only hands the batch it already accepted to the usage sinks.
A usage count reaches an external sink only if the person opted in at the browser, the operator enabled the receiver
(`GOJIBERRY_DOCSTORE_TELEMETRY=on`), **and** the operator named that sink in `GOJIBERRY_OBS_USAGE_SINKS`.

## What is and is not sent

By construction, not by promise:

- **Names are closed.** A metric, a label, a span and a span attribute exist only if they are in the tables below.
  Anything else is dropped and counted (`obs_dropped_total`), never forwarded.
- **Values are closed.** A label value is one of the listed values; a span attribute is a listed value or a number in a
  fixed range. There is no string parameter that a request could fill.
- **Routes are templates.** The middleware maps a path to one of the route templates (`/docs/{id}`, never
  `/docs/3f2a…`). It reads the method and the path to choose the template and the status the handler wrote, and
  nothing else of the request.

| Sent | Never sent |
| --- | --- |
| counts and durations per route template, method and status class | IP addresses or any other network address |
| store operation counts and timings by operation and result | user, document, key, share or session identifiers |
| sweep runs, items removed and durations, by sweep name | tokens, cookies, headers, the query string, the request body |
| limiter decisions by limiter kind (`allowed`, `limited`) | the user agent |
| sign-in, passkey, directory, public and admin outcomes by flow | raw request paths |
| errors by fixed code | free text of any kind, including error messages |
| store size as three numbers (documents, records, bytes) | document names or contents |
| trace spans with the attributes below and random span ids | anything a request chose |

A test (`TestNothingRequestDerivedReachesAnySink`) sends thousands of requests with random paths, headers, queries,
bodies and remote addresses that carry recognisable markers (an address, an id, a token, a user agent, an e-mail) and
fails if a marker, or any label that is not an allowed value, reaches a sink.

### Metrics

| Name | Kind | Labels | Why |
| --- | --- | --- | --- |
| `http_requests_total` | counter | `route`, `method`, `status_class` | how much each route template is used and how it ends |
| `http_request_duration_seconds` | histogram | `route`, `method` | which routes are slow |
| `http_inflight_requests` | gauge | none | how many requests are being served now |
| `auth_outcomes_total` | counter | `flow`, `outcome` | whether sign-in, passkey, directory, public and admin routes work or are refused |
| `limiter_decisions_total` | counter | `limiter`, `outcome` | whether rate limits are too tight or being hit |
| `errors_total` | counter | `code` | failures by a fixed code, never by message |
| `store_ops_total` | counter | `op`, `result` | how the store behaves |
| `store_op_duration_seconds` | histogram | `op` | how slow the store is |
| `sweep_runs_total` | counter | `sweep`, `result` | whether the background sweeps run and succeed |
| `sweep_removed_total` | counter | `sweep` | how much each sweep removes |
| `sweep_duration_seconds` | histogram | `sweep` | how long each sweep takes |
| `store_documents`, `store_records`, `store_bytes` | gauge | none | the size of the store (counts, no identifier), sampled hourly while `GOJIBERRY_DOCSTORE_DIRECTORY_ADMIN_TOKEN` is set |
| `queue_size` | gauge | `queue` | whether a buffer is filling up |
| `usage_events_total` | counter | `fact`, `choice` | the **usage producer**: the facts and values of the telemetry allowlist, after opt-in |
| `obs_dropped_total` | counter | `reason` | what the allowlist refused, so a wrong call is visible |

Label values:

| Label | Values |
| --- | --- |
| `route` | `/health`, `/ready`, `/quota`, `/docs`, `/docs/{id}`, `/docs/{id}/acl`, `/docs/{id}/renew`, `/auth/{provider}/login`, `/auth/{provider}/callback`, `/auth/me`, `/auth/logout`, `/auth/passkey/*`, `/auth/other`, `/directory/*`, `/admin/*`, `/telemetry`, `/me/config`, `/public/*`, `other` |
| `method` | `GET`, `HEAD`, `POST`, `PUT`, `DELETE`, `OPTIONS`, `other` |
| `status_class` | `1xx`, `2xx`, `3xx`, `4xx`, `5xx`, `other` |
| `flow` | `signin`, `passkey`, `directory`, `public`, `admin` |
| `outcome` | `ok`, `denied` (401, 403), `limited` (429), `rejected` (other 4xx), `error` (5xx) for `auth_outcomes_total`; `allowed`, `limited` for `limiter_decisions_total` |
| `limiter` | `directory`, `passkey`, `login`, `auth_miss`, `telemetry`, `admin`, `other` |
| `code` | `server_error`, `panic`, `store_failed`, `sweep_failed`, `sink_failed`, `not_ready` |
| `op` | `load`, `save`, `save_if`, `delete`, `list`, `renew` |
| `result` | `ok`, `not_found`, `conflict`, `canceled`, `error` |
| `sweep` | `expiry`, `ratelimit`, `directory`, `sessions`, `passkey_challenges`, `public`, `usage_sampling` |
| `queue` | `posthog_buffer` |
| `reason` | `unknown_metric`, `wrong_kind`, `unknown_label`, `bad_label_value`, `bad_value`, `unknown_span`, `unknown_attribute`, `bad_attribute_value` |
| `fact`, `choice` | the names and values of the [telemetry allowlist](telemetry.md#what-can-be-sent) |

### Spans

Traces are head-sampled (`GOJIBERRY_OBS_TRACE_SAMPLE`, default `0.1`): a root span is kept with that probability and
its children follow it. Span ids are random per span; they exist to join a child to its parent and are derived from
nothing of the request.

| Span | Attributes | What |
| --- | --- | --- |
| `http.request` | `route`, `method`, `status_class`, `status_code`, `result` | one request, by route template |
| `store.load`, `store.save`, `store.delete`, `store.renew` | `op`, `result` | one store operation, a child of the request |
| `store.list` | `op`, `result`, `count` | a listing of the store |
| `sweep.run` | `sweep`, `result`, `count` | one run of a background sweep |

Attributes: the enumerated ones (`route`, `method`, `status_class`, `op`, `result`, `sweep`) take the values of the label
of the same name; `status_code` is a number from 100 to 599 and `count` a number from 0 to 1,000,000,000.

## Configuration

Environment variables only, as in [configuration.md](configuration.md). A bad value is a start-up error that names the
variable and never repeats the value.

| Variable | Meaning |
| --- | --- |
| `GOJIBERRY_OBS` | `on` or `off`. **Default off; unset is off, and unsetting it is the kill switch.** While off, no sink is built, no secret file is read and nothing is sent; the other `GOJIBERRY_OBS_*` variables are ignored (the server says so in its log). |
| `GOJIBERRY_OBS_SINKS` | Where the operator metrics and traces go, comma separated: `builtin`, `otlp`, `posthog`. Default `builtin`. |
| `GOJIBERRY_OBS_USAGE_SINKS` | Where the browsers' usage counts are also sent, besides the admin pages that always have them: `otlp`, `posthog` (`builtin` is accepted and changes nothing). Default none. |
| `GOJIBERRY_OBS_TRACE_SAMPLE` | Share of requests that get a trace, from `0` to `1`. Default `0.1`. |

A sink the build does not contain (for example `otlp` in the dependency-free `cmd/docstore`) is a start-up error.

## Looking before enabling: `GET /admin/observe`

With the admin guard of `/admin/usage` (`GOJIBERRY_DOCSTORE_DIRECTORY_ADMIN_TOKEN`, header `X-Gojiberry-Admin-Token`),
`GET /admin/observe` answers with the whole allowlist (metrics with their label values, spans, attributes), the list of
what is never sent, whether observability is on, which sinks it is routed to, how many points the allowlist dropped, and
for each sink its own status (for the builtin sink, its current totals). **It answers while observability is off**, so
the operator can read exactly what would be sent before turning anything on.

## Example: builtin only

```
GOJIBERRY_OBS=on
GOJIBERRY_DOCSTORE_DIRECTORY_ADMIN_TOKEN=...   # the admin guard
```

Nothing leaves the server. `GET /admin/observe` shows the totals in memory (a restart starts them again).

## Shutdown

On `SIGTERM` or `SIGINT` (with observability on) the server stops accepting requests, lets the running ones finish for
up to ten seconds, then flushes and closes every sink, so the last counts are sent.

## Not done yet

The PostHog and OpenTelemetry adapters are the next steps (roadmap 57 and 81). Persistent builtin totals, and instrumenting the collaboration hub and sync servers are not built.
