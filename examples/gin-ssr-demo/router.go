package main

import (
	"net/http"

	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/secure"
	"github.com/gin-gonic/gin"

	"github.com/elaous/gojiberry/xango"
)

func newRouter() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	_ = r.SetTrustedProxies(nil)
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
