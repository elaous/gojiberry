# Web push for mentions

A person who is mentioned (@handle) in a comment is told by the page while it is open (the announcer and "Mentions of you"). Web push tells them when the page is closed: the browser's own push service wakes the page's service worker, which shows a notification. Mail (`mentionmail`, SMTP and Resend parameters) stays as it is; push is one more channel.

A notification says who mentioned the person and in which document, never what they wrote. The message is encrypted end to end between the server and the browser (RFC 8291), so the push service (Google, Mozilla, Apple) carries it without reading it.

## The parts

| Part | What it does |
| --- | --- |
| `webpush` | VAPID keys and the signed `Authorization` header (RFC 8292), message encryption (RFC 8291, aes128gcm), the sender, subscription store, and `webpush.Handler` (the three routes the page uses) |
| `mentionpush` | `Notify`: turns the new mentions of a comment set into one notification per person and sends them to that person's subscriptions; a mention is marked as sent only after the push service accepted it |
| `wasmserve` service worker | the `push` handler shows the notification, `notificationclick` opens the page (only an address of the app's own origin is followed) |
| showcase `pushnotify.go` | the checkbox in the Comments tab: asks permission, subscribes, hands the subscription to the server |

## Setting it up (the operator)

1. Make a VAPID key once and keep it: `webpush.NewVAPID` returns it, `Marshal`/`ParseVAPID` store it (it is a private key: keep it like any secret, and do not change it, or every subscription stops working).
2. Mount the handler, with an `Authorize` that checks the signed-in session and that the handle is the person's own. `Authorize` is required: a subscription receives the notifications addressed to its handle, so without it anybody could subscribe as @ana and learn who mentions her.

```go
h, err := webpush.Handler(webpush.Config{VAPID: key, Store: store, Authorize: func(r *http.Request, handle string) bool { /* session owns handle */ }})
mux.Handle("/push/", http.StripPrefix("/push", h))
```

   The routes are `GET /key` (the public key), `POST /subscribe` and `POST /unsubscribe`.
3. When a document changes, call `mentionpush.Notify` with the sender (`webpush.NewSender`), the store, the comment set, the document title and link, and the handles you know about. It returns the handles told.
4. Put `push.json` next to `index.html`: `{"server": "/push"}` (https or relative to the page). Without it the checkbox does not appear.

## Limits

- The subscription's endpoint is a URL the browser chose. The server only accepts https endpoints and refuses private, loopback, link-local and shared-address ranges, both when it is added and at the moment of the connection, and does not follow redirects.
- A subscription the push service reports gone (404/410) is removed. When the browser renews a subscription by itself (`pushsubscriptionchange`), the service worker subscribes again with the same options (they include the server's key) and tells the open pages, which give the new subscription to the server under the person's handle; with no page open, the page does it the next time it opens (it gives the server its current subscription each time, and the server keeps one per endpoint). If the renewal fails (permission taken back) the page says the notifications stopped and the person ticks the box again. Tested with the worker's stand-in (`make sw-test`) and a stand-in browser (`TestAReplacedPushSubscriptionIsGivenToTheServer`); not with a real push service.
- The service worker is off when the page is opened with `?nosw`; the checkbox then says so.
- Tested with a real encryption check (the output decrypts with an independent http_ece implementation), unit tests, and a browser test with stand-ins for the push API. Delivery through a real push service and a real browser was not tried.
