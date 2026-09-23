package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	testkit "github.com/TokenFlux/TokenRouter/internal/apikey/testkit"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/service"
	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAPIKeyAuthForwardsUserScopedOpenAIFastPolicyToUpstream(t *testing.T) {

	upstreamBodies := make(chan []byte, 2)
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read request body", http.StatusInternalServerError)
			return
		}
		upstreamBodies <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_test","object":"response","model":"gpt-5","status":"completed","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`))
	}))
	defer upstreamServer.Close()

	settings := &tierpolicy.OpenAIFastPolicySettings{
		Rules: []tierpolicy.OpenAIFastPolicyRule{
			{
				ServiceTier: tierpolicy.OpenAIFastTierPriority,
				Action:      anthropic.BetaPolicyActionFilter,
				Scope:       anthropic.BetaPolicyScopeAll,
			},
			{
				ServiceTier: tierpolicy.OpenAIFastTierPriority,
				Action:      anthropic.BetaPolicyActionPass,
				Scope:       anthropic.BetaPolicyScopeAll,
				UserIDs:     []int64{42},
			},
		},
	}
	settingsJSON, err := json.Marshal(settings)
	require.NoError(t, err)

	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true

	settingService := gatewaytestkit.RuntimeReaders(&openAIFastPolicyForwardingSettingRepo{
		value: string(settingsJSON),
	})

	// 转发与原生选择使用同一响应归属、阻断和传输状态；本场景配置保持 simple 与 WS 关闭。
	responses := session.NewOpenAIWSStateStore(nil, gatewayprovider.LogOpenAIWSModeInfo)
	transient := accountcore.NewModelTransientState(0)
	circuit := egress.NewProxyStreamCircuit(egress.DefaultProxyStreamCircuitSettings())
	blocks := accountcore.NewRuntimeBlockState(time.Now)
	choices := selection.NewCompatible(selection.CompatibleDependencies{Responses: responses, ModelTransient: transient, ProxyCircuit: circuit, RuntimeBlocks: blocks}, selection.Options{Simple: true, WS: &egress.OpenAIWSOptions{}})
	gatewayService := service.NewOpenAIGatewayService(
		nil, nil, nil, cfg,
		nil, nil, &openAIFastPolicyForwardingHTTPUpstream{client: upstreamServer.Client()},
		nil, nil, nil, nil, nil, nil, settingService, nil, responseHeaderFilterForTest(cfg), responses, transient, circuit, choices,
	)
	gatewayService.BindRuntimeBlockState(blocks)

	groupID := int64(101)
	group := &routing.Group{
		ID:       groupID,
		Name:     "openai",
		Status:   billing.StatusActive,
		Platform: capability.PlatformOpenAI,
		Hydrated: true,
	}
	apiKeys := map[string]*apikey.APIKey{
		"key-user-42": newOpenAIFastPolicyForwardingAPIKey(1, "key-user-42", 42, groupID, group),
		"key-user-43": newOpenAIFastPolicyForwardingAPIKey(2, "key-user-43", 43, groupID, group),
	}
	apiKeyService := testkit.NewService(&openAIFastPolicyForwardingAPIKeyRepo{apiKeys: apiKeys}, nil, nil, nil, nil, nil, cfg)
	apiKeyService.Start()
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 900,
		Name:        "openai-upstream",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": upstreamServer.URL,
		},
		Extra: map[string]any{"use_responses_api": true}},
	}

	router := gin.New()
	router.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(apiKeyService, nil, cfg)))
	router.POST("/v1/responses", func(c *gin.Context) {
		body, readErr := io.ReadAll(c.Request.Body)
		if readErr != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		gatewayhttp.SetOpenAIClientTransport(c, gatewayhttp.OpenAIClientTransportHTTP)
		if _, forwardErr := gatewayService.Forward(c.Request.Context(), c, account, body); forwardErr != nil {
			c.Status(http.StatusBadGateway)
			return
		}
		c.Status(http.StatusOK)
	})

	send := func(apiKey string) {
		request := httptest.NewRequest(
			http.MethodPost,
			"/v1/responses",
			bytes.NewBufferString(`{"model":"gpt-5","stream":false,"service_tier":"priority","input":"hi"}`),
		)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("x-api-key", apiKey)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusOK, response.Code)
	}

	send("key-user-42")
	send("key-user-43")

	allowedUserBody := <-upstreamBodies
	otherUserBody := <-upstreamBodies
	require.Equal(t, tierpolicy.OpenAIFastTierPriority, gjson.GetBytes(allowedUserBody, "service_tier").String())
	require.False(t, gjson.GetBytes(otherUserBody, "service_tier").Exists())
}

func newOpenAIFastPolicyForwardingAPIKey(id int64, key string, userID, groupID int64, group *routing.Group) *apikey.APIKey {
	return &apikey.APIKey{
		ID:      id,
		UserID:  userID,
		Key:     key,
		Status:  billing.StatusActive,
		GroupID: &groupID,
		User: &identity.User{
			ID:          userID,
			Role:        identity.RoleUser,
			Status:      billing.StatusActive,
			Balance:     10,
			Concurrency: 1,
		},
		Group: group,
	}
}

type openAIFastPolicyForwardingAPIKeyRepo struct {
	apikey.APIKeyRepository
	apiKeys map[string]*apikey.APIKey
}

func (r *openAIFastPolicyForwardingAPIKeyRepo) GetByKeyForAuth(_ context.Context, key string) (*apikey.APIKey, error) {
	apiKey, ok := r.apiKeys[key]
	if !ok {
		return nil, apikey.ErrAPIKeyNotFound
	}
	clone := *apiKey
	return &clone, nil
}

func (r *openAIFastPolicyForwardingAPIKeyRepo) UpdateLastUsed(context.Context, int64, time.Time) error {
	return nil
}

type openAIFastPolicyForwardingSettingRepo struct {
	settingscore.Repository
	value string
}

func (r *openAIFastPolicyForwardingSettingRepo) GetValue(context.Context, string) (string, error) {
	return r.value, nil
}

type openAIFastPolicyForwardingHTTPUpstream struct {
	client *http.Client
}

func (u *openAIFastPolicyForwardingHTTPUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	// #nosec G704 -- 测试请求仅发送到本地 httptest.Server，不接受外部目标地址。
	return u.client.Do(req)
}

func (u *openAIFastPolicyForwardingHTTPUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}
