# Audit log

The docstore server can write an operator's audit log: one JSON line per event, to standard error, with nothing in it
that names a person, a document or its content. It is **off by default**.

```
GOJIBERRY_DOCSTORE_AUDIT=on
GOJIBERRY_DOCSTORE_AUDIT_KEY=<at least 16 characters>   # or GOJIBERRY_DOCSTORE_AUDIT_KEY_FILE=/run/secrets/audit
```

Anything but `on` or `off` stops the server at start rather than running without the log the operator meant. Without
the key the log still runs, but its lines carry no `ref`.

## A line

```json
{"time":"2026-03-01T12:00:00Z","kind":"access.granted","outcome":"ok","actor":"owner","count":2,"ref":"3fa9c1d20b7e4a55"}
```

Every field is a time, a number or a member of a closed set (package `audit`): the `kind`, the `outcome` (`ok`,
`denied`, `error`), the `actor` class (`owner`, `editor`, `viewer`, `anonymous`, `operator`, `system`) and a short
`reason` code. There is no field a name, an address, a document id or a piece of content could go in, and an event
with a value off the lists is refused and counted (`Logger.Rejected`) instead of written.

`ref` is a pseudonym: the first 8 bytes of HMAC-SHA256 of the subject (a document id or an account id) under
`GOJIBERRY_DOCSTORE_AUDIT_KEY`, in hex. It follows one document or account through the log. Only the holder of the key
can compute it for a given id (to ask "what happened to this document?"), and it cannot be turned back into the id.

## What is logged

| `kind` | When | `actor` | `count` |
| --- | --- | --- | --- |
| `document.created` | the first signed-in writer becomes a document's owner (`GOJIBERRY_DOCSTORE_REQUIRE_ACL`), or a save is made only if the document did not exist (`If-None-Match: *`) | `owner`, or `anonymous` | |
| `document.deleted` | `DELETE /docs/{id}` (a failed one is `error`/`unavailable`) | `owner`, `editor`, or `anonymous` | |
| `document.expired` | a sweep deleted documents whose time ran out | `system` | how many |
| `access.granted` | an access list was saved with grants added or whose role changed | `owner` | how many |
| `access.revoked` | an access list was saved with grants removed | `owner` | how many |
| `key.enrolled` | an owner key was enrolled in the directory | `owner` | |
| `key.revoked` | a directory key was revoked | `owner` | |
| `owner.reset` | an owner reset was requested (by the account) or done (by the operator) | `owner`, `operator` | |
| `ratelimit.hit` | a rate limit refused a request (`denied`, `over_rate`): no client, key or route | `anonymous` | 1 |
| `backup.made`, `backup.restored` | the `docstore backup` and `restore` commands | `operator` | documents |
| `telemetry.refused` | a usage report was refused | `anonymous` | |

Not logged: reads, ordinary saves of an existing document, `signin.failed` (sign-in lives in package `auth`, which has
no hook for it yet), `key.rotated` (a rotation is an enrollment) and `config.changed`.

The same facts are available as counts, with no per-event detail, in [observability.md](observability.md) and at
`GET /admin/usage`.
