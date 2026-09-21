//go:build unit

package app

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
	settingshttp "github.com/TokenFlux/TokenRouter/internal/settings/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/settings/composite"
	"github.com/stretchr/testify/require"
)

func floatPtrPQ(v float64) *float64 { return &v }

// newPlatformQuotaSettingsFixture 只组合原生准备器、Store 和身份额度读取，不复制资金规则。
func newPlatformQuotaSettingsFixture(seed map[string]string) (settingshttp.CompositeSettings, *identity.GrantSettings) {
	options, store := newCompositeSettingsHTTPFixture(&settingHandlerRepoStub{values: seed}, &config.Config{})
	return options.Settings, identity.NewGrantSettings(store, identity.GrantSettingsOptions{})
}

// commitQuotaSettingsFixture 使用与综合更新相同的一次提交和应用入口。
func commitQuotaSettingsFixture(ctx context.Context, source settingshttp.CompositeSettings, value *composite.Snapshot, auth *identity.AuthSourceDefaultSettings) error {
	session, err := source.BeginSettingsUpdate(ctx)
	if err != nil {
		return err
	}
	defer session.Close()
	values, err := source.PrepareSettingsWithAuthSourceDefaults(session.Context(), value, auth, nil)
	if err != nil {
		return err
	}
	return session.Commit(settingscore.PreparedChange{Module: "quota-settings", Values: values, Apply: source.ApplyPersistedSettings})
}

// TestSystemPlatformQuotas_WriteReadRoundTrip 验证系统层 platform quota 经 buildSystemSettingsUpdates（写）
// 再由 GetDefaultPlatformQuotas（读）正确往返，覆盖真实 write→read 路径并锁住平台补齐契约。
func TestSystemPlatformQuotas_WriteReadRoundTrip(t *testing.T) {
	svc, _ := newPlatformQuotaSettingsFixture(nil)
	ctx := context.Background()

	ten := 10.0
	ss := &composite.Snapshot{
		DefaultPlatformQuotas: map[string]*identity.DefaultPlatformQuotaSetting{
			"anthropic": {DailyLimitUSD: &ten, WeeklyLimitUSD: nil, MonthlyLimitUSD: nil},
		},
	}
	if err := commitQuotaSettingsFixture(ctx, svc, ss, nil); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}

	got, err := svc.GetDefaultPlatformQuotas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// 平台补齐契约：无论写了几个 platform，读回必须含全部允许平台
	for _, p := range billing.AllowedQuotaPlatforms {
		if _, ok := got[p]; !ok {
			t.Errorf("allowed-platform contract violated: missing platform %q", p)
		}
	}
	// 写入值正确往返
	if v := got["anthropic"].DailyLimitUSD; v == nil || *v != ten {
		t.Fatalf("anthropic daily round-trip failed: got %v, want 10", v)
	}
	// 未写入的平台字段为 nil
	if got["openai"].DailyLimitUSD != nil {
		t.Errorf("openai daily should be nil (not written), got %v", got["openai"].DailyLimitUSD)
	}
}

// TestSystemPlatformQuotas_EmptyMapClearsAll 验证空 map 的整体替换语义：
// 写入 DefaultPlatformQuotas={} 后，GetDefaultPlatformQuotas 返回全部允许平台、所有字段均为 nil，
// 明确文档化"空 map = 清空全部配额"是有意为之的 whole-replace 语义。
func TestSystemPlatformQuotas_EmptyMapClearsAll(t *testing.T) {
	svc, _ := newPlatformQuotaSettingsFixture(nil)
	ctx := context.Background()

	// 先写入有值的配置
	ten := 10.0
	if err := commitQuotaSettingsFixture(ctx, svc, &composite.Snapshot{
		DefaultPlatformQuotas: map[string]*identity.DefaultPlatformQuotaSetting{
			"anthropic": {DailyLimitUSD: &ten},
		},
	}, nil); err != nil {
		t.Fatalf("initial write: %v", err)
	}

	// 再写入空 map（整体替换语义：清空全部）
	if err := commitQuotaSettingsFixture(ctx, svc, &composite.Snapshot{
		DefaultPlatformQuotas: map[string]*identity.DefaultPlatformQuotaSetting{},
	}, nil); err != nil {
		t.Fatalf("empty map write: %v", err)
	}

	got, err := svc.GetDefaultPlatformQuotas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// 全部允许平台 key 仍然存在（补齐契约）
	for _, p := range billing.AllowedQuotaPlatforms {
		if _, ok := got[p]; !ok {
			t.Errorf("allowed-platform contract violated after empty write: missing %q", p)
		}
	}
	// 所有字段 nil（全部已清空）
	for _, p := range billing.AllowedQuotaPlatforms {
		pq := got[p]
		if pq == nil {
			continue
		}
		if pq.DailyLimitUSD != nil || pq.WeeklyLimitUSD != nil || pq.MonthlyLimitUSD != nil {
			t.Errorf("platform %q should have all-nil limits after empty-map write, got %+v", p, pq)
		}
	}
}

// TestUpdateSettingsWithAuthSourceDefaults_PlatformQuotaRoundTrip 验证 round-4 fix：
// PUT /admin/settings 携带的 auth source × platform × window 限额能完整写入并被 GetAuthSourcePlatformQuotas 读回。
// Round-4 之前 writeProviderDefaultGrantUpdates 完全没写 PQ key，前端配置静默丢失。
func TestUpdateSettingsWithAuthSourceDefaults_PlatformQuotaRoundTrip(t *testing.T) {
	svc, grants := newPlatformQuotaSettingsFixture(nil)
	systemSettings := &composite.Snapshot{}
	authDefaults := &identity.AuthSourceDefaultSettings{
		Email: identity.ProviderDefaultGrantSettings{
			PlatformQuotas: map[string]*identity.DefaultPlatformQuotaSetting{
				"anthropic": {
					DailyLimitUSD:   floatPtrPQ(5.0),
					WeeklyLimitUSD:  nil, // 无限额
					MonthlyLimitUSD: floatPtrPQ(100.0),
				},
				"openai": {
					DailyLimitUSD: floatPtrPQ(0), // 显式禁用
				},
			},
		},
	}
	if err := commitQuotaSettingsFixture(context.Background(), svc, systemSettings, authDefaults); err != nil {
		t.Fatalf("UpdateSettingsWithAuthSourceDefaults: %v", err)
	}
	got := grants.GetAuthSourcePlatformQuotas(context.Background(), "email")
	anthro := got["anthropic"]
	if anthro == nil || anthro.DailyLimitUSD == nil || *anthro.DailyLimitUSD != 5.0 {
		t.Errorf("anthropic daily round-trip failed: %+v", anthro)
	}
	if anthro != nil && anthro.WeeklyLimitUSD != nil {
		t.Errorf("anthropic weekly want nil (无限额), got %v", *anthro.WeeklyLimitUSD)
	}
	if anthro == nil || anthro.MonthlyLimitUSD == nil || *anthro.MonthlyLimitUSD != 100.0 {
		t.Errorf("anthropic monthly round-trip failed: %+v", anthro)
	}
	oai := got["openai"]
	if oai == nil || oai.DailyLimitUSD == nil || *oai.DailyLimitUSD != 0 {
		t.Errorf("openai daily=0 (禁用) round-trip failed: %+v", oai)
	}
	// 其他 source 不应有 quota（authDefaults 只填了 Email）
	if linux := grants.GetAuthSourcePlatformQuotas(context.Background(), "linuxdo"); len(linux) != 0 {
		t.Errorf("linuxdo should be empty, got %+v", linux)
	}
}

// TestUpdateSettingsWithAuthSourceDefaults_NilPlatformQuotaPreservesExisting 验证 #2 防御：
// 请求未携带某 auth source 的 platform quota（nil）时跳过写入、保留既有配置，
// 而非整体替换为空 map 清空（与系统层 nil 守卫一致）。
func TestUpdateSettingsWithAuthSourceDefaults_NilPlatformQuotaPreservesExisting(t *testing.T) {
	svc, grants := newPlatformQuotaSettingsFixture(map[string]string{
		identity.SettingKeyAuthSourcePlatformQuotas("email"): `{"anthropic":{"daily":5,"weekly":null,"monthly":null}}`,
	})
	// authDefaults 不携带 Email 的 PlatformQuotas（nil）——应保留既有配置
	authDefaults := &identity.AuthSourceDefaultSettings{
		Email: identity.ProviderDefaultGrantSettings{PlatformQuotas: nil},
	}
	if err := commitQuotaSettingsFixture(context.Background(), svc, &composite.Snapshot{}, authDefaults); err != nil {
		t.Fatalf("UpdateSettingsWithAuthSourceDefaults: %v", err)
	}
	anthro := grants.GetAuthSourcePlatformQuotas(context.Background(), "email")["anthropic"]
	if anthro == nil || anthro.DailyLimitUSD == nil || *anthro.DailyLimitUSD != 5.0 {
		t.Errorf("nil PlatformQuotas 应保留既有 anthropic daily=5，got %+v", anthro)
	}
}

// TestUpdateSettingsWithAuthSourceDefaults_NegativeQuotaRejected 验证改动 C：
// auth-source platform quota 含负数时，UpdateSettingsWithAuthSourceDefaults 返回 BadRequest 错误。
func TestUpdateSettingsWithAuthSourceDefaults_NegativeQuotaRejected(t *testing.T) {
	svc, _ := newPlatformQuotaSettingsFixture(nil)
	neg := -1.0
	authDefaults := &identity.AuthSourceDefaultSettings{
		Email: identity.ProviderDefaultGrantSettings{
			PlatformQuotas: map[string]*identity.DefaultPlatformQuotaSetting{
				"anthropic": {DailyLimitUSD: &neg},
			},
		},
	}
	err := commitQuotaSettingsFixture(context.Background(), svc, &composite.Snapshot{}, authDefaults)
	require.Error(t, err, "expected error for negative quota")
	require.Equal(t, "INVALID_DEFAULT_PLATFORM_QUOTA", apperror.Reason(err))
}
