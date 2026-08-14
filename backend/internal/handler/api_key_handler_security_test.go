package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/pkg/response"
	middleware2 "github.com/BrandonVee/TokenRouter/internal/server/middleware"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type apiKeyHandlerSecurityRepoStub struct {
	service.APIKeyRepository
	keys map[int64]*service.APIKey
}

func (s *apiKeyHandlerSecurityRepoStub) GetByID(ctx context.Context, id int64) (*service.APIKey, error) {
	key, ok := s.keys[id]
	if !ok {
		return nil, service.ErrAPIKeyNotFound
	}
	clone := *key
	return &clone, nil
}

func newAPIKeyHandlerSecurityRouter(repo *apiKeyHandlerSecurityRepoStub, userID int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	apiKeySvc := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, nil)
	handler := NewAPIKeyHandler(apiKeySvc)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: userID})
		c.Next()
	})
	router.GET("/api/v1/api-keys/:id", handler.GetByID)
	return router
}

func TestAPIKeyHandler_GetByID_HidesUnauthorizedKeyExistence(t *testing.T) {
	repo := &apiKeyHandlerSecurityRepoStub{
		keys: map[int64]*service.APIKey{
			7: {ID: 7, UserID: 99, Status: service.StatusAPIKeyActive},
		},
	}
	router := newAPIKeyHandlerSecurityRouter(repo, 42)

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
