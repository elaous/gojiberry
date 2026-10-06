// Package public is the server side of the public (anonymous) docstore offer described in
// docs/public-docstore.md: no account, one document per session, deleted 24 hours after its
// last edit, a read-only view link, a report route and an operator takedown.
//
// It is a set of small HTTP routes over any docstore.Adapter that expires documents
// (docstore.ExpiringAdapter), and every control a second replica or a gateway may want to
// replace is an interface with a single-process default, passed in Options:
//
//	Interface        What it does                                   Default                      Plug in
//	SessionStore     live anonymous sessions, the global, per-      MemorySessionStore           Redis, a table (SetDoc must be a
//	                 client and per-network caps, the reserve for                                compare-and-set; Create takes
//	                 new networks, the one-document claim                                        SessionCaps)
//	                 (SetDoc: atomic CAS); optional SessionReaper
//	Limiters         per-group request budgets (Session, Write,     passkey.RateLimiter per      a shared Redis budget, a gateway's
//	                 WriteAddr, View, Report, Admin), keyed by      group from Config            decision; passkey.Unlimited{}
//	                 ClientKey (IPv4, IPv6 /64); passkey.Limiter;
//	                 Admin counts failed authentications only
//	Quota            bytes per client per day, charged before the   MemoryQuota (fixed 24 h      Redis INCRBY with expiry, a table
//	                 body is read and refunded (QuotaRefunder,
//	                 WindowQuota: into the window it was charged in)      window per client)
//	                 for what is not stored
//	Options.ClientIP who the client is, for the limits and quota    remote address, or           your own function
//	                                                                passkey.TrustedProxies
//	AdminGuard       who may call DELETE /admin/takedown/<id>       TokenAdmin (header           operator sign-in, mTLS, a gateway
//	                                                                X-Gojiberry-Admin-Token,
//	                                                                constant-time compare)
//	Blocklist        refuses content before it is stored (422;      NoBlocklist; HashBlocklist   a hash-matching or scanning service
//	                 an error fails closed, 503)                    (SHA-256) is included
//	Challenger       captcha or proof-of-work hook before a         NoChallenge (interface only) a captcha or proof-of-work verifier
//	                 session is issued and a document created
//	Options.Store    documents and records; must implement          docstore.ExpiringAdapter     any adapter wrapped by it
//	                 docstore.Expirer (New refuses a store without)
//
// Over a limit the routes answer 429 with Retry-After (a global session cap or a full storage
// budget is 503; a document over Config.MaxDocBytes is 413; a body that arrives too slowly is 408).
//
// # Routes
//
//	POST   /public/session        a new anonymous session: {token, userId "anon:<random>", expires, limits}
//	POST   /public/doc            create the session's one document (409 if it has a live one)
//	PUT    /public/doc            save an edit (renews the deadline)
//	GET    /public/doc            download it (never renews)
//	DELETE /public/doc            discard it, freeing the session
//	GET    /public/view/<viewId>  the document, read-only; never renews; 410 once taken down
//	GET    /public/health         {"ok":true} (200) or {"ok":false} (503): nothing more
//	GET    /admin/health          the detail (problems, storage, last sweep) for the operator (AdminGuard)
//	POST   /public/report         {"id": "<view id or document id>", "reason": "..."} -> 202
//	DELETE /admin/takedown/<id>   operator: delete, tombstone, 410 from then on (AdminGuard; failed attempts
//	                              are rate limited per client, a right token never is)
//
// The session token (Authorization: Bearer) is the edit capability; the view ID is a second,
// separate unguessable ID derived one way from the document ID, so a view link reveals neither
// the token, nor the owner's user ID, nor the document ID.
//
// # Keyspaces
//
//	pubdoc/<docID>           the document: a plain, expiring document (hidden from /docs by Mount)
//	public/doc/<docID>       its record: owner user ID, view ID, save count, report count
//	public/view/<viewID>     view ID -> document ID
//	public/tomb/<viewID>     takedown tombstone
//	report/<reportID>        an abuse report
//
// "public/" and "report/" are system keyspaces (docstore.PublicPrefix, docstore.ReportPrefix):
// reserved from /docs, never adopted by the expiry sweeper. The records of one document are
// removed by Service.Sweep (and lazily when a view finds the document gone) once the document
// has expired; reports and tombstones have their own TTL (Config.ReportTTL, TombstoneTTL: 30
// days) enforced by the same sweep. Run it (Service.Run) next to ExpiringAdapter.Run.
package public
