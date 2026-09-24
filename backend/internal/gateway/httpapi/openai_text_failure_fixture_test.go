package httpapi

import (
	"context"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
)

type textFailureStore struct {
	gatewayprovider.ExecutionAccountStore
	tempUnschedCalls, rateLimitedCalls, updateCalls int
	modelRateLimitAccountID                         int64
	modelRateLimitKey                               string
}

func (s *textFailureStore) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	s.tempUnschedCalls++
	return nil
}
func (s *textFailureStore) SetRateLimited(context.Context, int64, time.Time) error {
	s.rateLimitedCalls++
	return nil
}
func (s *textFailureStore) SetRateLimitedIfLater(c context.Context, id int64, t time.Time) error {
	return s.SetRateLimited(c, id, t)
}
func (s *textFailureStore) UpdateExtra(context.Context, int64, map[string]any) error {
	s.updateCalls++
	return nil
}
func (s *textFailureStore) SetModelRateLimit(_ context.Context, id int64, key string, _ time.Time, _ ...string) error {
	s.modelRateLimitAccountID = id
	s.modelRateLimitKey = key
	return nil
}

// textFailureFixture 组合实际账号健康、阻断与协议分类器，存储替身只记录写入。
func textFailureFixture(store gatewayprovider.ExecutionAccountStore, observe bool) *OpenAITextExecutor {
	blocks := accountcore.NewRuntimeBlockState(time.Now)
	models := accountcore.NewModelTransientState(0)
	var health *accountprovider.UpstreamHealth
	if observe {
		health = gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{Store: store, Options: accountcore.HealthOptions{Block: blocks.BlockAccountScheduling}})
	}
	grokHealth := &accountprovider.GrokHealth{NormalizeModel: func(value *accountcore.Record, model string) string {
		return (gatewayprovider.ModelPolicy{Record: value}).NormalizeOpenAI(model)
	}, Store: store, Health: health, Runtime: blocks, ModelTransient: models}
	return &OpenAITextExecutor{Output: &OpenAIResponseOutput{Health: &accountprovider.OpenAIResponseHealth{Health: health, Runtime: blocks, ModelTransient: models}}, Grok: &GrokExecutor{Health: grokHealth}}
}
