package provider

import (
	"context"
	"database/sql"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/redis/go-redis/v9"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
)

func (c *HostObserver) DbPoolStats() (active int, idle int) {
	if c == nil || c.db == nil {
		return 0, 0
	}
	stats := c.db.Stats()
	return stats.InUse, stats.Idle
}
func (c *HostObserver) RedisPoolStats() (total int, idle int, ok bool) {
	if c == nil || c.redisClient == nil {
		return 0, 0, false
	}
	stats := c.redisClient.PoolStats()
	if stats == nil {
		return 0, 0, false
	}
	return int(stats.TotalConns), int(stats.IdleConns), true
}
func (c *HostObserver) CheckRedis(ctx context.Context) bool {
	if c == nil || c.redisClient == nil {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return c.redisClient.Ping(ctx).Err() == nil
}
func (c *HostObserver) CheckDB(ctx context.Context) bool {
	if c == nil || c.db == nil {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var one int
	if err := c.db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return false
	}
	return one == 1
}
func readIntFile(path string) (int64, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
func readUintFile(path string) (uint64, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
func readCgroupCPULimitCores() float64 {
	// cgroup v2: cpu.max => "<quota> <period>" or "max <period>"
	if raw, err := os.ReadFile("/sys/fs/cgroup/cpu.max"); err == nil {
		fields := strings.Fields(string(raw))
		if len(fields) >= 2 && fields[0] != "max" {
			quota, err1 := strconv.ParseFloat(fields[0], 64)
			period, err2 := strconv.ParseFloat(fields[1], 64)
			if err1 == nil && err2 == nil && quota > 0 && period > 0 {
				return quota / period
			}
		}
	}

	// cgroup v1: cpu.cfs_quota_us / cpu.cfs_period_us
	quota, okQuota := readIntFile("/sys/fs/cgroup/cpu/cpu.cfs_quota_us")
	period, okPeriod := readIntFile("/sys/fs/cgroup/cpu/cpu.cfs_period_us")
	if okQuota && okPeriod && quota > 0 && period > 0 {
		return float64(quota) / float64(period)
	}

	return 0
}
func readCgroupCPUUsageNanos() (usageNanos uint64, ok bool) {
	// cgroup v2: cpu.stat has usage_usec
	if raw, err := os.ReadFile("/sys/fs/cgroup/cpu.stat"); err == nil {
		lines := strings.Split(string(raw), "\n")
		for _, line := range lines {
			fields := strings.Fields(line)
			if len(fields) != 2 {
				continue
			}
			if fields[0] != "usage_usec" {
				continue
			}
			v, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				continue
			}
			return v * 1000, true
		}
	}

	// cgroup v1: cpuacct.usage is in nanoseconds
	if v, ok := readUintFile("/sys/fs/cgroup/cpuacct/cpuacct.usage"); ok {
		return v, true
	}

	return 0, false
}
func readCgroupMemoryBytes() (usedBytes uint64, totalBytes uint64, ok bool) {
	// cgroup v2 (most common in modern containers)
	if used, ok1 := readUintFile("/sys/fs/cgroup/memory.current"); ok1 {
		usedBytes = used
		rawMax, err := os.ReadFile("/sys/fs/cgroup/memory.max")
		if err == nil {
			s := strings.TrimSpace(string(rawMax))
			if s != "" && s != "max" {
				if v, err := strconv.ParseUint(s, 10, 64); err == nil {
					totalBytes = v
				}
			}
		}
		return usedBytes, totalBytes, true
	}

	// cgroup v1 fallback
	if used, ok1 := readUintFile("/sys/fs/cgroup/memory/memory.usage_in_bytes"); ok1 {
		usedBytes = used
		if limit, ok2 := readUintFile("/sys/fs/cgroup/memory/memory.limit_in_bytes"); ok2 {
			// Some environments report a very large number when unlimited.
			if limit > 0 && limit < (1<<60) {
				totalBytes = limit
			}
		}
		return usedBytes, totalBytes, true
	}

	return 0, 0, false
}
func (c *HostObserver) TryCgroupCPUPercent(now time.Time) *float64 {
	usageNanos, ok := readCgroupCPUUsageNanos()
	if !ok {
		return nil
	}

	// Initialize baseline sample.
	if c.lastCgroupCPUSampleAt.IsZero() {
		c.lastCgroupCPUUsageNanos = usageNanos
		c.lastCgroupCPUSampleAt = now
		return nil
	}

	elapsed := now.Sub(c.lastCgroupCPUSampleAt)
	if elapsed <= 0 {
		c.lastCgroupCPUUsageNanos = usageNanos
		c.lastCgroupCPUSampleAt = now
		return nil
	}

	prev := c.lastCgroupCPUUsageNanos
	c.lastCgroupCPUUsageNanos = usageNanos
	c.lastCgroupCPUSampleAt = now

	if usageNanos < prev {
		// Counter reset (container restarted).
		return nil
	}

	deltaUsageSec := float64(usageNanos-prev) / 1e9
	elapsedSec := elapsed.Seconds()
	if elapsedSec <= 0 {
		return nil
	}

	cores := readCgroupCPULimitCores()
	if cores <= 0 {
		// Can't reliably normalize; skip and fall back to gopsutil.
		return nil
	}

	pct := (deltaUsageSec / (elapsedSec * cores)) * 100
	if pct < 0 {
		pct = 0
	}
	// Clamp to avoid noise/jitter showing impossible values.
	if pct > 100 {
		pct = 100
	}
	v := ops.CompatRoundTo1DP(pct)
	return &v
}

// resolveMemoryStats 从 cgroup（容器）或宿主机指标中选择一组自洽的
// used、total、percent，绝不混用两种来源。
// cgroup 只有在同时报告当前使用量和明确上限（memory.max 是数字而不是 "max"，即
// cgroupTotal > 0）时才优先；否则三个值全部回退到宿主机，避免把容器 used 除以宿主机
// total 而严重低估内存占用。
func resolveMemoryStats(cgroupUsed, cgroupTotal uint64, cgroupOK bool, host *mem.VirtualMemoryStat) (usedMB *int64, totalMB *int64, usagePercent *float64) {
	if cgroupOK && cgroupTotal > 0 {
		u := int64(cgroupUsed / bytesPerMB)
		t := int64(cgroupTotal / bytesPerMB)
		p := ops.CompatRoundTo1DP(float64(cgroupUsed) / float64(cgroupTotal) * 100)
		return &u, &t, &p
	}

	if host == nil {
		return nil, nil, nil
	}

	u := int64(host.Used / bytesPerMB)
	usedMB = &u
	if host.Total > 0 {
		t := int64(host.Total / bytesPerMB)
		totalMB = &t
		p := ops.CompatRoundTo1DP(float64(host.Used) / float64(host.Total) * 100)
		usagePercent = &p
	} else {
		// 异常情况：宿主机没有报告总量时，保留 gopsutil 自身的百分比。
		p := ops.CompatRoundTo1DP(host.UsedPercent)
		usagePercent = &p
	}
	return usedMB, totalMB, usagePercent
}
func (c *HostObserver) CollectSystemStats(ctx context.Context) (*ops.CollectedSystemStats, error) {
	out := &ops.CollectedSystemStats{}
	if ctx == nil {
		ctx = context.Background()
	}

	sampleAt := time.Now().UTC()

	// CPU：优先使用 cgroup（容器）指标；cgroup CPU 记账不可用时回退到宿主机指标。
	if cpuPct := c.TryCgroupCPUPercent(sampleAt); cpuPct != nil {
		out.CpuUsagePercent = cpuPct
	}
	if out.CpuUsagePercent == nil {
		if cpuPercents, err := cpu.PercentWithContext(ctx, 0, false); err == nil && len(cpuPercents) > 0 {
			v := ops.CompatRoundTo1DP(cpuPercents[0])
			out.CpuUsagePercent = &v
		}
	}

	// 内存：仅当 cgroup 同时提供当前使用量和明确上限（memory.max != "max"）时优先使用；
	// 缺少上限或数据不完整时，used/total/percent 全部回退到宿主机指标。
	// 不能把容器 used 与宿主机 total 混用，否则会得到明显偏低的百分比（例如 60MB / 23GB）。
	cgroupUsed, cgroupTotal, cgroupOK := readCgroupMemoryBytes()
	var host *mem.VirtualMemoryStat
	if !cgroupOK || cgroupTotal == 0 {
		if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
			host = vm
		}
	}
	out.MemoryUsedMB, out.MemoryTotalMB, out.MemoryUsagePercent = resolveMemoryStats(cgroupUsed, cgroupTotal, cgroupOK, host)

	// 采集根分区磁盘使用量；容器部署时这里通常对应业务数据所在文件系统。
	if usage, err := disk.UsageWithContext(ctx, "/"); err == nil && usage != nil {
		usedMB := int64(usage.Used / bytesPerMB)
		totalMB := int64(usage.Total / bytesPerMB)
		out.DiskUsedMB = &usedMB
		out.DiskTotalMB = &totalMB
		if usage.Total > 0 {
			pct := ops.CompatRoundTo1DP(usage.UsedPercent)
			out.DiskUsagePercent = &pct
		}
	}

	return out, nil
}

// HostObserver 是唯一主机/连接池采样状态，技术层不认识业务数据表。
type HostObserver struct {
	db                      *sql.DB
	redisClient             *redis.Client
	lastCgroupCPUUsageNanos uint64
	lastCgroupCPUSampleAt   time.Time
}

func NewHostObserver(db *sql.DB, r *redis.Client) *HostObserver {
	return &HostObserver{db: db, redisClient: r}
}
func (c *HostObserver) GoroutineCount() int { return runtime.NumGoroutine() }

const bytesPerMB = 1024 * 1024
