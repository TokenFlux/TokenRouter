package middleware

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	ippkg "github.com/TokenFlux/TokenRouter/internal/server/clientip"
)

// rate_limiter.go adapts fixed-window admission results into HTTP rate-limit
// responses.
// Fixed-window storage reports admission, its observed count, and a retry delay
// to the HTTP layer. Middleware adds the resolved client address to a route
// bucket and translates rejection into 429, while each route chooses whether
// counter failure favors availability or rejection.

const (
	// Counter failure modes control HTTP admission after a storage error; callers
	// choose fail-close for routes that require counter availability.
	// RateLimitFailOpen continues request handling after a failed counter
	// operation.
	RateLimitFailOpen RateLimitFailureMode = iota
	// RateLimitFailClose rejects the request when the counter operation fails.
	RateLimitFailClose
)

// FixedWindowAllower supplies fixed-window admission, count, and retry
// observations to HTTP adapters.
type FixedWindowAllower interface {
	// Allow reports admission, observed request count, retry duration, and any
	// backend failure for the exact supplied key. HTTP callers choose the response
	// and failure policy; storage implementations own key prefixes and window
	// counting.
	Allow(context.Context, string, int, time.Duration) (bool, int64, time.Duration, error)
}

// RateLimiter converts a supplied fixed-window counter into client-address HTTP
// middleware.
type RateLimiter struct {
	// allower is retained for request counting. The caller keeps the
	// implementation usable and supports concurrent calls while installed
	// middleware serves requests.
	allower FixedWindowAllower
	// prefix decorates failed-counter diagnostic keys. The supplied allower owns
	// its storage namespace and receives the key passed to Allow directly.
	prefix string
}

// RateLimitFailureMode selects HTTP admission behavior when request counting
// returns an error.
type RateLimitFailureMode int

// RateLimitOptions supplies the counter-failure policy selected for an
// installed HTTP limiter.
type RateLimitOptions struct {
	// FailureMode rejects on errors when equal to RateLimitFailClose.
	// LimitWithOptions treats zero and every other value as the availability
	// policy.
	FailureMode RateLimitFailureMode
}

// AllowResult carries the counter observations used for admission and HTTP
// retry headers.
type AllowResult struct {
	Allowed bool  // counter permits this request
	Count   int64 // window count including request
	// RetryAfter carries the counter's rejected-request delay. The Redis adapter
	// returns remaining TTL when available, otherwise the supplied full window;
	// HTTP helpers round positive delays upward.
	RetryAfter time.Duration
}

// NewRateLimiter retains allower for later HTTP counting and sets the
// diagnostic prefix. Supply a usable implementation before invoking Allow or
// installed middleware; construction performs no counter operation and leaves
// storage namespacing to allower.
func NewRateLimiter(allower FixedWindowAllower) *RateLimiter {
	return &RateLimiter{allower: allower, prefix: "rate_limit:"}
}

// Allow returns the supplied counter's admission, count, and retry delay for
// the exact key. It passes ctx, limit, and window through directly; counter
// errors return to callers for their selected failure policy.
func (r *RateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (AllowResult, error) {
	allowed, count, retry, err := r.allower.Allow(ctx, key, limit, window)
	if err != nil {
		return AllowResult{}, err
	}
	return AllowResult{Allowed: allowed, Count: count, RetryAfter: retry}, nil
}

// clientIPForRateLimit resolves the client address used in an HTTP bucket. A
// captured forwarding policy overrides the trusted-proxy default supplied here;
// an empty resolver result falls back to Gin's ClientIP.
func clientIPForRateLimit(c *gin.Context) string {
	if resolved := ippkg.GetSecurityClientIP(c, false); resolved != "" {
		return resolved
	}
	// An empty security result cannot distinguish client buckets. Use Gin's
	// address resolution as the remaining request-level source for the bucket
	// suffix.
	return c.ClientIP()
}

// LimitWithOptions constructs a client-address bucket from key and the
// request's resolved address, then applies the supplied counter limit and
// window. Only RateLimitFailClose rejects on counter errors; zero and other
// option values continue request handling after logging that failure.
//
// A counter denial aborts with the fixed 429 JSON shape and adds a rounded
// Retry-After for a positive delay. The counter receives the route-and-address
// key directly, so its implementation supplies any storage prefix. Keep the
// limiter and its counter available while the installed middleware serves
// requests.
func (r *RateLimiter) LimitWithOptions(key string, limit int, window time.Duration, opts RateLimitOptions) gin.HandlerFunc {
	failureMode := opts.FailureMode
	if failureMode != RateLimitFailClose {
		failureMode = RateLimitFailOpen
	}

	return func(c *gin.Context) {
		result, err := r.Allow(c.Request.Context(), key+":"+clientIPForRateLimit(c), limit, window)
		if err != nil {
			logger.LegacyPrintf("middleware.rate_limiter", "[RateLimit] redis error: key=%s mode=%s err=%v", r.prefix+key, failureModeLabel(failureMode), err)
			// When fail-close is selected, the route requires a working counter before
			// admission. Its configured window supplies a retry delay when counting
			// provides no remaining TTL.
			if failureMode == RateLimitFailClose {
				abortRateLimit(c, window)
				return
			}
			// Counting has failed after this route selected availability on error.
			// Continue the handler chain because a missing observation does not
			// establish an exceeded budget.
			c.Next()
			return
		}

		if !result.Allowed {
			abortRateLimit(c, result.RetryAfter)
			return
		}

		c.Next()
	}
}

// abortRateLimit aborts with the limiter's 429 JSON response. A positive
// retryAfter sets an integer-seconds Retry-After rounded upward; nonpositive
// delays leave that header as it was before this helper.
func abortRateLimit(c *gin.Context, retryAfter time.Duration) {
	if retryAfter > 0 {
		// Round fractional seconds upward to avoid advertising a retry before the
		// counter's observed delay ends.
		seconds := int64(retryAfter / time.Second)
		if retryAfter%time.Second > 0 {
			seconds++
		}
		c.Header("Retry-After", strconv.FormatInt(seconds, 10))
	}
	c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
		"error":   "rate limit exceeded",
		"message": "Too many requests, please try again later",
	})
}

// failureModeLabel returns the diagnostic name for the selected counter-failure
// mode. The exact fail-close value names rejection; all other values use the
// availability label, matching LimitWithOptions normalization.
func failureModeLabel(mode RateLimitFailureMode) string {
	if mode == RateLimitFailClose {
		return "fail-close"
	}
	return "fail-open"
}
