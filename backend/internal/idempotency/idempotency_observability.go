package idempotency

import (
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// IdempotencyMetricsSnapshot 提供幂等核心指标快照（进程内累计）。
type IdempotencyMetricsSnapshot struct {
	ClaimTotal                uint64  `json:"claim_total"`
	ReplayTotal               uint64  `json:"replay_total"`
	ConflictTotal             uint64  `json:"conflict_total"`
	RetryBackoffTotal         uint64  `json:"retry_backoff_total"`
	ProcessingDurationCount   uint64  `json:"processing_duration_count"`
	ProcessingDurationTotalMs float64 `json:"processing_duration_total_ms"`
	StoreUnavailableTotal     uint64  `json:"store_unavailable_total"`
}

type idempotencyMetrics struct {
	claimTotal               atomic.Uint64
	replayTotal              atomic.Uint64
	conflictTotal            atomic.Uint64
	retryBackoffTotal        atomic.Uint64
	processingDurationCount  atomic.Uint64
	processingDurationMicros atomic.Uint64
	storeUnavailableTotal    atomic.Uint64
}

var defaultIdempotencyMetrics idempotencyMetrics

// GetIdempotencyMetricsSnapshot 返回当前幂等指标快照。
func GetIdempotencyMetricsSnapshot() IdempotencyMetricsSnapshot {
	totalMicros := defaultIdempotencyMetrics.processingDurationMicros.Load()
	return IdempotencyMetricsSnapshot{
		ClaimTotal:                defaultIdempotencyMetrics.claimTotal.Load(),
		ReplayTotal:               defaultIdempotencyMetrics.replayTotal.Load(),
		ConflictTotal:             defaultIdempotencyMetrics.conflictTotal.Load(),
		RetryBackoffTotal:         defaultIdempotencyMetrics.retryBackoffTotal.Load(),
		ProcessingDurationCount:   defaultIdempotencyMetrics.processingDurationCount.Load(),
		ProcessingDurationTotalMs: float64(totalMicros) / 1000.0,
		StoreUnavailableTotal:     defaultIdempotencyMetrics.storeUnavailableTotal.Load(),
	}
}

func (c *IdempotencyCoordinator) recordIdempotencyClaim(endpoint, scope string, attrs map[string]string) {
	defaultIdempotencyMetrics.claimTotal.Add(1)
	c.logIdempotencyMetric("idempotency_claim_total", endpoint, scope, "1", attrs)
}

func (c *IdempotencyCoordinator) recordIdempotencyReplay(endpoint, scope string, attrs map[string]string) {
	defaultIdempotencyMetrics.replayTotal.Add(1)
	c.logIdempotencyMetric("idempotency_replay_total", endpoint, scope, "1", attrs)
}

func (c *IdempotencyCoordinator) recordIdempotencyConflict(endpoint, scope string, attrs map[string]string) {
	defaultIdempotencyMetrics.conflictTotal.Add(1)
	c.logIdempotencyMetric("idempotency_conflict_total", endpoint, scope, "1", attrs)
}

func (c *IdempotencyCoordinator) recordIdempotencyRetryBackoff(endpoint, scope string, attrs map[string]string) {
	defaultIdempotencyMetrics.retryBackoffTotal.Add(1)
	c.logIdempotencyMetric("idempotency_retry_backoff_total", endpoint, scope, "1", attrs)
}

func (c *IdempotencyCoordinator) recordIdempotencyProcessingDuration(endpoint, scope string, duration time.Duration, attrs map[string]string) {
	if duration < 0 {
		duration = 0
	}
	defaultIdempotencyMetrics.processingDurationCount.Add(1)
	defaultIdempotencyMetrics.processingDurationMicros.Add(uint64(duration.Microseconds()))
	c.logIdempotencyMetric("idempotency_processing_duration_ms", endpoint, scope, strconv.FormatFloat(duration.Seconds()*1000, 'f', 3, 64), attrs)
}

// RecordIdempotencyStoreUnavailable 记录幂等存储不可用事件（用于降级路径观测）。
func (c *IdempotencyCoordinator) RecordIdempotencyStoreUnavailable(endpoint, scope, strategy string) {
	defaultIdempotencyMetrics.storeUnavailableTotal.Add(1)
	attrs := map[string]string{}
	if strategy != "" {
		attrs["strategy"] = strategy
	}
	c.logIdempotencyMetric("idempotency_store_unavailable_total", endpoint, scope, "1", attrs)
}

func (c *IdempotencyCoordinator) logIdempotencyAudit(endpoint, scope, keyHash, stateTransition string, replayed bool, attrs map[string]string) {
	var b strings.Builder
	builderWriteString(&b, "[IdempotencyAudit]")
	builderWriteString(&b, " endpoint=")
	builderWriteString(&b, safeAuditField(endpoint))
	builderWriteString(&b, " scope=")
	builderWriteString(&b, safeAuditField(scope))
	builderWriteString(&b, " key_hash=")
	builderWriteString(&b, safeAuditField(keyHash))
	builderWriteString(&b, " state_transition=")
	builderWriteString(&b, safeAuditField(stateTransition))
	builderWriteString(&b, " replayed=")
	builderWriteString(&b, strconv.FormatBool(replayed))
	if len(attrs) > 0 {
		appendSortedAttrs(&b, attrs)
	}
	c.observe("service.idempotency", b.String())
}

func (c *IdempotencyCoordinator) logIdempotencyMetric(name, endpoint, scope, value string, attrs map[string]string) {
	var b strings.Builder
	builderWriteString(&b, "[IdempotencyMetric]")
	builderWriteString(&b, " name=")
	builderWriteString(&b, safeAuditField(name))
	builderWriteString(&b, " endpoint=")
	builderWriteString(&b, safeAuditField(endpoint))
	builderWriteString(&b, " scope=")
	builderWriteString(&b, safeAuditField(scope))
	builderWriteString(&b, " value=")
	builderWriteString(&b, safeAuditField(value))
	if len(attrs) > 0 {
		appendSortedAttrs(&b, attrs)
	}
	c.observe("service.idempotency", b.String())
}

func appendSortedAttrs(builder *strings.Builder, attrs map[string]string) {
	if len(attrs) == 0 {
		return
	}
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		builderWriteByte(builder, ' ')
		builderWriteString(builder, k)
		builderWriteByte(builder, '=')
		builderWriteString(builder, safeAuditField(attrs[k]))
	}
}

func safeAuditField(v string) string {
	value := strings.TrimSpace(v)
	if value == "" {
		return "-"
	}
	// 日志按 key=value 输出，替换空白避免解析歧义。
	value = strings.ReplaceAll(value, "\n", "_")
	value = strings.ReplaceAll(value, "\r", "_")
	value = strings.ReplaceAll(value, "\t", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func resetIdempotencyMetricsForTest() {
	defaultIdempotencyMetrics.claimTotal.Store(0)
	defaultIdempotencyMetrics.replayTotal.Store(0)
	defaultIdempotencyMetrics.conflictTotal.Store(0)
	defaultIdempotencyMetrics.retryBackoffTotal.Store(0)
	defaultIdempotencyMetrics.processingDurationCount.Store(0)
	defaultIdempotencyMetrics.processingDurationMicros.Store(0)
	defaultIdempotencyMetrics.storeUnavailableTotal.Store(0)
}

func builderWriteString(builder *strings.Builder, value string) {
	_, _ = builder.WriteString(value)
}

func builderWriteByte(builder *strings.Builder, value byte) {
	_ = builder.WriteByte(value)
}

// Observer 只负责输出已脱敏的观察消息，业务状态与指标由幂等模块持有。
type Observer interface {
	Observe(component, message string)
}
type ObserverFunc func(component, message string)

func (f ObserverFunc) Observe(component, message string) { f(component, message) }

// notifyObserver 只调用当前拥有者的观察出口，不安装进程全局绑定。
func notifyObserver(observer Observer, component, message string) {
	if observer != nil {
		observer.Observe(component, message)
	}
}
func (c *IdempotencyCoordinator) observe(component, message string) {
	if c != nil {
		notifyObserver(c.observer, component, message)
	}
}
