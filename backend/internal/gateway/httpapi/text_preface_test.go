package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/gateway/execution"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	protocolgemini "github.com/TokenFlux/TokenRouter/internal/protocol/gemini"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// 未实现的端口若被意外调用会立即失败，确保拒绝路径不进入调度或存储。
type prefaceBackend struct {
	CompatibleTextBackend
	key       *apikey.APIKey
	events    []string
	block     bool
	policyErr error
	call      CompatibleTextCall
}

func (p *prefaceBackend) Access(*gin.Context) (*apikey.APIKey, bool) { return p.key, p.key != nil }
func (p *prefaceBackend) ObserveRequest(_ *gin.Context, m string, _ bool) {
	p.events = append(p.events, "request:"+m)
}
func (p *prefaceBackend) ObserveEndpoint(*gin.Context, bool) { p.events = append(p.events, "endpoint") }
func (p *prefaceBackend) ApplyUserPromptReplacement(_ context.Context, b []byte, protocol string) []byte {
	p.events = append(p.events, "prompt:"+protocol)
	return b
}
func (p *prefaceBackend) Reasoning(_ *gin.Context, _ *apikey.APIKey, b []byte) ([]byte, bool, error) {
	p.events = append(p.events, "reasoning")
	return b, false, p.policyErr
}
func (p *prefaceBackend) PolicyDenied(*gin.Context) { p.events = append(p.events, "denied") }
func (p *prefaceBackend) Plan(_ context.Context, k *apikey.APIKey, m string) routing.RoutePlan {
	p.events = append(p.events, "plan")
	return routing.Plan(routing.PlanInput{RequestedModel: m, GroupID: k.GroupID, Channel: routing.ChannelMappingResult{Mapped: true, MappedModel: "mapped-model"}})
}
func (p *prefaceBackend) BindPlan(*gin.Context, routing.RoutePlan) {
	p.events = append(p.events, "bind")
}
func (p *prefaceBackend) ImageIntent(_ *apikey.APIKey, _ string, b []byte, _ routing.ChannelMappingResult) ([]byte, bool) {
	p.events = append(p.events, "image")
	return b, false
}
func (p *prefaceBackend) ChatImageModel(string, routing.ChannelMappingResult) bool {
	p.events = append(p.events, "image")
	return false
}
func (p *prefaceBackend) Moderate(_ *gin.Context, _ *zap.Logger, _ *apikey.APIKey, _ authctx.AuthSubject, protocol, _ string, _ []byte) *moderation.Decision {
	p.events = append(p.events, "moderate:"+protocol)
	return &moderation.Decision{Blocked: p.block, Message: "blocked"}
}
func (p *prefaceBackend) BindErrors(*gin.Context)         { p.events = append(p.events, "errors") }
func (p *prefaceBackend) AuthLatency(*gin.Context, int64) {}
func (p *prefaceBackend) Eligibility(ctx context.Context, _ *apikey.APIKey, _ *billing.UserSubscription) error {
	if scheduler.RequestLease(ctx) == nil {
		return errors.New("missing request lease")
	}
	p.events = append(p.events, "eligibility")
	return nil
}
func (p *prefaceBackend) Isolate(context.Context, *apikey.APIKey, int64, string) error { return nil }
func (p *prefaceBackend) Execution(c *gin.Context, call CompatibleTextCall, _ CompatibleTextKind) textflow.MessagePorts {
	p.call = call
	p.events = append(p.events, "execution")
	return &prefaceLoop{ctx: c.Request.Context()}
}
func (p *prefaceBackend) FailoverObservation(context.Context, string, map[string]any) {}

// 前置测试在循环接管后结束，平台转发由既有账号循环测试覆盖。
type prefaceLoop struct {
	textflow.MessagePorts
	ctx context.Context
}

func (p *prefaceLoop) Context() context.Context { return p.ctx }
func (p *prefaceLoop) Begin()                   {}
func (p *prefaceLoop) Finish(bool)              {}
func (p *prefaceLoop) PrepareAttempt() bool     { return false }
func prefaceContext(body string) (*gin.Context, *httptest.ResponseRecorder) {

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/test", strings.NewReader(body))
	c.Set(authctx.ContextKeyUser, authctx.AuthSubject{UserID: 42, Concurrency: 0})
	return c, w
}
func prefaceKey() *apikey.APIKey {
	id := int64(7)
	return &apikey.APIKey{ID: 9, GroupID: &id, Group: &routing.Group{ID: id, Platform: "gemini"}}
}
func prefaceConcurrency() *ConcurrencyHelper {
	return NewConcurrencyHelper(scheduler.NewConcurrencyService(nil), SSEPingFormatNone, 0)
}
func TestCompatiblePrefaceOrderingAndEnvelope(t *testing.T) {
	for _, responses := range []bool{true, false} {
		name, protocol, field := "chat", "chat_completions", "type"
		if responses {
			name, protocol, field = "responses", "openai_responses", "code"
		}
		t.Run(name, func(t *testing.T) {
			p := &prefaceBackend{key: prefaceKey(), block: true}
			h := NewCompatibleTextHandler(MessagesHTTPOptions{MaxBodyBytes: 1024}, p, p, nil, p)
			c, w := prefaceContext(`{"model":"original","stream":true}`)
			if responses {
				h.Responses(c)
			} else {
				h.ChatCompletions(c)
			}
			prefix := []string{"request:", "prompt:" + protocol, "reasoning"}
			if responses {
				prefix = append(prefix, "request:original", "endpoint", "plan", "bind", "image")
			} else {
				prefix = append(prefix, "plan", "bind", "image", "request:original", "endpoint")
			}
			moderationProtocol := moderation.ContentModerationProtocolOpenAIChat
			if responses {
				moderationProtocol = moderation.ContentModerationProtocolOpenAIResponses
			}
			require.Equal(t, append(prefix, "moderate:"+moderationProtocol), p.events)
			require.Contains(t, w.Body.String(), `"`+field+`":"content_policy_violation"`)
			require.NotEqual(t, http.StatusOK, w.Code)
		})
	}
}
func TestCompatiblePrefaceValidationBeforeScheduling(t *testing.T) {
	for _, tc := range []struct {
		name, body, message string
		limit               int64
		policy              bool
	}{
		{"empty", "", "Request body is empty", 1024, false},
		{"json", "{", "Failed to parse request body", 1024, false},
		{"model", `{"input":"text"}`, "model is required", 1024, false},
		{"stream", `{"model":"m","stream":"true"}`, InvalidStreamFieldTypeMessage, 1024, false},
		{"policy", `{"model":"m","stream":"true"}`, "policy rejected", 1024, true},
		{"limit", `{"model":"m"}`, "Request body too large, limit is 4B", 4, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, responses := range []bool{true, false} {
				p := &prefaceBackend{key: prefaceKey()}
				if tc.policy {
					p.policyErr = errors.New("policy rejected")
				}
				h := NewCompatibleTextHandler(MessagesHTTPOptions{MaxBodyBytes: tc.limit}, p, p, nil, p)
				c, w := prefaceContext(tc.body)
				if responses {
					h.Responses(c)
				} else {
					h.ChatCompletions(c)
				}
				require.Contains(t, w.Body.String(), tc.message)
				require.NotContains(t, p.events, "plan")
			}
		})
	}
}
func TestCompatiblePrefacePassesRequestLeaseAndOriginalModel(t *testing.T) {
	for _, responses := range []bool{true, false} {
		p := &prefaceBackend{key: prefaceKey()}
		h := NewCompatibleTextHandler(MessagesHTTPOptions{MaxBodyBytes: 1024}, p, p, prefaceConcurrency(), p)
		c, w := prefaceContext(`{"model":"original","input":"hello","messages":[{"role":"user","content":"hi"}]}`)
		if responses {
			h.Responses(c)
		} else {
			h.ChatCompletions(c)
		}
		require.Equal(t, 200, w.Code, w.Body.String())
		require.Equal(t, "original", p.call.Model)
		require.Equal(t, "mapped-model", p.call.Mapping.MappedModel)
		require.NotNil(t, scheduler.RequestLease(p.call.RequestContext))
		require.Equal(t, []string{"eligibility", "execution"}, p.events[len(p.events)-2:])
	}
}

// Gemini 与兼容入口的审核顺序不同，单独实现端口避免套用兼容链。
type geminiPrefaceBackend struct {
	GeminiNativeBackend
	base  *prefaceBackend
	call  GeminiNativeCall
	bound int64
}

func (p *geminiPrefaceBackend) Access(c *gin.Context) (*apikey.APIKey, bool) { return p.base.Access(c) }
func (p *geminiPrefaceBackend) HasForcedPlatform(*gin.Context) bool          { return false }
func (p *geminiPrefaceBackend) SafeModelSegment(string) bool                 { return true }
func (p *geminiPrefaceBackend) ObserveRequest(c *gin.Context, m string, s bool) {
	p.base.ObserveRequest(c, m, s)
}
func (p *geminiPrefaceBackend) ObserveEndpoint(c *gin.Context, s bool) { p.base.ObserveEndpoint(c, s) }
func (p *geminiPrefaceBackend) Moderate(c *gin.Context, l *zap.Logger, k *apikey.APIKey, a authctx.AuthSubject, m string, b []byte) *moderation.Decision {
	return p.base.Moderate(c, l, k, a, "gemini", m, b)
}
func (p *geminiPrefaceBackend) Plan(c context.Context, k *apikey.APIKey, m string) routing.RoutePlan {
	return p.base.Plan(c, k, m)
}
func (p *geminiPrefaceBackend) BindPlan(c *gin.Context, r routing.RoutePlan) { p.base.BindPlan(c, r) }
func (p *geminiPrefaceBackend) BindErrors(c *gin.Context)                    { p.base.BindErrors(c) }
func (p *geminiPrefaceBackend) Eligibility(c context.Context, k *apikey.APIKey, s *billing.UserSubscription) error {
	return p.base.Eligibility(c, k, s)
}
func (p *geminiPrefaceBackend) Isolate(context.Context, *apikey.APIKey, int64, string) error {
	return nil
}
func (p *geminiPrefaceBackend) CachedSession(context.Context, *int64, string) (int64, error) {
	return p.bound, nil
}
func (p *geminiPrefaceBackend) Prefetch(_ *gin.Context, _, _ int64) {
	p.base.events = append(p.base.events, "prefetch")
}
func (p *geminiPrefaceBackend) DigestChain(*protocolgemini.GeminiRequest) string { return "digest" }
func (p *geminiPrefaceBackend) PrefixHash(int64, int64, string, string, string, string) string {
	return "prefix"
}
func (p *geminiPrefaceBackend) FindSession(context.Context, int64, string, string) (string, int64, string, bool) {
	return "session-id", 87, "previous", true
}
func (p *geminiPrefaceBackend) DigestSessionKey(string, string) string { return "digest-session" }
func (p *geminiPrefaceBackend) BindSticky(context.Context, *int64, string, int64) error {
	p.base.events = append(p.base.events, "sticky")
	return nil
}
func (p *geminiPrefaceBackend) Execution(c *gin.Context, call GeminiNativeCall) textflow.MessagePorts {
	p.call = call
	return &prefaceLoop{ctx: c.Request.Context()}
}
func (p *geminiPrefaceBackend) FailoverObservation(context.Context, string, map[string]any) {}
func TestGeminiNativePrefacePreservesModerationBeforeMapping(t *testing.T) {
	base := &prefaceBackend{key: prefaceKey(), block: true}
	p := &geminiPrefaceBackend{base: base}
	h := NewGeminiNativeHandler(GeminiNativeOptions{}, p, base, nil, nil, p)
	c, w := prefaceContext(`not-json`)
	c.Params = gin.Params{{Key: "modelAction", Value: "/m:generateContent"}}
	h.GeminiV1BetaModels(c)
	require.Equal(t, []string{"request:m", "endpoint", "prompt:gemini", "moderate:gemini"}, base.events)
	require.Contains(t, w.Body.String(), "blocked")
}
func TestGeminiNativePrefaceCarriesSignatureAndDigestState(t *testing.T) {
	for _, bound := range []int64{65, 0} {
		t.Run(map[bool]string{true: "sticky", false: "digest"}[bound > 0], func(t *testing.T) {
			base := &prefaceBackend{key: prefaceKey()}
			p := &geminiPrefaceBackend{base: base, bound: bound}
			h := NewGeminiNativeHandler(GeminiNativeOptions{}, p, base, prefaceConcurrency(), func() string { t.Fatal("matched session must not create id"); return "" }, p)
			c, w := prefaceContext(`{"contents":[{"role":"user","parts":[{"text":"/.gemini/tmp/` + strings.Repeat("a", 64) + `"}]}]}`)
			c.Params = gin.Params{{Key: "modelAction", Value: "/original:streamGenerateContent"}}
			h.GeminiV1BetaModels(c)
			require.Equal(t, 200, w.Code, w.Body.String())
			require.Equal(t, "original", p.call.Model)
			require.Equal(t, "mapped-model", p.call.ModelName)
			require.True(t, p.call.Stream)
			require.True(t, p.call.HasBoundSession)
			want := bound
			if want == 0 {
				want = 87
				require.Equal(t, "previous", p.call.MatchedDigestChain)
				require.Equal(t, "session-id", p.call.SessionUUID)
				require.Contains(t, base.events, "sticky")
			}
			require.Equal(t, want, p.call.SignatureState.BoundAccountID)
			require.Equal(t, want, p.call.BoundAccountID)
			require.Equal(t, bound == 0, p.call.UseDigestFallback)
		})
	}
}

func (p *prefaceBackend) Execute(_ context.Context, in execution.Request, _ upstream.OutputSink) (execution.ExecutionResult, error) {
	p.call = CompatibleTextCall{MessagesCall: MessagesCall{Key: in.Funding.Key, Subscription: in.Funding.Subscription, Parsed: in.Text.Parsed, Body: in.Body, Model: in.Model, Mapping: in.Text.Mapping}, RequestContext: in.Text.SelectionContext}
	p.events = append(p.events, "execution")
	return execution.ExecutionResult{}, nil
}
func (p *geminiPrefaceBackend) Execute(_ context.Context, in execution.Request, _ upstream.OutputSink) (execution.ExecutionResult, error) {
	p.call = GeminiNativeCall{MessagesCall: MessagesCall{Key: in.Funding.Key, Model: in.Model, Stream: in.Stream, HasBoundSession: in.Text.HasBoundSession, BoundAccountID: in.Text.BoundAccountID}, ModelName: in.Text.GeminiModel, SignatureState: in.Text.SignatureState, MatchedDigestChain: in.Text.MatchedDigestChain, SessionUUID: in.Text.SessionUUID, UseDigestFallback: in.Text.UseDigestFallback}
	return execution.ExecutionResult{}, nil
}
