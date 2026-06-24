package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDataShareCaptureDurationRecorderBucketsAndPercentiles(t *testing.T) {
	recorder := newDataShareCaptureDurationRecorder(3)

	recorder.Observe(DataShareCaptureDurationPartFlushTotal, 5*time.Millisecond)
	recorder.Observe(DataShareCaptureDurationPartFlushTotal, 60*time.Millisecond)
	recorder.Observe(DataShareCaptureDurationPartFlushTotal, 1500*time.Millisecond)

	stats := recorder.Snapshot()
	require.Equal(t, minDataSharingCaptureDurationWindowSize, stats.WindowSize)
	part := findDataShareCaptureDurationPart(t, stats, DataShareCaptureDurationPartFlushTotal)
	require.Equal(t, 3, part.SampleCount)
	require.Equal(t, int64(1500), part.LastMillis)
	require.Equal(t, int64(60), part.P50Millis)
	require.Equal(t, int64(1500), part.P95Millis)
	require.Equal(t, 1, durationBucketCount(part, "<10ms"))
	require.Equal(t, 1, durationBucketCount(part, "50-100ms"))
	require.Equal(t, 1, durationBucketCount(part, "1-2s"))
}

func TestDataShareCaptureDurationRecorderEmptySnapshotIncludesAllParts(t *testing.T) {
	recorder := newDataShareCaptureDurationRecorder(64)

	stats := recorder.Snapshot()

	require.Equal(t, 64, stats.WindowSize)
	require.Zero(t, stats.SampleCount)
	require.Len(t, stats.Parts, len(dataShareCaptureDurationPartDefinitions))
	for _, part := range stats.Parts {
		require.Zero(t, part.SampleCount)
		require.Len(t, part.Buckets, len(dataShareCaptureDurationBucketDefinitions))
	}
}

func TestDataShareCaptureDurationRecorderResizeKeepsLatestSamples(t *testing.T) {
	recorder := newDataShareCaptureDurationRecorder(64)
	for i := 0; i < 40; i++ {
		recorder.Observe(DataShareCaptureDurationPartDBWrite, time.Duration(i)*time.Millisecond)
	}

	recorder.SetWindowSize(32)

	part := findDataShareCaptureDurationPart(t, recorder.Snapshot(), DataShareCaptureDurationPartDBWrite)
	require.Equal(t, 32, part.SampleCount)
	require.Equal(t, int64(39), part.LastMillis)
	require.Equal(t, int64(39), part.MaxMillis)
	require.Equal(t, 2, part.Buckets[0].Count)
}

func TestNormalizeDataShareCaptureRuntimeSettingsClampsDurationWindow(t *testing.T) {
	settings := normalizeDataShareCaptureRuntimeSettings(DataShareCaptureRuntimeSettings{
		WorkerCount:            1,
		QueueSize:              1,
		TaskTimeoutSeconds:     1,
		BufferIdleFlushSeconds: 1,
		BufferMaxSessions:      1,
		BufferMaxPendingEvents: 1,
		DurationWindowSize:     maxDataSharingCaptureDurationWindowSize + 1,
	})

	require.Equal(t, maxDataSharingCaptureDurationWindowSize, settings.DurationWindowSize)

	settings.DurationWindowSize = 1
	require.Equal(t, minDataSharingCaptureDurationWindowSize, normalizeDataShareCaptureRuntimeSettings(settings).DurationWindowSize)
}

func findDataShareCaptureDurationPart(t *testing.T, stats DataShareCaptureDurationStats, key DataShareCaptureDurationPartKey) DataShareCaptureDurationPart {
	t.Helper()
	for _, part := range stats.Parts {
		if part.Key == string(key) {
			return part
		}
	}
	t.Fatalf("duration part %s not found", key)
	return DataShareCaptureDurationPart{}
}

func findDataShareExportDurationPart(t *testing.T, stats DataShareExportDurationStats, key DataShareExportDurationPartKey) DataShareExportDurationPart {
	t.Helper()
	for _, part := range stats.Parts {
		if part.Key == string(key) {
			return part
		}
	}
	t.Fatalf("export duration part %s not found", key)
	return DataShareExportDurationPart{}
}

func durationBucketCount(part DataShareCaptureDurationPart, label string) int {
	for _, bucket := range part.Buckets {
		if bucket.Label == label {
			return bucket.Count
		}
	}
	return 0
}
