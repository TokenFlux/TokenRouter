//go:build unit

package routing_test

import (
	"context"
	"errors"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// groupPlatformRepoStub 只实现 UpdateGroup 走到的两个方法，其余靠内嵌接口占位。
type groupPlatformRepoStub struct {
	routing.GroupRepository

	group     *routing.Group
	updated   *routing.Group
	updateErr error
}

func (r *groupPlatformRepoStub) GetByID(_ context.Context, _ int64) (*routing.Group, error) {
	cloned := *r.group
	return &cloned, nil
}

func (r *groupPlatformRepoStub) Update(_ context.Context, group *routing.Group) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	r.updated = group
	return nil
}

type pricingConfigCacheInvalidatorSpy struct {
	calls int
}

func (s *pricingConfigCacheInvalidatorSpy) InvalidateCache() { s.calls++ }

// 共享价格配置缓存持有 groupID → platform，而共享价格配置定价/模型映射/模型白名单都按平台严格隔离。
// 改了分组平台却不失效缓存，最长 10 分钟内这些查找仍按旧平台匹配（静默走错价）。
func TestUpdateGroupInvalidatesPricingConfigCacheOnPlatformChange(t *testing.T) {
	tests := []struct {
		name          string
		fromPlatform  string
		inputPlatform string
		wantCalls     int
	}{
		{
			name:          "platform changed invalidates",
			fromPlatform:  capability.PlatformAnthropic,
			inputPlatform: capability.PlatformOpenAI,
			wantCalls:     1,
		},
		{
			name:          "same platform does not invalidate",
			fromPlatform:  capability.PlatformAnthropic,
			inputPlatform: capability.PlatformAnthropic,
			wantCalls:     0,
		},
		{
			// 请求里不带 platform 字段时不应该动缓存
			name:          "platform omitted does not invalidate",
			fromPlatform:  capability.PlatformAnthropic,
			inputPlatform: "",
			wantCalls:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &groupPlatformRepoStub{group: &routing.Group{ID: 7, Name: "g", Platform: tt.fromPlatform}}
			spy := &pricingConfigCacheInvalidatorSpy{}
			svc := newOriginalGroupAdmin(repo, nil, spy)

			got, err := svc.UpdateGroup(context.Background(), 7, &routing.UpdateGroupInput{Platform: tt.inputPlatform})
			require.NoError(t, err)
			require.NotNil(t, got)
			require.Equal(t, tt.wantCalls, spy.calls)
		})
	}
}

// 依赖可以不注入（例如测试或裁剪构建），此时不应 panic——缓存靠 TTL 自然重建。
func TestUpdateGroupWithoutPricingConfigCacheInvalidator(t *testing.T) {
	repo := &groupPlatformRepoStub{group: &routing.Group{ID: 7, Name: "g", Platform: capability.PlatformAnthropic}}
	svc := newOriginalGroupAdmin(repo, nil, nil)

	got, err := svc.UpdateGroup(context.Background(), 7, &routing.UpdateGroupInput{Platform: capability.PlatformOpenAI})
	require.NoError(t, err)
	require.Equal(t, capability.PlatformOpenAI, got.Platform)
}

// 分组事务失败时数据库仍是旧平台，因此不能提前失效并重建共享价格配置缓存。
func TestUpdateGroupDoesNotInvalidatePricingConfigCacheWhenUpdateFails(t *testing.T) {
	repo := &groupPlatformRepoStub{
		group:     &routing.Group{ID: 7, Name: "g", Platform: capability.PlatformAnthropic},
		updateErr: errors.New("update failed"),
	}
	spy := &pricingConfigCacheInvalidatorSpy{}
	svc := newOriginalGroupAdmin(repo, nil, spy)

	got, err := svc.UpdateGroup(context.Background(), 7, &routing.UpdateGroupInput{Platform: capability.PlatformOpenAI})
	require.Error(t, err)
	require.Nil(t, got)
	require.Zero(t, spy.calls)
}
