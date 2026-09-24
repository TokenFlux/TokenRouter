// 测试通过公开构造入口装配规则，不访问新模块的内部缓存。
package messageforward_test

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	gatewaytelemetry "github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"
)

type errorRulesFixtureRepo struct {
	errorpolicy.ErrorPassthroughRepository
	rules []*errorpolicy.ErrorPassthroughRule
}

func (r errorRulesFixtureRepo) List(context.Context) ([]*errorpolicy.ErrorPassthroughRule, error) {
	return r.rules, nil
}
func newErrorRulesTestService(rules []*errorpolicy.ErrorPassthroughRule) *errorpolicy.ErrorPassthroughService {
	s := errorpolicy.NewErrorPassthroughService(errorRulesFixtureRepo{rules: rules}, nil, gatewaytelemetry.ErrorRules)
	s.Start()
	return s
}

func newNonFailoverPassthroughRule(statusCode int, keyword string, respCode int, customMessage string) *errorpolicy.ErrorPassthroughRule {
	return &errorpolicy.ErrorPassthroughRule{
		ID:              1,
		Name:            "non-failover-rule",
		Enabled:         true,
		Priority:        1,
		ErrorCodes:      []int{statusCode},
		Keywords:        []string{keyword},
		MatchMode:       errorpolicy.MatchModeAll,
		PassthroughCode: false,
		ResponseCode:    &respCode,
		PassthroughBody: false,
		CustomMessage:   &customMessage,
	}
}
