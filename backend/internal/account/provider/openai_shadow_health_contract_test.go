//go:build unit

package provider

import (
	"context"
	"log/slog"
	"net/http"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/stretchr/testify/require"
)

// TestPersistOpenAI429PlanType_SkipsShadow 验证外审第7轮 P1:429 plan_type 同步走 BulkUpdate 直写
// (不经 persistAccountCredentials),必须对影子早返,否则会把 plan_type 写进影子 credentials。
func TestPersistOpenAI429PlanType_SkipsShadow(t *testing.T) {
	ctx := context.Background()
	body := []byte(`{"error":{"type":"usage_limit_reached","plan_type":"pro"}}`)
	parentID := int64(1)

	t.Run("shadow_skipped", func(t *testing.T) {
		repo := &openAI429SnapshotRepo{}
		shadow := &accountcore.Record{Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Credentials: map[string]any{}, ParentAccountID: &parentID}
		accountcore.PersistOpenAIObservedPlan(ctx, repo, shadow, openai.ParseUsageLimitPlanType(body), slog.Info, slog.Warn)
		require.Empty(t, shadow.Credentials, "影子不可被写入 plan_type 凭据")
	})

	t.Run("normal_account_writes", func(t *testing.T) {
		repo := &openAI429SnapshotRepo{}
		normal := &accountcore.Record{ID: 9, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Credentials: map[string]any{}}
		accountcore.PersistOpenAIObservedPlan(ctx, repo, normal, openai.ParseUsageLimitPlanType(body), slog.Info, slog.Warn)
		require.Equal(t, "pro", normal.Credentials["plan_type"], "普通账号应写入 plan_type(反向对照,证明 body 有效、写路径通)")
	})
}

// updateExtraSpyRepo 记录 UpdateExtra 是否被调用,用于验证影子 codex_* 快照守卫。
type updateExtraSpyRepo struct {
	*openAI429SnapshotRepo
	updateExtraCalled bool
}

func (r *updateExtraSpyRepo) UpdateExtra(_ context.Context, _ int64, _ map[string]any) error {
	r.updateExtraCalled = true
	return nil
}

// TestPersistOpenAICodexSnapshot_SkipsShadow 验证外审第7轮 P1:影子 codex_* 仅由 QueryUsage
// (/wham/usage bengalfox)更新,不能被 429 路径的 x-codex-* 全局头快照污染。
func TestPersistOpenAICodexSnapshot_SkipsShadow(t *testing.T) {
	headers := http.Header{}
	headers.Set("x-codex-primary-used-percent", "50")
	parentID := int64(1)

	t.Run("shadow_skipped", func(t *testing.T) {
		spy := &updateExtraSpyRepo{openAI429SnapshotRepo: &openAI429SnapshotRepo{}}
		s := newOpenAI429Observer(spy)
		shadow := &accountcore.Record{Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, ParentAccountID: &parentID}
		s.PersistCodexSnapshot(context.Background(), shadow, headers)
		require.False(t, spy.updateExtraCalled, "影子不应写 codex_* 头快照")
	})

	t.Run("normal_account_writes", func(t *testing.T) {
		spy := &updateExtraSpyRepo{openAI429SnapshotRepo: &openAI429SnapshotRepo{}}
		s := newOpenAI429Observer(spy)
		normal := &accountcore.Record{ID: 9, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}
		s.PersistCodexSnapshot(context.Background(), normal, headers)
		require.True(t, spy.updateExtraCalled, "普通账号应写 codex_* 头快照(反向对照)")
	})
}
