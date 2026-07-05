package main

import (
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// perClientRateLimit returns Gin middleware that rejects a client (by
// remote IP) with 429 once it exceeds a token-bucket refilling at rps
// requests/second with the given burst, via the standard library's own
// golang.org/x/time/rate -- the same primitive Echo's own built-in
// RateLimiter middleware (see main.go in ../echo-ssr-demo) uses
// underneath, just wired by hand here since Gin ships no rate limiter
// of its own.
func perClientRateLimit(rps float64, burst int) gin.HandlerFunc {
	var mu sync.Mutex
	limiters := make(map[string]*rate.Limiter)

	limiterFor := func(clientIP string) *rate.Limiter {
		mu.Lock()
		defer mu.Unlock()
		l, ok := limiters[clientIP]
		if !ok {
			l = rate.NewLimiter(rate.Limit(rps), burst)
			limiters[clientIP] = l
		}
		return l
	}

	return func(c *gin.Context) {
		if !limiterFor(c.ClientIP()).Allow() {
			c.AbortWithStatus(http.StatusTooManyRequests)
			return
		}
		c.Next()
	}
}
