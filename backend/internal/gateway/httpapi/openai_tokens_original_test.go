package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/routing"

	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

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

// 计数端口不暴露用户/账号槽或完成提交，测试失败路径不能偷偷进入这些能力。
func (p *tokenEntryOriginalProbe) CountExecution(_ *gin.Context, call OpenAICountCall) textflow.SingleCountPorts {
	p.mark("count-execution")
	return &openAITextCountProbe{parent: p, call: call}
}

type openAITextCountProbe struct {
	parent *tokenEntryOriginalProbe
	call   OpenAICountCall
}

func (p *openAITextCountProbe) Select() (bool, error) {
	p.parent.mark("count-select")
	return true, nil
}

func (p *openAITextCountProbe) Selected() { p.parent.mark("count-latency") }

func (p *openAITextCountProbe) SelectionFailed(error) { p.parent.mark("count-selection-failed") }

func (p *openAITextCountProbe) Forward() error { p.parent.mark("count-forward"); return nil }

func (p *openAITextCountProbe) ForwardFailed(error) { p.parent.mark("count-forward-failed") }

func TestOpenAITextCountTokensHTTP(t *testing.T) {
	t.Run("funds precede single no-slot selection", func(t *testing.T) {
		p, _, c, w, _ := newNativeTokenEntryProbe(t, `{"model":"gpt-5","messages":[{"role":"user","content":"hello"}]}`)
		newOpenAITokensEntryProbe(p).CountTokens(c)
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
		p, _, c, _, _ := newNativeTokenEntryProbe(t, `{"model":"gpt-5","messages":[]}`)
		p.eligibility = errors.New("unavailable")
		newOpenAITokensEntryProbe(p).CountTokens(c)
		require.NotContains(t, p.events, "count-select")
		require.NotContains(t, p.events, "user-slot")
	})
	t.Run("grok count uses local estimator without funds or selection", func(t *testing.T) {
		p, _, c, w, _ := newNativeTokenEntryProbe(t, `{"model":"grok-4","messages":[{"role":"user","content":"hello"}]}`)
		p.key = nil
		newOpenAITokensEntryProbe(p).GrokCountTokens(c)
		require.Equal(t, 200, w.Code)
		require.Contains(t, w.Body.String(), "input_tokens")
		require.Empty(t, p.events)
	})
	t.Run("grok local empty body error remains Anthropic", func(t *testing.T) {
		p, _, c, w, _ := newNativeTokenEntryProbe(t, "")
		newOpenAITokensEntryProbe(p).GrokCountTokens(c)
		require.Equal(t, 400, w.Code)
		require.Contains(t, w.Body.String(), `"type":"error"`)
	})
}

// 原计数断言改为构造独立原生入口。
func newOpenAITokensEntryProbe(p *tokenEntryOriginalProbe) *OpenAITokensHandler {
	return NewOpenAITokensHandler(OpenAITokenOptions{MaxBodyBytes: 1024 * 1024, MaxSwitches: 2}, p, p)
}

// 原入口顺序夹具只替换计数所需能力，不构造旧 Handler。
type tokenEntryOriginalProbe struct {
	OpenAITokenPorts
	events      []string
	key         *apikey.APIKey
	eligibility error
}

func (p *tokenEntryOriginalProbe) mark(value string) { p.events = append(p.events, value) }
func (p *tokenEntryOriginalProbe) Access(*gin.Context) (*apikey.APIKey, bool) {
	p.mark("access")
	return p.key, p.key != nil
}
func (p *tokenEntryOriginalProbe) Plan(_ context.Context, key *apikey.APIKey, model string) routing.RoutePlan {
	p.mark("plan")
	return routing.Plan(routing.PlanInput{GroupID: key.GroupID, RequestedModel: model})
}
func (p *tokenEntryOriginalProbe) Eligibility(context.Context, *apikey.APIKey, *billing.UserSubscription) error {
	p.mark("eligibility")
	return p.eligibility
}
func (p *tokenEntryOriginalProbe) SessionHash(*gin.Context, OpenAISessionInput, []byte) string {
	return "session"
}
func (p *tokenEntryOriginalProbe) MessageAccountModel(_ context.Context, _ *apikey.APIKey, model string) string {
	return model
}
func (p *tokenEntryOriginalProbe) MappedBodyCache(body []byte) func(bool, string) []byte {
	p.mark("mapped-cache")
	return func(bool, string) []byte { return body }
}
func (p *tokenEntryOriginalProbe) ApplyUserPromptReplacementToBody(_ context.Context, body []byte, _ string) []byte {
	return body
}
func newNativeTokenEntryProbe(t *testing.T, body string) (*tokenEntryOriginalProbe, *OpenAITokensHandler, *gin.Context, *httptest.ResponseRecorder, io.ReadCloser) {
	t.Helper()
	writer := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	c.Set(authctx.ContextKeyUser, authctx.AuthSubject{UserID: 7, Concurrency: 2})
	p := &tokenEntryOriginalProbe{key: &apikey.APIKey{ID: 9, UserID: 7}}
	return p, newOpenAITokensEntryProbe(p), c, writer, c.Request.Body
}
