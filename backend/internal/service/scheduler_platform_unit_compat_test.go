//go:build unit

package service

import "context"

// Grok 资格用例的原 unit 断言从新调度器读取结果。
func (s *defaultOpenAIAccountScheduler) selectByLoadBalance(ctx context.Context, req OpenAIAccountScheduleRequest) (*AccountSelectionResult, int, int, float64, error) {
	core, scope := s.platformSelector()
	v, count, k, skew, err := core.SelectByLoadBalance(ctx, platformSelectionInput(req))
	return scope.restore(v), count, k, skew, err
}
