package main

import (
	"net/http"
	"sync"
	"time"

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
	type client struct {
		limiter *rate.Limiter
		seen    time.Time
	}
	var mu sync.Mutex
	clients := make(map[string]*client)
	var sweptAt time.Time

	limiterFor := func(clientIP string) *rate.Limiter {
		mu.Lock()
		defer mu.Unlock()
		now := time.Now()
		// Forget clients not seen for a while (at most once a minute), so the map does
		// not grow with every address that ever called.
		if now.Sub(sweptAt) > time.Minute {
			sweptAt = now
			for ip, c := range clients {
				if now.Sub(c.seen) > 10*time.Minute {
					delete(clients, ip)
				}
			}
		}
		c, ok := clients[clientIP]
		if !ok {
			c = &client{limiter: rate.NewLimiter(rate.Limit(rps), burst)}
			clients[clientIP] = c
		}
		c.seen = now
		return c.limiter
	}

	return func(c *gin.Context) {
		if !limiterFor(c.ClientIP()).Allow() {
			c.AbortWithStatus(http.StatusTooManyRequests)
			return
		}
		c.Next()
	}
}
