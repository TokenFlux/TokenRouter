//go:build unit

package creative_test

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/stretchr/testify/require"
)

// TestCanTransitionCreativeRun 校验创作台任务状态机。
func TestCanTransitionCreativeRun(t *testing.T) {
	valid := []struct{ from, to string }{
		{creative.CreativeRunStatusQueued, creative.CreativeRunStatusRunning},
		{creative.CreativeRunStatusQueued, creative.CreativeRunStatusCancelled},
		// 创建失败回滚路径允许 queued 直接转 failed。
		{creative.CreativeRunStatusQueued, creative.CreativeRunStatusFailed},
		// worker 恢复发现载荷过期时允许 queued 直接转 result_lost。
		{creative.CreativeRunStatusQueued, creative.CreativeRunStatusResultLost},
		{creative.CreativeRunStatusRunning, creative.CreativeRunStatusSucceeded},
		{creative.CreativeRunStatusRunning, creative.CreativeRunStatusFailed},
		{creative.CreativeRunStatusRunning, creative.CreativeRunStatusCancelled},
		{creative.CreativeRunStatusRunning, creative.CreativeRunStatusResultLost},
		// 成功任务的临时输出过期后可降级为 result_lost。
		{creative.CreativeRunStatusSucceeded, creative.CreativeRunStatusResultLost},
	}
	for _, tc := range valid {
		require.True(t, creative.CanTransitionCreativeRun(tc.from, tc.to), "%s -> %s 应当合法", tc.from, tc.to)
	}

	invalid := []struct{ from, to string }{
		{"", creative.CreativeRunStatusRunning},
		{creative.CreativeRunStatusQueued, ""},
		{creative.CreativeRunStatusQueued, creative.CreativeRunStatusSucceeded},
		{creative.CreativeRunStatusRunning, creative.CreativeRunStatusQueued},
		{creative.CreativeRunStatusRunning, creative.CreativeRunStatusRunning},
		{creative.CreativeRunStatusSucceeded, creative.CreativeRunStatusRunning},
		{creative.CreativeRunStatusFailed, creative.CreativeRunStatusRunning},
		{creative.CreativeRunStatusCancelled, creative.CreativeRunStatusRunning},
		{creative.CreativeRunStatusResultLost, creative.CreativeRunStatusRunning},
		{creative.CreativeRunStatusSucceeded, creative.CreativeRunStatusFailed},
		{creative.CreativeRunStatusFailed, creative.CreativeRunStatusResultLost},
	}
	for _, tc := range invalid {
		require.False(t, creative.CanTransitionCreativeRun(tc.from, tc.to), "%s -> %s 应当非法", tc.from, tc.to)
	}
}

func TestIsTerminalCreativeRunStatus(t *testing.T) {
	for _, status := range []string{creative.CreativeRunStatusSucceeded, creative.CreativeRunStatusFailed, creative.CreativeRunStatusCancelled, creative.CreativeRunStatusResultLost} {
		require.True(t, creative.IsTerminalCreativeRunStatus(status))
	}
	for _, status := range []string{creative.CreativeRunStatusQueued, creative.CreativeRunStatusRunning, ""} {
		require.False(t, creative.IsTerminalCreativeRunStatus(status))
	}
}

func TestIsValidCreativeRunID(t *testing.T) {
	require.True(t, creative.IsValidCreativeRunID("crun_0123456789abcdef"))
	require.False(t, creative.IsValidCreativeRunID("imgbatch_0123"))
	require.False(t, creative.IsValidCreativeRunID("crun_"))
	require.False(t, creative.IsValidCreativeRunID(""))
}

func TestNewCreativeRunID(t *testing.T) {
	runID, err := creative.NewCreativeRunID()
	require.NoError(t, err)
	require.True(t, creative.IsValidCreativeRunID(runID))
	other, err := creative.NewCreativeRunID()
	require.NoError(t, err)
	require.NotEqual(t, runID, other)
}

// TestBuildCreativeRequestFingerprint 校验指纹确定性：输入不变指纹不变，任一字段变化指纹变化。
func TestBuildCreativeRequestFingerprint(t *testing.T) {
	base := creative.CreativeFingerprintPayload{
		GroupID:      12,
		Model:        "gemini-3.1-flash-image",
		Operation:    creative.CreativeOperationGenerate,
		PromptSHA256: creative.Sha256Hex([]byte("hello")),
		ImageSHA256:  []string{creative.Sha256Hex([]byte("img"))},
		ImageSize:    "1K",
		AspectRatio:  "1:1",
		OutputCount:  1,
	}
	first := creative.BuildCreativeRequestFingerprint(base)
	require.NotEmpty(t, first)
	require.Equal(t, first, creative.BuildCreativeRequestFingerprint(base))

	changedPrompt := base
	changedPrompt.PromptSHA256 = creative.Sha256Hex([]byte("world"))
	require.NotEqual(t, first, creative.BuildCreativeRequestFingerprint(changedPrompt))

	changedGroup := base
	changedGroup.GroupID = 13
	require.NotEqual(t, first, creative.BuildCreativeRequestFingerprint(changedGroup))
}
