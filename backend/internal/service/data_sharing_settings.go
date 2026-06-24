package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// SetDefaultCaptureRuntimeSettings 设置配置文件提供的默认采集运行时参数，数据库配置仍可覆盖。
func (s *DataSharingService) SetDefaultCaptureRuntimeSettings(settings DataShareCaptureRuntimeSettings) {
	if s == nil {
		return
	}
	s.defaultRuntimeSettings = normalizeDataShareCaptureRuntimeSettings(settings)
	s.exportBatchSize.Store(int64(s.defaultRuntimeSettings.ExportBatchSize))
	s.exportWorkerCount.Store(int64(s.defaultRuntimeSettings.ExportWorkerCount))
	if s.captureBuffer != nil {
		s.captureBuffer.UpdateRuntimeSettings(s.defaultRuntimeSettings)
	}
	if s.captureDurations != nil {
		s.captureDurations.SetWindowSize(s.defaultRuntimeSettings.DurationWindowSize)
	}
	if s.exportDurations != nil {
		s.exportDurations.SetWindowSize(s.defaultRuntimeSettings.DurationWindowSize)
	}
}

// LoadRuntimeSettings 从数据库加载运行时配置并同步到当前 worker。
func (s *DataSharingService) LoadRuntimeSettings(ctx context.Context) (*DataShareCaptureRuntimeSettings, error) {
	settings, err := s.GetCaptureRuntimeSettings(ctx)
	if err != nil {
		return nil, err
	}
	s.applyCaptureRuntimeSettings(settings)
	return settings, nil
}

// GetNotice 返回当前数据共享须知；未配置时返回默认模板和版本 1。
func (s *DataSharingService) GetNotice(ctx context.Context) (*DataShareNotice, error) {
	return defaultDataSharingNotice(ctx, s.settingRepo)
}

// UpdateNotice 更新数据共享须知并递增版本号。
func (s *DataSharingService) UpdateNotice(ctx context.Context, content string) (*DataShareNotice, error) {
	if s == nil || s.settingRepo == nil {
		return nil, ErrSettingNotFound
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, ErrDataShareNoticeMissing
	}
	current, err := s.GetNotice(ctx)
	if err != nil {
		return nil, err
	}
	version := current.Version + 1
	if version < 1 {
		version = 1
	}
	updates := map[string]string{
		SettingKeyDataSharingNoticeContent: content,
		SettingKeyDataSharingNoticeVersion: strconv.Itoa(version),
	}
	if err := s.settingRepo.SetMultiple(ctx, updates); err != nil {
		return nil, err
	}
	return &DataShareNotice{Content: content, Version: version, UpdatedAt: time.Now()}, nil
}

// ConfirmNotice 校验用户确认的数据共享须知版本。
func (s *DataSharingService) ConfirmNotice(ctx context.Context, version int) (*DataShareNotice, error) {
	notice, err := s.GetNotice(ctx)
	if err != nil {
		return nil, err
	}
	if version <= 0 || version != notice.Version {
		return nil, ErrDataSharingConsentRequired
	}
	return notice, nil
}

// GetCaptureSkipRules 返回当前生效的数据共享采集跳过规则。
func (s *DataSharingService) GetCaptureSkipRules(ctx context.Context) ([]DataShareCaptureSkipRule, error) {
	rules, err := s.loadCaptureSkipRules(ctx)
	if err != nil {
		return nil, err
	}
	return cloneDataShareCaptureSkipRules(rules), nil
}

// UpdateCaptureSkipRules 保存管理端维护的数据共享采集跳过规则。
func (s *DataSharingService) UpdateCaptureSkipRules(ctx context.Context, rules []DataShareCaptureSkipRule) ([]DataShareCaptureSkipRule, error) {
	if s == nil || s.settingRepo == nil {
		return nil, ErrSettingNotFound
	}
	normalized, err := normalizeDataShareCaptureSkipRules(rules)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return nil, err
	}
	if err := s.settingRepo.Set(ctx, SettingKeyDataSharingCaptureSkipRules, string(data)); err != nil {
		return nil, err
	}
	s.clearCaptureSkipRulesCache()
	return cloneDataShareCaptureSkipRules(normalized), nil
}

// GetStorageLimit 返回数据共享采集空间阈值和当前压缩后占用。
func (s *DataSharingService) GetStorageLimit(ctx context.Context) (*DataShareStorageLimit, error) {
	limitBytes, err := s.loadStorageLimitBytes(ctx)
	if err != nil {
		return nil, err
	}
	currentBytes := int64(0)
	if s != nil && s.repo != nil {
		currentBytes, err = s.repo.TotalStorageBytes(ctx)
		if err != nil {
			return nil, err
		}
	}
	return buildDataShareStorageLimit(limitBytes, currentBytes), nil
}

// UpdateStorageLimit 保存数据共享采集空间阈值；0 表示关闭容量限制。
func (s *DataSharingService) UpdateStorageLimit(ctx context.Context, limitBytes int64) (*DataShareStorageLimit, error) {
	if s == nil || s.settingRepo == nil {
		return nil, ErrSettingNotFound
	}
	if limitBytes < 0 {
		return nil, ErrDataShareStorageLimitInvalid
	}
	if err := s.settingRepo.Set(ctx, SettingKeyDataSharingStorageLimit, strconv.FormatInt(limitBytes, 10)); err != nil {
		return nil, err
	}
	return s.GetStorageLimit(ctx)
}

// GetCaptureRuntimeSettings 返回数据共享采集运行时配置。
func (s *DataSharingService) GetCaptureRuntimeSettings(ctx context.Context) (*DataShareCaptureRuntimeSettings, error) {
	settings, err := s.loadCaptureRuntimeSettings(ctx)
	if err != nil {
		return nil, err
	}
	return settings, nil
}

// UpdateCaptureRuntimeSettings 保存数据共享采集运行时配置，并立即更新当前进程 worker。
func (s *DataSharingService) UpdateCaptureRuntimeSettings(ctx context.Context, settings DataShareCaptureRuntimeSettings) (*DataShareCaptureRuntimeSettings, error) {
	if s == nil || s.settingRepo == nil {
		return nil, ErrSettingNotFound
	}
	if settings.WorkerCount <= 0 || settings.QueueSize <= 0 || settings.TaskTimeoutSeconds <= 0 {
		return nil, ErrDataShareCaptureRuntimeInvalid
	}
	if settings.BufferIdleFlushSeconds <= 0 {
		settings.BufferEnabled = defaultDataSharingCaptureBufferEnabled
		settings.BufferIdleFlushSeconds = defaultDataSharingCaptureBufferIdleSeconds
		settings.BufferMaxSessions = defaultDataSharingCaptureBufferMaxSessions
		settings.BufferMaxPendingEvents = defaultDataSharingCaptureBufferMaxEvents
	}
	settings = normalizeDataShareCaptureRuntimeSettings(settings)
	if err := s.settingRepo.Set(ctx, SettingKeyDataSharingCaptureRuntime, dataShareCaptureRuntimeSettingsJSON(settings)); err != nil {
		return nil, err
	}
	s.applyCaptureRuntimeSettings(&settings)
	return &settings, nil
}

func (s *DataSharingService) loadCaptureRuntimeSettings(ctx context.Context) (*DataShareCaptureRuntimeSettings, error) {
	if s == nil {
		return defaultDataShareCaptureRuntimeSettings(), nil
	}
	defaultSettingsValue := normalizeDataShareCaptureRuntimeSettings(s.defaultRuntimeSettings)
	defaultSettings := &defaultSettingsValue
	if s.captureWorker != nil {
		stats := s.captureWorker.Stats()
		if stats.WorkerCount > 0 {
			defaultSettings.WorkerCount = stats.WorkerCount
		}
		if stats.QueueCapacity > 0 {
			defaultSettings.QueueSize = stats.QueueCapacity
		}
		if stats.FlushQueueCapacity > 0 {
			defaultSettings.FlushQueueSize = stats.FlushQueueCapacity
		}
		if stats.TaskTimeoutSeconds > 0 {
			defaultSettings.TaskTimeoutSeconds = stats.TaskTimeoutSeconds
		}
		if stats.CompressionLevel != "" {
			defaultSettings.CompressionLevel = stats.CompressionLevel
		}
	}
	if s.captureBuffer != nil {
		bufferStats := s.captureBuffer.Stats()
		defaultSettings.BufferEnabled = bufferStats.Enabled
		if bufferStats.IdleFlushSeconds > 0 {
			defaultSettings.BufferIdleFlushSeconds = bufferStats.IdleFlushSeconds
		}
		if bufferStats.MaxSessions > 0 {
			defaultSettings.BufferMaxSessions = bufferStats.MaxSessions
		}
		if bufferStats.MaxPendingEvents > 0 {
			defaultSettings.BufferMaxPendingEvents = bufferStats.MaxPendingEvents
		}
	}
	if s.captureDurations != nil {
		defaultSettings.DurationWindowSize = s.captureDurations.Snapshot().WindowSize
	}
	if exportBatchSize := int(s.exportBatchSize.Load()); exportBatchSize > 0 {
		defaultSettings.ExportBatchSize = exportBatchSize
	}
	if exportWorkerCount := int(s.exportWorkerCount.Load()); exportWorkerCount > 0 {
		defaultSettings.ExportWorkerCount = exportWorkerCount
	}
	if s.settingRepo == nil {
		return defaultSettings, nil
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyDataSharingCaptureRuntime)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return defaultSettings, nil
		}
		return nil, err
	}
	settings := *defaultSettings
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return nil, ErrDataShareCaptureRuntimeInvalid
	}
	if !gjson.Get(raw, "flush_queue_size").Exists() {
		settings.FlushQueueSize = settings.QueueSize
	}
	if !gjson.Get(raw, "duration_window_size").Exists() {
		settings.DurationWindowSize = defaultSettings.DurationWindowSize
	}
	if !gjson.Get(raw, "export_batch_size").Exists() {
		settings.ExportBatchSize = defaultSettings.ExportBatchSize
	}
	if !gjson.Get(raw, "export_worker_count").Exists() {
		settings.ExportWorkerCount = defaultSettings.ExportWorkerCount
	}
	if settings.WorkerCount <= 0 || settings.QueueSize <= 0 || settings.TaskTimeoutSeconds <= 0 ||
		settings.BufferIdleFlushSeconds <= 0 || settings.BufferMaxSessions <= 0 || settings.BufferMaxPendingEvents <= 0 ||
		settings.DurationWindowSize <= 0 || settings.ExportBatchSize <= 0 || settings.ExportWorkerCount <= 0 {
		return nil, ErrDataShareCaptureRuntimeInvalid
	}
	if strings.TrimSpace(settings.CompressionLevel) == "" {
		settings.CompressionLevel = defaultSettings.CompressionLevel
	}
	settings = normalizeDataShareCaptureRuntimeSettings(settings)
	return &settings, nil
}

func (s *DataSharingService) applyCaptureRuntimeSettings(settings *DataShareCaptureRuntimeSettings) {
	if s == nil || settings == nil || settings.WorkerCount <= 0 || settings.QueueSize <= 0 || settings.TaskTimeoutSeconds <= 0 ||
		settings.BufferIdleFlushSeconds <= 0 || settings.BufferMaxSessions <= 0 || settings.BufferMaxPendingEvents <= 0 ||
		settings.DurationWindowSize <= 0 || settings.ExportBatchSize <= 0 || settings.ExportWorkerCount <= 0 {
		return
	}
	SetDataShareCompressionLevel(settings.CompressionLevel)
	if s.captureWorker != nil {
		s.captureWorker.UpdateRuntimeSettings(
			settings.WorkerCount,
			settings.QueueSize,
			settings.FlushQueueSize,
			time.Duration(settings.TaskTimeoutSeconds)*time.Second,
		)
	}
	if s.captureBuffer != nil {
		s.captureBuffer.UpdateRuntimeSettings(*settings)
	}
	if s.captureDurations != nil {
		s.captureDurations.SetWindowSize(settings.DurationWindowSize)
	}
	if s.exportDurations != nil {
		s.exportDurations.SetWindowSize(settings.DurationWindowSize)
	}
	s.exportBatchSize.Store(int64(normalizeDataShareExportBatchSize(settings.ExportBatchSize)))
	s.exportWorkerCount.Store(int64(normalizeDataShareExportWorkerCount(settings.ExportWorkerCount)))
}

func defaultDataShareCaptureRuntimeSettings() *DataShareCaptureRuntimeSettings {
	settings := normalizeDataShareCaptureRuntimeSettings(DataShareCaptureRuntimeSettings{
		WorkerCount:            defaultDataSharingCaptureWorkerCount,
		QueueSize:              defaultDataSharingCaptureQueueSize,
		FlushQueueSize:         defaultDataSharingCaptureQueueSize,
		TaskTimeoutSeconds:     defaultDataSharingCaptureTaskTimeoutSeconds,
		CompressionLevel:       string(defaultDataSharingCaptureCompressionLevel),
		BufferEnabled:          defaultDataSharingCaptureBufferEnabled,
		BufferIdleFlushSeconds: defaultDataSharingCaptureBufferIdleSeconds,
		BufferMaxSessions:      defaultDataSharingCaptureBufferMaxSessions,
		BufferMaxPendingEvents: defaultDataSharingCaptureBufferMaxEvents,
		DurationWindowSize:     defaultDataSharingCaptureDurationWindowSize,
		ExportBatchSize:        defaultDataShareExportBatchSize,
		ExportWorkerCount:      defaultDataShareExportWorkerCount(),
	})
	return &settings
}

func normalizeDataShareCaptureRuntimeSettings(settings DataShareCaptureRuntimeSettings) DataShareCaptureRuntimeSettings {
	opts := normalizeDataSharingCapturePoolOptions(DataSharingCaptureWorkerPoolOptions{
		WorkerCount:    settings.WorkerCount,
		QueueSize:      settings.QueueSize,
		FlushQueueSize: settings.FlushQueueSize,
		TaskTimeout:    time.Duration(settings.TaskTimeoutSeconds) * time.Second,
	})
	bufferIdleSeconds := settings.BufferIdleFlushSeconds
	if bufferIdleSeconds <= 0 {
		bufferIdleSeconds = defaultDataSharingCaptureBufferIdleSeconds
	}
	if bufferIdleSeconds > maxDataSharingCaptureBufferIdleSeconds {
		bufferIdleSeconds = maxDataSharingCaptureBufferIdleSeconds
	}
	bufferMaxSessions := settings.BufferMaxSessions
	if bufferMaxSessions <= 0 {
		bufferMaxSessions = defaultDataSharingCaptureBufferMaxSessions
	}
	if bufferMaxSessions > maxDataSharingCaptureBufferMaxSessions {
		bufferMaxSessions = maxDataSharingCaptureBufferMaxSessions
	}
	bufferMaxPendingEvents := settings.BufferMaxPendingEvents
	if bufferMaxPendingEvents <= 0 {
		bufferMaxPendingEvents = defaultDataSharingCaptureBufferMaxEvents
	}
	if bufferMaxPendingEvents > maxDataSharingCaptureBufferMaxEvents {
		bufferMaxPendingEvents = maxDataSharingCaptureBufferMaxEvents
	}
	durationWindowSize := normalizeDataShareCaptureDurationWindowSize(settings.DurationWindowSize)
	exportBatchSize := normalizeDataShareExportBatchSize(settings.ExportBatchSize)
	exportWorkerCount := normalizeDataShareExportWorkerCount(settings.ExportWorkerCount)
	return DataShareCaptureRuntimeSettings{
		WorkerCount:            opts.WorkerCount,
		QueueSize:              opts.QueueSize,
		FlushQueueSize:         opts.FlushQueueSize,
		TaskTimeoutSeconds:     durationSecondsCeil(opts.TaskTimeout),
		CompressionLevel:       NormalizeDataShareCompressionLevel(settings.CompressionLevel),
		BufferEnabled:          true,
		BufferIdleFlushSeconds: bufferIdleSeconds,
		BufferMaxSessions:      bufferMaxSessions,
		BufferMaxPendingEvents: bufferMaxPendingEvents,
		DurationWindowSize:     durationWindowSize,
		ExportBatchSize:        exportBatchSize,
		ExportWorkerCount:      exportWorkerCount,
	}
}

func (s *DataSharingService) currentExportBatchSize() int {
	if s == nil {
		return defaultDataShareExportBatchSize
	}
	return normalizeDataShareExportBatchSize(int(s.exportBatchSize.Load()))
}

func (s *DataSharingService) currentExportWorkerCount() int {
	if s == nil {
		return defaultDataShareExportWorkerCount()
	}
	return normalizeDataShareExportWorkerCount(int(s.exportWorkerCount.Load()))
}

func defaultDataShareExportWorkerCount() int {
	// 导出 worker 默认按 CPU 给保守并发，避免大导出压满机器影响网关请求。
	cpus := runtime.NumCPU()
	if cpus <= 2 {
		return 1
	}
	if cpus > maxDataShareExportWorkerCount {
		return maxDataShareExportWorkerCount
	}
	return cpus
}

// NormalizeDataShareCompressionLevel 归一化管理端可配置的 zstd 压缩等级。
func NormalizeDataShareCompressionLevel(level string) string {
	switch DataShareCompressionLevel(strings.ToLower(strings.TrimSpace(level))) {
	case DataShareCompressionLevelFastest:
		return string(DataShareCompressionLevelFastest)
	case DataShareCompressionLevelDefault:
		return string(DataShareCompressionLevelDefault)
	case DataShareCompressionLevelBetter:
		return string(DataShareCompressionLevelBetter)
	case DataShareCompressionLevelBest:
		return string(DataShareCompressionLevelBest)
	default:
		return string(defaultDataSharingCaptureCompressionLevel)
	}
}

// SetDataShareCompressionLevel 在线更新后续采集 payload 使用的 zstd 压缩等级。
func SetDataShareCompressionLevel(level string) string {
	normalized := NormalizeDataShareCompressionLevel(level)
	dataShareCompressionLevel.Store(normalized)
	return normalized
}

// CurrentDataShareCompressionLevel 返回当前采集 payload 使用的 zstd 压缩等级。
func CurrentDataShareCompressionLevel() string {
	level, _ := dataShareCompressionLevel.Load().(string)
	if strings.TrimSpace(level) == "" {
		return string(defaultDataSharingCaptureCompressionLevel)
	}
	return NormalizeDataShareCompressionLevel(level)
}

func dataShareCaptureRuntimeSettingsJSON(settings DataShareCaptureRuntimeSettings) string {
	data, err := json.Marshal(settings)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func (s *DataSharingService) loadStorageLimitBytes(ctx context.Context) (int64, error) {
	if s == nil || s.settingRepo == nil {
		return 0, nil
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyDataSharingStorageLimit)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return 0, nil
		}
		return 0, err
	}
	limitBytes, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || limitBytes < 0 {
		return 0, ErrDataShareStorageLimitInvalid
	}
	return limitBytes, nil
}

func buildDataShareStorageLimit(limitBytes, currentBytes int64) *DataShareStorageLimit {
	if limitBytes < 0 {
		limitBytes = 0
	}
	if currentBytes < 0 {
		currentBytes = 0
	}
	out := &DataShareStorageLimit{
		LimitBytes:          limitBytes,
		CurrentStorageBytes: currentBytes,
		Enabled:             limitBytes > 0,
		Exceeded:            limitBytes > 0 && currentBytes >= limitBytes,
	}
	if limitBytes > 0 {
		out.UsageRatio = float64(currentBytes) / float64(limitBytes)
	}
	return out
}

func (s *DataSharingService) captureStorageLimitOption(ctx context.Context) DataShareUpsertOptions {
	limitBytes, err := s.loadStorageLimitBytes(ctx)
	if err != nil {
		slog.Warn("data sharing: failed to load storage limit, capture continues without limit", "error", err)
		return DataShareUpsertOptions{DurationRecorder: s.captureDurations}
	}
	return DataShareUpsertOptions{StorageLimitBytes: limitBytes, DurationRecorder: s.captureDurations}
}

func (s *DataSharingService) shouldSkipDataShareCapture(ctx context.Context, input DataShareCaptureInput) bool {
	rules, err := s.loadCaptureSkipRules(ctx)
	if err != nil {
		slog.Warn("data sharing: failed to load capture skip rules", "error", err)
		return false
	}
	return dataShareCaptureSkipRulesMatch(input, rules)
}

func (s *DataSharingService) loadCaptureSkipRules(ctx context.Context) ([]DataShareCaptureSkipRule, error) {
	if s == nil || s.settingRepo == nil {
		return defaultDataShareCaptureSkipRules(), nil
	}
	now := time.Now()
	s.skipRulesMu.RLock()
	if now.Before(s.skipRulesCacheExpiresAt) && s.skipRulesCache != nil {
		cached := cloneDataShareCaptureSkipRules(s.skipRulesCache)
		s.skipRulesMu.RUnlock()
		return cached, nil
	}
	s.skipRulesMu.RUnlock()

	raw, err := s.settingRepo.GetValue(ctx, SettingKeyDataSharingCaptureSkipRules)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			rules := defaultDataShareCaptureSkipRules()
			s.storeCaptureSkipRulesCache(rules)
			return rules, nil
		}
		return nil, err
	}
	var rules []DataShareCaptureSkipRule
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		slog.Warn("data sharing: invalid capture skip rules json, fallback to defaults", "error", err)
		rules = defaultDataShareCaptureSkipRules()
		s.storeCaptureSkipRulesCache(rules)
		return rules, nil
	}
	normalized, err := normalizeDataShareCaptureSkipRules(rules)
	if err != nil {
		slog.Warn("data sharing: invalid capture skip rules config, fallback to defaults", "error", err)
		normalized = defaultDataShareCaptureSkipRules()
	}
	s.storeCaptureSkipRulesCache(normalized)
	return cloneDataShareCaptureSkipRules(normalized), nil
}

func (s *DataSharingService) storeCaptureSkipRulesCache(rules []DataShareCaptureSkipRule) {
	if s == nil {
		return
	}
	s.skipRulesMu.Lock()
	defer s.skipRulesMu.Unlock()
	s.skipRulesCache = cloneDataShareCaptureSkipRules(rules)
	s.skipRulesCacheExpiresAt = time.Now().Add(dataShareSkipRulesCacheTTL)
}

func (s *DataSharingService) clearCaptureSkipRulesCache() {
	if s == nil {
		return
	}
	s.skipRulesMu.Lock()
	defer s.skipRulesMu.Unlock()
	s.skipRulesCache = nil
	s.skipRulesCacheExpiresAt = time.Time{}
}

func defaultDataShareCaptureSkipRules() []DataShareCaptureSkipRule {
	return []DataShareCaptureSkipRule{
		{
			ID:             "claude_code_title",
			Name:           "Claude Code 标题生成",
			Enabled:        true,
			ClientFamilies: []string{"claude-cli"},
			RequestPaths:   []string{"/v1/messages"},
			FieldScopes:    []string{"system"},
			Patterns:       []string{"Generate a concise, sentence-case title"},
			MatchMode:      dataShareSkipRuleMatchContains,
		},
		{
			ID:             "opencode_title_system",
			Name:           "opencode 标题生成系统提示",
			Enabled:        true,
			ClientFamilies: []string{"opencode"},
			RequestPaths:   []string{"/v1/messages", "/v1/chat/completions", "/v1/responses"},
			FieldScopes:    []string{"system"},
			Patterns: []string{
				"You are a title generator. You output ONLY a thread title. Nothing else.",
				"Generate a brief title that would help the user find this conversation later.",
				"NEVER respond to questions, just generate a title for the conversation",
			},
			MatchMode: dataShareSkipRuleMatchContains,
		},
		{
			ID:             "opencode_title_user_prompt",
			Name:           "opencode 标题生成用户提示",
			Enabled:        true,
			ClientFamilies: []string{"opencode"},
			RequestPaths:   []string{"/v1/messages", "/v1/chat/completions", "/v1/responses"},
			FieldScopes:    []string{"messages", "input"},
			Patterns:       []string{"Generate a title for this conversation:"},
			MatchMode:      dataShareSkipRuleMatchContains,
		},
		{
			ID:           "agent_title_from_messages",
			Name:         "Agent 会话标题生成",
			Enabled:      true,
			RequestPaths: []string{"/v1/messages", "/v1/chat/completions", "/v1/responses"},
			FieldScopes:  []string{"messages", "input"},
			Patterns:     []string{"Please write a 5-10 word title for the following conversation:"},
			MatchMode:    dataShareSkipRuleMatchContains,
		},
		{
			ID:           "agent_topic_title",
			Name:         "Agent 主题标题提取",
			Enabled:      true,
			RequestPaths: []string{"/v1/messages", "/v1/chat/completions", "/v1/responses"},
			FieldScopes:  []string{"system", "instructions"},
			Patterns:     []string{"extract a 2-3 word title"},
			MatchMode:    dataShareSkipRuleMatchContains,
		},
		{
			ID:           "agent_warmup",
			Name:         "Agent 预热请求",
			Enabled:      true,
			RequestPaths: []string{"/v1/messages", "/v1/chat/completions", "/v1/responses"},
			FieldScopes:  []string{"messages", "input"},
			Patterns:     []string{"Warmup"},
			MatchMode:    dataShareSkipRuleMatchEquals,
		},
		{
			ID:        "excluded_models",
			Name:      "默认排除模型",
			Enabled:   true,
			Models:    []string{"gpt-5.4-mini", "codex-auto-review"},
			MatchMode: dataShareSkipRuleMatchEquals,
		},
	}
}

func normalizeDataShareCaptureSkipRules(rules []DataShareCaptureSkipRule) ([]DataShareCaptureSkipRule, error) {
	out := make([]DataShareCaptureSkipRule, 0, len(rules))
	seenIDs := map[string]struct{}{}
	for _, rule := range rules {
		normalized, err := normalizeDataShareCaptureSkipRule(rule)
		if err != nil {
			return nil, err
		}
		if _, ok := seenIDs[normalized.ID]; ok {
			return nil, ErrDataShareSkipRulesInvalid
		}
		seenIDs[normalized.ID] = struct{}{}
		out = append(out, normalized)
	}
	return out, nil
}

func normalizeDataShareCaptureSkipRule(rule DataShareCaptureSkipRule) (DataShareCaptureSkipRule, error) {
	rule.ID = strings.TrimSpace(rule.ID)
	rule.Name = strings.TrimSpace(rule.Name)
	rule.MatchMode = strings.ToLower(strings.TrimSpace(rule.MatchMode))
	if rule.MatchMode == "" {
		rule.MatchMode = dataShareSkipRuleMatchContains
	}
	if rule.ID == "" || rule.Name == "" {
		return DataShareCaptureSkipRule{}, ErrDataShareSkipRulesInvalid
	}
	if rule.MatchMode != dataShareSkipRuleMatchContains && rule.MatchMode != dataShareSkipRuleMatchEquals {
		return DataShareCaptureSkipRule{}, ErrDataShareSkipRulesInvalid
	}
	rule.ClientFamilies = uniqueTrimmedStrings(rule.ClientFamilies, func(v string) string {
		return strings.ToLower(normalizeDataShareUserAgent(v))
	})
	rule.RequestPaths = uniqueTrimmedStrings(rule.RequestPaths, func(v string) string {
		return strings.ToLower(normalizeDataShareRequestPath(v))
	})
	rule.Models = uniqueTrimmedStrings(rule.Models, func(v string) string {
		return strings.ToLower(strings.TrimSpace(v))
	})
	rule.FieldScopes = uniqueTrimmedStrings(rule.FieldScopes, func(v string) string {
		return strings.ToLower(strings.TrimSpace(v))
	})
	rule.Patterns = uniqueTrimmedStrings(rule.Patterns, strings.TrimSpace)
	if len(rule.Models) == 0 && len(rule.Patterns) == 0 {
		return DataShareCaptureSkipRule{}, ErrDataShareSkipRulesInvalid
	}
	if len(rule.Patterns) > 0 && len(rule.FieldScopes) == 0 {
		return DataShareCaptureSkipRule{}, ErrDataShareSkipRulesInvalid
	}
	for _, scope := range rule.FieldScopes {
		if !isDataShareSkipScope(scope) {
			return DataShareCaptureSkipRule{}, ErrDataShareSkipRulesInvalid
		}
	}
	return rule, nil
}

func uniqueTrimmedStrings(values []string, normalize func(string) string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = normalize(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func isDataShareSkipScope(scope string) bool {
	switch scope {
	case "system", "messages", "input", "instructions":
		return true
	default:
		return false
	}
}

func cloneDataShareCaptureSkipRules(rules []DataShareCaptureSkipRule) []DataShareCaptureSkipRule {
	out := make([]DataShareCaptureSkipRule, 0, len(rules))
	for _, rule := range rules {
		cloned := rule
		cloned.ClientFamilies = append([]string(nil), rule.ClientFamilies...)
		cloned.RequestPaths = append([]string(nil), rule.RequestPaths...)
		cloned.Models = append([]string(nil), rule.Models...)
		cloned.FieldScopes = append([]string(nil), rule.FieldScopes...)
		cloned.Patterns = append([]string(nil), rule.Patterns...)
		out = append(out, cloned)
	}
	return out
}

func dataShareCaptureSkipRulesMatch(input DataShareCaptureInput, rules []DataShareCaptureSkipRule) bool {
	texts := dataShareSkipCandidateTexts(input.RequestBody)
	clientFamily := strings.ToLower(normalizeDataShareUserAgent(input.UserAgent))
	requestPath := strings.ToLower(normalizeDataShareRequestPath(input.InboundEndpoint))
	models := dataShareSkipCandidateModels(input)
	for _, rule := range rules {
		if !rule.Enabled ||
			!dataShareSkipRuleApplies(rule.ClientFamilies, clientFamily) ||
			!dataShareSkipRuleApplies(rule.RequestPaths, requestPath) ||
			!dataShareSkipRuleModelsApply(rule.Models, models) {
			continue
		}
		if len(rule.Patterns) == 0 && len(rule.Models) > 0 {
			return true
		}
		for _, scope := range rule.FieldScopes {
			for _, text := range texts[scope] {
				if dataShareSkipRuleTextMatches(rule, text) {
					return true
				}
			}
		}
	}
	return false
}

func dataShareSkipCandidateModels(input DataShareCaptureInput) []string {
	candidates := []string{
		input.UpstreamModel,
		input.Model,
		gjson.GetBytes(input.RequestBody, "model").String(),
	}
	return uniqueTrimmedStrings(candidates, func(v string) string {
		return strings.ToLower(strings.TrimSpace(v))
	})
}

func dataShareSkipRuleApplies(allowed []string, value string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, item := range allowed {
		if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(value)) {
			return true
		}
	}
	return false
}

func dataShareSkipRuleModelsApply(allowed []string, models []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, allowedModel := range allowed {
		for _, model := range models {
			if strings.EqualFold(strings.TrimSpace(allowedModel), strings.TrimSpace(model)) {
				return true
			}
		}
	}
	return false
}

func dataShareSkipRuleTextMatches(rule DataShareCaptureSkipRule, text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	for _, pattern := range rule.Patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		left, right := text, pattern
		if !rule.CaseSensitive {
			left = strings.ToLower(left)
			right = strings.ToLower(right)
		}
		switch rule.MatchMode {
		case dataShareSkipRuleMatchEquals:
			if left == right {
				return true
			}
		default:
			if strings.Contains(left, right) {
				return true
			}
		}
	}
	return false
}

func dataShareSkipCandidateTexts(body []byte) map[string][]string {
	out := map[string][]string{
		"system":       {},
		"messages":     {},
		"input":        {},
		"instructions": {},
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return out
	}
	add := func(scope string, value any) {
		if text := strings.TrimSpace(dataShareContentText(value)); text != "" {
			out[scope] = append(out[scope], text)
		}
	}
	add("system", payload["system"])
	add("system", payload["system_instruction"])
	add("instructions", payload["instructions"])
	add("instructions", payload["system_instruction"])
	add("input", payload["input"])
	appendDataShareSkipResponsesInput(out, payload["input"])
	appendDataShareSkipMessages(out, payload["messages"])
	appendDataShareSkipContents(out, payload["contents"])
	return out
}

func appendDataShareSkipResponsesInput(out map[string][]string, raw any) {
	for _, item := range anySlice(raw) {
		msg, ok := mapFromAny(item)
		if !ok {
			continue
		}
		text := strings.TrimSpace(dataShareContentText(firstPresentAny(msg["content"], msg["text"])))
		if text == "" {
			continue
		}
		role := strings.TrimSpace(strings.ToLower(stringFromAny(msg["role"])))
		if role == "system" || role == "developer" {
			out["system"] = append(out["system"], text)
		}
	}
}

func appendDataShareSkipMessages(out map[string][]string, raw any) {
	for _, item := range anySlice(raw) {
		msg, ok := mapFromAny(item)
		if !ok {
			if text := strings.TrimSpace(dataShareContentText(item)); text != "" {
				out["messages"] = append(out["messages"], text)
			}
			continue
		}
		text := strings.TrimSpace(dataShareContentText(firstPresentAny(msg["content"], msg["text"])))
		if text == "" {
			continue
		}
		role := strings.TrimSpace(strings.ToLower(stringFromAny(msg["role"])))
		if role == "system" || role == "developer" {
			out["system"] = append(out["system"], text)
			continue
		}
		out["messages"] = append(out["messages"], text)
	}
}

func appendDataShareSkipContents(out map[string][]string, raw any) {
	for _, item := range anySlice(raw) {
		msg, ok := mapFromAny(item)
		if !ok {
			continue
		}
		text := strings.TrimSpace(dataShareContentText(firstPresentAny(msg["parts"], msg["content"], msg["text"])))
		if text == "" {
			continue
		}
		role := strings.TrimSpace(strings.ToLower(stringFromAny(msg["role"])))
		if role == "system" || role == "developer" {
			out["system"] = append(out["system"], text)
			continue
		}
		out["messages"] = append(out["messages"], text)
	}
}

func defaultDataSharingNotice(ctx context.Context, repo SettingRepository) (*DataShareNotice, error) {
	if repo == nil {
		return &DataShareNotice{Content: defaultDataSharingNoticeContent, Version: 1, UpdatedAt: time.Now()}, nil
	}
	settings, err := repo.GetMultiple(ctx, []string{SettingKeyDataSharingNoticeContent, SettingKeyDataSharingNoticeVersion})
	if err != nil {
		return nil, err
	}
	content := strings.TrimSpace(settings[SettingKeyDataSharingNoticeContent])
	if content == "" {
		content = defaultDataSharingNoticeContent
	}
	version, _ := strconv.Atoi(strings.TrimSpace(settings[SettingKeyDataSharingNoticeVersion]))
	if version <= 0 {
		version = 1
	}
	return &DataShareNotice{Content: content, Version: version, UpdatedAt: time.Now()}, nil
}
