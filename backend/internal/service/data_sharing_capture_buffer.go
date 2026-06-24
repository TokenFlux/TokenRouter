package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"go.uber.org/zap"
)

// DataSharingCaptureBufferFlush 负责把缓冲中合并后的 session 写入持久化层。
type DataSharingCaptureBufferFlush func(ctx context.Context, session *DataShareSession) error

// DataSharingCaptureBufferHydrate 负责按缓冲 key 读取已落库的完整 session。
type DataSharingCaptureBufferHydrate func(ctx context.Context, key string) (*DataShareSession, error)

// DataSharingCaptureBufferScheduleFlush 负责把一次快照落库任务提交给外部执行器。
type DataSharingCaptureBufferScheduleFlush func(job DataSharingCaptureJob) DataSharingCaptureSubmitMode

// DataSharingCaptureBufferOptions 描述采集缓冲池的依赖与初始配置。
type DataSharingCaptureBufferOptions struct {
	Flush            DataSharingCaptureBufferFlush
	Hydrate          DataSharingCaptureBufferHydrate
	ScheduleFlush    DataSharingCaptureBufferScheduleFlush
	DurationRecorder DataShareCaptureDurationRecorder
}

// DataSharingCaptureBufferStats 是管理端可见的采集缓冲池运行时统计。
type DataSharingCaptureBufferStats struct {
	Enabled                 bool       `json:"enabled"`
	IdleFlushSeconds        int        `json:"idle_flush_seconds"`
	MaxSessions             int        `json:"max_sessions"`
	MaxPendingEvents        int        `json:"max_pending_events"`
	BufferedSessions        int        `json:"buffered_sessions"`
	PendingEvents           int        `json:"pending_events"`
	FlushingSessions        int64      `json:"flushing_sessions"`
	SubmittedTotal          uint64     `json:"submitted_total"`
	FlushSuccessTotal       uint64     `json:"flush_success_total"`
	FlushFailedTotal        uint64     `json:"flush_failed_total"`
	DroppedTotal            uint64     `json:"dropped_total"`
	LastFlushDurationMillis int64      `json:"last_flush_duration_millis"`
	LastError               string     `json:"last_error"`
	LastErrorAt             *time.Time `json:"last_error_at,omitempty"`
	LastSuccessAt           *time.Time `json:"last_success_at,omitempty"`
}

// DataSharingCaptureBuffer 按 trajectory_id 聚合采集增量，降低热点 session 的重复落库成本。
type DataSharingCaptureBuffer struct {
	mu               sync.Mutex
	flush            DataSharingCaptureBufferFlush
	hydrate          DataSharingCaptureBufferHydrate
	scheduleFlush    DataSharingCaptureBufferScheduleFlush
	durationRecorder DataShareCaptureDurationRecorder
	entries          map[string]*dataSharingCaptureBufferEntry
	stopped          bool
	enabled          bool
	idleFlush        time.Duration
	flushTimeout     time.Duration
	maxSessions      int
	maxPending       int
	pendingEvents    int
	flushWG          sync.WaitGroup
	flushing         atomic.Int64
	submittedTotal   atomic.Uint64
	successTotal     atomic.Uint64
	failedTotal      atomic.Uint64
	droppedTotal     atomic.Uint64
	lastDurationMS   atomic.Int64
	lastError        atomic.Value
	lastErrorAt      atomic.Value
	lastSuccessAt    atomic.Value
}

type dataSharingCaptureBufferEntry struct {
	key                   string
	session               *DataShareSession
	eventCount            int
	lastUpdated           time.Time
	timer                 *time.Timer
	hydrating             bool
	hydrateErr            error
	flushing              bool
	lastFlushed           *DataShareSession
	lastFlushedEventCount int
	cond                  *sync.Cond
}

// NewDataSharingCaptureBuffer 创建进程内数据共享采集缓冲池。
func NewDataSharingCaptureBuffer(opts DataSharingCaptureBufferOptions) *DataSharingCaptureBuffer {
	b := &DataSharingCaptureBuffer{
		flush:            opts.Flush,
		hydrate:          opts.Hydrate,
		scheduleFlush:    opts.ScheduleFlush,
		durationRecorder: opts.DurationRecorder,
		entries:          map[string]*dataSharingCaptureBufferEntry{},
		enabled:          defaultDataSharingCaptureBufferEnabled,
		idleFlush:        time.Duration(defaultDataSharingCaptureBufferIdleSeconds) * time.Second,
		flushTimeout:     time.Duration(defaultDataSharingCaptureTaskTimeoutSeconds) * time.Second,
		maxSessions:      defaultDataSharingCaptureBufferMaxSessions,
		maxPending:       defaultDataSharingCaptureBufferMaxEvents,
	}
	return b
}

// UpdateRuntimeSettings 在线更新缓冲池配置，后续提交和 flush 调度立即使用新阈值。
func (b *DataSharingCaptureBuffer) UpdateRuntimeSettings(settings DataShareCaptureRuntimeSettings) {
	if b == nil {
		return
	}
	normalized := normalizeDataShareCaptureRuntimeSettings(settings)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.enabled = true
	b.idleFlush = time.Duration(normalized.BufferIdleFlushSeconds) * time.Second
	b.flushTimeout = time.Duration(normalized.TaskTimeoutSeconds) * time.Second
	b.maxSessions = normalized.BufferMaxSessions
	b.maxPending = normalized.BufferMaxPendingEvents
	for _, entry := range b.entries {
		b.scheduleEntryTimerLocked(entry, b.remainingIdleFlushLocked(entry))
	}
}

// Submit 合并一次采集结果；缓冲池强制开启，落库统一由 idle flush 触发。
func (b *DataSharingCaptureBuffer) Submit(ctx context.Context, session *DataShareSession) error {
	if b == nil || session == nil {
		return nil
	}
	submitStart := time.Now()
	defer func() {
		b.recordDuration(DataShareCaptureDurationPartBufferSubmitTotal, time.Since(submitStart))
	}()
	if b.flush == nil {
		return errors.New("data sharing capture buffer flush is nil")
	}
	key := session.TrajectoryID
	if key == "" {
		key = session.SessionID
	}
	if key == "" {
		key = time.Now().Format(time.RFC3339Nano)
	}

	b.mu.Lock()
	if b.stopped {
		b.mu.Unlock()
		return b.flush(ctx, finalizeBufferedDataShareSession(session))
	}
	if b.pendingEvents >= b.maxPending || (b.entries[key] == nil && len(b.entries) >= b.maxSessions) {
		if !b.flushOldestLocked() {
			b.droppedTotal.Add(1)
			b.mu.Unlock()
			return nil
		}
	}
	entry := b.entries[key]
	shouldHydrate := false
	if entry == nil {
		entry = b.newEntryLocked(key)
		b.entries[key] = entry
		if b.hydrate != nil {
			entry.hydrating = true
			shouldHydrate = true
		}
	}
	if shouldHydrate {
		b.mu.Unlock()
		hydrateStart := time.Now()
		b.hydrateEntry(ctx, entry, key)
		b.recordDuration(DataShareCaptureDurationPartBufferHydrate, time.Since(hydrateStart))
		b.mu.Lock()
	}

	for entry.hydrating && !b.stopped {
		entry.cond.Wait()
	}
	if b.entries[key] != entry {
		b.mu.Unlock()
		return b.Submit(ctx, session)
	}
	if entry.hydrateErr != nil {
		err := entry.hydrateErr
		if b.entries[key] == entry && entry.session == nil && entry.eventCount == 0 && !entry.flushing {
			delete(b.entries, key)
		}
		b.mu.Unlock()
		return err
	}
	if b.stopped {
		b.mu.Unlock()
		return b.flush(ctx, finalizeBufferedDataShareSession(session))
	}
	mergeStart := time.Now()
	session = b.prepareIncomingSessionLocked(entry.session, session)
	entry.session = mergeBufferedDataShareSession(entry.session, session)
	entry.eventCount++
	entry.lastUpdated = time.Now()
	b.pendingEvents++
	b.submittedTotal.Add(1)
	b.scheduleEntryTimerLocked(entry, b.idleFlush)
	b.recordDuration(DataShareCaptureDurationPartBufferMerge, time.Since(mergeStart))
	b.mu.Unlock()
	return nil
}

func (b *DataSharingCaptureBuffer) prepareIncomingSessionLocked(existing *DataShareSession, incoming *DataShareSession) *DataShareSession {
	if incoming == nil {
		return incoming
	}
	switch incoming.captureMode {
	case dataShareCaptureModeOpenAIResponsesRaw:
		return (&DataSharingService{}).buildOpenAIResponsesIncrementalSession(existing, incoming)
	case dataShareCaptureModeMessagesRaw:
		return buildMessagesIncrementalSession(existing, incoming)
	default:
		return incoming
	}
}

// FlushAll 立即落库当前缓冲内容；用于正常停机和测试。
func (b *DataSharingCaptureBuffer) FlushAll(ctx context.Context) {
	if b == nil {
		return
	}
	for {
		b.mu.Lock()
		var selected *dataSharingCaptureBufferEntry
		var hydrating *dataSharingCaptureBufferEntry
		for _, entry := range b.entries {
			if !entry.flushing && !entry.hydrating {
				selected = entry
				break
			}
			if entry.hydrating && hydrating == nil {
				hydrating = entry
			}
		}
		if selected == nil {
			if hydrating != nil {
				b.waitHydrateLocked(ctx, hydrating)
				b.mu.Unlock()
				if ctx != nil && ctx.Err() != nil {
					return
				}
				continue
			}
			b.mu.Unlock()
			b.flushWG.Wait()
			b.mu.Lock()
			empty := len(b.entries) == 0
			b.mu.Unlock()
			if empty {
				return
			}
			continue
		}
		session, key := b.detachFlushLocked(selected)
		b.mu.Unlock()
		err := b.flushEntry(ctx, key, session)
		if ctx != nil && ctx.Err() != nil {
			return
		}
		if err != nil {
			return
		}
	}
}

func (b *DataSharingCaptureBuffer) waitHydrateLocked(ctx context.Context, entry *dataSharingCaptureBufferEntry) {
	if b == nil || entry == nil || entry.cond == nil {
		return
	}
	if ctx == nil {
		for entry.hydrating {
			entry.cond.Wait()
		}
		return
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			b.mu.Lock()
			entry.cond.Broadcast()
			b.mu.Unlock()
		case <-done:
		}
	}()
	defer close(done)
	for entry.hydrating && ctx.Err() == nil {
		entry.cond.Wait()
	}
}

// Stop 停止接收新缓冲并尽量 drain 已缓冲内容。
func (b *DataSharingCaptureBuffer) Stop(ctx context.Context) {
	if b == nil {
		return
	}
	b.mu.Lock()
	if b.stopped {
		b.mu.Unlock()
		b.flushWG.Wait()
		return
	}
	b.stopped = true
	for _, entry := range b.entries {
		if entry.timer != nil {
			entry.timer.Stop()
			entry.timer = nil
		}
		if entry.cond != nil {
			entry.cond.Broadcast()
		}
	}
	b.mu.Unlock()
	b.FlushAll(ctx)
}

// Stats 返回缓冲池当前状态和累计计数。
func (b *DataSharingCaptureBuffer) Stats() DataSharingCaptureBufferStats {
	if b == nil {
		return DataSharingCaptureBufferStats{}
	}
	b.mu.Lock()
	stats := DataSharingCaptureBufferStats{
		Enabled:                 b.enabled,
		IdleFlushSeconds:        durationSecondsCeil(b.idleFlush),
		MaxSessions:             b.maxSessions,
		MaxPendingEvents:        b.maxPending,
		BufferedSessions:        len(b.entries),
		PendingEvents:           b.pendingEvents,
		FlushingSessions:        b.flushing.Load(),
		SubmittedTotal:          b.submittedTotal.Load(),
		FlushSuccessTotal:       b.successTotal.Load(),
		FlushFailedTotal:        b.failedTotal.Load(),
		DroppedTotal:            b.droppedTotal.Load(),
		LastFlushDurationMillis: b.lastDurationMS.Load(),
	}
	b.mu.Unlock()
	lastError, _ := b.lastError.Load().(string)
	stats.LastError = lastError
	stats.LastErrorAt = dataShareAtomicTimeValue(&b.lastErrorAt)
	stats.LastSuccessAt = dataShareAtomicTimeValue(&b.lastSuccessAt)
	return stats
}

func (b *DataSharingCaptureBuffer) newEntryLocked(key string) *dataSharingCaptureBufferEntry {
	entry := &dataSharingCaptureBufferEntry{key: key}
	entry.cond = sync.NewCond(&b.mu)
	return entry
}

func (b *DataSharingCaptureBuffer) hydrateEntry(ctx context.Context, entry *dataSharingCaptureBufferEntry, key string) {
	var hydrated *DataShareSession
	var err error
	if b.hydrate != nil {
		hydrated, err = b.hydrate(ctx, key)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	current := b.entries[key]
	if current != entry {
		return
	}
	if err != nil && !errors.Is(err, ErrDataShareSessionNotFound) {
		entry.hydrateErr = err
		b.recordFailure(err.Error())
		logger.L().With(
			zap.String("component", "service.data_sharing_capture_buffer"),
			zap.String("trajectory_id", key),
			zap.Error(err),
		).Warn("data_sharing.capture_buffer_hydrate_failed")
	}
	if hydrated != nil {
		entry.session = mergeBufferedDataShareSession(hydrated, entry.session)
	}
	if err == nil || errors.Is(err, ErrDataShareSessionNotFound) {
		entry.hydrateErr = nil
	}
	entry.hydrating = false
	entry.cond.Broadcast()
}

func (b *DataSharingCaptureBuffer) scheduleEntryTimerLocked(entry *dataSharingCaptureBufferEntry, delay time.Duration) {
	if b == nil || entry == nil || !b.enabled || b.stopped || entry.flushing {
		return
	}
	if delay <= 0 {
		delay = time.Millisecond
	}
	if entry.timer == nil {
		entry.timer = time.AfterFunc(delay, func() {
			b.flushByKey(entry.key)
		})
		return
	}
	entry.timer.Reset(delay)
}

func (b *DataSharingCaptureBuffer) remainingIdleFlushLocked(entry *dataSharingCaptureBufferEntry) time.Duration {
	if b == nil || entry == nil {
		return 0
	}
	if entry.lastUpdated.IsZero() {
		return b.idleFlush
	}
	return time.Until(entry.lastUpdated.Add(b.idleFlush))
}

func (b *DataSharingCaptureBuffer) flushByKey(key string) {
	b.mu.Lock()
	entry := b.entries[key]
	if entry == nil {
		b.mu.Unlock()
		return
	}
	if b.enabled && !b.stopped {
		if remaining := b.remainingIdleFlushLocked(entry); remaining > 0 {
			b.scheduleEntryTimerLocked(entry, remaining)
			b.mu.Unlock()
			return
		}
	}
	if entry.hydrating {
		b.mu.Unlock()
		return
	}
	b.startFlushLocked(entry, context.Background(), true)
	b.mu.Unlock()
}

func (b *DataSharingCaptureBuffer) flushOldestLocked() bool {
	var selected *dataSharingCaptureBufferEntry
	for _, entry := range b.entries {
		if entry.flushing || entry.hydrating {
			continue
		}
		if selected == nil || entry.eventCount > selected.eventCount {
			selected = entry
		}
	}
	if selected == nil {
		return false
	}
	b.startFlushLocked(selected, context.Background(), true)
	return true
}

func (b *DataSharingCaptureBuffer) startFlushLocked(entry *dataSharingCaptureBufferEntry, ctx context.Context, async bool) {
	if b == nil || entry == nil || entry.flushing || entry.hydrating {
		return
	}
	session, key := b.detachFlushLocked(entry)
	run := func() {
		_ = b.flushEntry(ctx, key, session)
	}
	if !async {
		run()
		return
	}
	if b.scheduleFlush != nil {
		b.flushWG.Add(1)
		mode := b.scheduleFlush(DataSharingCaptureJob{
			Kind: DataSharingCaptureJobKindFlush,
			Flush: func(jobCtx context.Context) error {
				defer b.flushWG.Done()
				return b.flushEntry(jobCtx, key, session)
			},
			Metadata: DataSharingCaptureJobMetadata{RequestID: key},
		})
		if mode == DataSharingCaptureSubmitModeEnqueued {
			return
		}
		b.flushWG.Done()
		err := errors.New("data sharing capture flush queue is full")
		b.recordFailure(err.Error())
		b.finishFlushLocked(key, err)
		return
	}
	b.flushWG.Add(1)
	go func() {
		defer b.flushWG.Done()
		run()
	}()
}

func (b *DataSharingCaptureBuffer) detachFlushLocked(entry *dataSharingCaptureBufferEntry) (*DataShareSession, string) {
	if entry.timer != nil {
		entry.timer.Stop()
		entry.timer = nil
	}
	session := entry.session
	eventCount := entry.eventCount
	entry.session = nil
	entry.eventCount = 0
	entry.lastFlushed = cloneBufferedDataShareSession(session)
	entry.lastFlushedEventCount = eventCount
	entry.flushing = true
	b.pendingEvents -= eventCount
	if b.pendingEvents < 0 {
		b.pendingEvents = 0
	}
	return session, entry.key
}

func (b *DataSharingCaptureBuffer) flushEntry(ctx context.Context, key string, session *DataShareSession) error {
	if session == nil {
		b.finishFlush(key, nil)
		return nil
	}
	start := time.Now()
	finalizeStart := time.Now()
	session = finalizeBufferedDataShareSession(session)
	b.recordDuration(DataShareCaptureDurationPartFlushFinalize, time.Since(finalizeStart))
	b.flushing.Add(1)
	flushCtx, cancel := b.flushContext(ctx)
	err := b.flush(flushCtx, session)
	cancel()
	b.flushing.Add(-1)
	b.lastDurationMS.Store(time.Since(start).Milliseconds())
	b.recordDuration(DataShareCaptureDurationPartFlushTotal, time.Since(start))
	if err != nil {
		b.recordFailure(err.Error())
		logger.L().With(
			zap.String("component", "service.data_sharing_capture_buffer"),
			zap.String("trajectory_id", session.TrajectoryID),
			zap.Error(err),
		).Warn("data_sharing.capture_buffer_flush_failed")
	} else {
		b.successTotal.Add(1)
		b.lastSuccessAt.Store(time.Now())
	}
	b.finishFlush(key, err)
	return err
}

// recordFailure 记录一次缓冲池失败，并同步更新管理端展示的最近失败时间。
func (b *DataSharingCaptureBuffer) recordFailure(msg string) {
	if b == nil {
		return
	}
	b.failedTotal.Add(1)
	b.lastError.Store(truncateDataSharingCaptureError(msg))
	b.lastErrorAt.Store(time.Now())
}

func dataShareAtomicTimeValue(value *atomic.Value) *time.Time {
	if value == nil {
		return nil
	}
	t, ok := value.Load().(time.Time)
	if !ok || t.IsZero() {
		return nil
	}
	// 返回副本，避免调用方拿到可修改的内部状态引用。
	out := t
	return &out
}

func (b *DataSharingCaptureBuffer) recordDuration(part DataShareCaptureDurationPartKey, duration time.Duration) {
	if b == nil || b.durationRecorder == nil {
		return
	}
	b.durationRecorder.Observe(part, duration)
}

func (b *DataSharingCaptureBuffer) flushContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	b.mu.Lock()
	timeout := b.flushTimeout
	b.mu.Unlock()
	if timeout <= 0 {
		timeout = time.Duration(defaultDataSharingCaptureTaskTimeoutSeconds) * time.Second
	}
	return context.WithTimeout(ctx, timeout)
}

func (b *DataSharingCaptureBuffer) finishFlush(key string, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.finishFlushLocked(key, err)
}

func (b *DataSharingCaptureBuffer) finishFlushLocked(key string, err error) {
	entry := b.entries[key]
	if entry == nil {
		return
	}
	entry.flushing = false
	if err != nil && entry.lastFlushed != nil {
		lastFlushedEventCount := entry.lastFlushedEventCount
		if lastFlushedEventCount <= 0 {
			lastFlushedEventCount = 1
		}
		entry.session = mergeBufferedDataShareSession(entry.lastFlushed, entry.session)
		entry.lastFlushed = nil
		entry.lastFlushedEventCount = 0
		entry.eventCount += lastFlushedEventCount
		b.pendingEvents += lastFlushedEventCount
		entry.lastUpdated = time.Now()
		if !b.stopped {
			b.scheduleEntryTimerLocked(entry, b.idleFlush)
		}
		return
	}
	if entry.eventCount > 0 {
		// 成功落库后若同 session 又进了新增量，保留已落库快照作为下一次覆盖保存的基线。
		entry.session = mergeBufferedDataShareSession(entry.lastFlushed, entry.session)
	}
	entry.lastFlushed = nil
	entry.lastFlushedEventCount = 0
	if entry.eventCount == 0 {
		delete(b.entries, key)
		return
	}
	b.scheduleEntryTimerLocked(entry, b.remainingIdleFlushLocked(entry))
}

func mergeBufferedDataShareSession(existing *DataShareSession, incoming *DataShareSession) *DataShareSession {
	if existing == nil {
		return cloneBufferedDataShareSession(incoming)
	}
	if incoming == nil {
		return existing
	}
	now := time.Now()
	existing.Model = firstNonBlank(incoming.Model, existing.Model)
	existing.RequestPath = firstNonBlank(incoming.RequestPath, existing.RequestPath)
	existing.UserAgent = firstNonBlank(incoming.UserAgent, existing.UserAgent)
	incomingCount := incoming.SourceRequestCount
	if incomingCount <= 0 {
		incomingCount = 1
	}
	existing.SourceRequestCount += incomingCount
	existing.InputTokens += incoming.InputTokens
	existing.OutputTokens += incoming.OutputTokens
	existing.TotalTokens += incoming.TotalTokens
	mergeBufferedDataShareActualCost(existing, incoming)
	if incoming.EndedAt != nil {
		existing.EndedAt = incoming.EndedAt
	} else if existing.EndedAt == nil {
		existing.EndedAt = &now
	}
	if existing.SystemPrompt == nil || firstNonBlank(optionalStringValue(existing.SystemPrompt)) == "" {
		existing.SystemPrompt = incoming.SystemPrompt
	}
	existing.Messages = mergeBufferedDataShareMessages(existing.Messages, incoming.Messages)
	existing.Tools = mergeBufferedDataShareTools(existing.Tools, incoming.Tools)
	existing.Usage = mergeBufferedDataShareUsage(existing.Usage, incoming.Usage)
	existing.Meta = mergeBufferedDataShareMeta(existing.Meta, incoming.Meta)
	if incoming.captureState != nil {
		existing.captureState = cloneDataShareResponsesCaptureState(incoming.captureState)
	}
	if len(incoming.captureRequestMessages) > 0 {
		existing.captureRequestMessages = cloneBufferedDataShareMaps(incoming.captureRequestMessages)
	}
	if len(incoming.captureResponseMessages) > 0 {
		existing.captureResponseMessages = cloneBufferedDataShareMaps(incoming.captureResponseMessages)
	}
	existing.UpdatedAt = now
	return existing
}

func buildMessagesIncrementalSession(existing *DataShareSession, raw *DataShareSession) *DataShareSession {
	if raw == nil {
		return nil
	}
	out := cloneBufferedDataShareSession(raw)
	out.captureMode = dataShareCaptureModeIncremental
	requestMessages := cloneBufferedDataShareMaps(raw.captureRequestMessages)
	if len(requestMessages) == 0 && raw.captureInput != nil {
		requestMessages = normalizeCaptureRequestMessages(*raw.captureInput)
	}
	responseMessages := cloneBufferedDataShareMaps(raw.captureResponseMessages)
	if len(responseMessages) == 0 && raw.captureInput != nil {
		responseMessages = normalizeCaptureResponseMessages(*raw.captureInput)
	}
	base := []map[string]any(nil)
	if existing != nil {
		base = existing.Messages
	}
	requestDelta := dataShareMessagesRequestDelta(base, requestMessages)
	messages := cloneBufferedDataShareMaps(requestDelta)
	if len(requestDelta) == 0 && dataShareMessagesAreExistingSuffix(base, responseMessages) {
		responseMessages = nil
	}
	messages = append(messages, cloneBufferedDataShareMaps(responseMessages)...)
	out.Messages = normalizeDataShareMessages(messages)
	out.captureRequestMessages = cloneBufferedDataShareMaps(requestMessages)
	out.captureResponseMessages = cloneBufferedDataShareMaps(responseMessages)
	return out
}

func dataShareMessagesRequestDelta(existing, incoming []map[string]any) []map[string]any {
	if len(incoming) == 0 {
		return nil
	}
	if len(existing) == 0 {
		return cloneBufferedDataShareMaps(incoming)
	}
	existingIdentities := dataShareMessageIdentities(existing)
	incomingIdentities := dataShareMessageIdentities(incoming)
	replayIndex := newDataShareMessagesReplayIndex(existingIdentities)
	// Anthropic Messages 是无状态请求；compaction 后请求可能是“新摘要 + 已保存近期消息 + 新消息”。
	// 因此这里扫描整段请求，保留新摘要等前置内容，只跳过已在历史中出现的连续重放窗口。
	out := make([]map[string]any, 0, len(incoming))
	hasPriorCompaction := false
	for i := 0; i < len(incoming); {
		match := dataShareBestRequestReplayMatchAt(existingIdentities, replayIndex, incomingIdentities, i, hasPriorCompaction)
		if match.length > 0 {
			if !hasPriorCompaction {
				hasPriorCompaction = dataShareMessagesHaveCompaction(incoming[i : i+match.length])
			}
			i += match.length
			continue
		}
		if !hasPriorCompaction && dataShareMessageHasCompaction(incoming[i]) {
			hasPriorCompaction = true
		}
		out = append(out, cloneDataShareMap(incoming[i]))
		i++
	}
	return out
}

type dataShareMessagesReplayIndex struct {
	identityPositions map[string][]int
	windowPositions   map[string][]int
}

func newDataShareMessagesReplayIndex(existingIdentities []string) dataShareMessagesReplayIndex {
	index := dataShareMessagesReplayIndex{
		identityPositions: map[string][]int{},
	}
	for i, identity := range existingIdentities {
		if identity == "" {
			continue
		}
		index.identityPositions[identity] = append(index.identityPositions[identity], i)
	}
	if len(existingIdentities) >= dataShareLongReplayMinMessages {
		index.windowPositions = dataShareReplayWindowIndex(existingIdentities)
	}
	return index
}

func dataShareBestRequestReplayMatchAt(existingIdentities []string, index dataShareMessagesReplayIndex, incomingIdentities []string, incomingStart int, hasPriorCompaction bool) dataShareReplayMatch {
	if len(existingIdentities) == 0 || incomingStart < 0 || incomingStart >= len(incomingIdentities) {
		return dataShareReplayMatch{}
	}
	identity := incomingIdentities[incomingStart]
	if identity == "" {
		return dataShareReplayMatch{}
	}
	best := dataShareReplayMatch{}
	if longMatch := dataShareBestIndexedReplayMatch(existingIdentities, index.windowPositions, incomingIdentities, incomingStart); longMatch.length > 0 {
		best = longMatch
	}
	candidates := index.identityPositions[identity]
	if len(candidates) > dataShareReplayWindowCandidateLimit {
		return best
	}
	for _, existingStart := range candidates {
		length := dataShareContiguousKeyMatchLen(existingIdentities, existingStart, incomingIdentities, incomingStart)
		if !dataShareRequestReplayMatchSafe(len(existingIdentities), incomingStart, existingStart, length, hasPriorCompaction) {
			continue
		}
		if length > best.length || (length == best.length && dataShareRequestReplayMatchPreferred(best, existingStart, length, len(existingIdentities))) {
			best = dataShareReplayMatch{existingStart: existingStart, incomingStart: incomingStart, length: length}
		}
	}
	return best
}

func dataShareRequestReplayMatchSafe(existingLen int, incomingStart int, existingStart int, length int, hasPriorCompaction bool) bool {
	if length < dataShareReplayOverlapMinMessages {
		// compaction 后客户端可能只保留一条旧历史尾部消息；只在明确有 compaction 前缀且贴住旧尾部时跳过。
		return hasPriorCompaction && length == 1 && existingStart+length == existingLen && incomingStart > 0
	}
	// 请求开头重放是无状态 Messages 的常规形态，沿用旧逻辑直接跳过。
	if incomingStart == 0 {
		return true
	}
	// compaction 后客户端通常会在新摘要后保留旧历史尾部；短窗口只信任这种贴住尾部的重放。
	if existingStart+length == existingLen {
		return true
	}
	// 很长的连续窗口碰撞概率极低，可用于覆盖客户端从历史中段截断发送的情况。
	return length >= dataShareLongReplayMinMessages
}

func dataShareMessagesHaveCompaction(messages []map[string]any) bool {
	for _, msg := range messages {
		if dataShareMessageHasCompaction(msg) {
			return true
		}
	}
	return false
}

func dataShareMessageHasCompaction(msg map[string]any) bool {
	content := firstPresentAny(msg["content"], msg["text"])
	for _, block := range anySlice(content) {
		item, ok := mapFromAny(block)
		if !ok {
			continue
		}
		if strings.TrimSpace(stringFromAny(item["type"])) == "compaction" {
			return true
		}
	}
	text := strings.ToLower(strings.TrimSpace(dataShareContentText(content)))
	return strings.HasPrefix(text, "compaction:") || strings.HasPrefix(text, "compaction summary:")
}

func dataShareRequestReplayMatchPreferred(current dataShareReplayMatch, existingStart int, length int, existingLen int) bool {
	if current.length == 0 {
		return true
	}
	currentSuffix := current.existingStart+current.length == existingLen
	nextSuffix := existingStart+length == existingLen
	if nextSuffix != currentSuffix {
		return nextSuffix
	}
	return existingStart > current.existingStart
}

func dataShareMessagesAreExistingSuffix(existing, incoming []map[string]any) bool {
	if len(existing) == 0 || len(incoming) == 0 || len(incoming) > len(existing) {
		return false
	}
	existingIdentities := dataShareMessageIdentities(existing)
	incomingIdentities := dataShareMessageIdentities(incoming)
	start := len(existingIdentities) - len(incomingIdentities)
	for i := range incomingIdentities {
		if incomingIdentities[i] == "" || incomingIdentities[i] != existingIdentities[start+i] {
			return false
		}
	}
	return true
}

func finalizeBufferedDataShareSession(session *DataShareSession) *DataShareSession {
	if session == nil {
		return nil
	}
	session.Messages = CompactDataShareMessages(normalizeDataShareMessages(session.Messages))
	session.Tools = normalizeDataShareTools(session.Tools)
	session.Usage = normalizeDataShareUsage(session.Usage)
	session.Meta = normalizeDataShareMeta(session.Meta)
	qualityReport := evaluateCompactDataShareSessionQuality(session.Model, optionalStringValue(session.SystemPrompt), session.Messages, session.Tools, session.Usage)
	qualityStatus, qualityErrors := qualityReport.Status, qualityReport.Errors
	if dataShareHasReplayDuplicateBlock(session.Messages) {
		qualityStatus = DataShareQualityInvalid
		qualityErrors = appendDataShareQualityError(qualityErrors, dataShareQualityErrorReplayDuplicateBlock)
	}
	status, finalSnapshot := dataShareCompletionState(qualityStatus)
	session.Status = status
	session.IsFinalSnapshot = finalSnapshot
	session.QualityStatus = qualityStatus
	session.QualityErrors = qualityErrors
	session.Exportable = DataShareQualityExportable(qualityStatus)
	session.SessionJSON = BuildFinalizedDataShareSessionPayload(session)
	session.SessionJSONFinalized = true
	return session
}

func cloneBufferedDataShareSession(session *DataShareSession) *DataShareSession {
	if session == nil {
		return nil
	}
	clone := *session
	if session.SystemPrompt != nil {
		prompt := *session.SystemPrompt
		clone.SystemPrompt = &prompt
	}
	if session.EndedAt != nil {
		endedAt := *session.EndedAt
		clone.EndedAt = &endedAt
	}
	if session.ActualCost != nil {
		actualCost := *session.ActualCost
		clone.ActualCost = &actualCost
	}
	clone.Messages = cloneBufferedDataShareMaps(session.Messages)
	clone.Tools = cloneBufferedDataShareMaps(session.Tools)
	clone.Usage = cloneDataShareMap(session.Usage)
	clone.Meta = cloneDataShareMap(session.Meta)
	clone.SessionJSON = cloneDataShareMap(session.SessionJSON)
	clone.SessionJSONFinalized = session.SessionJSONFinalized
	clone.captureMode = session.captureMode
	if session.captureInput != nil {
		input := cloneDataShareCaptureInput(*session.captureInput)
		clone.captureInput = &input
	}
	clone.captureState = cloneDataShareResponsesCaptureState(session.captureState)
	clone.captureRequestItems = cloneDataShareResponsesInputItems(session.captureRequestItems)
	clone.captureResponseItems = cloneBufferedDataShareMaps(session.captureResponseItems)
	clone.captureRequestMessages = cloneBufferedDataShareMaps(session.captureRequestMessages)
	clone.captureResponseMessages = cloneBufferedDataShareMaps(session.captureResponseMessages)
	return &clone
}

func mergeBufferedDataShareActualCost(existing *DataShareSession, incoming *DataShareSession) {
	if existing == nil || incoming == nil || incoming.ActualCost == nil {
		return
	}
	if existing.ActualCost == nil {
		if existing.ID > 0 {
			// 已落库但 actual_cost 为空的历史 session 成本整体未知，后续增量不能把它改成部分已知。
			return
		}
		actualCost := *incoming.ActualCost
		existing.ActualCost = &actualCost
		return
	}
	// 只有已知扣费才进入累加；历史未知的 NULL 不会被当作 0 参与统计。
	*existing.ActualCost += *incoming.ActualCost
}

func cloneBufferedDataShareMaps(items []map[string]any) []map[string]any {
	if len(items) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, cloneDataShareMap(item))
	}
	return out
}

func mergeBufferedDataShareMessages(existing, incoming []map[string]any) []map[string]any {
	if len(existing) == 0 {
		return cloneBufferedDataShareMaps(incoming)
	}
	if len(incoming) == 0 {
		return cloneBufferedDataShareMaps(existing)
	}
	if dataShareMessagesAreExistingPrefix(existing, incoming) {
		return cloneBufferedDataShareMaps(existing)
	}
	// Responses/Agent 客户端常把历史从对话开头重放；只追加已见 replay 后面的新增消息。
	if replay := dataShareReplaySkipLenForMessages(existing, incoming, 0); replay >= dataShareReplayOverlapMinMessages {
		if replay >= len(incoming) {
			return cloneBufferedDataShareMaps(existing)
		}
		return append(cloneBufferedDataShareMaps(existing), cloneBufferedDataShareMaps(incoming[replay:])...)
	}
	return append(cloneBufferedDataShareMaps(existing), cloneBufferedDataShareMaps(incoming)...)
}

func mergeBufferedDataShareTools(existing, incoming []map[string]any) []map[string]any {
	out := cloneBufferedDataShareMaps(existing)
	seen := make(map[string]struct{}, len(out))
	for _, tool := range out {
		seen[string(mustJSON(tool))] = struct{}{}
	}
	for _, tool := range incoming {
		key := string(mustJSON(tool))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, cloneDataShareMap(tool))
	}
	return out
}

func mergeBufferedDataShareUsage(existing, incoming map[string]any) map[string]any {
	out := cloneDataShareMap(existing)
	for k, v := range incoming {
		out[k] = intFromAny(out[k]) + intFromAny(v)
	}
	return normalizeDataShareUsage(out)
}

func mergeBufferedDataShareMeta(existing, incoming map[string]any) map[string]any {
	out := cloneDataShareMap(existing)
	for k, v := range incoming {
		out[k] = v
	}
	sourceIDs := appendStringValues(nil, stringsFromAny(existing["source_request_ids"])...)
	sourceIDs = appendStringValues(sourceIDs, stringsFromAny(incoming["source_request_ids"])...)
	sourceIDs = appendStringValues(sourceIDs, stringFromAny(existing["request_id"]), stringFromAny(incoming["request_id"]))
	out["source_request_ids"] = sourceIDs
	delete(out, "request_ids")
	return normalizeDataShareMeta(out)
}
