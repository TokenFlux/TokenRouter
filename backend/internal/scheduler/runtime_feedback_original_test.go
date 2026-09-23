// 反馈契约直接验证唯一调度实例，不经过平台包装。
package scheduler_test

import (
	"sync"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
	"github.com/stretchr/testify/require"
)

func TestAdvancedSchedulerRuntimeStatsUsesIndependentEWMAFactors(t *testing.T) {
	stats := scheduler.NewRuntimeStats(time.Now)
	stats.Report(31, false, nil, policy.FeedbackConfig{ErrorRateAlpha: 0.8, TtftAlpha: 0.4})
	feedback := stats.FeedbackSnapshot(31)
	require.InDelta(t, 0.8, feedback.ErrorRate, 0.000001)

	firstTTFT := 100
	stats.Report(31, true, &firstTTFT, policy.FeedbackConfig{ErrorRateAlpha: 0.5, TtftAlpha: 0.25})
	secondTTFT := 200
	stats.Report(31, true, &secondTTFT, policy.FeedbackConfig{ErrorRateAlpha: 0.5, TtftAlpha: 0.25})
	feedback = stats.FeedbackSnapshot(31)
	require.InDelta(t, 0.2, feedback.ErrorRate, 0.000001)
	require.InDelta(t, 125, feedback.TTFT, 0.000001)
}

func TestAdvancedSchedulerRuntimeFeedbackSnapshot_RecordsSamplesAndObservedTime(t *testing.T) {
	stats := scheduler.NewRuntimeStats(time.Now)
	ttft := 240
	stats.Report(401, true, &ttft)
	stats.Report(401, false, nil)

	snapshot := stats.FeedbackSnapshot(401)
	require.True(t, snapshot.HasFeedback)
	require.EqualValues(t, 2, snapshot.ErrorSamples)
	require.NotNil(t, snapshot.LastObservedAt)
	require.True(t, snapshot.HasTTFT)
	require.EqualValues(t, 1, snapshot.TTFTSamples)
	require.NotNil(t, snapshot.LastTTFTAt)
	require.InDelta(t, 0.2, snapshot.ErrorRate, 0.000001)
	require.InDelta(t, 240, snapshot.TTFT, 0.000001)
}

func TestOpenAIAccountRuntimeStats_ReportAndSnapshot(t *testing.T) {
	stats := scheduler.NewRuntimeStats(time.Now)
	stats.Report(1001, true, nil)
	firstTTFT := 100
	stats.Report(1001, false, &firstTTFT)
	secondTTFT := 200
	stats.Report(1001, false, &secondTTFT)

	errorRate, ttft, hasTTFT := stats.Snapshot(1001)
	require.True(t, hasTTFT)
	require.InDelta(t, 0.36, errorRate, 1e-9)
	require.InDelta(t, 120.0, ttft, 1e-9)
	require.Equal(t, 1, stats.Size())
}

func TestOpenAIAccountRuntimeStats_ReportConcurrent(t *testing.T) {
	stats := scheduler.NewRuntimeStats(time.Now)

	const (
		accountCount = 4
		workers      = 16
		iterations   = 800
	)
	var wg sync.WaitGroup
	wg.Add(workers)
	for worker := 0; worker < workers; worker++ {
		worker := worker
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				accountID := int64(i%accountCount + 1)
				success := (i+worker)%3 != 0
				ttft := 80 + (i+worker)%40
				stats.Report(accountID, success, &ttft)
			}
		}()
	}
	wg.Wait()

	require.Equal(t, accountCount, stats.Size())
	for accountID := int64(1); accountID <= accountCount; accountID++ {
		errorRate, ttft, hasTTFT := stats.Snapshot(accountID)
		require.GreaterOrEqual(t, errorRate, 0.0)
		require.LessOrEqual(t, errorRate, 1.0)
		require.True(t, hasTTFT)
		require.Greater(t, ttft, 0.0)
	}
}
