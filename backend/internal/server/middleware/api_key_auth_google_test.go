package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey/testkit"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/identity"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	billingcore "github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGoogleAPIKeyAuthRejectsOversizedCredentialsBeforeLookup(t *testing.T) {

	var calls atomic.Int32
	repo := fakeAPIKeyRepo{getByKey: func(context.Context, string) (*apikey.APIKey, error) {
		calls.Add(1)
		return nil, apikey.ErrAPIKeyNotFound
	}}
	cfg := &config.Config{RunMode: config.RunModeSimple}
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

func TestGoogleAPIKeyAuthMarksLookupBulkheadRejection(t *testing.T) {

	repo := fakeAPIKeyRepo{getByKey: func(context.Context, string) (*apikey.APIKey, error) {
		return nil, apikey.ErrAPIKeyAuthOverloaded
	}}
	cfg := &config.Config{RunMode: config.RunModeSimple}
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

func TestGoogleAPIKeyAuthCompositeModelListStillChecksQuota(t *testing.T) {

	user := &identity.User{ID: 7, Status: billingcore.StatusActive, Balance: 10}
	group := &routing.Group{ID: 9, Platform: capability.PlatformGemini, Status: billingcore.StatusActive, Hydrated: true}
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
	cfg := &config.Config{RunMode: config.RunModeStandard}
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
	require.Equal(t, "API key 额度已用完", response.Error.Message)
}

func TestAPIKeyAuthWithSubscriptionGoogle_UsageKeepsUnavailablePreferredSubscription(t *testing.T) {

	now := time.Now()
	group := &routing.Group{ID: 9, Platform: capability.PlatformGemini, Status: billingcore.StatusActive, Hydrated: true}
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
	}, nil, nil, nil, nil, nil, &config.Config{RunMode: config.RunModeStandard})
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
	router.Use(APIKeyAuthWithSubscriptionGoogle(apiKeyService, subscriptionService, &config.Config{RunMode: config.RunModeStandard}))
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
		Source         string `json:"source"`
		SubscriptionID int64  `json:"subscription_id"`
		Available      bool   `json:"available"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Equal(t, "subscription", response.Source)
	require.Equal(t, preferredID, response.SubscriptionID)
	require.False(t, response.Available)
}

type fakeAPIKeyRepo struct {
	getByKey       func(ctx context.Context, key string) (*apikey.APIKey, error)
	updateLastUsed func(ctx context.Context, id int64, usedAt time.Time) error
}

type fakeGoogleSubscriptionRepo struct {
	listActive     func(ctx context.Context, userID int64) ([]billingcore.UserSubscription, error)
	getByID        func(ctx context.Context, id int64) (*billingcore.UserSubscription, error)
	updateStatus   func(ctx context.Context, subscriptionID int64, status string) error
	activateWindow func(ctx context.Context, id int64, start time.Time) error
	resetDaily     func(ctx context.Context, id int64, start time.Time) error
	resetWeekly    func(ctx context.Context, id int64, start time.Time) error
	resetMonthly   func(ctx context.Context, id int64, start time.Time) error
}

func (f fakeGoogleSubscriptionRepo) FilterByGroup(_ context.Context, subs []billingcore.UserSubscription, _ int64) ([]billingcore.UserSubscription, error) {
	return subs, nil
}

func (f fakeAPIKeyRepo) Create(ctx context.Context, key *apikey.APIKey) error {
	return errors.New("not implemented")
}
func (f fakeAPIKeyRepo) GetByID(ctx context.Context, id int64) (*apikey.APIKey, error) {
	return nil, errors.New("not implemented")
}
func (f fakeAPIKeyRepo) GetKeyAndOwnerID(ctx context.Context, id int64) (string, int64, error) {
	return "", 0, errors.New("not implemented")
}
func (f fakeAPIKeyRepo) GetByKey(ctx context.Context, key string) (*apikey.APIKey, error) {
	if f.getByKey == nil {
		return nil, errors.New("unexpected call")
	}
	return f.getByKey(ctx, key)
}
func (f fakeAPIKeyRepo) GetByKeyForAuth(ctx context.Context, key string) (*apikey.APIKey, error) {
	return f.GetByKey(ctx, key)
}
func (f fakeAPIKeyRepo) Update(ctx context.Context, key *apikey.APIKey, _ apikey.APIKeyUpdateFields) error {
	return errors.New("not implemented")
}
func (f fakeAPIKeyRepo) Delete(ctx context.Context, id int64) error {
	return errors.New("not implemented")
}
func (f fakeAPIKeyRepo) DeleteWithAudit(ctx context.Context, id int64) error {
	return errors.New("not implemented")
}
func (f fakeAPIKeyRepo) ListByUserID(ctx context.Context, userID int64, params pagination.PaginationParams, _ apikey.APIKeyListFilters) ([]apikey.APIKey, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}
func (f fakeAPIKeyRepo) VerifyOwnership(ctx context.Context, userID int64, apiKeyIDs []int64) ([]int64, error) {
	return nil, errors.New("not implemented")
}
func (f fakeAPIKeyRepo) CountByUserID(ctx context.Context, userID int64) (int64, error) {
	return 0, errors.New("not implemented")
}
func (f fakeAPIKeyRepo) ExistsByKey(ctx context.Context, key string) (bool, error) {
	return false, errors.New("not implemented")
}
func (f fakeAPIKeyRepo) ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]apikey.APIKey, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}
func (f fakeAPIKeyRepo) SearchAPIKeys(ctx context.Context, userID int64, keyword string, limit int) ([]apikey.APIKey, error) {
	return nil, errors.New("not implemented")
}
func (f fakeAPIKeyRepo) ClearGroupIDByGroupID(ctx context.Context, groupID int64) (int64, error) {
	return 0, errors.New("not implemented")
}
func (f fakeAPIKeyRepo) CountByGroupID(ctx context.Context, groupID int64) (int64, error) {
	return 0, errors.New("not implemented")
}
func (f fakeAPIKeyRepo) ListKeysByUserID(ctx context.Context, userID int64) ([]string, error) {
	return nil, errors.New("not implemented")
}
func (f fakeAPIKeyRepo) ListKeysByGroupID(ctx context.Context, groupID int64) ([]string, error) {
	return nil, errors.New("not implemented")
}
func (f fakeAPIKeyRepo) IncrementQuotaUsed(ctx context.Context, id int64, amount float64) (float64, error) {
	return 0, errors.New("not implemented")
}
func (f fakeAPIKeyRepo) UpdateLastUsed(ctx context.Context, id int64, usedAt time.Time) error {
	if f.updateLastUsed != nil {
		return f.updateLastUsed(ctx, id, usedAt)
	}
	return nil
}
func (f fakeAPIKeyRepo) IncrementRateLimitUsage(ctx context.Context, id int64, cost float64) error {
	return nil
}
func (f fakeAPIKeyRepo) ResetRateLimitWindows(ctx context.Context, id int64) error {
	return nil
}
func (f fakeAPIKeyRepo) GetRateLimitData(ctx context.Context, id int64) (*apikey.APIKeyRateLimitData, error) {
	return &apikey.APIKeyRateLimitData{}, nil
}
func (f fakeAPIKeyRepo) UpdateGroupIDByUserAndGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (int64, error) {
	return 0, errors.New("not implemented")
}

func (f fakeGoogleSubscriptionRepo) Create(ctx context.Context, sub *billingcore.UserSubscription) error {
	return errors.New("not implemented")
}
func (f fakeGoogleSubscriptionRepo) GetByID(ctx context.Context, id int64) (*billingcore.UserSubscription, error) {
	if f.getByID != nil {
		return f.getByID(ctx, id)
	}
	return nil, errors.New("not implemented")
}
func (f fakeGoogleSubscriptionRepo) GetByIDIncludeDeleted(ctx context.Context, id int64) (*billingcore.UserSubscription, error) {
	return nil, errors.New("not implemented")
}
func (f fakeGoogleSubscriptionRepo) GetByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (*billingcore.UserSubscription, error) {
	return nil, errors.New("not implemented")
}
func (f fakeGoogleSubscriptionRepo) GetActiveByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (*billingcore.UserSubscription, error) {
	return nil, errors.New("not implemented")
}
func (f fakeGoogleSubscriptionRepo) GetLatestByUserIDAndPlanID(ctx context.Context, userID, planID int64) (*billingcore.UserSubscription, error) {
	return nil, errors.New("not implemented")
}
func (f fakeGoogleSubscriptionRepo) Update(ctx context.Context, sub *billingcore.UserSubscription) error {
	return errors.New("not implemented")
}
func (f fakeGoogleSubscriptionRepo) Delete(ctx context.Context, id int64) error {
	return errors.New("not implemented")
}
func (f fakeGoogleSubscriptionRepo) Restore(ctx context.Context, subscriptionID int64, restoredStatus string) (*billingcore.UserSubscription, error) {
	return nil, errors.New("not implemented")
}
func (f fakeGoogleSubscriptionRepo) ListByUserID(ctx context.Context, userID int64) ([]billingcore.UserSubscription, error) {
	return nil, errors.New("not implemented")
}
func (f fakeGoogleSubscriptionRepo) ListActiveByUserID(ctx context.Context, userID int64) ([]billingcore.UserSubscription, error) {
	if f.listActive != nil {
		return f.listActive(ctx, userID)
	}
	return nil, errors.New("not implemented")
}
func (f fakeGoogleSubscriptionRepo) ListByUserIDAndPlanID(ctx context.Context, userID, planID int64) ([]billingcore.UserSubscription, error) {
	return nil, errors.New("not implemented")
}
func (f fakeGoogleSubscriptionRepo) ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]billingcore.UserSubscription, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}
func (f fakeGoogleSubscriptionRepo) ListByPlanID(ctx context.Context, planID int64, params pagination.PaginationParams) ([]billingcore.UserSubscription, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}
func (f fakeGoogleSubscriptionRepo) List(ctx context.Context, params pagination.PaginationParams, userID, planID *int64, status, platform, sortBy, sortOrder string) ([]billingcore.UserSubscription, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}
func (f fakeGoogleSubscriptionRepo) ListBySourceOrderID(ctx context.Context, sourceOrderID int64) ([]billingcore.UserSubscription, error) {
	return nil, errors.New("not implemented")
}
func (f fakeGoogleSubscriptionRepo) ExistsByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (bool, error) {
	return false, errors.New("not implemented")
}
func (f fakeGoogleSubscriptionRepo) ExtendExpiry(ctx context.Context, subscriptionID int64, newExpiresAt time.Time) error {
	return errors.New("not implemented")
}
func (f fakeGoogleSubscriptionRepo) UpdateStatus(ctx context.Context, subscriptionID int64, status string) error {
	if f.updateStatus != nil {
		return f.updateStatus(ctx, subscriptionID, status)
	}
	return errors.New("not implemented")
}
func (f fakeGoogleSubscriptionRepo) UpdateNotes(ctx context.Context, subscriptionID int64, notes string) error {
	return errors.New("not implemented")
}
func (f fakeGoogleSubscriptionRepo) ActivateWindows(ctx context.Context, id int64, start time.Time, activation billingcore.SubscriptionWindowActivation) error {
	if f.activateWindow != nil {
		return f.activateWindow(ctx, id, start)
	}
	return errors.New("not implemented")
}
func (f fakeGoogleSubscriptionRepo) ResetUsageWindows(context.Context, int64, bool, bool, bool, time.Time) error {
	return errors.New("not implemented")
}
func (f fakeGoogleSubscriptionRepo) ResetDailyUsage(ctx context.Context, id int64, _ *time.Time, start time.Time) error {
	if f.resetDaily != nil {
		return f.resetDaily(ctx, id, start)
	}
	return errors.New("not implemented")
}
func (f fakeGoogleSubscriptionRepo) ResetWeeklyUsage(ctx context.Context, id int64, _ *time.Time, start time.Time) error {
	if f.resetWeekly != nil {
		return f.resetWeekly(ctx, id, start)
	}
	return errors.New("not implemented")
}
func (f fakeGoogleSubscriptionRepo) ResetMonthlyUsage(ctx context.Context, id int64, _ *time.Time, start time.Time) error {
	if f.resetMonthly != nil {
		return f.resetMonthly(ctx, id, start)
	}
	return errors.New("not implemented")
}
func (f fakeGoogleSubscriptionRepo) IncrementUsage(ctx context.Context, id int64, costUSD float64) error {
	return errors.New("not implemented")
}
func (f fakeGoogleSubscriptionRepo) BatchUpdateExpiredStatus(ctx context.Context) (int64, error) {
	return 0, errors.New("not implemented")
}

type googleErrorResponse struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

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

func TestApiKeyAuthWithSubscriptionGoogleSetsGroupContext(t *testing.T) {

	group := &routing.Group{
		ID:       99,
		Name:     "g1",
		Status:   billingcore.StatusActive,
		Platform: capability.PlatformGemini,
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
		&config.Config{RunMode: config.RunModeSimple},
	)
	apiKeyService.Start()

	cfg := &config.Config{RunMode: config.RunModeSimple}
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

func TestApiKeyAuthWithSubscriptionGoogle_QueryKeyAllowedOnV1Beta(t *testing.T) {

	r := gin.New()
	apiKeyService := newTestAPIKeyService(fakeAPIKeyRepo{
		getByKey: func(ctx context.Context, key string) (*apikey.APIKey, error) {
			return &apikey.APIKey{
				ID:     1,
				Key:    key,
				Status: billingcore.StatusActive,
				User: &identity.User{
					ID:     123,
					Status: billingcore.StatusActive,
				},
			}, nil
		},
	})
	cfg := &config.Config{RunMode: config.RunModeSimple}
	r.Use(APIKeyAuthWithSubscriptionGoogle(apiKeyService, nil, cfg))
	r.GET("/v1beta/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	req := httptest.NewRequest(http.MethodGet, "/v1beta/test?key=valid", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}

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
			ID:       groupID,
			Name:     "deleted",
			Status:   "deleted",
			Platform: capability.PlatformGemini,
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
	r.Use(APIKeyAuthWithSubscriptionGoogle(apiKeyService, nil, &config.Config{RunMode: config.RunModeSimple}))
	r.GET("/v1beta/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	req := httptest.NewRequest(http.MethodGet, "/v1beta/test", nil)
	req.Header.Set("x-goog-api-key", apiKey.Key)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	var resp googleErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "API Key 所属分组已删除", resp.Error.Message)
	require.True(t, markedBusinessLimited)
	require.Equal(t, gatewayhttp.OpsClientBusinessLimitedReasonAPIKeyGroupUnavailable, businessLimitedReason)
	require.True(t, rejected)
	require.Equal(t, IngressRejectGroupDeleted, rejectReason)
}

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

func TestApiKeyAuthWithSubscriptionGoogle_RejectsRuntimeKeyRestrictions(t *testing.T) {

	expiredAt := time.Now().Add(-time.Minute)
	tests := []struct {
		name            string
		configureAPIKey func(*apikey.APIKey)
		wantCode        int
		wantMessage     string
		wantStatus      string
	}{
		{
			name: "expired_at",
			configureAPIKey: func(apiKey *apikey.APIKey) {
				apiKey.ExpiresAt = &expiredAt
			},
			wantCode:    http.StatusForbidden,
			wantMessage: "API key 已过期",
			wantStatus:  "PERMISSION_DENIED",
		},
		{
			name: "quota_exhausted",
			configureAPIKey: func(apiKey *apikey.APIKey) {
				apiKey.Quota = 10
				apiKey.QuotaUsed = 10
			},
			wantCode:    http.StatusTooManyRequests,
			wantMessage: "API key 额度已用完",
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
			cfg := &config.Config{RunMode: config.RunModeStandard}
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
	cfg := &config.Config{RunMode: config.RunModeSimple}
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

func TestApiKeyAuthWithSubscriptionGoogle_BalanceBelowMinimumReserve(t *testing.T) {

	// 鉴权层保持历史语义：MinimumBalanceReserve 只用于 billing-cache 预检，
	// 0 < balance < reserve 的用户不得在鉴权中间件被硬 403。
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
	cfg := &config.Config{RunMode: config.RunModeSimple}
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
	cfg := &config.Config{RunMode: config.RunModeSimple}
	r.Use(APIKeyAuthWithSubscriptionGoogle(apiKeyService, nil, cfg))
	r.GET("/v1beta/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	req := httptest.NewRequest(http.MethodGet, "/v1beta/test", nil)
	req.Header.Set("x-goog-api-key", apiKey.Key)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, touchCalls)
}

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
	cfg := &config.Config{RunMode: config.RunModeStandard}
	r.Use(APIKeyAuthWithSubscriptionGoogle(apiKeyService, nil, cfg))
	r.GET("/v1beta/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	req := httptest.NewRequest(http.MethodGet, "/v1beta/test", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey.Key)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, touchCalls)
}

func TestApiKeyAuthWithSubscriptionGoogle_ExhaustedSubscriptionFallsBackToBalance(t *testing.T) {

	group := &routing.Group{
		ID:       77,
		Name:     "gemini-sub",
		Status:   billingcore.StatusActive,
		Platform: capability.PlatformGemini,
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
	r.Use(APIKeyAuthWithSubscriptionGoogle(apiKeyService, subscriptionService, &config.Config{RunMode: config.RunModeStandard}))
	r.GET("/v1beta/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	req := httptest.NewRequest(http.MethodGet, "/v1beta/test", nil)
	req.Header.Set("x-goog-api-key", apiKey.Key)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}
