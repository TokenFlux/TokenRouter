package provider

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 真实 cron 构造不注册任务，停止后也不允许新的注册或启动。
func TestScheduledCronLifecycle(t *testing.T) {
	for _, stopFirst := range []bool{true, false} {
		schedule, ok := NewScheduledCron(time.UTC).(*ScheduledCron)
		require.True(t, ok)
		require.Empty(t, schedule.cron.Entries())
		if stopFirst {
			require.NoError(t, schedule.Stop(context.Background()))
		}
		require.NoError(t, schedule.Start(context.Background(), func() {}))
		require.NoError(t, schedule.Start(context.Background(), func() {}))
		if stopFirst {
			require.Empty(t, schedule.cron.Entries())
		} else {
			require.Len(t, schedule.cron.Entries(), 1)
		}
		require.NoError(t, schedule.Stop(context.Background()))
	}
}

func TestScheduledCronRejectsInvalidExpression(t *testing.T) {
	_, err := NextScheduledTestRun("not a cron", time.Now())
	require.Error(t, err)
}
