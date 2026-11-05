package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/apikey/testkit"
	billingcore "github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/TokenFlux/TokenRouter/internal/team"
)

// Gateway authorization combines credential lookup with group, funding, and
// consumption eligibility while preserving the endpoint's error format. These
// scenarios cross gateway/httpapi/authorization.go,
// apikey/httpapi/authenticate.go, and ingress_reject.go with controlled key and
// subscription repositories, then inspect status, billing context, ownership,
// and admission markers.
//
// Credential syntax and invalid-auth abuse checks are distinguished from
// repository failures so an operational error does not become an expected
// rejection. Read-only usage and existing-task operations can retain an
// unavailable funding source; generation paths enforce consumption rules. The
// callback fixtures expose maintenance results and shallow record copies whose
// nested references remain shared during these serial requests.

// maxAPIKeyAuthorizationHeaderBytes mirrors the credential byte limit plus
// Authorization overhead used by the oversized-header rejection fixture.
const maxAPIKeyAuthorizationHeaderBytes = apikey.MaxAPIKeyCredentialBytes + 128

// fakeAPIKeyRepo supplies credential lookup and diagnostic-touch callbacks for
// Google authorization cases.
type fakeAPIKeyRepo struct {
	// getByKey controls exact credential lookup for a request. Configure it before
	// use; a nil callback reports an unexpected call in this Google fixture.
	getByKey func(ctx context.Context, key string) (*apikey.APIKey, error)
	// updateLastUsed receives a successful authentication's diagnostic touch when
	// configured. A nil callback accepts the touch, leaving fixture timestamps and
	// counters unchanged.
	updateLastUsed func(ctx context.Context, id int64, usedAt time.Time) error
}

// fakeGoogleSubscriptionRepo supplies subscription lookup and
// window-maintenance callbacks for Google authorization cases.
type fakeGoogleSubscriptionRepo struct {
	// listActive supplies the user's candidate funding subscriptions. A configured
	// callback decides returned sharing; a nil callback reports the unsupported
	// list operation.
	listActive func(ctx context.Context, userID int64) ([]billingcore.UserSubscription, error)
	// getByID supplies a selected or reloaded subscription by ID. Configure it
	// before maintenance checks; a nil callback reports unsupported subscription
	// lookup.
	getByID func(ctx context.Context, id int64) (*billingcore.UserSubscription, error)
	// updateStatus handles the subscription status change requested by validation.
	// A nil callback reports unsupported persistence, letting the service observe
	// that fixture failure.
	updateStatus func(ctx context.Context, subscriptionID int64, status string) error
	// activateWindow handles activation with the subscription ID and start time.
	// The fixture ignores the activation mask, and an absent callback reports
	// unsupported activation.
	activateWindow func(ctx context.Context, id int64, start time.Time) error
	// resetDaily handles the daily reset ID and new window start. The
	// expected-window pointer is ignored, and a nil callback reports unsupported
	// maintenance.
	resetDaily func(ctx context.Context, id int64, start time.Time) error
	// resetWeekly handles the weekly reset ID and new window start. The
	// expected-window pointer is ignored, and a nil callback reports unsupported
	// maintenance.
	resetWeekly func(ctx context.Context, id int64, start time.Time) error
	// resetMonthly handles the monthly reset ID and new window start. The
	// expected-window pointer is ignored, and a nil callback reports unsupported
	// maintenance.
	resetMonthly func(ctx context.Context, id int64, start time.Time) error
}

// googleErrorResponse decodes the Google error envelope asserted by
// authorization scenarios.
type googleErrorResponse struct {
	Error struct {
		Code    int    `json:"code"`    // response HTTP status code
		Message string `json:"message"` // public protocol error message
		Status  string `json:"status"`  // Google error status name
	} `json:"error"` // protocol error envelope object
}

// stubApiKeyRepo supplies credential lookup and diagnostic touches for standard
// gateway authorization.
type stubApiKeyRepo struct {
	// getByKey controls the exact credential lookup result for a request.
	// Configure it before use; a missing callback returns this fixture's
	// unsupported-call error.
	getByKey func(ctx context.Context, key string) (*apikey.APIKey, error)
	// updateLastUsed receives a successful authentication's diagnostic touch when
	// configured. A nil callback accepts the touch, leaving fixture timestamps and
	// counters unchanged.
	updateLastUsed func(ctx context.Context, id int64, usedAt time.Time) error
}

// stubGroupRepoForAuth supplies explicit groups for unavailable-group fallback
// authorization scenarios.
type stubGroupRepoForAuth struct {
	// groupsByID is populated before requests and read by exact ID. Returned
	// records copy the outer Group value while its nested references stay shared.
	groupsByID map[int64]routing.Group
}

// stubUserSubscriptionRepo supplies funding lookup and maintenance callbacks
// for standard gateway authorization.
type stubUserSubscriptionRepo struct {
	// listActive supplies the user's candidate funding subscriptions. A configured
	// callback decides returned sharing; a nil callback reports the unsupported
	// list operation.
	listActive func(ctx context.Context, userID int64) ([]billingcore.UserSubscription, error)
	// getByID supplies a selected or reloaded subscription by ID. Configure it
	// before maintenance checks; a nil callback reports unsupported subscription
	// lookup.
	getByID func(ctx context.Context, id int64) (*billingcore.UserSubscription, error)
	// updateStatus handles the subscription status change requested by validation.
	// A nil callback reports unsupported persistence, letting the service observe
	// that fixture failure.
	updateStatus func(ctx context.Context, subscriptionID int64, status string) error
	// activateWindow handles activation with the subscription ID and start time.
	// The fixture ignores the activation mask, and an absent callback reports
	// unsupported activation.
	activateWindow func(ctx context.Context, id int64, start time.Time) error
	// resetDaily handles the daily reset ID and new window start. The
	// expected-window pointer is ignored, and a nil callback reports unsupported
	// maintenance.
	resetDaily func(ctx context.Context, id int64, start time.Time) error
	// resetWeekly handles the weekly reset ID and new window start. The
	// expected-window pointer is ignored, and a nil callback reports unsupported
	// maintenance.
	resetWeekly func(ctx context.Context, id int64, start time.Time) error
	// resetMonthly handles the monthly reset ID and new window start. The
	// expected-window pointer is ignored, and a nil callback reports unsupported
	// maintenance.
	resetMonthly func(ctx context.Context, id int64, start time.Time) error
}

// subscriptionAuthGroups reports absent group details to the subscription
// fixture's optional group reader.
type subscriptionAuthGroups struct{}

// TestGoogleAPIKeyAuthRejectsOversizedCredentialsBeforeLookup sends a Google
// credential beyond the supported byte width. Authentication returns 401 with
// an invalid-key marker and leaves the repository callback uncalled.
func TestGoogleAPIKeyAuthRejectsOversizedCredentialsBeforeLookup(t *testing.T) {
	var calls atomic.Int32
	repo := fakeAPIKeyRepo{getByKey: func(context.Context, string) (*apikey.APIKey, error) {
		calls.Add(1)
		return nil, apikey.ErrAPIKeyNotFound
	}}
	cfg := &config.Config{}
	svc := testkit.NewService(repo, nil, nil, nil, nil, nil, cfg)
	svc.Start()
	r := gin.New()
	var reason IngressRejectReason
	var rejected bool
	r.Use(func(c *gin.Context) {
		c.Next()
		reason, rejected = GetIngressRejectReason(c)
	})
	r.Use(APIKeyAuthGoogle(svc, cfg))
	r.GET("/v1beta/models", func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1beta/models", nil)
	req.Header.Set("x-goog-api-key", strings.Repeat("x", apikey.MaxAPIKeyCredentialBytes+1))
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Zero(t, calls.Load())
	require.True(t, rejected)
	require.Equal(t, IngressRejectInvalidAPIKey, reason)
}

// TestGoogleAPIKeyAuthMarksLookupBulkheadRejection supplies a valid-shaped
// Google credential whose lookup reports exhausted authentication capacity. The
// request returns 503 and retains the expected admission-overload marker.
func TestGoogleAPIKeyAuthMarksLookupBulkheadRejection(t *testing.T) {
	repo := fakeAPIKeyRepo{getByKey: func(context.Context, string) (*apikey.APIKey, error) {
		return nil, apikey.ErrAPIKeyAuthOverloaded
	}}
	cfg := &config.Config{}
	svc := testkit.NewService(repo, nil, nil, nil, nil, nil, cfg)
	svc.Start()
	r := gin.New()
	var reason IngressRejectReason
	var rejected bool
	r.Use(func(c *gin.Context) {
		c.Next()
		reason, rejected = GetIngressRejectReason(c)
	})
	r.Use(APIKeyAuthGoogle(svc, cfg))
	r.GET("/v1beta/models", func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1beta/models", nil)
	req.Header.Set("x-goog-api-key", "valid-shape")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.True(t, rejected)
	require.Equal(t, IngressRejectAPIKeyAuthOverloaded, reason)
}

// TestGoogleAPIKeyAuthCompositeModelListStillChecksQuota lists models with an
// exhausted composite key. The request returns a Google 429 quota message
// despite needing no single selected model group.
func TestGoogleAPIKeyAuthCompositeModelListStillChecksQuota(t *testing.T) {
	user := &identity.User{ID: 7, Status: billingcore.StatusActive, Balance: 10}
	group := &routing.Group{ID: 9, Status: billingcore.StatusActive, Hydrated: true}
	apiKey := &apikey.APIKey{
		ID: 10, UserID: user.ID, Key: "google-composite-exhausted", Status: apikey.StatusAPIKeyQuotaExhausted,
		User: user, IsComposite: true, Quota: 1, QuotaUsed: 1,
		CompositeGroups: []apikey.APIKeyCompositeGroup{{GroupID: group.ID, Prefix: "Gemini", NormalizedPrefix: "gemini", Group: group}},
	}
	repo := fakeAPIKeyRepo{getByKey: func(_ context.Context, key string) (*apikey.APIKey, error) {
		if key != apiKey.Key {
			return nil, apikey.ErrAPIKeyNotFound
		}
		clone := *apiKey
		return &clone, nil
	}}
	cfg := &config.Config{}
	svc := testkit.NewService(repo, nil, nil, nil, nil, nil, cfg)
	svc.Start()
	router := gin.New()
	router.Use(APIKeyAuthWithSubscriptionGoogle(svc, nil, cfg))
	router.GET("/v1beta/models", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1beta/models", nil)
	req.Header.Set("x-goog-api-key", apiKey.Key)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusTooManyRequests, w.Code)
	var response googleErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Equal(t, "The API key quota has been exhausted.", response.Error.Message)
}

// TestAPIKeyAuthWithSubscriptionGoogle_UsageKeepsUnavailablePreferredSubscription
// reads usage with an expired preferred subscription and available balance. Its
// context retains the selected subscription source and reports that source
// unavailable.
func TestAPIKeyAuthWithSubscriptionGoogle_UsageKeepsUnavailablePreferredSubscription(t *testing.T) {
	now := time.Now()
	group := &routing.Group{ID: 9, Status: billingcore.StatusActive, Hydrated: true}
	user := &identity.User{ID: 7, Status: billingcore.StatusActive, Role: identity.RoleUser, Balance: 100}
	preferredID := int64(55)
	apiKey := &apikey.APIKey{
		ID:                      100,
		UserID:                  user.ID,
		Key:                     "google-usage-unavailable-preferred-plan",
		Status:                  apikey.StatusAPIKeyActive,
		GroupID:                 &group.ID,
		Group:                   group,
		User:                    user,
		BillingMode:             apikey.APIKeyBillingModeSubscription,
		PreferredSubscriptionID: &preferredID,
	}
	subscription := &billingcore.UserSubscription{
		ID:        preferredID,
		UserID:    user.ID,
		PlanID:    1,
		Status:    billingcore.SubscriptionStatusActive,
		StartsAt:  now.Add(-2 * time.Hour),
		ExpiresAt: now.Add(-time.Hour),
		Plan:      &billingcore.SubscriptionPlan{ID: 1, GroupIDs: []int64{group.ID}},
	}
	apiKeyService := testkit.NewService(fakeAPIKeyRepo{
		getByKey: func(_ context.Context, key string) (*apikey.APIKey, error) {
			if key != apiKey.Key {
				return nil, apikey.ErrAPIKeyNotFound
			}
			clone := *apiKey
			return &clone, nil
		},
	}, nil, nil, nil, nil, nil, &config.Config{})
	apiKeyService.Start()
	subscriptionService := newSubscriptionAuthFixture(fakeGoogleSubscriptionRepo{
		getByID: func(_ context.Context, id int64) (*billingcore.UserSubscription, error) {
			if id != preferredID {
				return nil, billingcore.ErrSubscriptionNotFound
			}
			clone := *subscription
			return &clone, nil
		},
	})
	router := gin.New()
	router.Use(APIKeyAuthWithSubscriptionGoogle(apiKeyService, subscriptionService, &config.Config{}))
	router.GET("/v1/usage", func(c *gin.Context) {
		billing, ok := gatewayhttp.GetAPIKeyBillingContext(c)
		if !ok || billing == nil || billing.Subscription == nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"source":          billing.Source,
			"subscription_id": billing.Subscription.ID,
			"available":       billing.Available,
		})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
	req.Header.Set("x-goog-api-key", apiKey.Key)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var response struct {
		Source         string `json:"source"`          // selected funding source name
		SubscriptionID int64  `json:"subscription_id"` // selected subscription record ID
		Available      bool   `json:"available"`       // selected funding availability flag
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Equal(t, "subscription", response.Source)
	require.Equal(t, preferredID, response.SubscriptionID)
	require.False(t, response.Available)
}

// TestApiKeyAuthWithSubscriptionGoogle_MissingKey sends a request with no
// credential. Authentication returns the Google 401 envelope and its
// required-key message before the endpoint executes.
func TestApiKeyAuthWithSubscriptionGoogle_MissingKey(t *testing.T) {
	r := gin.New()
	apiKeyService := newTestAPIKeyService(fakeAPIKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			return nil, errors.New("should not be called")
		},
	})
	r.Use(APIKeyAuthWithSubscriptionGoogle(apiKeyService, nil, &config.Config{}))
	r.GET("/v1beta/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	req := httptest.NewRequest(http.MethodGet, "/v1beta/test", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	var resp googleErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, http.StatusUnauthorized, resp.Error.Code)
	require.Equal(t, "API key is required", resp.Error.Message)
	require.Equal(t, "UNAUTHENTICATED", resp.Error.Status)
}

// TestApiKeyAuthWithSubscriptionGoogle_QueryApiKeyRejected uses the deprecated
// api_key query parameter on a Google path. The response returns Google's 400
// INVALID_ARGUMENT envelope with the credential migration message.
func TestApiKeyAuthWithSubscriptionGoogle_QueryApiKeyRejected(t *testing.T) {
	r := gin.New()
	apiKeyService := newTestAPIKeyService(fakeAPIKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			return nil, errors.New("should not be called")
		},
	})
	r.Use(APIKeyAuthWithSubscriptionGoogle(apiKeyService, nil, &config.Config{}))
	r.GET("/v1beta/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	req := httptest.NewRequest(http.MethodGet, "/v1beta/test?api_key=legacy", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var resp googleErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, http.StatusBadRequest, resp.Error.Code)
	require.Equal(t, "Query parameter api_key is deprecated. Use Authorization header or key instead.", resp.Error.Message)
	require.Equal(t, "INVALID_ARGUMENT", resp.Error.Status)
}

// TestApiKeyAuthWithSubscriptionGoogleSetsGroupContext authenticates an active
// key carrying a hydrated group. The downstream handler observes that same
// group ID in the request-state context.
func TestApiKeyAuthWithSubscriptionGoogleSetsGroupContext(t *testing.T) {
	group := &routing.Group{
		ID:     99,
		Name:   "g1",
		Status: billingcore.StatusActive,

		Hydrated: true,
	}
	user := &identity.User{
		ID:          7,
		Role:        identity.RoleUser,
		Status:      billingcore.StatusActive,
		Balance:     10,
		Concurrency: 3,
	}
	apiKey := &apikey.APIKey{
		ID:     100,
		UserID: user.ID,
		Key:    "test-key",
		Status: billingcore.StatusActive,
		User:   user,
		Group:  group,
	}
	apiKey.GroupID = &group.ID

	apiKeyService := testkit.NewService(
		fakeAPIKeyRepo{
			getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
				if key != apiKey.Key {
					return nil, apikey.ErrAPIKeyNotFound
				}
				clone := *apiKey
				return &clone, nil
			},
		},
		nil,
		nil,
		nil,
		nil,
		nil,
		&config.Config{},
	)
	apiKeyService.Start()

	cfg := &config.Config{}
	r := gin.New()
	r.Use(APIKeyAuthWithSubscriptionGoogle(apiKeyService, nil, cfg))
	r.GET("/v1beta/test", func(c *gin.Context) {
		groupFromCtx, ok := requeststate.GroupFromContext(c.Request.Context())
		if !ok || groupFromCtx == nil || groupFromCtx.ID != group.ID {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/v1beta/test", nil)
	req.Header.Set("x-api-key", apiKey.Key)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}

// TestApiKeyAuthWithSubscriptionGoogle_QueryKeyAllowedOnV1Beta supplies
// Google's supported key query parameter with an active, funded user. The
// authenticated request reaches the successful v1beta endpoint and returns HTTP
// 200.
func TestApiKeyAuthWithSubscriptionGoogle_QueryKeyAllowedOnV1Beta(t *testing.T) {
	r := gin.New()
	apiKeyService := newTestAPIKeyService(fakeAPIKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			return &apikey.APIKey{
				ID:     1,
				Key:    key,
				Status: billingcore.StatusActive,
				User: &identity.User{
					ID:      123,
					Balance: 10,
					Status:  billingcore.StatusActive,
				},
			}, nil
		},
	})
	cfg := &config.Config{}
	r.Use(APIKeyAuthWithSubscriptionGoogle(apiKeyService, nil, cfg))
	r.GET("/v1beta/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	req := httptest.NewRequest(http.MethodGet, "/v1beta/test?key=valid", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}

// TestApiKeyAuthWithSubscriptionGoogle_InvalidKey makes credential lookup
// return key-not-found. The request receives Google's unauthorized envelope and
// the invalid-key admission marker remains observable after handling.
func TestApiKeyAuthWithSubscriptionGoogle_InvalidKey(t *testing.T) {
	r := gin.New()
	apiKeyService := newTestAPIKeyService(fakeAPIKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			return nil, apikey.ErrAPIKeyNotFound
		},
	})
	var rejectReason IngressRejectReason
	var rejected bool
	r.Use(func(c *gin.Context) {
		c.Next()
		rejectReason, rejected = GetIngressRejectReason(c)
	})
	r.Use(APIKeyAuthWithSubscriptionGoogle(apiKeyService, nil, &config.Config{}))
	r.GET("/v1beta/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	req := httptest.NewRequest(http.MethodGet, "/v1beta/test", nil)
	req.Header.Set("Authorization", "Bearer invalid")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	var resp googleErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, http.StatusUnauthorized, resp.Error.Code)
	require.Equal(t, "Invalid API key", resp.Error.Message)
	require.Equal(t, "UNAUTHENTICATED", resp.Error.Status)
	require.True(t, rejected)
	require.Equal(t, IngressRejectInvalidAPIKey, rejectReason)
}

// TestApiKeyAuthWithSubscriptionGoogle_MarksUnavailableGroupBusinessLimited
// authenticates a key referencing a deleted group. The forbidden response
// retains the group-deleted rejection and the separate business-limit reason.
func TestApiKeyAuthWithSubscriptionGoogle_MarksUnavailableGroupBusinessLimited(t *testing.T) {
	groupID := int64(101)
	user := &identity.User{
		ID:          7,
		Role:        identity.RoleUser,
		Status:      billingcore.StatusActive,
		Balance:     10,
		Concurrency: 3,
	}
	apiKey := &apikey.APIKey{
		ID:      100,
		UserID:  user.ID,
		GroupID: &groupID,
		Key:     "google-group-deleted",
		Status:  billingcore.StatusActive,
		User:    user,
		Group: &routing.Group{
			ID:     groupID,
			Name:   "deleted",
			Status: "deleted",

			Hydrated: true,
		},
	}

	r := gin.New()
	var markedBusinessLimited bool
	var businessLimitedReason string
	var rejectReason IngressRejectReason
	var rejected bool
	r.Use(func(c *gin.Context) {
		c.Next()
		markedBusinessLimited = gatewayhttp.HasOpsClientBusinessLimited(c)
		rejectReason, rejected = GetIngressRejectReason(c)
		if v, ok := c.Get(gatewayhttp.OpsClientBusinessLimitedReasonKey); ok {
			businessLimitedReason, _ = v.(string)
		}
	})
	apiKeyService := newTestAPIKeyService(fakeAPIKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			if key != apiKey.Key {
				return nil, apikey.ErrAPIKeyNotFound
			}
			clone := *apiKey
			return &clone, nil
		},
	})
	r.Use(APIKeyAuthWithSubscriptionGoogle(apiKeyService, nil, &config.Config{}))
	r.GET("/v1beta/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	req := httptest.NewRequest(http.MethodGet, "/v1beta/test", nil)
	req.Header.Set("x-goog-api-key", apiKey.Key)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	var resp googleErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "The API key group has been deleted.", resp.Error.Message)
	require.True(t, markedBusinessLimited)
	require.Equal(t, gatewayhttp.OpsClientBusinessLimitedReasonAPIKeyGroupUnavailable, businessLimitedReason)
	require.True(t, rejected)
	require.Equal(t, IngressRejectGroupDeleted, rejectReason)
}

// TestApiKeyAuthWithSubscriptionGoogle_RepoError injects a database lookup
// failure for a supplied credential. The client receives a Google 500
// validation message and INTERNAL status rather than a missing-key response.
func TestApiKeyAuthWithSubscriptionGoogle_RepoError(t *testing.T) {
	r := gin.New()
	apiKeyService := newTestAPIKeyService(fakeAPIKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			return nil, errors.New("db down")
		},
	})
	r.Use(APIKeyAuthWithSubscriptionGoogle(apiKeyService, nil, &config.Config{}))
	r.GET("/v1beta/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	req := httptest.NewRequest(http.MethodGet, "/v1beta/test", nil)
	req.Header.Set("Authorization", "Bearer any")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	var resp googleErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, http.StatusInternalServerError, resp.Error.Code)
	require.Equal(t, "Failed to validate API key", resp.Error.Message)
	require.Equal(t, "INTERNAL", resp.Error.Status)
}

// TestApiKeyAuthWithSubscriptionGoogle_DisabledKey supplies a disabled key
// behind a valid-shaped Authorization Bearer credential. Authentication returns
// Google's unauthorized envelope with the disabled-key message before endpoint
// handling.
func TestApiKeyAuthWithSubscriptionGoogle_DisabledKey(t *testing.T) {
	r := gin.New()
	apiKeyService := newTestAPIKeyService(fakeAPIKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			return &apikey.APIKey{
				ID:     1,
				Key:    key,
				Status: billingcore.StatusDisabled,
				User: &identity.User{
					ID:     123,
					Status: billingcore.StatusActive,
				},
			}, nil
		},
	})
	r.Use(APIKeyAuthWithSubscriptionGoogle(apiKeyService, nil, &config.Config{}))
	r.GET("/v1beta/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	req := httptest.NewRequest(http.MethodGet, "/v1beta/test", nil)
	req.Header.Set("Authorization", "Bearer disabled")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	var resp googleErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, http.StatusUnauthorized, resp.Error.Code)
	require.Equal(t, "API key is disabled", resp.Error.Message)
	require.Equal(t, "UNAUTHENTICATED", resp.Error.Status)
}

// TestApiKeyAuthWithSubscriptionGoogle_RejectsRuntimeKeyRestrictions varies key
// expiry and exhausted quota after credential lookup succeeds. Google status,
// public message, and error status name protect each runtime rejection shape.
func TestApiKeyAuthWithSubscriptionGoogle_RejectsRuntimeKeyRestrictions(t *testing.T) {
	expiredAt := time.Now().Add(-time.Minute)
	tests := []struct {
		name            string               // authorization scenario label
		configureAPIKey func(*apikey.APIKey) // fixture key restriction mutation
		wantCode        int                  // expected response HTTP status
		wantMessage     string               // expected public response message
		wantStatus      string               // expected Google error status
	}{
		{
			name: "expired_at",
			configureAPIKey: func(apiKey *apikey.APIKey) {
				apiKey.ExpiresAt = &expiredAt
			},
			wantCode:    http.StatusForbidden,
			wantMessage: "The API key has expired.",
			wantStatus:  "PERMISSION_DENIED",
		},
		{
			name: "quota_exhausted",
			configureAPIKey: func(apiKey *apikey.APIKey) {
				apiKey.Quota = 10
				apiKey.QuotaUsed = 10
			},
			wantCode:    http.StatusTooManyRequests,
			wantMessage: "The API key quota has been exhausted.",
			wantStatus:  "RESOURCE_EXHAUSTED",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apiKey := &apikey.APIKey{
				ID:     1,
				Key:    "runtime-restricted",
				Status: billingcore.StatusActive,
				User: &identity.User{
					ID:      123,
					Status:  billingcore.StatusActive,
					Balance: 10,
				},
			}
			tt.configureAPIKey(apiKey)

			apiKeyService := newTestAPIKeyService(fakeAPIKeyRepo{
				getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
					clone := *apiKey
					return &clone, nil
				},
			})
			cfg := &config.Config{}
			r := gin.New()
			r.Use(APIKeyAuthWithSubscriptionGoogle(apiKeyService, nil, cfg))
			r.GET("/v1beta/test", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

			req := httptest.NewRequest(http.MethodGet, "/v1beta/test", nil)
			req.Header.Set("x-goog-api-key", apiKey.Key)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			require.Equal(t, tt.wantCode, rec.Code)
			var resp googleErrorResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
			require.Equal(t, tt.wantCode, resp.Error.Code)
			require.Equal(t, tt.wantMessage, resp.Error.Message)
			require.Equal(t, tt.wantStatus, resp.Error.Status)
		})
	}
}

// TestApiKeyAuthWithSubscriptionGoogle_IPRestrictionDoesNotTrustForwardedClientIPByDefault
// uses zero-valued configuration and no trusted proxies with spoofed forwarding
// headers. The ACL denies the actual peer and reports that peer in Google's
// error.
func TestApiKeyAuthWithSubscriptionGoogle_IPRestrictionDoesNotTrustForwardedClientIPByDefault(t *testing.T) {
	apiKey := &apikey.APIKey{
		ID:          1,
		Key:         "google-ip-restricted",
		Status:      billingcore.StatusActive,
		IPWhitelist: []string{"1.2.3.4"},
		User: &identity.User{
			ID:      123,
			Status:  billingcore.StatusActive,
			Balance: 10,
		},
	}
	apiKeyService := newTestAPIKeyService(fakeAPIKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			clone := *apiKey
			return &clone, nil
		},
	})
	cfg := &config.Config{}
	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	var businessLimitedReason string
	r.Use(func(c *gin.Context) {
		c.Next()
		if value, ok := c.Get(gatewayhttp.OpsClientBusinessLimitedReasonKey); ok {
			businessLimitedReason, _ = value.(string)
		}
	})
	r.Use(APIKeyAuthWithSubscriptionGoogle(apiKeyService, nil, cfg))
	r.GET("/v1beta/test", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	req := httptest.NewRequest(http.MethodGet, "/v1beta/test", nil)
	req.RemoteAddr = "9.9.9.9:12345"
	req.Header.Set("x-goog-api-key", apiKey.Key)
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.Header.Set("X-Real-IP", "1.2.3.4")
	req.Header.Set("CF-Connecting-IP", "1.2.3.4")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	var resp googleErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "Access denied. Your IP is 9.9.9.9", resp.Error.Message)
	require.Equal(t, gatewayhttp.OpsClientBusinessLimitedReasonIPRestriction, businessLimitedReason)
}

// TestApiKeyAuthWithSubscriptionGoogle_InsufficientBalance authenticates an
// active user with zero balance and no eligible subscription. Its Google
// response is forbidden with the insufficient-balance message.
func TestApiKeyAuthWithSubscriptionGoogle_InsufficientBalance(t *testing.T) {
	r := gin.New()
	apiKeyService := newTestAPIKeyService(fakeAPIKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			return &apikey.APIKey{
				ID:     1,
				Key:    key,
				Status: billingcore.StatusActive,
				User: &identity.User{
					ID:      123,
					Status:  billingcore.StatusActive,
					Balance: 0,
				},
			}, nil
		},
	})
	r.Use(APIKeyAuthWithSubscriptionGoogle(apiKeyService, nil, &config.Config{}))
	r.GET("/v1beta/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	req := httptest.NewRequest(http.MethodGet, "/v1beta/test", nil)
	req.Header.Set("Authorization", "Bearer ok")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	var resp googleErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, http.StatusForbidden, resp.Error.Code)
	require.Equal(t, "Insufficient account balance", resp.Error.Message)
	require.Equal(t, "PERMISSION_DENIED", resp.Error.Status)
}

// TestApiKeyAuthWithSubscriptionGoogle_BalanceBelowMinimumReserve configures a
// reserve above the fixture's positive balance. The authorization stage still
// allows the request, protecting its positive-balance check independently of
// later billing reserve checks.
func TestApiKeyAuthWithSubscriptionGoogle_BalanceBelowMinimumReserve(t *testing.T) {
	// MinimumBalanceReserve applies to billing-cache prechecks; authentication
	// accepts a positive balance.
	r := gin.New()
	apiKeyService := newTestAPIKeyService(fakeAPIKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			return &apikey.APIKey{
				ID:     1,
				Key:    key,
				Status: billingcore.StatusActive,
				User: &identity.User{
					ID:      123,
					Status:  billingcore.StatusActive,
					Balance: 0.005,
				},
			}, nil
		},
	})
	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.01
	r.Use(APIKeyAuthWithSubscriptionGoogle(apiKeyService, nil, cfg))
	r.GET("/v1beta/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	req := httptest.NewRequest(http.MethodGet, "/v1beta/test", nil)
	req.Header.Set("Authorization", "Bearer ok")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}

// TestApiKeyAuthWithSubscriptionGoogle_RejectsExhaustedBalance supplies an
// active credential with an exhausted wallet. Authorization returns Google's
// forbidden insufficient-balance response before the successful endpoint can
// run.
func TestApiKeyAuthWithSubscriptionGoogle_RejectsExhaustedBalance(t *testing.T) {
	r := gin.New()
	apiKeyService := newTestAPIKeyService(fakeAPIKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			return &apikey.APIKey{
				ID:     1,
				Key:    key,
				Status: billingcore.StatusActive,
				User: &identity.User{
					ID:      123,
					Status:  billingcore.StatusActive,
					Balance: 0,
				},
			}, nil
		},
	})
	cfg := &config.Config{}
	r.Use(APIKeyAuthWithSubscriptionGoogle(apiKeyService, nil, cfg))
	r.GET("/v1beta/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	req := httptest.NewRequest(http.MethodGet, "/v1beta/test", nil)
	req.Header.Set("Authorization", "Bearer ok")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	var resp googleErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, http.StatusForbidden, resp.Error.Code)
	require.Equal(t, "Insufficient account balance", resp.Error.Message)
	require.Equal(t, "PERMISSION_DENIED", resp.Error.Status)
}

// TestApiKeyAuthWithSubscriptionGoogle_TouchesLastUsedOnSuccess records the
// callback for an admitted Google credential. It observes that key ID and a
// nonzero touch timestamp after the response succeeds.
func TestApiKeyAuthWithSubscriptionGoogle_TouchesLastUsedOnSuccess(t *testing.T) {
	user := &identity.User{
		ID:          11,
		Role:        identity.RoleUser,
		Status:      billingcore.StatusActive,
		Balance:     10,
		Concurrency: 3,
	}
	apiKey := &apikey.APIKey{
		ID:     201,
		UserID: user.ID,
		Key:    "google-touch-ok",
		Status: billingcore.StatusActive,
		User:   user,
	}

	var touchedID int64
	var touchedAt time.Time
	r := gin.New()
	apiKeyService := newTestAPIKeyService(fakeAPIKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			if key != apiKey.Key {
				return nil, apikey.ErrAPIKeyNotFound
			}
			clone := *apiKey
			return &clone, nil
		},
		updateLastUsed: func(ctx context.Context, id int64, usedAt time.Time) error {
			touchedID = id
			touchedAt = usedAt
			return nil
		},
	})
	cfg := &config.Config{}
	r.Use(APIKeyAuthWithSubscriptionGoogle(apiKeyService, nil, cfg))
	r.GET("/v1beta/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	req := httptest.NewRequest(http.MethodGet, "/v1beta/test", nil)
	req.Header.Set("x-goog-api-key", apiKey.Key)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, apiKey.ID, touchedID)
	require.False(t, touchedAt.IsZero())
}

// TestApiKeyAuthWithSubscriptionGoogle_TouchFailureDoesNotBlock makes last-used
// persistence return an error after successful credential validation. The
// request remains admitted and the touch callback runs once.
func TestApiKeyAuthWithSubscriptionGoogle_TouchFailureDoesNotBlock(t *testing.T) {
	user := &identity.User{
		ID:          12,
		Role:        identity.RoleUser,
		Status:      billingcore.StatusActive,
		Balance:     10,
		Concurrency: 3,
	}
	apiKey := &apikey.APIKey{
		ID:     202,
		UserID: user.ID,
		Key:    "google-touch-fail",
		Status: billingcore.StatusActive,
		User:   user,
	}

	touchCalls := 0
	r := gin.New()
	apiKeyService := newTestAPIKeyService(fakeAPIKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			if key != apiKey.Key {
				return nil, apikey.ErrAPIKeyNotFound
			}
			clone := *apiKey
			return &clone, nil
		},
		updateLastUsed: func(ctx context.Context, id int64, usedAt time.Time) error {
			touchCalls++
			return errors.New("write failed")
		},
	})
	cfg := &config.Config{}
	r.Use(APIKeyAuthWithSubscriptionGoogle(apiKeyService, nil, cfg))
	r.GET("/v1beta/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	req := httptest.NewRequest(http.MethodGet, "/v1beta/test", nil)
	req.Header.Set("x-goog-api-key", apiKey.Key)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, touchCalls)
}

// TestApiKeyAuthWithSubscriptionGoogle_TouchesLastUsedInStandardMode
// authenticates an active Google fixture using the Authorization Bearer header.
// Successful handling records exactly one diagnostic touch through the supplied
// repository callback.
func TestApiKeyAuthWithSubscriptionGoogle_TouchesLastUsedInStandardMode(t *testing.T) {
	user := &identity.User{
		ID:          13,
		Role:        identity.RoleUser,
		Status:      billingcore.StatusActive,
		Balance:     10,
		Concurrency: 3,
	}
	apiKey := &apikey.APIKey{
		ID:     203,
		UserID: user.ID,
		Key:    "google-touch-standard",
		Status: billingcore.StatusActive,
		User:   user,
	}

	touchCalls := 0
	r := gin.New()
	apiKeyService := newTestAPIKeyService(fakeAPIKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			if key != apiKey.Key {
				return nil, apikey.ErrAPIKeyNotFound
			}
			clone := *apiKey
			return &clone, nil
		},
		updateLastUsed: func(ctx context.Context, id int64, usedAt time.Time) error {
			touchCalls++
			return nil
		},
	})
	cfg := &config.Config{}
	r.Use(APIKeyAuthWithSubscriptionGoogle(apiKeyService, nil, cfg))
	r.GET("/v1beta/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	req := httptest.NewRequest(http.MethodGet, "/v1beta/test", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey.Key)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, touchCalls)
}

// TestApiKeyAuthWithSubscriptionGoogle_ExhaustedSubscriptionFallsBackToBalance
// supplies an exhausted candidate subscription and positive wallet balance.
// Automatic funding still admits the Google request with its available balance
// path.
func TestApiKeyAuthWithSubscriptionGoogle_ExhaustedSubscriptionFallsBackToBalance(t *testing.T) {
	group := &routing.Group{
		ID:     77,
		Name:   "gemini-sub",
		Status: billingcore.StatusActive,

		Hydrated: true,
	}
	user := &identity.User{
		ID:          999,
		Role:        identity.RoleUser,
		Status:      billingcore.StatusActive,
		Balance:     10,
		Concurrency: 3,
	}
	apiKey := &apikey.APIKey{
		ID:     501,
		UserID: user.ID,
		Key:    "google-sub-limit",
		Status: billingcore.StatusActive,
		User:   user,
		Group:  group,
	}
	apiKey.GroupID = &group.ID

	apiKeyService := newTestAPIKeyService(fakeAPIKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			if key != apiKey.Key {
				return nil, apikey.ErrAPIKeyNotFound
			}
			clone := *apiKey
			return &clone, nil
		},
	})

	now := time.Now()
	dailyLimit := 1.0
	sub := &billingcore.UserSubscription{
		ID:               601,
		UserID:           user.ID,
		PlanID:           group.ID,
		Status:           billingcore.SubscriptionStatusActive,
		ExpiresAt:        now.Add(24 * time.Hour),
		DailyWindowStart: &now,
		DailyLimitUSD:    &dailyLimit,
		DailyUsageUSD:    10,
	}
	subscriptionService := newSubscriptionAuthFixture(fakeGoogleSubscriptionRepo{
		listActive: func(ctx context.Context, userID int64) ([]billingcore.UserSubscription, error) {
			if userID != user.ID {
				return nil, nil
			}
			clone := *sub
			return []billingcore.UserSubscription{clone}, nil
		},
		updateStatus:   func(ctx context.Context, subscriptionID int64, status string) error { return nil },
		activateWindow: func(ctx context.Context, id int64, start time.Time) error { return nil },
		resetDaily:     func(ctx context.Context, id int64, start time.Time) error { return nil },
		resetWeekly:    func(ctx context.Context, id int64, start time.Time) error { return nil },
		resetMonthly:   func(ctx context.Context, id int64, start time.Time) error { return nil },
	})

	r := gin.New()
	r.Use(APIKeyAuthWithSubscriptionGoogle(apiKeyService, subscriptionService, &config.Config{}))
	r.GET("/v1beta/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	req := httptest.NewRequest(http.MethodGet, "/v1beta/test", nil)
	req.Header.Set("x-goog-api-key", apiKey.Key)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}

// TestTeamAPIKeyErrorsHaveStableGatewayStatus maps team identity and
// member-budget errors through the standard and Google helpers. Both preserve
// the represented HTTP statuses, and standard responses retain their reason
// codes.
func TestTeamAPIKeyErrorsHaveStableGatewayStatus(t *testing.T) {
	tests := []struct {
		name       string // authorization scenario label
		err        error  // team admission error fixture
		wantStatus int    // expected response HTTP status
		wantCode   string // expected protocol reason code
	}{
		{name: "feature_disabled", err: team.ErrTeamFeatureDisabled, wantStatus: http.StatusForbidden, wantCode: "TEAM_FEATURE_DISABLED"},
		{name: "team_suspended", err: team.ErrTeamSuspended, wantStatus: http.StatusForbidden, wantCode: "TEAM_SUSPENDED"},
		{name: "membership_missing", err: team.ErrTeamMembershipRequired, wantStatus: http.StatusForbidden, wantCode: "TEAM_MEMBERSHIP_REQUIRED"},
		{name: "actor_inactive", err: apikey.ErrTeamActorInactive, wantStatus: http.StatusForbidden, wantCode: "TEAM_ACTOR_INACTIVE"},
		{name: "owner_inactive", err: apikey.ErrTeamBillingOwnerInactive, wantStatus: http.StatusForbidden, wantCode: "TEAM_BILLING_OWNER_INACTIVE"},
		{name: "daily_limit", err: team.ErrTeamMemberDailyExceeded, wantStatus: http.StatusTooManyRequests, wantCode: "TEAM_MEMBER_DAILY_LIMIT_EXCEEDED"},
		{name: "weekly_limit", err: team.ErrTeamMemberWeeklyExceeded, wantStatus: http.StatusTooManyRequests, wantCode: "TEAM_MEMBER_WEEKLY_LIMIT_EXCEEDED"},
		{name: "monthly_limit", err: team.ErrTeamMemberMonthlyExceeded, wantStatus: http.StatusTooManyRequests, wantCode: "TEAM_MEMBER_MONTHLY_LIMIT_EXCEEDED"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			require.True(t, abortTeamAPIKeyError(ctx, test.err))
			require.Equal(t, test.wantStatus, recorder.Code)
			require.Contains(t, recorder.Body.String(), test.wantCode)

			googleStatus, _, ok := googleTeamAPIKeyError(test.err)
			require.True(t, ok)
			require.Equal(t, test.wantStatus, googleStatus)
		})
	}
}

// TestTeamMemberLimitsSkipNonConsumingRequests classifies usage, model listing,
// batch management, and token counting methods and paths. The represented reads
// and controls qualify, while generation, submission, and the removed billing
// path do not.
func TestTeamMemberLimitsSkipNonConsumingRequests(t *testing.T) {
	tests := []struct {
		method string // request HTTP method
		path   string // request path under test
	}{
		{method: http.MethodGet, path: "/v1/usage"},
		{method: http.MethodGet, path: "/v1/models"},
		{method: http.MethodGet, path: "/v1/images/batches"},
		{method: http.MethodGet, path: "/v1/images/batches/test"},
		{method: http.MethodDelete, path: "/v1/images/batches/test"},
		{method: http.MethodDelete, path: "/v1/images/batches/test/outputs"},
		{method: http.MethodPost, path: "/v1/images/batches/test/cancel"},
		{method: http.MethodPost, path: "/v1/messages/count_tokens"},
	}
	for _, test := range tests {
		require.True(t, isAPIKeyNonConsumingRequest(test.method, test.path), "%s %s", test.method, test.path)
	}
	require.False(t, isAPIKeyNonConsumingRequest(http.MethodGet, "/v1/sub2api/billing"))
	require.False(t, isAPIKeyNonConsumingRequest(http.MethodPost, "/v1/messages"))
	require.False(t, isAPIKeyNonConsumingRequest(http.MethodPost, "/v1/images/batches"))
}

// TestAPIKeyAuthRejectsOversizedCredentialsBeforeLookup exceeds key and
// Authorization header limits in different credential representations. Each
// response is unauthorized and the repository lookup count remains zero.
func TestAPIKeyAuthRejectsOversizedCredentialsBeforeLookup(t *testing.T) {
	var calls atomic.Int32
	repo := &stubApiKeyRepo{getByKey: func(context.Context, string) (*apikey.APIKey, error) {
		calls.Add(1)
		return nil, apikey.ErrAPIKeyNotFound
	}}
	cfg := &config.Config{}
	svc := testkit.NewService(repo, nil, nil, nil, nil, nil, cfg)
	svc.Start()

	for _, headers := range []map[string]string{
		{"x-api-key": strings.Repeat("x", apikey.MaxAPIKeyCredentialBytes+1)},
		{"Authorization": "Bearer " + strings.Repeat("x", apikey.MaxAPIKeyCredentialBytes+1)},
		{"Authorization": strings.Repeat("x", maxAPIKeyAuthorizationHeaderBytes+1)},
	} {
		r := gin.New()
		r.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(svc, nil, cfg)))
		r.GET("/t", func(c *gin.Context) { c.Status(http.StatusOK) })
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/t", nil)
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusUnauthorized, w.Code)
	}
	require.Zero(t, calls.Load())
}

// TestAPIKeyAuthEnforcesQuotaAndCredentials varies subscription maintenance,
// reloaded quota, key limits, and bearer spelling through standard
// authorization. The responses protect maintenance completion and current-quota
// rejection alongside automatic balance funding and accepted credential forms.
func TestAPIKeyAuthEnforcesQuotaAndCredentials(t *testing.T) {
	group := &routing.Group{
		ID:       42,
		Name:     "sub",
		Status:   billingcore.StatusActive,
		Hydrated: true,
	}
	user := &identity.User{
		ID:          7,
		Role:        identity.RoleUser,
		Status:      billingcore.StatusActive,
		Balance:     10,
		Concurrency: 3,
	}
	apiKey := &apikey.APIKey{
		ID:     100,
		UserID: user.ID,
		Key:    "test-key",
		Status: billingcore.StatusActive,
		User:   user,
		Group:  group,
	}
	apiKey.GroupID = &group.ID

	apiKeyRepo := &stubApiKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			if key != apiKey.Key {
				return nil, apikey.ErrAPIKeyNotFound
			}
			clone := *apiKey
			return &clone, nil
		},
	}

	t.Run("completes_maintenance_before_request", func(t *testing.T) {
		cfg := &config.Config{}

		apiKeyService := testkit.NewService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
		apiKeyService.Start()

		past := time.Now().Add(-48 * time.Hour)
		sub := &billingcore.UserSubscription{
			ID:                 55,
			UserID:             user.ID,
			PlanID:             group.ID,
			Status:             billingcore.SubscriptionStatusActive,
			ExpiresAt:          time.Now().Add(24 * time.Hour),
			DailyWindowStart:   &past,
			WeeklyWindowStart:  &past,
			MonthlyWindowStart: &past,
			DailyUsageUSD:      0,
		}
		// Buffer the maintenance signal because resetDaily must finish before the
		// request returns, while the assertion receives the signal only after that
		// response.
		maintenanceCalled := make(chan struct{}, 1)
		subscriptionRepo := &stubUserSubscriptionRepo{
			getByID: func(ctx context.Context, id int64) (*billingcore.UserSubscription, error) {
				clone := *sub
				return &clone, nil
			},
			listActive: func(ctx context.Context, userID int64) ([]billingcore.UserSubscription, error) {
				clone := *sub
				return []billingcore.UserSubscription{clone}, nil
			},
			updateStatus:   func(ctx context.Context, subscriptionID int64, status string) error { return nil },
			activateWindow: func(ctx context.Context, id int64, start time.Time) error { return nil },
			// This callback mutates the shared fixture before signaling completion. The
			// buffered signal lets the synchronous authorization check return before its
			// assertion receives that observation.
			resetDaily: func(ctx context.Context, id int64, start time.Time) error {
				sub.DailyWindowStart = &start
				sub.DailyUsageUSD = 0
				maintenanceCalled <- struct{}{}
				return nil
			},
			resetWeekly: func(ctx context.Context, id int64, start time.Time) error {
				sub.WeeklyWindowStart = &start
				return nil
			},
			resetMonthly: func(ctx context.Context, id int64, start time.Time) error {
				sub.MonthlyWindowStart = &start
				return nil
			},
		}
		subscriptionService := newSubscriptionAuthFixture(subscriptionRepo)
		t.Cleanup(subscriptionService.Stop)

		router := newAuthTestRouter(apiKeyService, subscriptionService, cfg)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/t", nil)
		req.Header.Set("x-api-key", apiKey.Key)
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		// Observe the completed reset after the synchronous request. Bound the
		// receive wait so a missing maintenance callback reports a test failure
		// instead of hanging the suite.
		select {
		case <-maintenanceCalled:
		case <-time.After(time.Second):
			t.Fatalf("expected maintenance to complete before response")
		}
	})

	t.Run("revalidates_cas_loser_from_database", func(t *testing.T) {
		cfg := &config.Config{}
		apiKeyService := testkit.NewService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
		apiKeyService.Start()

		past := time.Now().Add(-48 * time.Hour)
		current := time.Now()
		stale := &billingcore.UserSubscription{
			ID:                 56,
			UserID:             user.ID,
			PlanID:             group.ID,
			Status:             billingcore.SubscriptionStatusActive,
			ExpiresAt:          current.Add(24 * time.Hour),
			DailyWindowStart:   &past,
			WeeklyWindowStart:  &past,
			MonthlyWindowStart: &past,
			DailyUsageUSD:      10,
		}
		dailyLimit := 1.0
		stale.DailyLimitUSD = &dailyLimit
		fresh := *stale
		fresh.DailyWindowStart = &current
		fresh.WeeklyWindowStart = &current
		fresh.MonthlyWindowStart = &current
		fresh.DailyUsageUSD = 2

		// Return the stale candidate during selection and fresh data during reread.
		// Reset callbacks accept maintenance without changing state, modeling the
		// competing-writer observation used by quota revalidation.
		subscriptionRepo := &stubUserSubscriptionRepo{
			listActive: func(context.Context, int64) ([]billingcore.UserSubscription, error) {
				clone := *stale
				return []billingcore.UserSubscription{clone}, nil
			},
			getByID: func(context.Context, int64) (*billingcore.UserSubscription, error) {
				clone := fresh
				return &clone, nil
			},
			resetDaily:   func(context.Context, int64, time.Time) error { return nil },
			resetWeekly:  func(context.Context, int64, time.Time) error { return nil },
			resetMonthly: func(context.Context, int64, time.Time) error { return nil },
		}
		subscriptionService := newSubscriptionAuthFixture(subscriptionRepo)
		router := newAuthTestRouter(apiKeyService, subscriptionService, cfg)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/t", nil)
		req.Header.Set("x-api-key", apiKey.Key)
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusTooManyRequests, w.Code)
	})

	t.Run("旧配置仍检查配额", func(t *testing.T) {
		// An exhausted key quota rejects the request before upstream execution.
		// This key is shared by sibling subtests. Save both quota values before
		// exhaustion and restore them afterward so later credential cases start with
		// their intended budget.
		originalQuota, originalUsed := apiKey.Quota, apiKey.QuotaUsed
		apiKey.Quota, apiKey.QuotaUsed = 1, 1
		defer func() { apiKey.Quota, apiKey.QuotaUsed = originalQuota, originalUsed }()
		// The key is shared across these serial subtests. Restore its billing mode
		// after selecting balance so subscription fixtures in later cases keep their
		// own intended funding choice.
		originalBillingMode := apiKey.BillingMode
		apiKey.BillingMode = apikey.APIKeyBillingModeBalance
		defer func() { apiKey.BillingMode = originalBillingMode }()
		cfg := &config.Config{}
		apiKeyService := testkit.NewService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
		apiKeyService.Start()
		subscriptionService := newSubscriptionAuthFixture(&stubUserSubscriptionRepo{})
		router := newAuthTestRouter(apiKeyService, subscriptionService, cfg)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/t", nil)
		req.Header.Set("x-api-key", apiKey.Key)
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusTooManyRequests, w.Code)
	})

	t.Run("接受小写Bearer凭据", func(t *testing.T) {
		// The key is shared across these serial subtests. Restore its billing mode
		// after selecting balance so subscription fixtures in later cases keep their
		// own intended funding choice.
		originalBillingMode := apiKey.BillingMode
		apiKey.BillingMode = apikey.APIKeyBillingModeBalance
		defer func() { apiKey.BillingMode = originalBillingMode }()
		cfg := &config.Config{}
		apiKeyService := testkit.NewService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
		apiKeyService.Start()
		subscriptionService := newSubscriptionAuthFixture(&stubUserSubscriptionRepo{})
		router := newAuthTestRouter(apiKeyService, subscriptionService, cfg)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/t", nil)
		req.Header.Set("Authorization", "bearer "+apiKey.Key)
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("falls_back_to_balance_when_subscription_is_exhausted", func(t *testing.T) {
		cfg := &config.Config{}
		apiKeyService := testkit.NewService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
		apiKeyService.Start()

		now := time.Now()
		dailyLimit := 1.0
		sub := &billingcore.UserSubscription{
			ID:               55,
			UserID:           user.ID,
			PlanID:           group.ID,
			Status:           billingcore.SubscriptionStatusActive,
			ExpiresAt:        now.Add(24 * time.Hour),
			DailyWindowStart: &now,
			DailyLimitUSD:    &dailyLimit,
			DailyUsageUSD:    10,
		}
		subscriptionRepo := &stubUserSubscriptionRepo{
			listActive: func(ctx context.Context, userID int64) ([]billingcore.UserSubscription, error) {
				if userID != sub.UserID {
					return nil, nil
				}
				clone := *sub
				return []billingcore.UserSubscription{clone}, nil
			},
			updateStatus:   func(ctx context.Context, subscriptionID int64, status string) error { return nil },
			activateWindow: func(ctx context.Context, id int64, start time.Time) error { return nil },
			resetDaily:     func(ctx context.Context, id int64, start time.Time) error { return nil },
			resetWeekly:    func(ctx context.Context, id int64, start time.Time) error { return nil },
			resetMonthly:   func(ctx context.Context, id int64, start time.Time) error { return nil },
		}
		subscriptionService := newSubscriptionAuthFixture(subscriptionRepo)
		router := newAuthTestRouter(apiKeyService, subscriptionService, cfg)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/t", nil)
		req.Header.Set("x-api-key", apiKey.Key)
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
	})
}

// TestAPIKeyAuthPreferredSubscriptionRejectsPlanRestrictedGroup binds a key to
// a group outside its preferred plan's group list. The request is forbidden
// with the preferred-subscription group reason even though the wallet has
// balance.
func TestAPIKeyAuthPreferredSubscriptionRejectsPlanRestrictedGroup(t *testing.T) {
	now := time.Now()
	group := &routing.Group{ID: 9, Status: billingcore.StatusActive, Hydrated: true}
	user := &identity.User{ID: 7, Status: billingcore.StatusActive, Role: identity.RoleUser, Balance: 100}
	preferredID := int64(55)
	apiKey := &apikey.APIKey{
		ID:                      100,
		UserID:                  user.ID,
		Key:                     "preferred-plan-group",
		Status:                  apikey.StatusAPIKeyActive,
		GroupID:                 &group.ID,
		Group:                   group,
		User:                    user,
		BillingMode:             apikey.APIKeyBillingModeSubscription,
		PreferredSubscriptionID: &preferredID,
	}
	subscription := &billingcore.UserSubscription{
		ID: preferredID, UserID: user.ID, PlanID: 1, Status: billingcore.SubscriptionStatusActive,
		StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
		Plan: &billingcore.SubscriptionPlan{ID: 1, GroupIDs: []int64{8}},
	}
	repo := &stubApiKeyRepo{getByKey: func(_ context.Context, key string) (*apikey.APIKey, error) {
		if key != apiKey.Key {
			return nil, apikey.ErrAPIKeyNotFound
		}
		copyKey := *apiKey
		return &copyKey, nil
	}}
	subscriptionRepo := &stubUserSubscriptionRepo{getByID: func(_ context.Context, id int64) (*billingcore.UserSubscription, error) {
		if id != preferredID {
			return nil, billingcore.ErrSubscriptionNotFound
		}
		copySubscription := *subscription
		return &copySubscription, nil
	}}
	cfg := &config.Config{}
	apiKeyService := testkit.NewService(repo, nil, nil, nil, nil, nil, cfg)
	apiKeyService.Start()
	subscriptionService := newSubscriptionAuthFixture(subscriptionRepo)
	router := newAuthTestRouter(apiKeyService, subscriptionService, cfg)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.Header.Set("x-api-key", apiKey.Key)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
	requireAPIKeyAuthError(t, w, "PREFERRED_SUBSCRIPTION_GROUP_NOT_ALLOWED", apikey.ErrPreferredSubscriptionGroup.Error())
}

// TestAPIKeyAuthPreferredSubscriptionDoesNotFallBackAfterQuotaExhaustion
// exhausts the explicitly selected plan while retaining positive balance.
// Authorization returns the subscription usage-limit error and keeps that
// preference decisive.
func TestAPIKeyAuthPreferredSubscriptionDoesNotFallBackAfterQuotaExhaustion(t *testing.T) {
	now := time.Now()
	group := &routing.Group{ID: 9, Status: billingcore.StatusActive, Hydrated: true}
	user := &identity.User{ID: 7, Status: billingcore.StatusActive, Role: identity.RoleUser, Balance: 100}
	preferredID := int64(55)
	dailyLimit := 1.0
	apiKey := &apikey.APIKey{
		ID:                      100,
		UserID:                  user.ID,
		Key:                     "preferred-plan-exhausted",
		Status:                  apikey.StatusAPIKeyActive,
		GroupID:                 &group.ID,
		Group:                   group,
		User:                    user,
		BillingMode:             apikey.APIKeyBillingModeSubscription,
		PreferredSubscriptionID: &preferredID,
	}
	subscription := &billingcore.UserSubscription{
		ID: preferredID, UserID: user.ID, PlanID: 1, Status: billingcore.SubscriptionStatusActive,
		StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
		DailyWindowStart: &now, DailyLimitUSD: &dailyLimit, DailyUsageUSD: dailyLimit,
		Plan: &billingcore.SubscriptionPlan{ID: 1},
	}
	repo := &stubApiKeyRepo{getByKey: func(_ context.Context, key string) (*apikey.APIKey, error) {
		if key != apiKey.Key {
			return nil, apikey.ErrAPIKeyNotFound
		}
		copyKey := *apiKey
		return &copyKey, nil
	}}
	subscriptionRepo := &stubUserSubscriptionRepo{getByID: func(_ context.Context, id int64) (*billingcore.UserSubscription, error) {
		if id != preferredID {
			return nil, billingcore.ErrSubscriptionNotFound
		}
		copySubscription := *subscription
		return &copySubscription, nil
	}}
	cfg := &config.Config{}
	apiKeyService := testkit.NewService(repo, nil, nil, nil, nil, nil, cfg)
	apiKeyService.Start()
	subscriptionService := newSubscriptionAuthFixture(subscriptionRepo)
	router := newAuthTestRouter(apiKeyService, subscriptionService, cfg)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.Header.Set("x-api-key", apiKey.Key)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusTooManyRequests, w.Code)
	requireAPIKeyAuthError(t, w, "USAGE_LIMIT_EXCEEDED", billingcore.ErrDailyLimitExceeded.Error())
}

// TestAPIKeyAuthUsageKeepsPreferredSubscriptionSource reads both usage aliases
// using a valid preferred plan. Each handler receives the subscription source,
// matching subscription ID, and an available funding flag.
func TestAPIKeyAuthUsageKeepsPreferredSubscriptionSource(t *testing.T) {
	now := time.Now()
	group := &routing.Group{ID: 9, Status: billingcore.StatusActive, Hydrated: true}
	user := &identity.User{ID: 7, Status: billingcore.StatusActive, Role: identity.RoleUser, Balance: 100}
	preferredID := int64(55)
	apiKey := &apikey.APIKey{
		ID:                      100,
		UserID:                  user.ID,
		Key:                     "usage-preferred-plan",
		Status:                  apikey.StatusAPIKeyActive,
		GroupID:                 &group.ID,
		Group:                   group,
		User:                    user,
		BillingMode:             apikey.APIKeyBillingModeSubscription,
		PreferredSubscriptionID: &preferredID,
	}
	subscription := &billingcore.UserSubscription{
		ID: preferredID, UserID: user.ID, PlanID: 1, Status: billingcore.SubscriptionStatusActive,
		StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
		Plan: &billingcore.SubscriptionPlan{ID: 1, GroupIDs: []int64{group.ID}},
	}
	apiKeyRepo := &stubApiKeyRepo{getByKey: func(_ context.Context, key string) (*apikey.APIKey, error) {
		if key != apiKey.Key {
			return nil, apikey.ErrAPIKeyNotFound
		}
		copyKey := *apiKey
		return &copyKey, nil
	}}
	subscriptionRepo := &stubUserSubscriptionRepo{getByID: func(_ context.Context, id int64) (*billingcore.UserSubscription, error) {
		if id != preferredID {
			return nil, billingcore.ErrSubscriptionNotFound
		}
		copySubscription := *subscription
		return &copySubscription, nil
	}}
	cfg := &config.Config{}
	apiKeyService := testkit.NewService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
	apiKeyService.Start()
	subscriptionService := newSubscriptionAuthFixture(subscriptionRepo)
	router := gin.New()
	router.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(apiKeyService, subscriptionService, cfg)))
	usage := func(c *gin.Context) {
		billing, ok := gatewayhttp.GetAPIKeyBillingContext(c)
		if !ok || billing == nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"source":          billing.Source,
			"subscription_id": billing.Subscription.ID,
			"available":       billing.Available,
		})
	}
	router.GET("/v1/usage", usage)
	router.GET("/antigravity/v1/usage", usage)

	for _, path := range []string{"/v1/usage", "/antigravity/v1/usage"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("x-api-key", apiKey.Key)
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code, path)
		var response struct {
			Source         string `json:"source"`          // selected funding source name
			SubscriptionID int64  `json:"subscription_id"` // selected subscription record ID
			Available      bool   `json:"available"`       // selected funding availability flag
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response), path)
		require.Equal(t, "subscription", response.Source, path)
		require.Equal(t, preferredID, response.SubscriptionID, path)
		require.True(t, response.Available, path)
	}
}

// TestAPIKeyAuthAntigravityUsageKeepsUnavailablePreferredSubscription reads an
// existing usage endpoint after the chosen subscription expired. Its successful
// handler retains that subscription source and ID with availability false.
func TestAPIKeyAuthAntigravityUsageKeepsUnavailablePreferredSubscription(t *testing.T) {
	now := time.Now()
	group := &routing.Group{ID: 9, Status: billingcore.StatusActive, Hydrated: true}
	user := &identity.User{ID: 7, Status: billingcore.StatusActive, Role: identity.RoleUser, Balance: 100}
	preferredID := int64(55)
	apiKey := &apikey.APIKey{
		ID:                      100,
		UserID:                  user.ID,
		Key:                     "antigravity-usage-unavailable-preferred-plan",
		Status:                  apikey.StatusAPIKeyActive,
		GroupID:                 &group.ID,
		Group:                   group,
		User:                    user,
		BillingMode:             apikey.APIKeyBillingModeSubscription,
		PreferredSubscriptionID: &preferredID,
	}
	subscription := &billingcore.UserSubscription{
		ID: preferredID, UserID: user.ID, PlanID: 1, Status: billingcore.SubscriptionStatusActive,
		StartsAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour),
		Plan: &billingcore.SubscriptionPlan{ID: 1, GroupIDs: []int64{group.ID}},
	}
	apiKeyRepo := &stubApiKeyRepo{getByKey: func(_ context.Context, key string) (*apikey.APIKey, error) {
		if key != apiKey.Key {
			return nil, apikey.ErrAPIKeyNotFound
		}
		copyKey := *apiKey
		return &copyKey, nil
	}}
	subscriptionRepo := &stubUserSubscriptionRepo{getByID: func(_ context.Context, id int64) (*billingcore.UserSubscription, error) {
		if id != preferredID {
			return nil, billingcore.ErrSubscriptionNotFound
		}
		copySubscription := *subscription
		return &copySubscription, nil
	}}
	cfg := &config.Config{}
	apiKeyService := testkit.NewService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
	apiKeyService.Start()
	subscriptionService := newSubscriptionAuthFixture(subscriptionRepo)
	router := gin.New()
	router.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(apiKeyService, subscriptionService, cfg)))
	var got *billingcore.APIKeyBillingContext
	router.GET("/antigravity/v1/usage", func(c *gin.Context) {
		got, _ = gatewayhttp.GetAPIKeyBillingContext(c)
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/antigravity/v1/usage", nil)
	req.Header.Set("x-api-key", apiKey.Key)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, got)
	require.Equal(t, "subscription", got.Source)
	require.NotNil(t, got.Subscription)
	require.Equal(t, preferredID, got.Subscription.ID)
	require.False(t, got.Available)
}

// TestAPIKeyAuthRejectsUnboundGroupBeforeSubscriptionSelection supplies a
// preferred plan but leaves the ordinary key unbound. Authorization returns
// GROUP_REQUIRED before that plan can supply a request group.
func TestAPIKeyAuthRejectsUnboundGroupBeforeSubscriptionSelection(t *testing.T) {
	now := time.Now()
	user := &identity.User{ID: 7, Status: billingcore.StatusActive, Role: identity.RoleUser, Balance: 100}
	preferredID := int64(55)
	apiKey := &apikey.APIKey{
		ID:                      100,
		UserID:                  user.ID,
		Key:                     "preferred-plan-without-bound-group",
		Status:                  apikey.StatusAPIKeyActive,
		User:                    user,
		BillingMode:             apikey.APIKeyBillingModeSubscription,
		PreferredSubscriptionID: &preferredID,
	}
	subscription := &billingcore.UserSubscription{
		ID: preferredID, UserID: user.ID, PlanID: 1, Status: billingcore.SubscriptionStatusActive,
		StartsAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
		Plan: &billingcore.SubscriptionPlan{ID: 1, GroupIDs: []int64{9}},
	}
	apiKeyRepo := &stubApiKeyRepo{getByKey: func(_ context.Context, key string) (*apikey.APIKey, error) {
		if key != apiKey.Key {
			return nil, apikey.ErrAPIKeyNotFound
		}
		copyKey := *apiKey
		return &copyKey, nil
	}}
	subscriptionRepo := &stubUserSubscriptionRepo{getByID: func(_ context.Context, id int64) (*billingcore.UserSubscription, error) {
		if id != preferredID {
			return nil, billingcore.ErrSubscriptionNotFound
		}
		copySubscription := *subscription
		return &copySubscription, nil
	}}
	cfg := &config.Config{}
	apiKeyService := testkit.NewService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
	apiKeyService.Start()
	subscriptionService := newSubscriptionAuthFixture(subscriptionRepo)
	router := newAuthTestRouter(apiKeyService, subscriptionService, cfg)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.Header.Set("x-api-key", apiKey.Key)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), "GROUP_REQUIRED")
}

// TestAPIKeyAuthSetsGroupContext authenticates an active grouped key whose key
// policy forces Fast off. Successful handling requires the expected group ID,
// payer identity, and Fast-off policy in the access snapshot.
func TestAPIKeyAuthSetsGroupContext(t *testing.T) {
	group := &routing.Group{
		ID:     101,
		Name:   "g1",
		Status: billingcore.StatusActive,

		Hydrated: true,
	}
	user := &identity.User{
		ID:          7,
		Role:        identity.RoleUser,
		Status:      billingcore.StatusActive,
		Balance:     10,
		Concurrency: 3,
	}
	apiKey := &apikey.APIKey{
		ID:             100,
		UserID:         user.ID,
		Key:            "test-key",
		Status:         billingcore.StatusActive,
		FastModePolicy: apikey.APIKeyFastModePolicyForceOff,
		User:           user,
		Group:          group,
	}
	apiKey.GroupID = &group.ID

	apiKeyRepo := &stubApiKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			if key != apiKey.Key {
				return nil, apikey.ErrAPIKeyNotFound
			}
			clone := *apiKey
			return &clone, nil
		},
	}

	cfg := &config.Config{}
	apiKeyService := testkit.NewService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
	apiKeyService.Start()
	router := gin.New()
	router.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(apiKeyService, nil, cfg)))
	router.GET("/t", func(c *gin.Context) {
		groupFromCtx, ok := requeststate.GroupFromContext(c.Request.Context())
		if !ok || groupFromCtx == nil || groupFromCtx.ID != group.ID {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false})
			return
		}
		access, ok := apikey.AccessSnapshotFromContext(c.Request.Context())
		if !ok || access.PayerUserID != user.ID {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false})
			return
		}
		if access.FastModePolicy() != apikey.APIKeyFastModePolicyForceOff {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.Header.Set("x-api-key", apiKey.Key)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
}

// TestAPIKeyAuthRejectsExclusiveGroupWhenUserNoLongerAllowed supplies an active
// exclusive group whose owner lacks allowed-group membership. The request is
// forbidden with GROUP_NOT_ALLOWED before its successful fixture endpoint.
func TestAPIKeyAuthRejectsExclusiveGroupWhenUserNoLongerAllowed(t *testing.T) {
	group := &routing.Group{
		ID:          202,
		Name:        "exclusive",
		Status:      billingcore.StatusActive,
		IsExclusive: true,
		Hydrated:    true,
	}
	user := &identity.User{
		ID:            7,
		Role:          identity.RoleUser,
		Status:        billingcore.StatusActive,
		Balance:       10,
		Concurrency:   3,
		AllowedGroups: []int64{},
	}
	apiKey := &apikey.APIKey{
		ID:     100,
		UserID: user.ID,
		Key:    "test-key",
		Status: billingcore.StatusActive,
		User:   user,
		Group:  group,
	}
	apiKey.GroupID = &group.ID

	apiKeyRepo := &stubApiKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			if key != apiKey.Key {
				return nil, apikey.ErrAPIKeyNotFound
			}
			clone := *apiKey
			return &clone, nil
		},
	}

	cfg := &config.Config{}
	apiKeyService := testkit.NewService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
	apiKeyService.Start()
	router := newAuthTestRouter(apiKeyService, nil, cfg)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.Header.Set("x-api-key", apiKey.Key)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), "GROUP_NOT_ALLOWED")
}

// TestAPIKeyAuthOverwritesInvalidContextGroup supplies an unhydrated context
// group alongside the key's valid hydrated group. The downstream handler
// receives the valid key group with a different record pointer.
func TestAPIKeyAuthOverwritesInvalidContextGroup(t *testing.T) {
	group := &routing.Group{
		ID:     101,
		Name:   "g1",
		Status: billingcore.StatusActive,

		Hydrated: true,
	}
	user := &identity.User{
		ID:          7,
		Role:        identity.RoleUser,
		Status:      billingcore.StatusActive,
		Balance:     10,
		Concurrency: 3,
	}
	apiKey := &apikey.APIKey{
		ID:     100,
		UserID: user.ID,
		Key:    "test-key",
		Status: billingcore.StatusActive,
		User:   user,
		Group:  group,
	}
	apiKey.GroupID = &group.ID

	apiKeyRepo := &stubApiKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			if key != apiKey.Key {
				return nil, apikey.ErrAPIKeyNotFound
			}
			clone := *apiKey
			return &clone, nil
		},
	}

	cfg := &config.Config{}
	apiKeyService := testkit.NewService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
	apiKeyService.Start()
	router := gin.New()
	router.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(apiKeyService, nil, cfg)))

	invalidGroup := &routing.Group{
		ID: group.ID,

		Status: group.Status,
	}
	router.GET("/t", func(c *gin.Context) {
		groupFromCtx, ok := requeststate.GroupFromContext(c.Request.Context())
		if !ok || groupFromCtx == nil || groupFromCtx.ID != group.ID || !groupFromCtx.Hydrated || groupFromCtx == invalidGroup {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.Header.Set("x-api-key", apiKey.Key)
	req = req.WithContext(requeststate.WithGroup(req.Context(), invalidGroup))
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
}

// TestAPIKeyAuthRejectsUnavailableGroup varies active, disabled, deleted, and
// missing bound groups. Forbidden cases retain their expected group rejection
// and business-limit markers, while the active group reaches the endpoint.
func TestAPIKeyAuthRejectsUnavailableGroup(t *testing.T) {
	groupID := int64(101)
	user := &identity.User{
		ID:          7,
		Role:        identity.RoleUser,
		Status:      billingcore.StatusActive,
		Balance:     10,
		Concurrency: 3,
	}

	tests := []struct {
		name       string              // authorization scenario label
		group      *routing.Group      // bound group state fixture
		wantStatus int                 // expected response HTTP status
		wantCode   string              // expected protocol reason code
		wantMarked bool                // expected business-limit marker presence
		wantReject IngressRejectReason // expected admission rejection reason
	}{
		{
			name: "active group passes",
			group: &routing.Group{
				ID:     groupID,
				Name:   "active",
				Status: billingcore.StatusActive,

				Hydrated: true,
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "disabled group is forbidden",
			group: &routing.Group{
				ID:     groupID,
				Name:   "disabled",
				Status: billingcore.StatusDisabled,

				Hydrated: true,
			},
			wantStatus: http.StatusForbidden,
			wantCode:   "GROUP_DISABLED",
			wantMarked: true,
			wantReject: IngressRejectGroupDisabled,
		},
		{
			name: "deleted status group is forbidden",
			group: &routing.Group{
				ID:     groupID,
				Name:   "deleted",
				Status: "deleted",

				Hydrated: true,
			},
			wantStatus: http.StatusForbidden,
			wantCode:   "GROUP_DELETED",
			wantMarked: true,
			wantReject: IngressRejectGroupDeleted,
		},
		{
			name:       "missing group edge is forbidden",
			group:      nil,
			wantStatus: http.StatusForbidden,
			wantCode:   "GROUP_DELETED",
			wantMarked: true,
			wantReject: IngressRejectGroupDeleted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apiKey := &apikey.APIKey{
				ID:      100,
				UserID:  user.ID,
				GroupID: &groupID,
				Key:     "test-key",
				Status:  billingcore.StatusActive,
				User:    user,
				Group:   tt.group,
			}
			apiKeyRepo := &stubApiKeyRepo{
				getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
					if key != apiKey.Key {
						return nil, apikey.ErrAPIKeyNotFound
					}
					clone := *apiKey
					return &clone, nil
				},
			}
			cfg := &config.Config{}
			apiKeyService := testkit.NewService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
			apiKeyService.Start()
			router := gin.New()
			var markedBusinessLimited bool
			var businessLimitedReason string
			var rejectReason IngressRejectReason
			var rejected bool
			router.Use(func(c *gin.Context) {
				c.Next()
				markedBusinessLimited = gatewayhttp.HasOpsClientBusinessLimited(c)
				rejectReason, rejected = GetIngressRejectReason(c)
				if v, ok := c.Get(gatewayhttp.OpsClientBusinessLimitedReasonKey); ok {
					businessLimitedReason, _ = v.(string)
				}
			})
			router.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(apiKeyService, nil, cfg)))
			router.GET("/t", func(c *gin.Context) {
				c.JSON(http.StatusOK, gin.H{"ok": true})
			})

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/t", nil)
			req.Header.Set("x-api-key", apiKey.Key)
			router.ServeHTTP(w, req)

			require.Equal(t, tt.wantStatus, w.Code)
			if tt.wantCode != "" {
				require.Contains(t, w.Body.String(), tt.wantCode)
			}
			require.Equal(t, tt.wantMarked, markedBusinessLimited)
			require.Equal(t, tt.wantReject != "", rejected)
			require.Equal(t, tt.wantReject, rejectReason)
			if tt.wantMarked {
				require.Equal(t, gatewayhttp.OpsClientBusinessLimitedReasonAPIKeyGroupUnavailable, businessLimitedReason)
			}
		})
	}
}

// TestAPIKeyAuthRejectsUserDisabledPublicGroup gives an active public group to
// a user whose loaded restrictions disable it. Authorization returns the
// user-group denial code before endpoint handling.
func TestAPIKeyAuthRejectsUserDisabledPublicGroup(t *testing.T) {
	groupID := int64(101)
	user := &identity.User{
		ID:                      7,
		Role:                    identity.RoleUser,
		Status:                  billingcore.StatusActive,
		Balance:                 10,
		Concurrency:             3,
		DisabledPublicGroups:    []int64{groupID},
		GroupRestrictionsLoaded: true,
	}
	group := &routing.Group{
		ID:     groupID,
		Name:   "public",
		Status: billingcore.StatusActive,

		Hydrated: true,
	}
	apiKey := &apikey.APIKey{
		ID:      100,
		UserID:  user.ID,
		GroupID: &groupID,
		Key:     "test-key",
		Status:  billingcore.StatusActive,
		User:    user,
		Group:   group,
	}
	apiKeyRepo := &stubApiKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			if key != apiKey.Key {
				return nil, apikey.ErrAPIKeyNotFound
			}
			clone := *apiKey
			return &clone, nil
		},
	}
	cfg := &config.Config{}
	apiKeyService := testkit.NewService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
	apiKeyService.Start()
	router := gin.New()
	router.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(apiKeyService, nil, cfg)))
	router.GET("/t", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.Header.Set("x-api-key", apiKey.Key)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), "GROUP_DISABLED_FOR_USER")
}

// TestAPIKeyAuthMarksOnlyExpectedIngressRejections varies missing or malformed
// credentials, credential lookup errors, and lookup overload. Expected
// admission failures receive markers, while the operational database error
// remains an unmarked 500.
func TestAPIKeyAuthMarksOnlyExpectedIngressRejections(t *testing.T) {
	tests := []struct {
		name       string              // authorization scenario label
		path       string              // request path under test
		key        string              // supplied credential text
		authHeader string              // supplied Authorization header value
		repoErr    error               // injected credential lookup failure
		wantStatus int                 // expected response HTTP status
		wantCode   string              // expected protocol reason code
		wantReason IngressRejectReason // expected admission rejection reason
	}{
		{
			name:       "query key deprecated",
			path:       "/t?key=legacy",
			wantStatus: http.StatusBadRequest,
			wantCode:   "api_key_in_query_deprecated",
			wantReason: IngressRejectQueryAPIKeyDeprecated,
		},
		{
			name:       "missing key",
			path:       "/t",
			wantStatus: http.StatusUnauthorized,
			wantCode:   "API_KEY_REQUIRED",
			wantReason: IngressRejectAPIKeyRequired,
		},
		{
			name:       "malformed authorization",
			path:       "/t",
			authHeader: "Basic not-a-bearer-key",
			wantStatus: http.StatusUnauthorized,
			wantCode:   "API_KEY_REQUIRED",
			wantReason: IngressRejectInvalidAPIKey,
		},
		{
			name:       "oversized key",
			path:       "/t",
			key:        strings.Repeat("x", apikey.MaxAPIKeyCredentialBytes+1),
			wantStatus: http.StatusUnauthorized,
			wantCode:   "INVALID_API_KEY",
			wantReason: IngressRejectInvalidAPIKey,
		},
		{
			name:       "invalid key",
			path:       "/t",
			key:        "invalid",
			repoErr:    apikey.ErrAPIKeyNotFound,
			wantStatus: http.StatusUnauthorized,
			wantCode:   "INVALID_API_KEY",
			wantReason: IngressRejectInvalidAPIKey,
		},
		{
			name:       "repository failure remains operational error",
			path:       "/t",
			key:        "valid-shape",
			repoErr:    errors.New("database unavailable"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
		},
		{
			name:       "auth lookup bulkhead rejection is an admission rejection",
			path:       "/t",
			key:        "valid-shape",
			repoErr:    apikey.ErrAPIKeyAuthOverloaded,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "API_KEY_AUTH_OVERLOADED",
			wantReason: IngressRejectAPIKeyAuthOverloaded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &stubApiKeyRepo{getByKey: func(context.Context, string) (*apikey.APIKey, error) {
				return nil, tt.repoErr
			}}
			cfg := &config.Config{}
			apiKeyService := testkit.NewService(repo, nil, nil, nil, nil, nil, cfg)
			apiKeyService.Start()
			router := gin.New()
			var reason IngressRejectReason
			var rejected bool
			router.Use(func(c *gin.Context) {
				c.Next()
				reason, rejected = GetIngressRejectReason(c)
			})
			router.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(apiKeyService, nil, cfg)))
			router.GET("/t", func(c *gin.Context) { c.Status(http.StatusOK) })

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if tt.key != "" {
				req.Header.Set("x-api-key", tt.key)
			}
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			router.ServeHTTP(w, req)

			require.Equal(t, tt.wantStatus, w.Code)
			require.Contains(t, w.Body.String(), tt.wantCode)
			require.Equal(t, tt.wantReason != "", rejected)
			require.Equal(t, tt.wantReason, reason)
		})
	}
}

// TestAPIKeyAuthSetsOpsFallbackKeyOnEarlyAbort loads a valid key whose group is
// disabled. The forbidden request retains its key, user, and group data through
// the diagnostic fallback for later observation.
func TestAPIKeyAuthSetsOpsFallbackKeyOnEarlyAbort(t *testing.T) {
	groupID := int64(101)
	user := &identity.User{
		ID:          7,
		Role:        identity.RoleUser,
		Status:      billingcore.StatusActive,
		Balance:     10,
		Concurrency: 3,
	}
	apiKey := &apikey.APIKey{
		ID:      100,
		UserID:  user.ID,
		GroupID: &groupID,
		Key:     "test-key",
		Status:  billingcore.StatusActive,
		User:    user,
		Group: &routing.Group{
			ID:     groupID,
			Name:   "disabled",
			Status: billingcore.StatusDisabled,

			Hydrated: true,
		},
	}
	apiKeyRepo := &stubApiKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			if key != apiKey.Key {
				return nil, apikey.ErrAPIKeyNotFound
			}
			clone := *apiKey
			return &clone, nil
		},
	}
	cfg := &config.Config{}
	apiKeyService := testkit.NewService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
	apiKeyService.Start()

	router := gin.New()
	var fallback *apikey.APIKey
	var fallbackOK bool
	router.Use(func(c *gin.Context) {
		c.Next()
		fallback, fallbackOK = keyhttp.GetOpsFallbackAPIKey(c)
	})
	router.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(apiKeyService, nil, cfg)))
	router.GET("/t", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.Header.Set("x-api-key", apiKey.Key)
	router.ServeHTTP(w, req)

	// The early-abort diagnostic copy carries key ownership even though the
	// disabled group prevents this request from reaching gateway execution.
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), "GROUP_DISABLED")
	require.True(t, fallbackOK, "鉴权早退时也应写入 ops fallback api key")
	require.NotNil(t, fallback)
	require.Equal(t, apiKey.ID, fallback.ID)
	require.NotNil(t, fallback.User)
	require.Equal(t, user.ID, fallback.User.ID)
	require.NotNil(t, fallback.GroupID)
	require.Equal(t, groupID, *fallback.GroupID)
	require.NotNil(t, fallback.Group)
}

// TestAPIKeyAuthGoogleSetsOpsFallbackKeyOnEarlyAbort loads a Google credential
// before its disabled group is rejected. The request remains forbidden while
// its diagnostic key copy keeps key and user attribution.
func TestAPIKeyAuthGoogleSetsOpsFallbackKeyOnEarlyAbort(t *testing.T) {
	groupID := int64(202)
	user := &identity.User{
		ID:          9,
		Role:        identity.RoleUser,
		Status:      billingcore.StatusActive,
		Balance:     10,
		Concurrency: 3,
	}
	apiKey := &apikey.APIKey{
		ID:      200,
		UserID:  user.ID,
		GroupID: &groupID,
		Key:     "g-key",
		Status:  billingcore.StatusActive,
		User:    user,
		Group: &routing.Group{
			ID:     groupID,
			Name:   "disabled",
			Status: billingcore.StatusDisabled,

			Hydrated: true,
		},
	}
	apiKeyRepo := &stubApiKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			if key != apiKey.Key {
				return nil, apikey.ErrAPIKeyNotFound
			}
			clone := *apiKey
			return &clone, nil
		},
	}
	cfg := &config.Config{}
	apiKeyService := testkit.NewService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
	apiKeyService.Start()

	router := gin.New()
	var fallback *apikey.APIKey
	var fallbackOK bool
	router.Use(func(c *gin.Context) {
		c.Next()
		fallback, fallbackOK = keyhttp.GetOpsFallbackAPIKey(c)
	})
	router.Use(APIKeyAuthWithSubscriptionGoogle(apiKeyService, nil, cfg))
	router.GET("/t", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.Header.Set("x-goog-api-key", apiKey.Key)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
	require.True(t, fallbackOK, "Google 鉴权早退时也应写入 ops fallback api key")
	require.NotNil(t, fallback)
	require.Equal(t, apiKey.ID, fallback.ID)
	require.NotNil(t, fallback.User)
	require.Equal(t, user.ID, fallback.User.ID)
}

// TestAPIKeyAuthGoogleRejectsExclusiveGroupWhenUserNoLongerAllowed loads an
// active key in an exclusive group lacking current user permission. The Google
// request is forbidden and carries the business-limit marker.
func TestAPIKeyAuthGoogleRejectsExclusiveGroupWhenUserNoLongerAllowed(t *testing.T) {
	groupID := int64(303)
	user := &identity.User{
		ID:            7,
		Role:          identity.RoleUser,
		Status:        billingcore.StatusActive,
		Balance:       10,
		Concurrency:   3,
		AllowedGroups: []int64{},
	}
	apiKey := &apikey.APIKey{
		ID:      100,
		UserID:  user.ID,
		GroupID: &groupID,
		Key:     "test-key",
		Status:  billingcore.StatusActive,
		User:    user,
		Group: &routing.Group{
			ID:          groupID,
			Name:        "exclusive",
			Status:      billingcore.StatusActive,
			IsExclusive: true,
			Hydrated:    true,
		},
	}
	apiKeyRepo := &stubApiKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			if key != apiKey.Key {
				return nil, apikey.ErrAPIKeyNotFound
			}
			clone := *apiKey
			return &clone, nil
		},
	}
	cfg := &config.Config{}
	apiKeyService := testkit.NewService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
	apiKeyService.Start()

	router := gin.New()
	var markedBusinessLimited bool
	router.Use(func(c *gin.Context) {
		c.Next()
		markedBusinessLimited = gatewayhttp.HasOpsClientBusinessLimited(c)
	})
	router.Use(APIKeyAuthGoogle(apiKeyService, cfg))
	router.GET("/t", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.Header.Set("x-goog-api-key", apiKey.Key)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), "You do not have access to the selected group.")
	require.True(t, markedBusinessLimited)
}

// TestRequireGroupAssignmentMarksUngroupedKeyBusinessLimited injects a loaded
// ordinary key with no group. The group guard returns a Messages forbidden
// response and retains both group-unassigned admission and business-limit
// markers.
func TestRequireGroupAssignmentMarksUngroupedKeyBusinessLimited(t *testing.T) {
	apiKey := &apikey.APIKey{
		ID:     100,
		Key:    "ungrouped-key",
		Status: billingcore.StatusActive,
	}

	router := gin.New()
	var markedBusinessLimited bool
	var businessLimitedReason string
	var rejectReason IngressRejectReason
	var rejected bool
	router.Use(func(c *gin.Context) {
		c.Next()
		markedBusinessLimited = gatewayhttp.HasOpsClientBusinessLimited(c)
		rejectReason, rejected = GetIngressRejectReason(c)
		if v, ok := c.Get(gatewayhttp.OpsClientBusinessLimitedReasonKey); ok {
			businessLimitedReason, _ = v.(string)
		}
	})
	router.Use(func(c *gin.Context) {
		c.Set(string(keyhttp.ContextKeyAPIKey), apiKey)
		c.Next()
	})
	router.Use(RequireGroupAssignment(gatewayhttp.AnthropicErrorWriter))
	router.GET("/t", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), "not assigned to any group")
	require.True(t, rejected)
	require.Equal(t, IngressRejectGroupUnassigned, rejectReason)
	require.True(t, markedBusinessLimited)
	require.Equal(t, gatewayhttp.OpsClientBusinessLimitedReasonAPIKeyGroupUnassigned, businessLimitedReason)
}

// TestAPIKeyAuthUsesExplicitUnavailableFallback toggles an unavailable group's
// configured fallback while the key permits fallback. A configured target
// becomes the effective group, while its absence keeps the disabled-group
// rejection.
func TestAPIKeyAuthUsesExplicitUnavailableFallback(t *testing.T) {
	for _, explicit := range []bool{true, false} {
		t.Run(strconv.FormatBool(explicit), func(t *testing.T) {
			disabledGroupID := int64(101)
			defaultGroupID := int64(202)
			user := &identity.User{
				ID:          7,
				Role:        identity.RoleUser,
				Status:      billingcore.StatusActive,
				Balance:     10,
				Concurrency: 3,
			}
			disabledGroup := &routing.Group{
				UnavailableFallbackGroupID: &defaultGroupID,
				ID:                         disabledGroupID,
				Name:                       "openai-disabled",
				Status:                     billingcore.StatusDisabled,

				Hydrated: true,
			}
			if !explicit {
				disabledGroup.UnavailableFallbackGroupID = nil
			}
			apiKey := &apikey.APIKey{
				ID:                           100,
				UserID:                       user.ID,
				GroupID:                      &disabledGroupID,
				Key:                          "test-key",
				Status:                       billingcore.StatusActive,
				User:                         user,
				Group:                        disabledGroup,
				FallbackWhenGroupUnavailable: true,
			}

			apiKeyRepo := &stubApiKeyRepo{
				getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
					if key != apiKey.Key {
						return nil, apikey.ErrAPIKeyNotFound
					}
					clone := *apiKey
					return &clone, nil
				},
			}
			groupRepo := &stubGroupRepoForAuth{groupsByID: map[int64]routing.Group{
				disabledGroupID: *disabledGroup,
				defaultGroupID:  {ID: defaultGroupID, Name: "openai-default", Status: billingcore.StatusActive, Hydrated: true, RateMultiplier: 1},
			}}

			cfg := &config.Config{}
			apiKeyService := testkit.NewService(apiKeyRepo, nil, groupRepo, nil, nil, nil, cfg)
			apiKeyService.Start()
			router := gin.New()
			router.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(apiKeyService, nil, cfg)))
			router.GET("/t", func(c *gin.Context) {
				currentKey, ok := keyhttp.GetAPIKeyFromContext(c)
				if !ok || currentKey.GroupID == nil || *currentKey.GroupID != defaultGroupID {
					c.JSON(http.StatusInternalServerError, gin.H{"ok": false})
					return
				}
				groupFromCtx, ok := requeststate.GroupFromContext(c.Request.Context())
				if !ok || groupFromCtx == nil || groupFromCtx.ID != defaultGroupID {
					c.JSON(http.StatusInternalServerError, gin.H{"ok": false})
					return
				}
				c.JSON(http.StatusOK, gin.H{"ok": true})
			})

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/t", nil)
			req.Header.Set("x-api-key", apiKey.Key)
			router.ServeHTTP(w, req)

			if explicit {
				require.Equal(t, http.StatusOK, w.Code)
			} else {
				require.Equal(t, http.StatusForbidden, w.Code)
				require.Contains(t, w.Body.String(), "GROUP_DISABLED")
			}
		})
	}
}

// TestAPIKeyAuthRejectsDisabledGroupWhenFallbackDisabled gives the disabled
// group an explicit active fallback but leaves key fallback disabled.
// Authorization keeps the GROUP_DISABLED response rather than rebinding the
// fixture.
func TestAPIKeyAuthRejectsDisabledGroupWhenFallbackDisabled(t *testing.T) {
	disabledGroupID := int64(101)
	defaultGroupID := int64(202)
	user := &identity.User{
		ID:          7,
		Role:        identity.RoleUser,
		Status:      billingcore.StatusActive,
		Balance:     10,
		Concurrency: 3,
	}
	disabledGroup := &routing.Group{
		UnavailableFallbackGroupID: &defaultGroupID,
		ID:                         disabledGroupID,
		Name:                       "openai-disabled",
		Status:                     billingcore.StatusDisabled,

		Hydrated: true,
	}
	apiKey := &apikey.APIKey{
		ID:      100,
		UserID:  user.ID,
		GroupID: &disabledGroupID,
		Key:     "test-key",
		Status:  billingcore.StatusActive,
		User:    user,
		Group:   disabledGroup,
	}

	apiKeyRepo := &stubApiKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			if key != apiKey.Key {
				return nil, apikey.ErrAPIKeyNotFound
			}
			clone := *apiKey
			return &clone, nil
		},
	}
	groupRepo := &stubGroupRepoForAuth{groupsByID: map[int64]routing.Group{
		disabledGroupID: *disabledGroup,
		defaultGroupID:  {ID: defaultGroupID, Name: "explicit-fallback", Status: billingcore.StatusActive, Hydrated: true, RateMultiplier: 1},
	}}

	cfg := &config.Config{}
	apiKeyService := testkit.NewService(apiKeyRepo, nil, groupRepo, nil, nil, nil, cfg)
	apiKeyService.Start()
	router := gin.New()
	router.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(apiKeyService, nil, cfg)))
	router.GET("/t", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.Header.Set("x-api-key", apiKey.Key)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), "GROUP_DISABLED")
}

// TestAPIKeyAuthIPRestrictionUsesTrustedPathWhenSwitchDisabled disables
// compatibility and configures no trusted peer. Forwarded allowlist addresses
// are ignored, and the denial reports the actual client peer with the IP
// business-limit reason.
func TestAPIKeyAuthIPRestrictionUsesTrustedPathWhenSwitchDisabled(t *testing.T) {
	user := &identity.User{
		ID:          7,
		Role:        identity.RoleUser,
		Status:      billingcore.StatusActive,
		Balance:     10,
		Concurrency: 3,
	}
	apiKey := &apikey.APIKey{
		ID:          100,
		UserID:      user.ID,
		Key:         "test-key",
		Status:      billingcore.StatusActive,
		User:        user,
		IPWhitelist: []string{"1.2.3.4"},
	}

	apiKeyRepo := &stubApiKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			if key != apiKey.Key {
				return nil, apikey.ErrAPIKeyNotFound
			}
			clone := *apiKey
			return &clone, nil
		},
	}

	cfg := &config.Config{}
	cfg.SetTrustForwardedHeaders(false)
	apiKeyService := testkit.NewService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
	apiKeyService.Start()
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	var markedBusinessLimited bool
	var businessLimitedReason string
	router.Use(func(c *gin.Context) {
		c.Next()
		markedBusinessLimited = gatewayhttp.HasOpsClientBusinessLimited(c)
		if v, ok := c.Get(gatewayhttp.OpsClientBusinessLimitedReasonKey); ok {
			businessLimitedReason, _ = v.(string)
		}
	})
	router.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(apiKeyService, nil, cfg)))
	router.GET("/t", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.RemoteAddr = "9.9.9.9:12345"
	req.Header.Set("x-api-key", apiKey.Key)
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.Header.Set("X-Real-IP", "1.2.3.4")
	req.Header.Set("CF-Connecting-IP", "1.2.3.4")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
	requireAPIKeyAuthError(t, w, "ACCESS_DENIED", "Access denied. Your IP is 9.9.9.9")
	require.True(t, markedBusinessLimited)
	require.Equal(t, gatewayhttp.OpsClientBusinessLimitedReasonIPRestriction, businessLimitedReason)
}

// TestAPIKeyAuthIPRestrictionIncludesClientIPForBlacklistDenial blacklists the
// direct peer with forwarding trust absent. Authorization returns ACCESS_DENIED
// with that resolved address in the public message.
func TestAPIKeyAuthIPRestrictionIncludesClientIPForBlacklistDenial(t *testing.T) {
	user := &identity.User{
		ID:          7,
		Role:        identity.RoleUser,
		Status:      billingcore.StatusActive,
		Balance:     10,
		Concurrency: 3,
	}
	apiKey := &apikey.APIKey{
		ID:          100,
		UserID:      user.ID,
		Key:         "test-key",
		Status:      billingcore.StatusActive,
		User:        user,
		IPBlacklist: []string{"9.9.9.9"},
	}

	apiKeyRepo := &stubApiKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			if key != apiKey.Key {
				return nil, apikey.ErrAPIKeyNotFound
			}
			clone := *apiKey
			return &clone, nil
		},
	}

	cfg := &config.Config{}
	apiKeyService := testkit.NewService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
	apiKeyService.Start()
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(apiKeyService, nil, cfg)))
	router.GET("/t", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.RemoteAddr = "9.9.9.9:12345"
	req.Header.Set("x-api-key", apiKey.Key)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
	requireAPIKeyAuthError(t, w, "ACCESS_DENIED", "Access denied. Your IP is 9.9.9.9")
}

// TestAPIKeyAuthIPRestrictionUsesConfiguredTrustedProxy disables raw-header
// compatibility and trusts the direct proxy explicitly. Its forwarded
// allowlisted address passes the ACL and reaches the successful endpoint.
func TestAPIKeyAuthIPRestrictionUsesConfiguredTrustedProxy(t *testing.T) {
	user := &identity.User{
		ID:          7,
		Role:        identity.RoleUser,
		Status:      billingcore.StatusActive,
		Balance:     10,
		Concurrency: 3,
	}
	apiKey := &apikey.APIKey{
		ID:          100,
		UserID:      user.ID,
		Key:         "test-key",
		Status:      billingcore.StatusActive,
		User:        user,
		IPWhitelist: []string{"1.2.3.4"},
	}

	bindAuthTestGroup(apiKey)
	apiKeyRepo := &stubApiKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			if key != apiKey.Key {
				return nil, apikey.ErrAPIKeyNotFound
			}
			clone := *apiKey
			return &clone, nil
		},
	}

	cfg := &config.Config{}
	cfg.SetTrustForwardedHeaders(false)
	apiKeyService := testkit.NewService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
	apiKeyService.Start()
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies([]string{"9.9.9.9"}))
	router.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(apiKeyService, nil, cfg)))
	router.GET("/t", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.RemoteAddr = "9.9.9.9:12345"
	req.Header.Set("x-api-key", apiKey.Key)
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.Header.Set("X-Real-IP", "1.2.3.4")
	req.Header.Set("CF-Connecting-IP", "1.2.3.4")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
}

// TestAPIKeyAuthIPRestrictionUsesForwardedClientIPInDenialWhenTrusted resolves
// a forwarded address through an explicitly trusted proxy. That address fails
// the allowlist, and the denial reports it instead of the proxy peer.
func TestAPIKeyAuthIPRestrictionUsesForwardedClientIPInDenialWhenTrusted(t *testing.T) {
	user := &identity.User{
		ID:          7,
		Role:        identity.RoleUser,
		Status:      billingcore.StatusActive,
		Balance:     10,
		Concurrency: 3,
	}
	apiKey := &apikey.APIKey{
		ID:          100,
		UserID:      user.ID,
		Key:         "test-key",
		Status:      billingcore.StatusActive,
		User:        user,
		IPWhitelist: []string{"9.9.9.9"},
	}

	apiKeyRepo := &stubApiKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			if key != apiKey.Key {
				return nil, apikey.ErrAPIKeyNotFound
			}
			clone := *apiKey
			return &clone, nil
		},
	}

	cfg := &config.Config{}
	cfg.SetTrustForwardedHeaders(false)
	apiKeyService := testkit.NewService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
	apiKeyService.Start()
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies([]string{"9.9.9.9"}))
	router.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(apiKeyService, nil, cfg)))
	router.GET("/t", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.RemoteAddr = "9.9.9.9:12345"
	req.Header.Set("x-api-key", apiKey.Key)
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.Header.Set("X-Real-IP", "1.2.3.4")
	req.Header.Set("CF-Connecting-IP", "1.2.3.4")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
	requireAPIKeyAuthError(t, w, "ACCESS_DENIED", "Access denied. Your IP is 1.2.3.4")
}

// TestAPIKeyAuthTouchesLastUsedOnSuccess authenticates an active grouped key
// with a capturing touch callback. Successful handling records the matching key
// ID and a nonzero timestamp.
func TestAPIKeyAuthTouchesLastUsedOnSuccess(t *testing.T) {
	user := &identity.User{
		ID:          7,
		Role:        identity.RoleUser,
		Status:      billingcore.StatusActive,
		Balance:     10,
		Concurrency: 3,
	}
	apiKey := &apikey.APIKey{
		ID:     100,
		UserID: user.ID,
		Key:    "touch-ok",
		Status: billingcore.StatusActive,
		User:   user,
	}

	var touchedID int64
	var touchedAt time.Time
	bindAuthTestGroup(apiKey)
	apiKeyRepo := &stubApiKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			if key != apiKey.Key {
				return nil, apikey.ErrAPIKeyNotFound
			}
			clone := *apiKey
			return &clone, nil
		},
		updateLastUsed: func(ctx context.Context, id int64, usedAt time.Time) error {
			touchedID = id
			touchedAt = usedAt
			return nil
		},
	}

	cfg := &config.Config{}
	apiKeyService := testkit.NewService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
	apiKeyService.Start()
	router := newAuthTestRouter(apiKeyService, nil, cfg)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.Header.Set("x-api-key", apiKey.Key)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, apiKey.ID, touchedID)
	require.False(t, touchedAt.IsZero(), "expected touch timestamp")
}

// TestAPIKeyAuthTouchLastUsedFailureDoesNotBlock returns a write failure from
// the diagnostic touch callback. Standard authorization still allows the
// request and invokes that callback exactly once in this case.
func TestAPIKeyAuthTouchLastUsedFailureDoesNotBlock(t *testing.T) {
	user := &identity.User{
		ID:          8,
		Role:        identity.RoleUser,
		Status:      billingcore.StatusActive,
		Balance:     10,
		Concurrency: 3,
	}
	apiKey := &apikey.APIKey{
		ID:     101,
		UserID: user.ID,
		Key:    "touch-fail",
		Status: billingcore.StatusActive,
		User:   user,
	}

	touchCalls := 0
	bindAuthTestGroup(apiKey)
	apiKeyRepo := &stubApiKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			if key != apiKey.Key {
				return nil, apikey.ErrAPIKeyNotFound
			}
			clone := *apiKey
			return &clone, nil
		},
		updateLastUsed: func(ctx context.Context, id int64, usedAt time.Time) error {
			touchCalls++
			return errors.New("db unavailable")
		},
	}

	cfg := &config.Config{}
	apiKeyService := testkit.NewService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
	apiKeyService.Start()
	router := newAuthTestRouter(apiKeyService, nil, cfg)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.Header.Set("x-api-key", apiKey.Key)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "touch failure should not block request")
	require.Equal(t, 1, touchCalls)
}

// TestAPIKeyAuthTouchesLastUsedInStandardMode serves an active key through the
// standard authorization fixture. A successful response records one diagnostic
// touch using the supplied callback.
func TestAPIKeyAuthTouchesLastUsedInStandardMode(t *testing.T) {
	user := &identity.User{
		ID:          9,
		Role:        identity.RoleUser,
		Status:      billingcore.StatusActive,
		Balance:     10,
		Concurrency: 3,
	}
	apiKey := &apikey.APIKey{
		ID:     102,
		UserID: user.ID,
		Key:    "touch-standard",
		Status: billingcore.StatusActive,
		User:   user,
	}

	touchCalls := 0
	bindAuthTestGroup(apiKey)
	apiKeyRepo := &stubApiKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			if key != apiKey.Key {
				return nil, apikey.ErrAPIKeyNotFound
			}
			clone := *apiKey
			return &clone, nil
		},
		updateLastUsed: func(ctx context.Context, id int64, usedAt time.Time) error {
			touchCalls++
			return nil
		},
	}

	cfg := &config.Config{}
	apiKeyService := testkit.NewService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
	apiKeyService.Start()
	router := newAuthTestRouter(apiKeyService, nil, cfg)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.Header.Set("x-api-key", apiKey.Key)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 1, touchCalls)
}

// TestAPIKeyAuthRemovedBillingPathUsesNormalQuotaChecks deliberately registers
// the removed billing path in the authorization fixture. An exhausted key then
// receives the normal quota rejection, confirming that this path has no
// consumption bypass.
func TestAPIKeyAuthRemovedBillingPathUsesNormalQuotaChecks(t *testing.T) {
	user := &identity.User{ID: 7, Role: identity.RoleUser, Status: billingcore.StatusActive, Balance: 10}
	apiKey := &apikey.APIKey{
		ID: 100, UserID: user.ID, Key: "removed-billing-path", Status: apikey.StatusAPIKeyQuotaExhausted,
		User: user, Quota: 1, QuotaUsed: 1,
	}

	bindAuthTestGroup(apiKey)
	apiKeyRepo := &stubApiKeyRepo{
		getByKey: func(context.Context, string) (*apikey.APIKey, error) {
			clone := *apiKey
			return &clone, nil
		},
	}
	cfg := &config.Config{}
	apiKeyService := testkit.NewService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
	apiKeyService.Start()
	router := newAuthTestRouter(apiKeyService, nil, cfg)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sub2api/billing", nil)
	req.Header.Set("x-api-key", apiKey.Key)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusTooManyRequests, w.Code)
	requireAPIKeyAuthError(t, w, "API_KEY_QUOTA_EXHAUSTED", "The API key quota has been exhausted.")
}

// TestAPIKeyAuthUsageStillTouchesLastUsed serves an existing usage endpoint
// with an active key and funded user. Its consumption bypass still performs one
// successful-authentication diagnostic touch after admission.
func TestAPIKeyAuthUsageStillTouchesLastUsed(t *testing.T) {
	user := &identity.User{ID: 7, Role: identity.RoleUser, Status: billingcore.StatusActive, Balance: 10}
	apiKey := &apikey.APIKey{ID: 100, UserID: user.ID, Key: "usage-touch", Status: billingcore.StatusActive, User: user}
	touchCalls := 0
	apiKeyRepo := &stubApiKeyRepo{
		getByKey: func(context.Context, string) (*apikey.APIKey, error) {
			clone := *apiKey
			return &clone, nil
		},
		updateLastUsed: func(context.Context, int64, time.Time) error {
			touchCalls++
			return nil
		},
	}
	cfg := &config.Config{}
	apiKeyService := testkit.NewService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
	apiKeyService.Start()
	router := newAuthTestRouter(apiKeyService, nil, cfg)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
	req.Header.Set("x-api-key", apiKey.Key)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 1, touchCalls)
}

// TestAPIKeyAuthAllowsBalanceBelowMinimumReserve sets the fixture's positive
// wallet below the configured reserve. Authorization admits that wallet at this
// stage, keeping reserve-based prechecks separate from its balance eligibility.
func TestAPIKeyAuthAllowsBalanceBelowMinimumReserve(t *testing.T) {
	user := &identity.User{
		ID:          10,
		Role:        identity.RoleUser,
		Status:      billingcore.StatusActive,
		Balance:     0.005,
		Concurrency: 3,
	}
	apiKey := &apikey.APIKey{
		ID:     103,
		UserID: user.ID,
		Key:    "held-balance-low",
		Status: billingcore.StatusActive,
		User:   user,
	}
	bindAuthTestGroup(apiKey)
	apiKeyRepo := &stubApiKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			if key != apiKey.Key {
				return nil, apikey.ErrAPIKeyNotFound
			}
			clone := *apiKey
			userClone := *user
			clone.User = &userClone
			return &clone, nil
		},
	}

	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.01
	apiKeyService := testkit.NewService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
	apiKeyService.Start()
	router := newAuthTestRouter(apiKeyService, nil, cfg)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.Header.Set("x-api-key", apiKey.Key)
	router.ServeHTTP(w, req)

	// MinimumBalanceReserve applies to billing-cache prechecks; authentication
	// accepts a positive balance.
	require.Equal(t, http.StatusOK, w.Code)
}

// TestAPIKeyAuthRejectsExhaustedBalance gives an active grouped key a zero
// wallet. Standard authorization returns the insufficient-balance reason and
// public message before endpoint execution.
func TestAPIKeyAuthRejectsExhaustedBalance(t *testing.T) {
	user := &identity.User{
		ID:          10,
		Role:        identity.RoleUser,
		Status:      billingcore.StatusActive,
		Balance:     0,
		Concurrency: 3,
	}
	apiKey := &apikey.APIKey{
		ID:     104,
		UserID: user.ID,
		Key:    "held-balance-zero",
		Status: billingcore.StatusActive,
		User:   user,
	}
	bindAuthTestGroup(apiKey)
	apiKeyRepo := &stubApiKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			if key != apiKey.Key {
				return nil, apikey.ErrAPIKeyNotFound
			}
			clone := *apiKey
			userClone := *user
			clone.User = &userClone
			return &clone, nil
		},
	}

	cfg := &config.Config{}
	apiKeyService := testkit.NewService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
	apiKeyService.Start()
	router := newAuthTestRouter(apiKeyService, nil, cfg)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.Header.Set("x-api-key", apiKey.Key)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
	requireAPIKeyAuthError(t, w, "INSUFFICIENT_BALANCE", "Insufficient account balance")
}

// TestAPIKeyAuthOpenAIQuotaErrorFormat sends an exhausted key to a Responses
// path. The 429 body decodes to OpenAI's insufficient_quota type and code,
// with a nil parameter value.
func TestAPIKeyAuthOpenAIQuotaErrorFormat(t *testing.T) {
	user := &identity.User{ID: 11, Role: identity.RoleUser, Status: billingcore.StatusActive, Balance: 10}
	group := &routing.Group{ID: 8, Status: billingcore.StatusActive}
	apiKey := &apikey.APIKey{
		ID: 105, UserID: user.ID, Key: "openai-quota-exhausted", Status: apikey.StatusAPIKeyQuotaExhausted,
		User: user, Group: group, GroupID: &group.ID,
	}
	apiKeyRepo := &stubApiKeyRepo{getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
		if key != apiKey.Key {
			return nil, apikey.ErrAPIKeyNotFound
		}
		clone := *apiKey
		userClone := *user
		clone.User = &userClone
		return &clone, nil
	}}

	cfg := &config.Config{}
	router := newAuthTestRouter(testkit.NewService(apiKeyRepo, nil, nil, nil, nil, nil, cfg), nil, cfg)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	req.Header.Set("x-api-key", apiKey.Key)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusTooManyRequests, w.Code)
	var response struct {
		Error struct {
			Message string  `json:"message"` // public protocol error message
			Type    string  `json:"type"`    // protocol error type name
			Param   *string `json:"param"`   // associated error parameter name
			Code    string  `json:"code"`    // protocol error reason code
		} `json:"error"` // protocol error envelope object
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Equal(t, "The API key quota has been exhausted.", response.Error.Message)
	require.Equal(t, "insufficient_quota", response.Error.Type)
	require.Nil(t, response.Error.Param)
	require.Equal(t, "insufficient_quota", response.Error.Code)
}

// TestAPIKeyAuthQuotaErrorKeepsLegacyFormatOutsideResponses sends the same
// exhausted-key condition to a Messages fixture path. The response keeps the
// represented top-level quota reason and message expected by these
// legacy-context tests.
func TestAPIKeyAuthQuotaErrorKeepsLegacyFormatOutsideResponses(t *testing.T) {
	user := &identity.User{ID: 11, Role: identity.RoleUser, Status: billingcore.StatusActive, Balance: 10}
	group := &routing.Group{ID: 8, Status: billingcore.StatusActive}
	apiKey := &apikey.APIKey{
		ID: 105, UserID: user.ID, Key: "openai-quota-exhausted", Status: apikey.StatusAPIKeyQuotaExhausted,
		User: user, Group: group, GroupID: &group.ID,
	}
	apiKeyRepo := &stubApiKeyRepo{getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
		if key != apiKey.Key {
			return nil, apikey.ErrAPIKeyNotFound
		}
		clone := *apiKey
		userClone := *user
		clone.User = &userClone
		return &clone, nil
	}}

	cfg := &config.Config{}
	router := newAuthTestRouter(testkit.NewService(apiKeyRepo, nil, nil, nil, nil, nil, cfg), nil, cfg)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("x-api-key", apiKey.Key)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusTooManyRequests, w.Code)
	requireAPIKeyAuthError(t, w, "API_KEY_QUOTA_EXHAUSTED", "The API key quota has been exhausted.")
}

// TestAPIKeyAuthAllowsBatchManagementAfterQuotaExhaustion uses an exhausted key
// and empty wallet to manage existing batches. Listing, reading, cancellation,
// and deletion requests reach their successful fixture endpoints.
func TestAPIKeyAuthAllowsBatchManagementAfterQuotaExhaustion(t *testing.T) {
	user := &identity.User{ID: 11, Role: identity.RoleUser, Status: billingcore.StatusActive, Balance: 0}
	apiKey := &apikey.APIKey{
		ID: 106, UserID: user.ID, Key: "batch-management-exhausted", Status: apikey.StatusAPIKeyQuotaExhausted,
		User: user, Quota: 1, QuotaUsed: 1,
	}
	apiKeyRepo := &stubApiKeyRepo{getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
		if key != apiKey.Key {
			return nil, apikey.ErrAPIKeyNotFound
		}
		clone := *apiKey
		userClone := *user
		clone.User = &userClone
		return &clone, nil
	}}

	cfg := &config.Config{}
	router := newAuthTestRouter(testkit.NewService(apiKeyRepo, nil, nil, nil, nil, nil, cfg), nil, cfg)
	requests := []struct {
		method string // request HTTP method
		path   string // request path under test
	}{
		{method: http.MethodGet, path: "/v1/images/batches"},
		{method: http.MethodGet, path: "/v1/images/batches/batch-1"},
		{method: http.MethodPost, path: "/v1/images/batches/batch-1/cancel"},
		{method: http.MethodDelete, path: "/v1/images/batches/batch-1"},
	}
	for _, request := range requests {
		response := httptest.NewRecorder()
		req := httptest.NewRequest(request.method, request.path, nil)
		req.Header.Set("x-api-key", apiKey.Key)
		router.ServeHTTP(response, req)
		require.Equal(t, http.StatusOK, response.Code, "%s %s", request.method, request.path)
	}
}

// TestAPIKeyAuthCompositeModelListStillChecksQuota supplies an exhausted
// composite key to ordinary and batch model lists. Both return the standard
// key-quota rejection despite requiring no single selected request model.
func TestAPIKeyAuthCompositeModelListStillChecksQuota(t *testing.T) {
	user := &identity.User{ID: 11, Role: identity.RoleUser, Status: billingcore.StatusActive, Balance: 10}
	group := &routing.Group{ID: 9, Status: billingcore.StatusActive, Hydrated: true}
	apiKey := &apikey.APIKey{
		ID: 107, UserID: user.ID, Key: "composite-model-list-exhausted", Status: apikey.StatusAPIKeyQuotaExhausted,
		User: user, IsComposite: true, Quota: 1, QuotaUsed: 1,
		CompositeGroups: []apikey.APIKeyCompositeGroup{{GroupID: group.ID, Prefix: "GPT", NormalizedPrefix: "gpt", Group: group}},
	}
	repo := &stubApiKeyRepo{getByKey: func(_ context.Context, key string) (*apikey.APIKey, error) {
		if key != apiKey.Key {
			return nil, apikey.ErrAPIKeyNotFound
		}
		clone := *apiKey
		return &clone, nil
	}}
	cfg := &config.Config{}
	router := newAuthTestRouter(testkit.NewService(repo, nil, nil, nil, nil, nil, cfg), nil, cfg)

	for _, path := range []string{"/v1/models", "/v1/images/batches/models"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("x-api-key", apiKey.Key)
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusTooManyRequests, w.Code, path)
		requireAPIKeyAuthError(t, w, "API_KEY_QUOTA_EXHAUSTED", "The API key quota has been exhausted.")
	}
}

// TestAPIKeyAuthInvalidAbuseReturns429BeforeRepository combines absent,
// malformed, and unknown credentials in one client's abuse bucket. The next
// request returns 429 with a retry header and never adds another repository
// lookup.
func TestAPIKeyAuthInvalidAbuseReturns429BeforeRepository(t *testing.T) {
	repoCalls := 0
	repo := &stubApiKeyRepo{getByKey: func(context.Context, string) (*apikey.APIKey, error) {
		repoCalls++
		return nil, apikey.ErrAPIKeyNotFound
	}}
	cfg := invalidAuthAbuseTestConfig(3)
	svc := testkit.NewService(repo, nil, nil, nil, nil, nil, cfg)
	svc.Start()
	r := gin.New()
	var reason IngressRejectReason
	r.Use(func(c *gin.Context) { c.Next(); reason, _ = GetIngressRejectReason(c) })
	r.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(svc, nil, cfg)))
	r.POST("/v1/messages", func(c *gin.Context) { c.Status(http.StatusOK) })

	requests := []*http.Request{
		httpRequest(t, "/v1/messages", "", ""),
		httpRequest(t, "/v1/messages", "Basic malformed", ""),
		httpRequest(t, "/v1/messages", "", "random-invalid-key"),
	}
	for _, req := range requests {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.NotEqual(t, http.StatusTooManyRequests, w.Code)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httpRequest(t, "/v1/messages", "", "another-random-key"))
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.Equal(t, "60", w.Header().Get("Retry-After"))
	require.Contains(t, w.Body.String(), "INVALID_AUTH_RATE_LIMITED")
	require.Equal(t, IngressRejectInvalidAuthRateLimited, reason)
	require.Equal(t, 1, repoCalls, "rate-limited request must not reach the repository")
}

// TestGoogleAPIKeyAuthInvalidAbuseReturnsProtocol429 exhausts the Google
// client's invalid-credential allowance with two lookup failures. The next
// response is Google's quota-shaped 429 with the invalid-auth rate-limit marker
// and retry header.
func TestGoogleAPIKeyAuthInvalidAbuseReturnsProtocol429(t *testing.T) {
	repoCalls := 0
	repo := fakeAPIKeyRepo{getByKey: func(context.Context, string) (*apikey.APIKey, error) {
		repoCalls++
		return nil, apikey.ErrAPIKeyNotFound
	}}
	cfg := invalidAuthAbuseTestConfig(2)
	svc := testkit.NewService(repo, nil, nil, nil, nil, nil, cfg)
	svc.Start()
	r := gin.New()
	var reason IngressRejectReason
	r.Use(func(c *gin.Context) { c.Next(); reason, _ = GetIngressRejectReason(c) })
	r.Use(APIKeyAuthGoogle(svc, cfg))
	r.POST("/v1beta/models/test:generateContent", func(c *gin.Context) { c.Status(http.StatusOK) })
	for _, key := range []string{"random-1", "random-2"} {
		w := httptest.NewRecorder()
		req := httpRequest(t, "/v1beta/models/test:generateContent", "", key)
		req.Header.Del("x-api-key")
		req.Header.Set("x-goog-api-key", key)
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusUnauthorized, w.Code)
	}
	w := httptest.NewRecorder()
	req := httpRequest(t, "/v1beta/models/test:generateContent", "", "random-3")
	req.Header.Del("x-api-key")
	req.Header.Set("x-goog-api-key", "random-3")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.Equal(t, "60", w.Header().Get("Retry-After"))
	require.Contains(t, w.Body.String(), "RESOURCE_EXHAUSTED")
	require.Equal(t, IngressRejectInvalidAuthRateLimited, reason)
	require.Equal(t, 2, repoCalls)
}

// TestInvalidAuthAbuseDoesNotCountValidOrOperationalFailures interleaves an
// invalid credential with successful authentication and database errors. The
// abuse health count retains the single invalid attempt, including after a
// valid Google-style header is accepted.
func TestInvalidAuthAbuseDoesNotCountValidOrOperationalFailures(t *testing.T) {
	user := &identity.User{ID: 1, Status: billingcore.StatusActive, Role: identity.RoleUser, Balance: 1}
	repo := &stubApiKeyRepo{getByKey: func(_ context.Context, key string) (*apikey.APIKey, error) {
		switch key {
		case "valid-key":
			return bindAuthTestGroup(&apikey.APIKey{ID: 1, UserID: 1, Key: key, Status: billingcore.StatusActive, User: user}), nil
		case "db-error":
			return nil, errors.New("database unavailable")
		default:
			return nil, apikey.ErrAPIKeyNotFound
		}
	}}
	cfg := invalidAuthAbuseTestConfig(10)
	svc := testkit.NewService(repo, nil, nil, nil, nil, nil, cfg)
	svc.Start()
	r := gin.New()
	r.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(svc, nil, cfg)))
	r.POST("/t", func(c *gin.Context) { c.Status(http.StatusOK) })

	for _, tc := range []struct {
		key  string // supplied credential text
		want int    // expected response HTTP status
	}{{"invalid", 401}, {"valid-key", 200}, {"db-error", 500}, {"db-error", 500}} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httpRequest(t, "/t", "", tc.key))
		require.Equal(t, tc.want, w.Code)
	}
	w := httptest.NewRecorder()
	req := httpRequest(t, "/t", "", "")
	req.Header.Set("x-goog-api-key", "valid-key")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, uint64(1), svc.InvalidAuthAbuseHealth().Recorded)
}

// APIKeyAuthGoogle returns the Google authorization fixture with subscription
// support absent. Services and configuration stay available during request
// execution, while the gateway constructor chooses Google credential handling
// and error envelopes.
func APIKeyAuthGoogle(apiKeyService *apikey.APIKeyService, cfg *config.Config) gin.HandlerFunc {
	return APIKeyAuthWithSubscriptionGoogle(apiKeyService, nil, cfg)
}

// APIKeyAuthWithSubscriptionGoogle returns the shared test authorization
// callbacks using the Google gateway constructor. Keep the supplied services
// and configuration usable while requests execute; a missing subscription
// service is handled by the shared helper.
func APIKeyAuthWithSubscriptionGoogle(apiKeyService *apikey.APIKeyService, subscriptionService *billingcore.SubscriptionService, cfg *config.Config) gin.HandlerFunc {
	return newGatewayAuthorization(apiKeyService, subscriptionService, cfg, true)
}

// googleTeamAPIKeyError delegates team-error mapping to the Google
// authentication adapter. The success flag identifies a recognized team error;
// callers inspect that flag before using the returned status and public
// message.
func googleTeamAPIKeyError(err error) (int, string, bool) { return keyhttp.GoogleTeamError(err) }

// FilterByGroup returns the supplied subscription slice for every group ID. Its
// backing records remain shared, and this fixture performs no group filtering;
// scenarios configure candidate group eligibility in their input records.
func (f fakeGoogleSubscriptionRepo) FilterByGroup(_ context.Context, subs []billingcore.UserSubscription, _ int64) ([]billingcore.UserSubscription, error) {
	return subs, nil
}

// Create rejects key creation with the authentication fixture's
// unimplemented-operation error.
func (f fakeAPIKeyRepo) Create(ctx context.Context, key *apikey.APIKey) error {
	return errors.New("not implemented")
}

// GetByID rejects key-ID lookup with the authentication fixture's unimplemented
// error.
func (f fakeAPIKeyRepo) GetByID(ctx context.Context, id int64) (*apikey.APIKey, error) {
	return nil, errors.New("not implemented")
}

// GetKeyAndOwnerID rejects credential-owner lookup with the fixture's
// unimplemented-operation error.
func (f fakeAPIKeyRepo) GetKeyAndOwnerID(ctx context.Context, id int64) (string, int64, error) {
	return "", 0, errors.New("not implemented")
}

// GetByKey invokes the configured credential callback with ctx and the exact
// supplied key. A nil callback returns the unexpected-call error; the callback
// determines returned record sharing and lookup failures.
func (f fakeAPIKeyRepo) GetByKey(ctx context.Context, key string) (*apikey.APIKey, error) {
	if f.getByKey == nil {
		return nil, errors.New("unexpected call")
	}
	return f.getByKey(ctx, key)
}

// GetByKeyForAuth delegates exact credential lookup to this fixture's GetByKey
// callback path. Returned objects and errors are supplied by that callback, so
// callers follow its configured sharing and missing-callback behavior.
func (f fakeAPIKeyRepo) GetByKeyForAuth(ctx context.Context, key string) (*apikey.APIKey, error) {
	return f.GetByKey(ctx, key)
}

// RotateCredential panics if this authentication fixture receives a rotation
// request. The scenarios configure credential reads and diagnostic touches;
// rotation callers require a repository that implements credential mutation.
func (f fakeAPIKeyRepo) RotateCredential(context.Context, *apikey.APIKey, string) error {
	panic("unexpected RotateCredential call")
}

// Update rejects key updates with the authentication fixture's
// unimplemented-operation error.
func (f fakeAPIKeyRepo) Update(ctx context.Context, key *apikey.APIKey, _ apikey.APIKeyUpdateFields) error {
	return errors.New("not implemented")
}

// Delete rejects key deletion with the authentication fixture's
// unimplemented-operation error.
func (f fakeAPIKeyRepo) Delete(ctx context.Context, id int64) error {
	return errors.New("not implemented")
}

// DeleteWithAudit rejects audited key deletion with the fixture's
// unimplemented-operation error.
func (f fakeAPIKeyRepo) DeleteWithAudit(ctx context.Context, id int64) error {
	return errors.New("not implemented")
}

// ListByUserID rejects user-key pagination with the fixture's
// unimplemented-operation error.
func (f fakeAPIKeyRepo) ListByUserID(ctx context.Context, userID int64, params pagination.PaginationParams, _ apikey.APIKeyListFilters) ([]apikey.APIKey, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

// VerifyOwnership rejects ownership queries with the authentication fixture's
// unimplemented-operation error.
func (f fakeAPIKeyRepo) VerifyOwnership(ctx context.Context, userID int64, apiKeyIDs []int64) ([]int64, error) {
	return nil, errors.New("not implemented")
}

// CountByUserID rejects user-key counting with the fixture's
// unimplemented-operation error.
func (f fakeAPIKeyRepo) CountByUserID(ctx context.Context, userID int64) (int64, error) {
	return 0, errors.New("not implemented")
}

// ExistsByKey rejects credential-existence queries with the fixture's
// unimplemented-operation error.
func (f fakeAPIKeyRepo) ExistsByKey(ctx context.Context, key string) (bool, error) {
	return false, errors.New("not implemented")
}

// ListByGroupID rejects group-key pagination with the fixture's
// unimplemented-operation error.
func (f fakeAPIKeyRepo) ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]apikey.APIKey, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

// SearchAPIKeys rejects key searches with the authentication fixture's
// unimplemented-operation error.
func (f fakeAPIKeyRepo) SearchAPIKeys(ctx context.Context, userID int64, keyword string, limit int) ([]apikey.APIKey, error) {
	return nil, errors.New("not implemented")
}

// ClearGroupIDByGroupID rejects group-binding removal with the fixture's
// unimplemented-operation error.
func (f fakeAPIKeyRepo) ClearGroupIDByGroupID(ctx context.Context, groupID int64) (int64, error) {
	return 0, errors.New("not implemented")
}

// CountByGroupID rejects group-key counting with the fixture's
// unimplemented-operation error.
func (f fakeAPIKeyRepo) CountByGroupID(ctx context.Context, groupID int64) (int64, error) {
	return 0, errors.New("not implemented")
}

// ListKeysByUserID rejects user credential listing with the fixture's
// unimplemented error.
func (f fakeAPIKeyRepo) ListKeysByUserID(ctx context.Context, userID int64) ([]string, error) {
	return nil, errors.New("not implemented")
}

// ListKeysByGroupID rejects group credential listing with the fixture's
// unimplemented error.
func (f fakeAPIKeyRepo) ListKeysByGroupID(ctx context.Context, groupID int64) ([]string, error) {
	return nil, errors.New("not implemented")
}

// IncrementQuotaUsed rejects quota writes with the authentication fixture's
// unimplemented-operation error.
func (f fakeAPIKeyRepo) IncrementQuotaUsed(ctx context.Context, id int64, amount float64) (float64, error) {
	return 0, errors.New("not implemented")
}

// UpdateLastUsed invokes the configured touch callback with the key ID and
// timestamp. Without a callback it reports success and leaves fixture state
// unchanged, keeping diagnostic persistence optional in these authorization
// cases.
func (f fakeAPIKeyRepo) UpdateLastUsed(ctx context.Context, id int64, usedAt time.Time) error {
	if f.updateLastUsed != nil {
		return f.updateLastUsed(ctx, id, usedAt)
	}
	return nil
}

// IncrementRateLimitUsage accepts diagnostic spending updates and leaves
// fixture counters unchanged.
func (f fakeAPIKeyRepo) IncrementRateLimitUsage(ctx context.Context, id int64, cost float64) error {
	return nil
}

// ResetRateLimitWindows accepts window resets and leaves authentication fixture
// state unchanged.
func (f fakeAPIKeyRepo) ResetRateLimitWindows(ctx context.Context, id int64) error {
	return nil
}

// GetRateLimitData returns a fresh zero-valued rate-counter record for any key
// ID. It reports success while keeping no persistent counters, so this Google
// fixture supplies empty observations independently of request counting.
func (f fakeAPIKeyRepo) GetRateLimitData(ctx context.Context, id int64) (*apikey.APIKeyRateLimitData, error) {
	return &apikey.APIKeyRateLimitData{}, nil
}

// UpdateGroupIDByUserAndGroup rejects group rebinding with the fixture's
// unimplemented-operation error.
func (f fakeAPIKeyRepo) UpdateGroupIDByUserAndGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (int64, error) {
	return 0, errors.New("not implemented")
}

// Create rejects subscription creation with the fixture's
// unimplemented-operation error.
func (f fakeGoogleSubscriptionRepo) Create(ctx context.Context, sub *billingcore.UserSubscription) error {
	return errors.New("not implemented")
}

// GetByID invokes the configured ID lookup callback for funding or maintenance
// reload. A nil callback reports the unimplemented operation; callback results
// determine sharing, missing records, and failures.
func (f fakeGoogleSubscriptionRepo) GetByID(ctx context.Context, id int64) (*billingcore.UserSubscription, error) {
	if f.getByID != nil {
		return f.getByID(ctx, id)
	}
	return nil, errors.New("not implemented")
}

// GetByIDIncludeDeleted rejects deleted-inclusive subscription lookup with the
// fixture's unimplemented error.
func (f fakeGoogleSubscriptionRepo) GetByIDIncludeDeleted(ctx context.Context, id int64) (*billingcore.UserSubscription, error) {
	return nil, errors.New("not implemented")
}

// GetByUserIDAndGroupID rejects group subscription lookup with the fixture's
// unimplemented-operation error.
func (f fakeGoogleSubscriptionRepo) GetByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (*billingcore.UserSubscription, error) {
	return nil, errors.New("not implemented")
}

// GetActiveByUserIDAndGroupID rejects active group lookup with the fixture's
// unimplemented-operation error.
func (f fakeGoogleSubscriptionRepo) GetActiveByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (*billingcore.UserSubscription, error) {
	return nil, errors.New("not implemented")
}

// GetLatestByUserIDAndPlanID rejects latest-plan lookup with the fixture's
// unimplemented-operation error.
func (f fakeGoogleSubscriptionRepo) GetLatestByUserIDAndPlanID(ctx context.Context, userID, planID int64) (*billingcore.UserSubscription, error) {
	return nil, errors.New("not implemented")
}

// Update rejects subscription replacement with the fixture's
// unimplemented-operation error.
func (f fakeGoogleSubscriptionRepo) Update(ctx context.Context, sub *billingcore.UserSubscription) error {
	return errors.New("not implemented")
}

// Delete rejects subscription deletion with the fixture's
// unimplemented-operation error.
func (f fakeGoogleSubscriptionRepo) Delete(ctx context.Context, id int64) error {
	return errors.New("not implemented")
}

// Restore rejects subscription restoration with the fixture's
// unimplemented-operation error.
func (f fakeGoogleSubscriptionRepo) Restore(ctx context.Context, subscriptionID int64, restoredStatus string) (*billingcore.UserSubscription, error) {
	return nil, errors.New("not implemented")
}

// ListByUserID rejects subscription history listing with the fixture's
// unimplemented-operation error.
func (f fakeGoogleSubscriptionRepo) ListByUserID(ctx context.Context, userID int64) ([]billingcore.UserSubscription, error) {
	return nil, errors.New("not implemented")
}

// ListActiveByUserID invokes the configured candidate-list callback for the
// supplied user. Missing callback configuration reports an unimplemented
// operation, and returned slices retain whatever sharing the callback supplies.
func (f fakeGoogleSubscriptionRepo) ListActiveByUserID(ctx context.Context, userID int64) ([]billingcore.UserSubscription, error) {
	if f.listActive != nil {
		return f.listActive(ctx, userID)
	}
	return nil, errors.New("not implemented")
}

// ListByUserIDAndPlanID rejects plan history listing with the fixture's
// unimplemented-operation error.
func (f fakeGoogleSubscriptionRepo) ListByUserIDAndPlanID(ctx context.Context, userID, planID int64) ([]billingcore.UserSubscription, error) {
	return nil, errors.New("not implemented")
}

// ListByGroupID rejects group subscription pagination with the fixture's
// unimplemented-operation error.
func (f fakeGoogleSubscriptionRepo) ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]billingcore.UserSubscription, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

// ListByPlanID rejects plan subscription pagination with the fixture's
// unimplemented-operation error.
func (f fakeGoogleSubscriptionRepo) ListByPlanID(ctx context.Context, planID int64, params pagination.PaginationParams) ([]billingcore.UserSubscription, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

// List rejects general subscription pagination with the fixture's
// unimplemented-operation error.
func (f fakeGoogleSubscriptionRepo) List(ctx context.Context, params pagination.PaginationParams, userID, planID *int64, status, platform, sortBy, sortOrder string) ([]billingcore.UserSubscription, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

// ListBySourceOrderID rejects source-order lookup with the fixture's
// unimplemented-operation error.
func (f fakeGoogleSubscriptionRepo) ListBySourceOrderID(ctx context.Context, sourceOrderID int64) ([]billingcore.UserSubscription, error) {
	return nil, errors.New("not implemented")
}

// ExistsByUserIDAndGroupID rejects membership queries with the fixture's
// unimplemented-operation error.
func (f fakeGoogleSubscriptionRepo) ExistsByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (bool, error) {
	return false, errors.New("not implemented")
}

// ExtendExpiry rejects subscription extension with the fixture's
// unimplemented-operation error.
func (f fakeGoogleSubscriptionRepo) ExtendExpiry(ctx context.Context, subscriptionID int64, newExpiresAt time.Time) error {
	return errors.New("not implemented")
}

// UpdateStatus invokes the configured status callback with the subscription ID
// and requested state. A nil callback reports unimplemented persistence, so
// validation observes that fixture failure directly.
func (f fakeGoogleSubscriptionRepo) UpdateStatus(ctx context.Context, subscriptionID int64, status string) error {
	if f.updateStatus != nil {
		return f.updateStatus(ctx, subscriptionID, status)
	}
	return errors.New("not implemented")
}

// UpdateNotes rejects subscription note changes with the fixture's
// unimplemented-operation error.
func (f fakeGoogleSubscriptionRepo) UpdateNotes(ctx context.Context, subscriptionID int64, notes string) error {
	return errors.New("not implemented")
}

// ActivateWindows invokes the activation callback with ID and start time while
// ignoring the activation mask. Missing callback configuration reports the
// unsupported operation; the callback controls any fixture changes.
func (f fakeGoogleSubscriptionRepo) ActivateWindows(ctx context.Context, id int64, start time.Time, activation billingcore.SubscriptionWindowActivation) error {
	if f.activateWindow != nil {
		return f.activateWindow(ctx, id, start)
	}
	return errors.New("not implemented")
}

// ResetUsageWindows rejects combined window resets with the fixture's
// unimplemented-operation error.
func (f fakeGoogleSubscriptionRepo) ResetUsageWindows(context.Context, int64, bool, bool, bool, time.Time) error {
	return errors.New("not implemented")
}

// ResetDailyUsage passes ID and the new start to the daily reset callback,
// ignoring expectedWindowStart. A nil callback reports the unsupported reset,
// so these fixture callbacks do not implement database compare-and-swap.
func (f fakeGoogleSubscriptionRepo) ResetDailyUsage(ctx context.Context, id int64, _ *time.Time, start time.Time) error {
	if f.resetDaily != nil {
		return f.resetDaily(ctx, id, start)
	}
	return errors.New("not implemented")
}

// ResetWeeklyUsage passes ID and the new start to the weekly reset callback,
// ignoring expectedWindowStart. A nil callback reports the unsupported reset,
// leaving callback behavior to the scenario's maintenance fixture.
func (f fakeGoogleSubscriptionRepo) ResetWeeklyUsage(ctx context.Context, id int64, _ *time.Time, start time.Time) error {
	if f.resetWeekly != nil {
		return f.resetWeekly(ctx, id, start)
	}
	return errors.New("not implemented")
}

// ResetMonthlyUsage passes ID and the new start to the monthly reset callback,
// ignoring expectedWindowStart. A nil callback reports the unsupported reset,
// leaving callback behavior to the scenario's maintenance fixture.
func (f fakeGoogleSubscriptionRepo) ResetMonthlyUsage(ctx context.Context, id int64, _ *time.Time, start time.Time) error {
	if f.resetMonthly != nil {
		return f.resetMonthly(ctx, id, start)
	}
	return errors.New("not implemented")
}

// IncrementUsage rejects subscription usage writes with the fixture's
// unimplemented-operation error.
func (f fakeGoogleSubscriptionRepo) IncrementUsage(ctx context.Context, id int64, costUSD float64) error {
	return errors.New("not implemented")
}

// BatchUpdateExpiredStatus rejects expiry maintenance with the fixture's
// unimplemented-operation error.
func (f fakeGoogleSubscriptionRepo) BatchUpdateExpiredStatus(ctx context.Context) (int64, error) {
	return 0, errors.New("not implemented")
}

// newTestAPIKeyService constructs the API-key test service with the supplied
// repository and zero-valued configuration. Its other optional repository and
// cache dependencies are nil, keeping fixture lookup behavior in the supplied
// key source. It returns an unstarted service; zero cache settings disable
// local positive, shared positive, and local negative caching.
func newTestAPIKeyService(repo apikey.APIKeyRepository) *apikey.APIKeyService {
	return testkit.NewService(
		repo,
		nil, // userRepo (unused in GetByKey)
		nil, // groupRepo
		nil, // userSubRepo
		nil, // userGroupRateRepo
		nil, // cache
		&config.Config{},
	)
}

// newAuthTestRouter mounts standard authorization before successful fixture
// endpoints for generation, usage, model listing, and batch management. It also
// registers the removed billing path deliberately so classification can be
// tested apart from production route removal.
func newAuthTestRouter(apiKeyService *apikey.APIKeyService, subscriptionService *billingcore.SubscriptionService, cfg *config.Config) *gin.Engine {
	router := gin.New()
	router.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(apiKeyService, subscriptionService, cfg)))
	ok := func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
	router.GET("/t", ok)
	router.POST("/v1/responses", ok)
	router.POST("/v1/messages", ok)
	router.GET("/v1/usage", ok)
	router.GET("/antigravity/v1/usage", ok)
	router.GET("/v1/models", ok)
	router.GET("/v1/images/batches/models", ok)
	router.GET("/v1/sub2api/billing", ok)
	router.GET("/v1/images/batches", ok)
	router.GET("/v1/images/batches/:id", ok)
	router.POST("/v1/images/batches/:id/cancel", ok)
	router.DELETE("/v1/images/batches/:id", ok)
	return router
}

// requireAPIKeyAuthError decodes the represented top-level legacy error
// response and compares its code and message. Invalid JSON or mismatched fields
// fail the calling test using its supplied recorder.
func requireAPIKeyAuthError(t *testing.T, w *httptest.ResponseRecorder, code, message string) {
	t.Helper()

	var resp httpx.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, code, resp.Code)
	require.Equal(t, message, resp.Message)
}

// Create rejects group creation with the fallback fixture's
// unimplemented-operation error.
func (r *stubGroupRepoForAuth) Create(ctx context.Context, group *routing.Group) error {
	return errors.New("not implemented")
}

// GetByID returns an outer Group copy from the exact-ID fixture map, or
// ErrGroupNotFound. Nested references remain shared, and the map stays
// unchanged while requests use these fallback group results.
func (r *stubGroupRepoForAuth) GetByID(ctx context.Context, id int64) (*routing.Group, error) {
	if value, ok := r.groupsByID[id]; ok {
		return &value, nil
	}
	return nil, routing.ErrGroupNotFound
}

// GetByIDLite delegates to the same exact-ID lookup and outer-copy behavior as
// GetByID. The fixture supplies no separate lightweight representation, and
// nested group references remain shared with its setup data.
func (r *stubGroupRepoForAuth) GetByIDLite(ctx context.Context, id int64) (*routing.Group, error) {
	return r.GetByID(ctx, id)
}

// Update rejects group updates with the fallback fixture's
// unimplemented-operation error.
func (r *stubGroupRepoForAuth) Update(ctx context.Context, group *routing.Group) error {
	return errors.New("not implemented")
}

// Delete rejects group deletion with the fallback fixture's
// unimplemented-operation error.
func (r *stubGroupRepoForAuth) Delete(ctx context.Context, id int64) error {
	return errors.New("not implemented")
}

// DeleteCascade rejects cascading group deletion with the fixture's
// unimplemented-operation error.
func (r *stubGroupRepoForAuth) DeleteCascade(ctx context.Context, id int64) ([]int64, error) {
	return nil, errors.New("not implemented")
}

// List rejects group pagination with the fallback fixture's
// unimplemented-operation error.
func (r *stubGroupRepoForAuth) List(ctx context.Context, params pagination.PaginationParams) ([]routing.Group, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

// ListWithFilters rejects filtered group pagination with the fixture's
// unimplemented-operation error.
func (r *stubGroupRepoForAuth) ListWithFilters(ctx context.Context, params pagination.PaginationParams, platform, status, search string, isExclusive *bool) ([]routing.Group, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

// ListActive rejects active-group enumeration with the fixture's
// unimplemented-operation error.
func (r *stubGroupRepoForAuth) ListActive(ctx context.Context) ([]routing.Group, error) {
	return nil, errors.New("not implemented")
}

// ExistsByName rejects group-name queries with the fallback fixture's
// unimplemented-operation error.
func (r *stubGroupRepoForAuth) ExistsByName(ctx context.Context, name string) (bool, error) {
	return false, errors.New("not implemented")
}

// GetProviderCount rejects provider counting with the fallback fixture's
// unimplemented-operation error.
func (r *stubGroupRepoForAuth) GetProviderCount(ctx context.Context, groupID int64) (int64, int64, error) {
	return 0, 0, errors.New("not implemented")
}

// DeleteProviderGroupsByGroupID rejects membership deletion with the fixture's
// unimplemented-operation error.
func (r *stubGroupRepoForAuth) DeleteProviderGroupsByGroupID(ctx context.Context, groupID int64) (int64, error) {
	return 0, errors.New("not implemented")
}

// GetProviderIDsByGroupIDs rejects membership enumeration with the fixture's
// unimplemented-operation error.
func (r *stubGroupRepoForAuth) GetProviderIDsByGroupIDs(ctx context.Context, groupIDs []int64) ([]int64, error) {
	return nil, errors.New("not implemented")
}

// BindProvidersToGroup rejects membership writes with the fallback fixture's
// unimplemented-operation error.
func (r *stubGroupRepoForAuth) BindProvidersToGroup(ctx context.Context, groupID int64, providerIDs []int64) error {
	return errors.New("not implemented")
}

// UpdateSortOrders rejects sort-order writes with the fallback fixture's
// unimplemented-operation error.
func (r *stubGroupRepoForAuth) UpdateSortOrders(ctx context.Context, updates []routing.GroupSortOrderUpdate) error {
	return errors.New("not implemented")
}

// Create rejects key creation with the authentication fixture's
// unimplemented-operation error.
func (r *stubApiKeyRepo) Create(ctx context.Context, key *apikey.APIKey) error {
	return errors.New("not implemented")
}

// GetByID rejects key-ID lookup with the authentication fixture's unimplemented
// error.
func (r *stubApiKeyRepo) GetByID(ctx context.Context, id int64) (*apikey.APIKey, error) {
	return nil, errors.New("not implemented")
}

// GetKeyAndOwnerID rejects credential-owner lookup with the fixture's
// unimplemented-operation error.
func (r *stubApiKeyRepo) GetKeyAndOwnerID(ctx context.Context, id int64) (string, int64, error) {
	return "", 0, errors.New("not implemented")
}

// GetByKey invokes the configured credential callback with ctx and the exact
// supplied key. Missing lookup configuration returns the
// unimplemented-operation error; record sharing and lookup failures come from
// the configured callback.
func (r *stubApiKeyRepo) GetByKey(ctx context.Context, key string) (*apikey.APIKey, error) {
	if r.getByKey != nil {
		return r.getByKey(ctx, key)
	}
	return nil, errors.New("not implemented")
}

// GetByKeyForAuth delegates exact credential lookup to this fixture's GetByKey
// callback path. Returned objects and errors are supplied by that callback, so
// callers follow its configured sharing and missing-callback behavior.
func (r *stubApiKeyRepo) GetByKeyForAuth(ctx context.Context, key string) (*apikey.APIKey, error) {
	return r.GetByKey(ctx, key)
}

// RotateCredential panics if this authentication fixture receives a rotation
// request. The scenarios configure credential reads and diagnostic touches;
// rotation callers require a repository that implements credential mutation.
func (r *stubApiKeyRepo) RotateCredential(context.Context, *apikey.APIKey, string) error {
	panic("unexpected RotateCredential call")
}

// Update rejects key updates with the authentication fixture's
// unimplemented-operation error.
func (r *stubApiKeyRepo) Update(ctx context.Context, key *apikey.APIKey, _ apikey.APIKeyUpdateFields) error {
	return errors.New("not implemented")
}

// Delete rejects key deletion with the authentication fixture's
// unimplemented-operation error.
func (r *stubApiKeyRepo) Delete(ctx context.Context, id int64) error {
	return errors.New("not implemented")
}

// DeleteWithAudit rejects audited key deletion with the fixture's
// unimplemented-operation error.
func (r *stubApiKeyRepo) DeleteWithAudit(ctx context.Context, id int64) error {
	return errors.New("not implemented")
}

// ListByUserID rejects user-key pagination with the fixture's
// unimplemented-operation error.
func (r *stubApiKeyRepo) ListByUserID(ctx context.Context, userID int64, params pagination.PaginationParams, _ apikey.APIKeyListFilters) ([]apikey.APIKey, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

// VerifyOwnership rejects ownership queries with the authentication fixture's
// unimplemented-operation error.
func (r *stubApiKeyRepo) VerifyOwnership(ctx context.Context, userID int64, apiKeyIDs []int64) ([]int64, error) {
	return nil, errors.New("not implemented")
}

// CountByUserID rejects user-key counting with the fixture's
// unimplemented-operation error.
func (r *stubApiKeyRepo) CountByUserID(ctx context.Context, userID int64) (int64, error) {
	return 0, errors.New("not implemented")
}

// ExistsByKey rejects credential-existence queries with the fixture's
// unimplemented-operation error.
func (r *stubApiKeyRepo) ExistsByKey(ctx context.Context, key string) (bool, error) {
	return false, errors.New("not implemented")
}

// ListByGroupID rejects group-key pagination with the fixture's
// unimplemented-operation error.
func (r *stubApiKeyRepo) ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]apikey.APIKey, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

// SearchAPIKeys rejects key searches with the authentication fixture's
// unimplemented-operation error.
func (r *stubApiKeyRepo) SearchAPIKeys(ctx context.Context, userID int64, keyword string, limit int) ([]apikey.APIKey, error) {
	return nil, errors.New("not implemented")
}

// ClearGroupIDByGroupID rejects group-binding removal with the fixture's
// unimplemented-operation error.
func (r *stubApiKeyRepo) ClearGroupIDByGroupID(ctx context.Context, groupID int64) (int64, error) {
	return 0, errors.New("not implemented")
}

// UpdateGroupIDByUserAndGroup rejects group rebinding with the fixture's
// unimplemented-operation error.
func (r *stubApiKeyRepo) UpdateGroupIDByUserAndGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (int64, error) {
	return 0, errors.New("not implemented")
}

// CountByGroupID rejects group-key counting with the fixture's
// unimplemented-operation error.
func (r *stubApiKeyRepo) CountByGroupID(ctx context.Context, groupID int64) (int64, error) {
	return 0, errors.New("not implemented")
}

// ListKeysByUserID rejects user credential listing with the fixture's
// unimplemented error.
func (r *stubApiKeyRepo) ListKeysByUserID(ctx context.Context, userID int64) ([]string, error) {
	return nil, errors.New("not implemented")
}

// ListKeysByGroupID rejects group credential listing with the fixture's
// unimplemented error.
func (r *stubApiKeyRepo) ListKeysByGroupID(ctx context.Context, groupID int64) ([]string, error) {
	return nil, errors.New("not implemented")
}

// IncrementQuotaUsed rejects quota writes with the authentication fixture's
// unimplemented-operation error.
func (r *stubApiKeyRepo) IncrementQuotaUsed(ctx context.Context, id int64, amount float64) (float64, error) {
	return 0, errors.New("not implemented")
}

// UpdateLastUsed invokes the configured touch callback with the key ID and
// timestamp. Without a callback it reports success and leaves fixture state
// unchanged, keeping diagnostic persistence optional in these authorization
// cases.
func (r *stubApiKeyRepo) UpdateLastUsed(ctx context.Context, id int64, usedAt time.Time) error {
	if r.updateLastUsed != nil {
		return r.updateLastUsed(ctx, id, usedAt)
	}
	return nil
}

// IncrementRateLimitUsage accepts diagnostic spending updates and leaves
// fixture counters unchanged.
func (r *stubApiKeyRepo) IncrementRateLimitUsage(ctx context.Context, id int64, cost float64) error {
	return nil
}

// ResetRateLimitWindows accepts window resets and leaves authentication fixture
// state unchanged.
func (r *stubApiKeyRepo) ResetRateLimitWindows(ctx context.Context, id int64) error {
	return nil
}

// GetRateLimitData reports successful absence of a rate-counter record for any
// key ID. It returns nil data because this standard authorization fixture
// retains no repository rate state.
func (r *stubApiKeyRepo) GetRateLimitData(ctx context.Context, id int64) (*apikey.APIKeyRateLimitData, error) {
	return nil, nil
}

// FilterByGroup returns the supplied subscription slice for every group ID. Its
// backing records remain shared, and this fixture performs no group filtering;
// scenarios configure candidate group eligibility in their input records.
func (r *stubUserSubscriptionRepo) FilterByGroup(_ context.Context, subs []billingcore.UserSubscription, _ int64) ([]billingcore.UserSubscription, error) {
	return subs, nil
}

// Create rejects subscription creation with the fixture's
// unimplemented-operation error.
func (r *stubUserSubscriptionRepo) Create(ctx context.Context, sub *billingcore.UserSubscription) error {
	return errors.New("not implemented")
}

// GetByID invokes the configured ID lookup callback for funding or maintenance
// reload. A nil callback reports the unimplemented operation; callback results
// determine sharing, missing records, and failures.
func (r *stubUserSubscriptionRepo) GetByID(ctx context.Context, id int64) (*billingcore.UserSubscription, error) {
	if r.getByID != nil {
		return r.getByID(ctx, id)
	}
	return nil, errors.New("not implemented")
}

// GetByIDIncludeDeleted rejects deleted-inclusive subscription lookup with the
// fixture's unimplemented error.
func (r *stubUserSubscriptionRepo) GetByIDIncludeDeleted(ctx context.Context, id int64) (*billingcore.UserSubscription, error) {
	return nil, errors.New("not implemented")
}

// GetByUserIDAndGroupID rejects group subscription lookup with the fixture's
// unimplemented-operation error.
func (r *stubUserSubscriptionRepo) GetByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (*billingcore.UserSubscription, error) {
	return nil, errors.New("not implemented")
}

// GetActiveByUserIDAndGroupID rejects active group lookup with the fixture's
// unimplemented-operation error.
func (r *stubUserSubscriptionRepo) GetActiveByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (*billingcore.UserSubscription, error) {
	return nil, errors.New("not implemented")
}

// GetLatestByUserIDAndPlanID rejects latest-plan lookup with the fixture's
// unimplemented-operation error.
func (r *stubUserSubscriptionRepo) GetLatestByUserIDAndPlanID(ctx context.Context, userID, planID int64) (*billingcore.UserSubscription, error) {
	return nil, errors.New("not implemented")
}

// Update rejects subscription replacement with the fixture's
// unimplemented-operation error.
func (r *stubUserSubscriptionRepo) Update(ctx context.Context, sub *billingcore.UserSubscription) error {
	return errors.New("not implemented")
}

// Delete rejects subscription deletion with the fixture's
// unimplemented-operation error.
func (r *stubUserSubscriptionRepo) Delete(ctx context.Context, id int64) error {
	return errors.New("not implemented")
}

// Restore rejects subscription restoration with the fixture's
// unimplemented-operation error.
func (r *stubUserSubscriptionRepo) Restore(ctx context.Context, subscriptionID int64, restoredStatus string) (*billingcore.UserSubscription, error) {
	return nil, errors.New("not implemented")
}

// ListByUserID rejects subscription history listing with the fixture's
// unimplemented-operation error.
func (r *stubUserSubscriptionRepo) ListByUserID(ctx context.Context, userID int64) ([]billingcore.UserSubscription, error) {
	return nil, errors.New("not implemented")
}

// ListActiveByUserID invokes the configured candidate-list callback for the
// supplied user. Missing callback configuration reports an unimplemented
// operation, and returned slices retain whatever sharing the callback supplies.
func (r *stubUserSubscriptionRepo) ListActiveByUserID(ctx context.Context, userID int64) ([]billingcore.UserSubscription, error) {
	if r.listActive != nil {
		return r.listActive(ctx, userID)
	}
	return nil, errors.New("not implemented")
}

// ListByUserIDAndPlanID rejects plan history listing with the fixture's
// unimplemented-operation error.
func (r *stubUserSubscriptionRepo) ListByUserIDAndPlanID(ctx context.Context, userID, planID int64) ([]billingcore.UserSubscription, error) {
	return nil, errors.New("not implemented")
}

// ListByGroupID rejects group subscription pagination with the fixture's
// unimplemented-operation error.
func (r *stubUserSubscriptionRepo) ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]billingcore.UserSubscription, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

// ListByPlanID rejects plan subscription pagination with the fixture's
// unimplemented-operation error.
func (r *stubUserSubscriptionRepo) ListByPlanID(ctx context.Context, planID int64, params pagination.PaginationParams) ([]billingcore.UserSubscription, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

// List rejects general subscription pagination with the fixture's
// unimplemented-operation error.
func (r *stubUserSubscriptionRepo) List(ctx context.Context, params pagination.PaginationParams, userID, planID *int64, status, platform, sortBy, sortOrder string) ([]billingcore.UserSubscription, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

// ListBySourceOrderID rejects source-order lookup with the fixture's
// unimplemented-operation error.
func (r *stubUserSubscriptionRepo) ListBySourceOrderID(ctx context.Context, sourceOrderID int64) ([]billingcore.UserSubscription, error) {
	return nil, errors.New("not implemented")
}

// ExistsByUserIDAndGroupID rejects membership queries with the fixture's
// unimplemented-operation error.
func (r *stubUserSubscriptionRepo) ExistsByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (bool, error) {
	return false, errors.New("not implemented")
}

// ExtendExpiry rejects subscription extension with the fixture's
// unimplemented-operation error.
func (r *stubUserSubscriptionRepo) ExtendExpiry(ctx context.Context, subscriptionID int64, newExpiresAt time.Time) error {
	return errors.New("not implemented")
}

// UpdateStatus invokes the configured status callback with the subscription ID
// and requested state. A nil callback reports unimplemented persistence, so
// validation observes that fixture failure directly.
func (r *stubUserSubscriptionRepo) UpdateStatus(ctx context.Context, subscriptionID int64, status string) error {
	if r.updateStatus != nil {
		return r.updateStatus(ctx, subscriptionID, status)
	}
	return errors.New("not implemented")
}

// UpdateNotes rejects subscription note changes with the fixture's
// unimplemented-operation error.
func (r *stubUserSubscriptionRepo) UpdateNotes(ctx context.Context, subscriptionID int64, notes string) error {
	return errors.New("not implemented")
}

// ActivateWindows invokes the activation callback with ID and start time while
// ignoring the activation mask. Missing callback configuration reports the
// unsupported operation; the callback controls any fixture changes.
func (r *stubUserSubscriptionRepo) ActivateWindows(ctx context.Context, id int64, start time.Time, activation billingcore.SubscriptionWindowActivation) error {
	if r.activateWindow != nil {
		return r.activateWindow(ctx, id, start)
	}
	return errors.New("not implemented")
}

// ResetUsageWindows rejects combined window resets with the fixture's
// unimplemented-operation error.
func (r *stubUserSubscriptionRepo) ResetUsageWindows(context.Context, int64, bool, bool, bool, time.Time) error {
	return errors.New("not implemented")
}

// ResetDailyUsage passes ID and the new start to the daily reset callback,
// ignoring expectedWindowStart. A nil callback reports the unsupported reset,
// so these fixture callbacks do not implement database compare-and-swap.
func (r *stubUserSubscriptionRepo) ResetDailyUsage(ctx context.Context, id int64, _ *time.Time, newWindowStart time.Time) error {
	if r.resetDaily != nil {
		return r.resetDaily(ctx, id, newWindowStart)
	}
	return errors.New("not implemented")
}

// ResetWeeklyUsage passes ID and the new start to the weekly reset callback,
// ignoring expectedWindowStart. A nil callback reports the unsupported reset,
// leaving callback behavior to the scenario's maintenance fixture.
func (r *stubUserSubscriptionRepo) ResetWeeklyUsage(ctx context.Context, id int64, _ *time.Time, newWindowStart time.Time) error {
	if r.resetWeekly != nil {
		return r.resetWeekly(ctx, id, newWindowStart)
	}
	return errors.New("not implemented")
}

// ResetMonthlyUsage passes ID and the new start to the monthly reset callback,
// ignoring expectedWindowStart. A nil callback reports the unsupported reset,
// leaving callback behavior to the scenario's maintenance fixture.
func (r *stubUserSubscriptionRepo) ResetMonthlyUsage(ctx context.Context, id int64, _ *time.Time, newWindowStart time.Time) error {
	if r.resetMonthly != nil {
		return r.resetMonthly(ctx, id, newWindowStart)
	}
	return errors.New("not implemented")
}

// IncrementUsage rejects subscription usage writes with the fixture's
// unimplemented-operation error.
func (r *stubUserSubscriptionRepo) IncrementUsage(ctx context.Context, id int64, costUSD float64) error {
	return errors.New("not implemented")
}

// BatchUpdateExpiredStatus rejects expiry maintenance with the fixture's
// unimplemented-operation error.
func (r *stubUserSubscriptionRepo) BatchUpdateExpiredStatus(ctx context.Context) (int64, error) {
	return 0, errors.New("not implemented")
}

// bindAuthTestGroup attaches a new active hydrated group to a nonnil key and
// returns that same key pointer. It replaces GroupID and Group together so
// authentication, quota, and cache fixtures use a consistent bound group.
func bindAuthTestGroup(key *apikey.APIKey) *apikey.APIKey {
	group := &routing.Group{ID: 9001, Name: "explicit-test-group", Status: billingcore.StatusActive, Hydrated: true, RateMultiplier: 1}
	key.GroupID = &group.ID
	key.Group = group
	return key
}

// isAPIKeyNonConsumingRequest delegates the supplied method and path's
// non-consuming classification to gateway HTTP rules. Callers use the result
// for member-budget checks, while other key quota and funding rules retain
// their own admission scope.
func isAPIKeyNonConsumingRequest(method, path string) bool {
	return gatewayhttp.IsAPIKeyNonConsumingRequest(method, path)
}

// abortTeamAPIKeyError delegates team-error response handling to the
// authentication adapter and reports whether it handled err. Recognized errors
// write and abort; callers continue their own error handling when the flag is
// false.
func abortTeamAPIKeyError(c *gin.Context, err error) bool { return keyhttp.AbortTeamError(c, err) }

// RequireGroupAssignment adapts legacy fixture key context to the gateway group
// guard. Loaded ordinary keys require GroupID; a composite no-group marker
// enables the represented exception. The supplied writer produces the protocol
// response, and rejections attach admission and business-limit markers.
func RequireGroupAssignment(writeError gatewayhttp.GatewayErrorWriter) gin.HandlerFunc {
	return gatewayhttp.RequireGroupAssignment(gatewayhttp.GroupAssignmentOptions{Access: func(c *gin.Context) gatewayhttp.GroupAssignmentAccess {
		key, ok := keyhttp.GetAPIKeyFromContext(c)
		if !ok || key == nil {
			return gatewayhttp.GroupAssignmentAccess{}
		}
		_, noGroup := c.Get(gatewayhttp.CompositeKeyNoGroupContextKey)
		return gatewayhttp.GroupAssignmentAccess{Loaded: true, Assigned: key.GroupID != nil, CompositeNoGroup: key.IsComposite && noGroup}
	}, WriteError: writeError, Rejected: func(c *gin.Context) {
		gatewayhttp.MarkOpsClientBusinessLimited(c, gatewayhttp.OpsClientBusinessLimitedReasonAPIKeyGroupUnassigned)
		MarkIngressRejected(c, IngressRejectGroupUnassigned)
	}})
}

// invalidAuthAbuseTestConfig returns an enabled invalid-auth bucket
// configuration using threshold and minute-long observation and block periods.
// Its bounded capacity supports the repeated-client fixtures; tests configure
// threshold before constructing their service.
func invalidAuthAbuseTestConfig(threshold int) *config.Config {
	return &config.Config{APIKey: config.APIKeyConfig{InvalidKeyAbuse: config.InvalidAuthAbuseConfig{
		Enabled:       true,
		Threshold:     threshold,
		WindowSeconds: 60,
		BlockSeconds:  60,
		Capacity:      256,
	}}}
}

// httpRequest constructs a POST with a fixed public peer and optional
// Authorization and x-api-key values. Empty credential arguments leave their
// headers absent so one client can exercise the different invalid-auth failure
// paths.
func httpRequest(t *testing.T, path, authorization, apiKey string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.RemoteAddr = "203.0.113.10:12345"
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	if apiKey != "" {
		req.Header.Set("x-api-key", apiKey)
	}
	return req
}

// GetByIDLite reports successful absence of subscription group details for
// every ID. The fixture has no group source, so callers receive nil data
// instead of a synthetic group name or configuration.
func (subscriptionAuthGroups) GetByIDLite(context.Context, int64) (*billingcore.SubscriptionPlanGroup, error) {
	return nil, nil
}

// newSubscriptionAuthFixture constructs validation around the supplied
// subscription repository and absent group-details reader. The mutation adapter
// has no base client; these scenarios supply transaction-free contexts so
// repository callbacks produce their maintenance observations.
//
// Omitting DateRuntime selects system time and the local calendar. Stop is an
// inert hook here because periodic workers belong to SubscriptionExpiryService.
func newSubscriptionAuthFixture(repo billingcore.UserSubscriptionRepository) *billingcore.SubscriptionService {
	return billingcore.NewSubscriptionService(subscriptionAuthGroups{}, repo, billingpostgres.NewSubscriptionMutations(nil))
}
