package main

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"golang.org/x/time/rate"

	"github.com/elaous/gojiberry/gox"
)

// newRouter builds the real Echo instance this demo serves, with the
// same three security middlewares wired ahead of the page route as
// ../gin-ssr-demo, all three built into Echo itself this time:
//
//   - middleware.SecureWithConfig: the header-based protections
//     (X-Frame-Options, X-Content-Type-Options, X-XSS-Protection,
//     Content-Security-Policy, Referrer-Policy,
//     Strict-Transport-Security) that gofiber/fiber's own helmet
//     middleware covers. HSTSMaxAge is set purely for demonstration --
//     this demo has no TLS listener, so the header is emitted but
//     browsers only act on it over a real https connection; a real
//     deployment would only set this behind TLS.
//   - middleware.CORSWithConfig: a real (non-wildcard) CORS policy --
//     only AllowOrigins may make cross-origin requests, and only with
//     AllowMethods, not Echo's own wildcard-everything default.
//   - middleware.RateLimiterWithConfig: Echo's built-in per-client rate
//     limiter, backed by the same golang.org/x/time/rate token bucket
//     the Gin demo wires by hand in its own ratelimit.go, since Gin
//     ships no equivalent of its own.
//
// It's a separate func (not inlined into main) so main_test.go can spin
// up a real *httptest.Server against the exact same instance without
// going through main's blocking Start.
func newRouter() *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	e.Use(middleware.SecureWithConfig(middleware.SecureConfig{
		XSSProtection:         "1; mode=block",
		ContentTypeNosniff:    "nosniff",
		XFrameOptions:         "DENY",
		ContentSecurityPolicy: "default-src 'self'",
		ReferrerPolicy:        "strict-origin-when-cross-origin",
		HSTSMaxAge:            31536000,
		HSTSExcludeSubdomains: false,
	}))

	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: []string{"https://trusted.example"},
		AllowMethods: []string{http.MethodGet},
		AllowHeaders: []string{"Origin", "Content-Type"},
	}))

	e.Use(middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{
		Store: middleware.NewRateLimiterMemoryStoreWithConfig(middleware.RateLimiterMemoryStoreConfig{
			Rate:  rate.Limit(2),
			Burst: 3,
		}),
	}))

	e.GET("/", func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderContentType, "text/html; charset=utf-8")
		return gox.Render(c.Response().Writer, Page(PageProps{Name: c.QueryParam("name")}))
	})

	return e
}
