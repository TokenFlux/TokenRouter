// 测试通过公开构造入口装配规则，不访问规则模块的内部缓存。
package googleforward_test

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
