//go:build unit

package identity_test

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestMergePlatformQuotaDefaults_PatchSemantics(t *testing.T) {
	five := 5.0
	base := identity.DefaultPlatformQuotaSetting{
		DailyLimitUSD:  &five,
		WeeklyLimitUSD: &five,
	}
	ten := 10.0
	patch := identity.DefaultPlatformQuotaSetting{DailyLimitUSD: &ten}

	identity.MergePlatformQuotaDefaults(&base, &patch)
	if base.DailyLimitUSD == nil || *base.DailyLimitUSD != 10.0 {
		t.Errorf("daily not patched: %+v", base.DailyLimitUSD)
	}
	if base.WeeklyLimitUSD == nil || *base.WeeklyLimitUSD != 5.0 {
		t.Errorf("weekly should remain 5.0: %+v", base.WeeklyLimitUSD)
	}
}

func TestMergePlatformQuotaDefaults_ZeroIsExplicitDisable(t *testing.T) {
	five := 5.0
	base := identity.DefaultPlatformQuotaSetting{DailyLimitUSD: &five}
	zero := 0.0
	patch := identity.DefaultPlatformQuotaSetting{DailyLimitUSD: &zero}

	identity.MergePlatformQuotaDefaults(&base, &patch)
	if base.DailyLimitUSD == nil || *base.DailyLimitUSD != 0 {
		t.Errorf("explicit 0 should patch base, got %+v", base.DailyLimitUSD)
	}
}

func TestMergePlatformQuotaDefaults_NilSrcIsNoop(t *testing.T) {
	five := 5.0
	base := identity.DefaultPlatformQuotaSetting{DailyLimitUSD: &five}
	identity.MergePlatformQuotaDefaults(&base, nil)
	if base.DailyLimitUSD == nil || *base.DailyLimitUSD != 5.0 {
		t.Errorf("nil src should be no-op: %+v", base.DailyLimitUSD)
	}
}

func TestAllowedQuotaPlatformsIncludesQoder(t *testing.T) {
	require.True(t, billing.IsAllowedQuotaPlatform(capability.PlatformQoder))
	require.Contains(t, billing.AllowedQuotaPlatforms, capability.PlatformQoder)
}

func TestGetDefaultPlatformQuotas_ReturnsAllowedPlatforms(t *testing.T) {
	zero := 0.0
	svc := newGrantQuotaReadFixture(map[string]string{
		// 新 JSON 格式：anthropic daily=10.5, openai monthly=0, 其他平台无配置
		billing.SettingKeyDefaultPlatformQuotas: `{"anthropic":{"daily":10.5},"openai":{"monthly":0}}`,
	})
	got, err := svc.GetDefaultPlatformQuotas(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 必须包含全部允许 platform key（补齐契约）
	for _, platform := range billing.AllowedQuotaPlatforms {
		if _, ok := got[platform]; !ok {
			t.Errorf("missing platform key: %q", platform)
		}
	}
	// anthropic daily = 10.5
	if v := got["anthropic"].DailyLimitUSD; v == nil || *v != 10.5 {
		t.Errorf("anthropic daily want 10.5, got %v", v)
	}
	// openai monthly = 0（显式禁用）
	if v := got["openai"].MonthlyLimitUSD; v == nil || *v != zero {
		t.Errorf("openai monthly want 0 (explicit disable), got %v", v)
	}
	// gemini 无配置 → weekly = nil
	if v := got["gemini"].WeeklyLimitUSD; v != nil {
		t.Errorf("gemini weekly want nil (not configured), got %v", *v)
	}
}

func TestGetAuthSourcePlatformQuotas_OnlyConfiguredReturned(t *testing.T) {
	source := "email"
	// 新 JSON 格式：anthropic daily=5, monthly=100；openai weekly=0；其他平台无配置
	svc := newGrantQuotaReadFixture(map[string]string{
		identity.SettingKeyAuthSourcePlatformQuotas(source): `{"anthropic":{"daily":5,"monthly":100},"openai":{"weekly":0}}`,
	})
	got := svc.GetAuthSourcePlatformQuotas(context.Background(), source)

	// anthropic 有配置 → 在结果中
	anthro, ok := got["anthropic"]
	if !ok {
		t.Fatal("expected anthropic to be present")
	}
	if anthro.DailyLimitUSD == nil || *anthro.DailyLimitUSD != 5.0 {
		t.Errorf("anthropic daily want 5.0, got %v", anthro.DailyLimitUSD)
	}
	if anthro.MonthlyLimitUSD == nil || *anthro.MonthlyLimitUSD != 100.0 {
		t.Errorf("anthropic monthly want 100.0, got %v", anthro.MonthlyLimitUSD)
	}
	if anthro.WeeklyLimitUSD != nil {
		t.Errorf("anthropic weekly not configured, want nil, got %v", *anthro.WeeklyLimitUSD)
	}

	// openai weekly=0 → 在结果中
	oai, ok := got["openai"]
	if !ok {
		t.Fatal("expected openai to be present")
	}
	if oai.WeeklyLimitUSD == nil || *oai.WeeklyLimitUSD != 0 {
		t.Errorf("openai weekly want 0, got %v", oai.WeeklyLimitUSD)
	}

	// gemini / antigravity 无配置 → 不在结果中（override 语义）
	if _, ok := got["gemini"]; ok {
		t.Error("gemini not configured, should be absent from result")
	}
	if _, ok := got["antigravity"]; ok {
		t.Error("antigravity not configured, should be absent from result")
	}
}

func TestGetAuthSourcePlatformQuotas_AllNegativeOrEmpty_NoEntry(t *testing.T) {
	source := "linuxdo"
	// 新 JSON 格式：未配置任何平台（空 JSON key）→ 返回空 map
	svc := newGrantQuotaReadFixture(map[string]string{
		identity.SettingKeyAuthSourcePlatformQuotas(source): `{}`,
	})
	got := svc.GetAuthSourcePlatformQuotas(context.Background(), source)
	// 空 map → override 语义，无 openai 条目
	if _, ok := got["openai"]; ok {
		t.Error("empty JSON object should result in no openai entry")
	}
	if len(got) != 0 {
		t.Errorf("expected empty result map, got %v", got)
	}
}

// TestGetAuthSourcePlatformQuotas_JSON 验证新 JSON key 读写语义：
// 写入 JSON，断言已配置平台在结果中、未配置平台不在结果中（override 语义）。
func TestGetAuthSourcePlatformQuotas_JSON(t *testing.T) {
	svc := newGrantQuotaReadFixture(map[string]string{
		identity.SettingKeyAuthSourcePlatformQuotas("email"): `{"openai":{"daily":null,"weekly":null,"monthly":20}}`,
	})
	got := svc.GetAuthSourcePlatformQuotas(context.Background(), "email")

	// openai monthly = 20
	oai, ok := got["openai"]
	if !ok {
		t.Fatal("expected openai to be present")
	}
	if oai.MonthlyLimitUSD == nil || *oai.MonthlyLimitUSD != 20 {
		t.Errorf("openai monthly want 20, got %v", oai.MonthlyLimitUSD)
	}
	if oai.DailyLimitUSD != nil {
		t.Errorf("openai daily want nil, got %v", *oai.DailyLimitUSD)
	}
	if oai.WeeklyLimitUSD != nil {
		t.Errorf("openai weekly want nil, got %v", *oai.WeeklyLimitUSD)
	}

	// anthropic 未配置 → 不在结果中（override 语义）
	if _, ok := got["anthropic"]; ok {
		t.Error("anthropic not configured, should be absent from result")
	}
}

// grantQuotaReadFixture 复用身份设置夹具，只补充额度单键读取端口。
type grantQuotaReadFixture struct{ *authSourceDefaultsRepoStub }

func (s *grantQuotaReadFixture) GetValue(_ context.Context, key string) (string, error) {
	return s.values[key], nil
}
func newGrantQuotaReadFixture(values map[string]string) *identity.GrantSettings {
	return identity.NewGrantSettings(&grantQuotaReadFixture{&authSourceDefaultsRepoStub{values: values}}, identity.GrantSettingsOptions{})
}
