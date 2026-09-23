//go:build unit

package selection

import (
	"context"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	scheduler "github.com/TokenFlux/TokenRouter/internal/scheduler"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// newBedrockRoutingTestAccount 使用虚构凭据构造可调度账号，测试不会访问真实 AWS。
func newBedrockRoutingTestAccount(id int64, region string, forceGlobal bool) gatewayprovider.ExecutionAccount {
	account := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: id, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeBedrock,
		Status: billing.StatusActive, Schedulable: true, Concurrency: 5, Priority: int(id),
		Credentials: map[string]any{
			"aws_region": region, "auth_mode": "sigv4",
			"aws_access_key_id": "test-akid", "aws_secret_access_key": "test-secret",
		}},
	}
	if forceGlobal {
		account.Record.Credentials["aws_force_global"] = "true"
	}
	return account
}

// 正式转发与管理员测试必须使用相同 ID；全局推理不改变来源端点和 SigV4 签名范围。

// 无有效路由时不调用上游或写账号状态，管理员仅在确有全局能力时收到开启提示。

// 地域不支持的粘性账号必须被跳过，账号筛选和错误诊断应使用相同的区域规则。
func TestBedrockRegionRouting_SchedulerAndDiagnosisAgree(t *testing.T) {
	groupID := int64(5200)
	invalid := newBedrockRoutingTestAccount(1, "ap-northeast-1", false)
	valid := newBedrockRoutingTestAccount(2, "us-east-1", false)
	for _, loadBatchEnabled := range []bool{false, true} {
		for _, withValid := range []bool{false, true} {
			name := "全部无效"
			if withValid {
				name = "存在有效账号"
			}
			if loadBatchEnabled {
				name += "批量负载"
			}
			t.Run(name, func(t *testing.T) {
				accounts := []gatewayprovider.ExecutionAccount{invalid}
				if withValid {
					accounts = append(accounts, valid)
				}
				repo := &mockAccountRepoForPlatform{accounts: accounts, accountsByID: map[int64]*gatewayprovider.ExecutionAccount{}}
				for i := range repo.accounts {
					repo.accounts[i].Record.AccountGroups = []accountcore.GroupMembership{{AccountID: repo.accounts[i].Record.ID, GroupID: groupID}}
					repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
				}
				group := &routing.Group{ID: groupID, Platform: capability.PlatformAnthropic, Status: billing.StatusActive, Hydrated: true}
				cfg := testConfig()
				cfg.Gateway.Scheduling.LoadBatchEnabled = loadBatchEnabled
				gateway := newGenericSelectionForTest(GenericDependencies{
					Reads: Reads{
						Accounts: repo,

						Groups: &mockGroupRepoForGateway{groups: map[int64]*routing.Group{groupID: group}},
					},
					Shared: Shared{
						Cache:       &mockGatewayCacheForPlatform{sessionBindings: map[string]int64{"sticky": 1}},
						Concurrency: scheduler.NewConcurrencyService(&mockConcurrencyCache{}, scheduler.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
					},
				}, cfg)

				diagnosis := gatewayprovider.NewModelAvailability(selectionAvailabilityFixture{repo}, nil, false, false).DiagnoseGeneral(context.Background(), &groupID, "claude-sonnet-5", capability.PlatformAnthropic)
				require.True(t, diagnosis.HasAccountsInPool)
				require.Equal(t, withValid, diagnosis.HasModelSupport)
				selected, err := gateway.SelectAccountWithLoadAwareness(context.Background(), &groupID, "sticky", "claude-sonnet-5", nil, "", 0)
				if withValid {
					require.NoError(t, err)
					require.Equal(t, valid.Record.ID, selected.Account.Record.ID)
					if selected.ReleaseFunc != nil {
						selected.ReleaseFunc()
					}
				} else {
					require.Error(t, err)
					require.Nil(t, selected)
				}
			})
		}
	}
}

// 模型广场通过公共模型解析器获得相同的区域可用性，不自行回退到默认目录。

// bedrockMarketplaceGroups 仅提供区域路由回归所需的分组数据。

func (m *mockAccountRepoForPlatform) availabilityRecords(_ context.Context, groupID *int64, platforms []string, includeGrouped bool) ([]gatewayprovider.ExecutionAccount, error) {
	platformSet := make(map[string]struct{}, len(platforms))
	for _, platform := range platforms {
		platformSet[platform] = struct{}{}
	}
	result := make([]gatewayprovider.ExecutionAccount, 0, len(m.accounts))
	for _, acc := range m.accounts {
		if _, ok := platformSet[acc.Record.Platform]; !ok || acc.Record.Status != billing.StatusActive || !acc.Record.Schedulable {
			continue
		}
		if groupID != nil {
			inGroup := false
			for _, accountGroup := range acc.Record.AccountGroups {
				if accountGroup.GroupID == *groupID {
					inGroup = true
					break
				}
			}
			if !inGroup {
				continue
			}
		} else if !includeGrouped && (len(acc.Record.AccountGroups) > 0 || len(acc.Record.GroupIDs) > 0) {
			continue
		}
		result = append(result, acc)
	}
	return result, nil
}

// selectionAvailabilityFixture 保留原候选查询过滤，只投影诊断需要的记录。
type selectionAvailabilityFixture struct{ *mockAccountRepoForPlatform }

func (s selectionAvailabilityFixture) ListModelAvailabilityCandidates(ctx context.Context, group *int64, platforms []string, all bool) ([]accountcore.Record, error) {
	values, err := s.availabilityRecords(ctx, group, platforms, all)
	out := make([]accountcore.Record, len(values))
	for i := range values {
		out[i] = values[i].Record
	}
	return out, err
}
