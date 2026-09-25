package httpapi

import (
	"context"
	"strings"
	"testing"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
	"github.com/stretchr/testify/require"

	selectionadapter "github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// streamSelectionDiagnosticSource 为真实流执行后的下一次选择提供可调度查询投影。
// 流夹具原本只提供传输字段；诊断 API 所需的分组与活动状态只补在查询副本中。
type streamSelectionDiagnosticSource struct {
	value gatewayprovider.ExecutionAccount
	group routing.Group
}

func (s streamSelectionDiagnosticSource) GetAccount(context.Context, int64) (*gatewayprovider.ExecutionAccount, error) {
	return &s.value, nil
}

func (s streamSelectionDiagnosticSource) GetGroup(context.Context, int64) (*routing.Group, error) {
	return &s.group, nil
}

func (s streamSelectionDiagnosticSource) ListAccountsForSchedulerScoreFilter(context.Context, string, string, string, string, int64, string) ([]gatewayprovider.ExecutionAccount, error) {
	return []gatewayprovider.ExecutionAccount{s.value}, nil
}

func (s streamSelectionDiagnosticSource) ListSchedulableAccountsForAdvancedSchedulerScore(context.Context, *int64, string) ([]gatewayprovider.ExecutionAccount, error) {
	return []gatewayprovider.ExecutionAccount{s.value}, nil
}

// selectionDiagnosticForStreamTest 通过实际诊断接口观察同一代理熔断实例。
func selectionDiagnosticForStreamTest(t *testing.T, source *OpenAIResponsesExecutor, value *gatewayprovider.ExecutionAccount) (bool, string) {
	t.Helper()
	copy := *value
	copy.Record.Status = "active"
	copy.Record.Schedulable = true
	copy.Record.GroupIDs = []int64{1}
	projection := streamSelectionDiagnosticSource{value: copy, group: routing.Group{ID: 1, Platform: value.Record.Platform, Status: "active", Hydrated: true, SchedulerType: routing.GroupSchedulerTypeAdvanced}}
	parameters := scheduler.NewParameters(scheduler.NewSettingsRuntime(scheduler.Diagnostics{}), nil, scheduler.DefaultParameters())
	choices := selectionadapter.NewCompatible(selectionadapter.CompatibleDependencies{Responses: source.Lineage.Store, RuntimeBlocks: source.Output.Health.Runtime, ModelTransient: source.Output.Health.ModelTransient, ProxyCircuit: source.Output.ProxyCircuit}, selectionadapter.DefaultOptions())
	diagnostic := selectionadapter.NewDiagnostics(projection, selectionadapter.Shared{Parameters: parameters}, nil, choices)
	result, err := diagnostic.GetDetail(context.Background(), value.Record.ID, policy.AdvancedSchedulerScoreDiagnosticRequest{GroupID: 1})
	require.NoError(t, err)
	require.NotNil(t, result.Detail)
	return result.Detail.Eligible, strings.Join(result.Detail.HardFilterReasons, ",")
}
