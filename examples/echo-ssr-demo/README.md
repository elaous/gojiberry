# echo-ssr-demo

`examples/http-server-demo`'s "gox over plain net/http" SSR demo,
except served by [Echo](https://github.com/labstack/echo), with three
real security middlewares fronting the page: secure headers, CORS, and
a per-client rate limit -- all three built into Echo itself, unlike the
`../gin-ssr-demo` sibling which needs `gin-contrib/*` packages plus one
hand-rolled middleware for the same coverage.

It's a separate Go module (like `examples/canvas-app`), `replace`-ing
`github.com/elaous/gojiberry` with the local checkout, so it can depend
on Echo without adding it to the root module.

## How it works

- `page.gox` is `Page`, unchanged in spirit from `http-server-demo`'s
  own: `gox.Render` only needs an `io.Writer`, and `c.Response().Writer`
  satisfies that directly -- Echo's routing, binding, and middleware
  stack are completely untouched; gox just replaces `html/template` as
  the thing producing the response body.
- `router.go`'s `newRouter` wires three middlewares ahead of the page
  route, all from `github.com/labstack/echo/v4/middleware`:
  - `SecureWithConfig` -- the header-based protections
    (`X-Frame-Options`, `X-Content-Type-Options`, `X-XSS-Protection`,
    `Content-Security-Policy`, `Referrer-Policy`,
    `Strict-Transport-Security`) that gofiber/fiber's own helmet
    middleware covers. One real, non-obvious difference from the Gin
    sibling's `gin-contrib/secure`: Echo only emits
    `Strict-Transport-Security` over a connection it considers actually
    secure (`c.IsTLS()` or a `X-Forwarded-Proto: https` header) --
    sending HSTS over plain HTTP is meaningless, so Echo correctly
    omits it rather than emitting it unconditionally the way
    `gin-contrib/secure` does. `main_test.go` verifies both sides of
    that behavior.
  - `CORSWithConfig` -- a real, non-wildcard CORS policy: only
    `https://trusted.example` may make cross-origin requests, and only
    with `GET`.
  - `RateLimiterWithConfig` -- Echo's own built-in per-client rate
    limiter, backed by the same `golang.org/x/time/rate` token bucket
    `../gin-ssr-demo` wires by hand, since Gin ships no equivalent of
    its own.
- `main.go` just calls `newRouter().Start(":8098")`; `newRouter` is its
  own function precisely so `main_test.go` can drive the identical Echo
  instance through a real `httptest.Server` without going through
  `Start`.

## Running it

```
cd examples/echo-ssr-demo
(cd ../.. && go run ./cmd/goxc examples/echo-ssr-demo)
go run .
# open http://localhost:8098/?name=world
```

## Verified

`main_test.go` drives the real Echo instance through a real `net/http`
client against a real `httptest.Server` -- not a direct call into the
handler function:

- The page renders and every secure header is present with the exact
  configured value, and `Strict-Transport-Security` is correctly absent
  over the plain-HTTP test server.
- Setting `X-Forwarded-Proto: https` (simulating a TLS-terminating
  proxy) makes `Strict-Transport-Security` appear with the configured
  `max-age` and `includeSubdomains`.
- A cross-origin preflight (`OPTIONS` with `Origin`) from the
  configured trusted origin gets `Access-Control-Allow-Origin` echoed
  back; the same preflight from an untrusted origin gets nothing back.
- Four rapid requests from the same client (burst of 3) genuinely get a
  `429 Too Many Requests` on the fourth, against a fresh rate limiter
  state so earlier subtests' requests can't interfere.

```
go test ./...
```
