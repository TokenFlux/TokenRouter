// 入口迁移契约固定 HTTP 错误优先级、等待后二次权益检查及请求快照取时点。
package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/execution"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// 嵌入实际投影适配，仅替换会发起用例 I/O 的端口；HTTP 读取和错误输出使用真实实现。
type openAITextEntryProbe struct {
	openAITextHTTPBackend
	events                          []string
	key                             *apikey.APIKey
	allowed, owned, image, canceled bool
	eligibility                     error
	rewrite                         []byte
	decision                        *moderation.Decision
	call                            *gatewayhttp.OpenAITextCall
}

func (p *openAITextEntryProbe) mark(s string) { p.events = append(p.events, s) }
func (p *openAITextEntryProbe) Access(*gin.Context) (*apikey.APIKey, bool) {
	p.mark("access")
	return p.key, p.key != nil
}
func (p *openAITextEntryProbe) Dependencies(*gin.Context, *zap.Logger) bool {
	p.mark("dependencies")
	return true
}
func (p *openAITextEntryProbe) AllowsMessages(*apikey.APIKey) bool {
	p.mark("messages-policy")
	return p.allowed
}
func (p *openAITextEntryProbe) NormalizeCompact(_ *gin.Context, _ *zap.Logger, body []byte) ([]byte, bool) {
	p.mark("compact-normalize")
	return body, true
}
func (p *openAITextEntryProbe) StartCompact(*gin.Context, time.Duration) func() {
	p.mark("keepalive-start")
	return func() { p.mark("keepalive-stop") }
}
func (p *openAITextEntryProbe) Reasoning(_ *gin.Context, _ *apikey.APIKey, body []byte) ([]byte, bool, error) {
	p.mark("reasoning")
	if p.rewrite != nil {
		return p.rewrite, true, nil
	}
	return body, false, nil
}
func (p *openAITextEntryProbe) MessageReasoning(*gin.Context, *apikey.APIKey, []byte) {
	p.mark("message-reasoning")
}
func (p *openAITextEntryProbe) ApplyUserPromptReplacement(_ context.Context, body []byte, format string) []byte {
	p.mark("prompt:" + format)
	return body
}
func (p *openAITextEntryProbe) ValidateOwner(context.Context, int64, string, int64, int64) (bool, error) {
	p.mark("owner-check")
	return p.owned, nil
}
func (p *openAITextEntryProbe) SetOwner(c *gin.Context, u, k int64) {
	p.mark("owner-set")
	p.openAITextHTTPBackend.SetOwner(c, u, k)
}
func (p *openAITextEntryProbe) Moderate(*gin.Context, *zap.Logger, *apikey.APIKey, authctx.AuthSubject, protocol.ProtocolID, string, []byte) *moderation.Decision {
	p.mark("moderate")
	return p.decision
}
func (p *openAITextEntryProbe) Plan(context.Context, *apikey.APIKey, string) routing.RoutePlan {
	p.mark("plan")
	return routing.RoutePlan{}
}
func (p *openAITextEntryProbe) ChatImageModel(string, routing.ChannelMappingResult) bool {
	p.mark("chat-model")
	return p.image
}
func (p *openAITextEntryProbe) ImageIntent(model string, body []byte, _ routing.ChannelMappingResult, _ string) ([]byte, string, bool) {
	p.mark("image-intent")
	return body, model, false
}
func (p *openAITextEntryProbe) UserSlot(c *gin.Context, _ int64, _ int, _ bool, _ *bool, _ *zap.Logger) (func(), bool) {
	p.mark("user-slot")
	if p.canceled {
		c.Status(499)
		return nil, false
	}
	return func() { p.mark("user-release") }, true
}
func (p *openAITextEntryProbe) Eligibility(context.Context, *apikey.APIKey, *billing.UserSubscription) error {
	p.mark("eligibility")
	return p.eligibility
}
func (p *openAITextEntryProbe) SessionHash(_ *gin.Context, kind gatewayhttp.OpenAISessionInput, _ []byte) string {
	if kind == gatewayhttp.OpenAIExplicitSession {
		return "explicit"
	}
	return "session"
}
func (p *openAITextEntryProbe) RejectCyber(*gin.Context, *apikey.APIKey, []byte, string, protocol.ProtocolID) bool {
	p.mark("cyber-check")
	return false
}
func (p *openAITextEntryProbe) Isolate(context.Context, *apikey.APIKey, int64, string, string) error {
	p.mark("isolation")
	return nil
}
func (p *openAITextEntryProbe) GuardianContext(ctx context.Context, _ *gin.Context, _ []byte, _ string) context.Context {
	p.mark("guardian")
	return ctx
}
func (p *openAITextEntryProbe) MappedBodyCache(body []byte) func(bool, string) []byte {
	p.mark("mapped-cache")
	return func(bool, string) []byte { return body }
}
func (p *openAITextEntryProbe) MessageAccountModel(_ context.Context, _ *apikey.APIKey, model string) string {
	return model
}
func (p *openAITextEntryProbe) Execution(_ *gin.Context, call gatewayhttp.OpenAITextCall) textflow.ResponsePorts {
	p.mark("execution")
	p.call = &call
	return openAITextNoAttempt{}
}

// 不选择账号的终点证明前置组合已进入统一循环，未额外发起供应商请求。
type openAITextNoAttempt struct{ textflow.ResponsePorts }

func (openAITextNoAttempt) CanAttempt() bool { return false }

type openAITextReadProbe struct {
	io.Reader
	reads int
}

func (b *openAITextReadProbe) Read(p []byte) (int, error) { b.reads++; return b.Reader.Read(p) }
func (*openAITextReadProbe) Close() error                 { return nil }

func newOpenAITextEntryProbe(t *testing.T, body string) (*openAITextEntryProbe, *gatewayhttp.OpenAITextHandler, *gin.Context, *httptest.ResponseRecorder, *openAITextReadProbe) {
	t.Helper()
	c, w := func() (*gin.Context, *httptest.ResponseRecorder) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		return c, w
	}()
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	reader := &openAITextReadProbe{Reader: strings.NewReader(body)}
	c.Request.Body = reader
	c.Set(authctx.ContextKeyUser, authctx.AuthSubject{UserID: 7, Concurrency: 2})
	p := &openAITextEntryProbe{openAITextHTTPBackend: openAITextHTTPBackend{h: &OpenAIGatewayHandler{}}, key: &apikey.APIKey{ID: 9, UserID: 7}, allowed: true, owned: true}
	h := gatewayhttp.NewOpenAITextHandler(gatewayhttp.OpenAITextOptions{MaxBodyBytes: 1024 * 1024, MaxSwitches: 2}, p, p, p)
	return p, h, c, w, reader
}
func assertOpenAITextEventBefore(t *testing.T, events []string, a, b string) {
	t.Helper()
	ai, bi := -1, -1
	for i, event := range events {
		if event == a {
			ai = i
		}
		if event == b {
			bi = i
		}
	}
	require.GreaterOrEqual(t, ai, 0, events)
	require.Greater(t, bi, ai, events)
}
func TestOpenAITextHTTPPreludeErrorOrder(t *testing.T) {
	t.Run("credential rejection does not read body", func(t *testing.T) {
		p, h, c, w, r := newOpenAITextEntryProbe(t, `broken`)
		p.key = nil
		h.Responses(c)
		require.Equal(t, 401, w.Code)
		require.Zero(t, r.reads)
	})
	t.Run("messages policy precedes body and dependencies", func(t *testing.T) {
		p, h, c, w, r := newOpenAITextEntryProbe(t, `broken`)
		p.allowed = false
		h.Messages(c)
		require.Equal(t, 403, w.Code)
		require.Zero(t, r.reads)
		require.NotContains(t, p.events, "dependencies")
	})
	t.Run("responses keepalive begins before JSON validation", func(t *testing.T) {
		p, h, c, w, _ := newOpenAITextEntryProbe(t, `broken`)
		h.Responses(c)
		require.Equal(t, 400, w.Code)
		require.Contains(t, w.Body.String(), "Failed to parse request body")
		require.Contains(t, p.events, "keepalive-start")
		require.Equal(t, "keepalive-stop", p.events[len(p.events)-1])
		require.NotContains(t, p.events, "reasoning")
	})
	t.Run("owner denial precedes moderation and slot", func(t *testing.T) {
		p, h, c, w, _ := newOpenAITextEntryProbe(t, `{"model":"gpt-5","previous_response_id":"resp_private","input":"hi"}`)
		p.owned = false
		h.Responses(c)
		require.Equal(t, 400, w.Code)
		require.Contains(t, w.Body.String(), "not available for this user")
		require.NotContains(t, p.events, "moderate")
		require.NotContains(t, p.events, "user-slot")
	})
	t.Run("chat channel image restriction precedes moderation", func(t *testing.T) {
		p, h, c, w, _ := newOpenAITextEntryProbe(t, `{"model":"gpt-image-1","messages":[]}`)
		p.image = true
		h.ChatCompletions(c)
		require.Equal(t, 400, w.Code)
		require.NotContains(t, p.events, "moderate")
		require.Contains(t, p.events, "plan")
	})
	t.Run("messages keeps permissive stream while chat rejects", func(t *testing.T) {
		p, h, c, w, _ := newOpenAITextEntryProbe(t, `{"model":"gpt-5","stream":"true","messages":[]}`)
		h.Messages(c)
		require.Equal(t, 200, w.Code)
		require.NotNil(t, p.call)
		require.True(t, p.call.Stream)
		_, h, c, w, _ = newOpenAITextEntryProbe(t, `{"model":"gpt-5","stream":"true","messages":[]}`)
		h.ChatCompletions(c)
		require.Equal(t, 400, w.Code)
		require.Contains(t, w.Body.String(), "invalid stream field type")
	})
}
func TestOpenAITextHTTPWaitAndSnapshot(t *testing.T) {
	for _, entry := range []string{"responses", "messages", "chat"} {
		t.Run(entry, func(t *testing.T) {
			p, h, c, w, _ := newOpenAITextEntryProbe(t, `{"model":"gpt-5","input":"hi","messages":[]}`)
			run := h.Responses
			if entry == "messages" {
				run = h.Messages
			}
			if entry == "chat" {
				run = h.ChatCompletions
			}
			run(c)
			require.Equal(t, 200, w.Code)
			require.NotNil(t, p.call)
			assertOpenAITextEventBefore(t, p.events, "user-slot", "eligibility")
			assertOpenAITextEventBefore(t, p.events, "eligibility", "execution")
			assertOpenAITextEventBefore(t, p.events, "execution", "user-release")
			if entry == "chat" {
				assertOpenAITextEventBefore(t, p.events, "plan", "moderate")
			} else {
				assertOpenAITextEventBefore(t, p.events, "moderate", "plan")
			}
		})
	}
	t.Run("failed recheck releases without session execution", func(t *testing.T) {
		p, h, c, _, _ := newOpenAITextEntryProbe(t, `{"model":"gpt-5","input":"hi"}`)
		p.eligibility = errors.New("unavailable")
		h.Responses(c)
		require.Contains(t, p.events, "user-release")
		require.NotContains(t, p.events, "execution")
		require.NotContains(t, p.events, "cyber-check")
	})
	t.Run("wait cancellation does not recheck or execute", func(t *testing.T) {
		p, h, c, _, _ := newOpenAITextEntryProbe(t, `{"model":"gpt-5","input":"hi"}`)
		p.canceled = true
		h.Responses(c)
		require.NotContains(t, p.events, "eligibility")
		require.NotContains(t, p.events, "execution")
	})
	t.Run("responses hashes prompt before reasoning rewrite", func(t *testing.T) {
		original := `{"model":"gpt-5","input":"hi"}`
		p, h, c, _, _ := newOpenAITextEntryProbe(t, original)
		p.rewrite = []byte(`{"model":"gpt-5","input":"rewritten"}`)
		h.Responses(c)
		require.NotNil(t, p.call)
		require.Equal(t, []byte(original), p.call.SessionHashBody)
		require.Equal(t, p.rewrite, p.call.Body)
		require.NotNil(t, p.call.SelectionContext)
	})
}

// 计数端口不暴露用户/账号槽或完成提交，测试失败路径不能偷偷进入这些能力。
func (p *openAITextEntryProbe) CountExecution(_ *gin.Context, call gatewayhttp.OpenAICountCall) textflow.SingleCountPorts {
	p.mark("count-execution")
	return &openAITextCountProbe{parent: p, call: call}
}

type openAITextCountProbe struct {
	parent *openAITextEntryProbe
	call   gatewayhttp.OpenAICountCall
}

func (p *openAITextCountProbe) Select() (bool, error) {
	p.parent.mark("count-select")
	return true, nil
}
func (p *openAITextCountProbe) Selected()             { p.parent.mark("count-latency") }
func (p *openAITextCountProbe) SelectionFailed(error) { p.parent.mark("count-selection-failed") }
func (p *openAITextCountProbe) Forward() error        { p.parent.mark("count-forward"); return nil }
func (p *openAITextCountProbe) ForwardFailed(error)   { p.parent.mark("count-forward-failed") }
func TestOpenAITextCountTokensHTTP(t *testing.T) {
	t.Run("funds precede single no-slot selection", func(t *testing.T) {
		p, h, c, w, _ := newOpenAITextEntryProbe(t, `{"model":"gpt-5","messages":[{"role":"user","content":"hello"}]}`)
		h.CountTokens(c)
		require.Equal(t, 200, w.Code)
		assertOpenAITextEventBefore(t, p.events, "plan", "eligibility")
		assertOpenAITextEventBefore(t, p.events, "eligibility", "count-select")
		assertOpenAITextEventBefore(t, p.events, "count-select", "count-latency")
		assertOpenAITextEventBefore(t, p.events, "count-latency", "count-forward")
		require.NotContains(t, p.events, "user-slot")
		require.NotContains(t, p.events, "moderate")
		require.NotContains(t, p.events, "execution")
	})
	t.Run("failed funds check does not select", func(t *testing.T) {
		p, h, c, _, _ := newOpenAITextEntryProbe(t, `{"model":"gpt-5","messages":[]}`)
		p.eligibility = errors.New("unavailable")
		h.CountTokens(c)
		require.NotContains(t, p.events, "count-select")
		require.NotContains(t, p.events, "user-slot")
	})
	t.Run("grok count uses local estimator without funds or selection", func(t *testing.T) {
		p, h, c, w, _ := newOpenAITextEntryProbe(t, `{"model":"grok-4","messages":[{"role":"user","content":"hello"}]}`)
		p.key = nil
		h.GrokCountTokens(c)
		require.Equal(t, 200, w.Code)
		require.Contains(t, w.Body.String(), "input_tokens")
		require.Empty(t, p.events)
	})
	t.Run("grok local empty body error remains Anthropic", func(t *testing.T) {
		_, h, c, w, _ := newOpenAITextEntryProbe(t, "")
		h.GrokCountTokens(c)
		require.Equal(t, 400, w.Code)
		require.Contains(t, w.Body.String(), `"type":"error"`)
	})
}

func (p *openAITextEntryProbe) Execute(_ context.Context, in execution.Request, _ upstream.OutputSink) (execution.ExecutionResult, error) {
	p.mark("execution")
	proto := protocol.ProtocolOpenAIResponses
	switch in.Text.Kind {
	case execution.TextOpenAIChat:
		proto = protocol.ProtocolOpenAIChatCompletions
	case execution.TextOpenAIMessages:
		proto = protocol.ProtocolAnthropicMessages
	}
	p.call = &gatewayhttp.OpenAITextCall{Protocol: proto, Key: in.Funding.Key, Subscription: in.Funding.Subscription, Body: in.Body, ForwardBody: in.AttemptBody, SessionHashBody: in.Text.SessionHashBody, Model: in.Model, ForwardModel: in.Text.ForwardModel, Stream: in.Stream, Mapping: in.Text.Mapping, SessionHash: in.SessionHash, SelectionContext: in.Text.SelectionContext, PreviousResponseID: in.Text.PreviousResponseID, AccountLayerModel: in.Text.AccountLayerModel, PromptCacheKey: in.Text.PromptCacheKey, NativeCompactionV2: in.Text.NativeCompactionV2, LegacyCompact: in.Text.LegacyCompact}
	return execution.ExecutionResult{}, nil
}
