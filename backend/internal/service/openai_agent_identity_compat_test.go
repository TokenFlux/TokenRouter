package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	upstreamcore "github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIAgentIdentityPassthroughKeepsSessionAndPromptCacheHeaders(t *testing.T) {

	key, privateKey := newTestAgentIdentityKey(t)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 24,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"auth_mode":          accountcore.OpenAIAuthModeAgentIdentity,
			"agent_runtime_id":   key.RuntimeID,
			"agent_private_key":  privateKey,
			"task_id":            key.TaskID,
			"chatgpt_account_id": "account-agent-passthrough",
		}},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.4","instructions":"Reply OK","input":[],"stream":true,"prompt_cache_key":"cache-agent"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("session_id", "client-session")
	c.Request.Header.Set("conversation_id", "client-conversation")
	c.Request.Header.Set("Authorization", "Bearer inbound-must-not-forward")

	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{}))
	req, err := svc.Requests.BuildPassthrough(context.Background(), c, account, body, "")
	require.NoError(t, err)
	require.Equal(t, "AgentAssertion", strings.SplitN(req.Header.Get("Authorization"), " ", 2)[0])
	require.Equal(t, "account-agent-passthrough", req.Header.Get("chatgpt-account-id"))
	require.NotEqual(t, "client-session", req.Header.Get("session_id"))
	require.NotEqual(t, "client-conversation", req.Header.Get("conversation_id"))
	require.Equal(t, openai.IsolateOpenAIUpstreamSessionID(0, accountprovider.CodexIdentityNamespace(account.View()), "client-session"), req.Header.Get("session_id"))
	require.Equal(t, openai.IsolateOpenAIUpstreamSessionID(0, accountprovider.CodexIdentityNamespace(account.View()), "client-conversation"), req.Header.Get("conversation_id"))
	requestBody, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.Contains(t, string(requestBody), `"prompt_cache_key":"cache-agent"`)

	// 认证模式不能改变会话隔离或提示缓存语义，因此与相同 OAuth 请求对照而非固定实现哈希。
	oauthAccount := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 26,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"chatgpt_account_id": "account-agent-passthrough",
		}},
	}
	oauthRecorder := httptest.NewRecorder()
	oauthContext, _ := gin.CreateTestContext(oauthRecorder)
	oauthContext.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	oauthContext.Request.Header.Set("session_id", "client-session")
	oauthContext.Request.Header.Set("conversation_id", "client-conversation")
	oauthReq, err := svc.Requests.BuildPassthrough(context.Background(), oauthContext, oauthAccount, body, "oauth-token")
	require.NoError(t, err)
	require.Equal(t, oauthReq.Header.Get("session_id"), req.Header.Get("session_id"))
	require.Equal(t, oauthReq.Header.Get("conversation_id"), req.Header.Get("conversation_id"))
}

func TestOpenAIAgentIdentityErrorRedactionDoesNotLeakCredentialValues(t *testing.T) {
	key, privateKey := newTestAgentIdentityKey(t)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 25,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"auth_mode":         accountcore.OpenAIAuthModeAgentIdentity,
			"agent_runtime_id":  key.RuntimeID,
			"agent_private_key": privateKey,
			"task_id":           key.TaskID,
			"access_token":      key.RuntimeID + "-oauth-value",
		}},
	}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{}))
	oauthValue := account.View().GetCredential("access_token")
	redacted := svc.agentIdentity.Redact(context.Background(), account, []byte(`{"message":"runtime-test task-test `+oauthValue+` AgentAssertion abc123"}`))
	require.NotContains(t, string(redacted), key.RuntimeID)
	require.NotContains(t, string(redacted), key.TaskID)
	require.NotContains(t, string(redacted), oauthValue)
	require.NotContains(t, string(redacted), "AgentAssertion abc123")
	require.Contains(t, string(redacted), "[redacted]")
}

func TestOpenAIAuthenticationHeadersPreserveOAuthPATAndAPIKeyBearerModes(t *testing.T) {
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{}))
	tests := []struct {
		name    string
		account *gatewayprovider.ExecutionAccount
		token   string
	}{
		{name: "oauth", account: &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}, token: "oauth-runtime-token"},
		{name: "personal access token", account: &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Credentials: map[string]any{"auth_mode": accountcore.OpenAIAuthModePersonalAccessToken}}}, token: "pat-runtime-token"},
		{name: "api key", account: &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}, token: "api-key-runtime-token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers, err := svc.agentIdentity.Headers(context.Background(), tt.account, tt.token)
			require.NoError(t, err)
			require.Equal(t, "Bearer "+tt.token, headers.Get("Authorization"))
		})
	}
}

func TestOpenAIWSAgentIdentityRecoveryRequiresTaskInvalidBody(t *testing.T) {
	require.False(t, openai.IsAgentTaskInvalidWSDialError(&openai.WSDialError{
		StatusCode:   http.StatusUnauthorized,
		ResponseBody: []byte(`{"error":{"code":"invalid_signature"}}`),
	}))
	require.True(t, openai.IsAgentTaskInvalidWSDialError(&openai.WSDialError{
		StatusCode:   http.StatusUnauthorized,
		ResponseBody: []byte(`{"error":{"code":"invalid_task_id"}}`),
	}))
}

func TestValidateOpenAIWSBearerTokenAllowsAgentIdentityWithoutStoredToken(t *testing.T) {
	t.Run("Given Agent Identity When a WS path receives no bearer token Then dial-time assertion auth is allowed", func(t *testing.T) {
		account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
			Type: capability.AccountTypeOAuth,
			Credentials: map[string]any{
				"auth_mode": accountcore.OpenAIAuthModeAgentIdentity,
			}},
		}

		require.NoError(t, validateOpenAIWSBearerToken(account, ""))
	})

	t.Run("Given bearer credentials When a WS path receives no token Then the request is rejected", func(t *testing.T) {
		accounts := []*gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Credentials: map[string]any{"auth_mode": accountcore.OpenAIAuthModePersonalAccessToken}}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}},
		}

		for _, account := range accounts {
			require.EqualError(t, validateOpenAIWSBearerToken(account, ""), "token is empty")
		}
	})
}

func TestOpenAIAgentIdentityTaskInvalidRetriesExactlyOnce(t *testing.T) {

	key, privateKey := newTestAgentIdentityKey(t)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 23,
		Name:        "agent-identity",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"auth_mode":          accountcore.OpenAIAuthModeAgentIdentity,
			"agent_runtime_id":   key.RuntimeID,
			"agent_private_key":  privateKey,
			"task_id":            "task-old",
			"chatgpt_account_id": "account-agent-retry",
		}},
	}
	repo := &agentIdentityForwardRepo{account: account}
	registerCalls := 0
	registerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		registerCalls++
		_, _ = io.WriteString(w, `{"task_id":"task-new"}`)
	}))
	defer registerServer.Close()
	oldBase := openAIAgentIdentityAuthAPIBaseURL
	openAIAgentIdentityAuthAPIBaseURL = registerServer.URL
	t.Cleanup(func() { openAIAgentIdentityAuthAPIBaseURL = oldBase })

	successBody := `{"id":"resp-agent-retry","object":"response","model":"gpt-5.4","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{StatusCode: http.StatusUnauthorized, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"invalid_task_id"}}`))},
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(successBody))},
	}}
	require.True(t, openai.IsAgentTaskInvalidHTTPResponse(http.StatusUnauthorized, []byte(`{"error":{"code":"invalid_task_id"}}`)))
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{cfg: &config.Config{}, accountRepo: repo, httpUpstream: upstream}))
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.4","instructions":"Reply OK","input":[],"stream":false}`))

	_, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"gpt-5.4","instructions":"Reply OK","input":[],"stream":false}`))
	require.NoError(t, err)
	require.Equal(t, 1, registerCalls)
	require.Len(t, upstream.requests, 2)
	require.NotEqual(t, upstream.requests[0].Header.Get("Authorization"), upstream.requests[1].Header.Get("Authorization"))
	require.Equal(t, "task-new", decodeAgentAssertionTask(t, upstream.requests[1].Header.Get("Authorization")))

	// 连续两次 task 失效也只能重试一次，避免恢复路径无限循环。
	upstream.responses = []*http.Response{
		{StatusCode: http.StatusUnauthorized, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"invalid_task_id"}}`))},
		{StatusCode: http.StatusUnauthorized, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"invalid_task_id"}}`))},
	}
	rec2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(rec2)
	c2.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.4","instructions":"Reply OK","input":[],"stream":false}`))
	_, err = svc.Forward(context.Background(), c2, account, []byte(`{"model":"gpt-5.4","instructions":"Reply OK","input":[],"stream":false}`))
	require.Error(t, err)
	require.Equal(t, 2, registerCalls)
	require.Len(t, upstream.requests, 4)

	// 透传路径复用相同的单次 task 恢复契约。
	account.Record.Extra = map[string]any{"openai_passthrough": true}
	account.Record.Credentials["task_id"] = "task-old-passthrough"
	upstream.responses = []*http.Response{
		{StatusCode: http.StatusUnauthorized, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"invalid_task_id"}}`))},
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\ndata: [DONE]\n\n"))},
	}
	rec3 := httptest.NewRecorder()
	c3, _ := gin.CreateTestContext(rec3)
	c3.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.4","instructions":"Reply OK","input":[],"stream":false}`))
	_, err = svc.Forward(context.Background(), c3, account, []byte(`{"model":"gpt-5.4","instructions":"Reply OK","input":[],"stream":false}`))
	require.NoError(t, err)
	require.Equal(t, 3, registerCalls)
	require.Len(t, upstream.requests, 6)
}

func TestOpenAIAgentIdentityCompatRoutesRecoverInvalidTaskOnce(t *testing.T) {

	tests := []struct {
		name string
		path string
		body []byte
		call func(*OpenAIGatewayService, context.Context, *gin.Context, *gatewayprovider.ExecutionAccount, []byte) (*forwardcore.OpenAIResult, error)
	}{
		{
			name: "chat completions",
			path: "/v1/chat/completions",
			body: []byte(`{"model":"gpt-5.4","stream":false,"messages":[{"role":"user","content":"hi"}]}`),
			call: func(s *OpenAIGatewayService, ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, body []byte) (*forwardcore.OpenAIResult, error) {
				return s.Text.Chat(ctx, c, account, body, "", "gpt-5.4")
			},
		},
		{
			name: "anthropic messages",
			path: "/v1/messages",
			body: []byte(`{"model":"gpt-5.4","stream":false,"max_tokens":32,"messages":[{"role":"user","content":"hi"}]}`),
			call: func(s *OpenAIGatewayService, ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, body []byte) (*forwardcore.OpenAIResult, error) {
				return s.Text.Messages(ctx, c, account, body, "", "gpt-5.4")
			},
		},
	}

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, privateKey := newTestAgentIdentityKey(t)
			account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: int64(40 + index),
				Name:        "agent-identity-compat",
				Platform:    capability.PlatformOpenAI,
				Type:        capability.AccountTypeOAuth,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Credentials: map[string]any{
					"auth_mode":          accountcore.OpenAIAuthModeAgentIdentity,
					"agent_runtime_id":   key.RuntimeID,
					"agent_private_key":  privateKey,
					"task_id":            "task-compat-old",
					"chatgpt_account_id": "account-compat-recovery",
				}},
			}
			repo := &agentIdentityForwardRepo{account: account}
			registerCalls := 0
			registerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				registerCalls++
				_, _ = io.WriteString(w, `{"task_id":"task-compat-new"}`)
			}))
			defer registerServer.Close()
			oldBase := openAIAgentIdentityAuthAPIBaseURL
			openAIAgentIdentityAuthAPIBaseURL = registerServer.URL
			t.Cleanup(func() { openAIAgentIdentityAuthAPIBaseURL = oldBase })

			upstream := &httpUpstreamRecorder{responses: []*http.Response{
				{StatusCode: http.StatusUnauthorized, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"invalid_task_id"}}`))},
				{StatusCode: http.StatusUnauthorized, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"invalid_task_id"}}`))},
			}}
			svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{cfg: &config.Config{}, accountRepo: repo, httpUpstream: upstream}))
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, tt.path, bytes.NewReader(tt.body))

			_, err := tt.call(svc, context.Background(), c, account, tt.body)
			require.Error(t, err)
			require.Equal(t, 1, registerCalls)
			require.Len(t, upstream.requests, 2)
			require.Equal(t, "task-compat-new", account.View().GetCredential("task_id"))
		})
	}
}

func TestOpenAIAgentIdentityChatRecoveryKeepsAutoDerivedSessionIsolationStable(t *testing.T) {

	key, privateKey := newTestAgentIdentityKey(t)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 52, Name: "agent-identity", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth,
		Status: billing.StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{
			"auth_mode":         accountcore.OpenAIAuthModeAgentIdentity,
			"agent_runtime_id":  key.RuntimeID,
			"agent_private_key": privateKey,
			"task_id":           "task-cache-old",
		},
		Extra: map[string]any{}},
	}
	repo := &agentIdentityForwardRepo{account: account}
	registerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"task_id":"task-cache-new"}`)
	}))
	defer registerServer.Close()
	oldBase := openAIAgentIdentityAuthAPIBaseURL
	openAIAgentIdentityAuthAPIBaseURL = registerServer.URL
	t.Cleanup(func() { openAIAgentIdentityAuthAPIBaseURL = oldBase })

	invalidTask := func() *http.Response {
		return &http.Response{StatusCode: http.StatusUnauthorized, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"invalid_task_id"}}`))}
	}
	upstream := &httpUpstreamRecorder{responses: []*http.Response{invalidTask(), invalidTask()}}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{cfg: &config.Config{}, accountRepo: repo, httpUpstream: upstream}))
	body := []byte(`{"model":"gpt-5.4","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Set("api_key", &apikey.APIKey{ID: 99})

	_, err := svc.Text.Chat(context.Background(), c, account, body, "", "gpt-5.4")
	require.Error(t, err)
	require.Len(t, upstream.requests, 2)
	firstKey := gjson.GetBytes(upstream.bodies[0], "prompt_cache_key").String()
	secondKey := gjson.GetBytes(upstream.bodies[1], "prompt_cache_key").String()
	require.NotEmpty(t, firstKey)
	require.Equal(t, firstKey, secondKey)
	require.Equal(t, upstreamcore.GenerateSessionUUID(openai.IsolateOpenAIUpstreamSessionID(99, accountprovider.CodexIdentityNamespace(gatewayhttp.CodexIdentityRecord(c, account.View())), firstKey)), upstream.requests[0].Header.Get("session_id"))
	require.Equal(t, upstream.requests[0].Header.Get("session_id"), upstream.requests[1].Header.Get("session_id"))
}

func decodeAgentAssertionTask(t *testing.T, header string) string {
	t.Helper()
	encoded := strings.TrimPrefix(header, "AgentAssertion ")
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	require.NoError(t, err)
	var envelope struct {
		TaskID string `json:"task_id"`
	}
	require.NoError(t, json.Unmarshal(decoded, &envelope))
	return envelope.TaskID
}

type agentIdentityForwardRepo struct {
	gatewayprovider.ExecutionAccountStore

	account *gatewayprovider.ExecutionAccount
}

func (r *agentIdentityForwardRepo) GetByID(_ context.Context, _ int64) (*gatewayprovider.ExecutionAccount, error) {
	return r.account, nil
}

func (r *agentIdentityForwardRepo) UpdateCredentials(_ context.Context, _ int64, credentials map[string]any) error {
	r.account.Record.Credentials = credentials
	return nil
}
