package main

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

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
