package provider

import (
	"testing"

	"github.com/shirou/gopsutil/v4/mem"
	"github.com/stretchr/testify/require"
)

const (
	testMiB = 1024 * 1024
	testGiB = 1024 * testMiB
)

// TestResolveMemoryStatsCgroupUsageButUnlimitedFallsBackToHost 覆盖核心回归：
// Docker + cgroup v2 未设置内存上限时，memory.current 是较小的容器值，而 memory.max
// 为 "max"（因此 cgroupTotal == 0）。旧逻辑会把容器 used 与宿主机 total 相除，
// 得到误导性的极小百分比；修复后必须整体回退到宿主机指标。
func TestResolveMemoryStatsCgroupUsageButUnlimitedFallsBackToHost(t *testing.T) {
	const cgroupUsed = uint64(64573440) // memory.current，约 61 MiB。
	host := &mem.VirtualMemoryStat{
		Used:        16 * testGiB,
		Total:       24 * testGiB,
		UsedPercent: 66.7,
	}

	usedMB, totalMB, pct := resolveMemoryStats(cgroupUsed, 0 /* memory.max = "max" */, true, host)

	require.NotNil(t, usedMB)
	require.NotNil(t, totalMB)
	require.NotNil(t, pct)

	// 三个值都必须来自宿主机，而不是 cgroup 容器值。
	require.Equal(t, int64(16*1024), *usedMB, "used must be host used, not container used")
	require.Equal(t, int64(24*1024), *totalMB, "total must be host total")
	require.InDelta(t, 66.7, *pct, 0.05, "percent must be host-derived, not container/host mix")

	// 防止回归到具体缺陷：容器 used（约 61 MiB）与宿主机 total 混算成约 0.3%。
	require.NotEqual(t, int64(cgroupUsed/testMiB), *usedMB, "must not report the container used value")
	require.Greater(t, *pct, 1.0, "percent must not collapse to the ~0.3%% mixed value")
}

// TestResolveMemoryStatsExplicitContainerLimitUsesCgroup 覆盖设置容器内存上限的情况：
// memory.current = 512 MiB、memory.max = 2 GiB 时应完全使用 cgroup 值得到约 25%，忽略宿主机。
func TestResolveMemoryStatsExplicitContainerLimitUsesCgroup(t *testing.T) {
	host := &mem.VirtualMemoryStat{
		Used:        16 * testGiB, // 刻意设置为不同值，必须忽略。
		Total:       24 * testGiB,
		UsedPercent: 66.7,
	}

	usedMB, totalMB, pct := resolveMemoryStats(512*testMiB, 2*testGiB, true, host)

	require.NotNil(t, usedMB)
	require.NotNil(t, totalMB)
	require.NotNil(t, pct)

	require.Equal(t, int64(512), *usedMB)
	require.Equal(t, int64(2048), *totalMB)
	require.InDelta(t, 25.0, *pct, 0.05)
}

// TestResolveMemoryStatsNoCgroupUsesHost 覆盖裸机或没有 cgroup 的主机：三个值都来自宿主机。
func TestResolveMemoryStatsNoCgroupUsesHost(t *testing.T) {
	host := &mem.VirtualMemoryStat{
		Used:        16 * testGiB,
		Total:       24 * testGiB,
		UsedPercent: 66.7,
	}

	usedMB, totalMB, pct := resolveMemoryStats(0, 0, false, host)

	require.NotNil(t, usedMB)
	require.NotNil(t, totalMB)
	require.NotNil(t, pct)
	require.Equal(t, int64(16*1024), *usedMB)
	require.Equal(t, int64(24*1024), *totalMB)
	require.InDelta(t, 66.7, *pct, 0.05)
}

// TestResolveMemoryStatsNoDataReturnsNil：cgroup 和宿主机数据都不可用时，所有输出均为空。
func TestResolveMemoryStatsNoDataReturnsNil(t *testing.T) {
	usedMB, totalMB, pct := resolveMemoryStats(0, 0, false, nil)
	require.Nil(t, usedMB)
	require.Nil(t, totalMB)
	require.Nil(t, pct)
}

// TestResolveMemoryStatsHostWithoutTotalKeepsGopsutilPercent：宿主机没有 total 时，
// 仍返回 used 值和 gopsutil 自身的百分比。
func TestResolveMemoryStatsHostWithoutTotalKeepsGopsutilPercent(t *testing.T) {
	host := &mem.VirtualMemoryStat{
		Used:        8 * testGiB,
		Total:       0,
		UsedPercent: 42.5,
	}

	usedMB, totalMB, pct := resolveMemoryStats(0, 0, false, host)

	require.NotNil(t, usedMB)
	require.Nil(t, totalMB)
	require.NotNil(t, pct)
	require.Equal(t, int64(8*1024), *usedMB)
	require.InDelta(t, 42.5, *pct, 0.05)
}
