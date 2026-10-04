package main

import (
	"net/http"

	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/secure"
	"github.com/gin-gonic/gin"

	"github.com/elaous/gojiberry/xango"
)

// newRouter builds the real Gin engine this demo serves, with the same
// three security middlewares wired ahead of the page route:
//
//   - secure.New: sets the header-based protections (X-Frame-Options,
//     X-Content-Type-Options, X-XSS-Protection, Content-Security-Policy,
//     Referrer-Policy, Strict-Transport-Security) that gofiber/fiber's
//     own helmet middleware covers, via gin-contrib's port of the same
//     idea. SSLRedirect is left off deliberately -- this demo has no TLS
//     listener, and redirecting every request to a nonexistent https
//     port would just break it; a real deployment behind TLS would
//     enable it (or set SSLProxyHeaders if TLS terminates upstream).
//   - cors.New: a real (non-wildcard) CORS policy -- only AllowOrigins
//     may make cross-origin requests, and only with AllowMethods/
//     AllowHeaders, not "allow everything" defaults.
//   - perClientRateLimit (ratelimit.go): a per-IP token bucket, since
//     Gin ships no rate limiter of its own the way Echo does.
//
// It's a separate func (not inlined into main) so main_test.go can spin
// up a real *httptest.Server against the exact same engine without
// going through main's blocking Run.
func newRouter() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.Use(secure.New(secure.Config{
		FrameDeny:             true,
		ContentTypeNosniff:    true,
		BrowserXssFilter:      true,
		ContentSecurityPolicy: "default-src 'self'",
		ReferrerPolicy:        "strict-origin-when-cross-origin",
		STSSeconds:            31536000,
		STSIncludeSubdomains:  true,
	}))

	r.Use(cors.New(cors.Config{
		AllowOrigins: []string{"https://trusted.example"},
		AllowMethods: []string{"GET"},
		AllowHeaders: []string{"Origin", "Content-Type"},
	}))

	r.Use(perClientRateLimit(2, 3))

	r.GET("/", func(c *gin.Context) {
		c.Header("Content-Type", "text/html; charset=utf-8")
		if err := xango.Render(c.Writer, Page(PageProps{Name: c.Query("name")})); err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
		}
	})

	return r
}
