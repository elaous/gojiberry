# gin-ssr-demo

`examples/http-server-demo`'s "xango over plain net/http" SSR demo,
except served by [Gin](https://github.com/gin-gonic/gin), with three
real security middlewares fronting the page: secure headers, CORS, and
a per-client rate limit.

It's a separate Go module (like `examples/canvas-app`), `replace`-ing
`github.com/elaous/gojiberry` with the local checkout, so it can depend
on Gin and its middleware ecosystem without adding any of that to the
root module.

## How it works

- `page.xg` is `Page`, unchanged in spirit from `http-server-demo`'s
  own: `xango.Render` only needs an `io.Writer`, and `*gin.Context`'s own
  `Writer` field satisfies that directly -- Gin's routing, binding, and
  middleware stack are completely untouched; xango just replaces
  `html/template` as the thing producing the response body.
- `router.go`'s `newRouter` wires three middlewares ahead of the page
  route:
  - `secure.New` (`github.com/gin-contrib/secure`) -- the header-based
    protections (`X-Frame-Options`, `X-Content-Type-Options`,
    `X-XSS-Protection`, `Content-Security-Policy`, `Referrer-Policy`,
    `Strict-Transport-Security`) that gofiber/fiber's own helmet
    middleware covers, via Gin's ecosystem port of the same idea.
  - `cors.New` (`github.com/gin-contrib/cors`) -- a real, non-wildcard
    CORS policy: only `https://trusted.example` may make cross-origin
    requests, and only with `GET`.
  - `ratelimit.go`'s `perClientRateLimit` -- a small hand-rolled
    per-client-IP token bucket built on the standard library's own
    `golang.org/x/time/rate`, since Gin ships no rate limiter of its
    own the way Echo does (see `../echo-ssr-demo`, which uses Echo's
    built-in one backed by the same `x/time/rate` primitive).
- `main.go` just calls `newRouter().Run(":8097")`; `newRouter` is its
  own function precisely so `main_test.go` can drive the identical
  engine through a real `httptest.Server` without going through `Run`.

## Running it

```
cd examples/gin-ssr-demo
(cd ../.. && go run ./cmd/xgc examples/gin-ssr-demo)
go run .
# open http://localhost:8097/?name=world
```

## Verified

`main_test.go` drives the real engine through a real `net/http` client
against a real `httptest.Server` -- not a direct call into the handler
function:

- The page renders and every secure header is present with the exact
  configured value.
- A cross-origin preflight (`OPTIONS` with `Origin`) from the
  configured trusted origin gets `Access-Control-Allow-Origin` echoed
  back; the same preflight from an untrusted origin gets nothing back.
- Four rapid requests from the same client (burst of 3) genuinely get a
  `429 Too Many Requests` on the fourth, against a fresh rate limiter
  state so earlier subtests' requests can't interfere.

```
go test ./...
```
