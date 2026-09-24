package httpapi

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
)

// 凭据预算由 HTTP 请求持有，平台准备器只接收显式状态，不读取 Gin 容器。
const credentialBudgetKey = "grok_credential_failover_deadline"

// RequestCredentialBudget 返回本请求共享的预算；这里只分配状态，不提前启动计时。
func RequestCredentialBudget(c *gin.Context) *requeststate.CredentialBudget {
	if c == nil {
		return nil
	}
	if value, ok := c.Get(credentialBudgetKey); ok {
		if state, ok := value.(*requeststate.CredentialBudget); ok {
			return state
		}
	}
	state := &requeststate.CredentialBudget{}
	c.Set(credentialBudgetKey, state)
	return state
}

// CredentialObserver 保留原凭据故障分类与 Ops 关联，不保存凭据值。
type CredentialObserver struct{ Context *gin.Context }

func (o CredentialObserver) ObserveCredentialFailure(id int64, class forward.GrokCredentialFailure) {
	AppendOpsUpstreamError(o.Context, ops.OpsUpstreamErrorEvent{
		Platform:  capability.PlatformGrok,
		AccountID: id,
		Stage:     string(forward.GatewayFailureStageAccountAuth),
		Scope:     string(class.Scope),
		Reason:    string(class.Reason),
		Kind:      "credential_failover",
		Message:   class.Message,
	})
}

// RequestCredentialExecutor 将同一凭据用例绑定到媒体、WS 和文本 HTTP 入口。
type RequestCredentialExecutor struct{ Runtime *provider.RequestCredentials }

func (s *RequestCredentialExecutor) Resolve(ctx context.Context, c *gin.Context, target *provider.ExecutionAccount) (string, string, error) {
	return s.Runtime.Resolve(ctx, RequestCredentialBudget(c), CredentialObserver{Context: c}, target)
}
