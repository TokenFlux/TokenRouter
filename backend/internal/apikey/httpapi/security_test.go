package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/apikey"

	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"

	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type apiKeyHandlerSecurityRepoStub struct {
	apikey.APIKeyRepository
	keys map[int64]*apikey.APIKey
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

			// 回归保护：无权访问和不存在使用相同响应，避免通过状态码枚举 API Key ID。
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
