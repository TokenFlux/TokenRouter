package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// qoderRuntimeContract 只替换外部端口，实际运行 HTTP、尝试循环及完成提交边界。
type qoderRuntimeContract struct {
	t                                   *testing.T
	partial                             bool
	wire                                protocol.ProtocolID
	events                              []string
	captures, records, binds, refreshes int
}

func (f *qoderRuntimeContract) CheckKey(context.Context, *apikey.APIKey, *billing.UserSubscription, string, bool) error {
	f.events = append(f.events, "funding")
	return nil
}
func (f *qoderRuntimeContract) Select(context.Context, *int64, string, string, map[int64]struct{}, int64) (QoderCompatibleSelection, error) {
	f.events = append(f.events, "select")
	return f, nil
}
func (f *qoderRuntimeContract) Plan(_ context.Context, key *apikey.APIKey, model string) routing.RoutePlan {
	return routing.Plan(routing.PlanInput{GroupID: key.GroupID, RequestedModel: model, Channel: routing.ChannelMappingResult{Mapped: true, MappedModel: "upstream-model"}})
}

func (f *qoderRuntimeContract) BindStickySession(context.Context, *int64, string, int64) error {
	f.binds++
	return nil
}
func (f *qoderRuntimeContract) Target() QoderCompatibleTarget { return f }
func (*qoderRuntimeContract) Acquired() bool                  { return true }
func (f *qoderRuntimeContract) ReleaseFunc() func() {
	return func() { f.events = append(f.events, "release") }
}
func (*qoderRuntimeContract) WaitPlan() *scheduler.AccountWaitPlan { return nil }
func (f *qoderRuntimeContract) Report(_ int64, ok bool, _ *forward.MessagesResult) {
	require.Equal(f.t, !f.partial, ok)
	f.events = append(f.events, "report")
}
func (f *qoderRuntimeContract) Switched() { f.t.Fatal("已有用量不得再次切号") }
func (*qoderRuntimeContract) Snapshot() account.AccountSnapshot {
	return account.AccountSnapshot{ID: 1, Platform: "qoder", Concurrency: 1}
}
func (f *qoderRuntimeContract) Forward(_ context.Context, c *gin.Context, body []byte, wire protocol.ProtocolID, model string) (*forward.MessagesResult, error) {
	f.events = append(f.events, "forward")
	require.Equal(f.t, f.wire, wire)
	require.Equal(f.t, "client-model", model)
	require.Equal(f.t, "upstream-model", gjson.GetBytes(body, "model").String())
	result := &forward.MessagesResult{RequestID: "response-id", Model: model, UpstreamModel: "upstream-model", Usage: upstream.TokenUsage{InputTokens: 3, OutputTokens: 2}}
	if f.partial {
		c.Writer.WriteHeader(http.StatusOK)
		_, err := c.Writer.Write([]byte("data: {\"text\":\"partial\"}\n\n"))
		require.NoError(f.t, err)
		return result, errors.New("after service")
	}
	c.JSON(http.StatusOK, gin.H{"result": "complete"})
	return result, nil
}
func (f *qoderRuntimeContract) Refresh(context.Context) (QoderCompatibleTarget, error) {
	f.refreshes++
	return f, nil
}
func (f *qoderRuntimeContract) Completion(_ context.Context, capture QoderCompletionCapture) *completion.Input {
	f.captures++
	require.Equal(f.t, "client-model", gjson.GetBytes(capture.Body, "model").String())
	require.Equal(f.t, 3, capture.Result.Usage.InputTokens)
	return &completion.Input{Account: &completion.AccountSnapshot{ID: 1}, Result: &completion.Result{}}
}
func (f *qoderRuntimeContract) Record(ctx context.Context, _ *completion.Input, openAI bool) error {
	require.NoError(f.t, ctx.Err())
	require.False(f.t, openAI)
	f.records++
	return nil
}

// 两种兼容协议保持一次执行、已观测部分结果一次完成以及成功专属粘性绑定。
func TestQoderCompatibleNativeHTTPCompletionBoundary(t *testing.T) {
	for _, endpoint := range []QoderEndpoint{QoderMessages, QoderResponses} {
		for _, partial := range []bool{false, true} {
			label := string(endpoint) + "/success"
			if partial {
				label = string(endpoint) + "/partial"
			}
			t.Run(label, func(t *testing.T) {
				wire := protocol.ProtocolAnthropicMessages
				if endpoint == QoderResponses {
					wire = protocol.ProtocolOpenAIResponses
				}
				fixture := &qoderRuntimeContract{t: t, wire: wire, partial: partial}
				group := int64(4)
				key := &apikey.APIKey{ID: 2, GroupID: &group, Group: &routing.Group{Platform: "qoder"}}
				options := QoderCompatibleOptions{Execution: fixture, Funding: fixture, Recorder: fixture, PlatformAvailable: true, ReadAccess: func(*gin.Context) (*apikey.APIKey, bool) { return key, true }, MayRefresh: func(error) bool { return false }, MaySwitch: func(error) bool { return false }, Errors: QoderErrorPresenter{Describe: func(error) forward.QoderErrorView { return forward.QoderErrorView{} }}}
				h := NewQoderCompatibleHandler(NewQoderCompatibleRuntime(options), nil, 3)
				response := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(response)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+string(endpoint), strings.NewReader(`{"model":"client-model","messages":[{"role":"user","content":"hello"}]}`))
				authctx.SetPrincipal(c, identity.Principal{UserID: 3}, 1, "")
				if endpoint == QoderMessages {
					h.Messages(c)
				} else {
					h.Responses(c)
				}
				require.Equal(t, []string{"funding", "select", "forward", "release", "report"}, fixture.events)
				require.Equal(t, 1, fixture.captures)
				require.Equal(t, 1, fixture.records)
				require.Zero(t, fixture.refreshes)
				if partial {
					require.Zero(t, fixture.binds)
					require.Contains(t, response.Body.String(), "partial")
					require.Contains(t, response.Body.String(), "Upstream request failed")
				} else {
					expected := 1
					if endpoint == QoderResponses {
						expected = 2
					}
					require.Equal(t, expected, fixture.binds)
				}
			})
		}
	}
}
