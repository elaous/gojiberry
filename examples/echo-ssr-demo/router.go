package main

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"golang.org/x/time/rate"

	"github.com/elaous/gojiberry/xango"
)

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
		return xango.Render(c.Response().Writer, Page(PageProps{Name: c.QueryParam("name")}))
	})

	return e
}
