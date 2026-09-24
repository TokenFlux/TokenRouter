package messageforward

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/infra/timingwheel"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/stretchr/testify/require"
)

// 私有交换契约只记录实际输出和活动写入；完整 HTTP envelope 由公开入口测试覆盖。
type privateHTTPBoundary struct {
	HTTPBoundary
	Request *http.Request
	Writer  *privateOutputSink
}

type privateOutputSink struct {
	recorder *httptest.ResponseRecorder
	written  bool
}

func (s *privateOutputSink) Begin(head upstream.OutputHead) error {
	for name, values := range head.Header {
		s.recorder.Header()[name] = append([]string(nil), values...)
	}
	s.recorder.WriteHeader(head.Status)
	s.written = true
	return nil
}
func (s *privateOutputSink) Emit(event upstream.OutputEvent) error {
	if len(event.Data) > 0 {
		s.written = true
		if _, err := s.recorder.Write(event.Data); err != nil {
			return err
		}
	}
	if event.Flush {
		s.recorder.Flush()
	}
	return nil
}
func (s *privateOutputSink) Written() bool { return s.written }
func newPrivateHTTPFixture(rec *httptest.ResponseRecorder) (*privateHTTPBoundary, error) {
	return &privateHTTPBoundary{Writer: &privateOutputSink{recorder: rec}}, nil
}
func (b *privateHTTPBoundary) Present() bool        { return true }
func (b *privateHTTPBoundary) RequestPresent() bool { return b.Request != nil }
func (b *privateHTTPBoundary) RequestHeaders() http.Header {
	if b.Request == nil {
		return http.Header{}
	}
	return b.Request.Header
}
func (b *privateHTTPBoundary) HasHeaderFilter() bool     { return false }
func (b *privateHTTPBoundary) Sink() upstream.OutputSink { return b.Writer }
func (b *privateHTTPBoundary) Written() bool             { return b.Writer.Written() }
func (b *privateHTTPBoundary) Size() int {
	if !b.Written() {
		return -1
	}
	return b.Writer.recorder.Body.Len()
}
func (b *privateHTTPBoundary) ServiceTier() string          { return "" }
func (b *privateHTTPBoundary) MarkPassthrough()             {}
func (b *privateHTTPBoundary) Observe(forwardcore.Notice)   {}
func (b *privateHTTPBoundary) SetError(int, string, string) {}
func (b *privateHTTPBoundary) MatchRule(string, int, []byte) *errorpolicy.ErrorPassthroughRule {
	return nil
}
func (b *privateHTTPBoundary) Commit() { b.Writer.written = true }
func (b *privateHTTPBoundary) MessageError(status int, _, _ string) {
	b.Writer.recorder.WriteHeader(status)
	b.Writer.written = true
}
func (b *privateHTTPBoundary) RawError(status int, body []byte) {
	b.Writer.recorder.WriteHeader(status)
	_, _ = b.Writer.recorder.Write(body)
	b.Writer.written = true
}
func (b *privateHTTPBoundary) WriteHeaders(dst, src http.Header, _ bool) {
	for key, values := range src {
		dst[key] = append([]string(nil), values...)
	}
}
func (b *privateHTTPBoundary) ReadResponseBody(reader io.Reader, limit int64, _ BodyKind) ([]byte, error) {
	return httpclient.ReadResponseBodyLimited(reader, limit)
}
func newPrivateRuntimeFixture(options *Options, deps Dependencies, _ any) *Runtime {
	value := Options{ResponseReadLimit: 128 * 1024 * 1024}
	if options != nil {
		value = *options
	}
	return NewRuntime(deps, value)
}
func newPrivateHealthFixture() *accountprovider.UpstreamHealth {
	return gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{})
}

// 参数只投影为原生输入，凭据校验和交换全部由待测实现执行。
func passthroughFixture(runtime *Runtime, ctx context.Context, output HTTPBoundary, target *gatewayprovider.ExecutionAccount, body []byte, model, original string, stream bool, started time.Time) (*forwardcore.Result, error) {
	return runtime.passthrough(ctx, output, target, forwardcore.APIKeyInput{Body: body, RequestModel: model, OriginalModel: original, RequestStream: stream, StartTime: started})
}

type anthropicHTTPUpstreamRecorder struct {
	lastReq  *http.Request
	lastBody []byte
	resp     *http.Response
	err      error
}

func newAnthropicAPIKeyAccountForTest() *gatewayprovider.ExecutionAccount {
	return &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 201,
		Name:        "anthropic-apikey-pass-test",
		Platform:    capability.PlatformAnthropic,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "upstream-anthropic-key",
			"base_url": "https://api.anthropic.com",
		},
		Extra: map[string]any{
			"anthropic_passthrough": true,
		},
		Status:      billing.StatusActive,
		Schedulable: true},
	}
}

func (u *anthropicHTTPUpstreamRecorder) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	u.lastReq = req
	if req != nil && req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		u.lastBody = b
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(b))
	}
	if u.err != nil {
		return nil, u.err
	}
	return u.resp, nil
}

func (u *anthropicHTTPUpstreamRecorder) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}

// 网关行为从最终写入端口核对，避免测试穿透队列内部表示。
type deferredActivityRepository struct {
	updates sync.Map
}

func (r *deferredActivityRepository) BatchUpdateLastUsed(_ context.Context, updates map[int64]time.Time) error {
	for id, ts := range updates {
		r.updates.Store(id, ts)
	}
	return nil
}
func newDeferredActivityRecorder(t *testing.T) (*accountcore.DeferredService, *sync.Map) {
	t.Helper()
	wheel := timingwheel.New()
	repo := &deferredActivityRepository{}
	svc := accountcore.NewDeferredService(repo, wheel, accountcore.DeferredOptions{Interval: time.Second, Now: time.Now, Observe: log.Printf})
	t.Cleanup(func() { require.NoError(t, svc.Stop()) })
	return svc, &repo.updates
}
