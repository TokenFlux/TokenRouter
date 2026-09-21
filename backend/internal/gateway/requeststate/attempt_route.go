package requeststate

import (
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// AttemptRoute 只保存当次候选的协议与模型计划，不持有账号记录或执行凭据。
// 值复制保持已选计划独立；模型映射在原调用时点由调用方传入。
type AttemptRoute struct {
	protocol  protocol.ProtocolID
	candidate routing.CandidatePlan
	planned   bool
}

func (a AttemptRoute) Protocol() protocol.ProtocolID { return a.protocol }

// Candidate 返回已捕获的值；缺少原计划时不会补造或重新查询。
func (a AttemptRoute) Candidate() (routing.CandidatePlan, bool) {
	return a.candidate, a.planned
}

// ResolveAttempt 每次 fresh 读取后复核能力，保留分组回退使旧计划失效的规则。
func (s RoutingState) ResolveAttempt(snapshot account.AccountSnapshot, previous AttemptRoute) (AttemptRoute, bool, error) {
	plan, planned := s.plan, s.planSet
	if planned && s.group != nil && plan.GroupID() != s.group.ID {
		planned = false
	}
	if previous.protocol != "" && !planned {
		return previous, false, nil
	}
	if s.clientProtocol == "" {
		return previous, false, nil
	}
	if !planned {
		plan = routing.Plan(routing.PlanInput{Group: s.group, ClientProtocol: s.clientProtocol})
	} else {
		plan = plan.WithClientProtocol(s.clientProtocol)
	}
	candidate, ok := plan.ResolveCandidate(snapshot)
	if !ok {
		return AttemptRoute{}, false, fmt.Errorf("account %d has no enabled route for %s", snapshot.ID, s.clientProtocol)
	}
	result := AttemptRoute{protocol: candidate.UpstreamProtocol, planned: planned}
	if planned {
		result.candidate = candidate
	}
	return result, true, nil
}

// ResolveModel 复用原生一跳规则，不在选择候选时提前读取模型配置。
func (a AttemptRoute) ResolveModel(id int64, platform string, mapping map[string]string, requested string) (string, bool) {
	if a.planned {
		snapshot := account.AccountSnapshot{ID: id, Platform: platform, ModelPolicy: account.NewModelRoutingSnapshot(platform, mapping)}
		candidate, matched := a.candidate.ResolveModel(snapshot, requested)
		return candidate.Models.AccountMappedModel, matched
	}
	return account.ResolveMappedModel(platform, mapping, requested)
}
