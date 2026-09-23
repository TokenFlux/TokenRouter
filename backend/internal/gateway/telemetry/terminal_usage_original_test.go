//go:build unit

package telemetry

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSampleOpenAIMissingUsageLogRateLimitsAndCounts(t *testing.T) {
	var sampler openAIMissingUsageLogSampler
	now := time.Unix(100, 0)
	logNow, total, suppressed := sampler.sample(now)
	require.True(t, logNow)
	require.Equal(t, uint64(1), total)
	require.Zero(t, suppressed)

	logNow, total, _ = sampler.sample(now.Add(time.Second))
	require.False(t, logNow)
	require.Equal(t, uint64(2), total)

	logNow, total, suppressed = sampler.sample(now.Add(openAIMissingUsageLogInterval))
	require.True(t, logNow)
	require.Equal(t, uint64(3), total)
	require.Equal(t, uint64(1), suppressed)
}
