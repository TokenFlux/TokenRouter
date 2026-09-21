//go:build unit

package batchimage_test

import (
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	"github.com/stretchr/testify/require"
)

func TestCanTransitionBatchImageJob(t *testing.T) {
	tests := []struct {
		name string
		from string
		to   string
		want bool
	}{
		{name: "created_to_uploading", from: batchimage.BatchImageJobStatusCreated, to: batchimage.BatchImageJobStatusUploading, want: true},
		{name: "uploading_to_submitted", from: batchimage.BatchImageJobStatusUploading, to: batchimage.BatchImageJobStatusSubmitted, want: true},
		{name: "submitted_to_running", from: batchimage.BatchImageJobStatusSubmitted, to: batchimage.BatchImageJobStatusRunning, want: true},
		{name: "running_self_poll", from: batchimage.BatchImageJobStatusRunning, to: batchimage.BatchImageJobStatusRunning, want: true},
		{name: "running_to_indexing", from: batchimage.BatchImageJobStatusRunning, to: batchimage.BatchImageJobStatusIndexing, want: true},
		{name: "indexing_to_settling", from: batchimage.BatchImageJobStatusIndexing, to: batchimage.BatchImageJobStatusSettling, want: true},
		{name: "settling_to_completed", from: batchimage.BatchImageJobStatusSettling, to: batchimage.BatchImageJobStatusCompleted, want: true},
		{name: "submitted_to_cancelled", from: batchimage.BatchImageJobStatusSubmitted, to: batchimage.BatchImageJobStatusCancelled, want: true},
		{name: "non_terminal_to_failed", from: batchimage.BatchImageJobStatusCreated, to: batchimage.BatchImageJobStatusFailed, want: true},
		{name: "completed_to_output_deleted", from: batchimage.BatchImageJobStatusCompleted, to: batchimage.BatchImageJobStatusOutputDeleted, want: true},
		{name: "failed_to_output_deleted", from: batchimage.BatchImageJobStatusFailed, to: batchimage.BatchImageJobStatusOutputDeleted, want: true},
		{name: "cancelled_to_output_deleted", from: batchimage.BatchImageJobStatusCancelled, to: batchimage.BatchImageJobStatusOutputDeleted, want: true},
		{name: "created_to_running_invalid", from: batchimage.BatchImageJobStatusCreated, to: batchimage.BatchImageJobStatusRunning, want: false},
		{name: "completed_to_running_invalid", from: batchimage.BatchImageJobStatusCompleted, to: batchimage.BatchImageJobStatusRunning, want: false},
		{name: "output_deleted_to_failed_invalid", from: batchimage.BatchImageJobStatusOutputDeleted, to: batchimage.BatchImageJobStatusFailed, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, batchimage.CanTransitionBatchImageJob(tt.from, tt.to))
		})
	}
}

func TestIsTerminalBatchImageJobStatus(t *testing.T) {
	require.True(t, batchimage.IsTerminalBatchImageJobStatus(batchimage.BatchImageJobStatusCompleted))
	require.True(t, batchimage.IsTerminalBatchImageJobStatus(batchimage.BatchImageJobStatusFailed))
	require.True(t, batchimage.IsTerminalBatchImageJobStatus(batchimage.BatchImageJobStatusCancelled))
	require.True(t, batchimage.IsTerminalBatchImageJobStatus(batchimage.BatchImageJobStatusOutputDeleted))
	require.False(t, batchimage.IsTerminalBatchImageJobStatus(batchimage.BatchImageJobStatusRunning))
}

func TestIsSupportedBatchImageProvider(t *testing.T) {
	require.True(t, batchimage.IsSupportedBatchImageProvider(batchimage.BatchImageProviderGeminiAPI))
	require.True(t, batchimage.IsSupportedBatchImageProvider(batchimage.BatchImageProviderVertex))
	require.False(t, batchimage.IsSupportedBatchImageProvider("gemini_oauth"))
	require.False(t, batchimage.IsSupportedBatchImageProvider(""))
}

func TestNewBatchImageID(t *testing.T) {
	id, err := batchimage.NewBatchImageID()
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(id, "imgbatch_"))
	require.Len(t, id, len("imgbatch_")+32)
}
