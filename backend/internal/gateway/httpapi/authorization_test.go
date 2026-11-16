package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// authorization_test.go checks request admission responses for ordinary and
// Google clients using controlled storage and limiter observations. Protected
// requests reject excessive Token rates before capacity reservation, while
// nonconsuming queries retain access and successful leases release after processing.

// tokenProtectionGroups supplies the current group price to request admission.
type tokenProtectionGroups struct {
	apikey.GroupRepository         // unused group repository operations
	rate                   float64 // current persisted group multiplier
}

// gatewayLimitCache returns a configured admission result and counts lease releases.
type gatewayLimitCache struct {
	apikey.APIKeyCache       // unused cache methods
	err                error // configured reservation failure
	// calls and released record reservations and cleanup independently. Ambiguous
	// store failures can require cleanup despite failed admission, unlike known limit rejections.
	calls    int // reservation call count
	released int // lease release count
}

// GetByIDLite returns the active group with its current test-store multiplier.
func (s *tokenProtectionGroups) GetByIDLite(context.Context, int64) (*routing.Group, error) {
	return &routing.Group{ID: 3, Status: apikey.StatusActive, RateMultiplier: s.rate}, nil
}

// TestTokenRateProtectionHTTP rejects protected requests before handler execution.
// Standard and Google responses both use 403, while model-list queries remain
// readable and consume no request-capacity reservation.
func TestTokenRateProtectionHTTP(t *testing.T) {
	for _, google := range []bool{false, true} {
		cache := &gatewayLimitCache{}
		keys := apikey.NewAPIKeyService(nil, nil, &tokenProtectionGroups{rate: .081}, nil, nil, cache, nil)
		t.Cleanup(keys.Stop)
		id := int64(3)
		key := &apikey.APIKey{ID: 1, User: &apikey.User{ID: 7}, GroupID: &id, MaxTokenRateMultiplier: .08}
		for _, method := range []string{"POST", "GET"} {
			path := "/v1/responses"
			if method == "GET" {
				path = "/v1/models"
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(method, path, nil)
			options := APIKeyAuthorizationOptions{}
			options.Authentication.Google = google
			release, admitted := acquireKeyRequest(c, keys, key, options)
			if method == "POST" {
				require.False(t, admitted)
				require.Equal(t, 403, recorder.Code)
				if !google {
					require.Contains(t, recorder.Body.String(), "API_KEY_TOKEN_RATE_LIMIT_EXCEEDED")
				}
			} else {
				require.True(t, admitted)
				release()
			}
			require.Zero(t, cache.calls)
		}
	}
}

// ReserveRequest records admission and returns the configured reservation result.
// The fixed wait is one and a half seconds so the HTTP assertion can verify
// upward rounding of Retry-After for either protocol's limit error.
func (s *gatewayLimitCache) ReserveRequest(context.Context, int64, string, int, int) (time.Duration, error) {
	s.calls++
	// Exercise fractional-second waits in HTTP retry-header rounding.
	return 1500 * time.Millisecond, s.err
}

// RefreshRequest lets admitted test leases renew through the service's normal lifecycle.
func (s *gatewayLimitCache) RefreshRequest(context.Context, int64, string) error { return nil }

// ReleaseRequest records a cleanup attempt and reports a successful test release.
func (s *gatewayLimitCache) ReleaseRequest(context.Context, int64, string) error {
	s.released++
	return nil
}

// TestTokenRateProtectionHTTPAfterPriceIncrease admits a Key capped at 0.1
// before its group price rises to 0.2. The same saved Key then receives a
// precise 403 explanation and remains capped at its user's original value.
func TestTokenRateProtectionHTTPAfterPriceIncrease(t *testing.T) {
	for _, google := range []bool{false, true} {
		groups := &tokenProtectionGroups{rate: .1}
		keys := apikey.NewAPIKeyService(nil, nil, groups, nil, nil, nil, nil)
		t.Cleanup(keys.Stop)
		id := int64(3)
		key := &apikey.APIKey{
			ID: 1, User: &apikey.User{ID: 7}, GroupID: &id,
			MaxTokenRateMultiplier: .1, Group: &routing.Group{ID: id, RateMultiplier: .1},
		}
		options := APIKeyAuthorizationOptions{}
		options.Authentication.Google = google
		for _, rate := range []float64{.1, .2} {
			groups.rate = rate
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			release, admitted := acquireKeyRequest(c, keys, key, options)
			if rate == .1 {
				require.True(t, admitted)
				release()
				continue
			}
			require.False(t, admitted)
			require.Equal(t, 403, recorder.Code)
			message := "Token multiplier 0.2x exceeds API key limit 0.1x. Check group pricing or update the key limit."
			if google {
				require.JSONEq(t, `{"error":{"code":403,"status":"PERMISSION_DENIED","message":`+strconv.Quote(message)+`}}`, recorder.Body.String())
			} else {
				require.JSONEq(t, `{"code":"API_KEY_TOKEN_RATE_LIMIT_EXCEEDED","message":`+strconv.Quote(message)+`}`, recorder.Body.String())
			}
			require.Equal(t, .1, key.MaxTokenRateMultiplier)
			require.Equal(t, .1, key.Group.RateMultiplier)
		}
	}
}

// TestKeyRequestAdmissionHTTP exercises standard and Google admission options
// across allowed, limited, unavailable-store, and non-consuming requests.
// Each case checks status, reservation count, and cleanup count; limit rejections
// also require a whole-second Retry-After rounded upward from the reported duration.
func TestKeyRequestAdmissionHTTP(t *testing.T) {
	for _, google := range []bool{false, true} {
		for _, tc := range []struct {
			// name identifies the case, while method and path select the admission
			// route; the same scenario runs through standard and Google error output.
			name, method, path string // name: case label; method: request verb; path: endpoint path
			err                error  // simulated store result
			// status, calls, and released describe one complete admission attempt.
			// Cleanup may occur after an uncertain store error even when the request is rejected.
			status, calls, released int // status: HTTP result; calls: reservation count; released: cleanup count
		}{
			{"allowed", "POST", "/v1/responses", nil, 200, 1, 1},
			{"concurrency", "POST", "/v1/responses", apikey.ErrKeyConcurrencyExceeded, 429, 1, 0},
			{"rpm", "POST", "/v1/responses", apikey.ErrKeyRPMExceeded, 429, 1, 0},
			{"unavailable", "POST", "/v1/responses", apikey.ErrKeyLimiterUnavailable, 503, 1, 1},
			{"usage", "GET", "/v1/usage", apikey.ErrKeyRPMExceeded, 200, 0, 0},
			{"models", "GET", "/v1/models", apikey.ErrKeyRPMExceeded, 200, 0, 0},
			{"cancel", "POST", "/v1/images/batches/1/cancel", apikey.ErrKeyRPMExceeded, 200, 0, 0},
		} {
			t.Run(tc.name, func(t *testing.T) {
				cache := &gatewayLimitCache{err: tc.err}
				keys := apikey.NewAPIKeyService(nil, nil, nil, nil, nil, cache, nil)
				// Stop the service after each case so lease renewal workers finish
				// before the next case observes its independent reservation and release counts.
				defer keys.Stop()
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(tc.method, tc.path, nil)
				options := APIKeyAuthorizationOptions{}
				options.Authentication.Google = google
				release, ok := acquireKeyRequest(c, keys, &apikey.APIKey{ID: 1, ConcurrencyLimit: 2, RPMLimit: 3}, options)
				if ok {
					// Return admitted capacity before checking counts. Release joins its
					// renewal worker and records cleanup synchronously, so these reads include completed cleanup.
					release()
				}
				require.Equal(t, tc.status, recorder.Code)
				require.Equal(t, tc.calls, cache.calls)
				require.Equal(t, tc.released, cache.released)
				if tc.status == 429 {
					require.Equal(t, "2", recorder.Header().Get("Retry-After"))
				}
			})
		}
	}
}

// TestResponsesAdmissionUsesTurns checks that only Responses WebSocket upgrades use per-turn admission.
// Supported GET roots must match when upgrade headers are present; ordinary
// GET, POST, and another WebSocket path must retain their own admission timing.
func TestResponsesAdmissionUsesTurns(t *testing.T) {
	for _, tc := range []struct {
		// method and path choose the request target. upgrade supplies WebSocket
		// headers, and want records whether this combination defers admission to turns.
		method, path  string // method: request verb; path: endpoint path
		upgrade, want bool   // upgrade: header presence; want: turn-admission result
	}{
		{"GET", "/responses", true, true},
		{"GET", "/v1/responses", true, true},
		{"GET", "/backend-api/codex/responses", true, true},
		{"POST", "/v1/responses", true, false},
		{"GET", "/v1/responses", false, false},
		{"GET", "/realtime", true, false},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(tc.method, tc.path, nil)
		if tc.upgrade {
			c.Request.Header.Set("Upgrade", "websocket")
			c.Request.Header.Set("Connection", "Upgrade")
		}
		require.Equal(t, tc.want, isResponsesTurnAdmission(c))
	}
}

// TestGetAPIKeyIDFromContext supplies absent, mistyped, and nil-pointer context values.
// Each unusable value must return the zero sentinel, while a stored APIKey
// exposes its identifier for request observation.
func TestGetAPIKeyIDFromContext(t *testing.T) {
	t.Run("context 为 nil", func(t *testing.T) {
		require.Equal(t, int64(0), APIKeyIDFromContext(nil))
	})

	t.Run("上下文没有 api_key", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		require.Equal(t, int64(0), APIKeyIDFromContext(c))
	})

	t.Run("api_key 类型错误", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Set("api_key", "not-api-key")
		require.Equal(t, int64(0), APIKeyIDFromContext(c))
	})

	t.Run("api_key 指针为空", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		var k *apikey.APIKey
		c.Set("api_key", k)
		require.Equal(t, int64(0), APIKeyIDFromContext(c))
	})

	t.Run("正常读取 api_key_id", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Set("api_key", &apikey.APIKey{ID: 12345})
		require.Equal(t, int64(12345), APIKeyIDFromContext(c))
	})
}
