package service

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// --- mock: 只记录临时不可调度写入，其余方法不应被调用 ---

type capacityShedAccountRepoStub struct {
	gatewayprovider.ExecutionAccountStore
	// 嵌入接口，未实现的方法会 panic（不应被调用）

	tempUnschedCalls int
}

func (r *capacityShedAccountRepoStub) SetTempUnschedulable(_ context.Context, _ int64, _ time.Time, _ string) error {
	r.tempUnschedCalls++
	return nil
}

func (r *capacityShedAccountRepoStub) GetByID(_ context.Context, id int64) (*gatewayprovider.ExecutionAccount, error) {
	return &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: id, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}, nil
}

// 上游容量降载是请求级信号：故障因素（客户端身份、模型容量）与账号无关，
// 同账号重试用尽后不得把账号临时摘掉——否则一个被降载的请求会顺着 failover
// 把整池账号逐个封禁，而每个账号都会以同一个错误失败。

// 非池模式账号同样要先在同账号重试：换号不改变降载因素。
func TestStreamFailedEventCapacityShedRetriesOnSameAccount(t *testing.T) {
	nonPool := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}

	for _, code := range []string{"server_is_overloaded", "slow_down"} {
		payload := []byte(`{"type":"response.failed","response":{"error":{"code":"` + code + `"}}}`)
		require.True(t, openai.IsOpenAIUpstreamCapacityShedEvent(payload), code)
		require.True(t, openAIStreamFailedEventRetryableOnSameAccount(
			accountcore.UpstreamErrorDecision{}, nonPool, http.StatusBadGateway, payload, "overloaded",
		), code)
	}

	// 非降载的 failed 事件在非池模式下仍不做同账号重试，避免放大改动面。
	other := []byte(`{"type":"response.failed","response":{"error":{"code":"server_error"}}}`)
	require.False(t, openai.IsOpenAIUpstreamCapacityShedEvent(other))
	require.False(t, openAIStreamFailedEventRetryableOnSameAccount(
		accountcore.UpstreamErrorDecision{}, nonPool, http.StatusBadGateway, other, "boom",
	))
}

func TestOpenAIHTTPCapacityShedIsRequestScopedForOAuthAccounts(t *testing.T) {
	payload := []byte(`{"error":{"type":"server_error","message":"Our servers are currently overloaded. Please try again later."}}`)
	failoverErr := newOpenAIUpstreamFailoverError(
		http.StatusBadRequest,
		http.Header{"X-Request-Id": []string{"rid-http-capacity"}},
		payload,
		"Our servers are currently overloaded. Please try again later.",
		false,
	)

	require.True(t, failoverErr.RetryableOnSameAccount)
	require.True(t, failoverErr.RequestScopedTransient)

	repo := &capacityShedAccountRepoStub{}
	accountcore.NewRetryCooldown(capacityRetryStore{repo}, accountcore.RetryCooldownOptions{}).Apply(context.Background(), accountcore.RetryCooldownInput{AccountID: 1, Status: failoverErr.StatusCode, Retryable: failoverErr.RetryableOnSameAccount, RequestScopedTransient: failoverErr.RequestScopedTransient})
	require.Zero(t, repo.tempUnschedCalls)

	healthObserver := newUpstreamHealthForTest(repo, &config.Config{}, nil, accountcore.HealthOptions{}, nil)

	gateway := withSchedulerParametersForTest(&OpenAIGatewayService{healthObserver: healthObserver})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
	require.False(t, gateway.handleOpenAIAccountUpstreamError(
		context.Background(),
		account,
		http.StatusBadRequest,
		nil,
		payload,
		"gpt-5",
	))
	require.Zero(t, repo.tempUnschedCalls)
}

// 上游降载的真实序列是「event: error → event: response.failed」。error 帧不算
// 客户端输出：若把它当首输出 flush，clientOutputStarted 被固化，随后的 failed
// 事件就进不了 pre-output failover 分支，只能把致命错误原样转发给客户端。
func TestOpenAIStreamErrorFrameDoesNotStartClientOutput(t *testing.T) {
	cases := []struct {
		data      string
		eventType string
		want      bool
	}{
		{`{"type":"error","error":{"code":"server_is_overloaded","message":"overloaded"}}`, "error", false},
		{`{"type":"error","error":{"code":"slow_down","message":"slow down"}}`, "error", false},
		{`{"type":"error","error":{"type":"rate_limit_error","code":"rate_limit_exceeded","message":"limited"}}`, "error", false},
		// 不可重试类错误帧维持原样转发（不进 failover），保留上游错误细节。
		{`{"type":"error","error":{"type":"invalid_request_error","code":"content_policy_violation","message":"blocked"}}`, "error", true},
		{`{"type":"response.failed","response":{"error":{"code":"server_is_overloaded"}}}`, "response.failed", false},
		{`{"type":"response.created","response":{"id":"resp_1"}}`, "response.created", false},
		{`{"type":"response.in_progress","response":{"id":"resp_1"}}`, "response.in_progress", false},
		{`{"type":"response.output_item.added","item":{"type":"reasoning","summary":[]}}`, "response.output_item.added", false},
		{`{"type":"response.output_item.added","item":{"type":"reasoning","encrypted_content":"ciphertext"}}`, "response.output_item.added", true},
		{`{"type":"response.reasoning_summary_part.added","part":{"type":"summary_text","text":""}}`, "response.reasoning_summary_part.added", false},
		{`{"type":"response.reasoning_summary_part.added","part":{"type":"summary_text","text":"thinking"}}`, "response.reasoning_summary_part.added", true},
		{`{"type":"response.content_part.added","part":{"type":"output_text","text":""}}`, "response.content_part.added", false},
		{`{"type":"response.output_text.delta","delta":"hi"}`, "response.output_text.delta", true},
		{`[DONE]`, "", true},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, openai.OpenAIStreamDataStartsClientOutput(tc.data, tc.eventType), "data=%s type=%s", tc.data, tc.eventType)
	}
}

func TestOpenAIStreamMetadataPreambleAndMessageOnlyOverloadFailOver(t *testing.T) {

	largeMetadata := strings.Repeat("x", 16*1024)
	stream := strings.Join([]string{
		"event: response.created",
		`data: {"type":"response.created","response":{"id":"resp_1","metadata":{"padding":"` + largeMetadata + `"}}}`,
		"",
		"event: response.output_item.added",
		`data: {"type":"response.output_item.added","item":{"type":"reasoning","summary":[]}}`,
		"",
		"event: response.reasoning_summary_part.added",
		`data: {"type":"response.reasoning_summary_part.added","part":{"type":"summary_text","text":""}}`,
		"",
		"event: error",
		`data: {"type":"error","error":{"type":"service_unavailable_error","message":"Our servers are currently overloaded. Please try again later."}}`,
		"",
	}, "\n")

	tests := []struct {
		name string
		run  func(*OpenAIGatewayService, *gin.Context, *http.Response, *gatewayprovider.ExecutionAccount) error
	}{
		{
			name: "native",
			run: func(svc *OpenAIGatewayService, c *gin.Context, resp *http.Response, account *gatewayprovider.ExecutionAccount) error {
				_, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, account, time.Now(), "model", "model")
				return err
			},
		},
		{
			name: "passthrough",
			run: func(svc *OpenAIGatewayService, c *gin.Context, resp *http.Response, account *gatewayprovider.ExecutionAccount) error {
				_, err := svc.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, account, time.Now(), "model", "model")
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}})
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(stream)),
				Header:     http.Header{"X-Request-Id": []string{"rid-message-only-overload"}},
			}
			account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Name: "acc"}}

			err := tt.run(svc, c, resp, account)
			require.Error(t, err)
			var failoverErr *forwardcore.UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.True(t, failoverErr.RetryableOnSameAccount)
			require.True(t, failoverErr.RequestScopedTransient)
			require.Equal(t, http.StatusServiceUnavailable, failoverErr.ClientStatusCode)
			require.Contains(t, failoverErr.ClientMessage, "servers are currently overloaded")
			require.False(t, c.Writer.Written())
			require.Empty(t, rec.Body.String())
		})
	}
}

// 回归用例（真实上游降载序列）：created → in_progress → error 帧 → response.failed。
// 期望仍然走 pre-output failover（同账号重试 + 请求级瞬时标记），且不向客户端写出任何字节。
func TestOpenAIStreamCapacityShedErrorFramePrecedingFailedStillFailsOver(t *testing.T) {

	cfg := &config.Config{
		Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			"event: response.created",
			`data: {"type":"response.created","response":{"id":"resp_1"},"sequence_number":0}`,
			"",
			"event: response.in_progress",
			`data: {"type":"response.in_progress","response":{"id":"resp_1"},"sequence_number":1}`,
			"",
			"event: error",
			`data: {"type":"error","error":{"type":"service_unavailable_error","code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later."},"sequence_number":2}`,
			"",
			"event: response.failed",
			`data: {"type":"response.failed","response":{"id":"resp_1","status":"failed","error":{"code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later."}},"sequence_number":3}`,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-shed-error-then-failed"}},
	}

	_, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Name: "acc"}}, time.Now(), "model", "model")
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.RetryableOnSameAccount)
	require.True(t, failoverErr.RequestScopedTransient)
	require.False(t, c.Writer.Written())
	require.Empty(t, rec.Body.String())
}

// 流中途（已有真实输出）降载时无法再 failover，此时必须把降载码改写为客户端
// 可重试的 server_error 再通过唯一 response.failed 终态转发——Codex 对
// server_is_overloaded/slow_down 判致命并终止会话，对其余错误码执行内置退避重试。
func TestOpenAIStreamCapacityShedAfterOutputRewritesCodeForClient(t *testing.T) {

	logSink, restore := captureStructuredLog(t)
	defer restore()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			"event: response.created",
			`data: {"type":"response.created","response":{"id":"resp_1"}}`,
			"",
			"event: response.output_text.delta",
			`data: {"type":"response.output_text.delta","delta":"partial"}`,
			"",
			"event: error",
			`data: {"type":"error","error":{"type":"service_unavailable_error","code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later."},"sequence_number":2}`,
			"",
			"event: response.failed",
			`data: {"type":"response.failed","response":{"id":"resp_1","status":"failed","error":{"code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later."}},"sequence_number":3}`,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-shed-after-output"}},
	}

	_, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Name: "acc"}}, time.Now(), "model", "model")
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))

	body := rec.Body.String()
	require.Contains(t, body, "partial")
	require.NotContains(t, body, "event: error")
	require.Equal(t, 1, strings.Count(body, "event: response.failed"))
	require.Equal(t, 1, strings.Count(body, `"code":"server_error"`))
	require.Contains(t, body, `"code":"server_error"`)
	require.NotContains(t, body, "server_is_overloaded")
	require.Contains(t, body, "Our servers are currently overloaded")
	require.True(t, logSink.ContainsMessage("gateway.failover_suppressed_after_semantic_output"))
	require.True(t, logSink.ContainsFieldValue("path", "native_sse"))
	require.True(t, logSink.ContainsFieldValue("upstream_request_id", "rid-shed-after-output"))
}

// helper 单测：只有降载码被改写，其余错误码（尤其 rate_limit_exceeded，客户端
// 依赖其原码解析重试延时）必须原样保留。
func TestSanitizeOpenAICapacityShedErrorCodeForClient(t *testing.T) {
	cases := []struct {
		name        string
		payload     string
		wantChanged bool
		wantContain string
	}{
		{
			name:        "failed事件嵌套code改写",
			payload:     `{"type":"response.failed","response":{"error":{"code":"server_is_overloaded","message":"overloaded"}}}`,
			wantChanged: true,
			wantContain: `"code":"server_error"`,
		},
		{
			name:        "error帧裸code改写",
			payload:     `{"type":"error","error":{"code":"slow_down","message":"slow down"}}`,
			wantChanged: true,
			wantContain: `"code":"server_error"`,
		},
		{
			name:        "failed事件只有过载文案时补充code",
			payload:     `{"type":"response.failed","response":{"error":{"message":"Our servers are currently overloaded. Please try again later."}}}`,
			wantChanged: true,
			wantContain: `"code":"server_error"`,
		},
		{
			name:        "error帧只有过载文案时补充code",
			payload:     `{"type":"error","error":{"message":"Server is overloaded. Please try again later."}}`,
			wantChanged: true,
			wantContain: `"code":"server_error"`,
		},
		{
			name:        "rate_limit不改写",
			payload:     `{"type":"response.failed","response":{"error":{"code":"rate_limit_exceeded","message":"try again in 3s"}}}`,
			wantChanged: false,
			wantContain: `"code":"rate_limit_exceeded"`,
		},
		{
			name:        "普通server_error不改写",
			payload:     `{"type":"response.failed","response":{"error":{"code":"server_error","message":"boom"}}}`,
			wantChanged: false,
			wantContain: `"code":"server_error"`,
		},
		{
			name:        "非JSON不改写",
			payload:     `not-json`,
			wantChanged: false,
			wantContain: `not-json`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, changed := openai.SanitizeOpenAICapacityShedErrorCodeForClient([]byte(tc.payload))
			require.Equal(t, tc.wantChanged, changed)
			require.Contains(t, string(out), tc.wantContain)
			if changed {
				require.NotContains(t, string(out), "server_is_overloaded")
				require.NotContains(t, string(out), "slow_down")
			}
		})
	}
}

// 只转换读取投影，后续冷却调用同一原生存储替身。
type capacityRetryStore struct{ *capacityShedAccountRepoStub }

func (s capacityRetryStore) GetByID(ctx context.Context, id int64) (*accountcore.Record, error) {
	value, err := s.capacityShedAccountRepoStub.GetByID(ctx, id)
	return gatewayprovider.ExecutionRecord(value), err
}
