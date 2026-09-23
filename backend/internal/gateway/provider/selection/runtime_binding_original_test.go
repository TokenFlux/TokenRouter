package selection

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
	shared := Shared{Feedback: feedback, Parameters: settings}
	messages := NewGeneric(GenericDependencies{Shared: shared}, DefaultOptions())
	openai := NewCompatible(CompatibleDependencies{Shared: shared}, DefaultOptions())
	gemini := NewGemini(GeminiDependencies{Shared: shared}, DefaultOptions())
	diagnostics := NewDiagnostics(nil, shared, messages, openai)
	require.Same(t, feedback, messages.advancedSchedulerStats())
	require.Same(t, feedback, openai.openaiAccountStats)
	require.Same(t, feedback, gemini.advancedSchedulerStats())
	require.Same(t, feedback, diagnostics.feedback)
	require.Same(t, settings, messages.schedulerParameters)
	require.Same(t, settings, openai.schedulerParameters)
	require.Same(t, settings, gemini.schedulerParameters)
	require.Same(t, settings, diagnostics.schedulerParameters)
}
