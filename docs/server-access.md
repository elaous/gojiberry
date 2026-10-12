# Who may call the servers

Every HTTP server in this repo (docstore, fontserver, syncserver, voiceserver, translate/server, brailleserver, and the collab hub) takes a `guard.Guard` in its `Config`. The guard sets the CORS headers and decides who gets through; the operator picks the policy.

| Guard | What it does |
|---|---|
| `guard.Open()` | Any origin, no check. The default when `Config.Guard` is nil (what the servers did before). Fine for a private network or a local demo. |
| `guard.Origins("https://app.example.com", ...)` | Only pages from these origins. A request carrying another `Origin` gets 403 whatever its method, so another site cannot fire even a simple POST. Requests without an `Origin` (curl, another server) pass: this stops other websites, not other programs. `"*"` means any origin. |
| `guard.Token(secret)` | Every request needs `Authorization: Bearer <secret>`, else 401. Preflights and `Options.Public` paths (`/health`) are exempt. CORS is open to any origin, since the token is the credential. |
| `guard.TokenFunc(func(*http.Request) bool)` | Your own check: a JWT, a table of API keys, ... |
| `guard.Chain(guard.Origins(...), guard.Token(...))` | Both. List `Origins` first so its CORS headers win. |

`guard.Options` adjusts the CORS methods and headers, the public paths, and `TokenHeader`: set it to carry the token in a header other than `Authorization` (docstore does, as `X-Gojiberry-Token`, because `Authorization` carries the sign-in session).

Any type with `Wrap(http.Handler) http.Handler` is a `Guard`; `guard.Func` adapts a function.

## From the environment

The command-line servers read their guard from the environment (`guard.FromEnv`): `<PREFIX>_ALLOW_ORIGINS` (comma-separated origins, or `*`) and `<PREFIX>_TOKEN`. With both set, a request must satisfy both; with neither, the server is open.

| Server | Prefix |
|---|---|
| docstore | `GOJIBERRY_DOCSTORE` (token in `X-Gojiberry-Token`) |
| fontserver | `GOJIBERRY_FONTSERVER` |
| syncserver | `GOJIBERRY_SYNC` |
| voiceserver | `GOJIBERRY_VOICESERVER` |
| translate/server | `GOJIBERRY_TRANSLATESERVER` |
| brailleserver | `GOJIBERRY_BRAILLESERVER` |
| collabhub | `GOJIBERRY_COLLAB` |

A browser page that talks to a token-guarded server has to hold the token, so it is visible to whoever can read the page: use a token for server-to-server calls or a private deployment, and `Origins` (plus the servers' own sign-in where they have one) for public pages.

## Documents nobody owns

By default a document with no ACL is open to everyone (for docstore, when sign-in is on; for the collab hub with `ACLRoles`). To close them: docstore `GOJIBERRY_DOCSTORE_REQUIRE_ACL=true` (`Server.WithRequireACL`) makes an unowned document inaccessible except to the first signed-in writer, who becomes its owner; the collab hub's `GOJIBERRY_COLLAB_NO_ACL_ROLE=none` (`hub.ACLRolesWith`) does the same, or gives everyone `viewer` or `editor` instead.

## The export gate on the server

The page's accessibility gate (warn or block, roadmap 62) can be raised by the operator in the served page, but a page can be edited in the person's own browser. Where documents are stored or sent, the server can hold the line itself: `GOJIBERRY_DOCSTORE_EXPORT_GATE=warn|block` (default none; any other value stops the server).

It covers one route: `GET /docs/{id}?for=export`, or the same request with the header `X-Gojiberry-Purpose: export`. This is the route a host's own export, mail or publish job should use to fetch a document. The server runs `a11ycheck.Check` and `GateMode.Decide` on it after sign-in and the access list have allowed the read, so a refusal is seen only by someone who could read the document anyway. `block` answers 422 when the document has errors, with `X-Gojiberry-Export-Gate: blocked` and `X-Gojiberry-Export-Errors: N` (no document content); there is no override, because an operator who sets `block` has decided there is none. `warn` serves the document and sets `X-Gojiberry-Export-Gate: warned` and the error count. An answer that is not the document (404, 401, 403) is passed on untouched.

What it does not cover, on purpose: the editor's ordinary load and save (`GET`/`PUT /docs/{id}` without the purpose), or nobody could open a document to fix it; and a reader who may read a document and fetches it without saying why gets the stored bytes, as before. The gate is a floor for the host's own export and send paths, not a copy-protection. A document the server cannot read as a document (the ciphertext of an end-to-end encrypted store) cannot be checked: `block` refuses it, `warn` serves it with `X-Gojiberry-Export-Gate: unchecked`. With an encrypted store keep the gate in the client and in the host's decrypting job.

A host that runs its own server calls the library instead: `exportgate.Decide(storedBytes, mode)` judges one stored document, and `exportgate.Wrap(handler, mode)` puts the route's gate in front of `docstore.Server.Handler()`:

```go
http.ListenAndServe(addr, exportgate.Wrap(srv.Handler(), a11ycheck.GateBlock))
```
