//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// openAITransportAccountRepoStub 只记录临时不可调度调用，其他仓储方法不应被触达。
type openAITransportAccountRepoStub struct {
	gatewayprovider.ExecutionAccountStore

	tempUnschedCalls []tempUnschedCall
}

// TestClassifyUpstreamTransportError 验证传输层上游错误的持久性分类。
// 持久错误重试同一代理/账号无意义，应摘除并告警；瞬时错误仅切换账号，不摘除当前账号。
func TestClassifyUpstreamTransportError(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		persistent bool
	}{
		// 持久错误：配置、凭据或路由问题，重试同一代理无济于事。
		{"socks5 proxy credential rejected", errors.New(`Post "https://chatgpt.com/backend-api/codex/responses": socks connect tcp 85.255.176.68:12324->chatgpt.com:443: username/password authentication failed`), true},
		{"proxy connection refused", errors.New(`proxyconnect tcp: dial tcp 1.2.3.4:1080: connect: connection refused`), true},
		{"no route to host", errors.New(`dial tcp 1.2.3.4:443: connect: no route to host`), true},
		{"dns resolution failure", errors.New(`dial tcp: lookup proxy.example.com: no such host`), true},
		{"network unreachable", errors.New(`dial tcp 1.2.3.4:443: connect: network is unreachable`), true},

		// 瞬时错误：短暂抖动，切换账号但不摘除当前账号。
		{"client timeout", errors.New(`Post "https://chatgpt.com/...": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`), false},
		{"i/o timeout", errors.New(`dial tcp 1.2.3.4:443: i/o timeout`), false},
		{"connection reset by peer", errors.New(`read tcp 10.0.0.1:5->2.2.2.2:443: read: connection reset by peer`), false},
		{"unexpected eof", errors.New(`unexpected EOF`), false},
		{"broken pipe", errors.New(`write tcp 10.0.0.1:5->2.2.2.2:443: write: broken pipe`), false},

		{"nil error", nil, false},

		// 类型化错误：覆盖 Go 常见的 net.OpError 与 syscall 错误链。
		{
			"ECONNREFUSED via net.OpError",
			&net.OpError{
				Op:  "dial",
				Net: "tcp",
				Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED},
			},
			true,
		},
		// 裸 syscall 错误（errors.Is 会遍历错误链）。
		{"ECONNREFUSED bare", syscall.ECONNREFUSED, true},
		{"EHOSTUNREACH bare", syscall.EHOSTUNREACH, true},
		{"ENETUNREACH bare", syscall.ENETUNREACH, true},

		// IsNotFound=true 的 *net.DNSError 表示持久 DNS 解析失败。
		{
			"DNS not found (IsNotFound=true)",
			&net.DNSError{Err: "no such host", Name: "proxy.example.com", IsNotFound: true},
			true,
		},
		// IsNotFound=false 的 *net.DNSError 表示瞬时 DNS 超时。
		{
			"DNS timeout (IsNotFound=false)",
			&net.DNSError{Err: "i/o timeout", Name: "proxy.example.com", IsTimeout: true},
			false,
		},

		// context.Canceled 表示客户端已离开，不应分类为持久错误。
		{"context.Canceled", context.Canceled, false},
		// context.DeadlineExceeded 表示上游缓慢，不应分类为持久错误。
		{"context.DeadlineExceeded", context.DeadlineExceeded, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := httpclient.ClassifyTransportFailure(tc.err).Persistent
			if got != tc.persistent {
				t.Fatalf("httpclient.ClassifyTransportFailure(%v).Persistent = %v, want %v", tc.err, got, tc.persistent)
			}
		})
	}
}

func (r *openAITransportAccountRepoStub) SetTempUnschedulable(_ context.Context, id int64, until time.Time, reason string) error {
	r.tempUnschedCalls = append(r.tempUnschedCalls, tempUnschedCall{accountID: id, until: until, reason: reason})
	return nil
}

func newOpenAITransportErrTestContext() (*gin.Context, *httptest.ResponseRecorder) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return c, rec
}

func TestHandleOpenAIUpstreamTransportError_PersistentEvictsAndFailsOver(t *testing.T) {
	repo := &openAITransportAccountRepoStub{}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{accountRepo: repo}))
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 4627, Name: "proxy-expired", Platform: capability.PlatformOpenAI}}
	c, rec := newOpenAITransportErrTestContext()

	before := time.Now()
	retErr := svc.handleOpenAIUpstreamTransportError(context.Background(), c, account,
		errors.New(`Post "https://chatgpt.com/backend-api/codex/responses": socks connect tcp 85.255.176.68:12324->chatgpt.com:443: username/password authentication failed`), false)
	after := time.Now()

	var failoverErr *forwardcore.UpstreamFailoverError
	require.True(t, errors.As(retErr, &failoverErr), "persistent error must return *UpstreamFailoverError")
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.Len(t, repo.tempUnschedCalls, 1)
	require.Equal(t, int64(4627), repo.tempUnschedCalls[0].accountID)
	require.Contains(t, repo.tempUnschedCalls[0].reason, "authentication failed")
	require.True(t, repo.tempUnschedCalls[0].until.After(before.Add(openAITransportErrorTempUnschedDuration-time.Second)))
	require.True(t, repo.tempUnschedCalls[0].until.Before(after.Add(openAITransportErrorTempUnschedDuration+time.Second)))
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.Equal(t, 0, rec.Body.Len())
}

func TestHandleOpenAIUpstreamTransportError_TransientFailsOverWithoutEviction(t *testing.T) {
	repo := &openAITransportAccountRepoStub{}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{accountRepo: repo}))
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 99, Name: "flaky", Platform: capability.PlatformOpenAI}}
	c, rec := newOpenAITransportErrTestContext()

	err := svc.handleOpenAIUpstreamTransportError(context.Background(), c, account,
		errors.New(`Post "https://chatgpt.com/...": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`), false)

	var failoverErr *forwardcore.UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr), "transient error must return *UpstreamFailoverError")
	require.Empty(t, repo.tempUnschedCalls)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.Equal(t, 0, rec.Body.Len())
}

func TestHandleOpenAIUpstreamTransportError_ContextCanceledNoFailover(t *testing.T) {
	repo := &openAITransportAccountRepoStub{}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{accountRepo: repo}))
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 77, Name: "healthy", Platform: capability.PlatformOpenAI}}
	c, rec := newOpenAITransportErrTestContext()

	err := svc.handleOpenAIUpstreamTransportError(context.Background(), c, account, context.Canceled, false)

	var failoverErr *forwardcore.UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr), "context.Canceled must not return *UpstreamFailoverError")
	require.Empty(t, repo.tempUnschedCalls)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.Equal(t, 0, rec.Body.Len())
}

func TestHandleOpenAIUpstreamTransportError_WrappedContextCanceledNoFailover(t *testing.T) {
	repo := &openAITransportAccountRepoStub{}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{accountRepo: repo}))
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 78, Name: "healthy2", Platform: capability.PlatformOpenAI}}
	c, _ := newOpenAITransportErrTestContext()

	err := svc.handleOpenAIUpstreamTransportError(context.Background(), c, account, fmt.Errorf("http request failed: %w", context.Canceled), false)

	var failoverErr *forwardcore.UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr), "wrapped context.Canceled must not return *UpstreamFailoverError")
	require.Empty(t, repo.tempUnschedCalls)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

// TestHandleOpenAIUpstreamTransportError_RecordsOllamaActivityOnly 验证传输错误只记录 Ollama Cloud 账号活动。
func TestHandleOpenAIUpstreamTransportError_RecordsOllamaActivityOnly(t *testing.T) {
	deferred, activity := newDeferredActivityRecorder(t)
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		accountRepo:     &openAITransportAccountRepoStub{},
		deferredService: deferred,
	}))
	ollama := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 501, Name: "ollama-cloud", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "k-ollama", "base_url": "https://ollama.com"}},
	}
	other := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 502, Name: "openai-official", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "k-openai", "base_url": "https://api.openai.com"}},
	}
	c, _ := newOpenAITransportErrTestContext()

	_ = svc.handleOpenAIUpstreamTransportError(context.Background(), c, ollama, errors.New("connection reset"), false)
	_ = svc.handleOpenAIUpstreamTransportError(context.Background(), c, other, errors.New("connection reset"), false)

	require.NoError(t, deferred.Stop())
	_, ok := activity.Load(int64(501))
	require.True(t, ok, "Ollama Cloud transport error must schedule last_used activity")
	_, ok = activity.Load(int64(502))
	require.False(t, ok, "non-Ollama transport error must not schedule Ollama activity")
}

// TestHandleOpenAIUpstreamTransportError_ContextCanceledSkipsOllamaActivity 验证客户端取消不会记录活动。
func TestHandleOpenAIUpstreamTransportError_ContextCanceledSkipsOllamaActivity(t *testing.T) {
	deferred, activity := newDeferredActivityRecorder(t)
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		accountRepo:     &openAITransportAccountRepoStub{},
		deferredService: deferred,
	}))
	ollama := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 503, Name: "ollama-canceled", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "k-ollama", "base_url": "https://ollama.com"}},
	}
	c, _ := newOpenAITransportErrTestContext()

	err := svc.handleOpenAIUpstreamTransportError(context.Background(), c, ollama, context.Canceled, false)

	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, deferred.Stop())
	_, ok := activity.Load(int64(503))
	require.False(t, ok, "context.Canceled is client disconnect before a fault; do not count as Ollama activity")
}

// TestHandleOpenAIAccountUpstreamError_RecordsOllamaActivityOnly 验证非 2xx 响应只记录 Ollama Cloud 账号活动。
func TestHandleOpenAIAccountUpstreamError_RecordsOllamaActivityOnly(t *testing.T) {
	deferred, activity := newDeferredActivityRecorder(t)
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{deferredService: deferred})
	ollama := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 504, Name: "ollama-429", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "k-ollama", "base_url": "https://ollama.com"}},
	}
	other := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 505, Name: "openai-429", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "k-openai", "base_url": "https://api.openai.com"}},
	}

	_ = gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.responseOutput.Health, ollama, http.StatusTooManyRequests, http.Header{}, []byte(`{"error":{"message":"rate"}}`), false, "gpt-test").StopScheduling
	_ = gatewayprovider.ApplyOpenAIResponseHealth(context.Background(), svc.responseOutput.Health, other, http.StatusTooManyRequests, http.Header{}, []byte(`{"error":{"message":"rate"}}`), false, "gpt-test").StopScheduling

	require.NoError(t, deferred.Stop())
	_, ok := activity.Load(int64(504))
	require.True(t, ok, "Ollama Cloud non-2xx must schedule last_used activity")
	_, ok = activity.Load(int64(505))
	require.False(t, ok, "non-Ollama non-2xx must not schedule Ollama activity")
}

// 传输错误测试使用的最小状态写入观测。
type tempUnschedCall struct {
	accountID int64
	until     time.Time
	reason    string
}
