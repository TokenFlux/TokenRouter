package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/config"
	"github.com/BrandonVee/TokenRouter/internal/handler"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

// codexModelsRemovalAccountRepo 提供仅含 API Key 账号的分组模型数据。
type codexModelsRemovalAccountRepo struct {
	service.AccountRepository
	accounts []service.Account
}

func (r *codexModelsRemovalAccountRepo) ListSchedulableByGroupID(context.Context, int64) ([]service.Account, error) {
	return append([]service.Account(nil), r.accounts...), nil
}

// newCodexModelsRemovalGatewayHandler 构造可执行本地模型列表逻辑的测试 handler。
func newCodexModelsRemovalGatewayHandler(repo service.AccountRepository) *handler.GatewayHandler {
	gatewayService := service.NewGatewayService(
		repo,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	return handler.NewGatewayHandler(
		gatewayService,
		nil,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		&config.Config{},
		nil,
	)
}

// 带 client_version 的模型请求应继续返回纯 API Key 分组的本地模型列表。
func TestGatewayRoutesModelsWithClientVersionUsesLocalList(t *testing.T) {
	repo := &codexModelsRemovalAccountRepo{
		accounts: []service.Account{
			{
				ID:       1,
				Platform: service.PlatformOpenAI,
				Type:     service.AccountTypeAPIKey,
				Credentials: map[string]any{
					"api_key": "sk-test",
					"model_mapping": map[string]any{
						"local-api-key-model": "local-api-key-model",
					},
				},
			},
		},
	}
	router := newGatewayRoutesTestRouterWithGatewayHandler(
		newCodexModelsRemovalGatewayHandler(repo),
		service.PlatformOpenAI,
	)
	paths := []string{
		"/v1/models?client_version=0.144.0",
		"/models?client_version=0.144.0",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			recorder := httptest.NewRecorder()

			router.ServeHTTP(recorder, req)

			require.Equal(t, http.StatusOK, recorder.Code)
			require.NotContains(t, recorder.Body.String(), "No available OpenAI OAuth accounts")
			var response struct {
				Object string `json:"object"`
				Data   []struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			require.Equal(t, "list", response.Object)
			require.Len(t, response.Data, 1)
			require.Equal(t, "local-api-key-model", response.Data[0].ID)
		})
	}
}

// Codex manifest 路由应被移除，已有 Responses 兼容路由仍需保留。
func TestGatewayRoutesCodexModelsManifestPathIsRemoved(t *testing.T) {
	router := newGatewayRoutesTestRouter(service.PlatformOpenAI)

	req := httptest.NewRequest(http.MethodGet, "/backend-api/codex/models?client_version=0.144.0", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	require.Equal(t, http.StatusNotFound, recorder.Code)

	// 合法 Live call 动态段仍需进入 Sideband handler，而不是被旧路由守卫误判为 404。
	req = httptest.NewRequest(http.MethodGet, "/backend-api/codex/call_test", nil)
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	require.NotEqual(t, http.StatusNotFound, recorder.Code)

	registered := make(map[string]string)
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = route.Handler
	}
	require.NotEmpty(t, registered[http.MethodPost+" /backend-api/codex/responses"])
	require.Empty(t, registered[http.MethodGet+" /backend-api/codex/models"])
	require.Equal(t, registered[http.MethodGet+" /v1/models"], registered[http.MethodGet+" /models"])
}
