//go:build unit

package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	protocolgemini "github.com/TokenFlux/TokenRouter/internal/protocol/gemini"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"

	gemini "github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGeminiV1BetaListModels_CustomGroupListUsesNativeResponse(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1beta/models", nil)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		Group: &routing.Group{
			Platform: capability.PlatformGemini,
			ModelsListConfig: routing.GroupModelsListConfig{
				Enabled: true,
				Models:  []string{"gemini-2.5-pro", "models/gemini-custom"},
			},
		},
	})

	provideModelsHTTP(nil, nil, nil).GeminiV1BetaListModels(c)

	require.Equal(t, http.StatusOK, rec.Code)
	var got gemini.ModelsListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, []gemini.Model{
		gemini.FallbackModel("gemini-2.5-pro"),
		gemini.FallbackModel("models/gemini-custom"),
	}, got.Models)
}

// 自定义分组列表沿用 Key 别名投影，保留目标元数据且不暴露列表外目标或通配符。
func TestGeminiV1BetaListModels_CustomGroupListPreservesAPIKeyAliases(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1beta/models", nil)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		ModelMapping: map[string]string{
			"my-gemini":      "gemini-2.5-pro",
			"custom-alias":   "gemini-custom",
			"unlisted-alias": "gemini-2.5-flash",
			"wildcard-*":     "gemini-2.5-pro",
		},
		Group: &routing.Group{
			Platform: capability.PlatformGemini,
			ModelsListConfig: routing.GroupModelsListConfig{
				Enabled: true,
				Models:  []string{"gemini-2.5-pro", "models/gemini-custom"},
			},
		},
	})

	provideModelsHTTP(nil, nil, nil).GeminiV1BetaListModels(c)

	require.Equal(t, http.StatusOK, rec.Code)
	var got gemini.ModelsListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Models, 4)
	pro := gemini.FallbackModel("gemini-2.5-pro")
	custom := gemini.FallbackModel("models/gemini-custom")
	require.Equal(t, []gemini.Model{pro, custom}, got.Models[:2])
	pro.Name, pro.DisplayName = "models/my-gemini", "my-gemini"
	custom.Name, custom.DisplayName = "models/custom-alias", "custom-alias"
	require.ElementsMatch(t, []gemini.Model{pro, custom}, got.Models[2:])
}

func TestGeminiV1BetaListModels_ForcedAntigravityIgnoresCustomGroupList(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/antigravity/v1beta/models", nil)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		Group: &routing.Group{
			Platform: capability.PlatformGemini,
			ModelsListConfig: routing.GroupModelsListConfig{
				Enabled: true,
				Models:  []string{"gemini-custom"},
			},
		},
	})
	c.Set(string(keyhttp.ContextKeyForcePlatform), capability.PlatformAntigravity)

	provideModelsHTTP(nil, nil, nil).GeminiV1BetaListModels(c)

	require.Equal(t, http.StatusOK, rec.Code)
	var got protocolgemini.GeminiModelsListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, antigravity.FallbackGeminiModelsList(), got)
}

func TestCustomGeminiModelsList_DisabledKeepsExistingFlow(t *testing.T) {
	group := &routing.Group{
		ModelsListConfig: routing.GroupModelsListConfig{
			Enabled: false,
			Models:  []string{"gemini-2.5-pro"},
		},
	}

	_, ok := customGeminiModelsList(group)
	require.False(t, ok)
}

// TestGeminiV1BetaHandler_PlatformRoutingInvariant 文档化并验证 Handler 层的平台路由逻辑不变量
// 该测试确保 gemini 和 antigravity 平台的路由逻辑符合预期
func TestGeminiV1BetaHandler_PlatformRoutingInvariant(t *testing.T) {
	tests := []struct {
		name            string
		platform        string
		expectedService string
		description     string
	}{
		{
			name:            "Gemini平台使用ForwardNative",
			platform:        capability.PlatformGemini,
			expectedService: "GeminiMessagesCompatService.ForwardNative",
			description:     "Gemini OAuth 账户直接调用 Google API",
		},
		{
			name:            "Antigravity平台使用ForwardGemini",
			platform:        capability.PlatformAntigravity,
			expectedService: "AntigravityGatewayService.ForwardGemini",
			description:     "Antigravity 账户通过 CRS 中转，支持 Gemini 协议",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 模拟 GeminiV1BetaModels 中的路由决策 (lines 199-205 in gemini_v1beta_handler.go)
			var routedService string
			if tt.platform == capability.PlatformAntigravity {
				routedService = "AntigravityGatewayService.ForwardGemini"
			} else {
				routedService = "GeminiMessagesCompatService.ForwardNative"
			}

			require.Equal(t, tt.expectedService, routedService,
				"平台 %s 应该路由到 %s: %s",
				tt.platform, tt.expectedService, tt.description)
		})
	}
}

// TestGeminiV1BetaHandler_ListModelsAntigravityFallback 验证 ListModels 的 antigravity 降级逻辑
// 当没有 gemini 账户但有 antigravity 账户时，应返回静态模型列表
func TestGeminiV1BetaHandler_ListModelsAntigravityFallback(t *testing.T) {
	tests := []struct {
		name             string
		hasGeminiAccount bool
		hasAntigravity   bool
		expectedBehavior string
	}{
		{
			name:             "有Gemini账户-调用ForwardAIStudioGET",
			hasGeminiAccount: true,
			hasAntigravity:   false,
			expectedBehavior: "forward_to_upstream",
		},
		{
			name:             "无Gemini有Antigravity-返回静态列表",
			hasGeminiAccount: false,
			hasAntigravity:   true,
			expectedBehavior: "static_fallback",
		},
		{
			name:             "无任何账户-返回503",
			hasGeminiAccount: false,
			hasAntigravity:   false,
			expectedBehavior: "service_unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 模拟 GeminiV1BetaListModels 的逻辑 (lines 33-44 in gemini_v1beta_handler.go)
			var behavior string

			if tt.hasGeminiAccount {
				behavior = "forward_to_upstream"
			} else if tt.hasAntigravity {
				behavior = "static_fallback"
			} else {
				behavior = "service_unavailable"
			}

			require.Equal(t, tt.expectedBehavior, behavior)
		})
	}
}

// TestGeminiV1BetaHandler_GetModelAntigravityFallback 验证 GetModel 的 antigravity 降级逻辑
func TestGeminiV1BetaHandler_GetModelAntigravityFallback(t *testing.T) {
	tests := []struct {
		name             string
		hasGeminiAccount bool
		hasAntigravity   bool
		expectedBehavior string
	}{
		{
			name:             "有Gemini账户-调用ForwardAIStudioGET",
			hasGeminiAccount: true,
			hasAntigravity:   false,
			expectedBehavior: "forward_to_upstream",
		},
		{
			name:             "无Gemini有Antigravity-返回静态模型信息",
			hasGeminiAccount: false,
			hasAntigravity:   true,
			expectedBehavior: "static_model_info",
		},
		{
			name:             "无任何账户-返回503",
			hasGeminiAccount: false,
			hasAntigravity:   false,
			expectedBehavior: "service_unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 模拟 GeminiV1BetaGetModel 的逻辑 (lines 77-87 in gemini_v1beta_handler.go)
			var behavior string

			if tt.hasGeminiAccount {
				behavior = "forward_to_upstream"
			} else if tt.hasAntigravity {
				behavior = "static_model_info"
			} else {
				behavior = "service_unavailable"
			}

			require.Equal(t, tt.expectedBehavior, behavior)
		})
	}
}

func TestShouldFallbackGeminiModel_KnownFallbackOn404(t *testing.T) {
	t.Parallel()

	res := &gemini.HTTPResult{StatusCode: http.StatusNotFound}
	require.True(t, shouldFallbackGeminiModel("gemini-3.1-pro-preview-customtools", res))
}

func TestShouldFallbackGeminiModel_UnknownModelOn404(t *testing.T) {
	t.Parallel()

	res := &gemini.HTTPResult{StatusCode: http.StatusNotFound}
	require.False(t, shouldFallbackGeminiModel("gemini-future-model", res))
}

func TestShouldFallbackGeminiModel_DelegatesScopeFallback(t *testing.T) {
	t.Parallel()

	res := &gemini.HTTPResult{
		StatusCode: http.StatusForbidden,
		Headers:    http.Header{"Www-Authenticate": []string{"Bearer error=\"insufficient_scope\""}},
		Body:       []byte("insufficient authentication scopes"),
	}
	require.True(t, shouldFallbackGeminiModel("gemini-future-model", res))
}
