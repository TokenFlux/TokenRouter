package ops

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// settings.go reads, validates, and saves Ops notification, alert, capture,
// and cleanup preferences.
// Ops runtime preferences use separate stored keys for mail, evaluator
// behavior, display cutoffs, and advanced capture/cleanup settings. Missing
// keys can receive best-effort defaults during a read, while malformed JSON
// returns defaults without overwriting the saved value. Valid JSON is decoded
// using each reader's own baseline, so omitted mail flags, evaluator flags, and
// display pointers do not all inherit the same defaults.
//
// Advanced reads use the published in-memory snapshot and leave its
// ignored-status slice shared. Updates validate before normalization, persist
// the resulting JSON, publish the advanced snapshot, and then call quota and
// cleanup consumers. A failed cleanup reload is diagnostic after persistence
// rather than a rejected settings update. Mail updates merge selected branches
// and can retain caller recipient slices, while full evaluator/cutoff updates
// return JSON-decoded copies. Storage and callback phases do not share a
// transaction.

// opsAlertEvaluatorLeaderLockKeyDefault and
// opsAlertEvaluatorLeaderLockTTLDefault supply normalization values for saved
// evaluator leases, distinct from the evaluator helper's own fallback
// constants.
const (
	opsAlertEvaluatorLeaderLockKeyDefault = "ops:alert:evaluator:leader" // stored evaluator lease identity
	opsAlertEvaluatorLeaderLockTTLDefault = 30 * time.Second             // stored evaluator default lifetime

	// These SettingKeyOps values identify independent settings records. Their
	// named Ops readers own missing-data behavior and normalization; writing one
	// key does not replace another preference family.
	SettingKeyOpsMetricThresholds          = "ops_metric_thresholds"           // dashboard display cutoff storage
	SettingKeyOpsRealtimeMonitoringEnabled = "ops_realtime_monitoring_enabled" // live query soft-switch storage
	SettingKeyOpsMonitoringEnabled         = "ops_monitoring_enabled"          // overall monitoring switch storage
	SettingKeyOpsRuntimeLogConfig          = "ops_runtime_log_config"          // runtime logger override storage
	SettingKeyOpsAlertRuntimeSettings      = "ops_alert_runtime_settings"      // evaluator cadence/silence/lease storage
	SettingKeyOpsEmailNotificationConfig   = "ops_email_notification_config"   // alert and report preference storage
	SettingKeyOpsAdvancedSettings          = "ops_advanced_settings"           // capture/quota/cleanup/display preference storage
	SettingKeyOpsMetricsIntervalSeconds    = "ops_metrics_interval_seconds"    // system collector cadence storage
)

// IsRealtimeMonitoringEnabled checks overall monitoring before reading the
// realtime soft switch. Missing storage, absent key, and any read failure
// permit realtime features; only trimmed case-insensitive false, zero, off, or
// disabled deny them. The receiver is required, and ctx reaches the settings
// reader as supplied.
//
// Overall monitoring uses the service's cached snapshot plus deployment
// options, while realtime settings are read on each permitted call. Failed
// realtime reads enable this second gate only after the first passes, so they
// cannot override disabled overall monitoring. The two flags therefore have
// different read timing when settings change.
func (s *OpsService) IsRealtimeMonitoringEnabled(ctx context.Context) bool {
	if !s.IsMonitoringEnabled(ctx) {
		return false
	}
	if s.settingRepo == nil {
		return true
	}

	value, err := s.settingRepo.GetValue(ctx, SettingKeyOpsRealtimeMonitoringEnabled)
	if err != nil {
		// Treat missing or failed realtime reads as enabled after overall monitoring
		// passes, so transient storage failure does not disable the live query
		// surface.
		if errors.Is(err, ErrSettingNotFound) {
			return true
		}
		return true
	}

	switch strings.ToLower(strings.TrimSpace(value)) {
	case "false", "0", "off", "disabled":
		return false
	default:
		return true
	}
}

// GetEmailNotificationConfig returns mail preferences using family defaults
// when service/storage is absent or saved JSON is malformed. A missing key also
// attempts to save defaults, ignoring encoding/write failure. Other read errors
// propagate, and nil context selects background.
//
// Valid JSON decodes into an initially zero-valued config, then
// recipient/schedule normalization runs. Consequently valid null or omitted
// boolean preferences stay false rather than inheriting the missing-key alert
// enablement default. Recipients are not validated as email addresses here, and
// malformed cron text remains available for the report scheduler to reject at
// registration.
func (s *OpsService) GetEmailNotificationConfig(ctx context.Context) (*OpsEmailNotificationConfig, error) {
	defaultCfg := defaultOpsEmailNotificationConfig()
	if s == nil || s.settingRepo == nil {
		return defaultCfg, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	raw, err := s.settingRepo.GetValue(ctx, SettingKeyOpsEmailNotificationConfig)
	if err != nil {
		// A missing setting attempts one default write with the read context.
		// Encoding or persistence failure is ignored, and the caller still receives
		// defaults.
		if errors.Is(err, ErrSettingNotFound) {
			if b, mErr := json.Marshal(defaultCfg); mErr == nil {
				_ = s.settingRepo.Set(ctx, SettingKeyOpsEmailNotificationConfig, string(b))
			}
			return defaultCfg, nil
		}
		return nil, err
	}

	cfg := &OpsEmailNotificationConfig{}
	// Malformed JSON yields family defaults without rewriting the saved value.
	// Valid JSON, including null, retains each destination's own baseline values.
	if err := json.Unmarshal([]byte(raw), cfg); err != nil {
		return defaultCfg, nil
	}
	normalizeOpsEmailNotificationConfig(cfg)
	return cfg, nil
}

// UpdateEmailNotificationConfig reads existing mail preferences and merges
// supplied Alert or Report branches before saving. Nil request is rejected,
// omitted branches remain saved, and a nil recipient slice in a supplied branch
// retains its prior list. Other supplied branch scalars replace existing
// values, including zero and false. Nil context selects background for storage
// work.
//
// Validation checks selected numeric/severity values before schedule/recipient
// normalization and persistence. Supplied recipient slices remain shared with
// the returned config; callers keep them stable through encoding and copy
// before mutation while shared. The read and write are independent, so
// concurrent updates can overwrite one another. A missing-key read can already
// persist defaults even if later update validation fails.
func (s *OpsService) UpdateEmailNotificationConfig(ctx context.Context, req *OpsEmailNotificationConfigUpdateRequest) (*OpsEmailNotificationConfig, error) {
	if s == nil || s.settingRepo == nil {
		return nil, errors.New("setting repository not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if req == nil {
		return nil, errors.New("invalid request")
	}

	cfg, err := s.GetEmailNotificationConfig(ctx)
	if err != nil {
		return nil, err
	}

	// Replace scalars only in supplied branches. Recipient presence selects
	// retention or replacement, and assigning the supplied slice leaves its
	// storage shared through encoding and return.
	if req.Alert != nil {
		cfg.Alert.Enabled = req.Alert.Enabled
		if req.Alert.Recipients != nil {
			cfg.Alert.Recipients = req.Alert.Recipients
		}
		cfg.Alert.MinSeverity = strings.TrimSpace(req.Alert.MinSeverity)
		cfg.Alert.RateLimitPerHour = req.Alert.RateLimitPerHour
		cfg.Alert.BatchingWindowSeconds = req.Alert.BatchingWindowSeconds
		cfg.Alert.IncludeResolvedAlerts = req.Alert.IncludeResolvedAlerts
	}

	if req.Report != nil {
		cfg.Report.Enabled = req.Report.Enabled
		if req.Report.Recipients != nil {
			cfg.Report.Recipients = req.Report.Recipients
		}
		cfg.Report.DailySummaryEnabled = req.Report.DailySummaryEnabled
		cfg.Report.DailySummarySchedule = strings.TrimSpace(req.Report.DailySummarySchedule)
		cfg.Report.WeeklySummaryEnabled = req.Report.WeeklySummaryEnabled
		cfg.Report.WeeklySummarySchedule = strings.TrimSpace(req.Report.WeeklySummarySchedule)
		cfg.Report.ErrorDigestEnabled = req.Report.ErrorDigestEnabled
		cfg.Report.ErrorDigestSchedule = strings.TrimSpace(req.Report.ErrorDigestSchedule)
		cfg.Report.ErrorDigestMinCount = req.Report.ErrorDigestMinCount
		cfg.Report.ProviderHealthEnabled = req.Report.ProviderHealthEnabled
		cfg.Report.ProviderHealthSchedule = strings.TrimSpace(req.Report.ProviderHealthSchedule)
		cfg.Report.ProviderHealthErrorRateThreshold = req.Report.ProviderHealthErrorRateThreshold
	}

	if err := validateOpsEmailNotificationConfig(cfg); err != nil {
		return nil, err
	}

	normalizeOpsEmailNotificationConfig(cfg)
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	if err := s.settingRepo.Set(ctx, SettingKeyOpsEmailNotificationConfig, string(raw)); err != nil {
		return nil, err
	}
	return cfg, nil
}

// defaultOpsEmailNotificationConfig allocates default alert-enabled and
// report-disabled preferences with empty recipient lists. Report cron text
// selects nine o'clock daily, Monday weekly, and daily digest/health schedules
// for the scheduler's configured timezone.
func defaultOpsEmailNotificationConfig() *OpsEmailNotificationConfig {
	return &OpsEmailNotificationConfig{
		Alert: OpsEmailAlertConfig{
			Enabled:               true,
			Recipients:            []string{},
			MinSeverity:           "",
			RateLimitPerHour:      0,
			BatchingWindowSeconds: 0,
			IncludeResolvedAlerts: false,
		},
		Report: OpsEmailReportConfig{
			Enabled:                          false,
			Recipients:                       []string{},
			DailySummaryEnabled:              false,
			DailySummarySchedule:             "0 9 * * *",
			WeeklySummaryEnabled:             false,
			WeeklySummarySchedule:            "0 9 * * 1",
			ErrorDigestEnabled:               false,
			ErrorDigestSchedule:              "0 9 * * *",
			ErrorDigestMinCount:              10,
			ProviderHealthEnabled:            false,
			ProviderHealthSchedule:           "0 9 * * *",
			ProviderHealthErrorRateThreshold: 10.0,
		},
	}
}

// normalizeOpsEmailNotificationConfig mutates a nonnil config to supply empty
// recipient lists and nonblank default cron text. It trims severity and
// schedule strings but leaves enablement, addresses, numeric policy, and cron
// validity as supplied.
func normalizeOpsEmailNotificationConfig(cfg *OpsEmailNotificationConfig) {
	if cfg == nil {
		return
	}
	if cfg.Alert.Recipients == nil {
		cfg.Alert.Recipients = []string{}
	}
	if cfg.Report.Recipients == nil {
		cfg.Report.Recipients = []string{}
	}

	cfg.Alert.MinSeverity = strings.TrimSpace(cfg.Alert.MinSeverity)
	cfg.Report.DailySummarySchedule = strings.TrimSpace(cfg.Report.DailySummarySchedule)
	cfg.Report.WeeklySummarySchedule = strings.TrimSpace(cfg.Report.WeeklySummarySchedule)
	cfg.Report.ErrorDigestSchedule = strings.TrimSpace(cfg.Report.ErrorDigestSchedule)
	cfg.Report.ProviderHealthSchedule = strings.TrimSpace(cfg.Report.ProviderHealthSchedule)

	// Fill empty schedule text so enabled reports still have a parseable default.
	// Nonempty malformed schedules remain supplied and are rejected by scheduler
	// registration.
	if cfg.Report.DailySummarySchedule == "" {
		cfg.Report.DailySummarySchedule = "0 9 * * *"
	}
	if cfg.Report.WeeklySummarySchedule == "" {
		cfg.Report.WeeklySummarySchedule = "0 9 * * 1"
	}
	if cfg.Report.ErrorDigestSchedule == "" {
		cfg.Report.ErrorDigestSchedule = "0 9 * * *"
	}
	if cfg.Report.ProviderHealthSchedule == "" {
		cfg.Report.ProviderHealthSchedule = "0 9 * * *"
	}
}

// validateOpsEmailNotificationConfig accepts supported case-sensitive mail
// severity and nonnegative limits plus a zero-to-one-hundred provider
// threshold. It validates even disabled branches, but does not inspect
// recipient addresses, cron syntax, or finite floating-point values.
func validateOpsEmailNotificationConfig(cfg *OpsEmailNotificationConfig) error {
	if cfg == nil {
		return errors.New("invalid config")
	}

	if cfg.Alert.RateLimitPerHour < 0 {
		return errors.New("alert.rate_limit_per_hour must be >= 0")
	}
	if cfg.Alert.BatchingWindowSeconds < 0 {
		return errors.New("alert.batching_window_seconds must be >= 0")
	}
	switch strings.TrimSpace(cfg.Alert.MinSeverity) {
	case "", "critical", "warning", "info":
	default:
		return errors.New("alert.min_severity must be one of: critical, warning, info, or empty")
	}

	if cfg.Report.ErrorDigestMinCount < 0 {
		return errors.New("report.error_digest_min_count must be >= 0")
	}
	// The stored report threshold uses percentage units, with both endpoints allowed.
	if cfg.Report.ProviderHealthErrorRateThreshold < 0 || cfg.Report.ProviderHealthErrorRateThreshold > 100 {
		return errors.New("report.provider_health_error_rate_threshold must be between 0 and 100")
	}
	return nil
}

// defaultOpsAlertRuntimeSettings allocates a sixty-second evaluator cadence,
// enabled thirty-second Redis lease, and disabled silencing with empty entries.
// The getter uses it for missing or malformed storage, while valid decoded
// fields follow separate normalization.
func defaultOpsAlertRuntimeSettings() *OpsAlertRuntimeSettings {
	return &OpsAlertRuntimeSettings{
		EvaluationIntervalSeconds: 60,
		DistributedLock: OpsDistributedLockSettings{
			Enabled: true,
			Key:     opsAlertEvaluatorLeaderLockKeyDefault,
			// Lease duration becomes integer seconds in stored JSON preferences.
			TTLSeconds: int(opsAlertEvaluatorLeaderLockTTLDefault.Seconds()),
		},
		Silencing: OpsAlertSilencingSettings{
			Enabled:            false,
			GlobalUntilRFC3339: "",
			GlobalReason:       "",
			Entries:            []OpsAlertSilenceEntry{},
		},
	}
}

// normalizeOpsDistributedLockSettings trims a nonnil lease key and fills blank
// key or nonpositive TTL from caller defaults. It leaves Enabled and excessive
// positive TTL supplied, so callers validate enabled writes before
// normalization.
func normalizeOpsDistributedLockSettings(s *OpsDistributedLockSettings, defaultKey string, defaultTTLSeconds int) {
	if s == nil {
		return
	}
	s.Key = strings.TrimSpace(s.Key)
	if s.Key == "" {
		s.Key = defaultKey
	}
	if s.TTLSeconds <= 0 {
		s.TTLSeconds = defaultTTLSeconds
	}
}

// normalizeOpsAlertSilencingSettings mutates nonnil silencing text by trimming
// global and per-entry times/reasons and supplying an empty entry slice. Rule
// pointers and severity slices remain shared, and parsing or future-deadline
// validation is not performed.
func normalizeOpsAlertSilencingSettings(s *OpsAlertSilencingSettings) {
	if s == nil {
		return
	}
	s.GlobalUntilRFC3339 = strings.TrimSpace(s.GlobalUntilRFC3339)
	s.GlobalReason = strings.TrimSpace(s.GlobalReason)
	if s.Entries == nil {
		s.Entries = []OpsAlertSilenceEntry{}
	}
	for i := range s.Entries {
		s.Entries[i].UntilRFC3339 = strings.TrimSpace(s.Entries[i].UntilRFC3339)
		s.Entries[i].Reason = strings.TrimSpace(s.Entries[i].Reason)
	}
}

// validateOpsDistributedLockSettings requires a nonblank key and positive lease
// lifetime up to one day. It does not inspect Enabled;
// UpdateOpsAlertRuntimeSettings calls this validator only when distributed
// locking is enabled.
func validateOpsDistributedLockSettings(s OpsDistributedLockSettings) error {
	if strings.TrimSpace(s.Key) == "" {
		return errors.New("distributed_lock.key is required")
	}
	// Bound an enabled lease lifetime to one positive day in seconds.
	if s.TTLSeconds <= 0 || s.TTLSeconds > int((24*time.Hour).Seconds()) {
		return errors.New("distributed_lock.ttl_seconds must be between 1 and 86400")
	}
	return nil
}

// validateOpsAlertSilencingSettings permits an empty global deadline but
// requires every entry deadline in RFC3339 form. Nonblank text is parsed before
// normalization, so surrounding whitespace can fail. Past deadlines and
// scope/severity values are not rejected here.
func validateOpsAlertSilencingSettings(s OpsAlertSilencingSettings) error {
	parse := func(raw string) error {
		if strings.TrimSpace(raw) == "" {
			return nil
		}
		if _, err := time.Parse(time.RFC3339, raw); err != nil {
			return errors.New("silencing time must be RFC3339")
		}
		return nil
	}

	if err := parse(s.GlobalUntilRFC3339); err != nil {
		return err
	}
	for _, entry := range s.Entries {
		if strings.TrimSpace(entry.UntilRFC3339) == "" {
			return errors.New("silencing.entries.until_rfc3339 is required")
		}
		if _, err := time.Parse(time.RFC3339, entry.UntilRFC3339); err != nil {
			return errors.New("silencing.entries.until_rfc3339 must be RFC3339")
		}
	}
	return nil
}

// GetOpsAlertRuntimeSettings returns family defaults for missing
// service/storage or malformed JSON, and attempts best-effort default storage
// for an absent key. Other read errors propagate; nil context selects
// background. Valid JSON starts from a zero-valued config rather than a default
// overlay.
//
// Nonpositive cadence receives the default, and blank/nonpositive lease values
// and silencing text/lists normalize. Omitted lock Enabled remains false after
// valid null or empty JSON, whereas an absent setting uses true. Positive
// cadence/TTL ranges and stored silencing times are not validated by this read;
// evaluator consumers apply their own runtime selection rules.
func (s *OpsService) GetOpsAlertRuntimeSettings(ctx context.Context) (*OpsAlertRuntimeSettings, error) {
	defaultCfg := defaultOpsAlertRuntimeSettings()
	if s == nil || s.settingRepo == nil {
		return defaultCfg, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	raw, err := s.settingRepo.GetValue(ctx, SettingKeyOpsAlertRuntimeSettings)
	if err != nil {
		// A missing setting attempts one default write with the read context.
		// Encoding or persistence failure is ignored, and the caller still receives
		// defaults.
		if errors.Is(err, ErrSettingNotFound) {
			if b, mErr := json.Marshal(defaultCfg); mErr == nil {
				_ = s.settingRepo.Set(ctx, SettingKeyOpsAlertRuntimeSettings, string(b))
			}
			return defaultCfg, nil
		}
		return nil, err
	}

	cfg := &OpsAlertRuntimeSettings{}
	// Malformed JSON yields family defaults without rewriting the saved value.
	// Valid JSON, including null, retains each destination's own baseline values.
	if err := json.Unmarshal([]byte(raw), cfg); err != nil {
		return defaultCfg, nil
	}

	if cfg.EvaluationIntervalSeconds <= 0 {
		cfg.EvaluationIntervalSeconds = defaultCfg.EvaluationIntervalSeconds
	}
	normalizeOpsDistributedLockSettings(&cfg.DistributedLock, opsAlertEvaluatorLeaderLockKeyDefault, defaultCfg.DistributedLock.TTLSeconds)
	normalizeOpsAlertSilencingSettings(&cfg.Silencing)

	return cfg, nil
}

// UpdateOpsAlertRuntimeSettings validates a complete evaluator config and
// persists it after in-place normalization. Cadence is limited to one day;
// lease and silence validation runs only for their enabled sections, then
// normalization supplies blank/default fields regardless of enablement. Nil
// context selects background for storage work.
//
// Callers retain ownership of the input, which can remain normalized even when
// serialization or storage fails. Successful JSON is decoded into separate
// return storage; its decode error is ignored after persistence. This method
// changes saved settings rather than restarting a loop, whose next runtime read
// sees cadence and silencing choices.
func (s *OpsService) UpdateOpsAlertRuntimeSettings(ctx context.Context, cfg *OpsAlertRuntimeSettings) (*OpsAlertRuntimeSettings, error) {
	if s == nil || s.settingRepo == nil {
		return nil, errors.New("setting repository not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if cfg == nil {
		return nil, errors.New("invalid config")
	}

	// Bound saved evaluator cadence to one positive day in seconds.
	if cfg.EvaluationIntervalSeconds < 1 || cfg.EvaluationIntervalSeconds > int((24*time.Hour).Seconds()) {
		return nil, errors.New("evaluation_interval_seconds must be between 1 and 86400")
	}
	if cfg.DistributedLock.Enabled {
		if err := validateOpsDistributedLockSettings(cfg.DistributedLock); err != nil {
			return nil, err
		}
	}
	if cfg.Silencing.Enabled {
		if err := validateOpsAlertSilencingSettings(cfg.Silencing); err != nil {
			return nil, err
		}
	}

	defaultCfg := defaultOpsAlertRuntimeSettings()
	normalizeOpsDistributedLockSettings(&cfg.DistributedLock, opsAlertEvaluatorLeaderLockKeyDefault, defaultCfg.DistributedLock.TTLSeconds)
	normalizeOpsAlertSilencingSettings(&cfg.Silencing)

	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	if err := s.settingRepo.Set(ctx, SettingKeyOpsAlertRuntimeSettings, string(raw)); err != nil {
		return nil, err
	}

	// Decode persisted JSON into separate return storage after a successful write.
	// Decode errors are ignored; the supplied normalized input remains owned by
	// the caller.
	updated := &OpsAlertRuntimeSettings{}
	_ = json.Unmarshal(raw, updated)
	return updated, nil
}

// defaultOpsAdvancedSettings allocates disabled cleanup with thirty-day
// retention, standard batch pacing, capture preferences, and display defaults.
// Its ignored-status list is independently allocated, and runtime
// initialization overlays saved JSON on this baseline.
func defaultOpsAdvancedSettings() *OpsAdvancedSettings {
	return &OpsAdvancedSettings{
		DataRetention: OpsDataRetentionSettings{
			CleanupEnabled:   false,
			CleanupSchedule:  opsCleanupDefaultSchedule,
			CleanupBatchSize: opsCleanupDefaultBatchSize,
			// Cleanup pacing stores whole milliseconds rather than duration nanoseconds.
			CleanupPauseMS:             int(opsCleanupDefaultBatchPause / time.Millisecond),
			ErrorLogRetentionDays:      30,
			MinuteMetricsRetentionDays: 30,
			HourlyMetricsRetentionDays: 30,
		},
		OpenAIProviderQuotaAutoPause:    OpsOpenAIProviderQuotaAutoPauseSettings{},
		IgnoreCountTokensErrors:         true,  // suppress captured count-token paths
		IgnoreContextCanceled:           true,  // suppress matching cancellation markers
		IgnoreNoAvailableProviders:      false, // retain provider-unavailable diagnostics
		IgnoreInvalidApiKeyErrors:       true,  // normalized legacy admission ignore flag
		IgnoreInsufficientBalanceErrors: false, // retain matching balance diagnostics
		IgnoredStatusCodes:              DefaultOpsIgnoredStatusCodes(),
		DisplayOpenAITokenStats:         false,
		DisplayAlertEvents:              true,
		AutoRefreshEnabled:              false,
		AutoRefreshIntervalSec:          30,
	}
}

// normalizeOpsAdvancedSettings mutates a nonnil advanced config to enforce the
// legacy invalid-key ignore flag, clamp quota defaults, and supply missing
// cleanup/refresh tuning. Negative retention selects thirty days while zero
// keeps full-clearing intent, and ignored-status normalization distinguishes
// nil defaults from an allocated empty policy.
//
// An empty decoded ignored-status list remains empty; nil receives the standard
// exclusions. Normalization does not validate cron syntax or positive tuning
// ranges, so update validation and cleanup reload remain separate consumers of
// the resulting preferences.
func normalizeOpsAdvancedSettings(cfg *OpsAdvancedSettings) {
	if cfg == nil {
		return
	}
	// Admission rejects use dedicated ingress aggregates. Normalize this retained
	// compatibility flag to true while the gateway legacy body-marker filter still
	// reads its value.
	cfg.IgnoreInvalidApiKeyErrors = true
	cfg.OpenAIProviderQuotaAutoPause.DefaultThreshold5h = clampOpsQuotaAutoPauseThreshold(cfg.OpenAIProviderQuotaAutoPause.DefaultThreshold5h)
	cfg.OpenAIProviderQuotaAutoPause.DefaultThreshold7d = clampOpsQuotaAutoPauseThreshold(cfg.OpenAIProviderQuotaAutoPause.DefaultThreshold7d)
	cfg.DataRetention.CleanupSchedule = strings.TrimSpace(cfg.DataRetention.CleanupSchedule)
	if cfg.DataRetention.CleanupSchedule == "" {
		cfg.DataRetention.CleanupSchedule = opsCleanupDefaultSchedule
	}
	if cfg.DataRetention.CleanupBatchSize <= 0 {
		cfg.DataRetention.CleanupBatchSize = opsCleanupDefaultBatchSize
	}
	if cfg.DataRetention.CleanupPauseMS <= 0 {
		// Runtime pacing converts the default duration to whole stored milliseconds.
		cfg.DataRetention.CleanupPauseMS = int(opsCleanupDefaultBatchPause / time.Millisecond)
	}
	// Retain zero as cleanup's whole-table clearing choice. Negative legacy
	// retention values receive defaults here, while administrator updates validate
	// those values before reaching normalization.
	if cfg.DataRetention.ErrorLogRetentionDays < 0 {
		cfg.DataRetention.ErrorLogRetentionDays = 30
	}
	if cfg.DataRetention.MinuteMetricsRetentionDays < 0 {
		cfg.DataRetention.MinuteMetricsRetentionDays = 30
	}
	if cfg.DataRetention.HourlyMetricsRetentionDays < 0 {
		cfg.DataRetention.HourlyMetricsRetentionDays = 30
	}
	if cfg.AutoRefreshIntervalSec <= 0 {
		cfg.AutoRefreshIntervalSec = 30
	}
	cfg.IgnoredStatusCodes = NormalizeOpsIgnoredStatusCodes(cfg.IgnoredStatusCodes)
}

// clampOpsQuotaAutoPauseThreshold maps nonpositive values to zero and values
// above one to one. Intermediate values remain supplied, including NaN because
// comparisons do not select either branch; later JSON encoding rejects
// unsupported numbers.
func clampOpsQuotaAutoPauseThreshold(value float64) float64 {
	// Quota policy uses allowance fractions; zero disables that default threshold.
	if value <= 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

// validateOpsAdvancedSettings checks retention from zero through 365 days,
// optional cleanup pacing ranges, refresh cadence, and every supplied HTTP
// status code. Validation precedes normalization, so negative values fail
// updates even when the normalizer can default them during reads; cron syntax
// is left to cleanup Reload.
func validateOpsAdvancedSettings(cfg *OpsAdvancedSettings) error {
	if cfg == nil {
		return errors.New("invalid config")
	}
	// Retention zero selects clearing; positive settings retain up to 365 calendar
	// days.
	if cfg.DataRetention.ErrorLogRetentionDays < 0 || cfg.DataRetention.ErrorLogRetentionDays > 365 {
		return errors.New("error_log_retention_days must be between 0 and 365")
	}
	if cfg.DataRetention.MinuteMetricsRetentionDays < 0 || cfg.DataRetention.MinuteMetricsRetentionDays > 365 {
		return errors.New("minute_metrics_retention_days must be between 0 and 365")
	}
	if cfg.DataRetention.HourlyMetricsRetentionDays < 0 || cfg.DataRetention.HourlyMetricsRetentionDays > 365 {
		return errors.New("hourly_metrics_retention_days must be between 0 and 365")
	}
	// Cleanup pacing bounds rows and milliseconds; zero selects normalized defaults.
	if cfg.DataRetention.CleanupBatchSize != 0 && (cfg.DataRetention.CleanupBatchSize < 100 || cfg.DataRetention.CleanupBatchSize > 5000) {
		return errors.New("cleanup_batch_size must be between 100 and 5000")
	}
	if cfg.DataRetention.CleanupPauseMS != 0 && (cfg.DataRetention.CleanupPauseMS < 1 || cfg.DataRetention.CleanupPauseMS > 2000) {
		return errors.New("cleanup_pause_ms must be between 1 and 2000")
	}
	// Dashboard refresh intervals range from fifteen seconds through five minutes.
	if cfg.AutoRefreshIntervalSec < 15 || cfg.AutoRefreshIntervalSec > 300 {
		return errors.New("auto_refresh_interval_seconds must be between 15 and 300")
	}
	// Reject a bad exclusion entry before normalization can discard it.
	for _, code := range cfg.IgnoredStatusCodes {
		if code < 100 || code > 599 {
			return errors.New("ignored_status_codes must be valid HTTP status codes")
		}
	}
	return nil
}

// cloneOpsIgnoredStatusCodes allocates and copies a supplied exclusion slice,
// retaining nil versus allocated-empty identity. Entries remain ordered and
// unvalidated here; callers choose normalization separately when loading or
// updating policy.
func cloneOpsIgnoredStatusCodes(codes []int) []int {
	if codes == nil {
		return nil
	}
	out := make([]int, len(codes))
	copy(out, codes)
	return out
}

// resolveOpsIgnoredStatusCodes delegates a fresh persisted advanced-settings
// read for query classification. It returns an independent exclusion slice
// under the repository helper's read-error policy, rather than using the
// in-memory advanced snapshot.
func (s *OpsService) resolveOpsIgnoredStatusCodes(ctx context.Context) []int {
	return resolveOpsIgnoredStatusCodesFromRepo(ctx, s.settingRepo)
}

// GetOpsAdvancedSettings returns a pointer to the current advanced snapshot
// value and ignores ctx. It performs no synchronous settings query, and the
// copied value retains IgnoredStatusCodes storage that callers treat as
// read-only.
func (s *OpsService) GetOpsAdvancedSettings(ctx context.Context) (*OpsAdvancedSettings, error) {
	_ = ctx
	cfg := s.OpsAdvancedSettingsSnapshot()
	return &cfg, nil
}

// OpsAdvancedSettingsSnapshot returns a value copy from the published runtime
// snapshot, or newly constructed defaults when absent. IgnoredStatusCodes
// remains shared when a snapshot exists, so callers copy that slice before
// editing returned values.
func (s *OpsService) OpsAdvancedSettingsSnapshot() OpsAdvancedSettings {
	if s != nil {
		if snapshot := s.runtimeSettings.Load(); snapshot != nil {
			return snapshot.advanced
		}
	}
	return *defaultOpsAdvancedSettings()
}

// UpdateOpsAdvancedSettings validates and normalizes caller-owned advanced
// preferences, persists JSON, and publishes the value into the runtime
// snapshot. The normalized input's IgnoredStatusCodes slice remains reachable
// from that snapshot, so callers leave input storage immutable after
// publication; the returned JSON-decoded config has separate slice storage. Nil
// context selects background for storage work.
//
// Quota publication follows persistence and snapshot publication, then cleanup
// Reload runs with the supplied context. Reload failure is reported and
// suppressed, leaving the settings update successful despite a failed schedule
// change. Callbacks are synchronous and independently bound, and their side
// effects are outside the settings write. The input can remain mutated if
// serialization or persistence fails after normalization.
func (s *OpsService) UpdateOpsAdvancedSettings(ctx context.Context, cfg *OpsAdvancedSettings) (*OpsAdvancedSettings, error) {
	if s == nil || s.settingRepo == nil {
		return nil, errors.New("setting repository not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if cfg == nil {
		return nil, errors.New("invalid config")
	}

	if err := validateOpsAdvancedSettings(cfg); err != nil {
		return nil, err
	}

	normalizeOpsAdvancedSettings(cfg)
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	// Persist before publishing the snapshot and invoking consumers. A write
	// failure leaves runtime publication untouched even though normalization has
	// already mutated caller input.
	if err := s.settingRepo.Set(ctx, SettingKeyOpsAdvancedSettings, string(raw)); err != nil {
		return nil, err
	}
	s.storeAdvancedSettingsSnapshot(cfg)
	// Publish quota thresholds synchronously after settings and snapshot
	// publication. The scheduler's cache can update before cleanup Reload runs,
	// and callback failure is outside the persisted write.
	if s.quotaAutoPauseSink != nil {
		s.quotaAutoPauseSink(cfg.OpenAIProviderQuotaAutoPause)
	}

	// Reload cleanup after settings already persist. A failed schedule rebuild is
	// reported through the service logger and suppressed, leaving the accepted
	// preferences saved.
	if s.cleanupReloader != nil {
		if rerr := s.cleanupReloader.Reload(ctx); rerr != nil {
			s.report(
				"[OpsSettings] cleanup reload after advanced-settings update failed: %v", rerr)
		}
	}

	// Decode persisted JSON into separate returned storage after consumer calls.
	// The decode error is suppressed, while the runtime snapshot still retains the
	// normalized input slice.
	updated := &OpsAdvancedSettings{}
	_ = json.Unmarshal(raw, updated)
	return updated, nil
}

// defaultOpsMetricThresholds allocates separate pointers for default dashboard
// cutoffs: SLA 99.5 percent, first-token p99 five hundred milliseconds, and
// request/upstream error rates five percent. These preferences color dashboard
// measurements and do not define evaluator alert rules.
func defaultOpsMetricThresholds() *OpsMetricThresholds {
	// Dashboard colors compare percentage cutoffs separately from the first-token
	// millisecond cutoff, so the defaults retain those units before pointer
	// construction.
	slaMin := 99.5
	ttftMax := 500.0
	reqErrMax := 5.0
	upstreamErrMax := 5.0
	return &OpsMetricThresholds{
		SLAPercentMin:               &slaMin,
		TTFTp99MsMax:                &ttftMax,
		RequestErrorRatePercentMax:  &reqErrMax,
		UpstreamErrorRatePercentMax: &upstreamErrMax,
	}
}

// GetMetricThresholds returns default dashboard cutoffs for missing
// service/storage or malformed JSON and best-effort initializes an absent key.
// Other read errors propagate. Valid JSON decodes into empty pointer fields, so
// null or omitted cutoffs remain nil rather than receiving individual defaults.
// Nil context selects background for storage work.
//
// OpsDashboardHeader treats an absent cutoff or measurement as a normal display
// classification. A saved null or empty object can therefore leave colors
// unbounded while a missing or malformed setting returns configured defaults;
// this reader applies no normalization or individual range validation after
// successful decoding.
func (s *OpsService) GetMetricThresholds(ctx context.Context) (*OpsMetricThresholds, error) {
	defaultCfg := defaultOpsMetricThresholds()
	if s == nil || s.settingRepo == nil {
		return defaultCfg, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	raw, err := s.settingRepo.GetValue(ctx, SettingKeyOpsMetricThresholds)
	if err != nil {
		// A missing setting attempts one default write with the read context.
		// Encoding or persistence failure is ignored, and the caller still receives
		// defaults.
		if errors.Is(err, ErrSettingNotFound) {
			if b, mErr := json.Marshal(defaultCfg); mErr == nil {
				_ = s.settingRepo.Set(ctx, SettingKeyOpsMetricThresholds, string(b))
			}
			return defaultCfg, nil
		}
		return nil, err
	}

	cfg := &OpsMetricThresholds{}
	// Malformed JSON yields family defaults without rewriting the saved value.
	// Valid JSON, including null, retains each destination's own baseline values.
	if err := json.Unmarshal([]byte(raw), cfg); err != nil {
		return defaultCfg, nil
	}

	return cfg, nil
}

// UpdateMetricThresholds validates supplied percentage cutoffs from zero to one
// hundred and a nonnegative first-token millisecond cutoff, then persists the
// complete pointer-valued config. Nil fields omit a cutoff. Successful JSON is
// decoded into separate returned scalar pointers, with decode error suppressed
// after storage. Nil context selects background for storage work.
func (s *OpsService) UpdateMetricThresholds(ctx context.Context, cfg *OpsMetricThresholds) (*OpsMetricThresholds, error) {
	if s == nil || s.settingRepo == nil {
		return nil, errors.New("setting repository not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if cfg == nil {
		return nil, errors.New("invalid config")
	}

	// Percent cutoffs use a zero-to-one-hundred range, while the first-token
	// cutoff uses nonnegative milliseconds; nil pointers mean that cutoff is
	// absent.
	if cfg.SLAPercentMin != nil && (*cfg.SLAPercentMin < 0 || *cfg.SLAPercentMin > 100) {
		return nil, errors.New("sla_percent_min must be between 0 and 100")
	}
	if cfg.TTFTp99MsMax != nil && *cfg.TTFTp99MsMax < 0 {
		return nil, errors.New("ttft_p99_ms_max must be >= 0")
	}
	if cfg.RequestErrorRatePercentMax != nil && (*cfg.RequestErrorRatePercentMax < 0 || *cfg.RequestErrorRatePercentMax > 100) {
		return nil, errors.New("request_error_rate_percent_max must be between 0 and 100")
	}
	if cfg.UpstreamErrorRatePercentMax != nil && (*cfg.UpstreamErrorRatePercentMax < 0 || *cfg.UpstreamErrorRatePercentMax > 100) {
		return nil, errors.New("upstream_error_rate_percent_max must be between 0 and 100")
	}

	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	if err := s.settingRepo.Set(ctx, SettingKeyOpsMetricThresholds, string(raw)); err != nil {
		return nil, err
	}

	// Decode successful JSON into independent cutoff pointers for the response
	// after persistence. A decode error is suppressed rather than undoing the
	// completed settings write.
	updated := &OpsMetricThresholds{}
	_ = json.Unmarshal(raw, updated)
	return updated, nil
}
