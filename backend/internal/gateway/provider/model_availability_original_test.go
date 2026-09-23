//go:build unit

package provider_test

import (
	"context"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestDiagnoseModelAvailabilityForPlatform_NoModel_AlwaysAvailable(t *testing.T) {
	repo := &availabilityAccountStore{accounts: nil, accountsByID: map[int64]*gatewayprovider.ExecutionAccount{}}
	svc := gatewayprovider.NewModelAvailability(repo, nil, false, false)

	diag := svc.DiagnoseGeneral(context.Background(), nil, "", capability.PlatformOpenAI)

	require.True(t, diag.HasAccountsInPool, "空模型必须保守返回 HasAccountsInPool=true，让调用方继续走 503")
	require.True(t, diag.HasModelSupport, "空模型必须保守返回 HasModelSupport=true，让调用方继续走 503")
}

func TestDiagnoseModelAvailabilityForPlatform_EmptyPlatform_AlwaysAvailable(t *testing.T) {
	repo := &availabilityAccountStore{accounts: nil, accountsByID: map[int64]*gatewayprovider.ExecutionAccount{}}
	svc := gatewayprovider.NewModelAvailability(repo, nil, false, false)

	diag := svc.DiagnoseGeneral(context.Background(), nil, "gpt-5", "")

	require.True(t, diag.HasAccountsInPool)
	require.True(t, diag.HasModelSupport, "空平台必须回落到 {true,true}，让调用方继续走 503")
}

func TestDiagnoseModelAvailabilityForPlatform_NilReceiver(t *testing.T) {
	var svc *routing.ModelAvailability

	diag := svc.DiagnoseGeneral(context.Background(), nil, "gpt-5", capability.PlatformOpenAI)

	require.True(t, diag.HasAccountsInPool)
	require.True(t, diag.HasModelSupport)
}

func TestDiagnoseModelAvailabilityForPlatform_NoAccountsInPool(t *testing.T) {
	repo := &availabilityAccountStore{accounts: nil, accountsByID: map[int64]*gatewayprovider.ExecutionAccount{}}
	svc := gatewayprovider.NewModelAvailability(repo, nil, false, false)

	diag := svc.DiagnoseGeneral(context.Background(), nil, "gpt-5", capability.PlatformOpenAI)

	require.False(t, diag.HasAccountsInPool)
	require.False(t, diag.HasModelSupport, "没有账号表示没有模型支持；调用方会走空池 503 分支")
}

func TestDiagnoseModelAvailabilityForPlatform_ExplicitMappingMatches(t *testing.T) {
	repo := &availabilityAccountStore{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1,
				Platform:    capability.PlatformOpenAI,
				Status:      billing.StatusActive,
				Schedulable: true,
				Credentials: map[string]any{
					"model_mapping": map[string]any{"gpt-5.1-codex-mini": "gpt-5.1-codex-mini"},
				}},
			},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}
	svc := gatewayprovider.NewModelAvailability(repo, nil, false, false)

	diag := svc.DiagnoseGeneral(context.Background(), nil, "gpt-5.1-codex-mini", capability.PlatformOpenAI)

	require.True(t, diag.HasAccountsInPool)
	require.True(t, diag.HasModelSupport)
}

func TestDiagnoseModelAvailabilityForPlatform_EmptyMappingAllowsAll(t *testing.T) {
	repo := &availabilityAccountStore{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true} /* 无 ModelMapping 表示允许全部模型 */},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}
	svc := gatewayprovider.NewModelAvailability(repo, nil, false, false)

	diag := svc.DiagnoseGeneral(context.Background(), nil, "gpt-5.1-codex-mini", capability.PlatformOpenAI)

	require.True(t, diag.HasModelSupport, "空 model_mapping 必须按 Account.IsModelSupported 语义视为允许全部模型")
}

func TestDiagnoseModelAvailabilityForPlatform_WildcardMappingMatches(t *testing.T) {
	repo := &availabilityAccountStore{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1,
				Platform:    capability.PlatformOpenAI,
				Status:      billing.StatusActive,
				Schedulable: true,
				Credentials: map[string]any{
					"model_mapping": map[string]any{"*": "gpt-5"},
				}},
			},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}
	svc := gatewayprovider.NewModelAvailability(repo, nil, false, false)

	diag := svc.DiagnoseGeneral(context.Background(), nil, "gpt-5.1-codex-mini", capability.PlatformOpenAI)

	require.True(t, diag.HasModelSupport, "通配符映射必须把请求模型视为可服务")
}

func TestDiagnoseModelAvailabilityForPlatform_NoMatchingModel_ReturnsNotFoundSignal(t *testing.T) {
	groupID := int64(42)
	repo := &availabilityAccountStore{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1,
				Platform:    capability.PlatformOpenAI,
				Status:      billing.StatusActive,
				Schedulable: true,
				AccountGroups: []accountcore.GroupMembership{
					{GroupID: groupID},
				},
				Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5": "gpt-5"}}},
			},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2,
				Platform:    capability.PlatformOpenAI,
				Status:      billing.StatusActive,
				Schedulable: true,
				AccountGroups: []accountcore.GroupMembership{
					{GroupID: groupID},
				},
				Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5-mini": "gpt-5-mini"}}},
			},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}
	svc := gatewayprovider.NewModelAvailability(repo, nil, false, false)

	diag := svc.DiagnoseGeneral(context.Background(), &groupID, "gpt-5.1-codex-mini", capability.PlatformOpenAI)

	require.True(t, diag.HasAccountsInPool, "分组内存在 OpenAI 账号")
	require.False(t, diag.HasModelSupport, "没有账号映射允许该模型时 handler 应返回 404")
}

func TestDiagnoseModelAvailabilityForPlatform_RateLimitedSupportingAccountRemainsConfigured(t *testing.T) {
	groupID := int64(42)
	cooldownUntil := time.Now().Add(time.Hour)
	repo := &availabilityAccountStore{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1,
				Platform:               capability.PlatformAnthropic,
				Status:                 billing.StatusActive,
				Schedulable:            true,
				RateLimitResetAt:       &cooldownUntil,
				OverloadUntil:          &cooldownUntil,
				TempUnschedulableUntil: &cooldownUntil,
				AccountGroups:          []accountcore.GroupMembership{{GroupID: groupID}},
				Credentials: map[string]any{
					"model_mapping": map[string]any{"claude-opus-4-8": "claude-opus-4-8"},
				}},
			},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	require.False(t, repo.accounts[0].View().IsSchedulable(), "test account must be excluded from normal scheduling while cooling down")
	svc := gatewayprovider.NewModelAvailability(repo, nil, false, false)

	// 诊断必须绕过只反映瞬时状态的快照。

	diag := svc.DiagnoseGeneral(context.Background(), &groupID, "claude-opus-4-8", capability.PlatformAnthropic)

	require.True(t, diag.HasAccountsInPool)
	require.True(t, diag.HasModelSupport, "a configured model remains supported while every matching account is temporarily cooling down")
}

func TestOpenAIDiagnoseModelAvailabilityForPlatform_RateLimitedSupportingAccountRemainsConfigured(t *testing.T) {
	groupID := int64(43)
	cooldownUntil := time.Now().Add(time.Hour)
	repo := &availabilityAccountStore{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2,
				Platform:               capability.PlatformOpenAI,
				Status:                 billing.StatusActive,
				Schedulable:            true,
				RateLimitResetAt:       &cooldownUntil,
				OverloadUntil:          &cooldownUntil,
				TempUnschedulableUntil: &cooldownUntil,
				AccountGroups:          []accountcore.GroupMembership{{GroupID: groupID}},
				Credentials: map[string]any{
					"model_mapping": map[string]any{"claude-opus-4-8": "claude-opus-4-8"},
				}},
			},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	require.False(t, repo.accounts[0].View().IsSchedulable(), "test account must be excluded from normal scheduling while cooling down")
	svc := gatewayprovider.NewModelAvailability(repo, nil, false, true)

	// 诊断必须绕过只反映瞬时状态的快照。

	diag := svc.DiagnoseCompatible(context.Background(), &groupID, "claude-opus-4-8", capability.PlatformOpenAI)

	require.True(t, diag.HasAccountsInPool)
	require.True(t, diag.HasModelSupport, "OpenAI-compatible diagnosis must keep transiently limited supporting accounts in the configured pool")
}

func TestDiagnoseModelAvailabilityForPlatform_WrongPlatformFiltersOut(t *testing.T) {
	// 分组里只有 Anthropic 账号，但用户路由到 OpenAI 网关。
	// 诊断必须按平台过滤掉 Anthropic 账号，因此 HasAccountsInPool=false，调用方保留 503。
	repo := &availabilityAccountStore{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1,
				Platform:    capability.PlatformAnthropic,
				Status:      billing.StatusActive,
				Schedulable: true,
				Credentials: map[string]any{"model_mapping": map[string]any{"claude-sonnet-4-5": "claude-sonnet-4-5"}}},
			},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}
	svc := gatewayprovider.NewModelAvailability(repo, nil, false, false)

	diag := svc.DiagnoseGeneral(context.Background(), nil, "gpt-5", capability.PlatformOpenAI)

	require.False(t, diag.HasAccountsInPool, "OpenAI 路由不能把 Anthropic 账号算进账号池")
	require.False(t, diag.HasModelSupport)
}

func TestOpenAIGatewayDiagnoseModelAvailabilityForPlatform_GrokPlatformFiltersOpenAIAccounts(t *testing.T) {
	repo := &availabilityAccountStore{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1,
				Platform:    capability.PlatformOpenAI,
				Status:      billing.StatusActive,
				Schedulable: true,
				Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5": "gpt-5"}}},
			},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}
	svc := gatewayprovider.NewModelAvailability(repo, nil, false, true)

	diag := svc.DiagnoseCompatible(context.Background(), nil, "grok-4.3", capability.PlatformGrok)

	require.False(t, diag.HasAccountsInPool, "Grok 诊断不能把 OpenAI 账号算进账号池")
	require.False(t, diag.HasModelSupport)
}

// availabilityAccountStore 保留原持久候选夹具的过滤边界，不提供瞬时快照或选号能力。
type availabilityAccountStore struct {
	accounts     []gatewayprovider.ExecutionAccount
	accountsByID map[int64]*gatewayprovider.ExecutionAccount
}

func (m *availabilityAccountStore) ListModelAvailabilityCandidates(_ context.Context, groupID *int64, platforms []string, includeGrouped bool) ([]accountcore.Record, error) {
	platformSet := make(map[string]struct{}, len(platforms))
	for _, platform := range platforms {
		platformSet[platform] = struct{}{}
	}
	result := make([]accountcore.Record, 0, len(m.accounts))
	for _, value := range m.accounts {
		if _, ok := platformSet[value.Record.Platform]; !ok || value.Record.Status != billing.StatusActive || !value.Record.Schedulable {
			continue
		}
		if groupID != nil {
			inGroup := false
			for _, group := range value.Record.AccountGroups {
				if group.GroupID == *groupID {
					inGroup = true
					break
				}
			}
			if !inGroup {
				continue
			}
		} else if !includeGrouped && (len(value.Record.AccountGroups) > 0 || len(value.Record.GroupIDs) > 0) {
			continue
		}
		result = append(result, *gatewayprovider.ExecutionRecord(&value))
	}
	return result, nil
}
