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

type rotationHTTPRepository struct {
	apiKeyHandlerSecurityRepoStub
	calls int
	err   error
}

type apiKeyHandlerSecurityRepoStub struct {
	apikey.APIKeyRepository
	keys map[int64]*apikey.APIKey
}

// availableGroupsUsers 为分组接口提供普通用户。
type availableGroupsUsers struct{}

type availableGroupsRepository struct{ apikey.GroupRepository }

func TestValidateAPIKeyCreateRequest(t *testing.T) {
	t.Parallel()

	zero := 0.0
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

func TestValidateAPIKeyUpdateRequest(t *testing.T) {
	t.Parallel()

	zero := 0.0
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

// TestRotateCredentialRoute 检查凭据轮换路由的归属校验和新凭据响应。
func TestRotateCredentialRoute(t *testing.T) {
	managed := "creative_studio"
	for _, test := range []struct {
		name   string
		id     string
		userID int64
		err    error
		status int
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
					Data struct {
						ID        int64   `json:"id"`
						Key       string  `json:"key"`
						QuotaUsed float64 `json:"quota_used"`
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

// TestKeyWritesRejectRetiredDefaultGroupFallback 检查身份认证后、读取 Key 服务前拒绝旧回退字段，包括 false 和 null。
func TestKeyWritesRejectRetiredDefaultGroupFallback(t *testing.T) {
	handler := NewAPIKeyHandler[struct{}](nil, nil)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set(authctx.ContextKeyUser, authctx.AuthSubject{UserID: 1}); c.Next() })
	router.POST("/keys", handler.Create)
	router.PUT("/keys/:id", handler.Update)
	for _, value := range []string{"true", "false", "null"} {
		for _, endpoint := range []struct{ method, path string }{{http.MethodPost, "/keys"}, {http.MethodPut, "/keys/1"}} {
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader(`{"name":"key","group_id":1,"fallback_to_default_group_when_unavailable":`+value+`}`))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, req)
			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Contains(t, recorder.Body.String(), "fallback_to_default_group_when_unavailable")
		}
	}
}

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

			// 无权访问和不存在的 API Key 返回相同响应。
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

func (r *rotationHTTPRepository) RotateCredential(_ context.Context, key *apikey.APIKey, _ string) error {
	r.calls++
	if r.err != nil {
		return r.err
	}
	r.keys[key.ID].Key = key.Key
	return nil
}

func (s *apiKeyHandlerSecurityRepoStub) GetByID(ctx context.Context, id int64) (*apikey.APIKey, error) {
	key, ok := s.keys[id]
	if !ok {
		return nil, apikey.ErrAPIKeyNotFound
	}
	clone := *key
	return &clone, nil
}

func newAPIKeyHandlerSecurityRouter(t *testing.T, repo *apiKeyHandlerSecurityRepoStub, userID int64) *gin.Engine {
	t.Helper()
	apiKeySvc := apikey.NewAPIKeyService(repo, nil, nil, nil, nil, nil, nil)
	apiKeySvc.Start()
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

func (availableGroupsUsers) GetByID(context.Context, int64) (*apikey.User, error) {
	return &apikey.User{ID: 1}, nil
}

func (availableGroupsRepository) ListActive(context.Context) ([]routing.Group, error) {
	return []routing.Group{{ID: 1, Status: "active"}, {ID: 2, Status: "active", IsExclusive: true}}, nil
}

// TestAvailableGroupsOptionalModels 轻量查询跳过目录计算，同时保留分组权限过滤。
func TestAvailableGroupsOptionalModels(t *testing.T) {
	for _, query := range []string{"", "?include_models=true", "?include_models=false", "?include_models=invalid"} {
		t.Run(query, func(t *testing.T) {
			type groupResponse struct {
				ID     int64    `json:"id"`
				Models []string `json:"models,omitempty"`
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
				Data []groupResponse `json:"data"`
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
