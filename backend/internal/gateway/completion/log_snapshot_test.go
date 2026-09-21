package completion

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// 首次存储调用改动自身输入且失败时，同步兜底仍应取得原始事实。
type mutatingUsageWriter struct {
	bestCalls int
	syncCalls int
	recorded  *UsageLog
}

func (w *mutatingUsageWriter) CreateBestEffort(_ context.Context, row *UsageLog) error {
	w.bestCalls++
	row.ActualCost = 99
	return errors.New("queue rejected")
}
func (w *mutatingUsageWriter) Create(_ context.Context, row *UsageLog) (bool, error) {
	w.syncCalls++
	w.recorded = row
	return true, nil
}
func TestSnapshotLogWriterKeepsFallbackFactIsolated(t *testing.T) {
	target := &mutatingUsageWriter{}
	row := &UsageLog{ActualCost: 1.25}
	recorder := NewRecorder(Dependencies{Logs: SnapshotLogWriter(target)}, RecorderOptions{})
	recorder.WriteUsage(context.Background(), row, "test")
	require.Equal(t, 1, target.bestCalls)
	require.Equal(t, 1, target.syncCalls)
	require.InDelta(t, 1.25, target.recorded.ActualCost, 1e-12)
	require.InDelta(t, 1.25, row.ActualCost, 1e-12)
	require.NotSame(t, row, target.recorded)
}
