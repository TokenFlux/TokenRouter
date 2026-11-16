package moderation

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// service.go evaluates moderation policy, runs synchronous and queued audits,
// and records risk outcomes.
// Ordinary admission combines process-local risk policy with current-turn
// input, configured-order keyword checks, recorded hashes, sampling, and
// external category scores. Keyword-only pre-block misses return before later
// stages, while a recorded-hash hit can block an observe request. External
// auditing keeps confirmed successful-unit hits effective alongside failures;
// an audit with no successful unit allows and records incomplete evidence.
//
// Observe tasks use current policy when workers execute, while already decided
// record tasks retain their saved policy. Their queue shares sampled length
// admission and an atomic estimated-byte budget, and shutdown closes admission
// before joining workers. Ordinary account actions and notices precede final
// log insertion as separate effects. Cyber warning/media and account policy
// instead commit together before authentication invalidation and notice
// handling. Runtime snapshots publish immutable policy and keyword data,
// retaining stale entries during deferred reload failures. Credential scheduling,
// health updates, and sampled key-load views live in api_key_health.go.

// Moderation modes, action labels, protocol identifiers, and normalized limits
// govern request admission, audit execution, retained observations, and
// administrator policy editing.
const (
	// Ordinary modes decide whether Check skips, queues, or synchronously audits
	// extracted content after the risk master gate.
	ContentModerationModeOff = "off" // ordinary Check mode skipped
	// external audit queued asynchronously.
	ContentModerationModeObserve = "observe"
	// external audit decides synchronously.
	ContentModerationModePreBlock = "pre_block"

	// Credential edit modes choose retained-list appending or supplied-list
	// replacement before metadata is aligned with saved key order.
	// retain then append credentials.
	contentModerationAPIKeysModeAppend = "append"
	// replace with supplied credentials.
	contentModerationAPIKeysModeReplace = "replace"

	// Stored action labels distinguish allowed observations, score blocks, cached
	// hash blocks, keyword blocks, and incomplete audit errors.
	// allowed observation action label.
	ContentModerationActionAllow     = "allow"
	ContentModerationActionBlock     = "block"      // external score block action
	ContentModerationActionHashBlock = "hash_block" // recorded input hash block
	// configured keyword block action.
	ContentModerationActionKeywordBlock = "keyword_block"
	ContentModerationActionError        = "error" // incomplete audit record action

	// The keyword category gives local substring blocks a named score observation
	// separate from external category evaluation.
	contentModerationKeywordCategory = "keyword"

	// Pre-block keyword strategies decide whether keyword matches, external API
	// evaluation, or their ordered combination determines later admission work.
	// settle pre-block with keywords.
	ContentModerationKeywordModeKeywordOnly = "keyword_only"
	// keywords before external score audit.
	ContentModerationKeywordModeKeywordAndAPI = "keyword_and_api"
	// skip pre-block keyword matching.
	ContentModerationKeywordModeAPIOnly = "api_only"

	// Requested-model scope modes normalize into all-model admission or exact
	// case-insensitive include and exclude membership checks.
	ContentModerationModelFilterAll = "all" // admit every requested model
	// admit listed requested models.
	ContentModerationModelFilterInclude = "include"
	ContentModerationModelFilterExclude = "exclude" // skip listed requested models

	// Protocol labels select current-turn extraction shapes for gateway body input
	// before hashing, sampling, and external audit unit construction.
	// Anthropic message body extraction.
	ContentModerationProtocolAnthropicMessages = "anthropic_messages"
	// Responses input item extraction.
	ContentModerationProtocolOpenAIResponses = "openai_responses"
	// Chat message body extraction.
	ContentModerationProtocolOpenAIChat = "openai_chat_completions"
	// Gemini contents part extraction.
	ContentModerationProtocolGemini = "gemini"
	// image prompt and reference extraction.
	ContentModerationProtocolOpenAIImages = "openai_images"

	// Audit connection defaults and text or image unit limits govern request
	// budgets, chunk overlap, batch size, and retained summary text.
	defaultContentModerationBaseURL    = "https://api.openai.com"
	defaultContentModerationModel      = "omni-moderation-latest"
	defaultContentModerationTimeoutMS  = 3000
	maxContentModerationTimeoutMS      = 30000
	maxModerationInputRunes            = 12000
	contentModerationChunkOverlap      = 256
	contentModerationTextBatchSize     = 16
	contentModerationImageConcurrency  = 4
	contentModerationAuditTotalTimeout = 30 * time.Second
	maxModerationExcerptRunes          = 240
	maxCyberWarningPromptExcerptRunes  = 1000

	// Runtime capacity, retry, retention, ban, and administrator input limits
	// supply policy defaults and supported clamps during normalization or
	// validation.
	defaultContentModerationWorkerCount                = 4
	maxContentModerationWorkerCount                    = 32
	defaultContentModerationQueueSize                  = 32768
	maxContentModerationQueueSize                      = 100000
	maxContentModerationBufferedBytes            int64 = 512 * 1024 * 1024
	defaultContentModerationBanThreshold               = 10
	defaultContentModerationViolationWindowHours       = 720
	defaultContentModerationBlockHTTPStatus            = 403
	defaultContentModerationBlockMessage               = "This request was blocked by the site's content policy. Revise your input and try again."
	defaultContentModerationRetryCount                 = 2
	maxContentModerationRetryCount                     = 5
	defaultContentModerationHitRetentionDays           = 180
	defaultContentModerationNonHitRetentionDays        = 3
	defaultContentModerationCyberBanThreshold          = 10
	defaultContentModerationCyberWindowHours           = 720
	maxContentModerationRetentionDays                  = 3650
	maxContentModerationNonHitRetentionDays            = 3
	maxContentModerationTestImages                     = 8
	maxContentModerationTestImageBytes                 = 8 * 1024 * 1024
	maxContentModerationTestImageDataURLBytes          = 12 * 1024 * 1024
	maxContentModerationBlockedKeywords                = 10000
	maxContentModerationBlockedKeywordRunes            = 200
	maxContentModerationModelFilterModels              = 1000
	maxContentModerationModelFilterRunes               = 200
	maxContentModerationAuditTextChars                 = 1000000
	defaultContentModerationAuditTextChars             = maxContentModerationAuditTextChars
	defaultContentModerationAPIKeyPriority             = 100
	maxContentModerationAPIKeyPriority                 = 1000
	maxContentModerationAPIKeyNoteRunes                = 200

	// Retention cleanup waits before its first round, repeats periodically, and
	// gives each round its independent background database budget.
	contentModerationCleanupInterval = 24 * time.Hour
	contentModerationCleanupTimeout  = 30 * time.Minute
	contentModerationCleanupDelay    = 5 * time.Minute

	// Runtime snapshots have a short ordinary lifetime while background reloads
	// receive a separate deadline and failures defer retry admission.
	contentModerationRuntimeCacheTTL       = time.Second
	contentModerationRuntimeRefreshTimeout = 5 * time.Second

	// Source labels distinguish directly supplied user units from returned tool
	// content and an aggregate containing both kinds of input.
	ContentModerationSourceUser  = "user"  // direct user input source
	ContentModerationSourceTool  = "tool"  // returned tool content source
	ContentModerationSourceMixed = "mixed" // aggregate user and tool input

	// Unit labels distinguish retained text and images from request-level failures
	// used when observe queue admission cannot proceed.
	ContentModerationItemTypeText  = "text"  // retained text unit kind
	ContentModerationItemTypeImage = "image" // retained image unit kind
	// whole-request audit failure kind.
	ContentModerationItemTypeRequest = "request"

	// Resolved audit proxy entries expire after this process-local lifetime,
	// causing later misses to read the selected stored proxy again.
	contentModerationProxyURLCacheTTL = time.Minute
)

// contentModerationCategoryOrder defines known threshold comparisons and stable
// known-category maximum-score tie order. Unknown provider categories can
// affect displayed maxima but do not participate in flagging here.
var contentModerationCategoryOrder = []string{
	"harassment",
	"harassment/threatening",
	"hate",
	"hate/threatening",
	"illicit",
	"illicit/violent",
	"self-harm",
	"self-harm/intent",
	"self-harm/instructions",
	"sexual",
	"sexual/minors",
	"violence",
	"violence/graphic",
}

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

// ContentModerationCheckInput carries request content, actor identity, and
// payment ownership into ordinary audit admission.
type ContentModerationCheckInput struct {
	RequestID string // platform request correlation identifier
	// UserID, UserEmail, BillingUserID, and TeamID separate the acting account
	// from payment ownership. Ordinary and Cyber risk actions target UserID;
	// billing and team values provide retained accounting context.
	UserID        int64  // risk actor account identifier
	UserEmail     string // risk actor recipient address
	BillingUserID int64  // paying owner account identifier
	// Queued observe work retains TeamID until buildStructuredLog copies its
	// integer value. Producers keep the supplied pointer stable until that
	// deferred build completes.
	TeamID     *int64 // optional accounting team identifier
	APIKeyID   int64  // triggering gateway key identifier
	APIKeyName string // triggering gateway key display name
	// GroupID remains shared with queued observe tasks and is reread for worker
	// scope checks. Producers keep that identifier stable until accepted deferred
	// work finishes.
	GroupID   *int64 // optional selected group identifier
	GroupName string // selected group display name
	Endpoint  string // observed gateway endpoint path
	Provider  string // gateway provider platform label
	Model     string // requested model scope name
	Protocol  string // input extraction protocol label
	// Body is read synchronously during Check extraction and dropped from queued
	// metadata after units are built. Callers keep the bytes stable while
	// extraction reads them.
	Body []byte // submitted request JSON bytes
	// PreExtracted supplies current-turn units already extracted from the identical
	// Body and Protocol earlier in the same request, skipping duplicate JSON parsing.
	PreExtracted *ContentModerationInput // optional pre-extracted current-turn content
	// NoMediaRetention omits ordinary log excerpts, input items, and image
	// snapshots while retaining decisions, categories, scores, and completeness
	// metadata. Flagged-input hash recording remains a separate cache operation
	// when available.
	NoMediaRetention bool // sensitive review retention switch
}

// ContentModerationInputItem is a positioned text or image unit retained for
// administrator input review.
type ContentModerationInputItem struct {
	Index  int    `json:"index"`  // original review item position
	Source string `json:"source"` // user or tool origin
	Type   string `json:"type"`   // text or image unit kind
	// Text and ImageRef identify the item representation selected by Type.
	// Extractors keep full text and reference spelling for review; external audit
	// limits are applied to a selected input later.
	Text string `json:"text,omitempty"` // full selected text bytes
	// literal selected image reference
	ImageRef string `json:"image_ref,omitempty"`
}

// ContentModerationImage links an audit image reference to its original review
// item position and source.
type ContentModerationImage struct {
	// SourceIndex remains the original item position after audit filtering. Audit
	// image indexes identify the filtered list separately, and
	// contentModerationHitMedia maps them back through this field.
	SourceIndex int    `json:"source_index"` // original review item position
	Source      string `json:"source"`       // user or tool origin
	Reference   string `json:"reference"`    // literal selected image reference
}

// ContentModerationInput keeps aggregate audit values alongside source-labeled
// positioned units used by review and image selection.
type ContentModerationInput struct {
	// Text, Images, Items, and ImageItems represent the same extracted input in
	// aggregate and positioned forms. Normalize builds missing items only when
	// Items is empty; callers supplying items keep aggregate values and image
	// associations consistent.
	Text       string                       // aggregate full text value
	Images     []string                     // ordered unique image references
	Items      []ContentModerationInputItem // positioned review input units
	ImageItems []ContentModerationImage     // image to review-position associations
	// Source is derived from normalized item sources when initially empty. A
	// supplied nonempty aggregate label survives Normalize, so callers keep it
	// consistent with their item representation.
	Source string // aggregate user, tool, or mixed
}

// ContentModerationDecision reports request admission and the local risk
// observations selected by ordinary moderation.
type ContentModerationDecision struct {
	// Allowed, Blocked, and Flagged describe different outcomes. Failed external
	// auditing and observe queue admission can return allow without confirmed
	// safety; a synchronous allowed result can still carry Flagged.
	// request admission is permitted
	Allowed    bool   `json:"allowed"`
	Blocked    bool   `json:"blocked"`     // local rejection is selected
	Flagged    bool   `json:"flagged"`     // local risk-hit observation
	Message    string `json:"message"`     // selected client rejection text
	StatusCode int    `json:"status_code"` // selected rejection HTTP status
	// InputHash is populated by the recorded-hash block path. Ordinary API and
	// keyword blocks return their own observations without populating this hash
	// field.
	// blocking recorded-input hash
	InputHash string `json:"input_hash,omitempty"`
	// displayed maximum score category
	HighestCategory string `json:"highest_category"`
	// displayed maximum category score
	HighestScore float64 `json:"highest_score"`
	// decision category score observations
	CategoryScores map[string]float64 `json:"category_scores"`
	// selected admission action label
	Action string `json:"action"`
}

// ContentModerationFailedUnit associates public failure text with its audit
// unit and original image review position.
type ContentModerationFailedUnit struct {
	Type  string `json:"type"`  // text, image, or request
	Index int    `json:"index"` // selected audit unit position
	// SourceIndex maps image failures to the original review item. Text chunk and
	// request-level failures keep zero because their unit constructors do not
	// assign a separate review position.
	SourceIndex int    `json:"source_index"` // original image review position
	Error       string `json:"error"`        // public failed unit summary
}

// ContentModerationLog retains ordinary decision, audit completeness, review
// content, and separately attributed account identities.
type ContentModerationLog struct {
	// ID can be assigned before media writes and commit. A persistence error can
	// leave this generated identity populated without establishing a saved row.
	ID int64 `json:"id"` // stored audit row identifier
	// platform request correlation identifier
	RequestID string `json:"request_id"`
	// UserID, UserEmail, BillingUserID, and TeamID separate the acting account
	// from payment ownership. Ordinary and Cyber risk actions target UserID;
	// billing and team values provide retained accounting context.
	// risk actor account identifier
	UserID    *int64 `json:"user_id,omitempty"`
	UserEmail string `json:"user_email"` // risk actor recipient address
	// paying owner account identifier
	BillingUserID *int64 `json:"billing_user_id,omitempty"`
	// optional accounting team identifier
	TeamID *int64 `json:"team_id,omitempty"`
	// triggering gateway key identifier
	APIKeyID *int64 `json:"api_key_id,omitempty"`
	// triggering gateway key display name
	APIKeyName string `json:"api_key_name"`
	// optional selected group identifier
	GroupID   *int64 `json:"group_id,omitempty"`
	GroupName string `json:"group_name"` // selected group display name
	Endpoint  string `json:"endpoint"`   // observed gateway endpoint path
	Provider  string `json:"provider"`   // gateway provider platform label
	Model     string `json:"model"`      // requested model scope name
	Mode      string `json:"mode"`       // ordinary audit policy mode
	Action    string `json:"action"`     // stored decision action label
	Flagged   bool   `json:"flagged"`    // local risk-hit observation
	// displayed maximum score category
	HighestCategory string `json:"highest_category"`
	// displayed maximum category score
	HighestScore float64 `json:"highest_score"`
	// configured keyword hit spelling
	MatchedKeyword string `json:"matched_keyword"`
	// stored decision category scores
	CategoryScores map[string]float64 `json:"category_scores"`
	// effective category threshold snapshot
	ThresholdSnapshot map[string]float64 `json:"threshold_snapshot"`
	// InputExcerpt is redacted and capped for summaries, while InputItems retain
	// full review values. NoMediaRetention removes both, marks ContentComplete
	// false, and prevents hit-image snapshot attachment.
	// redacted short aggregate text
	InputExcerpt string `json:"input_excerpt"`
	Source       string `json:"source"` // aggregate user or tool origin
	// full retained review input units
	InputItems []ContentModerationInputItem `json:"input_items,omitempty"`
	// retained review content completeness
	ContentComplete bool `json:"content_complete"`
	// AuditComplete, TextUnitCount, ImageUnitCount, FailedUnitCount, and
	// FailedUnits retain unit observations. A supplied audit result replaces
	// initial full-input counts with selected audit counts and failures. Local
	// keyword/hash records pass no audit result, keep AuditComplete true, and
	// retain those initial counts even though external units were not called.
	// stored unit audit completion
	AuditComplete bool `json:"audit_complete"`
	TextUnitCount int  `json:"text_unit_count"` // recorded text unit count
	// recorded image unit count
	ImageUnitCount int `json:"image_unit_count"`
	// recorded failed audit unit count
	FailedUnitCount int `json:"failed_unit_count"`
	// failed audit unit observations
	FailedUnits []ContentModerationFailedUnit `json:"failed_units,omitempty"`
	// retained hit image snapshots
	Media []ContentModerationMedia `json:"media,omitempty"`
	// UpstreamLatencyMS and QueueDelayMS retain supplied integer pointers during
	// log construction. Callers keep those values immutable until queued or
	// synchronous persistence has finished reading them.
	// milliseconds
	UpstreamLatencyMS *int   `json:"upstream_latency_ms,omitempty"`
	Error             string `json:"error"` // public audit failure summary
	// ordinary counted-hit total
	ViolationCount int `json:"violation_count"`
	// ordinary threshold-ban observation
	AutoBanned bool `json:"auto_banned"`
	// EmailSent records at least one nil ordinary-notice result. Notification can
	// suppress a send and return nil, so this flag alone does not confirm new
	// transport delivery.
	EmailSent bool `json:"email_sent"` // at least one nil notice result
	// current joined identity status
	UserStatus   string `json:"user_status"`
	QueueDelayMS *int   `json:"queue_delay_ms,omitempty"` // milliseconds
	// CreatedAt is assigned by CreateLog before media writes and commit. A returned
	// persistence failure can leave this time populated even when the transaction
	// rolls back.
	// database row creation time
	CreatedAt time.Time `json:"created_at"`
}

// ContentModerationCyberWarning retains upstream Cyber evidence and the
// independently counted account-policy outcome.
type ContentModerationCyberWarning struct {
	// ID is assigned during warning insertion before the Cyber transaction
	// commits. Caller storage can retain it after rollback or an uncertain commit
	// result.
	ID int64 `json:"id"` // stored Cyber warning identifier
	// platform request correlation identifier
	RequestID string `json:"request_id"`
	// UserID, UserEmail, BillingUserID, and TeamID separate the acting account
	// from payment ownership. Ordinary and Cyber risk actions target UserID;
	// billing and team values provide retained accounting context.
	// risk actor account identifier
	UserID    *int64 `json:"user_id,omitempty"`
	UserEmail string `json:"user_email"` // risk actor recipient address
	// paying owner account identifier
	BillingUserID *int64 `json:"billing_user_id,omitempty"`
	// optional accounting team identifier
	TeamID *int64 `json:"team_id,omitempty"`
	// triggering gateway key identifier
	APIKeyID *int64 `json:"api_key_id,omitempty"`
	// triggering gateway key display name
	APIKeyName string `json:"api_key_name"`
	// optional selected group identifier
	GroupID   *int64 `json:"group_id,omitempty"`
	GroupName string `json:"group_name"` // selected group display name
	// upstream provider record identifier
	ProviderID *int64 `json:"provider_id,omitempty"`
	// upstream provider display name
	ProviderName string `json:"provider_name"`
	Endpoint     string `json:"endpoint"` // observed gateway endpoint path
	Model        string `json:"model"`    // requested model scope name
	// observed upstream HTTP status
	UpstreamStatus int `json:"upstream_status"`
	// WarningText and PromptExcerpt are raw length-capped administrator review
	// text. They can contain credentials present in the supplied upstream warning
	// or user content, unlike ordinary redacted summary excerpts.
	// raw capped warning text
	WarningText string `json:"warning_text"`
	// raw capped prompt excerpt
	PromptExcerpt string `json:"prompt_excerpt"`
	Source        string `json:"source"` // aggregate user or tool origin
	// InputItems and Media retain normalized Cyber input and its image references.
	// This warning builder has no NoMediaRetention flag; callers choose the
	// material supplied to Cyber recording.
	// full retained Cyber review units
	InputItems []ContentModerationInputItem `json:"input_items,omitempty"`
	// retained input item presence
	ContentComplete bool `json:"content_complete"`
	// warning evidence completion label
	AuditComplete bool `json:"audit_complete"`
	TextUnitCount int  `json:"text_unit_count"` // retained text chunk count
	// retained image item count
	ImageUnitCount int `json:"image_unit_count"`
	// recorded failed unit count
	FailedUnitCount int `json:"failed_unit_count"`
	// stored failed unit observations
	FailedUnits []ContentModerationFailedUnit `json:"failed_units,omitempty"`
	// retained Cyber input images
	Media []ContentModerationMedia `json:"media,omitempty"`
	// Cyber window warning count
	ViolationCount int `json:"violation_count"`
	// Cyber threshold-ban observation
	AutoBanned bool `json:"auto_banned"`
	// EmailSent records a nil Cyber-notice result in the post-commit path. An
	// existing notification marker can yield that result without a new transport
	// send, and the later warning-marker write can still fail.
	EmailSent  bool   `json:"email_sent"`  // nil Cyber ban notice result
	UserStatus string `json:"user_status"` // current joined identity status
	// CreatedAt can be filled before the Cyber transaction commits. Caller storage
	// retains this time after failed steps or attempted rollback, while a commit
	// error can leave the database outcome uncertain.
	// database row creation time
	CreatedAt time.Time `json:"created_at"`
}

// ContentModerationCyberWarningPolicy supplies the count window and ban
// condition applied by the Cyber storage transaction.
type ContentModerationCyberWarningPolicy struct {
	// AutoBanEnabled, BanThreshold, and WindowHours are consumed inside Cyber
	// persistence. Nonpositive windows keep count at one; disablement requires
	// enabled policy, a positive threshold, and sufficient count for a locked
	// user.
	AutoBanEnabled bool // Cyber account disablement switch
	BanThreshold   int  // warning count trigger threshold
	WindowHours    int  // hours
}

// ContentModerationCyberWarningInput carries upstream error evidence and
// gateway attribution for synchronous Cyber warning recording.
type ContentModerationCyberWarningInput struct {
	RequestID string // platform request correlation identifier
	// UserID, UserEmail, BillingUserID, and TeamID separate the acting account
	// from payment ownership. Ordinary and Cyber risk actions target UserID;
	// billing and team values provide retained accounting context.
	UserID         int64  // risk actor account identifier
	UserEmail      string // risk actor recipient address
	BillingUserID  int64  // paying owner account identifier
	TeamID         *int64 // optional accounting team identifier
	APIKeyID       int64  // triggering gateway key identifier
	APIKeyName     string // triggering gateway key display name
	GroupID        *int64 // optional selected group identifier
	GroupName      string // selected group display name
	ProviderID     int64  // upstream provider record identifier
	ProviderName   string // upstream provider display name
	Endpoint       string // observed gateway endpoint path
	Model          string // requested model scope name
	UpstreamStatus int    // observed upstream HTTP status
	ResponseBody   []byte // upstream Cyber error evidence
	// WarningText, ResponseBody, and PromptExcerpt supply different evidence.
	// Detection combines supplied text and body policy signals; full Content
	// aggregate text takes precedence over PromptExcerpt when the warning is
	// built.
	WarningText   string                 // supplied upstream warning message
	PromptExcerpt string                 // optional raw text recovery value
	Content       ContentModerationInput // full selected current-turn content
}

// ContentModerationLogFilter selects ordinary administrator audit rows by
// action, identity, text, and time.
type ContentModerationLogFilter struct {
	RequestID  string                      // request or external alias filter
	Pagination pagination.PaginationParams // requested result page settings
	Result     string                      // ordinary action result filter
	GroupID    *int64                      // optional exact group filter
	Endpoint   string                      // optional exact endpoint filter
	// Search is wrapped in percent signs for ILIKE and leaves supplied wildcard
	// characters active. HTTP parses dates separately; zero or absent time values
	// add no SQL time predicate.
	Search string     // case-insensitive SQL pattern input
	From   *time.Time // inclusive lower creation time
	To     *time.Time // inclusive upper creation time
}

// ContentModerationCyberWarningFilter selects administrator Cyber rows and
// summary observations using common filters.
type ContentModerationCyberWarningFilter struct {
	RequestID string // request or external alias filter
	// Pagination is consumed by warning lists, while summaries ignore it and cap
	// each grouped list internally. Both query paths use the same identity, text,
	// and inclusive time predicates.
	Pagination pagination.PaginationParams // requested result page settings
	UserID     *int64                      // optional exact actor filter
	ProviderID *int64                      // optional exact provider filter
	Search     string                      // case-insensitive SQL pattern input
	From       *time.Time                  // inclusive lower creation time
	To         *time.Time                  // inclusive upper creation time
}

// ContentModerationCyberSummary contains overall warning counts and capped user
// and provider grouping observations.
type ContentModerationCyberSummary struct {
	// Events, Requests, Users, and Providers come from one totals query. ByUser and
	// ByProvider come from later separate queries, so concurrent warning changes
	// can separate their observations; each grouped list retains at most
	// one hundred rows.
	// total matching warning rows
	Events int64 `json:"events"`
	// distinct nonblank request identifiers
	Requests int64 `json:"requests"`
	Users    int64 `json:"users"` // distinct nonnull actor identifiers
	// distinct nonnull provider identifiers
	Providers int64 `json:"providers"`
	// largest matching actor groups
	ByUser []ContentModerationCyberUserSummary `json:"by_user"`
	// largest matching provider groups
	ByProvider []ContentModerationCyberProviderSummary `json:"by_provider"`
}

// ContentModerationCyberUserSummary is one grouped actor observation with
// recorded addresses, key labels, and latest time text.
type ContentModerationCyberUserSummary struct {
	Count int64 `json:"count"` // matching actor warning count
	// optional grouped actor identifier
	UserID *int64 `json:"user_id,omitempty"`
	// UserEmail displays the maximum stored address for this actor group. An
	// absent or empty maximum uses the repository's unassociated-user label.
	UserEmail string `json:"user_email"` // grouped actor email display
	// APIKeys joins distinct recorded IDs and names. Missing names use the
	// repository's unnamed label, and no nonnull key identity yields a dash.
	APIKeys  string `json:"api_keys"`  // grouped key identity display
	LastSeen string `json:"last_seen"` // Shanghai local latest-time text
}

// ContentModerationCyberProviderSummary is one grouped upstream observation
// with warning count and distinct actor count.
type ContentModerationCyberProviderSummary struct {
	Count int64 `json:"count"` // matching provider warning count
	// optional grouped provider identifier
	ProviderID *int64 `json:"provider_id,omitempty"`
	// ProviderName displays the maximum saved name for this group, using unknown
	// when that maximum is empty or absent.
	ProviderName string `json:"provider_name"` // grouped provider name display
	Users        int64  `json:"users"`         // distinct nonnull actor count
	LastSeen     string `json:"last_seen"`     // Shanghai local latest-time text
}

// ContentModerationCleanupResult reports successful retention-delete counts and
// the operation completion timestamp.
type ContentModerationCleanupResult struct {
	// DeletedHit, DeletedNonHit, and DeletedCyber come from separate delete
	// commands whose RowsAffected errors are suppressed. Earlier deletes remain
	// applied if a later command fails and no result is returned.
	// reported deleted flagged log count
	DeletedHit int64 `json:"deleted_hit"`
	// reported deleted unflagged log count
	DeletedNonHit int64 `json:"deleted_non_hit"`
	// reported deleted warning count
	DeletedCyber int64 `json:"deleted_cyber"`
	// successful operation completion time
	FinishedAt time.Time `json:"finished_at"`
}

// ContentModerationRuntimeStatus combines configured policy with independently
// sampled queue, audit, credential, and cleanup observations.
type ContentModerationRuntimeStatus struct {
	Enabled bool `json:"enabled"` // ordinary audit enablement switch
	// separately read risk center switch
	RiskControlEnabled bool   `json:"risk_control_enabled"`
	Mode               string `json:"mode"` // ordinary audit policy mode
	// configured admitted worker count
	WorkerCount int `json:"worker_count"`
	MaxWorkers  int `json:"max_workers"` // fixed maximum worker population
	// ActiveWorkers and IdleWorkers use current WorkerCount and sampled active
	// tasks. GetStatus clamps activity to configured count, so reducing worker
	// configuration can hide larger work still finishing.
	// sampled clamped active worker count
	ActiveWorkers int `json:"active_workers"`
	// configured count minus activity
	IdleWorkers int `json:"idle_workers"`
	// configured queue admission limit
	QueueSize   int `json:"queue_size"`
	QueueLength int `json:"queue_length"` // sampled buffered task count
	// QueueUsagePercent divides sampled queue length by configured QueueSize.
	// Queue length checks are concurrent samples, and the configured limit can
	// also shrink after tasks were queued, allowing values above one hundred.
	// percent
	QueueUsagePercent float64 `json:"queue_usage_percent"`
	// cumulative accepted queue sends
	Enqueued int64 `json:"enqueued"`
	Dropped  int64 `json:"dropped"` // cumulative capacity rejection count
	// counted record or checkSync completions
	Processed int64 `json:"processed"`
	Errors    int64 `json:"errors"` // asynchronous all-unit failure count
	// sampled active synchronous audits
	PreBlockActive int `json:"pre_block_active"`
	// counted synchronous decision outcomes
	PreBlockChecked int64 `json:"pre_block_checked"`
	// counted synchronous allowed outcomes
	PreBlockAllowed int64 `json:"pre_block_allowed"`
	// counted synchronous blocked outcomes
	PreBlockBlocked int64 `json:"pre_block_blocked"`
	// counted all-unit audit failures
	PreBlockErrors int64 `json:"pre_block_errors"`
	// milliseconds
	PreBlockAvgLatencyMS int64 `json:"pre_block_avg_latency_ms"`
	// sampled synchronous attempt activity
	PreBlockAPIKeyActive int64 `json:"pre_block_api_key_active"`
	// sampled unfrozen configured credentials
	PreBlockAPIKeyAvailableCount int64 `json:"pre_block_api_key_available_count"`
	// sampled completed synchronous attempts
	PreBlockAPIKeyTotalCalls int64 `json:"pre_block_api_key_total_calls"`
	// per-key sampled synchronous counters
	PreBlockAPIKeyLoads []ContentModerationAPIKeyLoad `json:"pre_block_api_key_loads"`
	// per-key sampled credential health
	APIKeyStatuses []ContentModerationAPIKeyStatus `json:"api_key_statuses"`
	// FlaggedHashCount is zero when no hash adapter is bound or its count read
	// fails. A failure is logged, so a displayed zero is not proof that an
	// available Redis set was empty.
	// observed Redis flagged-member count
	FlaggedHashCount int64 `json:"flagged_hash_count"`
	// last successful cleanup timestamp
	LastCleanupAt *time.Time `json:"last_cleanup_at,omitempty"`
	// last reported deleted flagged count
	LastCleanupDeletedHit int64 `json:"last_cleanup_deleted_hit"`
	// last reported deleted unflagged count
	LastCleanupDeletedNonHit int64 `json:"last_cleanup_deleted_non_hit"`
}

// ContentModerationUnbanUserResult identifies the account reported active after
// an administrator unban operation.
type ContentModerationUnbanUserResult struct {
	UserID int64  `json:"user_id"` // reactivated actor account identifier
	Status string `json:"status"`  // reported active identity status
}

// ContentModerationDeleteHashResult pairs a normalized administrator hash with
// the observed cache removal result.
type ContentModerationDeleteHashResult struct {
	InputHash string `json:"input_hash"` // normalized requested digest text
	Deleted   bool   `json:"deleted"`    // observed successful removal flag
}

// ContentModerationClearHashesResult contains the cache adapter count returned
// for an administrator clear request.
type ContentModerationClearHashesResult struct {
	Deleted int64 `json:"deleted"` // adapter sampled clear count
}

// ContentModerationRepository persists ordinary observations, coordinates Cyber
// policy effects, and supplies administrator query and retention operations.
type ContentModerationRepository interface {
	// CreateLog stores an ordinary observation and retained media, assigning
	// database IDs and time to supplied values. The PostgreSQL adapter uses one
	// log/media transaction; returned failures can leave precommit values
	// populated in the caller-owned struct.
	CreateLog(ctx context.Context, log *ContentModerationLog) error
	// ListLogs returns administrator summary rows and pagination metadata for the
	// supplied filter. The PostgreSQL count and page are separate reads, and full
	// input items, failed-unit detail, and binary media are retrieved through
	// review methods.
	ListLogs(ctx context.Context, filter ContentModerationLogFilter) ([]ContentModerationLog, *pagination.PaginationResult, error)
	// CountFlaggedByUserSince counts prior qualifying actor hits for ordinary side
	// effects. PostgreSQL excludes hash_block and records at or before its latest
	// stored auto-ban, applying since inclusively; the service adds the current
	// observation afterward.
	CountFlaggedByUserSince(ctx context.Context, userID int64, since time.Time) (int, error)
	// CreateCyberWarning stores Cyber evidence without applying user policy. The
	// PostgreSQL direct method inserts warning and media sequentially through the
	// DB, so callers use CreateCyberWarningAndApplyUserBan when warning/media work
	// needs its transaction.
	CreateCyberWarning(ctx context.Context, warning *ContentModerationCyberWarning) error
	// CreateCyberWarningAndApplyUserBan records warning/media and account policy
	// in the same PostgreSQL transaction, returning whether a new disablement was
	// applied. Valid actor rows are locked before the warning is inserted; the
	// inserted warning participates in count before policy fields and optional
	// status are written.
	//
	// Earlier failures leave rollback to be attempted, and commit errors can leave
	// database outcome uncertain. Caller storage can retain assigned warning/media
	// fields. Authentication invalidation and notices follow successful return and
	// cannot undo that committed transaction.
	CreateCyberWarningAndApplyUserBan(ctx context.Context, warning *ContentModerationCyberWarning, policy ContentModerationCyberWarningPolicy) (bool, error)
	// ListCyberWarnings returns administrator warning summaries and pagination
	// metadata for the supplied filter. PostgreSQL counts and selects in separate
	// reads, with full structured input and snapshot metadata available from the
	// review capability.
	ListCyberWarnings(ctx context.Context, filter ContentModerationCyberWarningFilter) ([]ContentModerationCyberWarning, *pagination.PaginationResult, error)
	// CountCyberWarningsByUserSince counts stored actor warnings for a time
	// window. PostgreSQL applies since inclusively and starts after the latest
	// recorded auto-ban; its separate transaction helper counts the current
	// inserted warning while applying policy.
	CountCyberWarningsByUserSince(ctx context.Context, userID int64, since time.Time) (int, error)
	// GetCyberSummary returns matching totals and grouped administrator
	// observations. PostgreSQL uses separate total, user, and provider queries
	// with a one-hundred-row cap on each grouped list, so callers do not assume
	// one consistent read moment.
	GetCyberSummary(ctx context.Context, filter ContentModerationCyberWarningFilter) (*ContentModerationCyberSummary, error)
	// MarkCyberWarningEmailSent marks a previously stored warning after a nil
	// notice callback result. PostgreSQL ignores nonpositive IDs and does not inspect
	// affected rows; an execution success can therefore return nil even if the row
	// was deleted concurrently.
	MarkCyberWarningEmailSent(ctx context.Context, id int64) error
	// CleanupExpiredLogs deletes ordinary hit, ordinary non-hit, and Cyber records
	// using their supplied cutoffs. PostgreSQL runs three independent deletes in
	// order and returns a result only after all succeed; prior deletes remain
	// applied if a later command fails.
	//
	// The schema cascades owner deletion to media. Reported counts suppress
	// RowsAffected errors, and the service publishes last-cleanup observations
	// only from a successfully returned result, so those observations can lag
	// partial deletions.
	CleanupExpiredLogs(ctx context.Context, hitBefore time.Time, nonHitBefore time.Time) (*ContentModerationCleanupResult, error)
}

// ContentModerationReviewRepository loads full administrator input detail and
// separately retrieved image snapshot bytes.
type ContentModerationReviewRepository interface {
	// GetLog loads the full stored ordinary input and failed units for
	// administrator review. PostgreSQL adds media metadata with a separate query
	// and joins current user status, so callers enforce administrator access
	// before invoking it.
	GetLog(ctx context.Context, id int64) (*ContentModerationLog, error)
	// GetCyberWarning loads stored Cyber input, failed units, and separately
	// queried media metadata. Callers enforce administrator access; the service
	// translates recognized missing-row errors and leaves binary content to the
	// separate media retrieval.
	GetCyberWarning(ctx context.Context, id int64) (*ContentModerationCyberWarning, error)
	// GetMediaContent loads one stored image snapshot by media ID for an
	// authorized administrator. The service accepts only ready nonempty bytes,
	// while the PostgreSQL query itself returns the stored retrieval status with
	// content. A successful read supplies nonnil media for service validation.
	GetMediaContent(ctx context.Context, id int64) (*ContentModerationMedia, error)
}

// ContentModerationHashCache stores and manages flagged-input identities used
// before external audit admission.
type ContentModerationHashCache interface {
	// RecordFlaggedInputHash stores an extracted identity for later request
	// checks. Service callers handle errors diagnostically and continue
	// independent side effects; production Redis retries can leave writes applied
	// even when the returned command result is uncertain.
	RecordFlaggedInputHash(ctx context.Context, inputHash string) error
	// HasFlaggedInputHash reports the cache membership observation and any read
	// error. Check logs an error and still uses the returned membership value:
	// true selects a block before sampling and external API work.
	HasFlaggedInputHash(ctx context.Context, inputHash string) (bool, error)
	// DeleteFlaggedInputHash returns an observed member-removal reply after the
	// service validates administrator input. Production Redis retries can observe
	// absence after an earlier uncertain successful removal, so false is not a
	// history of every attempted command.
	DeleteFlaggedInputHash(ctx context.Context, inputHash string) (bool, error)
	// ClearFlaggedInputHashes returns the adapter count for clearing recorded
	// hashes. Production Redis samples cardinality before a separate key delete,
	// so concurrent membership changes can separate this returned count from the
	// members actually removed.
	ClearFlaggedInputHashes(ctx context.Context) (int64, error)
	// CountFlaggedInputHashes reports the current membership count observed by its
	// adapter. GetStatus logs read errors and leaves its display count zero, while
	// missing adapters are handled separately by the service.
	CountFlaggedInputHashes(ctx context.Context) (int64, error)
}

// ContentModerationService owns ordinary admission, asynchronous observations,
// runtime snapshots, and separately persisted Cyber risk effects.
type ContentModerationService struct {
	// stopWaitOnce, stopWaitDone, and stopWaitErr publish one Stop result. The
	// worker writes stopWaitErr before closing stopWaitDone; channel receipt
	// synchronizes later callers reading that result.
	stopWaitOnce sync.Once     // shared stop waiter initializer
	stopWaitDone chan struct{} // shared stop result publication
	stopWaitErr  error         // joined shared drain result
	// runtimeMu protects runtimeStarted, runtimeStopped, runtimeDone, asyncQueue
	// closure, and runtimeWG registration. Enqueue holds a read lock through
	// sending; Stop holds the write lock before closure. runtimeWG joins workers
	// and cleanup, while synchronous requests and Background callbacks retain
	// their separate owners.
	runtimeMu      sync.RWMutex      // worker lifecycle and admission lock
	runtimeStarted bool              // background startup has occurred
	runtimeStopped bool              // queue admission permanently closed
	runtimeDone    chan struct{}     // worker and cleanup stop signal
	runtimeWG      sync.WaitGroup    // registered worker cleanup completion
	settingRepo    SettingRepository // moderation policy setting storage
	// audit and Cyber persistence
	repo      ContentModerationRepository
	hashCache ContentModerationHashCache // shared flagged-input set adapter
	groupRepo GroupRepository            // configured group validation reader
	userRepo  UserCommands               // ordinary account action commands
	// proxyRepo is assigned by SetProxyRepository before concurrent request work
	// begins. Cache entry publication is atomic, but assignment of this repository
	// field has no separate synchronization.
	proxyRepo ProxyRepository // stored outbound proxy reader
	// account authentication invalidation capability
	authCacheInvalidator APIKeyAuthCacheInvalidator
	emailService         RiskSender // risk notice delivery capability
	runtime              Runtime    // assembled technical callback values
	// immutable resolved proxy entry
	moderationProxyCache atomic.Pointer[moderationProxyURLCacheEntry]
	// asyncQueue and workerCount have fixed maxima allocated by construction.
	// Configured QueueSize is sampled during admission, and current WorkerCount
	// gates each worker before dequeue rather than resizing the runtime.
	// deferred audit and record channel
	asyncQueue  chan contentModerationTask
	workerCount int // fixed launched worker population
	// asyncActive, asyncEnqueued, asyncDropped, asyncProcessed, asyncErrors, and
	// asyncBufferedBytes track independently sampled task observations. Byte
	// reservations survive queue acceptance through execution and are released on
	// failed sends or worker completion.
	asyncActive        atomic.Int64 // currently executing accepted tasks
	asyncEnqueued      atomic.Int64 // cumulative accepted queue sends
	asyncDropped       atomic.Int64 // cumulative capacity rejection count
	asyncProcessed     atomic.Int64 // counted record or checkSync completions
	asyncErrors        atomic.Int64 // asynchronous all-unit audit failures
	asyncBufferedBytes atomic.Int64 // estimated admitted task bytes
	// preBlockActive, preBlockChecked, preBlockAllowed, preBlockBlocked,
	// preBlockErrors, and preBlockLatencyTotalMS describe synchronous decision
	// observations. Metrics update separately, and local keyword or hash decisions
	// contribute zero latency.
	preBlockActive         atomic.Int64 // currently executing synchronous audits
	preBlockChecked        atomic.Int64 // counted synchronous decision outcomes
	preBlockAllowed        atomic.Int64 // counted synchronous allow outcomes
	preBlockBlocked        atomic.Int64 // counted synchronous blocked outcomes
	preBlockErrors         atomic.Int64 // counted all-unit audit failures
	preBlockLatencyTotalMS atomic.Int64 // cumulative counted audit milliseconds
	// lastCleanupUnix, lastCleanupDeletedHit, and lastCleanupDeletedNonHit publish
	// successful cleanup observations through separate atomic stores. A status
	// reader can see values from different update moments.
	lastCleanupUnix       atomic.Int64 // Unix seconds, zero absent
	lastCleanupDeletedHit atomic.Int64 // latest reported flagged deletion count
	// latest reported unflagged deletion count
	lastCleanupDeletedNonHit atomic.Int64
	// runtimeSnapshot, runtimeRefreshMu, runtimeCacheTTL, and
	// runtimeRefreshRetryAt coordinate lazy loads and stale refresh. Published
	// entries remain immutable, reload/replacement is serialized, and retry time
	// delays admission after an error.
	// immutable hot-path policy entry
	runtimeSnapshot  atomic.Pointer[contentModerationRuntimeSnapshot]
	runtimeRefreshMu sync.Mutex // snapshot reload and replace lock
	// runtimeCacheTTL overrides the one-second snapshot lifetime only when
	// positive. Bind it before concurrent reads because this duration has no
	// separate synchronization.
	runtimeCacheTTL time.Duration // optional snapshot lifetime override
	// Unix nanoseconds, retry admission instant
	runtimeRefreshRetryAt atomic.Int64
	// keyHealthMu protects keyHealth and keySchedule together. Selection observes
	// freezes while updating smooth weights under the lock, and health helpers
	// return or mutate entries only through that shared synchronization.
	keyHealthMu sync.Mutex // credential state and scheduling lock
	// mutable state by credential hash
	keyHealth   map[string]*contentModerationKeyHealth
	keySchedule map[string]int64 // accumulated scheduling weight by hash
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

// contentModerationTask carries deferred audit or record work with its
// estimated byte reservation and enqueue time.
type contentModerationTask struct {
	// input and content retain identity pointers and selected content collections
	// after Body is dropped. The producer keeps those shared values stable through
	// deferred execution.
	input     ContentModerationCheckInput // attributed request metadata
	content   ContentModerationInput      // queued observe input values
	inputHash string                      // full extracted input identity
	// log and config distinguish record work from observe work. Records retain a
	// mutable log pointer and cloned policy; observe tasks reload policy and
	// rebuild aggregate text when a worker executes.
	log              *ContentModerationLog    // shared record awaiting persistence
	config           *ContentModerationConfig // saved record policy copy
	recordHash       bool                     // flagged-set recording choice
	applySideEffects bool                     // ordinary action delivery choice
	// bufferedBytes is the successful admission reservation released by either
	// rejected sending or task completion. Its estimate limits chosen retained
	// costs rather than measuring all Go heap allocations.
	bufferedBytes int64     // bytes
	enqueuedAt    time.Time // queue send preparation time
}

// contentModerationAuditResult aggregates successful score maxima, failed
// units, and flagged image positions for one audit.
type contentModerationAuditResult struct {
	// CategoryScores, FailedUnits, FlaggedImageIndexes, and SuccessfulUnits are
	// mutated by one merger consumer. Image workers send unit values through the
	// result channel; successful maxima remain usable when failures occur.
	CategoryScores map[string]float64 // successful category score maxima
	// public unit failure observations
	FailedUnits         []ContentModerationFailedUnit
	FlaggedImageIndexes []int // selected audit image indexes
	TextUnitCount       int   // planned text chunk count
	ImageUnitCount      int   // planned selected image count
	SuccessfulUnits     int   // successful merged unit count
	AuditComplete       bool  // no failed unit observed
}

// contentModerationUnitResult links one returned API outcome or error with its
// original audit unit identity.
type contentModerationUnitResult struct {
	unitType    string // text or image kind
	index       int    // selected audit unit position
	sourceIndex int    // original image review position
	// result and err describe a completed unit before its value is sent to the
	// merger. Workers keep the result and its score map stable after sending; the
	// merger copies category maxima into its own aggregate map.
	// successful returned score values
	result *moderationAPIResult
	err    error // unit attempt failure result
}

// contentModerationAPIError retains a received failure status and bounded
// provider message for diagnostic handling.
type contentModerationAPIError struct {
	StatusCode int    // received failure HTTP status
	Message    string // bounded captured provider diagnostic
}

// moderationProxyURLCacheEntry is one immutable stored-proxy resolution
// published through the service atomic pointer.
type moderationProxyURLCacheEntry struct {
	proxyID   int64     // resolved stored proxy identifier
	url       string    // resolved outbound proxy URL
	expiresAt time.Time // process-local cache expiry instant
}

// moderationAPIRequest is the JSON model and input shape sent to the external
// moderation transport.
type moderationAPIRequest struct {
	Model string `json:"model"` // configured external audit model
	Input any    `json:"input"` // scalar, batch, or multimodal input
}

// moderationAPIInputPart represents one text or image-URL part in a combined
// administrator audit request.
type moderationAPIInputPart struct {
	// Type selects the serialized part form. Text is used for text parts and
	// ImageURL for image parts; omitted companions use omitempty in the request
	// encoding.
	Type string `json:"type"`           // text or image_url label
	Text string `json:"text,omitempty"` // selected audit text value
	// nested audit image reference
	ImageURL *moderationAPIImageURLRef `json:"image_url,omitempty"`
}

// moderationAPIImageURLRef is the nested image-reference shape serialized for
// the moderation request.
type moderationAPIImageURLRef struct {
	URL string `json:"url"` // literal audit image reference
}

// moderationAPIResponse decodes the ordered result list returned by the
// external moderation endpoint.
type moderationAPIResponse struct {
	// ordered external audit outcomes
	Results []moderationAPIResult `json:"results"`
}

// moderationAPIResult decodes provider score observations that are evaluated
// against local configured thresholds.
type moderationAPIResult struct {
	// Flagged is decoded for compatibility with provider replies. Local admission
	// and administrator probe flagging use CategoryScores and configured
	// known-category thresholds instead of this provider boolean.
	// provider supplied flag observation
	Flagged bool `json:"flagged"`
	// provider supplied category scores
	CategoryScores map[string]float64 `json:"category_scores"`
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

// Normalize fills missing review items and source labels while retaining full
// text bytes. A nil receiver is a no-op. Images are filtered and deduplicated
// first; when Items is already nonempty, its existing item and ImageItems
// association remains the caller-supplied representation.
//
// When Items is empty, text receives the first position and normalized images
// receive subsequent item indexes with user source. An empty Source is derived
// from resulting items, while an existing Source is kept. Callers provide
// consistent supplied Items and ImageItems before downstream audit filtering.
func (in *ContentModerationInput) Normalize() {
	if in == nil {
		return
	}
	// Filter and deduplicate aggregate images before synthesizing positions, so newly
	// built ImageItems point to the same retained references as their review items.
	in.Images = normalizeModerationImages(in.Images)
	if len(in.Items) == 0 {
		if strings.TrimSpace(in.Text) != "" {
			in.Items = append(in.Items, ContentModerationInputItem{Index: len(in.Items), Source: ContentModerationSourceUser, Type: ContentModerationItemTypeText, Text: in.Text})
		}
		for _, image := range in.Images {
			item := ContentModerationInputItem{Index: len(in.Items), Source: ContentModerationSourceUser, Type: ContentModerationItemTypeImage, ImageRef: image}
			in.Items = append(in.Items, item)
			in.ImageItems = append(in.ImageItems, ContentModerationImage{SourceIndex: item.Index, Source: item.Source, Reference: image})
		}
	}
	if in.Source == "" {
		in.Source = contentModerationInputSource(in.Items)
	}
}

// Clone copies Images, Items, and ImageItems slice storage so callers can
// mutate or normalize the returned moderation input without aliasing retained snapshots.
func (in ContentModerationInput) Clone() ContentModerationInput {
	in.Images = slices.Clone(in.Images)
	in.Items = slices.Clone(in.Items)
	in.ImageItems = slices.Clone(in.ImageItems)
	return in
}

// IsEmpty reports whether aggregate text is blank and the image list is empty.
// Item collections alone do not make this predicate nonempty, so callers
// normalize or construct their aggregate representation before using it for
// admission.
func (in ContentModerationInput) IsEmpty() bool {
	return strings.TrimSpace(in.Text) == "" && len(in.Images) == 0
}

// ModerationInput returns aggregate text alone or text/image parts when Images
// is nonempty. Blank aggregate text is omitted from parts; it retains supplied
// reference strings and relies on earlier normalization for image filtering.
func (in ContentModerationInput) ModerationInput() any {
	if len(in.Images) == 0 {
		return in.Text
	}
	parts := make([]moderationAPIInputPart, 0, len(in.Images)+1)
	if strings.TrimSpace(in.Text) != "" {
		parts = append(parts, moderationAPIInputPart{Type: "text", Text: in.Text})
	}
	for _, image := range in.Images {
		parts = append(parts, moderationAPIInputPart{
			Type:     "image_url",
			ImageURL: &moderationAPIImageURLRef{URL: image},
		})
	}
	return parts
}

// normalizeContentModerationSource returns tool for its trimmed
// case-insensitive spelling and user for every other value. Mixed is an
// aggregate label, so individual unrecognized source values follow the user
// path.
func normalizeContentModerationSource(source string) string {
	if strings.EqualFold(strings.TrimSpace(source), ContentModerationSourceTool) {
		return ContentModerationSourceTool
	}
	return ContentModerationSourceUser
}

// contentModerationInputSource derives user, tool, or mixed from normalized
// item sources. Empty items yield user, and unrecognized per-item source labels
// also normalize to user. The decision uses retained item labels rather than
// whether a unit is text or an image.
//
// A single source kind keeps its label, while at least one retained item of
// each kind produces mixed regardless of encounter order. Filtered audit input
// recomputes this label from selected items; Normalize uses it when the caller
// has left aggregate Source blank.
func contentModerationInputSource(items []ContentModerationInputItem) string {
	hasUser := false
	hasTool := false
	for _, item := range items {
		switch normalizeContentModerationSource(item.Source) {
		case ContentModerationSourceTool:
			hasTool = true
		default:
			hasUser = true
		}
	}
	if hasUser && hasTool {
		return ContentModerationSourceMixed
	}
	if hasTool {
		return ContentModerationSourceTool
	}
	return ContentModerationSourceUser
}

// Hash returns a hexadecimal SHA-256 digest of aggregate text and ordered
// image-reference text. Check computes it before audit toggles and text limits,
// so recorded-hash lookup and deterministic sampling use the full extracted
// input. Source labels and review positions do not contribute.
//
// The digest input starts with text: and appends each reference digest after a
// newline and image: marker. References identify submitted spelling, not
// downloaded bytes. With d equal to the hexadecimal SHA-256 of reference r,
// text "abc\nimage:"+d with no images and text "abc" with image r both feed
// "text:abc\nimage:"+d. Text is unescaped and unframed, so the ambiguity exists
// before hashing and the digest cannot distinguish every text/image combination.
func (in ContentModerationInput) Hash() string {
	h := sha256.New()
	// Hash.Write accepts every supplied byte and never returns an error; the
	// discarded results cover the prefix, text, and reference-marker writes below.
	_, _ = h.Write([]byte("text:"))
	_, _ = h.Write([]byte(in.Text))
	for _, image := range in.Images {
		imageHash := sha256.Sum256([]byte(image))
		_, _ = h.Write([]byte("\nimage:"))
		_, _ = h.Write([]byte(hex.EncodeToString(imageHash[:])))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// NewContentModerationService binds storage and runtime adapters and allocates
// a maximum-sized queue with key-health state. Construction launches no
// workers. App binds proxy lookup before Start; enabled HTTP, snapshot,
// background refresh, and Cyber paths require their corresponding Runtime
// callbacks.
func NewContentModerationService(
	settingRepo SettingRepository,
	repo ContentModerationRepository,
	hashCache ContentModerationHashCache,
	groupRepo GroupRepository,
	userRepo UserCommands,
	authCacheInvalidator APIKeyAuthCacheInvalidator,
	emailService RiskSender,
	runtime Runtime,
) *ContentModerationService {
	svc := &ContentModerationService{
		settingRepo:          settingRepo,
		repo:                 repo,
		hashCache:            hashCache,
		groupRepo:            groupRepo,
		userRepo:             userRepo,
		authCacheInvalidator: authCacheInvalidator,
		emailService:         emailService,
		runtime:              runtime,
		workerCount:          maxContentModerationWorkerCount,
		asyncQueue:           make(chan contentModerationTask, maxContentModerationQueueSize),
		keyHealth:            make(map[string]*contentModerationKeyHealth),
		keySchedule:          make(map[string]int64),
	}
	return svc
}

// SetProxyRepository supplies proxy lookup and clears the current resolved-URL
// entry. Bind it before concurrent audit work because the repository field
// itself is assigned without synchronization; an already running lookup can
// still publish its result after the cache clear.
func (s *ContentModerationService) SetProxyRepository(proxyRepo ProxyRepository) {
	if s != nil {
		s.proxyRepo = proxyRepo
		// Clear the resolved URL when binding its lookup source so later audit
		// misses reload that selection through the newly supplied repository.
		s.moderationProxyCache.Store(nil)
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

// TestAPIKeys probes supplied credentials or the saved pool with temporary
// endpoint, model, timeout, and proxy overrides. An omitted proxy uses saved
// selection and a nonpositive override selects direct transport. Blank test
// content sends hello; saved credentials with real audit content select one
// scheduled unfrozen key, while explicit credentials or a health-only request
// probe their whole selected list.
//
// Individual call failures update process-local key health and are returned as
// item statuses, rather than failing the overall probe. The first successful
// result supplies the optional displayed audit evaluation. Validation and
// configuration read errors still fail the operation before probing, and input
// overrides are not persisted.
func (s *ContentModerationService) TestAPIKeys(ctx context.Context, input TestContentModerationAPIKeysInput) (*TestContentModerationAPIKeysResult, error) {
	cfg, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	keys := normalizeModerationAPIKeys(input.APIKeys)
	configured := false
	if len(keys) == 0 {
		keys = cfg.apiKeys()
		configured = true
	}
	if strings.TrimSpace(input.BaseURL) != "" {
		cfg.BaseURL = input.BaseURL
	}
	if strings.TrimSpace(input.Model) != "" {
		cfg.Model = input.Model
	}
	if input.TimeoutMS > 0 {
		cfg.TimeoutMS = input.TimeoutMS
	}
	if input.ProxyID != nil {
		if *input.ProxyID > 0 {
			proxyID := *input.ProxyID
			cfg.ProxyID = &proxyID
		} else {
			cfg.ProxyID = nil
		}
	}
	cfg.normalize()
	testInput, imageCount, err := buildModerationTestInput(input.Prompt, input.Images)
	if err != nil {
		return nil, err
	}
	auditOnly := contentModerationTestHasAuditInput(input.Prompt, input.Images)
	if configured && auditOnly {
		key, ok := s.nextUsableAPIKey(cfg)
		if !ok {
			return &TestContentModerationAPIKeysResult{
				Items:      s.apiKeyStatuses(cfg),
				ImageCount: imageCount,
			}, nil
		}
		keys = []string{key}
	}
	if len(keys) == 0 {
		return &TestContentModerationAPIKeysResult{Items: []ContentModerationAPIKeyStatus{}, ImageCount: imageCount}, nil
	}
	items := make([]ContentModerationAPIKeyStatus, 0, len(keys))
	var auditResult *ContentModerationTestAuditResult
	for idx, key := range keys {
		start := time.Now()
		httpStatus := 0
		result, err := s.callModerationOnceWithInput(ctx, cfg, key, testInput, &httpStatus)
		// Truncate elapsed duration to the integer milliseconds used by key health.
		latency := int(time.Since(start).Milliseconds())
		keyHash := moderationAPIKeyHash(key)
		if err != nil {
			s.markAPIKeyError(key, err.Error(), latency, httpStatus)
		} else {
			s.markAPIKeySuccess(key, latency, httpStatus)
			if auditResult == nil && len(result) > 0 {
				auditResult = buildContentModerationTestAuditResult(&result[0], cfg.Thresholds)
			}
		}
		priority := defaultContentModerationAPIKeyPriority
		note := ""
		if configured {
			metadata := cfg.apiKeyMetadataForHash(keyHash)
			priority = metadata.Priority
			note = metadata.Note
		}
		status := s.apiKeyStatusForHash(idx, keyHash, maskSecretTail(key), configured, priority, note)
		status.LastTested = true
		items = append(items, status)
	}
	return &TestContentModerationAPIKeysResult{Items: items, AuditResult: auditResult, ImageCount: imageCount}, nil
}

// Check returns a request-admission decision after risk enablement, ordinary
// mode, group, and requested-model gates. Missing dependencies, configuration
// load failures, disabled policy, unsupported empty extraction, and sample
// misses allow the request. Pre-block keywords run first; a keyword_only miss
// returns before hash checking, sampling, or external auditing.
//
// An enabled flagged-hash match blocks even in observe mode and adds its hash
// to a nonempty block message. Hash read errors are logged; returned membership
// still decides the match. Observe otherwise queues work and immediately
// allows; queue rejection attempts an incomplete log synchronously. Pre-block
// API decisions allow when every unit fails, and a confirmed threshold hit can
// block even if another unit is incomplete.
// @project-doc docs/domains/content_moderation.md#content_moderation_decision_pipeline
func (s *ContentModerationService) Check(ctx context.Context, input ContentModerationCheckInput) (*ContentModerationDecision, error) {
	allow := &ContentModerationDecision{Allowed: true, Action: ContentModerationActionAllow}
	if s == nil || s.settingRepo == nil || s.repo == nil {
		slog.Info("content_moderation.skip_unavailable",
			"user_id", input.UserID,
			"api_key_id", input.APIKeyID,
			"group_id", contentModerationLogGroupID(input.GroupID),
			"endpoint", input.Endpoint,
			"protocol", input.Protocol)
		return allow, nil
	}
	runtimeSnapshot, err := s.loadRuntimeSnapshot(ctx)
	// An initial runtime read failure produces an allowed decision and diagnostic
	// metadata; it does not certify this extracted request as successfully audited.
	if err != nil {
		slog.Warn("content_moderation.skip_config_load_failed",
			"user_id", input.UserID,
			"api_key_id", input.APIKeyID,
			"group_id", contentModerationLogGroupID(input.GroupID),
			"endpoint", input.Endpoint,
			"protocol", input.Protocol,
			"error", err)
		return allow, nil
	}
	if !runtimeSnapshot.riskControlEnabled {
		slog.Info("content_moderation.skip_feature_disabled",
			"user_id", input.UserID,
			"api_key_id", input.APIKeyID,
			"group_id", contentModerationLogGroupID(input.GroupID),
			"endpoint", input.Endpoint,
			"protocol", input.Protocol)
		return allow, nil
	}
	cfg := runtimeSnapshot.config
	inGroupScope := cfg.includesGroup(input.GroupID)
	inModelScope := cfg.includesModel(input.Model)
	slog.Info("content_moderation.config_loaded",
		"user_id", input.UserID,
		"api_key_id", input.APIKeyID,
		"group_id", contentModerationLogGroupID(input.GroupID),
		"group_name", input.GroupName,
		"endpoint", input.Endpoint,
		"provider", input.Provider,
		"protocol", input.Protocol,
		"model", input.Model,
		"enabled", cfg.Enabled,
		"mode", cfg.Mode,
		"all_groups", cfg.AllGroups,
		"configured_group_ids", cfg.GroupIDs,
		"in_group_scope", inGroupScope,
		"model_filter_type", cfg.ModelFilter.Type,
		"configured_models", cfg.ModelFilter.Models,
		"in_model_scope", inModelScope,
		"sample_rate", cfg.SampleRate,
		"api_key_count", len(cfg.apiKeys()),
		"pre_hash_check_enabled", cfg.PreHashCheckEnabled,
		"record_non_hits", cfg.RecordNonHits)
	if !cfg.Enabled {
		slog.Info("content_moderation.skip_config_disabled",
			"user_id", input.UserID,
			"api_key_id", input.APIKeyID,
			"group_id", contentModerationLogGroupID(input.GroupID),
			"endpoint", input.Endpoint,
			"protocol", input.Protocol)
		return allow, nil
	}
	if cfg.Mode == ContentModerationModeOff {
		slog.Info("content_moderation.skip_mode_off",
			"user_id", input.UserID,
			"api_key_id", input.APIKeyID,
			"group_id", contentModerationLogGroupID(input.GroupID),
			"endpoint", input.Endpoint,
			"protocol", input.Protocol)
		return allow, nil
	}
	if !inGroupScope {
		slog.Info("content_moderation.skip_group_out_of_scope",
			"user_id", input.UserID,
			"api_key_id", input.APIKeyID,
			"group_id", contentModerationLogGroupID(input.GroupID),
			"group_name", input.GroupName,
			"endpoint", input.Endpoint,
			"protocol", input.Protocol,
			"all_groups", cfg.AllGroups,
			"configured_group_ids", cfg.GroupIDs)
		return allow, nil
	}
	if !inModelScope {
		slog.Info("content_moderation.skip_model_out_of_scope",
			"user_id", input.UserID,
			"api_key_id", input.APIKeyID,
			"group_id", contentModerationLogGroupID(input.GroupID),
			"group_name", input.GroupName,
			"endpoint", input.Endpoint,
			"protocol", input.Protocol,
			"model", input.Model,
			"model_filter_type", cfg.ModelFilter.Type,
			"configured_models", cfg.ModelFilter.Models)
		return allow, nil
	}
	var content ContentModerationInput
	if input.PreExtracted != nil {
		// Reuse cloned units when an earlier request snapshot already extracted this
		// exact protocol and body buffer, avoiding a second multi-megabyte JSON pass.
		content = input.PreExtracted.Clone()
	} else {
		extractStart := time.Now()
		content = ExtractContentModerationInput(input.Protocol, input.Body)
		timing.ObserveSubstage(ctx, "moderation_extract_ms", time.Since(extractStart))
	}
	if content.IsEmpty() {
		slog.Info("content_moderation.skip_empty_input",
			"user_id", input.UserID,
			"api_key_id", input.APIKeyID,
			"group_id", contentModerationLogGroupID(input.GroupID),
			"endpoint", input.Endpoint,
			"protocol", input.Protocol,
			"body_bytes", len(input.Body))
		return allow, nil
	}
	content.Normalize()
	slog.Info("content_moderation.input_extracted",
		"user_id", input.UserID,
		"api_key_id", input.APIKeyID,
		"group_id", contentModerationLogGroupID(input.GroupID),
		"endpoint", input.Endpoint,
		"protocol", input.Protocol,
		"text_runes", len([]rune(content.Text)),
		"image_count", len(content.Images))
	// Hash full extracted text and ordered image references before selecting audit
	// limits, keeping local membership and sampling independent of later
	// unit exclusion.
	hashText := content.Hash()
	if cfg.Mode == ContentModerationModePreBlock {
		if cfg.KeywordBlockingMode != ContentModerationKeywordModeAPIOnly && len(cfg.BlockedKeywords) > 0 {
			if keyword, hit := runtimeSnapshot.matchBlockedKeyword(content.Text); hit {
				s.recordPreBlockSyncMetric(0, ContentModerationActionKeywordBlock)
				slog.Info("content_moderation.keyword_block",
					"user_id", input.UserID,
					"api_key_id", input.APIKeyID,
					"group_id", contentModerationLogGroupID(input.GroupID),
					"endpoint", input.Endpoint,
					"protocol", input.Protocol,
					"keyword_blocking_mode", cfg.KeywordBlockingMode,
					"keyword", keyword)
				scores := map[string]float64{contentModerationKeywordCategory: 1.0}
				log := s.buildStructuredLog(input, cfg, ContentModerationActionKeywordBlock, true, contentModerationKeywordCategory, 1.0, scores, content, nil, nil, "", nil)
				log.MatchedKeyword = keyword
				// Queue rejection keeps the keyword block effective and attempts its risk
				// record inline, including ordinary hit effects for this newly identified
				// keyword.
				if !s.enqueueRecord(input, cfg, log, hashText, false, true) {
					s.persistContentModerationLog(ctx, cfg, log, hashText, false, true)
				}
				return &ContentModerationDecision{
					Allowed:         false,
					Blocked:         true,
					Flagged:         true,
					Message:         cfg.BlockMessage,
					StatusCode:      cfg.BlockStatus,
					HighestCategory: contentModerationKeywordCategory,
					HighestScore:    1.0,
					CategoryScores:  scores,
					Action:          ContentModerationActionKeywordBlock,
				}, nil
			}
		}
		// A keyword-only miss settles admission here; continuing would add hash or
		// API rejection work to the configured keyword-only decision on this path.
		if cfg.KeywordBlockingMode == ContentModerationKeywordModeKeywordOnly {
			s.recordPreBlockSyncMetric(0, ContentModerationActionAllow)
			slog.Info("content_moderation.skip_api_keyword_only",
				"user_id", input.UserID,
				"api_key_id", input.APIKeyID,
				"group_id", contentModerationLogGroupID(input.GroupID),
				"endpoint", input.Endpoint,
				"protocol", input.Protocol)
			return allow, nil
		}
	}
	if cfg.PreHashCheckEnabled && s.hashCache != nil {
		matched, err := s.hashCache.HasFlaggedInputHash(ctx, hashText)
		// An uncertain hash lookup remains diagnostic; proceed to sampling and API
		// work unless the returned membership observation itself reports a match.
		if err != nil {
			slog.Warn("content_moderation.hash_check_failed", "user_id", input.UserID, "endpoint", input.Endpoint, "error", err)
		}
		if matched {
			if cfg.Mode == ContentModerationModePreBlock {
				s.recordPreBlockSyncMetric(0, ContentModerationActionHashBlock)
			}
			slog.Info("content_moderation.hash_block",
				"user_id", input.UserID,
				"api_key_id", input.APIKeyID,
				"group_id", contentModerationLogGroupID(input.GroupID),
				"endpoint", input.Endpoint,
				"protocol", input.Protocol,
				"input_hash", hashText)
			message := cfg.BlockMessage
			if message != "" {
				message = fmt.Sprintf("%s (hash: %s)", message, hashText)
			}
			scores := map[string]float64{"hash": 1.0}
			log := s.buildStructuredLog(input, cfg, ContentModerationActionHashBlock, true, "hash", 1.0, scores, content, nil, nil, "", nil)
			// Queue rejection records the repeated hash inline; ordinary violation side
			// effects stay disabled because this recorded identity
			// already selected the local block.
			if !s.enqueueRecord(input, cfg, log, hashText, false, false) {
				s.persistContentModerationLog(ctx, cfg, log, hashText, false, false)
			}
			return &ContentModerationDecision{
				Allowed:    false,
				Blocked:    true,
				Flagged:    true,
				Message:    message,
				StatusCode: cfg.BlockStatus,
				InputHash:  hashText,
				Action:     ContentModerationActionHashBlock,
			}, nil
		}
	}
	if !cfg.shouldSample(hashText) {
		if cfg.Mode == ContentModerationModePreBlock {
			s.recordPreBlockSyncMetric(0, ContentModerationActionAllow)
		}
		slog.Info("content_moderation.skip_sample_rate",
			"user_id", input.UserID,
			"api_key_id", input.APIKeyID,
			"group_id", contentModerationLogGroupID(input.GroupID),
			"endpoint", input.Endpoint,
			"protocol", input.Protocol,
			"sample_rate", cfg.SampleRate)
		return allow, nil
	}
	// Source toggles can remove every audit unit from otherwise retained input.
	// Skip that empty selected scope before API selection or observe queue admission.
	if contentModerationAuditInput(content, cfg).IsEmpty() {
		slog.Info("content_moderation.skip_empty_audit_scope",
			"user_id", input.UserID,
			"api_key_id", input.APIKeyID,
			"endpoint", input.Endpoint,
			"protocol", input.Protocol)
		return allow, nil
	}
	if cfg.Mode == ContentModerationModeObserve {
		slog.Info("content_moderation.enqueue_observe",
			"user_id", input.UserID,
			"api_key_id", input.APIKeyID,
			"group_id", contentModerationLogGroupID(input.GroupID),
			"endpoint", input.Endpoint,
			"protocol", input.Protocol,
			"queue_len", len(s.asyncQueue))
		// A rejected observe task leaves the admission decision allowed. Attempt an
		// incomplete request record inline so queue loss remains
		// visible to administrator review.
		if !s.enqueueAsync(input, cfg, content, hashText) {
			queueErr := errors.New("content moderation queue capacity exceeded")
			auditInput := contentModerationAuditInput(content, cfg)
			audit := &contentModerationAuditResult{
				CategoryScores: make(map[string]float64),
				FailedUnits: []ContentModerationFailedUnit{{
					Type:  ContentModerationItemTypeRequest,
					Index: 0,
					Error: queueErr.Error(),
				}},
				TextUnitCount:  countContentModerationTextChunks(auditInput.Text, maxModerationInputRunes, contentModerationChunkOverlap),
				ImageUnitCount: len(auditInput.ImageItems),
				AuditComplete:  false,
			}
			log := s.buildStructuredLog(input, cfg, ContentModerationActionError, false, "", 0, nil, content, nil, nil, queueErr.Error(), audit)
			s.persistContentModerationLog(ctx, cfg, log, hashText, false, false)
		}
		return allow, nil
	}

	return s.checkSync(ctx, input, cfg, content, hashText, nil, true), nil
}

// checkSync audits selected text and images and evaluates successful score
// units for admission. Supply a nonnil normalized cfg, a nonnil context, and
// stable content. allowBlock and pre_block together permit rejection; a nonnil
// queue delay identifies asynchronous execution. One thirty-second audit
// context covers all unit calls, while subsequent record work uses the parent
// context.
//
// An audit with no successful unit allows and attempts an incomplete record.
// Partial failure keeps successful score hits effective and records failed
// units. Sensitive retention prevents image attachment. Synchronous pre-block
// records prefer queueing and persist inline after rejection; an accepted
// queued record can still be unfinished when the admission decision returns.
func (s *ContentModerationService) checkSync(ctx context.Context, input ContentModerationCheckInput, cfg *ContentModerationConfig, content ContentModerationInput, hashText string, queueDelay *int, allowBlock bool) *ContentModerationDecision {
	allow := &ContentModerationDecision{Allowed: true, Action: ContentModerationActionAllow}
	auditInput := contentModerationAuditInput(content, cfg)
	if auditInput.IsEmpty() {
		slog.Info("content_moderation.skip_empty_audit_scope",
			"user_id", input.UserID,
			"api_key_id", input.APIKeyID,
			"endpoint", input.Endpoint,
			"protocol", input.Protocol)
		return allow
	}
	trackPreBlock := queueDelay == nil && allowBlock && cfg != nil && cfg.Mode == ContentModerationModePreBlock
	// Track direct pre-block audits as active synchronous work. Deferred
	// subtraction also removes failed audits from this activity observation when
	// the function returns.
	if trackPreBlock {
		s.preBlockActive.Add(1)
		defer s.preBlockActive.Add(-1)
	}
	start := time.Now()
	// Give text batches and image workers one audit deadline so later units use
	// remaining time instead of each extending the overall external audit budget.
	auditCtx, cancel := context.WithTimeout(ctx, contentModerationAuditTotalTimeout)
	result := s.auditContentModerationInput(auditCtx, cfg, auditInput, trackPreBlock)
	cancel()
	elapsed := time.Since(start)
	if queueDelay == nil {
		timing.ObserveSubstage(ctx, "moderation_external_ms", elapsed)
	}
	// Record the shared audit duration in truncated integer milliseconds.
	latency := int(elapsed.Milliseconds())
	if result.SuccessfulUnits == 0 {
		if trackPreBlock {
			s.recordPreBlockSyncMetric(latency, ContentModerationActionError)
		}
		slog.Warn("content_moderation.audit_api_failed",
			"user_id", input.UserID,
			"api_key_id", input.APIKeyID,
			"group_id", contentModerationLogGroupID(input.GroupID),
			"endpoint", input.Endpoint,
			"protocol", input.Protocol,
			"mode", cfg.Mode,
			"allow_block", allowBlock,
			"queue_delay_ms", queueDelay,
			"latency_ms", latency,
			"error", result.ErrorText())
		if queueDelay != nil {
			s.asyncErrors.Add(1)
		}
		// With no successful unit, admission remains allowed while the incomplete
		// record identifies failed audit work; this result is not confirmed safe content.
		log := s.buildStructuredLog(input, cfg, ContentModerationActionError, false, "", 0, nil, content, &latency, queueDelay, result.ErrorText(), result)
		s.persistContentModerationLog(ctx, cfg, log, hashText, false, false)
		return allow
	}

	flagged, highestCategory, highestScore := evaluateModerationScores(result.CategoryScores, cfg.Thresholds)
	action := ContentModerationActionAllow
	blocked := false
	if allowBlock && flagged && cfg.Mode == ContentModerationModePreBlock {
		action = ContentModerationActionBlock
		blocked = true
	}
	if trackPreBlock {
		s.recordPreBlockSyncMetric(latency, action)
	}
	slog.Info("content_moderation.audit_result",
		"user_id", input.UserID,
		"api_key_id", input.APIKeyID,
		"group_id", contentModerationLogGroupID(input.GroupID),
		"group_name", input.GroupName,
		"endpoint", input.Endpoint,
		"protocol", input.Protocol,
		"mode", cfg.Mode,
		"allow_block", allowBlock,
		"flagged", flagged,
		"blocked", blocked,
		"action", action,
		"highest_category", highestCategory,
		"highest_score", highestScore,
		"latency_ms", latency,
		"queue_delay_ms", queueDelay)
	if flagged || cfg.RecordNonHits || !result.AuditComplete {
		log := s.buildStructuredLog(input, cfg, action, flagged, highestCategory, highestScore, result.CategoryScores, content, &latency, queueDelay, result.ErrorText(), result)
		if !input.NoMediaRetention {
			// Use selected audit positions to recover original review-item identities
			// for hit snapshots. This branch attaches media only when the request
			// permits content retention.
			log.Media = contentModerationHitMedia(auditInput, result.FlaggedImageIndexes)
		}
		if queueDelay == nil && cfg.Mode == ContentModerationModePreBlock {
			// A decided synchronous audit prefers deferred recording; a rejected send
			// persists inline so accepted queue capacity does not
			// control its block decision.
			if !s.enqueueRecord(input, cfg, log, hashText, flagged, flagged) {
				s.persistContentModerationLog(ctx, cfg, log, hashText, flagged, flagged)
			}
		} else {
			s.persistContentModerationLog(ctx, cfg, log, hashText, flagged, flagged)
		}
	}
	if blocked {
		return &ContentModerationDecision{
			Allowed:         false,
			Blocked:         true,
			Flagged:         true,
			Message:         cfg.BlockMessage,
			StatusCode:      cfg.BlockStatus,
			HighestCategory: highestCategory,
			HighestScore:    highestScore,
			CategoryScores:  result.CategoryScores,
			Action:          action,
		}
	}
	return &ContentModerationDecision{
		Allowed:         true,
		Flagged:         flagged,
		Message:         "",
		HighestCategory: highestCategory,
		HighestScore:    highestScore,
		CategoryScores:  result.CategoryScores,
		Action:          action,
	}
}

// ErrorText joins failed-unit kind, index, and public error text for storage
// diagnostics. Nil results and empty failure lists return empty text, and the
// join follows the failure order recorded by the merger.
func (r *contentModerationAuditResult) ErrorText() string {
	if r == nil || len(r.FailedUnits) == 0 {
		return ""
	}
	parts := make([]string, 0, len(r.FailedUnits))
	for _, unit := range r.FailedUnits {
		parts = append(parts, fmt.Sprintf("%s[%d]: %s", unit.Type, unit.Index, unit.Error))
	}
	return strings.Join(parts, "; ")
}

// Error returns the moderation HTTP status and captured message as diagnostic
// text. A nil error receiver returns a generic label; public audit records
// separately replace this diagnostic text with status-class messages.
func (e *contentModerationAPIError) Error() string {
	if e == nil {
		return "moderation api error"
	}
	return fmt.Sprintf("moderation api status %d: %s", e.StatusCode, e.Message)
}

// auditContentModerationInput collects maximum category scores and
// incomplete-unit observations across text batches and images. Text batches
// execute sequentially first. Up to four image workers then submit
// independently completing unit results to one consumer, which owns all
// aggregate mutations.
//
// Cancellation makes accepted image jobs report failure but does not abandon
// their result sends; the consumer drains until the producer closes results
// after joining workers. That ordering retains each unit outcome and prevents a
// blocked result sender from being left behind. AuditComplete reports whether
// any failed unit was recorded, while successful scores can still support a
// blocking decision.
func (s *ContentModerationService) auditContentModerationInput(ctx context.Context, cfg *ContentModerationConfig, content ContentModerationInput, trackKeyLoad bool) *contentModerationAuditResult {
	result := &contentModerationAuditResult{
		CategoryScores: make(map[string]float64),
		TextUnitCount:  countContentModerationTextChunks(content.Text, maxModerationInputRunes, contentModerationChunkOverlap),
		ImageUnitCount: len(content.ImageItems),
	}
	forEachContentModerationTextBatch(content.Text, maxModerationInputRunes, contentModerationChunkOverlap, contentModerationTextBatchSize, func(batchStart int, chunks []string) {
		unitResults := s.auditTextBatch(ctx, cfg, chunks, batchStart, trackKeyLoad)
		for _, unit := range unitResults {
			mergeContentModerationUnitResult(result, unit, cfg.Thresholds)
		}
	})

	// Image workers publish individual outcomes to one merger after text completes;
	// draining every result keeps cancellation failures associated with
	// their accepted jobs.
	if len(content.ImageItems) > 0 {
		workerCount := min(contentModerationImageConcurrency, len(content.ImageItems))
		results := make(chan contentModerationUnitResult, workerCount)
		jobs := make(chan int)
		var wg sync.WaitGroup
		for range workerCount {
			wg.Go(func() {
				for index := range jobs {
					image := content.ImageItems[index]
					if err := ctx.Err(); err != nil {
						results <- contentModerationUnitResult{unitType: ContentModerationItemTypeImage, index: index, sourceIndex: image.SourceIndex, err: err}
						continue
					}
					parts := []moderationAPIInputPart{{Type: "image_url", ImageURL: &moderationAPIImageURLRef{URL: image.Reference}}}
					apiResult, err := s.callModeration(ctx, cfg, parts, trackKeyLoad)
					results <- contentModerationUnitResult{unitType: ContentModerationItemTypeImage, index: index, sourceIndex: image.SourceIndex, result: apiResult, err: err}
				}
			})
		}
		// Dispatch every image position, then close jobs and join workers before
		// closing results; the consumer stays available for all accepted result sends.
		go func() {
			for index := range content.ImageItems {
				jobs <- index
			}
			close(jobs)
			wg.Wait()
			close(results)
		}()
		for unit := range results {
			mergeContentModerationUnitResult(result, unit, cfg.Thresholds)
		}
	}
	result.AuditComplete = len(result.FailedUnits) == 0
	return result
}

// auditTextBatch associates one outcome with every supplied text chunk using
// startIndex. Empty chunks return nil, and a single chunk uses scalar API
// input. A successful batch is accepted only when result count matches input
// count, preserving positional association.
//
// Unsupported HTTP-400 batch messages and successful replies with mismatched
// counts retry chunks individually. Other failures are attached to every chunk
// without fan-out, limiting requests during provider failures. Each individual
// fallback still uses normal key selection and retry policy, and returned
// indexes remain the original batch positions.
func (s *ContentModerationService) auditTextBatch(ctx context.Context, cfg *ContentModerationConfig, chunks []string, startIndex int, trackKeyLoad bool) []contentModerationUnitResult {
	if len(chunks) == 0 {
		return nil
	}
	input := any(chunks)
	if len(chunks) == 1 {
		input = chunks[0]
	}
	results, err := s.callModerationBatch(ctx, cfg, input, trackKeyLoad)
	if err == nil && len(results) == len(chunks) {
		out := make([]contentModerationUnitResult, 0, len(results))
		for index := range results {
			item := results[index]
			out = append(out, contentModerationUnitResult{unitType: ContentModerationItemTypeText, index: startIndex + index, result: &item})
		}
		return out
	}
	resultCountMismatch := err == nil && len(results) != len(chunks)
	if resultCountMismatch {
		err = fmt.Errorf("moderation api returned %d results for %d inputs", len(results), len(chunks))
	}
	if len(chunks) == 1 {
		return []contentModerationUnitResult{{unitType: ContentModerationItemTypeText, index: startIndex, err: err}}
	}
	if !resultCountMismatch && !isContentModerationBatchUnsupportedError(err) {
		out := make([]contentModerationUnitResult, 0, len(chunks))
		for index := range chunks {
			out = append(out, contentModerationUnitResult{unitType: ContentModerationItemTypeText, index: startIndex + index, err: err})
		}
		return out
	}
	// Retry individual chunks when batch shape or result count prevents positional
	// association; other errors retain their batch scope so an upstream failure
	// does not fan out requests.
	out := make([]contentModerationUnitResult, 0, len(chunks))
	for index, chunk := range chunks {
		item, singleErr := s.callModeration(ctx, cfg, chunk, trackKeyLoad)
		out = append(out, contentModerationUnitResult{unitType: ContentModerationItemTypeText, index: startIndex + index, result: item, err: singleErr})
	}
	return out
}

// isContentModerationBatchUnsupportedError recognizes wrapped HTTP-400 errors
// containing selected lowercase batch-shape markers. Other statuses, error
// types, or messages return false; this classification permits individual
// retries in auditTextBatch. A matching typed error pointer must be nonnil
// because its status is read after errors.As succeeds.
func isContentModerationBatchUnsupportedError(err error) bool {
	var apiErr *contentModerationAPIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 400 {
		return false
	}
	message := strings.ToLower(apiErr.Message)
	for _, marker := range []string{"batch", "array", "must be a string", "expected string", "unsupported input"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// mergeContentModerationUnitResult updates an aggregate using one completed
// unit and local category thresholds. Nil targets are ignored. Failed or
// missing results append a public error record; successful units increase the
// success count and retain each category maximum across all successful units.
//
// Image hits also append their audit image index for later review mapping.
// Threshold evaluation uses local scores rather than the provider Flagged bit,
// so independently successful units remain effective even when another unit
// fails. The aggregate is owned by the single result consumer and requires no
// concurrent mutation.
func mergeContentModerationUnitResult(target *contentModerationAuditResult, unit contentModerationUnitResult, thresholds map[string]float64) {
	if target == nil {
		return
	}
	// Store public failure classes instead of bounded provider diagnostics here,
	// keeping echoed request or credential text out of the retained
	// failed-unit message.
	if unit.err != nil || unit.result == nil {
		errText := "moderation api returned no result"
		if unit.err != nil {
			errText = publicContentModerationError(unit.err)
		}
		target.FailedUnits = append(target.FailedUnits, ContentModerationFailedUnit{Type: unit.unitType, Index: unit.index, SourceIndex: unit.sourceIndex, Error: errText})
		return
	}
	target.SuccessfulUnits++
	for category, score := range unit.result.CategoryScores {
		if current, ok := target.CategoryScores[category]; !ok || score > current {
			target.CategoryScores[category] = score
		}
	}
	flagged, _, _ := evaluateModerationScores(unit.result.CategoryScores, thresholds)
	if flagged && unit.unitType == ContentModerationItemTypeImage {
		target.FlaggedImageIndexes = append(target.FlaggedImageIndexes, unit.index)
	}
}

// publicContentModerationError maps usable wrapped provider errors to generic
// stored failure classes. A matching contentModerationAPIError pointer must be
// nonnil because its status is read after type matching; other errors,
// including nil, receive a generic request-failed message.
func publicContentModerationError(err error) string {
	if apiErr, ok := errors.AsType[*contentModerationAPIError](err); ok {
		switch {
		case apiErr.StatusCode == 429:
			return "moderation provider rate limited"
		case apiErr.StatusCode >= 500:
			return "moderation provider unavailable"
		case apiErr.StatusCode >= 400:
			return "moderation provider rejected request"
		}
	}
	return "moderation provider request failed"
}

// normalizeContentModerationChunkOptions returns a positive chunk size and
// overlap smaller than it. Nonpositive sizes use the audit default, negative
// overlap becomes zero, and excessive overlap clamps below size so every chunk
// advances.
//
// Both the chunk counter and iterator use these values, so they agree on the
// same positive step. An overlap at least as large as the normalized size
// becomes size minus one; changing only one consumer would make reported unit
// counts diverge from emitted chunks.
func normalizeContentModerationChunkOptions(chunkSize int, overlap int) (int, int) {
	if chunkSize <= 0 {
		chunkSize = maxModerationInputRunes
	}
	if overlap < 0 {
		overlap = 0
	}
	// Keep a positive read step for overlapping chunks.
	if overlap >= chunkSize {
		overlap = chunkSize - 1
	}
	return chunkSize, overlap
}

// countContentModerationTextChunks returns the number of overlapping rune
// chunks for nonblank aggregate text. Whitespace-only input yields zero. Size
// and overlap use the same normalization as the iterator, guaranteeing a
// positive step.
//
// The first chunk covers up to chunkSize runes; only text beyond it contributes
// additional steps. Rounding those remaining steps up counts a partial tail
// while an exact full final chunk creates no overlap-only extra unit. Batch
// construction and audit status use this count, so its arithmetic follows the
// iterator termination rule.
func countContentModerationTextChunks(text string, chunkSize int, overlap int) int {
	if strings.TrimSpace(text) == "" {
		return 0
	}
	chunkSize, overlap = normalizeContentModerationChunkOptions(chunkSize, overlap)
	runeCount := utf8.RuneCountInString(text)
	if runeCount <= chunkSize {
		return 1
	}
	step := chunkSize - overlap
	// Count the partial final step produced by the chunk iterator.
	return 1 + (runeCount-chunkSize+step-1)/step
}

// forEachContentModerationTextBatch visits ordered rune chunks with original
// chunk start indexes in bounded batches. Blank text or a nil visitor
// contributes nothing; nonpositive sizes and batch limits select defaults, and
// overlap clamps below the normalized chunk size.
//
// The iterator carries the previous tail into the next chunk while reading new
// runes from the string. An exact final full chunk ends iteration instead of
// generating an overlap-only tail. Each submitted batch owns its slice; the
// final partial batch is visited once, and text is reconstructed from read
// runes, including replacement runes for invalid UTF-8 bytes.
func forEachContentModerationTextBatch(text string, chunkSize int, overlap int, batchSize int, visit func(startIndex int, chunks []string)) {
	if strings.TrimSpace(text) == "" || visit == nil {
		return
	}
	chunkSize, overlap = normalizeContentModerationChunkOptions(chunkSize, overlap)
	if batchSize <= 0 {
		batchSize = contentModerationTextBatchSize
	}
	reader := strings.NewReader(text)
	carry := make([]rune, 0, overlap)
	batch := make([]string, 0, batchSize)
	batchStart := 0
	chunkIndex := 0
	for {
		chunkRunes := make([]rune, len(carry), chunkSize)
		copy(chunkRunes, carry)
		reachedEOF := false
		for len(chunkRunes) < chunkSize {
			r, _, err := reader.ReadRune()
			if errors.Is(err, io.EOF) {
				reachedEOF = true
				break
			}
			// strings.Reader reports EOF for exhaustion; if another read error reaches
			// this branch, retain the collected runes and end further chunking.
			if err != nil {
				reachedEOF = true
				break
			}
			chunkRunes = append(chunkRunes, r)
		}
		// A complete final chunk already contains the stream tail. Stop here so
		// carrying overlap would not create another unit containing repeated text alone.
		if len(chunkRunes) == chunkSize && reader.Len() == 0 {
			reachedEOF = true
		}
		if len(chunkRunes) == 0 {
			break
		}
		batch = append(batch, string(chunkRunes))
		chunkIndex++
		if len(batch) == batchSize {
			visit(batchStart, batch)
			batchStart = chunkIndex
			batch = make([]string, 0, batchSize)
		}
		if reachedEOF {
			break
		}
		// Retain the previous chunk tail before reading new runes, preserving the
		// configured shared overlap while the iterator avoids retaining
		// a full-input rune slice.
		carry = append(carry[:0], chunkRunes[len(chunkRunes)-overlap:]...)
	}
	if len(batch) > 0 {
		visit(batchStart, batch)
	}
}

// contentModerationHitMedia returns pending snapshots for distinct valid
// indexes into selected audit ImageItems. Invalid and repeated indexes are
// skipped, while output follows the first supplied index order. Each entry
// keeps the selected image reference, source label, and original review-item
// SourceIndex.
//
// checkSync passes the filtered audit input with the result indexes, because
// source exclusions can make selected image positions differ from full review
// positions. Indexes select the audit collection; SourceIndex carries the
// review identity into snapshot metadata. Using the full unfiltered collection
// here could attach a hit to another submitted image.
func contentModerationHitMedia(content ContentModerationInput, indexes []int) []ContentModerationMedia {
	media := make([]ContentModerationMedia, 0, len(indexes))
	seen := make(map[int]struct{}, len(indexes))
	for _, index := range indexes {
		if index < 0 || index >= len(content.ImageItems) {
			continue
		}
		if _, ok := seen[index]; ok {
			continue
		}
		seen[index] = struct{}{}
		image := content.ImageItems[index]
		media = append(media, ContentModerationMedia{SourceIndex: image.SourceIndex, Source: image.Source, OriginalRef: image.Reference, SnapshotStatus: "pending"})
	}
	return media
}

// recordPreBlockSyncMetric records one synchronous decision outcome and a
// nonnegative latency contribution. Block, hash-block, and keyword-block
// actions share the blocked counter; error uses the error counter and other
// actions use allowed, with separately sampled atomics.
func (s *ContentModerationService) recordPreBlockSyncMetric(latencyMS int, action string) {
	if s == nil {
		return
	}
	s.preBlockChecked.Add(1)
	// Clamp invalid observations before accumulating completed-check latency
	// milliseconds.
	if latencyMS < 0 {
		latencyMS = 0
	}
	s.preBlockLatencyTotalMS.Add(int64(latencyMS))
	switch action {
	case ContentModerationActionBlock, ContentModerationActionHashBlock, ContentModerationActionKeywordBlock:
		s.preBlockBlocked.Add(1)
	case ContentModerationActionError:
		s.preBlockErrors.Add(1)
	default:
		s.preBlockAllowed.Add(1)
	}
}

// enqueueAsync admits an observe task and reports whether the queue accepted
// it. It strips raw Body and compacts recoverable aggregate text, but retains
// the supplied content collections and identity pointers. Callers keep those
// references stable until the worker finishes; execution reloads current
// configuration rather than storing cfg in this task.
//
// Admission holds runtimeMu against queue closure, samples configured queue
// length, and reserves an estimated byte cost atomically before sending.
// Concurrent readers can exceed the configured length sample, while channel
// capacity and byte CAS remain enforced. A rejected send returns its byte
// reservation and increments dropped; stopped or unavailable queues simply
// reject.
func (s *ContentModerationService) enqueueAsync(input ContentModerationCheckInput, cfg *ContentModerationConfig, content ContentModerationInput, hashText string) bool {
	if s == nil || s.asyncQueue == nil {
		return false
	}
	// Hold admission against Stop channel closure through the nonblocking send;
	// a stopped runtime cannot close asyncQueue while this sender is still using it.
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	if s.runtimeStopped {
		return false
	}

	queueSize := defaultContentModerationQueueSize
	if cfg != nil && cfg.QueueSize > 0 {
		queueSize = cfg.QueueSize
	}
	if len(s.asyncQueue) >= queueSize {
		slog.Warn("content_moderation.async_queue_full", "user_id", input.UserID, "endpoint", input.Endpoint, "queue_size", queueSize)
		s.asyncDropped.Add(1)
		return false
	}
	input.Body = nil
	content = compactContentModerationInputForQueue(content)
	task := contentModerationTask{
		input:      input,
		content:    content,
		inputHash:  hashText,
		enqueuedAt: time.Now(),
	}
	task.bufferedBytes = estimateContentModerationTaskBytes(task)
	if !s.reserveContentModerationBufferedBytes(task.bufferedBytes) {
		slog.Warn("content_moderation.async_queue_bytes_full", "user_id", input.UserID, "endpoint", input.Endpoint, "task_bytes", task.bufferedBytes, "max_bytes", maxContentModerationBufferedBytes)
		s.asyncDropped.Add(1)
		return false
	}
	select {
	case s.asyncQueue <- task:
		s.asyncEnqueued.Add(1)
		return true
	default:
		// The channel rejected this reserved task; return its byte cost before
		// reporting rejection so later senders can claim that capacity again.
		s.releaseContentModerationBufferedBytes(task.bufferedBytes)
		slog.Warn("content_moderation.async_queue_full", "user_id", input.UserID, "endpoint", input.Endpoint)
		s.asyncDropped.Add(1)
		return false
	}
}

// enqueueRecord admits an already decided log for deferred persistence and
// reports queue acceptance. It strips Body and clones cfg, while log remains a
// shared mutable pointer that the caller stops changing after successful
// admission. The worker uses that saved policy even if administrator
// configuration changes before execution.
//
// The lifecycle read lock prevents closure during admission, and an estimated
// byte reservation precedes the nonblocking send. Concurrent senders use
// sampled configured length, with channel capacity as the hard queue cap.
// Rejection after reservation compensates the byte count; the decision caller
// then persists the same log synchronously.
func (s *ContentModerationService) enqueueRecord(input ContentModerationCheckInput, cfg *ContentModerationConfig, log *ContentModerationLog, inputHash string, recordHash bool, applySideEffects bool) bool {
	if s == nil || s.asyncQueue == nil || log == nil {
		return false
	}
	// Hold admission against Stop channel closure through the nonblocking send;
	// a stopped runtime cannot close asyncQueue while this sender is still using it.
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	if s.runtimeStopped {
		return false
	}

	queueSize := defaultContentModerationQueueSize
	if cfg != nil && cfg.QueueSize > 0 {
		queueSize = cfg.QueueSize
	}
	if len(s.asyncQueue) >= queueSize {
		slog.Warn("content_moderation.record_queue_full",
			"user_id", input.UserID,
			"endpoint", input.Endpoint,
			"action", log.Action,
			"queue_size", queueSize)
		s.asyncDropped.Add(1)
		return false
	}
	input.Body = nil
	task := contentModerationTask{
		input:            input,
		inputHash:        inputHash,
		log:              log,
		config:           cloneContentModerationConfig(cfg),
		recordHash:       recordHash,
		applySideEffects: applySideEffects,
		enqueuedAt:       time.Now(),
	}
	task.bufferedBytes = estimateContentModerationTaskBytes(task)
	if !s.reserveContentModerationBufferedBytes(task.bufferedBytes) {
		slog.Warn("content_moderation.record_queue_bytes_full", "user_id", input.UserID, "endpoint", input.Endpoint, "action", log.Action, "task_bytes", task.bufferedBytes, "max_bytes", maxContentModerationBufferedBytes)
		s.asyncDropped.Add(1)
		return false
	}
	select {
	case s.asyncQueue <- task:
		s.asyncEnqueued.Add(1)
		return true
	default:
		// The channel rejected this reserved task; return its byte cost before
		// reporting rejection so later senders can claim that capacity again.
		s.releaseContentModerationBufferedBytes(task.bufferedBytes)
		slog.Warn("content_moderation.record_queue_full",
			"user_id", input.UserID,
			"endpoint", input.Endpoint,
			"action", log.Action)
		s.asyncDropped.Add(1)
		return false
	}
}

// worker drains tasks when its fixed ID is admitted by current WorkerCount.
// Each loop loads a snapshot before dequeuing; a read failure or reduced limit
// waits and can leave tasks behind at shutdown. Record tasks use saved policy
// when present; observe tasks recheck current ordinary enablement, mode, group,
// and model scope. Check settles the risk master gate and sampling before queue
// admission, and those decisions remain when an observe task executes.
//
// Reservations stay held through task execution and are released on return.
// Policy skips precede activity and processed counters; accepted
// record/checkSync paths increment processed even when independent persistence
// fails. Task panics are logged and cleanup defers run. Stop joins workers
// while their background contexts retain their own deadlines, and missing
// usable policy can make draining incomplete.
func (s *ContentModerationService) worker(id int) {
	for {
		select {
		case <-s.runtimeDone:
			if len(s.asyncQueue) == 0 {
				return
			}
		default:
		}

		ctx, cancel := context.WithTimeout(context.Background(), maxContentModerationTimeoutMS*time.Millisecond+10*time.Second)
		runtimeSnapshot, err := s.loadRuntimeSnapshot(ctx)
		// This worker has no usable policy or is beyond the current admitted count;
		// wait before another load, and allow shutdown to report remaining queued tasks.
		if err != nil || runtimeSnapshot == nil || runtimeSnapshot.config == nil || id >= runtimeSnapshot.config.WorkerCount {
			cancel()
			select {
			case <-s.runtimeDone:
				return
			case <-time.After(time.Second):
			}
			continue
		}
		cfg := runtimeSnapshot.config
		task, ok := s.dequeueAsyncTask(ctx, time.Second)
		if !ok {
			cancel()
			continue
		}
		// Release task resources on every return. A recovered panic ends this task
		// and records diagnostics; independent side effects already
		// completed are not replayed here.
		func() {
			defer cancel()
			defer s.releaseContentModerationBufferedBytes(task.bufferedBytes)
			defer func() {
				if r := recover(); r != nil {
					slog.Error("content_moderation.worker_panic", "worker_id", id, "recover", r)
				}
			}()
			if task.log != nil {
				s.asyncActive.Add(1)
				defer s.asyncActive.Add(-1)
				// Queue delay is sampled at execution in truncated integer milliseconds.
				queueDelay := int(time.Since(task.enqueuedAt).Milliseconds())
				task.log.QueueDelayMS = &queueDelay
				taskCfg := task.config
				// A record admitted without saved policy uses the valid configuration
				// loaded by this worker loop, so its persistence can still evaluate
				// ordinary side effects.
				if taskCfg == nil {
					taskCfg = cfg
				}
				s.persistContentModerationLog(ctx, taskCfg, task.log, task.inputHash, task.recordHash, task.applySideEffects)
				s.asyncProcessed.Add(1)
				return
			}
			if !cfg.Enabled || cfg.Mode == ContentModerationModeOff {
				return
			}
			if !cfg.includesGroup(task.input.GroupID) {
				return
			}
			if !cfg.includesModel(task.input.Model) {
				return
			}
			task.content.Text = contentModerationTextFromItems(task.content.Items)
			s.asyncActive.Add(1)
			defer s.asyncActive.Add(-1)
			// Observe delay samples elapsed queue waiting in truncated integer milliseconds.
			queueDelay := int(time.Since(task.enqueuedAt).Milliseconds())
			_ = s.checkSync(ctx, task.input, cfg, task.content, task.inputHash, &queueDelay, false)
			s.asyncProcessed.Add(1)
		}()
	}
}

// compactContentModerationInputForQueue clears aggregate Text when text items
// can reconstruct it or it is already blank. Other collections remain shared; a
// standalone nonblank Text without any text item is retained in this returned
// value.
func compactContentModerationInputForQueue(content ContentModerationInput) ContentModerationInput {
	hasTextItem := false
	for _, item := range content.Items {
		if item.Type == ContentModerationItemTypeText {
			hasTextItem = true
			break
		}
	}
	if hasTextItem || strings.TrimSpace(content.Text) == "" {
		content.Text = ""
	}
	return content
}

// contentModerationTextFromItems joins nonblank text-type item values using
// newline separators. It preserves each retained value spelling and order,
// ignores image or blank units, and returns empty when no text item
// contributes.
func contentModerationTextFromItems(items []ContentModerationInputItem) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		if item.Type == ContentModerationItemTypeText && strings.TrimSpace(item.Text) != "" {
			parts = append(parts, item.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// estimateContentModerationTaskBytes approximates queue retention using
// selected strings, media bytes, and fixed structural allowances. Record tasks
// count their retained log material; audit tasks include item text twice for
// reconstruction, while other fields and standalone content.Text are not
// comprehensively measured.
func estimateContentModerationTaskBytes(task contentModerationTask) int64 {
	// Fixed structural allowances approximate selected retained costs,
	// rather than measuring
	// all object, slice-capacity, or shared-string heap storage for a queued task.
	size := int64(1024 + len(task.input.Endpoint) + len(task.input.Provider) + len(task.input.Model) + len(task.input.Body))
	if task.log != nil {
		size += int64(len(task.log.InputExcerpt) + len(task.log.Error) + len(task.log.MatchedKeyword))
		for _, item := range task.log.InputItems {
			size += int64(len(item.Text) + len(item.ImageRef) + 64)
		}
		for _, media := range task.log.Media {
			size += int64(len(media.OriginalRef) + len(media.Content) + 128)
		}
		return size
	}
	textBytes := int64(0)
	for _, item := range task.content.Items {
		textBytes += int64(len(item.Text))
		size += int64(len(item.Text) + len(item.ImageRef) + 64)
	}
	// Charge item text again for aggregate reconstruction during worker execution;
	// standalone content.Text and other omitted fields keep this a
	// selected-cost estimate.
	return size + textBytes
}

// reserveContentModerationBufferedBytes claims an estimated task cost under the
// shared byte cap. Nonpositive sizes require no reservation and return true;
// nil services or oversized positive claims return false.
//
// The current total and requested cost determine whether a claim fits. A lost
// CAS repeats the decision using the updated total, preventing competing
// senders from spending the same sampled remaining bytes. The sender releases a
// successful reservation if channel admission fails, and the worker releases it
// after admitted task completion.
func (s *ContentModerationService) reserveContentModerationBufferedBytes(size int64) bool {
	if s == nil || size <= 0 || size > maxContentModerationBufferedBytes {
		return size <= 0
	}
	for {
		// A competing sender can claim remaining bytes after this read; retry a
		// lost CAS against the updated total before accepting or
		// rejecting the reservation.
		current := s.asyncBufferedBytes.Load()
		if current > maxContentModerationBufferedBytes-size {
			return false
		}
		if s.asyncBufferedBytes.CompareAndSwap(current, current+size) {
			return true
		}
	}
}

// releaseContentModerationBufferedBytes subtracts a positive task reservation
// from the shared counter. Nil services and nonpositive sizes do nothing;
// callers release each successful reservation once, because this subtraction
// has no per-task idempotency guard.
func (s *ContentModerationService) releaseContentModerationBufferedBytes(size int64) {
	if s == nil || size <= 0 {
		return
	}
	s.asyncBufferedBytes.Add(-size)
}

// dequeueAsyncTask waits for a queue value, context cancellation, or idle
// timeout and reports whether a task was received. Nonpositive idleWait selects
// one second. When several cases are ready, select chooses among them; the
// caller owns byte release for a received task.
//
// A closed, empty queue returns no task; a buffered receive can still precede
// closure detection. The timer stops on return. Canceled or idle waits leave
// queued reservations intact, and this helper releases no costs for values it
// did not receive.
func (s *ContentModerationService) dequeueAsyncTask(ctx context.Context, idleWait time.Duration) (contentModerationTask, bool) {
	var zero contentModerationTask
	if s == nil || s.asyncQueue == nil {
		return zero, false
	}
	if idleWait <= 0 {
		idleWait = time.Second
	}
	timer := time.NewTimer(idleWait)
	defer timer.Stop()
	// A selected task reaches the worker with its byte reservation still held;
	// canceled or idle waits leave ownership of queued reservations with the
	// channel.
	select {
	case task, ok := <-s.asyncQueue:
		return task, ok
	case <-ctx.Done():
		return zero, false
	case <-timer.C:
		return zero, false
	}
}

// ListLogs delegates ordinary audit listing after normalizing page, size, and
// empty sort order. Defaults select page one and twenty rows, and page size
// caps at one hundred; the PostgreSQL adapter orders newest records first.
func (s *ContentModerationService) ListLogs(ctx context.Context, filter ContentModerationLogFilter) ([]ContentModerationLog, *pagination.PaginationResult, error) {
	if filter.Pagination.Page <= 0 {
		filter.Pagination.Page = 1
	}
	if filter.Pagination.PageSize <= 0 {
		filter.Pagination.PageSize = 20
	}
	// Bound administrator result pages before repository summary loading.
	if filter.Pagination.PageSize > 100 {
		filter.Pagination.PageSize = 100
	}
	if filter.Pagination.SortOrder == "" {
		filter.Pagination.SortOrder = pagination.SortOrderDesc
	}
	return s.repo.ListLogs(ctx, filter)
}

// GetLog loads full review detail for a positive record ID when the repository
// supports review reads. Invalid IDs and unsupported repositories return
// application errors; the bound missing-row classifier translates absent
// records to not found.
func (s *ContentModerationService) GetLog(ctx context.Context, id int64) (*ContentModerationLog, error) {
	if id <= 0 {
		return nil, infraerrors.BadRequest("INVALID_CONTENT_MODERATION_LOG_ID", "Invalid audit record ID")
	}
	repo, ok := s.repo.(ContentModerationReviewRepository)
	if !ok {
		return nil, infraerrors.InternalServer("CONTENT_MODERATION_REVIEW_UNAVAILABLE", "Full review material repository is unavailable")
	}
	item, err := repo.GetLog(ctx, id)
	if s.runtime.MissingRow != nil && s.runtime.MissingRow(err) {
		return nil, infraerrors.NotFound("CONTENT_MODERATION_LOG_NOT_FOUND", "Audit record not found")
	}
	return item, err
}

// RecordCyberWarning stores recognized upstream Cyber evidence after risk,
// warning-switch, group, and model gates. Disabled or unmatched input returns
// nil, nil; configuration and persistence failures return errors. Warning
// selection combines supplied text, extracted body text, and Cyber policy-code
// detection, retaining a usable message or the literal policy marker.
//
// Media snapshots are prepared before the repository atomically records warning
// and policy effects. Authentication invalidation and disabled-account mail
// follow a successful commit, so failures in those effects keep the stored
// warning and ban. This synchronous operation is outside the worker runtimeWG;
// app request and task owners handle its lifetime.
func (s *ContentModerationService) RecordCyberWarning(ctx context.Context, input ContentModerationCyberWarningInput) (*ContentModerationCyberWarning, error) {
	if s == nil || s.settingRepo == nil || s.repo == nil {
		return nil, nil
	}
	if !s.isRiskControlEnabled(ctx) {
		return nil, nil
	}
	cfg, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	if !cfg.CyberWarningEnabled {
		return nil, nil
	}
	if !cfg.cyberInputInScope(input, "content_moderation.cyber_warning") {
		return nil, nil
	}
	warningText := strings.TrimSpace(input.WarningText)
	bodyWarningText := ExtractCyberWarningText(input.ResponseBody)
	warningTextMatched := s.runtime.CyberText(warningText)
	bodyMatched := s.runtime.CyberText(bodyWarningText) || s.runtime.CyberText(string(input.ResponseBody))
	cyberPolicyMatched, _, cyberPolicyMessage := s.runtime.CyberPolicy(input.ResponseBody)
	if !warningTextMatched && !bodyMatched && !cyberPolicyMatched {
		return nil, nil
	}
	// A body-level match replaces unrecognized supplied text with its extracted
	// message. Empty chosen text then tries body text, policy message, and the
	// policy marker so recognized evidence receives a stored review label.
	if bodyMatched && !warningTextMatched {
		warningText = bodyWarningText
	}
	if strings.TrimSpace(warningText) == "" {
		warningText = bodyWarningText
	}
	if strings.TrimSpace(warningText) == "" && cyberPolicyMatched {
		warningText = strings.TrimSpace(cyberPolicyMessage)
	}
	if strings.TrimSpace(warningText) == "" && cyberPolicyMatched {
		warningText = "cyber_policy"
	}
	warning := s.buildCyberWarning(input, warningText)
	if len(warning.Media) > 0 {
		warning.Media = s.runtime.SnapshotMedia(ctx, warning.Media)
	}
	autoBanJustApplied, err := s.repo.CreateCyberWarningAndApplyUserBan(ctx, warning, ContentModerationCyberWarningPolicy{
		AutoBanEnabled: cfg.CyberAutoBanEnabled,
		BanThreshold:   cfg.CyberBanThreshold,
		WindowHours:    cfg.CyberWindowHours,
	})
	if err != nil {
		return nil, err
	}
	// The warning and policy transaction has committed; authentication and notice
	// failures now remain independent of its persisted evidence and account outcome.
	s.applyCyberWarningPostCommitSideEffects(ctx, cfg, warning, autoBanJustApplied)
	return warning, nil
}

// CyberWarningInScope reports whether risk control and configured group/model
// scope permit Cyber side effects. Missing services and a failed risk-switch
// read return false; configuration errors are returned, and this scope check
// does not require the ordinary audit enable or mode switches.
func (s *ContentModerationService) CyberWarningInScope(ctx context.Context, input ContentModerationCyberWarningInput) (bool, error) {
	if s == nil || s.settingRepo == nil {
		return false, nil
	}
	if !s.isRiskControlEnabled(ctx) {
		return false, nil
	}
	cfg, err := s.loadConfig(ctx)
	if err != nil {
		return false, err
	}
	return cfg.cyberInputInScope(input, "content_moderation.cyber_policy"), nil
}

// CyberSessionBlockGroupInScope reports risk-control group eligibility from the
// cached runtime snapshot. Missing services return false; initial snapshot
// errors propagate. It uses group scope alone, so model, ordinary audit mode,
// and Cyber warning enablement do not gate this helper.
func (s *ContentModerationService) CyberSessionBlockGroupInScope(ctx context.Context, groupID *int64) (bool, error) {
	if s == nil || s.settingRepo == nil {
		return false, nil
	}
	runtimeSnapshot, err := s.loadRuntimeSnapshot(ctx)
	if err != nil {
		return false, err
	}
	if runtimeSnapshot == nil || !runtimeSnapshot.riskControlEnabled || runtimeSnapshot.config == nil {
		return false, nil
	}
	return runtimeSnapshot.config.includesGroup(groupID), nil
}

// cyberInputInScope checks configured group and requested-model eligibility and
// logs rejected scope with logPrefix. A nil config permits input; blank
// prefixes use the default label. Ordinary Enabled, Mode, and sampling settings
// do not participate in this check.
func (cfg *ContentModerationConfig) cyberInputInScope(input ContentModerationCyberWarningInput, logPrefix string) bool {
	if cfg == nil {
		return true
	}
	if logPrefix == "" {
		logPrefix = "content_moderation.cyber_scope"
	}
	// Apply configured group and requested-model scope before Cyber warning
	// persistence, so excluded evidence does not contribute to retained warning
	// counts.
	if !cfg.includesGroup(input.GroupID) {
		slog.Info(logPrefix+"_skip_group_out_of_scope",
			"user_id", input.UserID,
			"api_key_id", input.APIKeyID,
			"group_id", contentModerationLogGroupID(input.GroupID),
			"group_name", input.GroupName,
			"endpoint", input.Endpoint,
			"model", input.Model,
			"all_groups", cfg.AllGroups,
			"configured_group_ids", cfg.GroupIDs)
		return false
	}
	if !cfg.includesModel(input.Model) {
		slog.Info(logPrefix+"_skip_model_out_of_scope",
			"user_id", input.UserID,
			"api_key_id", input.APIKeyID,
			"group_id", contentModerationLogGroupID(input.GroupID),
			"group_name", input.GroupName,
			"endpoint", input.Endpoint,
			"model", input.Model,
			"model_filter_type", cfg.ModelFilter.Type,
			"configured_models", cfg.ModelFilter.Models)
		return false
	}
	return true
}

// ListCyberWarnings normalizes pagination and delegates administrator warning
// listing to the repository. It defaults page and size, caps size at one
// hundred, and supplies descending order when absent; the PostgreSQL query uses
// newest-first ordering.
func (s *ContentModerationService) ListCyberWarnings(ctx context.Context, filter ContentModerationCyberWarningFilter) ([]ContentModerationCyberWarning, *pagination.PaginationResult, error) {
	if filter.Pagination.Page <= 0 {
		filter.Pagination.Page = 1
	}
	if filter.Pagination.PageSize <= 0 {
		filter.Pagination.PageSize = 20
	}
	// Bound administrator result pages before repository summary loading.
	if filter.Pagination.PageSize > 100 {
		filter.Pagination.PageSize = 100
	}
	if filter.Pagination.SortOrder == "" {
		filter.Pagination.SortOrder = pagination.SortOrderDesc
	}
	return s.repo.ListCyberWarnings(ctx, filter)
}

// GetCyberWarning loads full warning detail for a positive ID through the
// review repository capability. Invalid IDs or unsupported storage return
// application errors; the bound missing-row predicate translates wrapped
// absent-row errors to not found.
func (s *ContentModerationService) GetCyberWarning(ctx context.Context, id int64) (*ContentModerationCyberWarning, error) {
	if id <= 0 {
		return nil, infraerrors.BadRequest("INVALID_CONTENT_MODERATION_CYBER_WARNING_ID", "Invalid Cyber record ID")
	}
	repo, ok := s.repo.(ContentModerationReviewRepository)
	if !ok {
		return nil, infraerrors.InternalServer("CONTENT_MODERATION_REVIEW_UNAVAILABLE", "Full review material repository is unavailable")
	}
	item, err := repo.GetCyberWarning(ctx, id)
	if s.runtime.MissingRow != nil && s.runtime.MissingRow(err) {
		return nil, infraerrors.NotFound("CONTENT_MODERATION_CYBER_WARNING_NOT_FOUND", "Cyber record not found")
	}
	return item, err
}

// GetMediaContent returns a ready nonempty binary snapshot for administrator
// delivery. It validates positive ID and review capability, maps absent rows
// using MissingRow, and rejects stored error, pending, or empty-content entries
// as unavailable media.
func (s *ContentModerationService) GetMediaContent(ctx context.Context, id int64) (*ContentModerationMedia, error) {
	if id <= 0 {
		return nil, infraerrors.BadRequest("INVALID_CONTENT_MODERATION_MEDIA_ID", "Invalid media ID")
	}
	repo, ok := s.repo.(ContentModerationReviewRepository)
	if !ok {
		return nil, infraerrors.InternalServer("CONTENT_MODERATION_REVIEW_UNAVAILABLE", "Full review material repository is unavailable")
	}
	item, err := repo.GetMediaContent(ctx, id)
	if s.runtime.MissingRow != nil && s.runtime.MissingRow(err) {
		return nil, infraerrors.NotFound("CONTENT_MODERATION_MEDIA_NOT_FOUND", "Media not found")
	}
	if err != nil {
		return nil, err
	}
	if item.SnapshotStatus != "ready" || len(item.Content) == 0 {
		return nil, infraerrors.NotFound("CONTENT_MODERATION_MEDIA_NOT_READY", "Media snapshot is unavailable")
	}
	return item, nil
}

// GetCyberSummary delegates grouped warning observations with the supplied
// filter and context. Nil services or missing repositories return an empty
// summary; storage errors propagate when a repository is available.
func (s *ContentModerationService) GetCyberSummary(ctx context.Context, filter ContentModerationCyberWarningFilter) (*ContentModerationCyberSummary, error) {
	if s == nil || s.repo == nil {
		return &ContentModerationCyberSummary{}, nil
	}
	return s.repo.GetCyberSummary(ctx, filter)
}

// UnbanUser returns active status after resolving a positive user ID and
// applying that status when needed. It translates recognized missing users and
// returns lookup or status errors before successful response.
//
// Authentication invalidation follows a successful status command and also runs
// for an already-active user, refreshing cached eligibility after administrator
// action. Historical violation records and hashes remain available, so this
// operation does not reset those counting sources. Invalidation has no error
// result and no rollback relationship with the status write.
func (s *ContentModerationService) UnbanUser(ctx context.Context, userID int64) (*ContentModerationUnbanUserResult, error) {
	if s == nil || s.userRepo == nil {
		return nil, infraerrors.InternalServer("CONTENT_MODERATION_USER_REPOSITORY_UNAVAILABLE", "User repository is unavailable")
	}
	if userID <= 0 {
		return nil, infraerrors.BadRequest("INVALID_USER_ID", "Invalid user ID")
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		if s.runtime.MissingUser != nil && s.runtime.MissingUser(err) {
			return nil, infraerrors.NotFound("USER_NOT_FOUND", "User not found")
		}
		return nil, fmt.Errorf("get content moderation unban user: %w", err)
	}
	if user.Status != StatusActive {
		user.Status = StatusActive
		if err := s.userRepo.SetStatus(ctx, user.ID, user.Status); err != nil {
			return nil, fmt.Errorf("update content moderation unban user: %w", err)
		}
	}
	// Refresh authentication after successful reactivation, including an
	// already-active
	// account whose cached state may still describe an earlier disabled observation.
	if s.authCacheInvalidator != nil {
		s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, userID)
	}
	return &ContentModerationUnbanUserResult{
		UserID: userID,
		Status: StatusActive,
	}, nil
}

// DeleteFlaggedInputHash validates a complete hexadecimal SHA-256 member before
// cache deletion. It returns the normalized lowercase hash with the adapter
// removal observation, rejects missing cache capability, and wraps Redis
// failures for administrator response.
func (s *ContentModerationService) DeleteFlaggedInputHash(ctx context.Context, inputHash string) (*ContentModerationDeleteHashResult, error) {
	inputHash = normalizeContentModerationHash(inputHash)
	if inputHash == "" {
		return nil, infraerrors.BadRequest("INVALID_CONTENT_MODERATION_HASH", "Invalid risk input hash")
	}
	if s == nil || s.hashCache == nil {
		return nil, infraerrors.InternalServer("CONTENT_MODERATION_HASH_CACHE_UNAVAILABLE", "Content moderation hash cache is unavailable")
	}
	deleted, err := s.hashCache.DeleteFlaggedInputHash(ctx, inputHash)
	if err != nil {
		return nil, fmt.Errorf("delete content moderation flagged hash: %w", err)
	}
	return &ContentModerationDeleteHashResult{
		InputHash: inputHash,
		Deleted:   deleted,
	}, nil
}

// ClearFlaggedInputHashes requests shared-set clearing and returns the adapter
// count. Missing cache capability and command errors fail the administrator
// operation; production Redis samples cardinality before a separate delete, so
// the count is not an atomic removed-member total.
func (s *ContentModerationService) ClearFlaggedInputHashes(ctx context.Context) (*ContentModerationClearHashesResult, error) {
	if s == nil || s.hashCache == nil {
		return nil, infraerrors.InternalServer("CONTENT_MODERATION_HASH_CACHE_UNAVAILABLE", "Content moderation hash cache is unavailable")
	}
	deleted, err := s.hashCache.ClearFlaggedInputHashes(ctx)
	if err != nil {
		return nil, fmt.Errorf("clear content moderation flagged hashes: %w", err)
	}
	return &ContentModerationClearHashesResult{Deleted: deleted}, nil
}

// GetStatus returns administrator configuration and independently sampled
// runtime observations. A nil service returns empty, while configuration errors
// propagate. Worker activity is clamped to the configured limit, pre-block
// average uses observed completed checks, and queue usage divides sampled
// length by current configured size.
//
// The counters, key-health views, risk switch, and hash count are read at
// separate moments, so their combination is diagnostic rather than a consistent
// transaction snapshot. Hash-count failures log and report zero. Last-cleanup
// fields update separately and represent the latest successfully returned
// cleanup result.
func (s *ContentModerationService) GetStatus(ctx context.Context) (*ContentModerationRuntimeStatus, error) {
	if s == nil {
		return &ContentModerationRuntimeStatus{}, nil
	}
	cfg, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	riskEnabled := s.isRiskControlEnabled(ctx)
	// Clamp sampled work for administrator occupancy under the current worker
	// limit.
	active := min(max(int(s.asyncActive.Load()), 0), cfg.WorkerCount)
	preBlockActive := max(int(s.preBlockActive.Load()), 0)
	preBlockChecked := s.preBlockChecked.Load()
	preBlockAvgLatency := int64(0)
	// Integer division reports sampled completed-check mean latency in whole
	// milliseconds.
	if preBlockChecked > 0 {
		preBlockAvgLatency = s.preBlockLatencyTotalMS.Load() / preBlockChecked
	}
	queueLength := 0
	if s.asyncQueue != nil {
		queueLength = len(s.asyncQueue)
	}
	queueUsage := 0.0
	// Current configured size determines the diagnostic percentage of queued
	// tasks.
	if cfg.QueueSize > 0 {
		queueUsage = float64(queueLength) * 100 / float64(cfg.QueueSize)
	}
	var flaggedHashCount int64
	if s.hashCache != nil {
		// Hash availability is a separate diagnostic read; its failure leaves the
		// displayed count zero and does not discard the other sampled
		// runtime observations.
		if n, err := s.hashCache.CountFlaggedInputHashes(ctx); err == nil {
			flaggedHashCount = n
		} else {
			slog.Warn("content_moderation.hash_count_failed", "error", err)
		}
	}
	var lastCleanupAt *time.Time
	if unix := s.lastCleanupUnix.Load(); unix > 0 {
		// Convert stored cleanup seconds to an administrator timestamp.
		t := time.Unix(unix, 0)
		lastCleanupAt = &t
	}
	return &ContentModerationRuntimeStatus{
		Enabled:                      cfg.Enabled,
		RiskControlEnabled:           riskEnabled,
		Mode:                         cfg.Mode,
		WorkerCount:                  cfg.WorkerCount,
		MaxWorkers:                   maxContentModerationWorkerCount,
		ActiveWorkers:                active,
		IdleWorkers:                  cfg.WorkerCount - active,
		QueueSize:                    cfg.QueueSize,
		QueueLength:                  queueLength,
		QueueUsagePercent:            queueUsage,
		Enqueued:                     s.asyncEnqueued.Load(),
		Dropped:                      s.asyncDropped.Load(),
		Processed:                    s.asyncProcessed.Load(),
		Errors:                       s.asyncErrors.Load(),
		PreBlockActive:               preBlockActive,
		PreBlockChecked:              preBlockChecked,
		PreBlockAllowed:              s.preBlockAllowed.Load(),
		PreBlockBlocked:              s.preBlockBlocked.Load(),
		PreBlockErrors:               s.preBlockErrors.Load(),
		PreBlockAvgLatencyMS:         preBlockAvgLatency,
		PreBlockAPIKeyActive:         s.preBlockAPIKeyActive(cfg),
		PreBlockAPIKeyAvailableCount: s.preBlockAPIKeyAvailableCount(cfg),
		PreBlockAPIKeyTotalCalls:     s.preBlockAPIKeyTotalCalls(cfg),
		PreBlockAPIKeyLoads:          s.preBlockAPIKeyLoads(cfg),
		APIKeyStatuses:               s.apiKeyStatuses(cfg),
		FlaggedHashCount:             flaggedHashCount,
		LastCleanupAt:                lastCleanupAt,
		LastCleanupDeletedHit:        s.lastCleanupDeletedHit.Load(),
		LastCleanupDeletedNonHit:     s.lastCleanupDeletedNonHit.Load(),
	}, nil
}

// cleanupWorker waits five minutes before its first retention cleanup, then
// repeats every day. runtimeDone stops future rounds; a cleanup already
// underway uses its own background deadline and remains joined by runtimeWG
// before Stop finishes.
//
// The timer resets after each completed round, so work duration extends the
// interval between round starts. When timer and stop are ready together,
// select can admit another round before the next loop observes shutdown.
func (s *ContentModerationService) cleanupWorker() {
	timer := time.NewTimer(contentModerationCleanupDelay)
	defer timer.Stop()
	// Shutdown competes with each timer before another round begins; an admitted
	// round keeps its separate background budget and finishes before this worker
	// can observe shutdown again.
	for {
		select {
		case <-s.runtimeDone:
			return
		case <-timer.C:
		}
		s.runCleanupOnce()
		timer.Reset(contentModerationCleanupInterval)
	}
}

// runCleanupOnce reads policy and requests retention deletion with a
// thirty-minute background context. Missing dependencies skip the round; read
// or cleanup errors log and leave the last-success observations unchanged.
// PostgreSQL cleanup can retain earlier successful deletions before a later
// failure. Flagged hashes belong to the separate Redis set and survive this
// log and warning cleanup.
func (s *ContentModerationService) runCleanupOnce() {
	if s == nil || s.repo == nil || s.settingRepo == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), contentModerationCleanupTimeout)
	defer cancel()
	cfg, err := s.loadConfig(ctx)
	// Skip deletion after a policy read failure because this round has no current
	// retention cutoffs. The last successful cleanup observations remain visible
	// until a later round completes.
	if err != nil {
		slog.Warn("content_moderation.cleanup_load_config_failed", "error", err)
		return
	}
	now := time.Now()
	// Compute both cutoffs from one local calendar instant, preserving calendar
	// days across timezone offset changes.
	hitBefore := now.AddDate(0, 0, -cfg.HitRetentionDays)
	nonHitBefore := now.AddDate(0, 0, -cfg.NonHitRetentionDays)
	result, err := s.repo.CleanupExpiredLogs(ctx, hitBefore, nonHitBefore)
	// Keep the previous success display after a failed round; the PostgreSQL
	// adapter can have applied earlier deletes before a later command reports
	// failure.
	if err != nil {
		slog.Warn("content_moderation.cleanup_failed", "error", err)
		return
	}
	if result == nil {
		return
	}
	// Publish successful cleanup time as Unix seconds and its reported counts
	// through separate stores. A status reader can sample between them, so this
	// display describes successful work at potentially different observation times.
	s.lastCleanupUnix.Store(result.FinishedAt.Unix())
	s.lastCleanupDeletedHit.Store(result.DeletedHit)
	s.lastCleanupDeletedNonHit.Store(result.DeletedNonHit)
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
// retaining the previous published snapshot. A deferred retry time or occupied
// refresh lock skips admission. The callback uses a five-second background
// context and extends retry deferral by a cache TTL after failure.
//
// The acquired mutex is released by the launched callback, so
// Runtime.Background must arrange its execution. App discards
// lifecycle.Tasks.Go admission failure; a trigger after that tracker has sealed
// can leave the mutex held. Request producers therefore finish before final
// task shutdown, and this service method has no independent launcher rejection
// result.
func (s *ContentModerationService) triggerRuntimeSnapshotRefresh() {
	if s == nil || s.runtimeRefreshDeferred() || !s.runtimeRefreshMu.TryLock() {
		return
	}
	if s.runtimeRefreshDeferred() {
		s.runtimeRefreshMu.Unlock()
		return
	}
	// Transfer refresh-lock release to the callback; Background must execute this
	// work while existing producers still have access to the app task owner.
	s.runtime.Background("service/content_moderation.go:triggerRuntimeSnapshotRefresh", func() {
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

// callModeration returns the first result from a normal key-selected audit
// attempt. It forwards the optional first load-tracking flag to
// callModerationBatch and returns errors or an empty-result failure before
// exposing a result pointer.
func (s *ContentModerationService) callModeration(ctx context.Context, cfg *ContentModerationConfig, input any, trackKeyLoad ...bool) (*moderationAPIResult, error) {
	results, err := s.callModerationBatch(ctx, cfg, input, trackKeyLoad...)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, errors.New("moderation api returned empty results")
	}
	return &results[0], nil
}

// callModerationBatch chooses an unfrozen weighted key for each attempt and
// returns the first successful result set. RetryCount adds attempts after the
// initial call, clamped to the supported limit. HTTP 400 ends attempts
// immediately; other failures update health and wait a linearly increasing
// delay before selecting again.
//
// Optional load tracking records each synchronous attempt independently from
// key health. Cancellation during the retry wait returns ctx.Err, while
// exhausted attempts return the last recorded failure. Keys can freeze between
// attempts, so unavailable selection can replace an earlier HTTP failure with
// the no-key result. This method does not validate positional result count;
// auditTextBatch owns that association.
func (s *ContentModerationService) callModerationBatch(ctx context.Context, cfg *ContentModerationConfig, input any, trackKeyLoad ...bool) ([]moderationAPIResult, error) {
	attempts := cfg.RetryCount + 1
	if attempts <= 0 {
		attempts = 1
	}
	// Clamp total attempts, including the initial provider exchange.
	if attempts > maxContentModerationRetryCount+1 {
		attempts = maxContentModerationRetryCount + 1
	}
	trackLoad := len(trackKeyLoad) > 0 && trackKeyLoad[0]
	var lastErr error
	for attempt := range attempts {
		key, ok := s.nextUsableAPIKey(cfg)
		if !ok {
			lastErr = errors.New("no moderation api key available")
			break
		}
		if trackLoad {
			s.beginModerationAPIKeyCall(key)
		}
		start := time.Now()
		httpStatus := 0
		result, err := s.callModerationOnceWithInput(ctx, cfg, key, input, &httpStatus)
		// Truncate elapsed duration to the integer milliseconds used by key health.
		latency := int(time.Since(start).Milliseconds())
		if err == nil {
			if trackLoad {
				s.finishModerationAPIKeyCall(key, latency, true)
			}
			s.markAPIKeySuccess(key, latency, httpStatus)
			return result, nil
		}
		if trackLoad {
			s.finishModerationAPIKeyCall(key, latency, false)
		}
		s.markAPIKeyError(key, err.Error(), latency, httpStatus)
		lastErr = err
		// A request-shape rejection is retained for batch compatibility handling;
		// retrying other credentials here would resend the same invalid
		// shape immediately.
		if httpStatus == 400 {
			break
		}
		if attempt == attempts-1 {
			break
		}
		// Delay subsequent attempts while still selecting their key afresh; the
		// shared caller context can end this wait before another
		// provider exchange begins.
		wait := time.Duration(100*(attempt+1)) * time.Millisecond
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
	return nil, lastErr
}

// callModerationOnceWithInput serializes one audit request and returns a
// nonempty provider result set or an error. It joins the configured base with
// /v1/moderations and applies TimeoutMS to the HTTP request context. Proxy
// lookup captures the supplied parent context, so its storage resolution is not
// shortened by that child HTTP timeout.
//
// Received status is written to httpStatus when supplied, including exchange or
// decode errors. Non-2xx bodies become diagnostic contentModerationAPIErrors;
// successful replies with no result are rejected. The caller owns retry, health
// updates, and matching returned results to its input units.
func (s *ContentModerationService) callModerationOnceWithInput(ctx context.Context, cfg *ContentModerationConfig, key string, input any, httpStatus *int) ([]moderationAPIResult, error) {
	base := strings.TrimRight(cfg.BaseURL, "/")
	endpoint, err := url.JoinPath(base, "/v1/moderations")
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(moderationAPIRequest{Model: cfg.Model, Input: input})
	if err != nil {
		return nil, err
	}
	// Interpret TimeoutMS as this HTTP child budget; proxy resolution retains
	// the parent context so that storage lookup can outlast this child deadline.
	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutMS)*time.Millisecond)
	defer cancel()
	var out moderationAPIResponse
	status, body, err := s.runtime.Audit.Execute(reqCtx, endpoint, key, raw, func() (string, error) {
		if cfg.ProxyID == nil {
			return "", nil
		}
		return s.resolveModerationProxyURL(ctx, *cfg.ProxyID)
	}, &out)
	if httpStatus != nil {
		*httpStatus = status
	}
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, &contentModerationAPIError{StatusCode: status, Message: strings.TrimSpace(string(body))}
	}
	if len(out.Results) == 0 {
		return nil, errors.New("moderation api returned empty results")
	}
	return out.Results, nil
}

// resolveModerationProxyURL returns the selected stored proxy URL, reusing its
// matching unexpired cache entry. Missing lookup capability or lookup errors
// stop the audit attempt. Inactive or expired records generate diagnostics and
// still provide their URL; this method does not replace them with a direct
// connection.
//
// Successful resolutions publish one ID/URL entry expiring one minute after
// lookup starts. Lookups are not coalesced, so concurrent misses can each read
// and publish. Cache clearing during configuration or repository changes cannot
// prevent an already running lookup from publishing afterward, and callers
// bind repository ownership before concurrent requests begin.
func (s *ContentModerationService) resolveModerationProxyURL(ctx context.Context, proxyID int64) (string, error) {
	now := time.Now()
	previous := s.moderationProxyCache.Load()
	if previous != nil && previous.proxyID == proxyID && now.Before(previous.expiresAt) {
		return previous.url, nil
	}
	if s.proxyRepo == nil {
		return "", errors.New("moderation proxy repository unavailable")
	}
	proxy, err := s.proxyRepo.Lookup(ctx, proxyID, now)
	if err != nil {
		return "", fmt.Errorf("resolve moderation proxy %d: %w", proxyID, err)
	}
	if !proxy.Active || proxy.Expired {
		slog.Warn("content_moderation.proxy_not_active",
			"proxy_id", proxyID,
			"proxy_name", proxy.Name,
			"status", proxy.Status,
			"expired", proxy.Expired)
	}
	proxyURL := proxy.URL
	if previous == nil || previous.proxyID != proxyID || previous.url != proxyURL {
		// Log the adapter-provided address label because the dial URL can include
		// authentication; the display label is constructed separately
		// by app proxy lookup.
		slog.Info("content_moderation.proxy_enabled",
			"proxy_id", proxyID,
			"proxy_name", proxy.Name,
			"proxy_addr", proxy.Address)
	}
	// Publish ID, URL, and expiry together so a hit matches one resolved
	// selection; lookup latency consumes the minute measured from its start.
	s.moderationProxyCache.Store(&moderationProxyURLCacheEntry{
		proxyID:   proxyID,
		url:       proxyURL,
		expiresAt: now.Add(contentModerationProxyURLCacheTTL),
	})
	return proxyURL, nil
}

// buildStructuredLog constructs an ordinary observation with separate actor,
// billing, team, and key identity. It copies score maps, threshold maps,
// item/failure slices, and group/team ID values; latency and queue-delay
// pointers remain supplied references and stay stable until persistence reads
// them.
//
// NoMediaRetention removes excerpt, input items, and media while marking
// content incomplete. Otherwise the summary excerpt is redacted and
// rune-limited, but full item values remain for authorized review. A supplied
// audit result replaces unit counts and completion flags, keeping failed-unit
// evidence distinct from any successful score hit. The input hash is handled by
// the hash cache, not a log field.
func (s *ContentModerationService) buildStructuredLog(input ContentModerationCheckInput, cfg *ContentModerationConfig, action string, flagged bool, highestCategory string, highestScore float64, scores map[string]float64, content ContentModerationInput, latency *int, queueDelay *int, errText string, audit *contentModerationAuditResult) *ContentModerationLog {
	var userID *int64
	if input.UserID > 0 {
		userID = &input.UserID
	}
	var billingUserID *int64
	if input.BillingUserID > 0 {
		billingUserID = &input.BillingUserID
	}
	var apiKeyID *int64
	if input.APIKeyID > 0 {
		apiKeyID = &input.APIKeyID
	}
	log := &ContentModerationLog{
		RequestID:         input.RequestID,
		UserID:            userID,
		UserEmail:         input.UserEmail,
		BillingUserID:     billingUserID,
		TeamID:            cloneInt64Ptr(input.TeamID),
		APIKeyID:          apiKeyID,
		APIKeyName:        input.APIKeyName,
		GroupID:           cloneInt64Ptr(input.GroupID),
		GroupName:         input.GroupName,
		Endpoint:          input.Endpoint,
		Provider:          input.Provider,
		Model:             input.Model,
		Mode:              cfg.Mode,
		Action:            action,
		Flagged:           flagged,
		HighestCategory:   highestCategory,
		HighestScore:      highestScore,
		CategoryScores:    cloneFloatMap(scores),
		ThresholdSnapshot: cloneFloatMap(cfg.Thresholds),
		Source:            content.Source,
		ContentComplete:   true,
		AuditComplete:     true,
		TextUnitCount:     countContentModerationTextChunks(content.Text, maxModerationInputRunes, contentModerationChunkOverlap),
		ImageUnitCount:    len(content.ImageItems),
		UpstreamLatencyMS: latency,
		QueueDelayMS:      queueDelay,
		Error:             errText,
	}
	if input.NoMediaRetention {
		// Remove retained content while leaving audit counts and scores for
		// administrator review. Later persistence can request a digest write through
		// the separate hash adapter.
		log.InputExcerpt = ""
		log.InputItems = nil
		log.ContentComplete = false
		log.Media = nil
	} else {
		log.InputExcerpt = sanitizeContentModerationExcerpt(content.Text, maxModerationExcerptRunes)
		log.InputItems = append([]ContentModerationInputItem(nil), content.Items...)
	}
	if audit != nil {
		log.AuditComplete = audit.AuditComplete
		log.TextUnitCount = audit.TextUnitCount
		log.ImageUnitCount = audit.ImageUnitCount
		log.FailedUnits = append([]ContentModerationFailedUnit(nil), audit.FailedUnits...)
		log.FailedUnitCount = len(log.FailedUnits)
	}
	return log
}

// persistContentModerationLog attempts hash recording, ordinary risk actions,
// notification, media snapshots, and final log insertion in that order. Nil
// services or logs do nothing. These operations are separate: a later failure
// does not undo a completed hash write, user status command, or notice
// callback. A nil notice result permits email markers without confirming a
// new transport send.
//
// Ordinary side effects count earlier persisted hits before this new log is
// inserted. Notification therefore sees its current ID, commonly zero for a
// newly built log, and source-key coordination is absent for that identity.
// Image retrieval failures become snapshot status/error values in the attempted
// log, while log insertion errors emit diagnostics. The method has no error
// result, and the request decision selected by Check remains effective.
func (s *ContentModerationService) persistContentModerationLog(ctx context.Context, cfg *ContentModerationConfig, log *ContentModerationLog, hashText string, recordHash bool, applySideEffects bool) {
	if s == nil || log == nil {
		return
	}
	// Hash storage precedes ordinary actions and final log insertion. Its errors
	// are logged while already selected admission and later independent
	// effects continue.
	if recordHash && s.hashCache != nil {
		if err := s.hashCache.RecordFlaggedInputHash(ctx, hashText); err != nil {
			slog.Warn("content_moderation.record_hash_failed", "user_id", contentModerationEmailUserID(log), "endpoint", log.Endpoint, "error", err)
		}
	}
	// Count and change account status before notices, then snapshot and insert the
	// log; later failures cannot roll these separately completed effects back.
	autoBanJustApplied := false
	if applySideEffects {
		autoBanJustApplied = s.applyFlaggedProviderSideEffects(ctx, cfg, log)
		s.sendFlaggedNotificationSideEffects(ctx, cfg, log, autoBanJustApplied)
	}
	if len(log.Media) > 0 {
		log.Media = s.runtime.SnapshotMedia(ctx, log.Media)
	}
	if s.repo != nil {
		if err := s.repo.CreateLog(ctx, log); err != nil {
			slog.Warn("content_moderation.create_log_failed", "user_id", contentModerationEmailUserID(log), "endpoint", log.Endpoint, "action", log.Action, "error", err)
			return
		}
	}
}

// applyFlaggedProviderSideEffects updates ordinary violation metadata and
// optionally disables the actor account. Missing flagged actor data returns
// false. The count is earlier qualifying logs in the configured window plus
// this observation; count read failure keeps one, and counting is separate from
// status change and later insertion.
//
// A threshold-reaching admin account is skipped. Successful disablement
// triggers authentication invalidation and returns true for a newly applied
// ban; an already-disabled eligible user marks AutoBanned but returns false.
// Concurrent ordinary hits can observe different persisted histories, and
// lookup or status failure returns false after diagnostics without a rollback
// of earlier independent work.
func (s *ContentModerationService) applyFlaggedProviderSideEffects(ctx context.Context, cfg *ContentModerationConfig, log *ContentModerationLog) bool {
	if s == nil || cfg == nil || log == nil || !log.Flagged || log.UserID == nil || *log.UserID <= 0 {
		return false
	}
	count := 1
	if s.repo != nil && cfg.ViolationWindowHours > 0 {
		// Convert configured hours to the duration used for this actor-count cutoff.
		since := time.Now().Add(-time.Duration(cfg.ViolationWindowHours) * time.Hour)
		// Include this yet-unwritten hit after the stored history read; a count error
		// keeps one, so it does not imply the actor has no older qualifying violations.
		if n, err := s.repo.CountFlaggedByUserSince(ctx, *log.UserID, since); err == nil {
			count = n + 1
		}
	}
	log.ViolationCount = count
	autoBanJustApplied := false
	if cfg.AutoBanEnabled && cfg.BanThreshold > 0 && count >= cfg.BanThreshold && s.userRepo != nil {
		user, err := s.userRepo.GetByID(ctx, *log.UserID)
		if err != nil {
			slog.Warn("content_moderation.ban_get_user_failed", "user_id", *log.UserID, "error", err)
			return false
		}
		if user.Role == "admin" {
			slog.Warn("content_moderation.autoban_skipped_admin", "user_id", *log.UserID, "role", user.Role, "count", count, "threshold", cfg.BanThreshold)
			// UserCommands supports account status writes and has no API-key disable
			// operation. This administrator exemption therefore leaves the counted
			// observation without an account or credential disable attempt.
			return false
		}
		if user.Status != StatusDisabled {
			user.Status = StatusDisabled
			if err := s.userRepo.SetStatus(ctx, user.ID, user.Status); err != nil {
				slog.Warn("content_moderation.ban_update_user_failed", "user_id", *log.UserID, "error", err)
				return false
			}
			// Invalidate after status persistence succeeds so subsequent authentication
			// can observe disablement; this side effect has no rollback or error return.
			if s.authCacheInvalidator != nil {
				s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, *log.UserID)
			}
			autoBanJustApplied = true
		}
		log.AutoBanned = true
	}
	return autoBanJustApplied
}

// sendFlaggedNotificationSideEffects attempts an enabled hit notice and a newly
// applied ordinary-ban notice. A usable email service, recipient, and flagged
// log are required; error diagnostics dereference UserID, so callers supply a
// valid actor pointer for delivery paths.
//
// Each nil callback result contributes to EmailSent, while either error is
// logged and does not prevent attempting the other notice. A notification
// suppression result can also be nil. This function precedes CreateLog in
// ordinary persistence, so the source ID can still be zero. Completed account
// changes remain applied when a notice returns an error.
func (s *ContentModerationService) sendFlaggedNotificationSideEffects(ctx context.Context, cfg *ContentModerationConfig, log *ContentModerationLog, autoBanJustApplied bool) {
	if s == nil || cfg == nil || log == nil || !log.Flagged {
		return
	}
	if s.emailService == nil || strings.TrimSpace(log.UserEmail) == "" {
		return
	}
	// Keep one success flag across independent sends; a failed later ban notice
	// must not erase the earlier violation callback's nil result.
	emailSent := false
	if cfg.EmailOnHit {
		if err := s.sendViolationEmail(ctx, cfg, log); err != nil {
			slog.Warn("content_moderation.email_failed", "user_id", *log.UserID, "email", log.UserEmail, "error", err)
		} else {
			emailSent = true
		}
	}
	if autoBanJustApplied {
		if err := s.sendAccountDisabledEmail(ctx, cfg, log); err != nil {
			slog.Warn("content_moderation.ban_email_failed", "user_id", *log.UserID, "email", log.UserEmail, "error", err)
		} else {
			emailSent = true
		}
	}
	log.EmailSent = emailSent
}

// buildCyberWarning constructs stored Cyber evidence with actor and billing
// identities kept separate. Positive scalar IDs become optional pointers, group
// and team values are copied, and strings are trimmed for metadata. Full
// structured input takes precedence over PromptExcerpt when it contains
// aggregate text; an empty content value can be rebuilt from that excerpt.
//
// The warning text and short prompt excerpt remain raw review text with rune
// caps. Every normalized image item becomes pending snapshot metadata, while
// the item slice is copied for review. This builder does not apply ordinary
// NoMediaRetention controls; callers choose what Cyber content they supply for
// retention.
func (s *ContentModerationService) buildCyberWarning(input ContentModerationCyberWarningInput, warningText string) *ContentModerationCyberWarning {
	var userID *int64
	if input.UserID > 0 {
		userID = &input.UserID
	}
	var billingUserID *int64
	if input.BillingUserID > 0 {
		billingUserID = &input.BillingUserID
	}
	var apiKeyID *int64
	if input.APIKeyID > 0 {
		apiKeyID = &input.APIKeyID
	}
	var providerID *int64
	if input.ProviderID > 0 {
		providerID = &input.ProviderID
	}
	content := input.Content
	// Retain excerpt text as synthesized input when the content aggregate is
	// empty, so normalization can still give the reviewer positioned units for
	// that supplied text.
	if content.IsEmpty() && strings.TrimSpace(input.PromptExcerpt) != "" {
		content = ContentModerationInput{Text: input.PromptExcerpt}
	}
	content.Normalize()
	promptExcerpt := input.PromptExcerpt
	if strings.TrimSpace(content.Text) != "" {
		promptExcerpt = ExtractContentModerationPromptExcerptFromInput(content)
	}
	media := make([]ContentModerationMedia, 0, len(content.ImageItems))
	for _, image := range content.ImageItems {
		media = append(media, ContentModerationMedia{SourceIndex: image.SourceIndex, Source: image.Source, OriginalRef: image.Reference, SnapshotStatus: "pending"})
	}
	return &ContentModerationCyberWarning{
		RequestID:      strings.TrimSpace(input.RequestID),
		UserID:         userID,
		UserEmail:      strings.TrimSpace(input.UserEmail),
		BillingUserID:  billingUserID,
		TeamID:         cloneInt64Ptr(input.TeamID),
		APIKeyID:       apiKeyID,
		APIKeyName:     strings.TrimSpace(input.APIKeyName),
		GroupID:        cloneInt64Ptr(input.GroupID),
		GroupName:      strings.TrimSpace(input.GroupName),
		ProviderID:     providerID,
		ProviderName:   strings.TrimSpace(input.ProviderName),
		Endpoint:       strings.TrimSpace(input.Endpoint),
		Model:          strings.TrimSpace(input.Model),
		UpstreamStatus: input.UpstreamStatus,
		// Cap both raw review strings at one thousand runes, keeping recognized
		// secret text available through the administrator warning detail.
		WarningText:     trimRawContentModerationText(warningText, 1000),
		PromptExcerpt:   trimRawContentModerationText(promptExcerpt, maxCyberWarningPromptExcerptRunes),
		Source:          content.Source,
		InputItems:      append([]ContentModerationInputItem(nil), content.Items...),
		ContentComplete: len(content.Items) > 0,
		AuditComplete:   true,
		TextUnitCount:   countContentModerationTextChunks(content.Text, maxModerationInputRunes, contentModerationChunkOverlap),
		ImageUnitCount:  len(content.ImageItems),
		Media:           media,
	}
}

// applyCyberWarningPostCommitSideEffects handles authentication invalidation
// and email for a newly committed Cyber ban. Missing actor data or a false
// new-ban result skips these effects. A missing email service or recipient
// still leaves invalidation applied.
//
// A nil notice result marks the in-memory warning and then attempts the stored
// flag for a positive ID. Notification can return nil for an existing delivery
// marker, so this does not establish a new SMTP send. A notice error prevents
// the flag attempt; marker-write failure is logged after the callback finishes.
// Warning and account changes stay committed, and the stored warning ID
// supplies notification source coordination.
func (s *ContentModerationService) applyCyberWarningPostCommitSideEffects(ctx context.Context, cfg *ContentModerationConfig, warning *ContentModerationCyberWarning, autoBanJustApplied bool) {
	if s == nil || cfg == nil || warning == nil || warning.UserID == nil || *warning.UserID <= 0 || !autoBanJustApplied {
		return
	}
	// Invalidate after committed disablement and before recipient checks, so
	// missing email delivery capability still permits later authentication to
	// observe the account action.
	if s.authCacheInvalidator != nil {
		s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, *warning.UserID)
	}
	if s.emailService == nil || strings.TrimSpace(warning.UserEmail) == "" {
		return
	}
	// Call against stored source identity before marking its nil result. If a
	// transport send occurred, later marker failure cannot undo it or the
	// committed Cyber account action.
	if err := s.sendCyberAccountDisabledEmail(ctx, cfg, warning); err != nil {
		slog.Warn("content_moderation.cyber_ban_email_failed", "user_id", *warning.UserID, "email", warning.UserEmail, "error", err)
		return
	}
	warning.EmailSent = true
	if s.repo != nil && warning.ID > 0 {
		if err := s.repo.MarkCyberWarningEmailSent(ctx, warning.ID); err != nil {
			slog.Warn("content_moderation.cyber_ban_email_mark_failed", "warning_id", warning.ID, "user_id", *warning.UserID, "error", err)
		}
	}
}

// contentModerationEmailUserID returns the pointed actor identifier, or zero
// when the log or pointer is absent. Diagnostic callers can report missing
// actor context without dereferencing a nil pointer; present values pass
// through even when nonpositive.
func contentModerationEmailUserID(log *ContentModerationLog) int64 {
	if log == nil || log.UserID == nil {
		return 0
	}
	return *log.UserID
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

// contentModerationLogGroupID returns the selected group value, or zero when
// its pointer is absent. Diagnostic callers use that zero for missing group
// context; supplied values pass through without a membership or positivity
// check.
func contentModerationLogGroupID(groupID *int64) int64 {
	if groupID == nil {
		return 0
	}
	return *groupID
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

// buildModerationTestInput creates scalar text or text/image API parts for
// administrator probes. It trims and rune-caps the prompt, accepts at most
// eight nonblank validated inline references, and returns hello when neither
// text nor images contributes content.
func buildModerationTestInput(prompt string, images []string) (any, int, error) {
	// Cap probe text to the size of one production audit chunk.
	prompt = trimRunes(normalizeContentModerationText(prompt), maxModerationInputRunes)
	normalizedImages := make([]string, 0, len(images))
	for _, image := range images {
		image = strings.TrimSpace(image)
		if image == "" {
			continue
		}
		// Reject later probe images beyond the supported per-request count.
		if len(normalizedImages) >= maxContentModerationTestImages {
			return nil, 0, infraerrors.BadRequest("TOO_MANY_MODERATION_TEST_IMAGES", fmt.Sprintf("At most %d test images can be uploaded", maxContentModerationTestImages))
		}
		if err := validateModerationTestImageDataURL(image); err != nil {
			return nil, 0, err
		}
		normalizedImages = append(normalizedImages, image)
	}
	// With no audit content, use a harmless fixed probe to observe credential
	// health; TestAPIKeys treats this intent differently from a supplied
	// content audit.
	if prompt == "" && len(normalizedImages) == 0 {
		return "hello", 0, nil
	}
	if len(normalizedImages) == 0 {
		return prompt, 0, nil
	}
	parts := make([]moderationAPIInputPart, 0, len(normalizedImages)+1)
	if prompt != "" {
		parts = append(parts, moderationAPIInputPart{Type: "text", Text: prompt})
	}
	for _, image := range normalizedImages {
		parts = append(parts, moderationAPIInputPart{
			Type:     "image_url",
			ImageURL: &moderationAPIImageURLRef{URL: image},
		})
	}
	return parts, len(normalizedImages), nil
}

// contentModerationTestHasAuditInput reports whether trimmed prompt or any
// nonblank image string represents administrator audit intent. It does not
// validate image format; buildModerationTestInput performs that validation
// before the selected key is called.
func contentModerationTestHasAuditInput(prompt string, images []string) bool {
	if normalizeContentModerationText(prompt) != "" {
		return true
	}
	for _, image := range images {
		if strings.TrimSpace(image) != "" {
			return true
		}
	}
	return false
}

// validateModerationTestImageDataURL checks encoded length, literal image data
// prefix, a base64 header marker, standard base64 decoding, and decoded byte
// size. It does not sniff image bytes or validate a full MIME subtype; those
// snapshot checks are separate.
func validateModerationTestImageDataURL(value string) error {
	// Reject oversized encoded input before allocating decoded probe bytes.
	if len(value) > maxContentModerationTestImageDataURLBytes {
		return infraerrors.BadRequest("MODERATION_TEST_IMAGE_TOO_LARGE", "Test images must not exceed 8MB")
	}
	if !strings.HasPrefix(value, "data:image/") {
		return infraerrors.BadRequest("INVALID_MODERATION_TEST_IMAGE", "Test images must use data:image/* base64 encoding")
	}
	parts := strings.SplitN(value, ",", 2)
	if len(parts) != 2 || !strings.Contains(parts[0], ";base64") {
		return infraerrors.BadRequest("INVALID_MODERATION_TEST_IMAGE", "Test images must be base64 data URLs")
	}
	raw, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return infraerrors.BadRequest("INVALID_MODERATION_TEST_IMAGE", "Invalid test image base64 encoding")
	}
	// Decoded bytes have their own cap despite encoded-length acceptance.
	if len(raw) > maxContentModerationTestImageBytes {
		return infraerrors.BadRequest("MODERATION_TEST_IMAGE_TOO_LARGE", "Test images must not exceed 8MB")
	}
	return nil
}

// buildContentModerationTestAuditResult copies scores and effective
// known-category thresholds for administrator display. Nil results return nil.
// It ignores the provider Flagged bit, applies local threshold evaluation, and
// reports the highest score again as CompositeScore.
//
// Unknown category scores remain in display data and can win its maximum while
// only known thresholds decide Flagged. Returned score and threshold maps can
// be mutated without changing the supplied maps; comparisons still use ordinary
// floating-point semantics.
func buildContentModerationTestAuditResult(result *moderationAPIResult, thresholds map[string]float64) *ContentModerationTestAuditResult {
	if result == nil {
		return nil
	}
	scores := make(map[string]float64, len(result.CategoryScores))
	maps.Copy(scores, result.CategoryScores)
	thresholdSnapshot := mergeContentModerationThresholds(ContentModerationDefaultThresholds(), thresholds)
	flagged, highestCategory, highestScore := evaluateModerationScores(scores, thresholdSnapshot)
	// The administrator composite display repeats the maximum category score.
	compositeScore := highestScore
	return &ContentModerationTestAuditResult{
		Flagged:         flagged,
		HighestCategory: highestCategory,
		HighestScore:    highestScore,
		CompositeScore:  compositeScore,
		CategoryScores:  scores,
		Thresholds:      thresholdSnapshot,
	}
}

// evaluateModerationScores returns local flagging and the displayed highest
// category and score. Ordered known categories compare their score with
// threshold using >=; missing map values read as zero, so a zero or absent
// threshold can flag a missing category. Provider Flagged does not participate.
//
// Known-category ties keep the first ordered category. Unknown scores can
// become the displayed maximum but never trigger flagging here; equal unknown
// maxima follow map iteration order when they exceed the known maximum. Callers
// normally supply the full normalized threshold map to make absence behavior
// deliberate.
func evaluateModerationScores(scores map[string]float64, thresholds map[string]float64) (bool, string, float64) {
	flagged := false
	highestCategory := ""
	highestScore := 0.0
	// Known missing scores read as zero during threshold comparison, and fixed
	// order keeps the first known maximum when scores tie across those categories.
	for _, category := range contentModerationCategoryOrder {
		score := scores[category]
		if score > highestScore || highestCategory == "" {
			highestScore = score
			highestCategory = category
		}
		if score >= thresholds[category] {
			flagged = true
		}
	}
	// Include unknown scores in display maxima after flagging decisions; a larger
	// unknown value can change the displayed category while leaving
	// flagging unchanged.
	for category, score := range scores {
		if score > highestScore || highestCategory == "" {
			highestScore = score
			highestCategory = category
		}
	}
	return flagged, highestCategory, highestScore
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

// ExtractCyberWarningText prefers the shared upstream error extractor, then
// nested response error fields, then trimmed raw body text. It applies no
// redaction or length cap; buildCyberWarning caps the retained review text
// later.
func ExtractCyberWarningText(body []byte) string {
	// Try supported nested response error fields after the common extractor, then
	// retain raw body text when no structured message supplies the Cyber
	// review evidence.
	text := strings.TrimSpace(upstream.ExtractErrorMessage(body))
	if text == "" {
		text = strings.TrimSpace(gjson.GetBytes(body, "response.error.message").String())
	}
	if text == "" {
		text = strings.TrimSpace(gjson.GetBytes(body, "response.status_details.error.message").String())
	}
	if text == "" {
		text = strings.TrimSpace(string(body))
	}
	return text
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

// sanitizeContentModerationExcerpt redacts recognized secret patterns before
// limiting rune length for administrator summaries. Redaction can change
// excerpt spelling and length; full retained input items follow separate review
// and retention rules.
func sanitizeContentModerationExcerpt(text string, max int) string {
	// Redaction precedes truncation so a recognized credential value crossing the
	// excerpt cutoff is replaced from the complete input before summary
	// length is selected.
	return trimRunes(redactContentModerationSecrets(text), max)
}

// trimRawContentModerationText trims surrounding whitespace and applies a rune
// limit to raw review text. It leaves recognized secret text intact before the
// cap, so Cyber callers keep this result within authorized administrator
// review.
func trimRawContentModerationText(text string, max int) string {
	return trimRunes(strings.TrimSpace(text), max)
}

// trimRunes returns text unchanged when its rune count fits and otherwise
// reconstructs its first max runes. Nonpositive limits return empty;
// reconstruction can replace invalid UTF-8 bytes with replacement runes on the
// shortened path.
func trimRunes(text string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	// Reconstructing this shortened rune slice can replace malformed UTF-8 with
	// replacement characters.
	return string(runes[:max])
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

// Start launches the fixed worker population and delayed cleanup after
// dependencies have been bound. Missing storage or prior start/stop state skips
// startup. runtimeMu serializes opening the runtime with stop, publishes
// runtimeDone, and registers workers before releasing the lock.
//
// Workers individually consult current configured WorkerCount to decide whether
// to dequeue; changing that setting does not create or remove the fixed
// goroutines. A stopped service remains closed and cannot restart. Construction
// and binding precede this call so workers share the same repositories and
// Runtime callbacks.
func (s *ContentModerationService) Start() {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	if s.runtimeStarted || s.runtimeStopped || s.settingRepo == nil || s.repo == nil {
		return
	}
	// Publish startup and register work while Stop cannot acquire the lifecycle
	// lock, ensuring its wait includes every worker accepted by this startup.
	s.runtimeStarted = true
	s.runtimeDone = make(chan struct{})
	for i := range s.workerCount {
		s.runtimeWG.Go(func() { ; s.worker(i) })
	}
	s.runtimeWG.Go(func() { ; s.cleanupWorker() })
}

// Stop closes queue admission and the runtime stop signal, then waits for
// registered workers and cleanup. The write lock excludes in-progress enqueue
// operations before channel closure. Active worker contexts keep their own
// deadlines, and workers may leave tasks behind when configuration is
// unavailable or their IDs are inactive.
//
// After the join, remaining queued tasks produce an incomplete-drain error.
// Synchronous Check, Cyber warning work, and Runtime.Background callbacks have
// separate owners outside runtimeWG. Repeated calls retain stopped state and
// wait again; caller-controlled bounded waiting is provided by StopContext.
func (s *ContentModerationService) Stop() error {
	s.runtimeMu.Lock()
	// Exclude senders under the write lock before closing queue and stop signals;
	// a sender holding the read lock finishes before either channel can close.
	if !s.runtimeStopped {
		s.runtimeStopped = true
		if s.runtimeDone != nil {
			close(s.runtimeDone)
		}
		if s.asyncQueue != nil {
			close(s.asyncQueue)
		}
	}
	s.runtimeMu.Unlock()
	// Join worker and cleanup contexts before judging buffered completion; disabled
	// workers or a missing usable config can leave records behind for this count.
	s.runtimeWG.Wait()
	if n := len(s.asyncQueue); n > 0 {
		return fmt.Errorf("content moderation drain incomplete: %d tasks", n)
	}
	return nil
}

// StopContext starts one shared Stop goroutine and waits for its result or the
// supplied context. Supply a nonnil context. Completion publishes stopWaitErr
// through channel closure so later waiters read the same joined result.
//
// A caller deadline ends that caller wait while Stop continues using worker and
// cleanup contexts. Its error includes separately sampled queued and active
// counts, which can change during collection. A ready result and a canceled
// context can compete in select, and timing out does not reopen queue admission
// or cancel the drain worker. Starting the goroutine does not wait for queue
// closure before entering the caller's select, so a timeout can precede
// admission sealing under runtimeMu.
func (s *ContentModerationService) StopContext(ctx context.Context) error {
	// Publish one eventual drain result to all waiters. A caller timeout leaves
	// this goroutine running; Stop seals admission when it acquires runtimeMu.
	s.stopWaitOnce.Do(func() {
		s.stopWaitDone = make(chan struct{})
		go func() { s.stopWaitErr = s.Stop(); close(s.stopWaitDone) }()
	})
	select {
	case <-s.stopWaitDone:
		return s.stopWaitErr
	case <-ctx.Done():
		return fmt.Errorf("content moderation drain incomplete: %d tasks: %w", int64(len(s.asyncQueue))+s.asyncActive.Load(), ctx.Err())
	}
}
