package gateway

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/MiChongs/QQMusicApi/gateway/internal/auth"
)

func APIKeyRateLimitMiddleware(limiter *auth.KeyRateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		via, _ := c.Get("auth.via")
		if via != "apikey" {
			c.Next()
			return
		}
		rec, exists := c.Get("auth.apikey_record")
		if !exists {
			c.Next()
			return
		}
		keyRec, ok := rec.(*auth.APIKeyRecord)
		if !ok || keyRec == nil {
			c.Next()
			return
		}

		if keyRec.RateLimitRPM > 0 {
			result, err := limiter.CheckRPM(c.Request.Context(), keyRec.ID, keyRec.RateLimitRPM)
			if err == nil && !result.Allowed {
				retryAfter := int(result.RetryAfter.Seconds())
				if retryAfter < 1 {
					retryAfter = 1
				}
				c.Header("Retry-After", strconv.Itoa(retryAfter))
				c.Header("X-RateLimit-Limit", strconv.FormatInt(result.Limit, 10))
				c.Header("X-RateLimit-Remaining", "0")
				c.Header("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(result.RetryAfter).Unix(), 10))
				FailData(c, http.StatusTooManyRequests, CodeRateLimited, "rate limit exceeded", map[string]any{
					"limitRpm":   keyRec.RateLimitRPM,
					"retryAfter": retryAfter,
				})
				return
			}
			if err == nil && result.Remaining >= 0 {
				c.Header("X-RateLimit-Limit", strconv.FormatInt(result.Limit, 10))
				c.Header("X-RateLimit-Remaining", strconv.FormatInt(result.Remaining, 10))
			}
		}

		if keyRec.RateLimitDaily > 0 {
			result, err := limiter.CheckDaily(c.Request.Context(), keyRec.ID, keyRec.RateLimitDaily)
			if err == nil && !result.Allowed {
				retryAfter := int(result.RetryAfter.Seconds())
				if retryAfter < 1 {
					retryAfter = 1
				}
				c.Header("Retry-After", strconv.Itoa(retryAfter))
				c.Header("X-RateLimit-Daily-Limit", strconv.FormatInt(result.Limit, 10))
				c.Header("X-RateLimit-Daily-Remaining", "0")
				FailData(c, http.StatusTooManyRequests, CodeRateLimited, "daily limit exceeded", map[string]any{
					"limitDaily": keyRec.RateLimitDaily,
					"retryAfter": retryAfter,
				})
				return
			}
			if err == nil && result.Remaining >= 0 {
				c.Header("X-RateLimit-Daily-Limit", strconv.FormatInt(result.Limit, 10))
				c.Header("X-RateLimit-Daily-Remaining", strconv.FormatInt(result.Remaining, 10))
			}
		}

		c.Next()
	}
}
