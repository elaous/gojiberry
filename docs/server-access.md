# Who may call the servers

Every HTTP server in this repo (docstore, fontserver, syncserver, voiceserver, translateserver, brailleserver, and the collab hub) takes a `guard.Guard` in its `Config`. The guard sets the CORS headers and decides who gets through; the operator picks the policy.

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
| translateserver | `GOJIBERRY_TRANSLATESERVER` |
| brailleserver | `GOJIBERRY_BRAILLESERVER` |
| collabhub | `GOJIBERRY_COLLAB` |

A browser page that talks to a token-guarded server has to hold the token, so it is visible to whoever can read the page: use a token for server-to-server calls or a private deployment, and `Origins` (plus the servers' own sign-in where they have one) for public pages.

## Documents nobody owns

By default a document with no ACL is open to everyone (for docstore, when sign-in is on; for the collab hub with `ACLRoles`). To close them: docstore `GOJIBERRY_DOCSTORE_REQUIRE_ACL=true` (`Server.WithRequireACL`) makes an unowned document inaccessible except to the first signed-in writer, who becomes its owner; the collab hub's `GOJIBERRY_COLLAB_NO_ACL_ROLE=none` (`hub.ACLRolesWith`) does the same, or gives everyone `viewer` or `editor` instead.
