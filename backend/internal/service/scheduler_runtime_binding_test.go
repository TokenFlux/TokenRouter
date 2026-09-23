package service

import (
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/stretchr/testify/require"
)

// 三种实际执行入口与诊断直接使用同一原生状态，健康适配不再代管。
func TestSchedulerRuntimeBindingKeepsSharedState(t *testing.T) {
	feedback := scheduler.NewRuntimeStats(time.Now)
	settings := scheduler.NewParameters(scheduler.NewSettingsRuntime(scheduler.Diagnostics{}), nil, scheduler.DefaultParameters())
	messages := withSchedulerParametersForTest(&GatewayService{})
	openai := withSchedulerParametersForTest(&OpenAIGatewayService{})
	gemini := withSchedulerParametersForTest(&GeminiMessagesCompatService{})
	diagnostics := withSchedulerParametersForTest(&AdvancedSchedulerScoreDiagnosticService{})
	messages.BindSchedulerRuntime(feedback, settings)
	openai.BindSchedulerRuntime(feedback, settings)
	gemini.BindSchedulerRuntime(feedback, settings)
	diagnostics.BindSchedulerRuntime(feedback, settings)
	require.Same(t, feedback, messages.advancedSchedulerStats())
	require.Same(t, feedback, openai.openaiAccountStats)
	require.Same(t, feedback, gemini.advancedSchedulerStats())
	require.Same(t, feedback, diagnostics.feedback)
	require.Same(t, settings, messages.schedulerParameters)
	require.Same(t, settings, openai.schedulerParameters)
	require.Same(t, settings, gemini.schedulerParameters)
	require.Same(t, settings, diagnostics.schedulerParameters)
}
