package moderation

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// config.go manages content moderation configuration normalization, API-key
// metadata, and cached runtime policy snapshots.
// Administrator updates validate proxy, threshold, text-limit, and API-key
// constraints before publishing an immutable snapshot in process memory so
// local reads avoid per-request settings queries.
//
// Expired snapshots trigger at most one background refresh under
// runtimeRefreshMu while continuing to serve the stale policy. Rejected
// background launches release runtimeRefreshMu immediately, and refresh errors
// extend a retry backoff window so transient storage failures cannot stall
// request admission or configuration saves.

// ContentModerationConfig stores ordinary audit policy, credential selection,
// review retention, and separate Cyber warning controls.
type ContentModerationConfig struct {
	// Enabled and Mode gate ordinary Check after the separate risk-control master
	// switch. Cyber warning and session-scope helpers use their own switches and
	// scope rules instead of these ordinary audit gates.
	Enabled bool   `json:"enabled"`  // ordinary audit enablement switch
	Mode    string `json:"mode"`     // ordinary audit execution mode
	BaseURL string `json:"base_url"` // external moderation endpoint base
	Model   string `json:"model"`    // external moderation model name
	// ProxyID selects stored audit proxy resolution when present. Nil selects
	// direct transport, and configuration normalization clears nonpositive
	// selections before ordinary runtime use.
	// selected outbound proxy identifier
	ProxyID *int64 `json:"proxy_id,omitempty"`
	// APIKey, APIKeys, and APIKeyMetadata represent stored credentials and their
	// hash-addressed attributes. normalize moves the legacy single value into the
	// ordered deduplicated list, then aligns metadata with retained key order.
	// legacy single credential value
	APIKey string `json:"api_key,omitempty"`
	// ordered stored moderation credentials
	APIKeys []string `json:"api_keys,omitempty"`
	// hash-addressed saved credential attributes
	APIKeyMetadata []ContentModerationAPIKeyMetadata `json:"api_key_metadata,omitempty"`
	// milliseconds
	TimeoutMS int `json:"timeout_ms"`
	// SampleRate selects external auditing after keyword and hash checks. A zero
	// rate skips that later stage while already confirmed local blocks still
	// apply.
	// percent
	SampleRate int `json:"sample_rate"`
	// AllGroups and GroupIDs define ordinary and Cyber group scope. AllGroups
	// admits absent group context; selected-group scope requires a supplied
	// identifier in the normalized list.
	// all-group audit scope switch
	AllGroups bool    `json:"all_groups"`
	GroupIDs  []int64 `json:"group_ids"` // selected group scope identifiers
	// RecordNonHits enables successful non-hit logs. Flagged or incomplete audit
	// results are recorded regardless of this switch in checkSync.
	// successful non-hit logging switch
	RecordNonHits bool `json:"record_non_hits"`
	// Thresholds decides known-category flagging after unit score maxima are
	// merged. Category order chooses known ties; unknown scores can supply a
	// displayed maximum without flagging.
	// category score decision thresholds
	Thresholds map[string]float64 `json:"thresholds"`
	// WorkerCount and QueueSize are live admission choices. The constructor fixes
	// maximum goroutines and channel capacity; workers consult the current count,
	// and senders sample the configured queue limit.
	// configured admitted worker count
	WorkerCount int `json:"worker_count"`
	QueueSize   int `json:"queue_size"` // configured queue admission limit
	// BlockStatus and BlockMessage describe blocked decisions. Hash matches append
	// their input hash to a nonempty message, while keyword and API blocks use the
	// configured message directly.
	// blocked request HTTP status
	BlockStatus int `json:"block_status"`
	// blocked request client message
	BlockMessage string `json:"block_message"`
	// ordinary violation notice switch
	EmailOnHit bool `json:"email_on_hit"`
	// AutoBanEnabled, BanThreshold, and ViolationWindowHours govern ordinary
	// actor-account actions. Counting reads earlier persisted non-hash hits before
	// the new log is written, so this path is separate from Cyber counting.
	// ordinary automatic ban switch
	AutoBanEnabled bool `json:"auto_ban_enabled"`
	// ordinary counted-hit ban threshold
	BanThreshold         int `json:"ban_threshold"`
	ViolationWindowHours int `json:"violation_window_hours"` // hours
	// retries after initial attempt
	RetryCount          int `json:"retry_count"`
	HitRetentionDays    int `json:"hit_retention_days"`     // days
	NonHitRetentionDays int `json:"non_hit_retention_days"` // days
	// recorded hash lookup switch
	PreHashCheckEnabled bool `json:"pre_hash_check_enabled"`
	// Cyber evidence recording switch
	CyberWarningEnabled bool `json:"cyber_warning_enabled"`
	// CyberAutoBanEnabled, CyberBanThreshold, and CyberWindowHours govern the
	// repository Cyber transaction policy. Its current warning participates in
	// counting, and account invalidation and notification follow commit.
	// Cyber automatic ban switch
	CyberAutoBanEnabled bool `json:"cyber_auto_ban_enabled"`
	// Cyber warning ban threshold
	CyberBanThreshold int `json:"cyber_ban_threshold"`
	CyberWindowHours  int `json:"cyber_violation_window_hours"` // hours
	// ordered substring block keywords
	BlockedKeywords []string `json:"blocked_keywords"`
	// keyword and API selection
	KeywordBlockingMode string `json:"keyword_blocking_mode"`
	// requested model scope selection
	ModelFilter ContentModerationModelFilter `json:"model_filter"`
	// AuditUserTextMaxChars, AuditImages, AuditToolOutputs, and
	// AuditToolOutputMaxChars select external audit units. User and tool rune
	// budgets are spent separately; disabling tool output also excludes tool
	// images while retained review input follows each Check retention choice.
	// user text rune budget
	AuditUserTextMaxChars int `json:"audit_user_text_max_chars"`
	// image audit inclusion switch
	AuditImages bool `json:"audit_images"`
	// tool audit inclusion switch
	AuditToolOutputs bool `json:"audit_tool_outputs"`
	// tool text rune budget
	AuditToolOutputMaxChars int `json:"audit_tool_output_max_chars"`
}

// ContentModerationConfigView is the administrator policy view with credential
// masks and process-local health observations.
type ContentModerationConfigView struct {
	Enabled bool   `json:"enabled"`  // ordinary audit enablement switch
	Mode    string `json:"mode"`     // ordinary audit execution mode
	BaseURL string `json:"base_url"` // external moderation endpoint base
	Model   string `json:"model"`    // external moderation model name
	ProxyID *int64 `json:"proxy_id"` // selected outbound proxy identifier
	// APIKeyConfigured, APIKeyMasked, APIKeyCount, APIKeyMasks, and APIKeyStatuses
	// describe the normalized saved pool. Masks and statuses follow its order,
	// while each health entry is sampled independently by configView.
	// nonempty saved credential pool
	APIKeyConfigured bool `json:"api_key_configured"`
	// first saved credential mask
	APIKeyMasked string `json:"api_key_masked"`
	// normalized saved credential count
	APIKeyCount int `json:"api_key_count"`
	// ordered saved credential masks
	APIKeyMasks []string `json:"api_key_masks"`
	// sampled configured credential health
	APIKeyStatuses []ContentModerationAPIKeyStatus `json:"api_key_statuses"`
	// milliseconds
	TimeoutMS  int  `json:"timeout_ms"`
	SampleRate int  `json:"sample_rate"` // percent
	AllGroups  bool `json:"all_groups"`  // all-group audit scope switch
	// selected group scope identifiers
	GroupIDs []int64 `json:"group_ids"`
	// successful non-hit logging switch
	RecordNonHits bool `json:"record_non_hits"`
	// category score decision thresholds
	Thresholds map[string]float64 `json:"thresholds"`
	// configured admitted worker count
	WorkerCount int `json:"worker_count"`
	// configured queue admission limit
	QueueSize   int `json:"queue_size"`
	BlockStatus int `json:"block_status"` // blocked request HTTP status
	// blocked request client message
	BlockMessage string `json:"block_message"`
	// ordinary violation notice switch
	EmailOnHit bool `json:"email_on_hit"`
	// ordinary automatic ban switch
	AutoBanEnabled bool `json:"auto_ban_enabled"`
	// ordinary counted-hit ban threshold
	BanThreshold         int `json:"ban_threshold"`
	ViolationWindowHours int `json:"violation_window_hours"` // hours
	// retries after initial attempt
	RetryCount          int `json:"retry_count"`
	HitRetentionDays    int `json:"hit_retention_days"`     // days
	NonHitRetentionDays int `json:"non_hit_retention_days"` // days
	// recorded hash lookup switch
	PreHashCheckEnabled bool `json:"pre_hash_check_enabled"`
	// Cyber evidence recording switch
	CyberWarningEnabled bool `json:"cyber_warning_enabled"`
	// Cyber automatic ban switch
	CyberAutoBanEnabled bool `json:"cyber_auto_ban_enabled"`
	// Cyber warning ban threshold
	CyberBanThreshold int `json:"cyber_ban_threshold"`
	CyberWindowHours  int `json:"cyber_violation_window_hours"` // hours
	// ordered substring block keywords
	BlockedKeywords []string `json:"blocked_keywords"`
	// keyword and API selection
	KeywordBlockingMode string `json:"keyword_blocking_mode"`
	// requested model scope selection
	ModelFilter ContentModerationModelFilter `json:"model_filter"`
	// user text rune budget
	AuditUserTextMaxChars int `json:"audit_user_text_max_chars"`
	// image audit inclusion switch
	AuditImages bool `json:"audit_images"`
	// tool audit inclusion switch
	AuditToolOutputs bool `json:"audit_tool_outputs"`
	// tool text rune budget
	AuditToolOutputMaxChars int `json:"audit_tool_output_max_chars"`
}

// ContentModerationAPIKeyMetadata associates scheduling priority and an
// administrator note with a credential hash.
type ContentModerationAPIKeyMetadata struct {
	KeyHash  string `json:"key_hash"` // hexadecimal normalized credential digest
	Priority int    `json:"priority"` // smooth scheduling weight
	Note     string `json:"note"`     // administrator credential display note
}

// ContentModerationAPIKeyEntryInput supplies a literal credential and
// attributes for administrator key-list edits.
type ContentModerationAPIKeyEntryInput struct {
	APIKey string `json:"api_key"` // literal provider authentication credential
	// Priority permits zero for defaulting in new entries. Validation rejects
	// negative or excessive values before normalization chooses the supported
	// scheduling weight.
	Priority int    `json:"priority"` // requested smooth scheduling weight
	Note     string `json:"note"`     // administrator credential display note
}

// ContentModerationAPIKeyStatus is one sampled credential-health observation
// with administrator scheduling metadata.
type ContentModerationAPIKeyStatus struct {
	Index int `json:"index"` // zero-based supplied credential position
	// hexadecimal normalized credential digest
	KeyHash string `json:"key_hash"`
	Masked  string `json:"masked"` // display credential tail mask
	// Status chooses active freeze before stored error and then observed success.
	// An expired freeze can leave error visible until a later successful attempt
	// clears the diagnostic.
	Status string `json:"status"` // unknown, frozen, error, or ok
	// freezable errors since success
	FailureCount int `json:"failure_count"`
	// cumulative successful attempt count
	SuccessCount int64 `json:"success_count"`
	// latest truncated attempt diagnostic
	LastError string `json:"last_error"`
	// latest observed attempt time
	LastCheckedAt *time.Time `json:"last_checked_at,omitempty"`
	// currently active freeze end
	FrozenUntil   *time.Time `json:"frozen_until,omitempty"`
	LastLatencyMS int        `json:"last_latency_ms"` // milliseconds
	// latest observed HTTP status
	LastHTTPStatus int `json:"last_http_status"`
	// LastTested is set by ordinary audit health updates as well as administrator
	// probes. Its name does not restrict it to a manually requested key test.
	// attempt observation has been recorded
	LastTested bool `json:"last_tested"`
	// Configured identifies saved-pool origin in TestAPIKeys and is true in
	// configured status lists. Supplied probe credentials can carry false even
	// when their literal value matches a saved credential.
	Configured bool `json:"configured"` // saved-pool test source flag
	Priority   int  `json:"priority"`   // smooth scheduling weight
	// administrator credential display note
	Note string `json:"note"`
}

// ContentModerationAPIKeyLoad reports sampled synchronous attempt counters and
// display metadata for one credential.
type ContentModerationAPIKeyLoad struct {
	Index int `json:"index"` // zero-based configured key position
	// hexadecimal normalized credential digest
	KeyHash string `json:"key_hash"`
	Masked  string `json:"masked"` // display credential tail mask
	Status  string `json:"status"` // sampled key availability label
	// Active, Total, Success, Errors, and AvgLatencyMS use key-health synchronous
	// counters. The status and latest attempt fields are sampled before those
	// counters, so concurrent updates can separate their observation times.
	Active int64 `json:"active"` // tracked synchronous in-flight attempts
	Total  int64 `json:"total"`  // tracked synchronous completed attempts
	// tracked synchronous successful attempts
	Success        int64  `json:"success"`
	Errors         int64  `json:"errors"`           // tracked synchronous failed attempts
	AvgLatencyMS   int64  `json:"avg_latency_ms"`   // milliseconds
	LastLatencyMS  int    `json:"last_latency_ms"`  // milliseconds
	LastHTTPStatus int    `json:"last_http_status"` // latest observed HTTP status
	Priority       int    `json:"priority"`         // smooth scheduling weight
	Note           string `json:"note"`             // administrator credential display note
}

// TestContentModerationAPIKeysInput supplies administrator probe content and
// temporary connection or credential choices.
type TestContentModerationAPIKeysInput struct {
	// APIKeys selects supplied nonblank credentials, or the saved pool when the
	// list normalizes to empty. A saved pool with audit content tests one
	// scheduled usable key.
	APIKeys []string `json:"api_keys"` // candidate moderation credentials
	// BaseURL, Model, and TimeoutMS override saved connection choices when
	// nonblank or positive. Blank endpoint/model and nonpositive timeout retain
	// saved values for the probe.
	BaseURL   string `json:"base_url"`   // temporary endpoint base override
	Model     string `json:"model"`      // temporary moderation model override
	TimeoutMS int    `json:"timeout_ms"` // milliseconds
	// ProxyID uses the saved selection when nil. Positive values select that
	// proxy, while nonpositive values request direct transport for this key test.
	ProxyID *int64 `json:"proxy_id"` // temporary outbound proxy selection
	// Prompt and Images select probe content. Empty selected content sends hello,
	// while nonblank images pass inline data and byte-limit checks before any
	// credential is called.
	Prompt string   `json:"prompt"` // administrator test text input
	Images []string `json:"images"` // inline base64 test references
}

// TestContentModerationAPIKeysResult contains key observations and the first
// successful probe score evaluation.
type TestContentModerationAPIKeysResult struct {
	// per-probed-key health observations
	Items []ContentModerationAPIKeyStatus `json:"items"`
	// AuditResult is absent when no successful probe result exists. The first
	// successful result supplies it even when later credentials succeed or fail
	// with different scores.
	// first successful result evaluation
	AuditResult *ContentModerationTestAuditResult `json:"audit_result,omitempty"`
	// accepted test image count
	ImageCount int `json:"image_count"`
}

// ContentModerationTestAuditResult records locally evaluated scores and
// effective thresholds for administrator probe display.
type ContentModerationTestAuditResult struct {
	Flagged bool `json:"flagged"` // local known-threshold hit
	// displayed maximum score category
	HighestCategory string `json:"highest_category"`
	// HighestScore and CompositeScore hold the same displayed maximum. Unknown
	// categories can supply that maximum while Flagged remains determined by the
	// known-category threshold comparisons.
	// displayed maximum category score
	HighestScore float64 `json:"highest_score"`
	// duplicate highest score display
	CompositeScore float64 `json:"composite_score"`
	// copied provider category scores
	CategoryScores map[string]float64 `json:"category_scores"`
	// copied effective decision thresholds
	Thresholds map[string]float64 `json:"thresholds"`
}

// UpdateContentModerationConfigInput retains field presence and credential-edit
// commands for partial administrator policy updates.
type UpdateContentModerationConfigInput struct {
	// Enabled, Mode, BaseURL, and Model override the saved values when their
	// pointers are present. Omitted or JSON-null fields remain nil and leave the
	// corresponding saved values unchanged.
	Enabled *bool   `json:"enabled"`  // ordinary audit enablement switch
	Mode    *string `json:"mode"`     // ordinary audit execution mode
	BaseURL *string `json:"base_url"` // external moderation endpoint base
	Model   *string `json:"model"`    // external moderation model name
	// ProxyID leaves selection unchanged when nil. A positive value selects that
	// stored proxy; a present nonpositive value clears selection and uses direct
	// audit transport.
	// selected outbound proxy identifier
	ProxyID *int64 `json:"proxy_id"`
	// APIKey, APIKeys, APIKeyEntries, APIKeyUpdates, APIKeysMode,
	// DeleteAPIKeyHashes, and ClearAPIKey describe credential editing in
	// UpdateConfig. ClearAPIKey wins over other key operations; replacement skips
	// hash deletion, and supplied entry attributes override metadata updates for
	// the same retained key.
	// single legacy credential input
	APIKey  *string   `json:"api_key"`
	APIKeys *[]string `json:"api_keys"` // ordered credential list input
	// credential and metadata additions
	APIKeyEntries *[]ContentModerationAPIKeyEntryInput `json:"api_key_entries"`
	// metadata updates by hash
	APIKeyUpdates *[]ContentModerationAPIKeyMetadata `json:"api_key_updates"`
	// append or replace selection
	APIKeysMode string `json:"api_keys_mode"`
	// credential hashes for deletion
	DeleteAPIKeyHashes *[]string `json:"delete_api_key_hashes"`
	// credential pool clearing request
	ClearAPIKey bool `json:"clear_api_key"`
	// TimeoutMS and SampleRate retain saved values when their pointers are absent.
	// Present values pass through the core normalization rules before
	// configuration persistence.
	// milliseconds
	TimeoutMS  *int `json:"timeout_ms"`
	SampleRate *int `json:"sample_rate"` // percent
	// AllGroups, GroupIDs, RecordNonHits, and Thresholds contribute writes when
	// supplied. Missing pointers retain saved values; the core normalizes IDs and
	// overlays recognized thresholds on its default category set.
	// all-group audit scope switch
	AllGroups *bool `json:"all_groups"`
	// selected group scope identifiers
	GroupIDs *[]int64 `json:"group_ids"`
	// successful non-hit logging switch
	RecordNonHits *bool `json:"record_non_hits"`
	// category score decision thresholds
	Thresholds *map[string]float64 `json:"thresholds"`
	// WorkerCount, QueueSize, BlockStatus, and BlockMessage retain saved values
	// when absent. Supplied values are validated or normalized by UpdateConfig
	// before the settings write can publish them.
	// configured admitted worker count
	WorkerCount *int `json:"worker_count"`
	// configured queue admission limit
	QueueSize   *int `json:"queue_size"`
	BlockStatus *int `json:"block_status"` // blocked request HTTP status
	// blocked request client message
	BlockMessage *string `json:"block_message"`
	// EmailOnHit, AutoBanEnabled, BanThreshold, ViolationWindowHours, RetryCount,
	// HitRetentionDays, and NonHitRetentionDays retain saved values when absent.
	// Present values select ordinary hit side effects, retry policy, and retention
	// through core normalization.
	// ordinary violation notice switch
	EmailOnHit *bool `json:"email_on_hit"`
	// ordinary automatic ban switch
	AutoBanEnabled *bool `json:"auto_ban_enabled"`
	// ordinary counted-hit ban threshold
	BanThreshold         *int `json:"ban_threshold"`
	ViolationWindowHours *int `json:"violation_window_hours"` // hours
	// retries after initial attempt
	RetryCount          *int `json:"retry_count"`
	HitRetentionDays    *int `json:"hit_retention_days"`     // days
	NonHitRetentionDays *int `json:"non_hit_retention_days"` // days
	// PreHashCheckEnabled, CyberWarningEnabled, CyberAutoBanEnabled,
	// CyberBanThreshold, and CyberWindowHours retain saved values when absent.
	// Their supplied values govern hash admission and the separately counted Cyber
	// evidence and ban paths.
	// recorded hash lookup switch
	PreHashCheckEnabled *bool `json:"pre_hash_check_enabled"`
	// Cyber evidence recording switch
	CyberWarningEnabled *bool `json:"cyber_warning_enabled"`
	// Cyber automatic ban switch
	CyberAutoBanEnabled *bool `json:"cyber_auto_ban_enabled"`
	// Cyber warning ban threshold
	CyberBanThreshold *int `json:"cyber_ban_threshold"`
	CyberWindowHours  *int `json:"cyber_violation_window_hours"` // hours
	// BlockedKeywords, KeywordBlockingMode, and ModelFilter retain saved values
	// when absent. The core normalizes supplied lists and applies keyword mode
	// before hash checking on the pre-block request path.
	// ordered substring block keywords
	BlockedKeywords *[]string `json:"blocked_keywords"`
	// keyword and API selection
	KeywordBlockingMode *string `json:"keyword_blocking_mode"`
	// requested model scope selection
	ModelFilter *ContentModerationModelFilter `json:"model_filter"`
	// AuditUserTextMaxChars, AuditImages, AuditToolOutputs, and
	// AuditToolOutputMaxChars retain saved values when absent. Supplied values
	// select what reaches external auditing while full review material follows the
	// retention field on each Check input.
	// user text rune budget
	AuditUserTextMaxChars *int `json:"audit_user_text_max_chars"`
	// image audit inclusion switch
	AuditImages *bool `json:"audit_images"`
	// tool audit inclusion switch
	AuditToolOutputs *bool `json:"audit_tool_outputs"`
	// tool text rune budget
	AuditToolOutputMaxChars *int `json:"audit_tool_output_max_chars"`
}

// ContentModerationModelFilter selects all models or exact requested-model
// membership by include or exclude mode.
type ContentModerationModelFilter struct {
	// Type and Models are normalized together before model checks. All clears
	// Models; include with no names rejects every requested model, while exclude
	// with no names admits it.
	Type   string   `json:"type"`   // all, include, or exclude
	Models []string `json:"models"` // exact requested-model name list
}

// contentModerationRuntimeSnapshot is an immutable policy and keyword index
// published for hot-path request checks.
type contentModerationRuntimeSnapshot struct {
	riskControlEnabled bool // snapshot master-switch observation
	// config, keywordMatcher, configDigest, and loadedAt are published in one
	// immutable entry. An unchanged digest reuses config and matcher storage while
	// a new entry refreshes risk enablement and load time.
	// immutable normalized policy storage
	config *ContentModerationConfig
	// immutable configured keyword index
	keywordMatcher *contentModerationKeywordMatcher
	configDigest   [sha256.Size]byte // SHA-256 of stored JSON bytes
	loadedAt       time.Time         // successful snapshot load time
}

// ContentModerationDefaultThresholds returns a new known-category threshold map
// for local moderation decisions.
func ContentModerationDefaultThresholds() map[string]float64 {
	return map[string]float64{
		"harassment":             0.98,
		"harassment/threatening": 0.90,
		"hate":                   0.65,
		"hate/threatening":       0.65,
		"illicit":                0.95,
		"illicit/violent":        0.95,
		"self-harm":              0.65,
		"self-harm/intent":       0.85,
		"self-harm/instructions": 0.65,
		"sexual":                 0.65,
		"sexual/minors":          0.65,
		"violence":               0.95,
		"violence/graphic":       0.95,
	}
}

// GetConfig reads stored policy and returns its masked administrator view with
// copied configurable collections. It uses the supplied context for storage,
// and reports malformed JSON or read failures instead of consulting the
// hot-path runtime snapshot.
func (s *ContentModerationService) GetConfig(ctx context.Context) (*ContentModerationConfigView, error) {
	cfg, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	return s.configView(cfg), nil
}

// UpdateConfig applies supplied fields to a newly loaded policy and persists
// the normalized result before publishing its process-local snapshot. Nil
// pointers retain loaded values. ClearAPIKey wins over credential edits;
// replacement replaces the pool when APIKeys or APIKeyEntries is supplied,
// while append can delete hashes and add keys with supplied entry attributes
// applied last.
//
// Replacement mode by itself keeps the current list and skips hash deletion.
// Validation or persistence failure returns before publication. Successful
// saving can wait behind initial loading or an admitted refresh before copied
// policy is published with current risk enablement. Concurrent read/write
// updates and replacements have no persisted revision ordering, so callers
// coordinate conflicting administrator edits.
func (s *ContentModerationService) UpdateConfig(ctx context.Context, input UpdateContentModerationConfigInput) (*ContentModerationConfigView, error) {
	cfg, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateContentModerationAPIKeyInputs(input.APIKeyEntries, input.APIKeyUpdates); err != nil {
		return nil, err
	}
	if err := validateContentModerationAuditTextLimit(input.AuditUserTextMaxChars, "user text"); err != nil {
		return nil, err
	}
	if err := validateContentModerationAuditTextLimit(input.AuditToolOutputMaxChars, "tool output"); err != nil {
		return nil, err
	}
	if input.Enabled != nil {
		cfg.Enabled = *input.Enabled
	}
	if input.Mode != nil {
		cfg.Mode = strings.TrimSpace(*input.Mode)
	}
	if input.BaseURL != nil {
		cfg.BaseURL = strings.TrimSpace(*input.BaseURL)
	}
	if input.Model != nil {
		cfg.Model = strings.TrimSpace(*input.Model)
	}
	if input.ProxyID != nil {
		if *input.ProxyID > 0 {
			proxyID := *input.ProxyID
			cfg.ProxyID = &proxyID
		} else {
			cfg.ProxyID = nil
		}
	}
	if input.TimeoutMS != nil {
		cfg.TimeoutMS = *input.TimeoutMS
	}
	if input.SampleRate != nil {
		cfg.SampleRate = *input.SampleRate
	}
	if input.WorkerCount != nil {
		cfg.WorkerCount = *input.WorkerCount
	}
	if input.QueueSize != nil {
		cfg.QueueSize = *input.QueueSize
	}
	if input.BlockStatus != nil {
		cfg.BlockStatus = *input.BlockStatus
	}
	if input.BlockMessage != nil {
		cfg.BlockMessage = strings.TrimSpace(*input.BlockMessage)
	}
	if input.EmailOnHit != nil {
		cfg.EmailOnHit = *input.EmailOnHit
	}
	if input.AutoBanEnabled != nil {
		cfg.AutoBanEnabled = *input.AutoBanEnabled
	}
	if input.BanThreshold != nil {
		cfg.BanThreshold = *input.BanThreshold
	}
	if input.ViolationWindowHours != nil {
		cfg.ViolationWindowHours = *input.ViolationWindowHours
	}
	if input.RetryCount != nil {
		cfg.RetryCount = *input.RetryCount
	}
	if input.HitRetentionDays != nil {
		cfg.HitRetentionDays = *input.HitRetentionDays
	}
	if input.NonHitRetentionDays != nil {
		cfg.NonHitRetentionDays = *input.NonHitRetentionDays
	}
	if input.PreHashCheckEnabled != nil {
		cfg.PreHashCheckEnabled = *input.PreHashCheckEnabled
	}
	if input.CyberWarningEnabled != nil {
		cfg.CyberWarningEnabled = *input.CyberWarningEnabled
	}
	if input.CyberAutoBanEnabled != nil {
		cfg.CyberAutoBanEnabled = *input.CyberAutoBanEnabled
	}
	if input.CyberBanThreshold != nil {
		cfg.CyberBanThreshold = *input.CyberBanThreshold
	}
	if input.CyberWindowHours != nil {
		cfg.CyberWindowHours = *input.CyberWindowHours
	}
	if input.BlockedKeywords != nil {
		cfg.BlockedKeywords = normalizeBlockedKeywords(*input.BlockedKeywords)
	}
	if input.KeywordBlockingMode != nil {
		cfg.KeywordBlockingMode = strings.TrimSpace(*input.KeywordBlockingMode)
	}
	if input.ModelFilter != nil {
		cfg.ModelFilter = *input.ModelFilter
	}
	if input.AuditUserTextMaxChars != nil {
		cfg.AuditUserTextMaxChars = *input.AuditUserTextMaxChars
	}
	if input.AuditImages != nil {
		cfg.AuditImages = *input.AuditImages
	}
	if input.AuditToolOutputs != nil {
		cfg.AuditToolOutputs = *input.AuditToolOutputs
	}
	if input.AuditToolOutputMaxChars != nil {
		cfg.AuditToolOutputMaxChars = *input.AuditToolOutputMaxChars
	}
	if input.AllGroups != nil {
		cfg.AllGroups = *input.AllGroups
	}
	if input.GroupIDs != nil {
		cfg.GroupIDs = normalizeInt64IDs(*input.GroupIDs)
	}
	if input.RecordNonHits != nil {
		cfg.RecordNonHits = *input.RecordNonHits
	}
	if input.Thresholds != nil {
		cfg.Thresholds = mergeContentModerationThresholds(ContentModerationDefaultThresholds(), *input.Thresholds)
	}
	// Clear or replacement removes prior metadata with the old pool. Append
	// retains existing order; later metadata alignment discards attributes for
	// removed keys so they cannot attach by an obsolete list position.
	if input.ClearAPIKey {
		cfg.APIKey = ""
		cfg.APIKeys = []string{}
		cfg.APIKeyMetadata = []ContentModerationAPIKeyMetadata{}
	} else {
		apiKeysMode := normalizeContentModerationAPIKeysMode(input.APIKeysMode)
		if input.DeleteAPIKeyHashes != nil && apiKeysMode != contentModerationAPIKeysModeReplace {
			cfg.APIKeys = deleteModerationAPIKeysByHash(cfg.apiKeys(), *input.DeleteAPIKeyHashes)
			cfg.APIKey = ""
		}
		entries := normalizeContentModerationAPIKeyEntryInputs(input.APIKeyEntries)
		newKeys := make([]string, 0, len(entries))
		if input.APIKeys != nil {
			newKeys = append(newKeys, *input.APIKeys...)
		}
		for _, entry := range entries {
			newKeys = append(newKeys, entry.APIKey)
		}
		if input.APIKeys != nil || input.APIKeyEntries != nil {
			if apiKeysMode == contentModerationAPIKeysModeReplace {
				cfg.APIKeys = normalizeModerationAPIKeys(newKeys)
				cfg.APIKeyMetadata = []ContentModerationAPIKeyMetadata{}
			} else {
				cfg.APIKeys = normalizeModerationAPIKeys(append(cfg.apiKeys(), newKeys...))
			}
			cfg.APIKey = ""
		}
		if input.APIKey != nil && strings.TrimSpace(*input.APIKey) != "" {
			cfg.APIKeys = normalizeModerationAPIKeys(append(cfg.APIKeys, *input.APIKey))
			cfg.APIKey = ""
		}
		if input.APIKeyUpdates != nil {
			if err := applyContentModerationAPIKeyMetadataUpdates(cfg, *input.APIKeyUpdates); err != nil {
				return nil, err
			}
		}
		// Supplied entry attributes override saved key metadata so an
		// existing key can receive a new
		// weight or note.
		applyContentModerationAPIKeyEntryMetadata(cfg, entries)
	}
	if err := s.validateConfig(ctx, cfg); err != nil {
		return nil, err
	}
	cfg.normalize()
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal content moderation config: %w", err)
	}
	// Save policy before publishing its copied runtime entry; a storage failure
	// leaves the request snapshot available with its previously loaded configuration.
	if err := s.settingRepo.Set(ctx, SettingKeyContentModerationConfig, string(raw)); err != nil {
		return nil, fmt.Errorf("save content moderation config: %w", err)
	}
	s.replaceRuntimeConfig(cfg, raw)
	// Clear the resolved proxy entry after saving policy so later audit misses
	// reload the selected proxy; an already running lookup can still publish
	// after this clear.
	s.moderationProxyCache.Store(nil)
	return s.configView(cfg), nil
}

// loadConfig reads dedicated moderation JSON using the supplied context. A
// recognized missing setting selects defaults, while other read errors
// propagate; parsing returns a newly normalized policy that callers may modify
// before persistence.
func (s *ContentModerationService) loadConfig(ctx context.Context) (*ContentModerationConfig, error) {
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyContentModerationConfig)
	if err != nil {
		// A missing stored policy selects defaults; other read errors remain visible
		// to dedicated management callers instead of producing a newly
		// defaulted configuration.
		if errors.Is(err, ErrSettingNotFound) {
			return parseContentModerationConfig("")
		}
		return nil, fmt.Errorf("get content moderation config: %w", err)
	}
	return parseContentModerationConfig(raw)
}

// parseContentModerationConfig overlays stored JSON on the default policy and
// normalizes the result. Blank settings return defaults; malformed JSON returns
// a configuration error. Legacy single credentials and omitted fields are
// interpreted by the same normalization path.
func parseContentModerationConfig(raw string) (*ContentModerationConfig, error) {
	cfg := defaultContentModerationConfig()
	if strings.TrimSpace(raw) == "" {
		cfg.normalize()
		return cfg, nil
	}
	if err := json.Unmarshal([]byte(raw), cfg); err != nil {
		return nil, infraerrors.BadRequest("INVALID_CONTENT_MODERATION_CONFIG", "Content moderation configuration is not valid JSON")
	}
	cfg.normalize()
	return cfg, nil
}

// loadRuntimeSnapshot returns a fresh or stale published policy for request
// processing. A present expired snapshot is returned immediately while
// background refresh is attempted; read failures retain its usable stale data.
// First load instead holds runtimeRefreshMu and reports a synchronous storage
// or parse failure.
//
// The lock makes initial callers share publication and coordinates replacement
// with reload. Returned config and matcher storage is immutable after
// publication, and callers keep it unchanged. A background launcher that never
// executes an admitted callback leaves its refresh lock occupied, so app shuts
// producers down before task admission seals.
func (s *ContentModerationService) loadRuntimeSnapshot(ctx context.Context) (*contentModerationRuntimeSnapshot, error) {
	if s == nil || s.settingRepo == nil {
		return nil, errors.New("content moderation setting repository unavailable")
	}
	now := time.Now()
	if snapshot := s.runtimeSnapshot.Load(); snapshot != nil {
		if now.Sub(snapshot.loadedAt) < s.runtimeSnapshotTTL() {
			return snapshot, nil
		}
		s.triggerRuntimeSnapshotRefresh()
		return snapshot, nil
	}

	// Serialize first-load publication and recheck after locking so concurrent
	// initial callers share the entry that the earliest completed load has published.
	s.runtimeRefreshMu.Lock()
	defer s.runtimeRefreshMu.Unlock()
	if snapshot := s.runtimeSnapshot.Load(); snapshot != nil {
		return snapshot, nil
	}
	return s.refreshRuntimeSnapshot(ctx)
}

// runtimeSnapshotTTL returns the service override when positive and otherwise
// the default one-second snapshot lifetime. Nil services use the default,
// allowing refresh and error backoff code to share the same policy.
func (s *ContentModerationService) runtimeSnapshotTTL() time.Duration {
	if s != nil && s.runtimeCacheTTL > 0 {
		return s.runtimeCacheTTL
	}
	return contentModerationRuntimeCacheTTL
}

// triggerRuntimeSnapshotRefresh admits at most one background reload while
// retaining the previous published snapshot. A deferred retry time, missing
// background launcher, or occupied refresh lock skips admission.
//
// Accepted callbacks hold runtimeRefreshMu across refreshRuntimeSnapshot and
// extend runtimeRefreshRetryAt on storage errors so expired-snapshot readers do
// not stampede the repository. When Runtime.Background rejects the task after
// shutdown seals task admission, runtimeRefreshMu is unlocked immediately so
// concurrent UpdateConfig writers cannot deadlock on a leaked lock.
func (s *ContentModerationService) triggerRuntimeSnapshotRefresh() {
	if s == nil || s.runtime.Background == nil || s.runtimeRefreshDeferred() || !s.runtimeRefreshMu.TryLock() {
		return
	}
	if s.runtimeRefreshDeferred() {
		s.runtimeRefreshMu.Unlock()
		return
	}
	// Release runtimeRefreshMu immediately if the background task tracker has
	// already sealed and rejects launching the callback.
	started := s.runtime.Background("service/content_moderation.go:triggerRuntimeSnapshotRefresh", func() {
		defer s.runtimeRefreshMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), contentModerationRuntimeRefreshTimeout)
		defer cancel()
		if _, err := s.refreshRuntimeSnapshot(ctx); err != nil {
			// Keep the stale snapshot available and defer admission so a failing settings
			// source is not called again by every expired-snapshot request in this interval.
			s.runtimeRefreshRetryAt.Store(time.Now().Add(s.runtimeSnapshotTTL()).UnixNano())
			slog.Warn("content_moderation.runtime_snapshot_refresh_failed", "error", err)
		}
	})
	if !started {
		s.runtimeRefreshMu.Unlock()
	}
}

// runtimeRefreshDeferred reports whether the stored retry instant is still in
// the future. Nil services return false; equality permits another refresh, and
// callers recheck around refresh-lock acquisition.
func (s *ContentModerationService) runtimeRefreshDeferred() bool {
	if s == nil {
		return false
	}
	return time.Now().UnixNano() < s.runtimeRefreshRetryAt.Load()
}

// refreshRuntimeSnapshot reads risk enablement and moderation JSON in one
// repository batch before publishing an immutable entry. Its callers hold
// runtimeRefreshMu. A matching raw JSON digest reuses config and matcher
// storage while refreshing risk enablement and load time; changed JSON is
// parsed before a new matcher is built.
//
// Read or parse failure returns before publication, keeping an existing
// snapshot available. Successful publication clears retry deferral after
// installing the complete entry. The digest compares serialized bytes, so
// semantically identical JSON with different spelling can rebuild the matcher
// even when its rule meanings match.
func (s *ContentModerationService) refreshRuntimeSnapshot(ctx context.Context) (*contentModerationRuntimeSnapshot, error) {
	values, err := s.settingRepo.GetMultiple(ctx, []string{
		SettingKeyRiskControlEnabled,
		SettingKeyContentModerationConfig,
	})
	if err != nil {
		return nil, fmt.Errorf("get content moderation runtime settings: %w", err)
	}
	rawConfig := values[SettingKeyContentModerationConfig]
	configDigest := sha256.Sum256([]byte(rawConfig))
	// Reuse immutable config and matcher storage when raw JSON matches, while
	// publishing refreshed risk enablement and load time as a separate complete entry.
	if current := s.runtimeSnapshot.Load(); current != nil && current.configDigest == configDigest {
		snapshot := &contentModerationRuntimeSnapshot{
			riskControlEnabled: values[SettingKeyRiskControlEnabled] == "true",
			config:             current.config,
			keywordMatcher:     current.keywordMatcher,
			configDigest:       configDigest,
			loadedAt:           time.Now(),
		}
		s.runtimeSnapshot.Store(snapshot)
		s.runtimeRefreshRetryAt.Store(0)
		return snapshot, nil
	}
	cfg, err := parseContentModerationConfig(rawConfig)
	if err != nil {
		return nil, err
	}
	snapshot := &contentModerationRuntimeSnapshot{
		riskControlEnabled: values[SettingKeyRiskControlEnabled] == "true",
		config:             cfg,
		keywordMatcher:     newContentModerationKeywordMatcher(cfg.BlockedKeywords),
		configDigest:       configDigest,
		loadedAt:           time.Now(),
	}
	// Publish only after parsing and matcher construction complete; failed loads
	// leave any earlier immutable entry available to request checks during
	// retry deferral.
	s.runtimeSnapshot.Store(snapshot)
	s.runtimeRefreshRetryAt.Store(0)
	return snapshot, nil
}

// replaceRuntimeConfig updates an existing request snapshot after a successful
// configuration write. Nil input and an absent snapshot are ignored, leaving
// first-load publication lazy. Config and matcher construction occur outside
// runtimeRefreshMu, then publication reacquires the lock and retains the
// currently published risk enablement.
//
// An initial load or already admitted refresh completes before replacement can
// publish. The method does not serialize separate administrator settings writes
// or order concurrent replacements by their database commit, so callers
// coordinate conflicting updates. Copied mutable policy collections are stable
// after the new snapshot is stored.
func (s *ContentModerationService) replaceRuntimeConfig(cfg *ContentModerationConfig, raw []byte) {
	if s == nil || cfg == nil {
		return
	}
	// Wait for any initial load before deciding whether to keep publication lazy;
	// checking outside this lock could miss a snapshot that is about to appear.
	s.runtimeRefreshMu.Lock()
	hasSnapshot := s.runtimeSnapshot.Load() != nil
	s.runtimeRefreshMu.Unlock()
	if !hasSnapshot {
		return
	}
	config := cloneContentModerationConfig(cfg)
	keywordMatcher := newContentModerationKeywordMatcher(cfg.BlockedKeywords)
	configDigest := sha256.Sum256(raw)

	s.runtimeRefreshMu.Lock()
	defer s.runtimeRefreshMu.Unlock()
	current := s.runtimeSnapshot.Load()
	if current == nil {
		return
	}
	// Replace saved policy under the refresh lock, retaining the latest published
	// master switch so a dedicated config update does not overwrite that
	// separate observation.
	s.runtimeSnapshot.Store(&contentModerationRuntimeSnapshot{
		riskControlEnabled: current.riskControlEnabled,
		config:             config,
		keywordMatcher:     keywordMatcher,
		configDigest:       configDigest,
		loadedAt:           time.Now(),
	})
}

// matchBlockedKeyword returns the configured-order match from the snapshot's
// immutable matcher. Missing snapshot or config returns no hit; a missing
// matcher uses the sequential configured-keyword helper so valid matching
// remains available.
func (s *contentModerationRuntimeSnapshot) matchBlockedKeyword(text string) (string, bool) {
	if s == nil || s.config == nil {
		return "", false
	}
	if s.keywordMatcher != nil {
		return s.keywordMatcher.Match(text)
	}
	// Keep matching available from the saved keyword list when no compiled index
	// is attached, using the sequential helper's configured-order result.
	return matchBlockedKeyword(text, s.config.BlockedKeywords)
}

// isRiskControlEnabled returns whether the stored master-switch string is
// exactly true. Any read error disables this direct check; Cyber and
// administrator status callers use it separately from the request snapshot
// cache.
func (s *ContentModerationService) isRiskControlEnabled(ctx context.Context) bool {
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyRiskControlEnabled)
	// A failed direct master-switch read disables this Cyber/status check, while
	// ordinary request checks can still be using their separately published
	// stale snapshot.
	if err != nil {
		return false
	}
	return raw == "true"
}

// validateConfig normalizes policy and checks mode, base URL syntax, proxy
// availability, response status, and selected scope references. Nil configs
// fail. Proxy lookup errors fail validation, while group existence is checked
// only when selected groups and a group repository are available.
func (s *ContentModerationService) validateConfig(ctx context.Context, cfg *ContentModerationConfig) error {
	if cfg == nil {
		return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_CONFIG", "Content moderation configuration is required")
	}
	cfg.normalize()
	switch cfg.Mode {
	case ContentModerationModeOff, ContentModerationModeObserve, ContentModerationModePreBlock:
	default:
		return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_MODE", "Invalid content moderation mode")
	}
	if _, err := url.ParseRequestURI(cfg.BaseURL); err != nil {
		return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_BASE_URL", "Invalid OpenAI base URL")
	}
	if cfg.ProxyID != nil {
		if s.proxyRepo == nil {
			return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_PROXY", "Proxy repository is unavailable")
		}
		if _, err := s.proxyRepo.Lookup(ctx, *cfg.ProxyID, time.Now()); err != nil {
			return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_PROXY", fmt.Sprintf("Proxy server not found: %d", *cfg.ProxyID))
		}
	}
	// Require an error response class for locally rejected requests.
	if cfg.BlockStatus < 400 || cfg.BlockStatus > 599 {
		return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_BLOCK_STATUS", "Block HTTP status code must be between 400 and 599")
	}
	if cfg.ModelFilter.Type != ContentModerationModelFilterAll && len(cfg.ModelFilter.Models) == 0 {
		return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_MODEL_FILTER", "At least one model is required when including or excluding models")
	}
	if !cfg.AllGroups && len(cfg.GroupIDs) > 0 && s.groupRepo != nil {
		for _, groupID := range cfg.GroupIDs {
			if err := s.groupRepo.CheckGroup(ctx, groupID); err != nil {
				return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_GROUP", fmt.Sprintf("Moderation group not found: %d", groupID))
			}
		}
	}
	return nil
}

// defaultContentModerationConfig returns a fresh disabled ordinary policy with
// pre-block mode and full audit scope. Ordinary hit notices and auto-ban are
// enabled within that disabled policy, while Cyber warnings default enabled and
// Cyber auto-ban defaults disabled.
func defaultContentModerationConfig() *ContentModerationConfig {
	return &ContentModerationConfig{
		Enabled:                 false,
		Mode:                    ContentModerationModePreBlock,
		BaseURL:                 defaultContentModerationBaseURL,
		Model:                   defaultContentModerationModel,
		TimeoutMS:               defaultContentModerationTimeoutMS,
		SampleRate:              100,
		AllGroups:               true,
		GroupIDs:                []int64{},
		RecordNonHits:           false,
		Thresholds:              ContentModerationDefaultThresholds(),
		WorkerCount:             defaultContentModerationWorkerCount,
		QueueSize:               defaultContentModerationQueueSize,
		BlockStatus:             defaultContentModerationBlockHTTPStatus,
		BlockMessage:            defaultContentModerationBlockMessage,
		EmailOnHit:              true,
		AutoBanEnabled:          true,
		BanThreshold:            defaultContentModerationBanThreshold,
		ViolationWindowHours:    defaultContentModerationViolationWindowHours,
		RetryCount:              defaultContentModerationRetryCount,
		HitRetentionDays:        defaultContentModerationHitRetentionDays,
		NonHitRetentionDays:     defaultContentModerationNonHitRetentionDays,
		PreHashCheckEnabled:     false,
		CyberWarningEnabled:     true,
		CyberAutoBanEnabled:     false,
		CyberBanThreshold:       defaultContentModerationCyberBanThreshold,
		CyberWindowHours:        defaultContentModerationCyberWindowHours,
		BlockedKeywords:         []string{},
		KeywordBlockingMode:     ContentModerationKeywordModeKeywordAndAPI,
		AuditUserTextMaxChars:   defaultContentModerationAuditTextChars,
		AuditImages:             true,
		AuditToolOutputs:        true,
		AuditToolOutputMaxChars: defaultContentModerationAuditTextChars,
		ModelFilter: ContentModerationModelFilter{
			Type:   ContentModerationModelFilterAll,
			Models: []string{},
		},
	}
}

// cloneContentModerationConfig copies mutable policy collections and the
// selected proxy integer into independent storage. A nil config returns nil;
// strings and scalar fields copy by value, allowing queued record policy and
// published snapshots to remain stable.
func cloneContentModerationConfig(cfg *ContentModerationConfig) *ContentModerationConfig {
	if cfg == nil {
		return nil
	}
	clone := *cfg
	clone.ProxyID = cloneInt64Ptr(cfg.ProxyID)
	clone.APIKeys = append([]string(nil), cfg.APIKeys...)
	clone.APIKeyMetadata = append([]ContentModerationAPIKeyMetadata(nil), cfg.APIKeyMetadata...)
	clone.GroupIDs = append([]int64(nil), cfg.GroupIDs...)
	clone.BlockedKeywords = append([]string(nil), cfg.BlockedKeywords...)
	clone.Thresholds = cloneFloatMap(cfg.Thresholds)
	clone.ModelFilter = ContentModerationModelFilter{
		Type:   cfg.ModelFilter.Type,
		Models: append([]string(nil), cfg.ModelFilter.Models...),
	}
	return &clone
}

// normalize mutates policy into the stored and runtime representation used by
// moderation readers. The caller supplies a nonnil editable config. Legacy
// APIKey joins the deduplicated key list and is cleared; metadata is rebuilt in
// key order so removed keys lose their metadata and newly retained keys receive
// defaults.
//
// Timeout, worker, queue, retention, text-limit, sampling, and retry values
// receive defaults or supported clamps. Group IDs, ordered keywords, model
// lists, and recognized thresholds are normalized separately. Mode is defaulted
// when empty but invalid nonempty modes are left for validateConfig, and
// whitespace-only base/model values can normalize to empty after their initial
// default checks.
func (cfg *ContentModerationConfig) normalize() {
	if cfg.APIKey != "" {
		cfg.APIKeys = normalizeModerationAPIKeys(append(cfg.APIKeys, cfg.APIKey))
		cfg.APIKey = ""
	} else {
		cfg.APIKeys = normalizeModerationAPIKeys(cfg.APIKeys)
	}
	cfg.APIKeyMetadata = normalizeContentModerationAPIKeyMetadata(cfg.APIKeys, cfg.APIKeyMetadata)
	if cfg.Mode == "" {
		cfg.Mode = ContentModerationModePreBlock
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultContentModerationBaseURL
	}
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.Model == "" {
		cfg.Model = defaultContentModerationModel
	}
	cfg.Model = strings.TrimSpace(cfg.Model)
	if cfg.ProxyID != nil && *cfg.ProxyID <= 0 {
		cfg.ProxyID = nil
	}
	if cfg.TimeoutMS <= 0 {
		cfg.TimeoutMS = defaultContentModerationTimeoutMS
	}
	// Bound the HTTP child budget within the supported audit-call limit.
	if cfg.TimeoutMS > maxContentModerationTimeoutMS {
		cfg.TimeoutMS = maxContentModerationTimeoutMS
	}
	// Keep negative rates in the disabled sampling range.
	if cfg.SampleRate < 0 {
		cfg.SampleRate = 0
	}
	// Treat excessive rates as full deterministic audit selection.
	if cfg.SampleRate > 100 {
		cfg.SampleRate = 100
	}
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = defaultContentModerationWorkerCount
	}
	// Configured admission cannot exceed the constructed maximum worker
	// population.
	if cfg.WorkerCount > maxContentModerationWorkerCount {
		cfg.WorkerCount = maxContentModerationWorkerCount
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultContentModerationQueueSize
	}
	// Configured admission cannot exceed the allocated maximum channel capacity.
	if cfg.QueueSize > maxContentModerationQueueSize {
		cfg.QueueSize = maxContentModerationQueueSize
	}
	// The recognized stored Chinese default selects the maintained response
	// message; other nonblank custom messages retain their wording after
	// surrounding whitespace is trimmed.
	if strings.TrimSpace(cfg.BlockMessage) == "" || cfg.BlockMessage == "内容审计命中风险规则，请调整输入后重试" {
		cfg.BlockMessage = defaultContentModerationBlockMessage
	}
	cfg.BlockMessage = strings.TrimSpace(cfg.BlockMessage)
	if cfg.BlockStatus <= 0 {
		cfg.BlockStatus = defaultContentModerationBlockHTTPStatus
	}
	if cfg.BanThreshold <= 0 {
		cfg.BanThreshold = defaultContentModerationBanThreshold
	}
	if cfg.ViolationWindowHours <= 0 {
		cfg.ViolationWindowHours = defaultContentModerationViolationWindowHours
	}
	if cfg.RetryCount < 0 {
		cfg.RetryCount = 0
	}
	// Bound repeated provider attempts before selecting another key again.
	if cfg.RetryCount > maxContentModerationRetryCount {
		cfg.RetryCount = maxContentModerationRetryCount
	}
	if cfg.HitRetentionDays <= 0 {
		cfg.HitRetentionDays = defaultContentModerationHitRetentionDays
	}
	// Limit retained hit and Cyber history to supported policy days.
	if cfg.HitRetentionDays > maxContentModerationRetentionDays {
		cfg.HitRetentionDays = maxContentModerationRetentionDays
	}
	if cfg.NonHitRetentionDays <= 0 {
		cfg.NonHitRetentionDays = defaultContentModerationNonHitRetentionDays
	}
	// Keep successful non-hit history within its shorter retention cap.
	if cfg.NonHitRetentionDays > maxContentModerationNonHitRetentionDays {
		cfg.NonHitRetentionDays = maxContentModerationNonHitRetentionDays
	}
	if cfg.CyberBanThreshold <= 0 {
		cfg.CyberBanThreshold = defaultContentModerationCyberBanThreshold
	}
	if cfg.CyberWindowHours <= 0 {
		cfg.CyberWindowHours = defaultContentModerationCyberWindowHours
	}
	if cfg.AuditUserTextMaxChars <= 0 {
		cfg.AuditUserTextMaxChars = defaultContentModerationAuditTextChars
	}
	// Bound the user-source audit budget before chunk construction.
	if cfg.AuditUserTextMaxChars > maxContentModerationAuditTextChars {
		cfg.AuditUserTextMaxChars = maxContentModerationAuditTextChars
	}
	if cfg.AuditToolOutputMaxChars <= 0 {
		cfg.AuditToolOutputMaxChars = defaultContentModerationAuditTextChars
	}
	// Bound the returned-tool audit budget independently from user text.
	if cfg.AuditToolOutputMaxChars > maxContentModerationAuditTextChars {
		cfg.AuditToolOutputMaxChars = maxContentModerationAuditTextChars
	}
	cfg.GroupIDs = normalizeInt64IDs(cfg.GroupIDs)
	cfg.Thresholds = mergeContentModerationThresholds(ContentModerationDefaultThresholds(), cfg.Thresholds)
	cfg.BlockedKeywords = normalizeBlockedKeywords(cfg.BlockedKeywords)
	cfg.KeywordBlockingMode = normalizeKeywordBlockingMode(cfg.KeywordBlockingMode)
	cfg.ModelFilter = normalizeContentModerationModelFilter(cfg.ModelFilter)
}

// includesGroup reports whether all-group scope or a listed group admits the
// supplied identifier. The config is required; a nil group is rejected for
// selected-group scope and admitted when AllGroups is true.
func (cfg *ContentModerationConfig) includesGroup(groupID *int64) bool {
	if cfg.AllGroups {
		return true
	}
	if groupID == nil {
		return false
	}
	return slices.Contains(cfg.GroupIDs, *groupID)
}

// includesModel reports requested-model membership under normalized
// all/include/exclude filtering. Nil configs admit all models; blank model
// names fail membership, which rejects an include match and passes an exclude
// filter.
func (cfg *ContentModerationConfig) includesModel(model string) bool {
	if cfg == nil {
		return true
	}
	filter := normalizeContentModerationModelFilter(cfg.ModelFilter)
	switch filter.Type {
	case ContentModerationModelFilterInclude:
		return contentModerationModelListContains(filter.Models, model)
	case ContentModerationModelFilterExclude:
		return !contentModerationModelListContains(filter.Models, model)
	default:
		return true
	}
}

// shouldSample selects deterministic audit admission from configured rate and
// the first two decoded hash bytes. Supply a nonnil config. Rates at least one
// hundred admit all, and nonpositive rates admit none before hash decoding;
// invalid or short hashes are admitted at intermediate rates.
//
// Valid hashes choose one modulo-one-hundred bucket and compare it with
// SampleRate. Repeated full-input hashes therefore keep the same decision
// across callers rather than consuming a quota or drawing a fresh random
// choice. Keyword and recorded-hash blocks are decided before this sampled API
// stage in Check.
func (cfg *ContentModerationConfig) shouldSample(hashText string) bool {
	if cfg.SampleRate >= 100 {
		return true
	}
	if cfg.SampleRate <= 0 {
		return false
	}
	raw, err := hex.DecodeString(hashText)
	// An unparseable intermediate-rate identity is admitted so malformed hash input
	// does not silently discard the API audit selected by this otherwise
	// enabled policy.
	if err != nil || len(raw) < 2 {
		return true
	}
	// The two-byte prefix fixes one deterministic percentage bucket for this digest.
	return int(binary.BigEndian.Uint16(raw[:2])%100) < cfg.SampleRate
}

// apiKeys returns normalized current credential-list entries in
// first-occurrence order. Nil configs return nil; this method reads APIKeys
// alone, so legacy APIKey conversion is performed earlier by configuration
// normalization.
func (cfg *ContentModerationConfig) apiKeys() []string {
	if cfg == nil {
		return nil
	}
	return normalizeModerationAPIKeys(cfg.APIKeys)
}

// configView builds the masked administrator policy with copied mutable
// configuration collections. The config is required. Credential health is
// sampled separately for each key, so those status entries need not represent
// one instant across concurrent audit updates.
func (s *ContentModerationService) configView(cfg *ContentModerationConfig) *ContentModerationConfigView {
	keys := cfg.apiKeys()
	masks := make([]string, 0, len(keys))
	for _, key := range keys {
		masks = append(masks, maskSecretTail(key))
	}
	apiKeyMasked := ""
	if len(masks) > 0 {
		apiKeyMasked = masks[0]
	}
	return &ContentModerationConfigView{
		Enabled:                 cfg.Enabled,
		Mode:                    cfg.Mode,
		BaseURL:                 cfg.BaseURL,
		Model:                   cfg.Model,
		ProxyID:                 cloneInt64Ptr(cfg.ProxyID),
		APIKeyConfigured:        len(keys) > 0,
		APIKeyMasked:            apiKeyMasked,
		APIKeyCount:             len(keys),
		APIKeyMasks:             masks,
		APIKeyStatuses:          s.apiKeyStatuses(cfg),
		TimeoutMS:               cfg.TimeoutMS,
		SampleRate:              cfg.SampleRate,
		AllGroups:               cfg.AllGroups,
		GroupIDs:                append([]int64(nil), cfg.GroupIDs...),
		RecordNonHits:           cfg.RecordNonHits,
		Thresholds:              cloneFloatMap(cfg.Thresholds),
		WorkerCount:             cfg.WorkerCount,
		QueueSize:               cfg.QueueSize,
		BlockStatus:             cfg.BlockStatus,
		BlockMessage:            cfg.BlockMessage,
		EmailOnHit:              cfg.EmailOnHit,
		AutoBanEnabled:          cfg.AutoBanEnabled,
		BanThreshold:            cfg.BanThreshold,
		ViolationWindowHours:    cfg.ViolationWindowHours,
		RetryCount:              cfg.RetryCount,
		HitRetentionDays:        cfg.HitRetentionDays,
		NonHitRetentionDays:     cfg.NonHitRetentionDays,
		PreHashCheckEnabled:     cfg.PreHashCheckEnabled,
		CyberWarningEnabled:     cfg.CyberWarningEnabled,
		CyberAutoBanEnabled:     cfg.CyberAutoBanEnabled,
		CyberBanThreshold:       cfg.CyberBanThreshold,
		CyberWindowHours:        cfg.CyberWindowHours,
		BlockedKeywords:         append([]string(nil), cfg.BlockedKeywords...),
		KeywordBlockingMode:     cfg.KeywordBlockingMode,
		ModelFilter:             cloneContentModerationModelFilter(cfg.ModelFilter),
		AuditUserTextMaxChars:   cfg.AuditUserTextMaxChars,
		AuditImages:             cfg.AuditImages,
		AuditToolOutputs:        cfg.AuditToolOutputs,
		AuditToolOutputMaxChars: cfg.AuditToolOutputMaxChars,
	}
}

// mergeContentModerationThresholds copies base entries and overlays recognized
// override categories into a new map. Unknown override keys are skipped, while
// base entries remain. Comparisons clamp values below zero or above one,
// including infinities; NaN bypasses both comparisons and JSON persistence
// rejects it later.
//
// Normalization normally supplies the complete default map before applying
// overrides. A zero override remains meaningful: evaluateModerationScores
// compares missing scores as zero, so that threshold can flag an omitted
// category. Callers supply finite intended policy values and choose a complete
// base when they need defaults for every recognized category.
func mergeContentModerationThresholds(base map[string]float64, override map[string]float64) map[string]float64 {
	out := cloneFloatMap(base)
	if out == nil {
		out = map[string]float64{}
	}
	for _, category := range contentModerationCategoryOrder {
		// Limit recognized override comparisons before publication; a resulting zero
		// threshold can count an omitted category zero as a hit during score evaluation.
		if v, ok := override[category]; ok {
			if v < 0 {
				v = 0
			}
			if v > 1 {
				v = 1
			}
			out[category] = v
		}
	}
	return out
}

// normalizeInt64IDs returns distinct positive identifiers sorted ascending in
// new storage. Empty or wholly rejected input produces an empty slice, letting
// configured group membership ignore invalid and duplicate supplied IDs.
func normalizeInt64IDs(ids []int64) []int64 {
	if len(ids) == 0 {
		return []int64{}
	}
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// normalizeBlockedKeywords trims, rune-caps, and case-insensitively
// deduplicates entries in their first configured order. It retains the first
// spelling and the supported prefix of the list, because match priority follows
// retained configuration position.
//
// Deduplication compares capped text, so entries differing only beyond the rune
// budget collapse into the first prefix. The list limit counts retained unique
// entries, leaving skipped blanks and duplicates outside that count. Matcher
// results use the retained spelling and order.
func normalizeBlockedKeywords(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, raw := range in {
		kw := strings.TrimSpace(raw)
		if kw == "" {
			continue
		}
		// Cap retained spelling before duplicate detection and configured match
		// priority.
		kw = trimRunes(kw, maxContentModerationBlockedKeywordRunes)
		key := strings.ToLower(kw)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, kw)
		// Keep the earliest retained entries for configured keyword match priority.
		if len(out) >= maxContentModerationBlockedKeywords {
			break
		}
	}
	return out
}

// normalizeKeywordBlockingMode recognizes exact trimmed keyword-only, API-only,
// or combined mode strings. Empty, differently cased, and unknown values select
// the combined default; runtime Check reads that normalized choice.
func normalizeKeywordBlockingMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case ContentModerationKeywordModeKeywordOnly:
		return ContentModerationKeywordModeKeywordOnly
	case ContentModerationKeywordModeAPIOnly:
		return ContentModerationKeywordModeAPIOnly
	case ContentModerationKeywordModeKeywordAndAPI:
		return ContentModerationKeywordModeKeywordAndAPI
	default:
		return ContentModerationKeywordModeKeywordAndAPI
	}
}

// normalizeContentModerationModelFilter returns a normalized filter type and
// newly normalized model names. All-model scope clears the names list; include
// or exclude can retain an empty list until configuration validation applies
// its own requirement.
//
// Type normalization precedes the all-model decision, so an unknown stored type
// discards supplied names. Include and exclude preserve the normalized list,
// even when empty. Stored-policy membership interprets those empty lists,
// while administrator validation separately requires at least one retained
// model name.
func normalizeContentModerationModelFilter(filter ContentModerationModelFilter) ContentModerationModelFilter {
	out := ContentModerationModelFilter{
		Type:   normalizeContentModerationModelFilterType(filter.Type),
		Models: normalizeContentModerationModelNames(filter.Models),
	}
	if out.Type == ContentModerationModelFilterAll {
		out.Models = []string{}
	}
	return out
}

// cloneContentModerationModelFilter returns normalized model scope with an
// independently allocated model-name slice. Its all-model path uses an empty
// list; scalar filter type and immutable string values copy by value.
func cloneContentModerationModelFilter(filter ContentModerationModelFilter) ContentModerationModelFilter {
	normalized := normalizeContentModerationModelFilter(filter)
	normalized.Models = append([]string(nil), normalized.Models...)
	return normalized
}

// normalizeContentModerationModelFilterType lowercases and trims the type,
// accepting all, include, and exclude. Empty or unknown values select all, so
// malformed stored filter types broaden this normalization result to all-model
// scope.
func normalizeContentModerationModelFilterType(filterType string) string {
	switch strings.ToLower(strings.TrimSpace(filterType)) {
	case ContentModerationModelFilterInclude:
		return ContentModerationModelFilterInclude
	case ContentModerationModelFilterExclude:
		return ContentModerationModelFilterExclude
	case ContentModerationModelFilterAll:
		return ContentModerationModelFilterAll
	default:
		return ContentModerationModelFilterAll
	}
}

// normalizeContentModerationModelNames trims and rune-caps distinct model
// spellings in first-occurrence order. Case-insensitive duplicates keep the
// first spelling; only the supported list prefix survives and empty input
// yields an empty slice.
//
// Duplicate detection uses capped names, so entries differing only beyond the
// rune budget collapse. Membership compares requested names without this
// truncation, so a longer requested name need not match its retained prefix.
// The list limit counts retained unique names rather than skipped entries.
func normalizeContentModerationModelNames(models []string) []string {
	if len(models) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, raw := range models {
		// Cap retained names before case-insensitive scope membership and duplicate
		// detection.
		model := trimRunes(strings.TrimSpace(raw), maxContentModerationModelFilterRunes)
		if model == "" {
			continue
		}
		key := strings.ToLower(model)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, model)
		// Later model names are excluded beyond the supported scope-list limit.
		if len(out) >= maxContentModerationModelFilterModels {
			break
		}
	}
	return out
}

// contentModerationModelListContains reports exact membership after trimming
// and lowercasing candidate and requested names. Blank requested names return
// false; substring or wildcard patterns do not participate in this comparison.
func contentModerationModelListContains(models []string, model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return false
	}
	for _, candidate := range models {
		if strings.ToLower(strings.TrimSpace(candidate)) == model {
			return true
		}
	}
	return false
}

// matchBlockedKeyword returns the first configured nonempty keyword present in
// lowered text. Empty inputs return no hit; the returned spelling is the
// supplied keyword, and configured ordering takes precedence over earliest text
// position.
func matchBlockedKeyword(text string, keywords []string) (string, bool) {
	if text == "" || len(keywords) == 0 {
		return "", false
	}
	lower := strings.ToLower(text)
	for _, kw := range keywords {
		if kw == "" {
			continue
		}
		if strings.Contains(lower, strings.ToLower(kw)) {
			return kw, true
		}
	}
	return "", false
}

// normalizeModerationAPIKeys returns trimmed nonblank distinct credentials in
// first-occurrence order and new storage. Deduplication is case-sensitive
// because credential spelling is retained for provider authentication, and
// empty input yields an empty slice.
func normalizeModerationAPIKeys(keys []string) []string {
	if len(keys) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, len(keys))
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	return out
}

// normalizeContentModerationAPIKeyPriority returns the default weight for
// nonpositive input and caps excessive weight. Valid positive values remain, so
// scheduling always receives a positive supported priority after this
// normalization.
func normalizeContentModerationAPIKeyPriority(priority int) int {
	if priority <= 0 {
		return defaultContentModerationAPIKeyPriority
	}
	// Keep each key weight within the supported scheduling range.
	if priority > maxContentModerationAPIKeyPriority {
		return maxContentModerationAPIKeyPriority
	}
	return priority
}

// normalizeContentModerationAPIKeyMetadata associates metadata with normalized
// current keys by hash and returns it in credential order. Invalid hashes are
// skipped and later metadata for one valid hash replaces earlier entries;
// missing metadata receives default priority.
//
// Deleted keys contribute no output metadata, while retained keys keep
// normalized notes and priorities. This joins hash-keyed administrator edits
// back to the literal credential list without relying on map traversal order,
// so displayed metadata and scheduling weights follow the same configured key
// identities.
func normalizeContentModerationAPIKeyMetadata(keys []string, items []ContentModerationAPIKeyMetadata) []ContentModerationAPIKeyMetadata {
	keys = normalizeModerationAPIKeys(keys)
	byHash := make(map[string]ContentModerationAPIKeyMetadata, len(items))
	for _, item := range items {
		hash := normalizeContentModerationHash(item.KeyHash)
		if hash == "" {
			continue
		}
		byHash[hash] = ContentModerationAPIKeyMetadata{
			KeyHash:  hash,
			Priority: normalizeContentModerationAPIKeyPriority(item.Priority),
			// Retain only the supported note prefix for hash-addressed administrator
			// display.
			Note: trimRunes(strings.TrimSpace(item.Note), maxContentModerationAPIKeyNoteRunes),
		}
	}
	out := make([]ContentModerationAPIKeyMetadata, 0, len(keys))
	for _, key := range keys {
		hash := moderationAPIKeyHash(key)
		item, ok := byHash[hash]
		if !ok {
			item = ContentModerationAPIKeyMetadata{KeyHash: hash, Priority: defaultContentModerationAPIKeyPriority}
		}
		out = append(out, item)
	}
	return out
}

// normalizeContentModerationAPIKeyEntryInputs prepares trimmed credential
// additions with supported priority and note length. Missing or empty input
// returns nil, and blank credentials are discarded after trimming.
//
// An index map associates each retained literal credential with its first
// output position. A later occurrence replaces the complete entry at that
// position, so its normalized priority and note win while credential ordering
// remains stable. Credentials are case-sensitive and space-normalized; callers
// can then merge this result into the saved key list and apply its selected
// scheduling attributes.
func normalizeContentModerationAPIKeyEntryInputs(input *[]ContentModerationAPIKeyEntryInput) []ContentModerationAPIKeyEntryInput {
	if input == nil || len(*input) == 0 {
		return nil
	}
	out := make([]ContentModerationAPIKeyEntryInput, 0, len(*input))
	indexes := make(map[string]int, len(*input))
	for _, item := range *input {
		item.APIKey = strings.TrimSpace(item.APIKey)
		if item.APIKey == "" {
			continue
		}
		item.Priority = normalizeContentModerationAPIKeyPriority(item.Priority)
		// Retain only the supported note prefix for hash-addressed administrator
		// display.
		item.Note = trimRunes(strings.TrimSpace(item.Note), maxContentModerationAPIKeyNoteRunes)
		// Replace attributes in the first retained position so repeated administrator
		// entries cannot reorder the credential pool while choosing their last
		// supplied metadata.
		if index, ok := indexes[item.APIKey]; ok {
			out[index] = item
			continue
		}
		indexes[item.APIKey] = len(out)
		out = append(out, item)
	}
	return out
}

// applyContentModerationAPIKeyEntryMetadata updates cfg metadata from supplied
// credential entries after existing metadata is normalized. Nil config or no
// entries does nothing; supplied attributes win by hash, and final
// normalization restores current key order while removing deleted keys.
//
// UpdateConfig applies these entries after its update-by-hash phase, so their
// priority and note win for an existing key. Map traversal does not decide
// display order: final normalization follows cfg.APIKeys and retains only
// attributes associated with that current list.
func applyContentModerationAPIKeyEntryMetadata(cfg *ContentModerationConfig, entries []ContentModerationAPIKeyEntryInput) {
	if cfg == nil || len(entries) == 0 {
		return
	}
	metadata := make(map[string]ContentModerationAPIKeyMetadata, len(cfg.APIKeyMetadata)+len(entries))
	for _, item := range normalizeContentModerationAPIKeyMetadata(cfg.APIKeys, cfg.APIKeyMetadata) {
		metadata[item.KeyHash] = item
	}
	for _, entry := range entries {
		hash := moderationAPIKeyHash(entry.APIKey)
		metadata[hash] = ContentModerationAPIKeyMetadata{KeyHash: hash, Priority: entry.Priority, Note: entry.Note}
	}
	items := make([]ContentModerationAPIKeyMetadata, 0, len(metadata))
	for _, item := range metadata {
		items = append(items, item)
	}
	// Rebuild from credential order after map collection, so unordered traversal
	// cannot decide displayed metadata positions or associate attributes by index.
	cfg.APIKeyMetadata = normalizeContentModerationAPIKeyMetadata(cfg.APIKeys, items)
}

// applyContentModerationAPIKeyMetadataUpdates applies normalized hash-addressed
// attributes for currently configured credentials. Nil config or empty updates
// does nothing. An unknown target returns an application error before
// cfg.APIKeyMetadata is assigned, leaving that collection unchanged.
//
// The helper prepares existing normalized metadata in a temporary map and
// applies supplied updates in order, so the last update for one hash wins.
// Final normalization restores credential-list order and retains untouched
// attributes for other keys. Callers use an editable config and validate input
// requirements before this helper; it publishes one replacement collection
// after every target has been accepted.
func applyContentModerationAPIKeyMetadataUpdates(cfg *ContentModerationConfig, updates []ContentModerationAPIKeyMetadata) error {
	if cfg == nil || len(updates) == 0 {
		return nil
	}
	configured := make(map[string]struct{}, len(cfg.APIKeys))
	metadata := make(map[string]ContentModerationAPIKeyMetadata, len(cfg.APIKeys))
	for _, item := range normalizeContentModerationAPIKeyMetadata(cfg.APIKeys, cfg.APIKeyMetadata) {
		configured[item.KeyHash] = struct{}{}
		metadata[item.KeyHash] = item
	}
	// Validate every target against configured identities before publishing the
	// replacement; a later unknown hash leaves existing cfg metadata assigned as
	// it was.
	for _, update := range updates {
		hash := normalizeContentModerationHash(update.KeyHash)
		if _, ok := configured[hash]; !ok {
			return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_API_KEY", "Moderation key to update not found")
		}
		metadata[hash] = ContentModerationAPIKeyMetadata{
			KeyHash:  hash,
			Priority: normalizeContentModerationAPIKeyPriority(update.Priority),
			// Retain only the supported note prefix for hash-addressed administrator
			// display.
			Note: trimRunes(strings.TrimSpace(update.Note), maxContentModerationAPIKeyNoteRunes),
		}
	}
	items := make([]ContentModerationAPIKeyMetadata, 0, len(metadata))
	for _, item := range metadata {
		items = append(items, item)
	}
	// Rebuild from credential order after map collection, so unordered traversal
	// cannot decide displayed metadata positions or associate attributes by index.
	cfg.APIKeyMetadata = normalizeContentModerationAPIKeyMetadata(cfg.APIKeys, items)
	return nil
}

// apiKeyMetadataForHash returns normalized metadata for a valid configured
// identity, or a default-priority entry when absent. The hash is normalized
// first; nil configs use the same default result with an empty note.
func (cfg *ContentModerationConfig) apiKeyMetadataForHash(hash string) ContentModerationAPIKeyMetadata {
	hash = normalizeContentModerationHash(hash)
	if cfg != nil {
		for _, item := range normalizeContentModerationAPIKeyMetadata(cfg.APIKeys, cfg.APIKeyMetadata) {
			if item.KeyHash == hash {
				return item
			}
		}
	}
	return ContentModerationAPIKeyMetadata{KeyHash: hash, Priority: defaultContentModerationAPIKeyPriority}
}

// validateContentModerationAPIKeyInputs checks administrator priority, note
// length, and update hash input. New entries permit zero priority for later
// defaulting, while metadata updates require a positive supported priority and
// a valid complete hash; nil lists are accepted.
func validateContentModerationAPIKeyInputs(entries *[]ContentModerationAPIKeyEntryInput, updates *[]ContentModerationAPIKeyMetadata) error {
	// Trimmed notes use the same rune budget as stored display metadata. New
	// entries permit the default-weight sentinel; hash updates require an
	// explicit positive weight.
	if entries != nil {
		for _, item := range *entries {
			if item.Priority < 0 || item.Priority > maxContentModerationAPIKeyPriority {
				return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_API_KEY_PRIORITY", "Moderation key priority must be between 1 and 1000")
			}
			if utf8.RuneCountInString(strings.TrimSpace(item.Note)) > maxContentModerationAPIKeyNoteRunes {
				return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_API_KEY_NOTE", "Moderation key note must not exceed 200 characters")
			}
		}
	}
	if updates != nil {
		for _, item := range *updates {
			if item.Priority <= 0 || item.Priority > maxContentModerationAPIKeyPriority {
				return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_API_KEY_PRIORITY", "Moderation key priority must be between 1 and 1000")
			}
			if normalizeContentModerationHash(item.KeyHash) == "" {
				return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_API_KEY", "Invalid moderation key identifier")
			}
			if utf8.RuneCountInString(strings.TrimSpace(item.Note)) > maxContentModerationAPIKeyNoteRunes {
				return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_API_KEY_NOTE", "Moderation key note must not exceed 200 characters")
			}
		}
	}
	return nil
}

// validateContentModerationAuditTextLimit accepts absent limits and rejects
// supplied nonpositive or oversized rune budgets. The label appears in the
// user-facing validation message; valid values are subsequently passed to
// policy normalization.
func validateContentModerationAuditTextLimit(limit *int, label string) error {
	if limit == nil {
		return nil
	}
	// Require a positive budget before normalization can substitute its fallback.
	if *limit <= 0 || *limit > maxContentModerationAuditTextChars {
		return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_AUDIT_TEXT_LIMIT", fmt.Sprintf("%s moderation character limit must be between 1 and %d", label, maxContentModerationAuditTextChars))
	}
	return nil
}

// deleteModerationAPIKeysByHash removes normalized credentials whose SHA-256
// identity matches a valid requested hash. Invalid deletion hashes are ignored,
// retained keys keep their order, and an empty deletion set returns the
// normalized list.
func deleteModerationAPIKeysByHash(keys []string, hashes []string) []string {
	keys = normalizeModerationAPIKeys(keys)
	deleteHashes := make(map[string]struct{}, len(hashes))
	for _, hash := range hashes {
		hash = normalizeContentModerationHash(hash)
		if hash != "" {
			deleteHashes[hash] = struct{}{}
		}
	}
	if len(deleteHashes) == 0 {
		return keys
	}
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		if _, ok := deleteHashes[moderationAPIKeyHash(key)]; ok {
			continue
		}
		out = append(out, key)
	}
	return out
}

// normalizeContentModerationAPIKeysMode recognizes trimmed case-insensitive
// replace and otherwise selects append. The caller checks this result before
// hash deletion and list editing, so unknown modes follow append behavior.
func normalizeContentModerationAPIKeysMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case contentModerationAPIKeysModeReplace:
		return contentModerationAPIKeysModeReplace
	default:
		return contentModerationAPIKeysModeAppend
	}
}

// normalizeContentModerationHash returns lowercase trimmed hexadecimal text of
// exactly SHA-256 digest width. Wrong length or invalid hex returns empty,
// letting administrator deletion and key-metadata operations reject malformed
// identities.
func normalizeContentModerationHash(inputHash string) string {
	inputHash = strings.ToLower(strings.TrimSpace(inputHash))
	// Require the full hexadecimal identity used by deletion and metadata lookup.
	if len(inputHash) != sha256.Size*2 {
		return ""
	}
	if _, err := hex.DecodeString(inputHash); err != nil {
		return ""
	}
	return inputHash
}

// cloneFloatMap copies score or threshold entries into a new mutable map. Nil
// input produces an empty map, and scalar float values are copied as supplied
// for independent observation or policy storage.
func cloneFloatMap(in map[string]float64) map[string]float64 {
	if in == nil {
		return map[string]float64{}
	}
	out := make(map[string]float64, len(in))
	maps.Copy(out, in)
	return out
}

// cloneInt64Ptr returns an independent copy of the pointed integer, retaining
// nil when the optional value is absent. Log and policy builders use this copy
// so later writes to the caller's integer cannot change their retained value.
func cloneInt64Ptr(in *int64) *int64 {
	if in == nil {
		return nil
	}
	v := *in
	return &v
}

// maskSecretTail returns a display mask with the final four bytes of a trimmed
// nonblank credential. Blank input stays empty and values of four bytes or
// fewer become four stars. Longer tails are byte slices and can split a
// multibyte character.
func maskSecretTail(secret string) string {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return ""
	}
	// The display suffix counts bytes, so a Unicode credential can split.
	if len(secret) <= 4 {
		return "****"
	}
	return strings.Repeat("*", 8) + secret[len(secret)-4:]
}
