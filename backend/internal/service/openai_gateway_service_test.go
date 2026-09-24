package service

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	upstreamcore "github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 编译期接口断言
var _ gatewayprovider.ExecutionAccountStore = (*stubOpenAIAccountRepo)(nil)
var _ session.GatewayCache = (*stubGatewayCache)(nil)

type stubOpenAIAccountRepo struct {
	gatewayprovider.ExecutionAccountStore

	accounts []gatewayprovider.

		// tempUnschedulableOpenAIAccountRepo 记录临时不可调度规则写入的模型范围。
		ExecutionAccount
}

type tempUnschedulableOpenAIAccountRepo struct {
	stubOpenAIAccountRepo
	modelRateLimitAccountID int64
	modelRateLimitKey       string
}

func (r *tempUnschedulableOpenAIAccountRepo) SetModelRateLimit(_ context.Context, accountID int64, modelKey string, _ time.Time, _ ...string) error {
	r.modelRateLimitAccountID = accountID
	r.modelRateLimitKey = modelKey
	return nil
}

type snapshotUpdateAccountRepo struct {
	stubOpenAIAccountRepo
	updateExtraCalls chan map[string]any
}

func (r *snapshotUpdateAccountRepo) UpdateExtra(ctx context.Context, id int64, updates map[string]any) error {
	if r.updateExtraCalls != nil {
		copied := make(map[string]any, len(updates))
		for k, v := range updates {
			copied[k] = v
		}
		r.updateExtraCalls <- copied
	}
	return nil
}

func (r stubOpenAIAccountRepo) GetByID(ctx context.Context, id int64) (*gatewayprovider.ExecutionAccount, error) {
	for i := range r.accounts {
		if r.accounts[i].Record.ID == id {
			return &r.accounts[i], nil
		}
	}
	return nil, errors.New("account not found")
}

func (r stubOpenAIAccountRepo) GetByIDs(ctx context.Context, ids []int64) ([]*gatewayprovider.ExecutionAccount, error) {
	if len(ids) == 0 {
		return []*gatewayprovider.ExecutionAccount{}, nil
	}
	index := make(map[int64]*gatewayprovider.ExecutionAccount, len(r.accounts))
	for i := range r.accounts {
		account := &r.accounts[i]
		index[account.Record.ID] = account
	}
	out := make([]*gatewayprovider.ExecutionAccount, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if account, ok := index[id]; ok {
			out = append(out, account)
		}
	}
	return out, nil
}

func (r stubOpenAIAccountRepo) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]gatewayprovider.ExecutionAccount, error) {
	var result []gatewayprovider.ExecutionAccount
	for _, acc := range r.accounts {
		if acc.Record.Platform == platform {
			result = append(result, acc)
		}
	}
	return result, nil
}

func (r stubOpenAIAccountRepo) ListSchedulableByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionAccount, error) {
	var result []gatewayprovider.ExecutionAccount
	for _, acc := range r.accounts {
		if acc.Record.Platform == platform {
			result = append(result, acc)
		}
	}
	return result, nil
}

func (r stubOpenAIAccountRepo) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionAccount, error) {
	return r.ListSchedulableByPlatform(ctx, platform)
}

func TestOpenAIGatewayService_ForwardAsAnthropic_CapacityShedReturnsRequestScopedFailoverWithoutCommit(t *testing.T) {

	body := []byte(`{"model":"gpt-5.4","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := []byte(`{"error":{"message":"Our servers are currently overloaded. Please try again later."}}`)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusBadRequest,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(upstreamBody)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(strings.Join([]string{
				`data: {"type":"response.output_text.delta","delta":"ok"}`,
				"",
				`data: {"type":"response.completed","response":{"id":"resp_second","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`,
				"",
			}, "\n"))),
		},
	}}
	repo := &tempUnschedulableOpenAIAccountRepo{}
	healthObserver := newUpstreamHealthForTest(repo, &config.Config{}, nil, accountcore.HealthOptions{}, nil)

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg: &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{
			Enabled: false, AllowInsecureHTTP: true,
		}}},
		httpUpstream:   upstream,
		healthObserver: healthObserver,
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 5099, Name: "temporary-unschedulable", Platform: capability.PlatformOpenAI,
		Type: capability.AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{
			"api_key":                    "sk-test",
			"base_url":                   "http://upstream.example",
			"model_provider":             "env-openai",
			"temp_unschedulable_enabled": true,
			"temp_unschedulable_rules": []any{map[string]any{
				"error_code":       float64(http.StatusBadRequest),
				"keywords":         []any{"our servers are currently overloaded", "please try again later"},
				"duration_minutes": float64(1),
			}},
		}},
	}

	_, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")

	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadRequest, failoverErr.StatusCode)
	require.True(t, failoverErr.ShouldRetryNextAccount())
	require.True(t, failoverErr.RetryableOnSameAccount)
	require.True(t, failoverErr.RequestScopedTransient)
	require.Zero(t, repo.modelRateLimitAccountID, "request-scoped capacity shedding must not change account health")
	require.Empty(t, repo.modelRateLimitKey)
	require.False(t, httpapi.IsResponseCommitted(c))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, rec.Body.String())

	secondRec := httptest.NewRecorder()
	secondContext, _ := gin.CreateTestContext(secondRec)
	secondContext.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	secondContext.Request.Header.Set("Content-Type", "application/json")
	secondAccount := *account
	secondAccount.Record.ID = 5100
	secondAccount.Record.Name = "healthy-failover-account"
	result, secondErr := svc.ForwardAsAnthropic(context.Background(), secondContext, &secondAccount, body, "", "")
	require.NoError(t, secondErr)
	require.NotNil(t, result)
	require.Equal(t, "resp_second", result.ResponseID)
	require.NotEmpty(t, secondRec.Body.String())
}

func TestFailoverOpenAIUpstreamHTTPError_NilContextSkipsTempUnschedulablePolicy(t *testing.T) {
	repo := &tempUnschedulableOpenAIAccountRepo{}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		healthObserver: newUpstreamHealthForTest(repo, &config.Config{}, nil, accountcore.HealthOptions{}, nil),
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 5099, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"temp_unschedulable_enabled": true,
			"temp_unschedulable_rules": []any{map[string]any{
				"error_code":       float64(http.StatusBadRequest),
				"keywords":         []any{"custom temporary outage"},
				"duration_minutes": float64(1),
			}},
		}},
	}
	body := []byte(`{"error":{"message":"Custom temporary outage."}}`)
	resp := &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{}}

	got := svc.failoverOpenAIUpstreamHTTPError(
		context.Background(), nil, account, resp, body,
		"Custom temporary outage.", "gpt-5.4",
	)

	require.Nil(t, got)
	require.Zero(t, repo.modelRateLimitAccountID)
	require.Empty(t, repo.modelRateLimitKey)
}

type cancelReadCloser struct{}

func (c cancelReadCloser) Read(p []byte) (int, error) { return 0, context.Canceled }
func (c cancelReadCloser) Close() error               { return nil }

type errReadCloser struct {
	err error
}

func (r errReadCloser) Read([]byte) (int, error) { return 0, r.err }
func (r errReadCloser) Close() error             { return nil }

type openAIStreamReadThenErrorCloser struct {
	reader *strings.Reader
	err    error
}

func (r *openAIStreamReadThenErrorCloser) Read(p []byte) (int, error) {
	if r.reader != nil && r.reader.Len() > 0 {
		return r.reader.Read(p)
	}
	return 0, r.err
}

func (r *openAIStreamReadThenErrorCloser) Close() error { return nil }

type failingGinWriter struct {
	gin.ResponseWriter
	failAfter int
	writes    int
}

func (w *failingGinWriter) Write(p []byte) (int, error) {
	if w.writes >= w.failAfter {
		return 0, errors.New("write failed")
	}
	w.writes++
	return w.ResponseWriter.Write(p)
}

func TestExtractOpenAIResponseIDFromJSONBytes(t *testing.T) {
	require.Equal(t, "resp_json", protocolopenai.ExtractOpenAIResponseIDFromJSONBytes([]byte(`{"id":"resp_json"}`)))
	require.Equal(t, "resp_sse", protocolopenai.ExtractOpenAIResponseIDFromJSONBytes([]byte(`{"type":"response.completed","response":{"id":"resp_sse"}}`)))
	require.Empty(t, protocolopenai.ExtractOpenAIResponseIDFromJSONBytes([]byte(`{"response":{}}`)))
	require.Empty(t, protocolopenai.ExtractOpenAIResponseIDFromJSONBytes([]byte(`not-json`)))
}

// 复现 #4386：gpt-image-2 /v1/images/edits 的 usage 携带 input_tokens_details.image_tokens，
// 提取器须将图片输入 token 单独填入 ImageInputTokens（此前被丢弃并入 InputTokens 按文本价计费）。
func TestExtractOpenAIUsage_CapturesImageInputTokens(t *testing.T) {
	body := []byte(`{"usage":{"input_tokens":371,"input_tokens_details":{"image_tokens":352,"text_tokens":19},"output_tokens":439,"output_tokens_details":{"image_tokens":439,"text_tokens":0},"total_tokens":810}}`)
	usage, ok := protocolopenai.ExtractOpenAIUsageFromJSONBytes(body)
	require.True(t, ok)
	require.Equal(t, 371, usage.InputTokens)
	require.Equal(t, 352, usage.ImageInputTokens)
	require.Equal(t, 439, usage.OutputTokens)
	require.Equal(t, 439, usage.ImageOutputTokens)

	// prompt_tokens_details 回退路径（部分上游用 prompt_tokens 口径）。
	promptStyle := []byte(`{"usage":{"prompt_tokens":100,"prompt_tokens_details":{"image_tokens":80}}}`)
	pu, ok := protocolopenai.ExtractOpenAIUsageFromJSONBytes(promptStyle)
	require.True(t, ok)
	require.Equal(t, 100, pu.InputTokens)
	require.Equal(t, 80, pu.ImageInputTokens)

	// 纯文本请求：无 image_tokens 时 ImageInputTokens 为 0，行为不变。
	textOnly := []byte(`{"usage":{"input_tokens":50,"output_tokens":10}}`)
	tu, ok := protocolopenai.ExtractOpenAIUsageFromJSONBytes(textOnly)
	require.True(t, ok)
	require.Zero(t, tu.ImageInputTokens)
}

func TestExtractOpenAIUsage_ReadsClineDataEnvelope(t *testing.T) {
	body := []byte(`{"data":{"choices":[{"message":{"content":"OK"}}],"usage":{"prompt_tokens":8,"completion_tokens":27,"total_tokens":35,"prompt_tokens_details":{"cached_tokens":4}}},"success":true}`)

	usage, ok := protocolopenai.ExtractOpenAIUsageFromJSONBytes(body)

	require.True(t, ok)
	require.Equal(t, 8, usage.InputTokens)
	require.Equal(t, 27, usage.OutputTokens)
	require.Equal(t, 4, usage.CacheReadInputTokens)
}

func TestExtractOpenAIUsage_ReadsWrappedResponsesDataEnvelope(t *testing.T) {
	body := []byte(`{"data":{"response":{"usage":{"input_tokens":11,"output_tokens":5,"total_tokens":16,"input_tokens_details":{"cached_tokens":2}}}}}`)

	usage, ok := protocolopenai.ExtractOpenAIUsageFromJSONBytes(body)

	require.True(t, ok)
	require.Equal(t, 11, usage.InputTokens)
	require.Equal(t, 5, usage.OutputTokens)
	require.Equal(t, 2, usage.CacheReadInputTokens)
}

func TestExtractOpenAIUsage_PreservesResponseUsagePriority(t *testing.T) {
	body := []byte(`{"data":{"usage":{"prompt_tokens":100,"completion_tokens":50}},"response":{"usage":{"input_tokens":11,"output_tokens":5}}}`)

	usage, ok := protocolopenai.ExtractOpenAIUsageFromJSONBytes(body)

	require.True(t, ok)
	require.Equal(t, 11, usage.InputTokens)
	require.Equal(t, 5, usage.OutputTokens)
}

func TestOpenAIGatewayService_BindHTTPResponseAccount(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	groupID := int64(4201)
	c.Set("api_key", &apikey.APIKey{ID: 501, GroupID: &groupID})
	httpapi.SetHTTPResponseOwner(c, 601, 501)

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 37001, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}
	svc.responseOutput.BindResponseAccount(context.Background(), c, account, "resp_http_001")

	got, err := svc.ResponseStateStore().GetResponseAccount(context.Background(), groupID, "resp_http_001")
	require.NoError(t, err)
	require.Equal(t, account.Record.ID, got)

	owned, err := session.ValidateHTTPResponseOwner(context.Background(), func() session.HTTPResponseOwnerReader { return svc.ResponseStateStore() }, groupID, "resp_http_001", 601, 501)
	require.NoError(t, err)
	require.True(t, owned)

	owned, err = session.ValidateHTTPResponseOwner(context.Background(), func() session.HTTPResponseOwnerReader { return svc.ResponseStateStore() }, groupID, "resp_http_001", 601, 502)
	require.NoError(t, err)
	require.True(t, owned, "API keys owned by the same downstream user remain interoperable")

	owned, err = session.ValidateHTTPResponseOwner(context.Background(), func() session.HTTPResponseOwnerReader { return svc.ResponseStateStore() }, groupID, "resp_http_001", 602, 501)
	require.NoError(t, err)
	require.False(t, owned)

	owned, err = session.ValidateHTTPResponseOwner(context.Background(), func() session.HTTPResponseOwnerReader { return svc.ResponseStateStore() }, groupID, "resp_unknown", 601, 501)
	require.NoError(t, err)
	require.False(t, owned)
}

type stubGatewayCache struct {
	sessionBindings map[string]int64
	deletedSessions map[string]int
}

func (c *stubGatewayCache) GetSessionAccountID(ctx context.Context, groupID int64, sessionHash string) (int64, error) {
	if id, ok := c.sessionBindings[sessionHash]; ok {
		return id, nil
	}
	return 0, errors.New("not found")
}

func (c *stubGatewayCache) SetSessionAccountID(ctx context.Context, groupID int64, sessionHash string, accountID int64, ttl time.Duration) error {
	if c.sessionBindings == nil {
		c.sessionBindings = make(map[string]int64)
	}
	c.sessionBindings[sessionHash] = accountID
	return nil
}

func (c *stubGatewayCache) RefreshSessionTTL(ctx context.Context, groupID int64, sessionHash string, ttl time.Duration) error {
	return nil
}

func (c *stubGatewayCache) DeleteSessionAccountID(ctx context.Context, groupID int64, sessionHash string) error {
	if c.sessionBindings == nil {
		return nil
	}
	if c.deletedSessions == nil {
		c.deletedSessions = make(map[string]int)
	}
	c.deletedSessions[sessionHash]++
	delete(c.sessionBindings, sessionHash)
	return nil
}

func (c *stubGatewayCache) SetSessionOwnerGroupID(ctx context.Context, userID int64, source, sessionHash string, groupID int64, ttl time.Duration) (bool, error) {
	return true, nil
}

func (c *stubGatewayCache) GetSessionOwnerGroupID(ctx context.Context, userID int64, source, sessionHash string) (int64, error) {
	return 0, errors.New("not found")
}

func (c *stubGatewayCache) RefreshSessionOwnerTTL(ctx context.Context, userID int64, source, sessionHash string, ttl time.Duration) error {
	return nil
}

func TestOpenAIStreamingTimeout(t *testing.T) {

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			StreamDataIntervalTimeout: 1,
			StreamKeepaliveInterval:   0,
			MaxLineSize:               defaultMaxLineSize,
		},
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	pr, pw := io.Pipe()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       pr,
		Header:     http.Header{},
	}

	start := time.Now()
	_, err := svc.responseOutput.Stream(c.Request.Context(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1}}, start, "model", "model", "")
	_ = pw.Close()
	_ = pr.Close()

	if err == nil || !strings.Contains(err.Error(), "stream data interval timeout") {
		t.Fatalf("expected stream timeout error, got %v", err)
	}
	if !strings.Contains(rec.Body.String(), "\"type\":\"error\"") || !strings.Contains(rec.Body.String(), "stream_timeout") {
		t.Fatalf("expected OpenAI-compatible error SSE event, got %q", rec.Body.String())
	}
}

func TestOpenAIStreamingContextCanceledReturnsIncompleteErrorWithoutInjectingErrorEvent(t *testing.T) {

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			StreamDataIntervalTimeout: 0,
			StreamKeepaliveInterval:   0,
			MaxLineSize:               defaultMaxLineSize,
		},
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       cancelReadCloser{},
		Header:     http.Header{},
	}

	_, err := svc.responseOutput.Stream(c.Request.Context(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1}}, time.Now(), "model", "model", "")
	if err == nil || !strings.Contains(err.Error(), "stream usage incomplete") {
		t.Fatalf("expected incomplete stream error, got %v", err)
	}
	if strings.Contains(rec.Body.String(), "event: error") || strings.Contains(rec.Body.String(), "stream_read_error") {
		t.Fatalf("expected no injected SSE error event, got %q", rec.Body.String())
	}
}

func TestOpenAIStreamingReadErrorBeforeOutputReturnsFailover(t *testing.T) {

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			StreamDataIntervalTimeout: 0,
			StreamKeepaliveInterval:   0,
			MaxLineSize:               defaultMaxLineSize,
		},
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       errReadCloser{err: io.ErrUnexpectedEOF},
		Header:     http.Header{"X-Request-Id": []string{"rid-disconnect"}},
	}

	_, err := svc.responseOutput.Stream(c.Request.Context(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Name: "acc"}}, time.Now(), "model", "model", "")
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.False(t, c.Writer.Written())
	require.Empty(t, rec.Body.String())
}

func TestOpenAIStreamingPostOutputDisconnectQuarantinesSharedProxyWithoutSameStreamFailover(t *testing.T) {

	proxyID := int64(4698)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 469801,
		Name:     "oauth-on-shared-proxy",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		ProxyID:  &proxyID},
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{
		MaxLineSize: defaultMaxLineSize,
	}}})
	// 本用例需要把两次循环视为独立故障，关闭生产环境的并发断流折叠窗口。
	svc.openaiProxyStreamCircuit = egress.NewProxyStreamCircuit(egress.ProxyStreamCircuitSettings{
		FailureThreshold: 2,
		FailureWindow:    time.Minute,
		QuarantineTTL:    10 * time.Minute,
		MaxEntries:       16,
	})
	bindCompatibleSelectionFixture(svc)

	for _, readErr := range []error{
		io.ErrUnexpectedEOF,
		errors.New("http2: client connection lost"),
	} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body: &openAIStreamReadThenErrorCloser{
				reader: strings.NewReader(strings.Join([]string{
					"event: response.output_text.delta",
					`data: {"type":"response.output_text.delta","delta":"partial"}`,
					"",
				}, "\n")),
				err: readErr,
			},
			Header: http.Header{"X-Request-Id": []string{"rid-proxy-disconnect"}},
		}

		_, err := svc.responseOutput.Stream(c.Request.Context(), resp, c, account, time.Now(), "gpt-5.6-sol", "gpt-5.6-sol", "")
		require.Error(t, err)
		var failoverErr *forwardcore.UpstreamFailoverError
		require.False(t, errors.As(err, &failoverErr), "post-output disconnect must not fail over inside the same stream")
		require.Contains(t, rec.Body.String(), "partial")
	}

	compatible, reason := selectionDiagnosticForStreamTest(t, svc, account)
	require.False(t, compatible, "the next request must exclude accounts sharing the quarantined proxy")
	require.Equal(t, "proxy_stream_quarantined", reason)
}

func TestOpenAIStreamingTerminalAndClientCancellationDoNotQuarantineProxy(t *testing.T) {

	proxyID := int64(4699)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 469901, Name: "oauth", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, ProxyID: &proxyID}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}})

	terminalRecorder := httptest.NewRecorder()
	terminalCtx, _ := gin.CreateTestContext(terminalRecorder)
	terminalCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	terminalResp := &http.Response{
		StatusCode: http.StatusOK,
		Body: &openAIStreamReadThenErrorCloser{
			reader: strings.NewReader(strings.Join([]string{
				"event: response.completed",
				`data: {"type":"response.completed","response":{"status":"completed","output":[],"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}}`,
				"",
			}, "\n")),
			err: io.ErrUnexpectedEOF,
		},
		Header: http.Header{},
	}
	_, err := svc.responseOutput.Stream(terminalCtx.Request.Context(), terminalResp, terminalCtx, account, time.Now(), "model", "model", "")
	require.NoError(t, err)

	for range 2 {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body: &openAIStreamReadThenErrorCloser{
				reader: strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"),
				err:    context.Canceled,
			},
			Header: http.Header{},
		}
		_, err = svc.responseOutput.Stream(c.Request.Context(), resp, c, account, time.Now(), "model", "model", "")
		require.Error(t, err)
	}

	compatible, reason := selectionDiagnosticForStreamTest(t, svc, account)
	require.True(t, compatible)
	require.Empty(t, reason)
}

func TestOpenAIStreamingResponseFailedBeforeOutputReturnsFailover(t *testing.T) {

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			StreamDataIntervalTimeout: 0,
			StreamKeepaliveInterval:   0,
			MaxLineSize:               defaultMaxLineSize,
		},
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
			"event: response.in_progress",
			`data: {"type":"response.in_progress","response":{"id":"resp_1"}}`,
			"",
			"event: response.failed",
			`data: {"type":"response.failed","error":{"message":"An error occurred while processing your request."}}`,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-failed"}},
	}

	_, err := svc.responseOutput.Stream(c.Request.Context(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Name: "acc"}}, time.Now(), "model", "model", "")
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.False(t, failoverErr.RetryableOnSameAccount)
	require.Contains(t, string(failoverErr.ResponseBody), "An error occurred while processing your request")
	require.False(t, c.Writer.Written())
	require.Empty(t, rec.Body.String())
}

func TestOpenAIStreamingResponseFailedCapacityBeforeOutputReturnsFailover(t *testing.T) {

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			StreamDataIntervalTimeout: 0,
			StreamKeepaliveInterval:   0,
			MaxLineSize:               defaultMaxLineSize,
		},
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			"event: response.failed",
			`data: {"type":"response.failed","response":{"error":{"message":"The selected model is at capacity. Please try again later."}}}`,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-capacity"}},
	}

	// 池模式的瞬态容量错误即使未显式配置 502，也应在同一账号上受限重试。
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeAPIKey,
		Name:     "pool-account",
		Credentials: map[string]any{
			"pool_mode": true,
		}},
	}
	_, err := svc.responseOutput.Stream(c.Request.Context(), resp, c, account, time.Now(), "model", "model", "")
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.True(t, failoverErr.RetryableOnSameAccount)
	require.Contains(t, string(failoverErr.ResponseBody), "selected model is at capacity")
	require.False(t, c.Writer.Written())
	require.Empty(t, rec.Body.String())
}

func TestOpenAIStreamingResponseFailedBeforeOutputServerOverloadedCodeReturnsFailover(t *testing.T) {

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			StreamDataIntervalTimeout: 0,
			StreamKeepaliveInterval:   0,
			MaxLineSize:               defaultMaxLineSize,
		},
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
			"event: response.failed",
			`data: {"type":"response.failed","response":{"id":"resp_1","error":{"code":"server_is_overloaded","message":"Please retry later."}}}`,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-overloaded-failed"}},
	}

	_, err := svc.responseOutput.Stream(c.Request.Context(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Name: "acc"}}, time.Now(), "model", "model", "")
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusServiceUnavailable, failoverErr.StatusCode)
	require.Contains(t, string(failoverErr.ResponseBody), "Please retry later")
	require.False(t, c.Writer.Written())
	require.Empty(t, rec.Body.String())
}

func TestOpenAIStreamingResponseFailedBeforeOutputRateLimitUsesPoolRetryPolicy(t *testing.T) {

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			StreamDataIntervalTimeout: 0,
			StreamKeepaliveInterval:   0,
			MaxLineSize:               defaultMaxLineSize,
		},
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
			"event: response.failed",
			`data: {"type":"response.failed","response":{"id":"resp_1","status":"failed","error":{"type":"invalid_request_error","code":"rate_limit_exceeded","message":"Concurrency limit exceeded for account, please retry later"}}}`,
			"",
		}, "\n"))),
		Header: http.Header{
			"X-Request-Id": []string{"rid-rate-limit-failed"},
			"Retry-After":  []string{"1"},
		},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeAPIKey,
		Name:     "pool-account",
		Credentials: map[string]any{
			"pool_mode":                    true,
			"pool_mode_retry_count":        float64(1),
			"pool_mode_retry_status_codes": []any{float64(http.StatusTooManyRequests)},
		}},
	}

	_, err := svc.responseOutput.Stream(c.Request.Context(), resp, c, account, time.Now(), "model", "model", "")
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
	require.True(t, failoverErr.RetryableOnSameAccount)
	require.Equal(t, "1", http.Header(failoverErr.ResponseHeaders).Get("Retry-After"))
	require.Equal(t, "rate_limit_error", gjson.GetBytes(failoverErr.ResponseBody, "error.type").String())
	require.Contains(t, string(failoverErr.ResponseBody), "Concurrency limit exceeded")
	require.False(t, c.Writer.Written())
	require.Empty(t, rec.Body.String())

	opsValue, ok := c.Get(httpapi.OpsUpstreamErrorsKey)
	require.True(t, ok)
	opsEvents, ok := opsValue.([]*ops.OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.NotEmpty(t, opsEvents)
	require.Equal(t, http.StatusTooManyRequests, opsEvents[len(opsEvents)-1].UpstreamStatusCode)
}

// 流内 rate limit 进入 OAuth 同账号重试窗口，但不立即写账号级限流/封禁状态：
// HTTP 200 流的 x-codex-* 头不能让窗口内的账号提前失去调度资格。
func TestOpenAIStreamingResponseFailedRateLimitDoesNotBlockAccountScheduling(t *testing.T) {

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			StreamDataIntervalTimeout: 0,
			StreamKeepaliveInterval:   0,
			MaxLineSize:               defaultMaxLineSize,
		},
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
			"event: response.failed",
			`data: {"type":"response.failed","response":{"id":"resp_1","status":"failed","error":{"code":"rate_limit_exceeded","message":"Concurrency limit exceeded for account, please retry later"}}}`,
			"",
		}, "\n"))),
		Header: http.Header{
			"X-Codex-Primary-Used-Percent":        []string{"12"},
			"X-Codex-Primary-Reset-After-Seconds": []string{"604800"},
			"Retry-After":                         []string{"1"},
		},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 11, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Name: "oauth-account"}}

	_, err := svc.responseOutput.Stream(c.Request.Context(), resp, c, account, time.Now(), "model", "model", "")
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
	require.True(t, failoverErr.RetryableOnSameAccount)
	require.False(t, failoverErr.SameAccountRetryDeadline.IsZero())
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestOpenAIStreamingResponseFailedAfterOutputSanitizesVerboseResponseForClient(t *testing.T) {

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			StreamDataIntervalTimeout: 0,
			StreamKeepaliveInterval:   0,
			MaxLineSize:               defaultMaxLineSize,
		},
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	longInstructions := strings.Repeat("You are GPT-5.1 running in the Codex CLI. ", 20)
	failedPayload := fmt.Sprintf(
		`{"type":"response.failed","response":{"id":"resp_failed","object":"response","created_at":1782446336,"status":"failed","instructions":%q,"output":[{"type":"message","content":[{"type":"output_text","text":"large"}]}],"usage":{"input_tokens":123,"output_tokens":0},"error":{"code":"context_length_exceeded","message":"Your input exceeds the context window of this model. Please adjust your input and try again."}}}`,
		longInstructions,
	)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			"event: response.created",
			`data: {"type":"response.created","response":{"id":"resp_failed"}}`,
			"",
			"event: response.output_text.delta",
			`data: {"type":"response.output_text.delta","delta":"partial"}`,
			"",
			"event: response.failed",
			"data: " + failedPayload,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-failed-after-output"}},
	}

	result, err := svc.responseOutput.Stream(c.Request.Context(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Name: "acc"}}, time.Now(), "model", "model", "")
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr), "流已向客户端输出后不得重放请求")
	require.NotNil(t, result)
	require.Equal(t, 123, result.Usage.InputTokens)

	body := rec.Body.String()
	require.Contains(t, body, "event: response.failed")
	require.Contains(t, body, "context_length_exceeded")
	require.Contains(t, body, `"type":"invalid_request_error"`)
	require.Contains(t, body, "Your input exceeds the context window")
	require.NotContains(t, body, "You are GPT-5.1 running in the Codex CLI")
	require.NotContains(t, body, `"instructions"`)
	require.NotContains(t, body, `"output"`)
	require.NotContains(t, body, `"usage"`)
}

func TestOpenAIStreamingContextWindowResponseFailedBeforeOutputPassesThrough(t *testing.T) {

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			StreamDataIntervalTimeout: 0,
			StreamKeepaliveInterval:   0,
			MaxLineSize:               defaultMaxLineSize,
		},
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
			"event: response.failed",
			`data: {"type":"response.failed","error":{"type":"upstream_error","message":"Your input exceeds the context window of this model. Please adjust your input and try again.","code":null}}`,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-context-window-failed"}},
	}

	_, err := svc.responseOutput.Stream(c.Request.Context(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Name: "acc"}}, time.Now(), "model", "model", "")
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.True(t, c.Writer.Written())
	require.Contains(t, rec.Body.String(), "response.failed")
	require.Contains(t, rec.Body.String(), `"type":"upstream_error"`)
	require.Contains(t, rec.Body.String(), "Your input exceeds the context window")
}

func TestOpenAIStreamingContextWindowResponseFailedBeforeOutputAppliesPassthroughRule(t *testing.T) {

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			StreamDataIntervalTimeout: 0,
			StreamKeepaliveInterval:   0,
			MaxLineSize:               defaultMaxLineSize,
		},
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	rule := gatewaytestkit.NonFailoverRule(http.StatusBadRequest, "context_length_exceeded", http.StatusBadRequest, "")
	rule.Platforms = []string{capability.PlatformOpenAI}
	rule.PassthroughBody = true
	rule.CustomMessage = nil
	ruleSvc := gatewaytestkit.ErrorRules([]*errorpolicy.ErrorPassthroughRule{rule})
	httpapi.BindErrorPassthroughService(c, ruleSvc)

	upstreamMessage := "Your input exceeds the context window of this model. Please adjust your input and try again."
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			"event: response.created",
			`data: {"type":"response.created","response":{"id":"resp_1"}}`,
			"",
			"event: response.failed",
			`data: {"type":"response.failed","response":{"id":"resp_1","error":{"type":"invalid_request_error","code":"context_length_exceeded","message":"` + upstreamMessage + `"}}}`,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-context-window-passthrough-rule"}},
	}

	_, err := svc.responseOutput.Stream(c.Request.Context(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Name: "acc"}}, time.Now(), "model", "model", "")
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.True(t, httpapi.IsResponseCommitted(c))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	body := rec.Body.String()
	require.Equal(t, "upstream_error", gjson.Get(body, "error.type").String())
	require.Equal(t, upstreamMessage, gjson.Get(body, "error.message").String())
	require.NotContains(t, body, "response.failed")
	require.NotContains(t, body, "Upstream request failed")
	// 命中透传规则也应记录 ops 上游错误事件（对齐 CC/Messages 与 antigravity 先例）。
	opsVal, opsRecorded := c.Get(httpapi.OpsUpstreamErrorsKey)
	require.True(t, opsRecorded, "passthrough hit should record an ops upstream error event")
	opsEvents, _ := opsVal.([]*ops.OpsUpstreamErrorEvent)
	require.NotEmpty(t, opsEvents)
}

func TestOpenAIStreamingPreambleOnlyMissingTerminalReturnsFailover(t *testing.T) {

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			StreamDataIntervalTimeout: 0,
			StreamKeepaliveInterval:   0,
			MaxLineSize:               defaultMaxLineSize,
		},
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
			"event: response.in_progress",
			`data: {"type":"response.in_progress","response":{"id":"resp_1"}}`,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-missing-terminal"}},
	}

	_, err := svc.responseOutput.Stream(c.Request.Context(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Name: "acc"}}, time.Now(), "model", "model", "")
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.False(t, c.Writer.Written())
	require.Empty(t, rec.Body.String())
}

func TestOpenAIStreamingPreambleKeepaliveUsesDownstreamIdle(t *testing.T) {

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			StreamDataIntervalTimeout: 0,
			StreamKeepaliveInterval:   1,
			MaxLineSize:               defaultMaxLineSize,
		},
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	pr, pw := io.Pipe()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       pr,
		Header:     http.Header{},
	}

	go func() {
		defer func() { _ = pw.Close() }()
		_, _ = pw.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n"))
		for i := 0; i < 6; i++ {
			time.Sleep(250 * time.Millisecond)
			_, _ = pw.Write([]byte("data: {\"type\":\"response.in_progress\",\"response\":{\"id\":\"resp_1\"}}\n\n"))
		}
		_, _ = pw.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":2}}}\n\n"))
	}()

	result, err := svc.responseOutput.Stream(c.Request.Context(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Name: "acc"}}, time.Now(), "model", "model", "")
	_ = pr.Close()
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, rec.Body.String(), ":\n\n")
	require.Contains(t, rec.Body.String(), "response.completed")
}

func TestOpenAIStreamingNormalizesTerminalOutputFromDeltas(t *testing.T) {

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			StreamDataIntervalTimeout: 0,
			StreamKeepaliveInterval:   0,
			MaxLineSize:               defaultMaxLineSize,
		},
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"type":"response.created","response":{"id":"resp_sdk_parse"}}`,
			"",
			`data: {"type":"response.output_text.delta","delta":"pon"}`,
			"",
			`data: {"type":"response.output_text.delta","delta":"g"}`,
			"",
			`data: {"type":"response.completed","response":{"id":"resp_sdk_parse","status":"completed","output":null,"usage":{"input_tokens":1,"output_tokens":1}}}`,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-sdk-parse"}},
	}

	result, err := svc.responseOutput.Stream(c.Request.Context(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Name: "acc"}}, time.Now(), "model", "model", "")
	require.NoError(t, err)
	require.NotNil(t, result)

	terminalType, terminalPayload, ok := protocolopenai.ExtractOpenAISSETerminalEvent(rec.Body.String())
	require.True(t, ok)
	require.Equal(t, "response.completed", terminalType)
	output := gjson.GetBytes(terminalPayload, "response.output")
	require.True(t, output.IsArray())
	require.Len(t, output.Array(), 1)
	require.Equal(t, "pong", gjson.GetBytes(terminalPayload, "response.output.0.content.0.text").String())
}

func TestOpenAIStreamingNormalizesTerminalOutputToEmptyArray(t *testing.T) {

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			StreamDataIntervalTimeout: 0,
			StreamKeepaliveInterval:   0,
			MaxLineSize:               defaultMaxLineSize,
		},
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"type":"response.completed","response":{"id":"resp_empty","status":"completed","output":null,"usage":{"input_tokens":1,"output_tokens":0}}}`,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-empty-output"}},
	}

	result, err := svc.responseOutput.Stream(c.Request.Context(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Name: "acc"}}, time.Now(), "model", "model", "")
	require.NoError(t, err)
	require.NotNil(t, result)

	terminalType, terminalPayload, ok := protocolopenai.ExtractOpenAISSETerminalEvent(rec.Body.String())
	require.True(t, ok)
	require.Equal(t, "response.completed", terminalType)
	output := gjson.GetBytes(terminalPayload, "response.output")
	require.True(t, output.IsArray())
	require.Len(t, output.Array(), 0)
}

func TestOpenAIStreamingPolicyResponseFailedBeforeOutputPassesThrough(t *testing.T) {

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			StreamDataIntervalTimeout: 0,
			StreamKeepaliveInterval:   0,
			MaxLineSize:               defaultMaxLineSize,
		},
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
			"event: response.failed",
			`data: {"type":"response.failed","error":{"type":"safety_error","message":"This request has been flagged for potentially high-risk cyber activity."}}`,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-policy-failed"}},
	}

	_, err := svc.responseOutput.Stream(c.Request.Context(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Name: "acc"}}, time.Now(), "model", "model", "")
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.True(t, c.Writer.Written())
	require.Contains(t, rec.Body.String(), "response.failed")
	require.Contains(t, rec.Body.String(), "high-risk cyber activity")
}

func TestOpenAIStreamingPolicyResponseFailedCarriesHTTPStatusWarning(t *testing.T) {

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			StreamDataIntervalTimeout: 0,
			StreamKeepaliveInterval:   0,
			MaxLineSize:               defaultMaxLineSize,
		},
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
			"event: response.failed",
			`data: {"type":"response.failed","error":{"type":"safety_error","message":"This request has been flagged for potentially high-risk cyber activity."}}`,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-policy-failed"}},
	}

	_, err := svc.responseOutput.Stream(c.Request.Context(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Name: "acc"}}, time.Now(), "model", "model", "")

	require.Error(t, err)
	warning, ok := forwardcore.WarningFromError(err)
	require.True(t, ok)
	require.Equal(t, http.StatusOK, warning.StatusCode)
	require.Contains(t, warning.Message, "high-risk cyber activity")
	require.Contains(t, string(warning.ResponseBody), "response.failed")
}

func TestOpenAIStreamingCybersecurityRiskResponseFailedCarriesHTTPStatusWarning(t *testing.T) {

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			StreamDataIntervalTimeout: 0,
			StreamKeepaliveInterval:   0,
			MaxLineSize:               defaultMaxLineSize,
		},
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
			"event: response.failed",
			`data: {"type":"response.failed","response":{"error":{"message":"This content was flagged for possible cybersecurity risk. If this seems wrong, try rephrasing your request."}}}`,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-cybersecurity-risk"}},
	}

	_, err := svc.responseOutput.Stream(c.Request.Context(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Name: "acc"}}, time.Now(), "model", "model", "")

	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	warning, ok := forwardcore.WarningFromError(err)
	require.True(t, ok)
	require.Equal(t, http.StatusOK, warning.StatusCode)
	require.Contains(t, warning.Message, "cybersecurity risk")
	require.Contains(t, string(warning.ResponseBody), "response.failed")
}

func TestOpenAIShouldFailoverUpstreamResponse_CyberWarningDoesNotFailover(t *testing.T) {

	body := []byte(`{"error":{"message":"This request has been flagged for potentially high-risk cyber activity."}}`)

	require.False(t,
		gatewayprovider.ShouldFailoverOpenAIResponse(http.StatusUnauthorized, "This request has been flagged for potentially high-risk cyber activity.", body),
	)
	require.True(t,
		gatewayprovider.ShouldFailoverOpenAIResponse(http.StatusUnauthorized, "User account is not active", []byte(`{"code":"USER_INACTIVE","message":"User account is not active"}`)),
	)
}

func TestOpenAIHandleErrorResponse_CyberWarningPassesThroughMessage(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	message := "This request has been flagged for potentially high-risk cyber activity."
	resp := &http.Response{
		StatusCode: http.StatusForbidden,
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"` + message + `"}}`)),
		Header:     http.Header{},
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Name: "openai", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}

	_, err := svc.responseOutput.ResponseError(context.Background(), resp, c, account, []byte(`{"model":"gpt-5"}`))

	require.Error(t, err)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), message)
	warning, ok := forwardcore.WarningFromError(err)
	require.True(t, ok)
	require.Equal(t, http.StatusForbidden, warning.StatusCode)
	require.Contains(t, warning.Message, "high-risk cyber")
}

func TestOpenAIStreamingClientDisconnectDrainsUpstreamUsage(t *testing.T) {

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			StreamDataIntervalTimeout: 0,
			StreamKeepaliveInterval:   0,
			MaxLineSize:               defaultMaxLineSize,
		},
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	c.Writer = &failingGinWriter{ResponseWriter: c.Writer, failAfter: 0}

	pr, pw := io.Pipe()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       pr,
		Header:     http.Header{},
	}

	go func() {
		defer func() { _ = pw.Close() }()
		_, _ = pw.Write([]byte("data: {\"type\":\"response.in_progress\",\"response\":{}}\n\n"))
		_, _ = pw.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":5,\"input_tokens_details\":{\"cached_tokens\":1}}}}\n\n"))
	}()

	result, err := svc.responseOutput.Stream(c.Request.Context(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1}}, time.Now(), "model", "model", "")
	_ = pr.Close()
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if result == nil || result.Usage == nil {
		t.Fatalf("expected usage result")
	}
	if result.Usage.InputTokens != 3 || result.Usage.OutputTokens != 5 || result.Usage.CacheReadInputTokens != 1 {
		t.Fatalf("unexpected usage: %+v", *result.Usage)
	}
	if strings.Contains(rec.Body.String(), "event: error") || strings.Contains(rec.Body.String(), "write_failed") {
		t.Fatalf("expected no injected SSE error event, got %q", rec.Body.String())
	}
}

func TestOpenAIStreamingMissingTerminalEventReturnsIncompleteError(t *testing.T) {

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			StreamDataIntervalTimeout: 0,
			StreamKeepaliveInterval:   0,
			MaxLineSize:               defaultMaxLineSize,
		},
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	pr, pw := io.Pipe()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       pr,
		Header:     http.Header{},
	}

	go func() {
		defer func() { _ = pw.Close() }()
		_, _ = pw.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\",\"output_index\":0}\n\n"))
	}()

	_, err := svc.responseOutput.Stream(c.Request.Context(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1}}, time.Now(), "model", "model", "")
	_ = pr.Close()
	if err == nil || !strings.Contains(err.Error(), "missing terminal event") {
		t.Fatalf("expected missing terminal event error, got %v", err)
	}
}

func TestOpenAIStreamingPassthroughMissingTerminalEventReturnsIncompleteError(t *testing.T) {

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			MaxLineSize: defaultMaxLineSize,
		},
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	pr, pw := io.Pipe()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       pr,
		Header:     http.Header{},
	}

	go func() {
		defer func() { _ = pw.Close() }()
		_, _ = pw.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\",\"output_index\":0}\n\n"))
	}()

	_, err := svc.responseOutput.PassthroughStream(c.Request.Context(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1}}, time.Now(), "", "")
	_ = pr.Close()
	if err == nil || !strings.Contains(err.Error(), "missing terminal event") {
		t.Fatalf("expected missing terminal event error, got %v", err)
	}
}

func TestOpenAIStreamingPassthroughPostOutputDisconnectQuarantinesSharedProxy(t *testing.T) {

	proxyID := int64(4698)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 469804, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, ProxyID: &proxyID}}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}})
	// 本用例需要把两次循环视为独立故障，关闭生产环境的并发断流折叠窗口。
	svc.openaiProxyStreamCircuit = egress.NewProxyStreamCircuit(egress.ProxyStreamCircuitSettings{
		FailureThreshold: 2,
		FailureWindow:    time.Minute,
		QuarantineTTL:    10 * time.Minute,
		MaxEntries:       16,
	})
	bindCompatibleSelectionFixture(svc)

	for _, readErr := range []error{io.ErrUnexpectedEOF, errors.New("http2: client connection lost")} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body: &openAIStreamReadThenErrorCloser{
				reader: strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"),
				err:    readErr,
			},
			Header: http.Header{"X-Request-Id": []string{"rid-passthrough-proxy-disconnect"}},
		}

		_, err := svc.responseOutput.PassthroughStream(c.Request.Context(), resp, c, account, time.Now(), "model", "model")
		require.Error(t, err)
		var failoverErr *forwardcore.UpstreamFailoverError
		require.False(t, errors.As(err, &failoverErr), "post-output disconnect must not fail over inside the same stream")
		require.Contains(t, rec.Body.String(), "partial")
	}

	require.True(t, svc.getOpenAIProxyStreamCircuit().IsBlocked(*account.Record.ProxyID, time.Now()))
}

func TestOpenAIStreamingPassthroughResponseFailedBeforeOutputReturnsFailover(t *testing.T) {

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			MaxLineSize: defaultMaxLineSize,
		},
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
			"event: response.failed",
			`data: {"type":"response.failed","error":{"message":"upstream processing failed"}}`,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-passthrough-failed"}},
	}

	_, err := svc.responseOutput.PassthroughStream(c.Request.Context(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Name: "acc"}}, time.Now(), "", "")
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.Contains(t, string(failoverErr.ResponseBody), "upstream processing failed")
	require.False(t, c.Writer.Written())
	require.Empty(t, rec.Body.String())
}

func TestOpenAIStreamingPassthroughContextWindowResponseFailedBeforeOutputAppliesPassthroughRule(t *testing.T) {

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			MaxLineSize: defaultMaxLineSize,
		},
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	rule := gatewaytestkit.NonFailoverRule(http.StatusBadRequest, "input exceeds the context window", http.StatusBadRequest, "")
	rule.Platforms = []string{capability.PlatformOpenAI}
	rule.PassthroughBody = true
	rule.CustomMessage = nil
	ruleSvc := gatewaytestkit.ErrorRules([]*errorpolicy.ErrorPassthroughRule{rule})
	httpapi.BindErrorPassthroughService(c, ruleSvc)

	upstreamMessage := "Your input exceeds the context window of this model. Please adjust your input and try again."
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			"event: response.created",
			`data: {"type":"response.created","response":{"id":"resp_1"}}`,
			"",
			"event: response.failed",
			`data: {"type":"response.failed","response":{"id":"resp_1","error":{"type":"invalid_request_error","code":"context_length_exceeded","message":"` + upstreamMessage + `"}}}`,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-pass-context-window-passthrough-rule"}},
	}

	_, err := svc.responseOutput.PassthroughStream(c.Request.Context(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Name: "acc"}}, time.Now(), "", "")
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.True(t, httpapi.IsResponseCommitted(c))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	body := rec.Body.String()
	require.Equal(t, "upstream_error", gjson.Get(body, "error.type").String())
	require.Equal(t, upstreamMessage, gjson.Get(body, "error.message").String())
	require.NotContains(t, body, "response.failed")
	require.NotContains(t, body, "Upstream request failed")
	// 命中透传规则也应记录 ops 上游错误事件（对齐 CC/Messages 与 antigravity 先例）。
	opsVal, opsRecorded := c.Get(httpapi.OpsUpstreamErrorsKey)
	require.True(t, opsRecorded, "passthrough hit should record an ops upstream error event")
	opsEvents, _ := opsVal.([]*ops.OpsUpstreamErrorEvent)
	require.NotEmpty(t, opsEvents)
}

func TestOpenAIStreamingPassthroughContextWindowResponseFailedBeforeOutputWithoutRulePassesThrough(t *testing.T) {

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			MaxLineSize: defaultMaxLineSize,
		},
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
			"event: response.failed",
			`data: {"type":"response.failed","response":{"id":"resp_1","error":{"type":"invalid_request_error","code":"context_length_exceeded","message":"Your input exceeds the context window of this model. Please adjust your input and try again."}}}`,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-pass-context-window-no-rule"}},
	}

	_, err := svc.responseOutput.PassthroughStream(c.Request.Context(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Name: "acc"}}, time.Now(), "", "")
	require.Error(t, err)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	body := rec.Body.String()
	require.Contains(t, body, "event: response.failed")
	require.Contains(t, body, "context_length_exceeded")
	require.Contains(t, body, "Your input exceeds the context window")
}

func TestOpenAIStreamingPassthroughResponseFailedAfterOutputSanitizesVerboseResponseForClient(t *testing.T) {

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			MaxLineSize: defaultMaxLineSize,
		},
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	longInstructions := strings.Repeat("You are GPT-5.1 running in the Codex CLI. ", 20)
	failedPayload := fmt.Sprintf(
		`{"type":"response.failed","response":{"id":"resp_pass_failed","object":"response","created_at":1782446336,"status":"failed","instructions":%q,"output":[{"type":"message","content":[{"type":"output_text","text":"large"}]}],"usage":{"input_tokens":123,"output_tokens":0},"error":{"code":"context_length_exceeded","message":"Your input exceeds the context window of this model. Please adjust your input and try again."}}}`,
		longInstructions,
	)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			"event: response.created",
			`data: {"type":"response.created","response":{"id":"resp_pass_failed"}}`,
			"",
			"event: response.output_text.delta",
			`data: {"type":"response.output_text.delta","delta":"partial"}`,
			"",
			"event: response.failed",
			"data: " + failedPayload,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-pass-failed-after-output"}},
	}

	result, err := svc.responseOutput.PassthroughStream(c.Request.Context(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Name: "acc"}}, time.Now(), "", "")
	require.Error(t, err)
	require.NotNil(t, result)
	require.Equal(t, 123, result.Usage.InputTokens)

	body := rec.Body.String()
	require.Contains(t, body, "event: response.failed")
	require.Contains(t, body, "context_length_exceeded")
	require.Contains(t, body, `"type":"invalid_request_error"`)
	require.Contains(t, body, "Your input exceeds the context window")
	require.NotContains(t, body, "You are GPT-5.1 running in the Codex CLI")
	require.NotContains(t, body, `"instructions"`)
	require.NotContains(t, body, `"output"`)
	require.NotContains(t, body, `"usage"`)
}

func TestOpenAIStreamingPassthroughResponseDoneWithoutDoneMarkerStillSucceeds(t *testing.T) {

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			MaxLineSize: defaultMaxLineSize,
		},
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	pr, pw := io.Pipe()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       pr,
		Header:     http.Header{},
	}

	go func() {
		defer func() { _ = pw.Close() }()
		_, _ = pw.Write([]byte("data: {\"type\":\"response.done\",\"response\":{\"usage\":{\"input_tokens\":2,\"output_tokens\":3,\"input_tokens_details\":{\"cached_tokens\":1}}}}\n\n"))
	}()

	result, err := svc.responseOutput.PassthroughStream(c.Request.Context(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1}}, time.Now(), "", "")
	_ = pr.Close()
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Usage)
	require.Equal(t, 2, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.Equal(t, 1, result.Usage.CacheReadInputTokens)
}

func TestOpenAIStreamingPassthroughResponseIncompleteWithoutDoneMarkerStillSucceeds(t *testing.T) {

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			MaxLineSize: defaultMaxLineSize,
		},
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	pr, pw := io.Pipe()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       pr,
		Header:     http.Header{},
	}

	go func() {
		defer func() { _ = pw.Close() }()
		_, _ = pw.Write([]byte("data: {\"type\":\"response.incomplete\",\"response\":{\"usage\":{\"input_tokens\":2,\"output_tokens\":3,\"input_tokens_details\":{\"cached_tokens\":1}}}}\n\n"))
	}()

	result, err := svc.responseOutput.PassthroughStream(c.Request.Context(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1}}, time.Now(), "", "")
	_ = pr.Close()
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Usage)
	require.Equal(t, 2, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.Equal(t, 1, result.Usage.CacheReadInputTokens)
}

func TestOpenAIStreamingTooLong(t *testing.T) {

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			StreamDataIntervalTimeout: 0,
			StreamKeepaliveInterval:   0,
			MaxLineSize:               64 * 1024,
		},
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	pr, pw := io.Pipe()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       pr,
		Header:     http.Header{},
	}

	go func() {
		defer func() { _ = pw.Close() }()
		// 写入超过 MaxLineSize 的单行数据，触发 ErrTooLong
		payload := "data: " + strings.Repeat("a", 128*1024) + "\n"
		_, _ = pw.Write([]byte(payload))
	}()

	_, err := svc.responseOutput.Stream(c.Request.Context(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2}}, time.Now(), "model", "model", "")
	_ = pr.Close()

	if !errors.Is(err, bufio.ErrTooLong) {
		t.Fatalf("expected ErrTooLong, got %v", err)
	}
	if !strings.Contains(rec.Body.String(), "\"type\":\"error\"") || !strings.Contains(rec.Body.String(), "response_too_large") {
		t.Fatalf("expected OpenAI-compatible error SSE event, got %q", rec.Body.String())
	}
}

func TestOpenAINonStreamingContentTypePassThrough(t *testing.T) {

	cfg := &config.Config{
		Security: config.SecurityConfig{
			ResponseHeaders: config.ResponseHeaderConfig{Enabled: false},
		},
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	body := []byte(`{"usage":{"input_tokens":1,"output_tokens":2,"input_tokens_details":{"cached_tokens":0}}}`)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/vnd.test+json"}},
	}

	_, err := svc.responseOutput.NonStream(c.Request.Context(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation}}, "model", "model")
	if err != nil {
		t.Fatalf("handleNonStreamingResponse error: %v", err)
	}

	if !strings.Contains(rec.Header().Get("Content-Type"), "application/vnd.test+json") {
		t.Fatalf("expected Content-Type passthrough, got %q", rec.Header().Get("Content-Type"))
	}
}

func TestOpenAINonStreamingContentTypeDefault(t *testing.T) {

	cfg := &config.Config{
		Security: config.SecurityConfig{
			ResponseHeaders: config.ResponseHeaderConfig{Enabled: false},
		},
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	body := []byte(`{"usage":{"input_tokens":1,"output_tokens":2,"input_tokens_details":{"cached_tokens":0}}}`)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(body)),
		Header:     http.Header{},
	}

	_, err := svc.responseOutput.NonStream(c.Request.Context(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation}}, "model", "model")
	if err != nil {
		t.Fatalf("handleNonStreamingResponse error: %v", err)
	}

	if !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("expected default Content-Type, got %q", rec.Header().Get("Content-Type"))
	}
}

func TestOpenAIStreamingHeadersOverride(t *testing.T) {

	cfg := &config.Config{
		Security: config.SecurityConfig{
			ResponseHeaders: config.ResponseHeaderConfig{Enabled: false},
		},
		Gateway: config.GatewayConfig{
			StreamDataIntervalTimeout: 0,
			StreamKeepaliveInterval:   0,
			MaxLineSize:               defaultMaxLineSize,
		},
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	pr, pw := io.Pipe()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       pr,
		Header: http.Header{
			"Cache-Control": []string{"upstream"},
			"X-Request-Id":  []string{"req-123"},
			"Content-Type":  []string{"application/custom"},
		},
	}

	go func() {
		defer func() { _ = pw.Close() }()
		_, _ = pw.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{}}\n\n"))
	}()

	_, err := svc.responseOutput.Stream(c.Request.Context(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1}}, time.Now(), "model", "model", "")
	_ = pr.Close()
	if err != nil {
		t.Fatalf("handleStreamingResponse error: %v", err)
	}

	if rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("expected Cache-Control override, got %q", rec.Header().Get("Cache-Control"))
	}
	if rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("expected Content-Type override, got %q", rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("X-Request-Id") != "req-123" {
		t.Fatalf("expected X-Request-Id passthrough, got %q", rec.Header().Get("X-Request-Id"))
	}
}

func TestOpenAIStreamingReuseScannerBufferAndStillWorks(t *testing.T) {

	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			StreamDataIntervalTimeout: 0,
			StreamKeepaliveInterval:   0,
			MaxLineSize:               defaultMaxLineSize,
		},
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	pr, pw := io.Pipe()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       pr,
		Header:     http.Header{},
	}

	go func() {
		defer func() { _ = pw.Close() }()
		_, _ = pw.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":2,\"input_tokens_details\":{\"cached_tokens\":3}}}}\n\n"))
	}()

	result, err := svc.responseOutput.Stream(c.Request.Context(), resp, c, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1}}, time.Now(), "model", "model", "")
	_ = pr.Close()
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Usage)
	require.Equal(t, 1, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.Equal(t, 3, result.Usage.CacheReadInputTokens)
}

func TestOpenAIInvalidBaseURLWhenAllowlistDisabled(t *testing.T) {

	cfg := &config.Config{
		Security: config.SecurityConfig{
			URLAllowlist: config.URLAllowlistConfig{Enabled: false},
		},
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg})

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "://invalid-url"}},
	}

	_, err := svc.buildUpstreamRequest(c.Request.Context(), c, account, []byte("{}"), "token", false, "", false)
	if err == nil {
		t.Fatalf("expected error for invalid base_url when allowlist disabled")
	}
}

func TestOpenAIValidateUpstreamBaseURLDisabledRequiresHTTPS(t *testing.T) {
	cfg := &config.Config{
		Security: config.SecurityConfig{
			URLAllowlist: config.URLAllowlistConfig{Enabled: false},
		},
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg})

	if _, err := svc.validateUpstreamBaseURL("http://not-https.example.com"); err == nil {
		t.Fatalf("expected http to be rejected when allow_insecure_http is false")
	}
	normalized, err := svc.validateUpstreamBaseURL("https://example.com")
	if err != nil {
		t.Fatalf("expected https to be allowed when allowlist disabled, got %v", err)
	}
	if normalized != "https://example.com" {
		t.Fatalf("expected raw url passthrough, got %q", normalized)
	}
}

func TestOpenAIValidateUpstreamBaseURLDisabledAllowsHTTP(t *testing.T) {
	cfg := &config.Config{
		Security: config.SecurityConfig{
			URLAllowlist: config.URLAllowlistConfig{
				Enabled:           false,
				AllowInsecureHTTP: true,
			},
		},
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg})

	normalized, err := svc.validateUpstreamBaseURL("http://not-https.example.com")
	if err != nil {
		t.Fatalf("expected http allowed when allow_insecure_http is true, got %v", err)
	}
	if normalized != "http://not-https.example.com" {
		t.Fatalf("expected raw url passthrough, got %q", normalized)
	}
}

func TestOpenAIValidateUpstreamBaseURLEnabledEnforcesAllowlist(t *testing.T) {
	cfg := &config.Config{
		Security: config.SecurityConfig{
			URLAllowlist: config.URLAllowlistConfig{
				Enabled:       true,
				UpstreamHosts: []string{"example.com"},
			},
		},
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: cfg})

	if _, err := svc.validateUpstreamBaseURL("https://example.com"); err != nil {
		t.Fatalf("expected allowlisted host to pass, got %v", err)
	}
	if _, err := svc.validateUpstreamBaseURL("https://evil.com"); err == nil {
		t.Fatalf("expected non-allowlisted host to fail")
	}
}

func TestOpenAIUpdateCodexUsageSnapshotFromHeaders(t *testing.T) {
	repo := &snapshotUpdateAccountRepo{updateExtraCalls: make(chan map[string]any, 1)}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{accountRepo: repo}))
	headers := http.Header{}
	headers.Set("x-codex-primary-used-percent", "12")
	headers.Set("x-codex-secondary-used-percent", "34")
	headers.Set("x-codex-primary-window-minutes", "300")
	headers.Set("x-codex-secondary-window-minutes", "10080")
	headers.Set("x-codex-primary-reset-after-seconds", "600")
	headers.Set("x-codex-secondary-reset-after-seconds", "86400")

	svc.UpdateCodexUsageSnapshotFromHeaders(context.Background(), 123, headers)

	select {
	case updates := <-repo.updateExtraCalls:
		require.Equal(t, 12.0, updates["codex_5h_used_percent"])
		require.Equal(t, 34.0, updates["codex_7d_used_percent"])
		require.Equal(t, 600, updates["codex_5h_reset_after_seconds"])
		require.Equal(t, 86400, updates["codex_7d_reset_after_seconds"])
	case <-time.After(2 * time.Second):
		t.Fatal("expected UpdateExtra to be called")
	}
}

func TestOpenAIResponsesRequestPathSuffix(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "exact v1 responses", path: "/v1/responses", want: ""},
		{name: "compact v1 responses", path: "/v1/responses/compact", want: "/compact"},
		{name: "compact alias responses", path: "/responses/compact/", want: "/compact"},
		{name: "nested suffix", path: "/openai/v1/responses/compact/detail", want: "/compact/detail"},
		{name: "unrelated path", path: "/v1/chat/completions", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c.Request = httptest.NewRequest(http.MethodPost, tt.path, nil)
			require.Equal(t, tt.want, httpapi.OpenAIResponsesRequestPathSuffix(c))
		})
	}
}

func TestNormalizeOpenAICompactRequestBodyPreservesCurrentCodexPayloadFields(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","input":[{"type":"message","role":"user","content":"compact me"}],"instructions":"compact-test","tools":[{"type":"function","name":"shell"}],"parallel_tool_calls":true,"reasoning":{"effort":"high"},"text":{"verbosity":"low"},"previous_response_id":"resp_123","store":true,"stream":true,"prompt_cache_key":"cache_123"}`)

	normalized, changed, err := openai.NormalizeOpenAICompactRequestBody(body)

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "gpt-5.5", gjson.GetBytes(normalized, "model").String())
	require.True(t, gjson.GetBytes(normalized, "tools").Exists())
	require.True(t, gjson.GetBytes(normalized, "parallel_tool_calls").Bool())
	require.Equal(t, "high", gjson.GetBytes(normalized, "reasoning.effort").String())
	require.Equal(t, "low", gjson.GetBytes(normalized, "text.verbosity").String())
	require.Equal(t, "resp_123", gjson.GetBytes(normalized, "previous_response_id").String())
	require.False(t, gjson.GetBytes(normalized, "store").Exists())
	require.False(t, gjson.GetBytes(normalized, "stream").Exists())
	require.False(t, gjson.GetBytes(normalized, "prompt_cache_key").Exists())
}

func TestNormalizeOpenAICompactRequestBodyDropsParallelToolCallsWithoutUsableTools(t *testing.T) {
	for _, body := range []string{
		`{"model":"gpt-5.5","input":[],"parallel_tool_calls":true}`,
		`{"model":"gpt-5.5","input":[],"tools":[],"parallel_tool_calls":false}`,
		`{"model":"gpt-5.5","input":[],"tools":null,"parallel_tool_calls":true}`,
		`{"model":"gpt-5.5","input":[],"tools":{},"parallel_tool_calls":true}`,
	} {
		normalized, changed, err := openai.NormalizeOpenAICompactRequestBody([]byte(body))
		require.NoError(t, err)
		require.True(t, changed)
		require.False(t, gjson.GetBytes(normalized, "parallel_tool_calls").Exists(), string(normalized))
	}

	withTools := []byte(`{"model":"gpt-5.5","tools":[{"type":"function","name":"lookup"}],"parallel_tool_calls":false}`)
	normalized, _, err := openai.NormalizeOpenAICompactRequestBody(withTools)
	require.NoError(t, err)
	require.True(t, gjson.GetBytes(normalized, "parallel_tool_calls").Exists())
	require.False(t, gjson.GetBytes(normalized, "parallel_tool_calls").Bool())
}

func TestOpenAIBuildUpstreamRequestOpenAIPassthroughPreservesCompactPath(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewReader([]byte(`{"model":"gpt-5"}`)))

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Type: capability.AccountTypeOAuth}}

	req, err := svc.buildUpstreamRequestOpenAIPassthrough(c.Request.Context(), c, account, []byte(`{"model":"gpt-5"}`), "token")
	require.NoError(t, err)
	require.Equal(t, chatgptCodexURL+"/compact", req.URL.String())
	require.Equal(t, "application/json", req.Header.Get("Accept"))
	require.Equal(t, openai.CodexCLIVersion, req.Header.Get("Version"))
	require.Empty(t, req.Header.Get("OpenAI-Beta"), "Codex OAuth HTTP must not synthesize the legacy responses beta header")
	require.NotEmpty(t, req.Header.Get("Session_Id"))
	require.Equal(t, upstreamcore.HTTPUpstreamProfileOpenAI, upstreamcore.HTTPUpstreamProfileFromContext(req.Context()))
}

func TestOpenAIBuildUpstreamRequestOpenAIPassthroughPreservesExplicitAPIKeyBetaHeader(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader([]byte(`{"model":"gpt-5"}`)))
	c.Request.Header.Set("OpenAI-Beta", "api-key-specific-beta")

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: &config.Config{
		Security: config.SecurityConfig{
			URLAllowlist: config.URLAllowlistConfig{Enabled: false},
		},
	}})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}

	req, err := svc.buildUpstreamRequestOpenAIPassthrough(c.Request.Context(), c, account, []byte(`{"model":"gpt-5"}`), "token")
	require.NoError(t, err)
	require.Equal(t, "api-key-specific-beta", req.Header.Get("OpenAI-Beta"), "OAuth-only backport must not alter API-key passthrough headers")
}

func TestOpenAIBuildUpstreamRequestOpenAIPassthroughDoesNotPropagateInternalRequestID(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader([]byte(`{"model":"gpt-5"}`)))
	c.Request.Header.Set("X-Sub2API-Request-ID", "internal-request-123")

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
	req, err := svc.buildUpstreamRequestOpenAIPassthrough(c.Request.Context(), c, account, []byte(`{"model":"gpt-5"}`), "token")
	require.NoError(t, err)
	require.Empty(t, req.Header.Get("X-Sub2API-Request-ID"))
}

func TestOpenAIBuildUpstreamRequestCompactForcesJSONAcceptForOAuth(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewReader([]byte(`{"model":"gpt-5"}`)))

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Type: capability.AccountTypeOAuth,
		Credentials: map[string]any{"chatgpt_account_id": "chatgpt-acc"}},
	}

	req, err := svc.buildUpstreamRequest(c.Request.Context(), c, account, []byte(`{"model":"gpt-5"}`), "token", false, "", true)
	require.NoError(t, err)
	require.Equal(t, chatgptCodexURL+"/compact", req.URL.String())
	require.Equal(t, "application/json", req.Header.Get("Accept"))
	require.Equal(t, openai.CodexCLIVersion, req.Header.Get("Version"))
	require.Empty(t, req.Header.Get("OpenAI-Beta"), "Codex OAuth HTTP must not synthesize the legacy responses beta header")
	require.NotEmpty(t, req.Header.Get("Session_Id"))
	require.Equal(t, upstreamcore.HTTPUpstreamProfileOpenAI, upstreamcore.HTTPUpstreamProfileFromContext(req.Context()))
}

func TestOpenAIBuildUpstreamRequestOAuthMessagesBridgeUsesSessionOnly(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.5","prompt_cache_key":"anthropic-metadata-session-1","input":[{"type":"message","role":"developer","content":[{"type":"input_text","text":"<sub2api-claude-code-todo-guard>"}]},{"type":"message","role":"user","content":"hello"}]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("OpenAI-Beta", "responses=experimental")
	c.Request.Header.Set("originator", "codex_cli_rs")

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Type: capability.AccountTypeOAuth,
		Credentials: map[string]any{"chatgpt_account_id": "chatgpt-acc"}},
	}

	req, err := svc.buildUpstreamRequest(c.Request.Context(), c, account, body, "token", true, "anthropic-metadata-session-1", false)
	require.NoError(t, err)
	require.NotEmpty(t, req.Header.Get("Session_Id"))
	require.Empty(t, req.Header.Get("Conversation_Id"))
	require.Empty(t, req.Header.Get("OpenAI-Beta"))
	require.Empty(t, req.Header.Get("originator"))
}

func TestOpenAIBuildUpstreamRequestPreservesCompactPathForAPIKeyBaseURL(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/responses/compact", bytes.NewReader([]byte(`{"model":"gpt-5"}`)))

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: &config.Config{
		Security: config.SecurityConfig{
			URLAllowlist: config.URLAllowlistConfig{Enabled: false},
		},
	}})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Type: capability.AccountTypeAPIKey,
		Platform:    capability.PlatformOpenAI,
		Credentials: map[string]any{"base_url": "https://example.com/v1"}},
	}

	req, err := svc.buildUpstreamRequest(c.Request.Context(), c, account, []byte(`{"model":"gpt-5"}`), "token", false, "", false)
	require.NoError(t, err)
	require.Equal(t, "https://example.com/v1/responses/compact", req.URL.String())
}

func TestOpenAIBuildUpstreamRequestOAuthOfficialClientOriginatorCompatibility(t *testing.T) {

	// 上游要求 originator 与最终 User-Agent 首段配套（issue #3901）：
	// originator 一律由最终 UA 推导；推导不出官方身份时整体回退默认 Codex TUI 身份。
	tests := []struct {
		name           string
		userAgent      string
		originator     string
		wantOriginator string
		wantUA         string
	}{
		{name: "official ua pairs originator", userAgent: "Codex Desktop/1.2.3", wantOriginator: "Codex Desktop", wantUA: "Codex Desktop/1.2.3"},
		{
			name:           "mismatched originator repaired from ua",
			userAgent:      "codex-tui/0.140.2 (Mac OS X 14.0; arm64) iTerm (codex-tui; 0.140.2)",
			originator:     "codex_cli_rs",
			wantOriginator: "codex-tui",
			wantUA:         "codex-tui/0.140.2 (Mac OS X 14.0; arm64) iTerm (codex-tui; 0.140.2)",
		},
		{name: "official originator without ua falls back to default identity", originator: "codex_vscode", wantOriginator: openai.CodexDefaultOriginator, wantUA: openai.CodexCLIUserAgent},
		{name: "third-party ua masked to default identity", userAgent: "luna/1.2.0", wantOriginator: openai.CodexDefaultOriginator, wantUA: openai.CodexCLIUserAgent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader([]byte(`{"model":"gpt-5"}`)))
			if tt.userAgent != "" {
				c.Request.Header.Set("User-Agent", tt.userAgent)
			}
			if tt.originator != "" {
				c.Request.Header.Set("originator", tt.originator)
			}

			svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
			account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Type: capability.AccountTypeOAuth,
				Credentials: map[string]any{"chatgpt_account_id": "chatgpt-acc"}},
			}

			isCodexCLI := openai.IsCodexOfficialClientByHeaders(c.GetHeader("User-Agent"), c.GetHeader("originator"))
			req, err := svc.buildUpstreamRequest(c.Request.Context(), c, account, []byte(`{"model":"gpt-5"}`), "token", false, "", isCodexCLI)
			require.NoError(t, err)
			require.Equal(t, tt.wantOriginator, req.Header.Get("originator"))
			require.Equal(t, tt.wantUA, req.Header.Get("User-Agent"))
		})
	}
}

func TestOpenAIBuildUpstreamRequestUsesTLSRouterUpstreamHeaders(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader([]byte(`{"model":"gpt-5"}`)))
	c.Request.Header.Set("User-Agent", "opencode/1.0")
	c.Request.Header.Set("originator", "opencode")

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
		Type: capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"chatgpt_account_id": "chatgpt-acc",
			"user_agent":         "codex-tui/9.8.0 account-fallback",
		}},
	}
	routerMatch := egress.TLSFingerprintRouterMatchResult{
		Matched:                 true,
		RouterID:                9,
		RuleName:                "Codex TUI",
		TLSFingerprintProfileID: 7,
		UpstreamUserAgent:       "codex-tui/9.9.0 (Mac OS X 15.5; arm64) iTerm (codex-tui; 9.9.0)",
		UpstreamOriginator:      "codex-tui",
	}

	req, err := svc.buildUpstreamRequest(c.Request.Context(), c, account, []byte(`{"model":"gpt-5"}`), "token", false, "", false, routerMatch)
	require.NoError(t, err)
	require.Equal(t, routerMatch.UpstreamUserAgent, req.Header.Get("User-Agent"))
	require.Equal(t, routerMatch.UpstreamOriginator, req.Header.Get("originator"))
}

func TestOpenAIBuildUpstreamRequestRouterEmptyUAUsesAccountFallback(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader([]byte(`{"model":"gpt-5"}`)))
	c.Request.Header.Set("User-Agent", "opencode/1.0")

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
		Type: capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"chatgpt_account_id": "chatgpt-acc",
			"user_agent":         "codex-tui/9.8.0 account-fallback",
		}},
	}

	req, err := svc.buildUpstreamRequest(c.Request.Context(), c, account, []byte(`{"model":"gpt-5"}`), "token", false, "", false, egress.TLSFingerprintRouterMatchResult{Matched: true})
	require.NoError(t, err)
	require.Equal(t, "codex-tui/9.8.0 account-fallback", req.Header.Get("User-Agent"))
	require.Equal(t, "codex-tui", req.Header.Get("originator"))
}

// ==================== P1-08 修复：model 替换性能优化测试 ====================

func TestReplaceModelInResponseBody(t *testing.T) {
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})

	tests := []struct {
		name     string
		body     string
		from     string
		to       string
		expected string
	}{
		{
			name:     "替换顶层 model",
			body:     `{"id":"chatcmpl-123","model":"gpt-4o","choices":[]}`,
			from:     "gpt-4o",
			to:       "alias",
			expected: `{"id":"chatcmpl-123","model":"alias","choices":[]}`,
		},
		{
			name:     "model 不匹配不替换",
			body:     `{"id":"chatcmpl-123","model":"gpt-3.5-turbo","choices":[]}`,
			from:     "gpt-4o",
			to:       "alias",
			expected: `{"id":"chatcmpl-123","model":"gpt-3.5-turbo","choices":[]}`,
		},
		{
			name:     "无 model 字段不替换",
			body:     `{"id":"chatcmpl-123","choices":[]}`,
			from:     "gpt-4o",
			to:       "alias",
			expected: `{"id":"chatcmpl-123","choices":[]}`,
		},
		{
			name:     "非法 JSON 返回原值",
			body:     `not json`,
			from:     "gpt-4o",
			to:       "alias",
			expected: `not json`,
		},
		{
			name:     "空 body 返回原值",
			body:     ``,
			from:     "gpt-4o",
			to:       "alias",
			expected: ``,
		},
		{
			name:     "保持嵌套结构不变",
			body:     `{"model":"gpt-4o","usage":{"prompt_tokens":10,"completion_tokens":20},"choices":[{"message":{"role":"assistant","content":"hello"}}]}`,
			from:     "gpt-4o",
			to:       "alias",
			expected: `{"model":"alias","usage":{"prompt_tokens":10,"completion_tokens":20},"choices":[{"message":{"role":"assistant","content":"hello"}}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := svc.replaceModelInResponseBody([]byte(tt.body), tt.from, tt.to)
			require.Equal(t, tt.expected, string(got))
		})
	}
}

func TestExtractOpenAISSEDataLine(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		wantData string
		wantOK   bool
	}{
		{name: "标准格式", line: `data: {"type":"x"}`, wantData: `{"type":"x"}`, wantOK: true},
		{name: "无空格格式", line: `data:{"type":"x"}`, wantData: `{"type":"x"}`, wantOK: true},
		{name: "纯空数据", line: `data:   `, wantData: ``, wantOK: true},
		{name: "非 data 行", line: `event: message`, wantData: ``, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := protocolopenai.ExtractSSEDataLine(tt.line)
			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.wantData, got)
		})
	}
}

func TestExtractOpenAIUsageFromJSONBytes_AcceptsResponseAndChatUsageShapes(t *testing.T) {
	usage, ok := protocolopenai.ExtractOpenAIUsageFromJSONBytes([]byte(`{"id":"resp_1","usage":{"input_tokens":9,"output_tokens":5,"input_tokens_details":{"cached_tokens":2,"cache_write_tokens":4}}}`))
	require.True(t, ok)
	require.Equal(t, 9, usage.InputTokens)
	require.Equal(t, 5, usage.OutputTokens)
	require.Equal(t, 2, usage.CacheReadInputTokens)
	require.Equal(t, 4, usage.CacheCreationInputTokens)

	usage, ok = protocolopenai.ExtractOpenAIUsageFromJSONBytes([]byte(`{"type":"response.completed","response":{"usage":{"prompt_tokens":13,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":4,"cache_creation_tokens":3,"image_tokens":2},"completion_tokens_details":{"image_tokens":1}}}}`))
	require.True(t, ok)
	require.Equal(t, 13, usage.InputTokens)
	require.Equal(t, 2, usage.ImageInputTokens)
	require.Equal(t, 7, usage.OutputTokens)
	require.Equal(t, 4, usage.CacheReadInputTokens)
	require.Equal(t, 3, usage.CacheCreationInputTokens)
	require.Equal(t, 1, usage.ImageOutputTokens)

	usage, ok = protocolopenai.ExtractOpenAIUsageFromJSONBytes([]byte(`{"usage":{"input_tokens":11,"output_tokens":2,"cache_write_input_tokens":6}}`))
	require.True(t, ok)
	require.Equal(t, 6, usage.CacheCreationInputTokens)

	usage, ok = protocolopenai.ExtractOpenAIUsageFromJSONBytes([]byte(`{"usage":{"input_tokens":20,"output_tokens":2,"cache_creation_input_tokens":19,"input_tokens_details":{"cache_write_tokens":7}}}`))
	require.True(t, ok)
	require.Equal(t, 7, usage.CacheCreationInputTokens, "官方嵌套字段应优先于兼容顶层别名")

	usage, ok = protocolopenai.ExtractOpenAIUsageFromJSONBytes([]byte(`{"usage":{"input_tokens":20,"output_tokens":2,"cache_creation_input_tokens":19,"input_tokens_details":{"cache_write_tokens":0}}}`))
	require.True(t, ok)
	require.Zero(t, usage.CacheCreationInputTokens, "官方嵌套字段显式为零时仍应优先于兼容顶层别名")

	usage, ok = protocolopenai.ExtractOpenAIUsageFromJSONBytes([]byte(`{"usage":{"input_tokens":20,"output_tokens":2,"cache_read_input_tokens":19,"input_tokens_details":{"cached_tokens":0}}}`))
	require.True(t, ok)
	require.Zero(t, usage.CacheReadInputTokens, "官方嵌套缓存读取字段显式为零时仍应优先于兼容顶层别名")

	// xAI 可能在可见 output_tokens 外单独返回 reasoning_tokens；仅算术一致的
	// 形态视为独立字段，OpenAI 标准形态已将推理 token 纳入 completion/output。
	usage, ok = protocolopenai.ExtractOpenAIUsageFromJSONBytes([]byte(`{"usage":{"input_tokens":10000,"output_tokens":500,"total_tokens":10800,"output_tokens_details":{"reasoning_tokens":300}}}`))
	require.True(t, ok)
	require.Equal(t, 800, usage.OutputTokens)
	usage, ok = protocolopenai.ExtractOpenAIUsageFromJSONBytes([]byte(`{"usage":{"input_tokens":10000,"output_tokens":500,"total_tokens":10500,"output_tokens_details":{"reasoning_tokens":300}}}`))
	require.True(t, ok)
	require.Equal(t, 500, usage.OutputTokens)
}

func TestExtractOpenAIUsageFromJSONBytes_IncludesGrokReasoningTokens(t *testing.T) {
	usage, ok := protocolopenai.ExtractOpenAIUsageFromJSONBytes([]byte(`{"usage":{"prompt_tokens":32,"completion_tokens":9,"total_tokens":135,"completion_tokens_details":{"reasoning_tokens":94}}}`))
	require.True(t, ok)
	require.Equal(t, 103, usage.OutputTokens, "Grok Chat usage bills visible completion plus reasoning tokens")

	usage, ok = protocolopenai.ExtractOpenAIUsageFromJSONBytes([]byte(`{"usage":{"input_tokens":32,"output_tokens":103,"total_tokens":135,"output_tokens_details":{"reasoning_tokens":94}}}`))
	require.True(t, ok)
	require.Equal(t, 103, usage.OutputTokens, "Responses output_tokens already includes reasoning when total confirms it")

	usage, ok = protocolopenai.ExtractOpenAIUsageFromJSONBytes([]byte(`{"usage":{"input_tokens":32,"output_tokens":9,"total_tokens":135,"output_tokens_details":{"reasoning_tokens":94}}}`))
	require.True(t, ok)
	require.Equal(t, 103, usage.OutputTokens, "Responses detail-only shape is normalized when total exposes the full output")
}

func TestExtractCodexFinalResponse_SampleReplay(t *testing.T) {
	body := strings.Join([]string{
		`event: message`,
		`data: {"type":"response.in_progress","response":{"id":"resp_1"}}`,
		`data: {"type":"response.completed","response":{"id":"resp_1","model":"gpt-4o","usage":{"input_tokens":11,"output_tokens":22,"input_tokens_details":{"cached_tokens":3}}}}`,
		`data: [DONE]`,
	}, "\n")

	finalResp, ok := protocolopenai.ExtractCodexFinalResponse(body)
	require.True(t, ok)
	require.Contains(t, string(finalResp), `"id":"resp_1"`)
	require.Contains(t, string(finalResp), `"input_tokens":11`)
}

func TestHandleSSEToJSON_CompletedEventReturnsJSON(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: &config.Config{}})
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	body := []byte(strings.Join([]string{
		`data: {"type":"response.in_progress","response":{"id":"resp_2"}}`,
		`data: {"type":"response.completed","response":{"id":"resp_2","model":"gpt-4o","usage":{"input_tokens":7,"output_tokens":9,"input_tokens_details":{"cached_tokens":1}}}}`,
		`data: [DONE]`,
	}, "\n"))

	usage, err := svc.responseOutput.SSEAsJSON(context.Background(), resp, c, nil, body, "gpt-4o", "gpt-4o")
	require.NoError(t, err)
	require.NotNil(t, usage)
	require.Equal(t, 7, usage.Usage.InputTokens)
	require.Equal(t, 9, usage.Usage.OutputTokens)
	require.Equal(t, 1, usage.Usage.CacheReadInputTokens)
	// Header 可能由上游 Content-Type 透传；关键是 body 已转换为最终 JSON 响应。
	require.NotContains(t, rec.Body.String(), "event:")
	require.Contains(t, rec.Body.String(), `"id":"resp_2"`)
	require.NotContains(t, rec.Body.String(), "data:")
}

func TestHandleNonStreamingResponse_APIKeyFallsBackToSSEBodyWhenContentTypeIsWrong(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: &config.Config{}})
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"type":"response.output_text.delta","delta":"hel"}`,
			`data: {"type":"response.output_text.delta","delta":"lo"}`,
			`data: {"type":"response.completed","response":{"id":"resp_api_key_sse","object":"response","model":"gpt-5.4","status":"completed","output":[],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}}`,
			`data: [DONE]`,
		}, "\n"))),
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Type: capability.AccountTypeAPIKey}}

	result, err := svc.responseOutput.NonStream(context.Background(), resp, c, account, "gpt-5.4", "gpt-5.4")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 3, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.NotContains(t, rec.Body.String(), "data:")
	require.Equal(t, "resp_api_key_sse", gjson.Get(rec.Body.String(), "id").String())
	require.Equal(t, "hello", gjson.Get(rec.Body.String(), "output.0.content.0.text").String())
}

func TestHandleNonStreamingResponse_OAuthJSONBodyWithDataEventTextKeepsJSONUsage(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", nil)

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: &config.Config{}})
	// Compact JSON 输出文本可以包含 data:/event: 普通字样，但不能因此被误判为 SSE。
	jsonBody := `{"id":"resp_oauth_compact","object":"response","model":"gpt-5.4","status":"completed",` +
		`"output":[{"type":"message","content":[{"type":"output_text",` +
		`"text":"processing data: 1,2,3 then event: click finished"}]}],` +
		`"usage":{"input_tokens":11,"output_tokens":22,"total_tokens":33}}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(jsonBody)),
	}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 146, Type: capability.AccountTypeOAuth}}

	result, err := svc.responseOutput.NonStream(context.Background(), resp, c, account, "gpt-5.4", "gpt-5.4")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 11, result.Usage.InputTokens)
	require.Equal(t, 22, result.Usage.OutputTokens)
	// 响应必须保持原始 JSON，不能经 SSE 路径改写或丢失用量。
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.Equal(t, "resp_oauth_compact", gjson.Get(rec.Body.String(), "id").String())
	require.Equal(t, int64(33), gjson.Get(rec.Body.String(), "usage.total_tokens").Int())
	require.Contains(t, rec.Body.String(), "processing data: 1,2,3 then event: click finished")
}

func TestHandleSSEToJSON_ReconstructsImageGenerationOutputItemDone(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: &config.Config{}})
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	body := []byte(strings.Join([]string{
		`data: {"type":"response.output_item.done","item":{"id":"ig_123","type":"image_generation_call","status":"generating","result":"aGVsbG8=","revised_prompt":"draw a cat","output_format":"png"}}`,
		`data: {"type":"response.completed","response":{"id":"resp_img","model":"gpt-5.4","output":[],"usage":{"input_tokens":7,"output_tokens":9,"output_tokens_details":{"image_tokens":4}}}}`,
		`data: [DONE]`,
	}, "\n"))

	usage, err := svc.responseOutput.SSEAsJSON(context.Background(), resp, c, nil, body, "gpt-5.4", "gpt-5.4")
	require.NoError(t, err)
	require.NotNil(t, usage)
	require.Equal(t, 4, usage.Usage.ImageOutputTokens)
	require.NotContains(t, rec.Body.String(), "data:")
	require.Equal(t, "image_generation_call", gjson.Get(rec.Body.String(), "output.0.type").String())
	require.Equal(t, "completed", gjson.Get(rec.Body.String(), "output.0.status").String())
	require.Equal(t, "aGVsbG8=", gjson.Get(rec.Body.String(), "output.0.result").String())
	require.Equal(t, "draw a cat", gjson.Get(rec.Body.String(), "output.0.revised_prompt").String())
}

func TestHandleSSEToJSON_NoFinalResponseKeepsSSEBody(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: &config.Config{}})
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	body := []byte(strings.Join([]string{
		`data: {"type":"response.in_progress","response":{"id":"resp_3"}}`,
		`data: [DONE]`,
	}, "\n"))

	usage, err := svc.responseOutput.SSEAsJSON(context.Background(), resp, c, nil, body, "gpt-4o", "gpt-4o")
	require.NoError(t, err)
	require.NotNil(t, usage)
	require.Equal(t, 0, usage.Usage.InputTokens)
	require.Contains(t, rec.Header().Get("Content-Type"), "text/event-stream")
	require.Contains(t, rec.Body.String(), `data: {"type":"response.in_progress"`)
}

func TestHandleSSEToJSON_ResponseFailedReturnsFailoverBeforeWrite(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{cfg: &config.Config{}})
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	body := []byte(strings.Join([]string{
		`data: {"type":"response.failed","error":{"message":"upstream rejected request"}}`,
		`data: [DONE]`,
	}, "\n"))

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Type: capability.AccountTypeAPIKey, Platform: capability.PlatformOpenAI}}
	usage, err := svc.responseOutput.SSEAsJSON(context.Background(), resp, c, account, body, "gpt-4o", "gpt-4o")
	require.Nil(t, usage)
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.False(t, c.Writer.Written())
}

func TestOpenAICompatSSEFrameParserResetsEventTypeAtFrameBoundary(t *testing.T) {
	var parser protocolopenai.OpenAICompatSSEFrameParser

	frame, ok := parser.AddLine("event: response.created")
	require.False(t, ok)
	require.Empty(t, frame)

	frame, ok = parser.AddLine(`data: {"response":{"id":"resp_1"}}`)
	require.False(t, ok)
	require.Empty(t, frame)

	frame, ok = parser.AddLine("")
	require.True(t, ok)
	require.Equal(t, "response.created", frame.EventType)
	require.JSONEq(t, `{"response":{"id":"resp_1"}}`, frame.Data)

	frame, ok = parser.AddLine(`data: {"delta":"ok"}`)
	require.False(t, ok)
	require.Empty(t, frame.EventType)

	frame, ok = parser.AddLine("")
	require.True(t, ok)
	require.Empty(t, frame.EventType)
	require.JSONEq(t, `{"delta":"ok"}`, frame.Data)
}
