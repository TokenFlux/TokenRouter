package httpapi

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/egress"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	upstreamcore "github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const compactionTestV2SSESuccessBody = "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"compaction\",\"id\":\"cmp_probe\",\"encrypted_content\":\"blob\"}}\n\n" +
	"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_probe\",\"output\":[]}}\n\n"

func TestAccountTestService_TestAccountConnection_OpenAICompactOAuthUsesNativeV2AndDoesNotPersistSupport(t *testing.T) {

	updateCalls := make(chan map[string]any, 1)
	account := accountcore.Record{
		ID:          1,
		Name:        "openai-oauth",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-acc",
		},
	}
	repo := &openAIProbeStore{openAIProbeRecords: openAIProbeRecords{accountsByID: map[int64]*accountcore.Record{account.ID: &account}}, updateExtraCalls: updateCalls}
	upstream := &openAIProbeTransport{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid-probe"}},
		Body:       io.NopCloser(strings.NewReader(compactionTestV2SSESuccessBody)),
	}}
	svc := &accountprovider.OpenAIAccountTest{
		Store:     repo,
		Transport: upstream,
	}

	rec := httptest.NewRecorder()
	c := &openAIProbeOutput{recorder: rec}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/1/test", bytes.NewReader(nil))

	err := executeOpenAIProbeRequest(t, svc, c, account.ID, "gpt-5.4", "", accountcore.AccountTestModeCompact)
	require.NoError(t, err)

	require.Equal(t, "https://chatgpt.com/backend-api/codex/responses", upstream.lastReq.URL.String())
	require.Equal(t, "chatgpt.com", upstream.lastReq.Host)
	require.Equal(t, "text/event-stream", upstream.lastReq.Header.Get("Accept"))
	require.Equal(t, openai.CodexCLIVersion, upstream.lastReq.Header.Get("Version"))
	require.NotEmpty(t, upstream.lastReq.Header.Get("Session_Id"))
	require.Contains(t, upstream.lastReq.Header.Get("x-codex-beta-features"), "remote_compaction_v2")
	require.Equal(t, upstreamcore.HTTPUpstreamProfileOpenAI, upstreamcore.HTTPUpstreamProfileFromContext(upstream.lastReq.Context()))
	require.Equal(t, openai.CodexCLIUserAgent, upstream.lastReq.Header.Get("User-Agent"))
	require.Equal(t, openai.CodexDefaultOriginator, upstream.lastReq.Header.Get("Originator"))
	require.Equal(t, "chatgpt-acc", upstream.lastReq.Header.Get("chatgpt-account-id"))
	require.Equal(t, "gpt-5.4", gjson.GetBytes(upstream.lastBody, "model").String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
	input := gjson.GetBytes(upstream.lastBody, "input").Array()
	require.NotEmpty(t, input)
	require.Equal(t, "compaction_trigger", input[len(input)-1].Get("type").String())

	require.Empty(t, updateCalls, "手动压缩测试不应写入能力状态")
	require.Contains(t, rec.Body.String(), `"type":"test_complete"`)
}

func TestAccountTestService_TestAccountConnection_OpenAICompactOAuth404DoesNotChangeCapability(t *testing.T) {

	updateCalls := make(chan map[string]any, 1)
	account := accountcore.Record{
		ID:          2,
		Name:        "openai-oauth",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Extra: map[string]any{
			"openai_compact_mode": "force_on",
		},
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-acc",
		},
	}
	repo := &openAIProbeStore{openAIProbeRecords: openAIProbeRecords{accountsByID: map[int64]*accountcore.Record{account.ID: &account}}, updateExtraCalls: updateCalls}
	upstream := &openAIProbeTransport{resp: &http.Response{
		StatusCode: http.StatusNotFound,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`404 page not found`)),
	}}
	svc := &accountprovider.OpenAIAccountTest{
		Store:     repo,
		Transport: upstream,
	}

	rec := httptest.NewRecorder()
	c := &openAIProbeOutput{recorder: rec}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/2/test", bytes.NewReader(nil))

	err := executeOpenAIProbeRequest(t, svc, c, account.ID, "gpt-5.4", "", accountcore.AccountTestModeCompact)
	require.Error(t, err)

	require.Empty(t, updateCalls, "手动压缩测试不应写入能力状态")
	require.Contains(t, rec.Body.String(), `"type":"error"`)
}

func TestAccountTestService_TestAccountConnection_OpenAICompactAPIKeyUsesNativeResponsesPath(t *testing.T) {

	updateCalls := make(chan map[string]any, 1)
	account := accountcore.Record{
		ID:          3,
		Name:        "openai-apikey",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":               "sk-test",
			"base_url":              "https://example.com/v1",
			"compact_model_mapping": map[string]any{"gpt-5.4": "gpt-5.4-openai-compact"},
		},
	}
	repo := &openAIProbeStore{openAIProbeRecords: openAIProbeRecords{accountsByID: map[int64]*accountcore.Record{account.ID: &account}}, updateExtraCalls: updateCalls}
	upstream := &openAIProbeTransport{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(compactionTestV2SSESuccessBody)),
	}}
	svc := &accountprovider.OpenAIAccountTest{
		Store:       repo,
		Transport:   upstream,
		ValidateURL: (egress.OperatorURLPolicy{}).Validate,
	}

	rec := httptest.NewRecorder()
	c := &openAIProbeOutput{recorder: rec}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/3/test", bytes.NewReader(nil))

	err := executeOpenAIProbeRequest(t, svc, c, account.ID, "gpt-5.4", "", accountcore.AccountTestModeCompact)
	require.NoError(t, err)

	require.Equal(t, "https://example.com/v1/responses", upstream.lastReq.URL.String())
	require.Equal(t, "gpt-5.4", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Contains(t, upstream.lastReq.Header.Get("x-codex-beta-features"), "remote_compaction_v2")
	require.Empty(t, updateCalls, "手动压缩测试不应写入能力状态")

}

func TestAccountTestService_TestAccountConnection_OpenAICompactAPIKeyDefaultBaseURLUsesResponsesPath(t *testing.T) {

	updateCalls := make(chan map[string]any, 1)
	account := accountcore.Record{
		ID:          4,
		Name:        "openai-apikey-default",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "sk-test",
		},
	}
	repo := &openAIProbeStore{openAIProbeRecords: openAIProbeRecords{accountsByID: map[int64]*accountcore.Record{account.ID: &account}}, updateExtraCalls: updateCalls}
	upstream := &openAIProbeTransport{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(compactionTestV2SSESuccessBody)),
	}}
	svc := &accountprovider.OpenAIAccountTest{
		Store:       repo,
		Transport:   upstream,
		ValidateURL: (egress.OperatorURLPolicy{}).Validate,
	}

	rec := httptest.NewRecorder()
	c := &openAIProbeOutput{recorder: rec}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/4/test", bytes.NewReader(nil))

	err := executeOpenAIProbeRequest(t, svc, c, account.ID, "gpt-5.4", "", accountcore.AccountTestModeCompact)
	require.NoError(t, err)
	require.Equal(t, "https://api.openai.com/v1/responses", upstream.lastReq.URL.String())
	require.Empty(t, updateCalls)
}

func TestAccountTestService_TestAccountConnection_OpenAILegacyCompactUsesDedicatedPathWithoutState(t *testing.T) {

	updateCalls := make(chan map[string]any, 1)
	account := accountcore.Record{
		ID:          5,
		Name:        "openai-legacy-compact",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":               "sk-test",
			"base_url":              "https://example.com/v1",
			"compact_model_mapping": map[string]any{"gpt-5.4": "gpt-5.4-openai-compact"},
		},
	}
	repo := &openAIProbeStore{openAIProbeRecords: openAIProbeRecords{accountsByID: map[int64]*accountcore.Record{account.ID: &account}}, updateExtraCalls: updateCalls}
	upstream := &openAIProbeTransport{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"legacy_compact_probe","status":"completed"}`)),
	}}
	svc := &accountprovider.OpenAIAccountTest{
		Store:       repo,
		Transport:   upstream,
		ValidateURL: (egress.OperatorURLPolicy{}).Validate,
	}

	rec := httptest.NewRecorder()
	c := &openAIProbeOutput{recorder: rec}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/5/test", bytes.NewReader(nil))

	err := executeOpenAIProbeRequest(t, svc, c, account.ID, "gpt-5.4", "", accountcore.AccountTestModeLegacyCompact)
	require.NoError(t, err)
	require.Equal(t, "https://example.com/v1/responses/compact", upstream.lastReq.URL.String())
	require.Equal(t, "gpt-5.4-openai-compact", gjson.GetBytes(upstream.lastBody, "model").String())

	require.Empty(t, updateCalls, "手动压缩测试不应写入能力状态")

}
