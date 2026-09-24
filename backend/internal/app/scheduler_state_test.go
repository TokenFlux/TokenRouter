package app

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

// 原生恢复与执行端必须观察同一停调代次，不能各自创建状态副本。
func TestAccountRuntimeBlockBindingSharesRecoveryFence(t *testing.T) {
	state := account.NewRuntimeBlockState(time.Now)
	gateway := &service.OpenAIGatewayService{}
	gateway.BindRuntimeBlockState(state)
	value := &gatewayprovider.ExecutionAccount{Record: account.Record{LoadLocation: time.LoadLocation, ID: 71, Platform: account.PlatformOpenAI}}
	gateway.BlockAccountScheduling(value, time.Now().Add(time.Minute), "装配合同")
	fence := state.ManagedRecoveryFence(value.Record.ID)
	require.NotZero(t, fence)
	require.Equal(t, fence, gateway.ManagedRecoveryFence(value.Record.ID))
	require.True(t, state.Blocked(value.Record.ID, func() string { return "" }))
	require.True(t, gateway.ClearAccountSchedulingBlockIfFence(value.Record.ID, fence))
	require.False(t, state.Blocked(value.Record.ID, func() string { return "" }))
	require.Greater(t, state.ManagedRecoveryFence(value.Record.ID), fence)
}

// 实际装配必须把新反馈及兼容参数入口绑定到同一实例。
func TestSchedulerSharedStateBindsLegacyConsumers(t *testing.T) {
	state := provideSchedulerSharedState(nil, nil)
	gateway := &service.OpenAIGatewayService{}
	gateway.BindSchedulerStickyStats(state.Sticky)
	require.Equal(t, int64(0), gateway.SnapshotOpenAICompatibilityFallbackMetrics().SessionHashLegacyReadFallbackTotal)
	other := provideSchedulerSharedState(nil, nil)
	require.NotSame(t, state.Settings, other.Settings)
	require.NotSame(t, state.Sticky, other.Sticky)
	// 从已绑定的真实执行入口上报，第二份装配不能改变原反馈作用域。
	selection.NewGeneric(selection.GenericDependencies{Shared: provideSelectionShared(nil, nil, nil, nil, state)}, selection.DefaultOptions()).ReportAdvancedAccountScheduleResult(&gatewayprovider.SelectionResult{AdvancedScheduler: true}, 51, false, nil)
	observed, _, _ := state.Feedback.Snapshot(51)
	require.Greater(t, observed, 0.0)
	untouched, _, _ := other.Feedback.Snapshot(51)
	require.Zero(t, untouched)
}

// 原生任务拥有者必须等待两个执行入口，停止后不能退回未跟踪 goroutine。
func TestGatewayBackgroundTasksUseApplicationOwner(t *testing.T) {
	for _, name := range []string{"messages", "openai"} {
		t.Run(name, func(t *testing.T) {
			tasks := lifecycle.NewTasks()
			openai := &service.OpenAIGatewayService{}
			bindGatewayBackground(tasks, openai)
			run := gatewayCommitEffects(nil, nil, nil, nil, nil, tasks, nil).Funds.Background
			if name == "openai" {
				run = openai.RunBackgroundTask
			}
			started := make(chan struct{})
			release := make(chan struct{})
			require.True(t, run("contract", func() { close(started); <-release }))
			<-started
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			err := tasks.Stop(ctx)
			close(release)
			require.ErrorContains(t, err, "contract=1")
			finish, done := context.WithTimeout(context.Background(), time.Second)
			defer done()
			require.NoError(t, tasks.Stop(finish))
			require.False(t, run("late", func() { t.Error("停止后不得执行任务") }))
		})
	}
}
