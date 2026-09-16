// 测试通过公开构造入口装配规则，不访问新模块的内部缓存。
package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/model"
)

type errorRulesFixtureRepo struct {
	ErrorPassthroughRepository
	rules []*model.ErrorPassthroughRule
}

func (r errorRulesFixtureRepo) List(context.Context) ([]*model.ErrorPassthroughRule, error) {
	return r.rules, nil
}
func newErrorRulesTestService(rules []*model.ErrorPassthroughRule) *ErrorPassthroughService {
	s := NewErrorPassthroughService(errorRulesFixtureRepo{rules: rules}, nil)
	s.Start()
	return s
}
