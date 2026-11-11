# B. Signing in with a passkey

This is part of the design [Encryption with SSH keys and passkeys](encryption-and-passkeys.md), which is split into parts so that each can be read on GitHub (one file of 147 KB was cut off). The index says where everything is.

## B. Signing in with a passkey

1. **WebAuthn**, not a password: the server stores each person's credential (ID, public
   key, signature counter, transports, label, created) and verifies a signed challenge.
   Library: `github.com/go-webauthn/webauthn` (BSD), not hand-written verification.
2. **Package.** `auth/passkey`: `BeginRegistration`/`FinishRegistration`,
   `BeginLogin`/`FinishLogin`, a `CredentialStore` interface (memory and docstore-backed
   implementations), and handlers that sit beside the OIDC ones. The relying party ID
   and allowed origins are configuration (`opendocs.io`, not guessed from the request).
3. **One session format.** A successful passkey login issues the same bearer token as
   OIDC (the package's rule: one token format). The user is `passkey:{credential owner
   id}`, **with no e-mail**: the address typed when registering is stored on the account as a
   label (it names the passkey in the browser) but nothing has verified it, and the rest of the
   library (access lists, `User.Email`) treats an e-mail as verified, so putting it in the
   session would let anyone register as `victim@corp.com` and be matched by an ACL that names
   that address. Marking an address verified (a link mailed to it, then a signed marker on the
   account) is future work. Linking a passkey to an existing OIDC identity is done while
   signed in with that identity (the e-mail in the request body is ignored then), and the
   registration token can only be finished by the session that began it.
   Sign-in refuses a credential whose signature counter did not go up (`CloneWarning`: logged,
   401, the credential stays flagged: remove it and register again). A server keeps at most
   10,000 unfinished ceremonies (503 beyond), and the provider name `passkey` is reserved.
4. **Discoverable credentials** allow signing in without typing a username. A person can
   register several passkeys, list them and remove one (not the last, unless another way
   to sign in exists).
5. **UI.** `components.Passkey`: register, list, remove, "Sign in with a passkey"; the
   browser calls `navigator.credentials.create/get` through `syscall/js`.
6. **Tests.** The server side against recorded ceremonies; the browser side with
   Chrome's virtual authenticator (the CDP WebAuthn domain), which `e2etest`
   can drive. Replay is covered by the signature counter and one-time challenges.

### B.1 Extension points

Everything a second replica or a larger deployment needs to share is an interface, with a
default that works in one process. Pass your own in `passkey.Config` (or the store you give
`passkey.New`):

| Interface | What it does | Default | Plug in |
|---|---|---|---|
| `Store` (+ optional `Updater`, `Creator`) | accounts and credentials; `Updater` changes one account atomically (the function may run again on a conflict), `Creator` makes one only if none exists | `MemoryStore`; `AdapterStore` (compare-and-set over a `docstore.ConditionalAdapter`, else this process's lock) | SQL tables, anything with a transaction or conditional write |
| `ChallengeStore` | started ceremonies; `Take` must be atomic so a challenge is good once | `MemoryChallenges` (10,000 at most, swept every 5 s) | `StoreChallenges` (over a `docstore.ConditionalAdapter`: replicas on one store share ceremonies; `GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES=store`), Redis `GETDEL`, a table with a delete-returning |
| `auth.SessionStore` (`auth.Config.Sessions`, `Manager.SetSessionStore`) | the bearer sessions and OpenID Connect login states of the session manager that passkey sign-in also mints its sessions through; `TakeState` is single use | `auth.MemorySessionStore` (100,000 of each, in the process) | `sessionstore.Store` (over a `docstore.ConditionalAdapter`: replicas on one store share sessions; `GOJIBERRY_DOCSTORE_SESSIONS=store`), Redis |
| `Limiter` | per client and route group: allowed now, or wait this long | `RateLimiter` (token bucket per address and group: 20 at once, 30 a minute) | Redis, a gateway; `Unlimited{}` to switch off |
| `auth.Limiter` (`Manager.SetLoginLimiter`) | a budget per client address in front of `GET /auth/<provider>/login` (key `login|<address>`), which writes a login state without a sign-in; over it 429 with `Retry-After` and nothing is written | none for an embedder; `cmd/docstore` sets one in every `GOJIBERRY_DOCSTORE_SESSIONS` mode (the passkey routes' 20 at once, 30 a minute) | any `docstore.Limiter` (`docstore.StoreLimiter` shares it between replicas), a gateway |
| `Config.ClientIP` | who the client is for the limiter | the remote address; `TrustedProxies(cidrs...)` reads `X-Forwarded-For` only from your proxies | your own function |

Over the limit the routes answer 429 with `Retry-After`. `cmd/docstore` reads
`GOJIBERRY_DOCSTORE_PASSKEY_TRUSTED_PROXIES` (comma separated CIDRs) for the last row, and the same list names the
client for the login limiter: **behind a reverse proxy it must be set, or every client shares the proxy's address and
one login budget** (the server logs a warning at start). The login budget lives where `GOJIBERRY_DOCSTORE_LIMITER`
says. It exists because a flood of unauthenticated logins would otherwise fill the 10,000 pending-login bound and
keep real logins out (503) until the sweep clears it; a flood from many addresses can still do that, for up to
about 12 minutes.
**Two tabs, one first passkey.** A passkey made while signed in (an OpenID Connect user with no passkey account yet)
makes the account `Create`d under the identity's own ID, so two ceremonies begun before either finished (two tabs or
devices) name the same account. `Creator.Create` is a conditional create (`SaveIf` with no digest on an `AdapterStore`
over a `ConditionalAdapter`; the `MemoryStore` lock; without a `Creator` the `Service`'s own lock, one process
only; an `AdapterStore` over a non-conditional adapter falls back to a process-local lock too, so there the guarantee is per process), so exactly one wins and nothing is overwritten: the loser's credential is not attached to the account (its
authenticator holds a user handle the server never stored, so it could not sign in anyway). The loser gets `409`
`{"code":"account_exists"}` (`passkey.ErrAlreadyCreated`, `client.ErrAlreadyCreated`), no longer the `410` of an
expired, used or unknown challenge, which stays `410`. `components.Passkey` shows it in the interface's language
(`passkey.alreadyCreated`: reload and add another passkey from the account; the passkey the second device made is not
used and can be deleted there). Signed out, every begin draws its own random account ID and handle, so two
ceremonies for one e-mail make two accounts (the address is an unverified label, not a key); nothing collides.
When e-mail verification arrives it must not merge or link passkey accounts on the bare e-mail label: passkey
sessions carry no e-mail, and two signed-out registrations with one address make two accounts by design.
**The rule for e-mail verification (roadmap item 41).** An account is never merged, linked or taken over because
an e-mail label matches, and a passkey session never acquires an e-mail, whatever is later verified. A verified
address may *link* two accounts only through a ceremony both take part in: the person proves each (a passkey of
each), chooses which to keep, and the other account's documents are shared to it, not moved silently. Until that
exists, `TestOneEmailLabelNeverMergesTwoPasskeyAccounts` (with the concurrent and unverified-label tests beside it)
fails on any change that makes two registrations of one address one identity, gives a passkey session an e-mail,
or lets a grant to the label reach the other account.
**Shared ceremonies.** `StoreChallenges` keeps each ceremony as a record `passkey/challenge/<hash of the token>`
(the token is not in the name). `Put` is a conditional create. `Take` loads the record and consumes it with one
conditional step on the digest it read, `DeleteIf` or, on a store with only `SaveIf`, a swap to a used marker
(then deleted), so of any number of concurrent Takes, on one replica or many, exactly one succeeds; the rest, and
a replay, get `ErrBadChallenge` (410). Records are named `passkey/challenge/<expiry minute>/<hash>` and the token is
`<random>.<expiry minute>`; a token that is not exactly that (canonical digits, one dot, at most 128 bytes, a minute in
range) never reaches the store. The deadline is the stored `Expires`, checked on every Take (a replica's own
clock, so skew moves it by seconds). `Sweep` (every minute in the server, and once at start) lists the prefix and
deletes by name what is a minute past its bucket, used markers included (they keep their ceremony's name), reading
nothing (the server makes the first sweep before it serves, retrying a failed listing a few times). Only names that
are exactly `<prefix><minute>/<64 lowercase hex>` are swept this way; a record of the earlier layout
(`passkey/challenge/<64 hex>`) is deleted once the store says it is over 24 hours old (or, if the store gives no time,
once a read shows it unparseable or expired) and is not counted; every other name is foreign and never touched.
   The cap (10,000 by default) and replicas. Three strategies were weighed: (a) count names from the listing the
sweep already makes; (b) sharded counters moved by compare-and-set, rejected: a hot record written on every Begin
and Take, retries under load, and a crashed replica leaks its increments; (c) the per-client `Limiter` on the Begin
routes as the real bound. The server uses (c) with (a) as the backstop: each replica counts from its last listing
plus its own writes, so at the cap `Put` answers `ErrBusy` (503) with no sweep and no `Load`, and a live challenge is
never evicted. **Worst-case overshoot** of the cap = the ceremonies all replicas can write between two listings =
(per-client rate per minute) x (distinct clients) x (sweep interval in minutes), and never more than
(replicas - 1) x cap; with the default budget (30 a minute on average per client and route group, burst 20) and the
one-minute sweep a client can write about 50 records in the first interval and 30 in each later one above the cap
(per replica when the limiter is per process, `GOJIBERRY_DOCSTORE_LIMITER=memory`), and the next listing stops it on
every replica. The count a sweep stores keeps the Puts and Takes of its own process that happened while it listed.
   `GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES=store` on a store without conditional writes prints a multi-line warning
(cause, effect, fix) and keeps ceremonies in each process; `store-required` makes the server refuse to start instead,
like `GOJIBERRY_DOCSTORE_SESSIONS`. Memory mode with `GOJIBERRY_DOCSTORE_REPLICAS` above 1 warns that ceremonies are
not shared.
**Shared sessions.** The bearer session a sign-in returns (a passkey one, minted through `Manager.StartSession`, or
an OpenID Connect one) is held by the `auth.Manager`'s `SessionStore`. By default that is in the process, so behind
several replicas a token is known only to the replica that issued it. With `GOJIBERRY_DOCSTORE_SESSIONS=store`
(`auth/sessionstore`) the records are in the store: `auth/session/<expiry hour>/<sha256 of the token>` holds the
user (ID, name, username, picture, verified e-mail and its flag), the issue time and the expiry; the token is never
stored, so someone who reads the store cannot sign in with it. A request reads its record (no cache: a logout on
any replica is seen by the next request on every other; a cache would keep a logged-out token alive for as long
as it lives): one store read per authenticated request, two under `GOJIBERRY_DOCSTORE_EXPIRY_TTL` (the
`ExpiringAdapter` reads the deadline record too). A well-formed random bearer token always reaches the store (one
read per `/docs` request that carries an `Authorization` header); the login limiter does not cover that. Expiry is the stored `Expires` checked on every read and does not slide (as in memory), so
`SessionTTL` is also the absolute age. `auth/state/<expiry minute>/<hash>` holds a login state (provider, return
URL, PKCE verifier, nonce), consumed once by `DeleteIf` or, where that is missing (an encrypting adapter has none
for an encrypted id), by a conditional swap to a used marker, so a replayed `state` fails on every replica and of
any number of concurrent callbacks exactly one wins. The sweep deletes by the expiry number in the
record name without reading anything; used markers go the same way. It lists the login states every minute and the
sessions (hour-wide buckets) at the first sweep, when the hour changes and at least every 10 minutes, so many
replicas make few LIST calls on an object store. At most 100,000 sessions and 10,000 pending
logins wait (`auth.ErrStoreFull`, 503); a live record is never evicted, and each replica counts from its last
sweep, so replicas can overshoot by what they write between sweeps. A store that cannot answer signs nobody in
(401), and a logout it cannot do is a 503, never a silent success. **Limit:** the PKCE verifier and the nonce sit in the state record, so anyone who can read the store (or decrypt it, in encrypted mode) can complete a stolen authorization code until the state is consumed or expires (10 minutes); the store is already inside the trust boundary for session records, which name the user. The records are not in `encrypt.DefaultSkip`
(they hold names and e-mail addresses and the PKCE verifier): under an encrypting adapter they are encrypted like
any file (`passkey/` and `auth/` are not skipped), `RepairAll` treats each as an item of its own, and the document
API refuses both prefixes in every mode. `store` on a store without conditional writes prints a warning that
names the fix and keeps sessions in memory; `store-required` refuses to start. Clock skew moves expiry by the
skew (limits register item 11). What remains per process is listed in the roadmap, backlog item 55.
A store without `Updater`/`Creator` still works in one process; behind several replicas it
needs them (a conditional write), or two sign-ins can overwrite each other's counter update.
