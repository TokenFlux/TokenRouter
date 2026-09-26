package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/usage"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// countHTTPContract 只替换外部执行与资金读取，实际 HTTP 和唯一尝试循环均运行。
type countHTTPContract struct {
	CountExecutor
	t        *testing.T
	events   []string
	attempts int
	bodies   [][]byte
	group    int64
	platform string
}

func (f *countHTTPContract) CheckKey(_ context.Context, _ *apikey.APIKey, _ *billing.UserSubscription, platform string, simple bool) error {
	f.events = append(f.events, "funding")
	f.platform = platform
	require.False(f.t, simple)
	return nil
}

func (f *countHTTPContract) ApplyUserPromptReplacementToBody(_ context.Context, body []byte, _ string) []byte {
	return body
}

func (f *countHTTPContract) SelectCountTarget(_ context.Context, group *int64, _ string, model string, excluded map[int64]struct{}) (CountTarget, error) {
	f.attempts++
	f.events = append(f.events, "select")
	require.Equal(f.t, f.group, *group)
	require.Equal(f.t, "client-model", model)
	if f.attempts == 2 {
		require.Contains(f.t, excluded, int64(1))
	}
	return countHTTPContractTarget{fixture: f, id: int64(f.attempts)}, nil
}

func (f *countHTTPContract) PlanCountRoute(_ context.Context, key *apikey.APIKey, model string) routing.RoutePlan {
	f.events = append(f.events, "plan")
	return routing.Plan(routing.PlanInput{GroupID: key.GroupID, RequestedModel: model, GroupMapping: routing.GroupMappingResult{Mapped: true, MappedModel: fmt.Sprintf("attempt-%d", f.attempts)}})
}

func (*countHTTPContract) TempUnscheduleRetryableError(context.Context, int64, *forwardcore.UpstreamFailoverError) {
}

type countHTTPContractTarget struct {
	fixture *countHTTPContract
	id      int64
}

func (t countHTTPContractTarget) Snapshot() account.AccountSnapshot {
	return account.AccountSnapshot{ID: t.id, Platform: "anthropic"}
}
func (t countHTTPContractTarget) RetryLimit() int { return 0 }
func (t countHTTPContractTarget) ReleaseSession(context.Context, string) {
	t.fixture.events = append(t.fixture.events, "release")
}

func (t countHTTPContractTarget) ForwardCountTokens(_ context.Context, c *gin.Context, parsed *requeststate.ParsedRequest) error {
	f := t.fixture
	f.events = append(f.events, "forward")
	f.bodies = append(f.bodies, append([]byte(nil), parsed.Body.Bytes()...))
	require.Equal(f.t, f.group, *parsed.GroupID)
	if t.id == 1 {
		return &forwardcore.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable}
	}
	c.JSON(http.StatusOK, gin.H{"input_tokens": 17})
	return nil
}

// 无槽计数保留资金预检、逐次原报文改写和失败会话释放，不提交费用或完成任务。
func TestCountTokensNativeHTTPAttemptContract(t *testing.T) {
	fixture := &countHTTPContract{t: t, group: 42}
	key := &apikey.APIKey{ID: 7, GroupID: &fixture.group, Group: &routing.Group{Platform: "anthropic"}}
	ports := CountHTTPPorts{
		Executor: fixture, Funding: fixture, Diagnoser: routing.ModelAvailabilityDiagnoserFunc(unexpectedCountModelDiagnosis),
		ReadAccess:           func(*gin.Context) (*apikey.APIKey, bool) { return key, true },
		ObserveCompatibility: func(*zap.Logger) {},
		BusinessError: func(*gin.Context, error, bool, func(int, string, string, bool)) bool {
			t.Fatal("未预期的选择失败")
			return false
		},
		Failure: func(*gin.Context, *forwardcore.UpstreamFailoverError, string, bool) { t.Fatal("未预期的耗尽") },
	}
	handler := NewCountTokensHandler(1<<20, 2, ports, fixture)
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(`{"model":"client-model","messages":[{"role":"user","content":"hello"}]}`))
	c.Request = c.Request.WithContext(apikey.WithForcePlatform(c.Request.Context(), "antigravity"))
	authctx.SetPrincipal(c, identity.Principal{UserID: 1}, 1, "")
	handler.CountTokens(c)
	require.Equal(t, http.StatusOK, response.Code)
	require.JSONEq(t, `{"input_tokens":17}`, response.Body.String())
	require.Equal(t, []string{"funding", "select", "plan", "forward", "release", "select", "plan", "forward"}, fixture.events)
	require.Equal(t, "antigravity", fixture.platform)
	require.Len(t, fixture.bodies, 2)
	require.Equal(t, "attempt-1", gjson.GetBytes(fixture.bodies[0], "model").String())
	require.Equal(t, "attempt-2", gjson.GetBytes(fixture.bodies[1], "model").String())
	value, ok := c.Get(opsRequestTypeKey)
	require.True(t, ok)
	require.Equal(t, int16(usage.RequestTypeSync), value)
}

// 原计数合同未配置诊断依赖；意外进入该分支必须继续使测试失败。
func unexpectedCountModelDiagnosis(context.Context, *int64, string, string) routing.ModelAvailabilityDiagnosis {
	panic("计数合同不应进入模型诊断")
}
