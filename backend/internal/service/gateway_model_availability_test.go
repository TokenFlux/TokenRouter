//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDiagnoseModelAvailabilityForPlatform_NoModel_AlwaysAvailable(t *testing.T) {
	repo := &mockAccountRepoForPlatform{accounts: nil, accountsByID: map[int64]*Account{}}
	svc := &GatewayService{accountRepo: repo, cfg: testConfig()}

	diag := svc.DiagnoseModelAvailabilityForPlatform(context.Background(), nil, "", PlatformOpenAI)

	require.True(t, diag.HasAccountsInPool, "空模型必须保守返回 HasAccountsInPool=true，让调用方继续走 503")
	require.True(t, diag.HasModelSupport, "空模型必须保守返回 HasModelSupport=true，让调用方继续走 503")
}

func TestDiagnoseModelAvailabilityForPlatform_EmptyPlatform_AlwaysAvailable(t *testing.T) {
	repo := &mockAccountRepoForPlatform{accounts: nil, accountsByID: map[int64]*Account{}}
	svc := &GatewayService{accountRepo: repo, cfg: testConfig()}

	diag := svc.DiagnoseModelAvailabilityForPlatform(context.Background(), nil, "gpt-5", "")

	require.True(t, diag.HasAccountsInPool)
	require.True(t, diag.HasModelSupport, "空平台必须回落到 {true,true}，让调用方继续走 503")
}

func TestDiagnoseModelAvailabilityForPlatform_NilReceiver(t *testing.T) {
	var svc *GatewayService

	diag := svc.DiagnoseModelAvailabilityForPlatform(context.Background(), nil, "gpt-5", PlatformOpenAI)

	require.True(t, diag.HasAccountsInPool)
	require.True(t, diag.HasModelSupport)
}

func TestDiagnoseModelAvailabilityForPlatform_NoAccountsInPool(t *testing.T) {
	repo := &mockAccountRepoForPlatform{accounts: nil, accountsByID: map[int64]*Account{}}
	svc := &GatewayService{accountRepo: repo, cfg: testConfig()}

	diag := svc.DiagnoseModelAvailabilityForPlatform(context.Background(), nil, "gpt-5", PlatformOpenAI)

	require.False(t, diag.HasAccountsInPool)
	require.False(t, diag.HasModelSupport, "没有账号表示没有模型支持；调用方会走空池 503 分支")
}

func TestDiagnoseModelAvailabilityForPlatform_ExplicitMappingMatches(t *testing.T) {
	repo := &mockAccountRepoForPlatform{
		accounts: []Account{
			{
				ID:          1,
				Platform:    PlatformOpenAI,
				Status:      StatusActive,
				Schedulable: true,
				Credentials: map[string]any{
					"model_mapping": map[string]any{"gpt-5.1-codex-mini": "gpt-5.1-codex-mini"},
				},
			},
		},
		accountsByID: map[int64]*Account{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
	}
	svc := &GatewayService{accountRepo: repo, cfg: testConfig()}

	diag := svc.DiagnoseModelAvailabilityForPlatform(context.Background(), nil, "gpt-5.1-codex-mini", PlatformOpenAI)

	require.True(t, diag.HasAccountsInPool)
	require.True(t, diag.HasModelSupport)
}

func TestDiagnoseModelAvailabilityForPlatform_EmptyMappingAllowsAll(t *testing.T) {
	repo := &mockAccountRepoForPlatform{
		accounts: []Account{
			{ID: 1, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true /* 无 ModelMapping 表示允许全部模型 */},
		},
		accountsByID: map[int64]*Account{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
	}
	svc := &GatewayService{accountRepo: repo, cfg: testConfig()}

	diag := svc.DiagnoseModelAvailabilityForPlatform(context.Background(), nil, "gpt-5.1-codex-mini", PlatformOpenAI)

	require.True(t, diag.HasModelSupport, "空 model_mapping 必须按 Account.IsModelSupported 语义视为允许全部模型")
}

func TestDiagnoseModelAvailabilityForPlatform_WildcardMappingMatches(t *testing.T) {
	repo := &mockAccountRepoForPlatform{
		accounts: []Account{
			{
				ID:          1,
				Platform:    PlatformOpenAI,
				Status:      StatusActive,
				Schedulable: true,
				Credentials: map[string]any{
					"model_mapping": map[string]any{"*": "gpt-5"},
				},
			},
		},
		accountsByID: map[int64]*Account{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
	}
	svc := &GatewayService{accountRepo: repo, cfg: testConfig()}

	diag := svc.DiagnoseModelAvailabilityForPlatform(context.Background(), nil, "gpt-5.1-codex-mini", PlatformOpenAI)

	require.True(t, diag.HasModelSupport, "通配符映射必须把请求模型视为可服务")
}

func TestDiagnoseModelAvailabilityForPlatform_NoMatchingModel_ReturnsNotFoundSignal(t *testing.T) {
	repo := &mockAccountRepoForPlatform{
		accounts: []Account{
			{
				ID:          1,
				Platform:    PlatformOpenAI,
				Status:      StatusActive,
				Schedulable: true,
				Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5": "gpt-5"}},
			},
			{
				ID:          2,
				Platform:    PlatformOpenAI,
				Status:      StatusActive,
				Schedulable: true,
				Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5-mini": "gpt-5-mini"}},
			},
		},
		accountsByID: map[int64]*Account{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
	}
	svc := &GatewayService{accountRepo: repo, cfg: testConfig()}

	diag := svc.DiagnoseModelAvailabilityForPlatform(context.Background(), nil, "gpt-5.1-codex-mini", PlatformOpenAI)

	require.True(t, diag.HasAccountsInPool, "分组内存在 OpenAI 账号")
	require.False(t, diag.HasModelSupport, "没有账号映射允许该模型时 handler 应返回 404")
}

func TestDiagnoseModelAvailabilityForPlatform_WrongPlatformFiltersOut(t *testing.T) {
	// 分组里只有 Anthropic 账号，但用户路由到 OpenAI 网关。
	// 诊断必须按平台过滤掉 Anthropic 账号，因此 HasAccountsInPool=false，调用方保留 503。
	repo := &mockAccountRepoForPlatform{
		accounts: []Account{
			{
				ID:          1,
				Platform:    PlatformAnthropic,
				Status:      StatusActive,
				Schedulable: true,
				Credentials: map[string]any{"model_mapping": map[string]any{"claude-sonnet-4-5": "claude-sonnet-4-5"}},
			},
		},
		accountsByID: map[int64]*Account{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
	}
	svc := &GatewayService{accountRepo: repo, cfg: testConfig()}

	diag := svc.DiagnoseModelAvailabilityForPlatform(context.Background(), nil, "gpt-5", PlatformOpenAI)

	require.False(t, diag.HasAccountsInPool, "OpenAI 路由不能把 Anthropic 账号算进账号池")
	require.False(t, diag.HasModelSupport)
}

func TestOpenAIGatewayDiagnoseModelAvailabilityForPlatform_GrokPlatformFiltersOpenAIAccounts(t *testing.T) {
	repo := &mockAccountRepoForPlatform{
		accounts: []Account{
			{
				ID:          1,
				Platform:    PlatformOpenAI,
				Status:      StatusActive,
				Schedulable: true,
				Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5": "gpt-5"}},
			},
		},
		accountsByID: map[int64]*Account{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
	}
	svc := &OpenAIGatewayService{accountRepo: repo, cfg: testConfig()}

	diag := svc.DiagnoseModelAvailabilityForPlatform(context.Background(), nil, "grok-4.3", PlatformGrok)

	require.False(t, diag.HasAccountsInPool, "Grok 诊断不能把 OpenAI 账号算进账号池")
	require.False(t, diag.HasModelSupport)
}
