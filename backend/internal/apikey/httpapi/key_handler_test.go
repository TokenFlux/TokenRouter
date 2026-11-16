package httpapi

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// rotationHTTPRepository records secret replacement calls and supplies an
// optional write failure.
type rotationHTTPRepository struct {
	apiKeyHandlerSecurityRepoStub       // embedded credential lookup fixture
	calls                         int   // recorded credential replacement attempts
	err                           error // injected replacement write error
}

// apiKeyHandlerSecurityRepoStub returns shallow records for credential
// visibility and rotation tests.
type apiKeyHandlerSecurityRepoStub struct {
	apikey.APIKeyRepository                          // unused repository methods require setup
	keys                    map[int64]*apikey.APIKey // retained credential lookup records
}

// availableGroupsUsers supplies one ordinary user with no exclusive group
// grants.
type availableGroupsUsers struct{}

// availableGroupsRepository offers public and exclusive candidates for console
// group filtering tests.
type availableGroupsRepository struct {
	apikey.GroupRepository // unused group methods require setup
}

// forceClaudeCacheHTTPRepository captures Key creation and update records for
// console HTTP handler assertions.
type forceClaudeCacheHTTPRepository struct {
	apikey.APIKeyRepository                           // unused repository methods require setup
	stored                  *apikey.APIKey            // latest persisted credential record
	lastUpdateFields        apikey.APIKeyUpdateFields // latest captured column mask
}

// TestValidateAPIKeyCreateRequest supplies omitted and bounded monetary
// allowances with positive or invalid relative expiry values. It checks
// accepted limits and early rejection of negative, nonfinite, oversized or
// nonpositive expiry input.
func TestValidateAPIKeyCreateRequest(t *testing.T) {
	t.Parallel()

	zero := 0.0
	// Exercise the nearest representable amount below the exclusive decimal bound.
	largeValid := math.Nextafter(1_000_000_000_000, 0)
	positiveDays := 1
	require.NoError(t, ValidateAPIKeyCreateRequest(CreateAPIKeyRequest{
		Quota:         &zero,
		RateLimit5h:   &largeValid,
		ExpiresInDays: &positiveDays,
	}))
	require.NoError(t, ValidateAPIKeyCreateRequest(CreateAPIKeyRequest{}))

	negative, nan, inf, tooLarge := -1.0, math.NaN(), math.Inf(1), 1_000_000_000_000.0
	zeroDays, negativeDays := 0, -1
	invalid := []CreateAPIKeyRequest{
		{Quota: &negative},
		{Quota: &nan},
		{RateLimit5h: &inf},
		{RateLimit1d: &negative},
		{RateLimit7d: &tooLarge},
		{ExpiresInDays: &zeroDays},
		{ExpiresInDays: &negativeDays},
	}
	for _, request := range invalid {
		require.Error(t, ValidateAPIKeyCreateRequest(request))
	}
}

// TestValidateAPIKeyUpdateRequest supplies omitted, unbounded and large valid
// monetary replacements beside invalid numeric values. It checks the service
// allowance error identity is retained for rejected HTTP inputs.
func TestValidateAPIKeyUpdateRequest(t *testing.T) {
	t.Parallel()

	zero := 0.0
	// Exercise the nearest representable amount below the exclusive decimal bound.
	largeValid := math.Nextafter(1_000_000_000_000, 0)
	require.NoError(t, ValidateAPIKeyUpdateRequest(UpdateAPIKeyRequest{
		Quota:       &zero,
		RateLimit7d: &largeValid,
	}))

	negative, nan, inf, tooLarge := -1.0, math.NaN(), math.Inf(-1), 1e100
	invalid := []UpdateAPIKeyRequest{
		{Quota: &negative},
		{RateLimit5h: &nan},
		{RateLimit1d: &inf},
		{RateLimit7d: &tooLarge},
	}
	for _, request := range invalid {
		require.ErrorIs(t, ValidateAPIKeyUpdateRequest(request), apikey.ErrAPIKeyLimitInvalid)
	}
}

// TestRotateCredentialRoute exercises subject presence, signed ID validation,
// missing or foreign records, managed Keys and write conflict. It checks
// status, replacement-secret shape, retained usage and absence of old-secret
// disclosure on rejection.
func TestRotateCredentialRoute(t *testing.T) {
	managed := "creative_studio"
	for _, test := range []struct {
		name   string // HTTP scenario display label
		id     string // submitted route identifier text
		userID int64  // injected authenticated subject identifier
		err    error  // injected replacement write error
		status int    // expected HTTP response status
	}{
		{name: "success", id: "7", userID: 3, status: http.StatusOK},
		{name: "unauthenticated", id: "7", status: http.StatusUnauthorized},
		{name: "invalid_id", id: "bad", userID: 3, status: http.StatusBadRequest},
		{name: "negative_id", id: "-1", userID: 3, status: http.StatusBadRequest},
		{name: "missing", id: "404", userID: 3, status: http.StatusNotFound},
		{name: "other_owner", id: "7", userID: 4, status: http.StatusNotFound},
		{name: "managed", id: "8", userID: 3, status: http.StatusNotFound},
		{name: "conflict", id: "7", userID: 3, err: apikey.ErrAPIKeyRotationConflict, status: http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &rotationHTTPRepository{
				apiKeyHandlerSecurityRepoStub: apiKeyHandlerSecurityRepoStub{keys: map[int64]*apikey.APIKey{
					7: {ID: 7, UserID: 3, Key: "sk-old", Name: "key", Status: apikey.StatusAPIKeyActive, QuotaUsed: 12},
					8: {ID: 8, UserID: 3, ManagedBy: &managed},
				}},
				err: test.err,
			}
			svc := apikey.NewAPIKeyService(repo, nil, nil, nil, nil, nil, &apikey.Options{})
			router := gin.New()
			group := router.Group("/api/v1")
			if test.userID != 0 {
				group.Use(func(c *gin.Context) {
					c.Set(authctx.ContextKeyUser, authctx.AuthSubject{UserID: test.userID})
				})
			}
			RegisterUserRoutes(group, NewAPIKeyHandler(svc, func(*routing.Group, *accessview.GroupCapacitySummary) *struct{} {
				return nil
			}))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/keys/"+test.id+"/rotate", nil))
			require.Equal(t, test.status, rec.Code, rec.Body.String())
			if test.status == http.StatusOK {
				var result struct {
					Data struct { // decoded HTTP response payload
						ID        int64   `json:"id"`         // returned record identifier
						Key       string  `json:"key"`        // returned replacement credential text
						QuotaUsed float64 `json:"quota_used"` // returned saved USD usage
					} `json:"data"`
				}
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
				require.Equal(t, int64(7), result.Data.ID)
				require.Equal(t, float64(12), result.Data.QuotaUsed)
				require.Regexp(t, `^sk-[0-9a-f]{64}$`, result.Data.Key)
				require.Equal(t, result.Data.Key, repo.keys[7].Key)
			} else {
				require.NotContains(t, rec.Body.String(), "sk-old")
				if test.err == nil {
					require.Zero(t, repo.calls)
				}
			}
		})
	}
}

// TestKeyWritesRejectRetiredDefaultGroupFallback submits the unsupported
// default-group field as true, false and null to both write routes. It checks
// strict JSON decoding rejects the field before the unconfigured service can
// run.
func TestKeyWritesRejectRetiredDefaultGroupFallback(t *testing.T) {
	handler := NewAPIKeyHandler[struct{}](nil, nil)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set(authctx.ContextKeyUser, authctx.AuthSubject{UserID: 1}); c.Next() })
	router.POST("/keys", handler.Create)
	router.PUT("/keys/:id", handler.Update)
	for _, value := range []string{"true", "false", "null"} {
		for _, endpoint := range []struct {
			method, path string // request HTTP method; submitted endpoint path
		}{{http.MethodPost, "/keys"}, {http.MethodPut, "/keys/1"}} {
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader(`{"name":"key","group_id":1,"fallback_to_default_group_when_unavailable":`+value+`}`))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, req)
			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Contains(t, recorder.Body.String(), "fallback_to_default_group_when_unavailable")
		}
	}
}

// TestAPIKeyHandler_GetByID_HidesUnauthorizedKeyExistence requests a foreign
// credential and a missing credential under one subject. It checks both routes
// return identical not-found envelopes so those cases expose no different
// lookup result.
func TestAPIKeyHandler_GetByID_HidesUnauthorizedKeyExistence(t *testing.T) {
	repo := &apiKeyHandlerSecurityRepoStub{
		keys: map[int64]*apikey.APIKey{
			7: {ID: 7, UserID: 99, Status: apikey.StatusAPIKeyActive},
		},
	}
	router := newAPIKeyHandlerSecurityRouter(t, repo, 42)

	var first response.Response
	for _, path := range []string{"/api/v1/api-keys/7", "/api/v1/api-keys/404"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			require.Equal(t, http.StatusNotFound, rec.Code)

			// Unauthorized access and a missing key return the same response.
			var got response.Response
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
			require.Equal(t, http.StatusNotFound, got.Code)
			require.Equal(t, "api key not found", got.Message)
			if first.Code == 0 {
				first = got
			} else {
				require.Equal(t, first, got)
			}
		})
	}
}

// RotateCredential counts replacement and returns its configured error before
// changing stored text when failed. Success writes the supplied secret on the
// fixture record; the old-secret compare is left to production storage tests.
func (r *rotationHTTPRepository) RotateCredential(_ context.Context, key *apikey.APIKey, _ string) error {
	r.calls++
	if r.err != nil {
		return r.err
	}
	r.keys[key.ID].Key = key.Key
	return nil
}

// GetByID returns a shallow copy of the record matching id or ErrAPIKeyNotFound
// when absent. Pointer and collection fields remain shared with the stored
// fixture; the context is ignored.
func (s *apiKeyHandlerSecurityRepoStub) GetByID(ctx context.Context, id int64) (*apikey.APIKey, error) {
	key, ok := s.keys[id]
	if !ok {
		return nil, apikey.ErrAPIKeyNotFound
	}
	clone := *key
	return &clone, nil
}

// newAPIKeyHandlerSecurityRouter starts a narrow Key service and registers
// cleanup after the test. Its router installs the supplied user subject
// directly and binds the detail endpoint to isolate ownership response
// behavior. Use this router for rejected lookups; successful presentation would
// reach its unbound group presenter.
func newAPIKeyHandlerSecurityRouter(t *testing.T, repo *apiKeyHandlerSecurityRepoStub, userID int64) *gin.Engine {
	t.Helper()
	apiKeySvc := apikey.NewAPIKeyService(repo, nil, nil, nil, nil, nil, nil)
	apiKeySvc.Start()
	// Join service shutdown after this router scenario so any allocated
	// authentication resources stop before the supplied fixture can be discarded.
	t.Cleanup(apiKeySvc.Stop)
	handler := NewAPIKeyHandler[struct{}](apiKeySvc, nil)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(authctx.ContextKeyUser, authctx.AuthSubject{UserID: userID})
		c.Next()
	})
	router.GET("/api/v1/api-keys/:id", handler.GetByID)
	return router
}

// GetByID returns one fresh ordinary user view for any requested identifier.
func (availableGroupsUsers) GetByID(context.Context, int64) (*apikey.User, error) {
	return &apikey.User{ID: 1}, nil
}

// ListActive returns fresh public and exclusive group candidates for access
// filtering.
func (availableGroupsRepository) ListActive(context.Context) ([]routing.Group, error) {
	return []routing.Group{{ID: 1, Status: "active"}, {ID: 2, Status: "active", IsExclusive: true}}, nil
}

// GetByID returns the active public group when id equals one and reports
// missing group data otherwise.
func (availableGroupsRepository) GetByID(_ context.Context, id int64) (*routing.Group, error) {
	if id == 1 {
		return &routing.Group{ID: 1, Status: "active"}, nil
	}
	return nil, routing.ErrGroupNotFound
}

// Create stores a shallow copy of key with ID 1 for subsequent update calls.
func (r *forceClaudeCacheHTTPRepository) Create(_ context.Context, key *apikey.APIKey) error {
	clone := *key
	clone.ID = 1
	r.stored = &clone
	*key = clone
	return nil
}

// ExistsByKey reports that every generated or custom credential secret is unused.
func (r *forceClaudeCacheHTTPRepository) ExistsByKey(context.Context, string) (bool, error) {
	return false, nil
}

// GetByID returns a shallow copy of the stored credential when id matches.
// Mismatched or absent records return ErrAPIKeyNotFound.
func (r *forceClaudeCacheHTTPRepository) GetByID(_ context.Context, id int64) (*apikey.APIKey, error) {
	if r.stored == nil || r.stored.ID != id {
		return nil, apikey.ErrAPIKeyNotFound
	}
	clone := *r.stored
	return &clone, nil
}

// Update records the supplied Key copy and selected persistence mask.
func (r *forceClaudeCacheHTTPRepository) Update(_ context.Context, key *apikey.APIKey, fields apikey.APIKeyUpdateFields) error {
	clone := *key
	r.stored = &clone
	r.lastUpdateFields = fields
	return nil
}

// TestAPIKeyHandler_CreateAndUpdateForceClaude1hCache submits HTTP creation and
// update requests toggling force_claude_1h_cache. It checks JSON binding,
// persisted repository state, selected update mask, and serialized DTO output.
func TestAPIKeyHandler_CreateAndUpdateForceClaude1hCache(t *testing.T) {
	repo := &forceClaudeCacheHTTPRepository{}
	service := apikey.NewAPIKeyService(repo, availableGroupsUsers{}, availableGroupsRepository{}, nil, nil, nil, &apikey.Options{})
	service.Start()
	t.Cleanup(service.Stop)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(authctx.ContextKeyUser, authctx.AuthSubject{UserID: 1})
		c.Next()
	})
	handler := NewAPIKeyHandler(service, func(*routing.Group, *accessview.GroupCapacitySummary) *struct{} { return nil })
	router.POST("/keys", handler.Create)
	router.PUT("/keys/:id", handler.Update)

	createRec := httptest.NewRecorder()
	createReq := httptest.NewRequest(http.MethodPost, "/keys", strings.NewReader(`{"name":"claude-key","group_id":1,"force_claude_1h_cache":true}`))
	createReq.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(createRec, createReq)
	require.Equal(t, http.StatusOK, createRec.Code, createRec.Body.String())

	var created struct {
		Data struct { // decoded console Key payload
			ID                 int64 `json:"id"`                    // returned credential identifier
			ForceClaude1hCache bool  `json:"force_claude_1h_cache"` // returned Claude 1h cache flag
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(createRec.Body.Bytes(), &created))
	require.Equal(t, int64(1), created.Data.ID)
	require.True(t, created.Data.ForceClaude1hCache)
	require.True(t, repo.stored.ForceClaude1hCache)

	updateRec := httptest.NewRecorder()
	updateReq := httptest.NewRequest(http.MethodPut, "/keys/1", strings.NewReader(`{"force_claude_1h_cache":false}`))
	updateReq.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(updateRec, updateReq)
	require.Equal(t, http.StatusOK, updateRec.Code, updateRec.Body.String())

	var updated struct {
		Data struct { // decoded console Key payload
			ForceClaude1hCache bool `json:"force_claude_1h_cache"` // returned Claude 1h cache flag
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(updateRec.Body.Bytes(), &updated))
	require.False(t, updated.Data.ForceClaude1hCache)
	require.False(t, repo.stored.ForceClaude1hCache)
	require.Equal(t, apikey.APIKeyUpdateFields{ForceClaude1hCache: true}, repo.lastUpdateFields)
}

// TestAvailableGroupsOptionalModels varies include_models omission, valid
// booleans and invalid text for one subject. It checks permission-filtered
// group output, optional model computation and bad-request rejection before
// presentation.
func TestAvailableGroupsOptionalModels(t *testing.T) {
	for _, query := range []string{"", "?include_models=true", "?include_models=false", "?include_models=invalid"} {
		t.Run(query, func(t *testing.T) {
			// groupResponse holds the permission-filtered group and optional models inspected here.
			type groupResponse struct {
				ID     int64    `json:"id"`               // returned record identifier
				Models []string `json:"models,omitempty"` // presented optional model identifiers
			}
			service := apikey.NewAPIKeyService(nil, availableGroupsUsers{}, availableGroupsRepository{}, nil, nil, nil, nil)
			handler := NewAPIKeyHandler(service, func(group *routing.Group, _ *accessview.GroupCapacitySummary) *groupResponse {
				return &groupResponse{ID: group.ID}
			})
			calls := 0
			handler.SetGroupPresentation(func(_ context.Context, group *routing.Group, _ *accessview.GroupCapacitySummary, includeModels bool) *groupResponse {
				if !includeModels {
					return &groupResponse{ID: group.ID}
				}
				calls++
				return &groupResponse{ID: group.ID, Models: []string{"known"}}
			})
			router := gin.New()
			router.Use(func(c *gin.Context) { c.Set(authctx.ContextKeyUser, authctx.AuthSubject{UserID: 1}) })
			router.GET("/groups/available", handler.GetAvailableGroups)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/groups/available"+query, nil))
			if query == "?include_models=invalid" {
				require.Equal(t, http.StatusBadRequest, recorder.Code)
				require.Zero(t, calls)
				return
			}
			require.Equal(t, http.StatusOK, recorder.Code)
			var result struct {
				Data []groupResponse `json:"data"` // decoded HTTP response payload
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
			require.Len(t, result.Data, 1)
			require.Equal(t, int64(1), result.Data[0].ID)
			if query == "?include_models=false" {
				require.Zero(t, calls)
				require.Empty(t, result.Data[0].Models)
			} else {
				require.Equal(t, 1, calls)
				require.Equal(t, []string{"known"}, result.Data[0].Models)
			}
		})
	}
}
