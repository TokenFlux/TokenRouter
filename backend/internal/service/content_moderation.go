package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	infraerrors "github.com/BrandonVee/TokenRouter/internal/pkg/errors"
	"github.com/BrandonVee/TokenRouter/internal/pkg/httpclient"
	"github.com/BrandonVee/TokenRouter/internal/pkg/pagination"
	"github.com/BrandonVee/TokenRouter/internal/pkg/servertiming"
	"github.com/tidwall/gjson"
)

const (
	ContentModerationModeOff      = "off"
	ContentModerationModeObserve  = "observe"
	ContentModerationModePreBlock = "pre_block"

	contentModerationAPIKeysModeAppend  = "append"
	contentModerationAPIKeysModeReplace = "replace"

	ContentModerationActionAllow        = "allow"
	ContentModerationActionBlock        = "block"
	ContentModerationActionHashBlock    = "hash_block"
	ContentModerationActionKeywordBlock = "keyword_block"
	ContentModerationActionError        = "error"

	contentModerationKeywordCategory = "keyword"

	ContentModerationKeywordModeKeywordOnly   = "keyword_only"
	ContentModerationKeywordModeKeywordAndAPI = "keyword_and_api"
	ContentModerationKeywordModeAPIOnly       = "api_only"

	ContentModerationModelFilterAll     = "all"
	ContentModerationModelFilterInclude = "include"
	ContentModerationModelFilterExclude = "exclude"

	ContentModerationProtocolAnthropicMessages = "anthropic_messages"
	ContentModerationProtocolOpenAIResponses   = "openai_responses"
	ContentModerationProtocolOpenAIChat        = "openai_chat_completions"
	ContentModerationProtocolGemini            = "gemini"
	ContentModerationProtocolOpenAIImages      = "openai_images"

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

	defaultContentModerationWorkerCount                = 4
	maxContentModerationWorkerCount                    = 32
	defaultContentModerationQueueSize                  = 32768
	maxContentModerationQueueSize                      = 100000
	maxContentModerationBufferedBytes            int64 = 512 * 1024 * 1024
	defaultContentModerationBanThreshold               = 10
	defaultContentModerationViolationWindowHours       = 720
	defaultContentModerationBlockHTTPStatus            = http.StatusForbidden
	defaultContentModerationBlockMessage               = "内容审计命中风险规则，请调整输入后重试"
	defaultContentModerationRetryCount                 = 2
	maxContentModerationRetryCount                     = 5
	defaultContentModerationHitRetentionDays           = 180
	defaultContentModerationNonHitRetentionDays        = 3
	defaultContentModerationCyberBanThreshold          = 10
	defaultContentModerationCyberWindowHours           = 720
	maxContentModerationRetentionDays                  = 3650
	maxContentModerationNonHitRetentionDays            = 3
	contentModerationKeyRateLimitFreezeDuration        = time.Minute
	contentModerationKeyAuthFreezeDuration             = 10 * time.Minute
	contentModerationKeyHTTPErrorFreezeDuration        = 10 * time.Second
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

	contentModerationCleanupInterval = 24 * time.Hour
	contentModerationCleanupTimeout  = 30 * time.Minute
	contentModerationCleanupDelay    = 5 * time.Minute

	contentModerationRuntimeCacheTTL       = time.Second
	contentModerationRuntimeRefreshTimeout = 5 * time.Second
)

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

func ContentModerationCategories() []string {
	out := make([]string, len(contentModerationCategoryOrder))
	copy(out, contentModerationCategoryOrder)
	return out
}

type ContentModerationConfig struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode"`
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
	// ProxyID 指定审计请求使用的代理，nil 表示直连。
	ProxyID                 *int64                            `json:"proxy_id,omitempty"`
	APIKey                  string                            `json:"api_key,omitempty"`
	APIKeys                 []string                          `json:"api_keys,omitempty"`
	APIKeyMetadata          []ContentModerationAPIKeyMetadata `json:"api_key_metadata,omitempty"`
	TimeoutMS               int                               `json:"timeout_ms"`
	SampleRate              int                               `json:"sample_rate"`
	AllGroups               bool                              `json:"all_groups"`
	GroupIDs                []int64                           `json:"group_ids"`
	RecordNonHits           bool                              `json:"record_non_hits"`
	Thresholds              map[string]float64                `json:"thresholds"`
	WorkerCount             int                               `json:"worker_count"`
	QueueSize               int                               `json:"queue_size"`
	BlockStatus             int                               `json:"block_status"`
	BlockMessage            string                            `json:"block_message"`
	EmailOnHit              bool                              `json:"email_on_hit"`
	AutoBanEnabled          bool                              `json:"auto_ban_enabled"`
	BanThreshold            int                               `json:"ban_threshold"`
	ViolationWindowHours    int                               `json:"violation_window_hours"`
	RetryCount              int                               `json:"retry_count"`
	HitRetentionDays        int                               `json:"hit_retention_days"`
	NonHitRetentionDays     int                               `json:"non_hit_retention_days"`
	PreHashCheckEnabled     bool                              `json:"pre_hash_check_enabled"`
	CyberWarningEnabled     bool                              `json:"cyber_warning_enabled"`
	CyberAutoBanEnabled     bool                              `json:"cyber_auto_ban_enabled"`
	CyberBanThreshold       int                               `json:"cyber_ban_threshold"`
	CyberWindowHours        int                               `json:"cyber_violation_window_hours"`
	BlockedKeywords         []string                          `json:"blocked_keywords"`
	KeywordBlockingMode     string                            `json:"keyword_blocking_mode"`
	ModelFilter             ContentModerationModelFilter      `json:"model_filter"`
	AuditUserTextMaxChars   int                               `json:"audit_user_text_max_chars"`
	AuditImages             bool                              `json:"audit_images"`
	AuditToolOutputs        bool                              `json:"audit_tool_outputs"`
	AuditToolOutputMaxChars int                               `json:"audit_tool_output_max_chars"`
}

type ContentModerationConfigView struct {
	Enabled                 bool                            `json:"enabled"`
	Mode                    string                          `json:"mode"`
	BaseURL                 string                          `json:"base_url"`
	Model                   string                          `json:"model"`
	ProxyID                 *int64                          `json:"proxy_id"`
	APIKeyConfigured        bool                            `json:"api_key_configured"`
	APIKeyMasked            string                          `json:"api_key_masked"`
	APIKeyCount             int                             `json:"api_key_count"`
	APIKeyMasks             []string                        `json:"api_key_masks"`
	APIKeyStatuses          []ContentModerationAPIKeyStatus `json:"api_key_statuses"`
	TimeoutMS               int                             `json:"timeout_ms"`
	SampleRate              int                             `json:"sample_rate"`
	AllGroups               bool                            `json:"all_groups"`
	GroupIDs                []int64                         `json:"group_ids"`
	RecordNonHits           bool                            `json:"record_non_hits"`
	Thresholds              map[string]float64              `json:"thresholds"`
	WorkerCount             int                             `json:"worker_count"`
	QueueSize               int                             `json:"queue_size"`
	BlockStatus             int                             `json:"block_status"`
	BlockMessage            string                          `json:"block_message"`
	EmailOnHit              bool                            `json:"email_on_hit"`
	AutoBanEnabled          bool                            `json:"auto_ban_enabled"`
	BanThreshold            int                             `json:"ban_threshold"`
	ViolationWindowHours    int                             `json:"violation_window_hours"`
	RetryCount              int                             `json:"retry_count"`
	HitRetentionDays        int                             `json:"hit_retention_days"`
	NonHitRetentionDays     int                             `json:"non_hit_retention_days"`
	PreHashCheckEnabled     bool                            `json:"pre_hash_check_enabled"`
	CyberWarningEnabled     bool                            `json:"cyber_warning_enabled"`
	CyberAutoBanEnabled     bool                            `json:"cyber_auto_ban_enabled"`
	CyberBanThreshold       int                             `json:"cyber_ban_threshold"`
	CyberWindowHours        int                             `json:"cyber_violation_window_hours"`
	BlockedKeywords         []string                        `json:"blocked_keywords"`
	KeywordBlockingMode     string                          `json:"keyword_blocking_mode"`
	ModelFilter             ContentModerationModelFilter    `json:"model_filter"`
	AuditUserTextMaxChars   int                             `json:"audit_user_text_max_chars"`
	AuditImages             bool                            `json:"audit_images"`
	AuditToolOutputs        bool                            `json:"audit_tool_outputs"`
	AuditToolOutputMaxChars int                             `json:"audit_tool_output_max_chars"`
}

// ContentModerationAPIKeyMetadata 保存审核 Key 的调度权重和管理员备注，不重复保存明文 Key。
type ContentModerationAPIKeyMetadata struct {
	KeyHash  string `json:"key_hash"`
	Priority int    `json:"priority"`
	Note     string `json:"note"`
}

// ContentModerationAPIKeyEntryInput 用于新增或替换带调度属性的审核 Key。
type ContentModerationAPIKeyEntryInput struct {
	APIKey   string `json:"api_key"`
	Priority int    `json:"priority"`
	Note     string `json:"note"`
}

type ContentModerationAPIKeyStatus struct {
	Index          int        `json:"index"`
	KeyHash        string     `json:"key_hash"`
	Masked         string     `json:"masked"`
	Status         string     `json:"status"`
	FailureCount   int        `json:"failure_count"`
	SuccessCount   int64      `json:"success_count"`
	LastError      string     `json:"last_error"`
	LastCheckedAt  *time.Time `json:"last_checked_at,omitempty"`
	FrozenUntil    *time.Time `json:"frozen_until,omitempty"`
	LastLatencyMS  int        `json:"last_latency_ms"`
	LastHTTPStatus int        `json:"last_http_status"`
	LastTested     bool       `json:"last_tested"`
	Configured     bool       `json:"configured"`
	Priority       int        `json:"priority"`
	Note           string     `json:"note"`
}

type ContentModerationAPIKeyLoad struct {
	Index          int    `json:"index"`
	KeyHash        string `json:"key_hash"`
	Masked         string `json:"masked"`
	Status         string `json:"status"`
	Active         int64  `json:"active"`
	Total          int64  `json:"total"`
	Success        int64  `json:"success"`
	Errors         int64  `json:"errors"`
	AvgLatencyMS   int64  `json:"avg_latency_ms"`
	LastLatencyMS  int    `json:"last_latency_ms"`
	LastHTTPStatus int    `json:"last_http_status"`
	Priority       int    `json:"priority"`
	Note           string `json:"note"`
}

type TestContentModerationAPIKeysInput struct {
	APIKeys   []string `json:"api_keys"`
	BaseURL   string   `json:"base_url"`
	Model     string   `json:"model"`
	TimeoutMS int      `json:"timeout_ms"`
	// ProxyID 为 nil 时沿用已保存配置，非正数强制直连，正数指定代理。
	ProxyID *int64   `json:"proxy_id"`
	Prompt  string   `json:"prompt"`
	Images  []string `json:"images"`
}

type TestContentModerationAPIKeysResult struct {
	Items       []ContentModerationAPIKeyStatus   `json:"items"`
	AuditResult *ContentModerationTestAuditResult `json:"audit_result,omitempty"`
	ImageCount  int                               `json:"image_count"`
}

type ContentModerationTestAuditResult struct {
	Flagged         bool               `json:"flagged"`
	HighestCategory string             `json:"highest_category"`
	HighestScore    float64            `json:"highest_score"`
	CompositeScore  float64            `json:"composite_score"`
	CategoryScores  map[string]float64 `json:"category_scores"`
	Thresholds      map[string]float64 `json:"thresholds"`
}

type UpdateContentModerationConfigInput struct {
	Enabled *bool   `json:"enabled"`
	Mode    *string `json:"mode"`
	BaseURL *string `json:"base_url"`
	Model   *string `json:"model"`
	// ProxyID 为 nil 时不修改，非正数清除代理，正数指定代理。
	ProxyID                 *int64                               `json:"proxy_id"`
	APIKey                  *string                              `json:"api_key"`
	APIKeys                 *[]string                            `json:"api_keys"`
	APIKeyEntries           *[]ContentModerationAPIKeyEntryInput `json:"api_key_entries"`
	APIKeyUpdates           *[]ContentModerationAPIKeyMetadata   `json:"api_key_updates"`
	APIKeysMode             string                               `json:"api_keys_mode"`
	DeleteAPIKeyHashes      *[]string                            `json:"delete_api_key_hashes"`
	ClearAPIKey             bool                                 `json:"clear_api_key"`
	TimeoutMS               *int                                 `json:"timeout_ms"`
	SampleRate              *int                                 `json:"sample_rate"`
	AllGroups               *bool                                `json:"all_groups"`
	GroupIDs                *[]int64                             `json:"group_ids"`
	RecordNonHits           *bool                                `json:"record_non_hits"`
	Thresholds              *map[string]float64                  `json:"thresholds"`
	WorkerCount             *int                                 `json:"worker_count"`
	QueueSize               *int                                 `json:"queue_size"`
	BlockStatus             *int                                 `json:"block_status"`
	BlockMessage            *string                              `json:"block_message"`
	EmailOnHit              *bool                                `json:"email_on_hit"`
	AutoBanEnabled          *bool                                `json:"auto_ban_enabled"`
	BanThreshold            *int                                 `json:"ban_threshold"`
	ViolationWindowHours    *int                                 `json:"violation_window_hours"`
	RetryCount              *int                                 `json:"retry_count"`
	HitRetentionDays        *int                                 `json:"hit_retention_days"`
	NonHitRetentionDays     *int                                 `json:"non_hit_retention_days"`
	PreHashCheckEnabled     *bool                                `json:"pre_hash_check_enabled"`
	CyberWarningEnabled     *bool                                `json:"cyber_warning_enabled"`
	CyberAutoBanEnabled     *bool                                `json:"cyber_auto_ban_enabled"`
	CyberBanThreshold       *int                                 `json:"cyber_ban_threshold"`
	CyberWindowHours        *int                                 `json:"cyber_violation_window_hours"`
	BlockedKeywords         *[]string                            `json:"blocked_keywords"`
	KeywordBlockingMode     *string                              `json:"keyword_blocking_mode"`
	ModelFilter             *ContentModerationModelFilter        `json:"model_filter"`
	AuditUserTextMaxChars   *int                                 `json:"audit_user_text_max_chars"`
	AuditImages             *bool                                `json:"audit_images"`
	AuditToolOutputs        *bool                                `json:"audit_tool_outputs"`
	AuditToolOutputMaxChars *int                                 `json:"audit_tool_output_max_chars"`
}

type ContentModerationModelFilter struct {
	Type   string   `json:"type"`
	Models []string `json:"models"`
}

// ContentModerationCheckInput 中 UserID 表示实际处置对象，BillingUserID 和 TeamID 仅表示归属。
type ContentModerationCheckInput struct {
	RequestID     string
	UserID        int64
	UserEmail     string
	BillingUserID int64
	TeamID        *int64
	APIKeyID      int64
	APIKeyName    string
	GroupID       *int64
	GroupName     string
	Endpoint      string
	Provider      string
	Model         string
	Protocol      string
	Body          []byte
}

const (
	ContentModerationSourceUser  = "user"
	ContentModerationSourceTool  = "tool"
	ContentModerationSourceMixed = "mixed"

	ContentModerationItemTypeText    = "text"
	ContentModerationItemTypeImage   = "image"
	ContentModerationItemTypeRequest = "request"
)

// ContentModerationInputItem 是管理员复审时展示的完整当前轮内容单元。
type ContentModerationInputItem struct {
	Index    int    `json:"index"`
	Source   string `json:"source"`
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageRef string `json:"image_ref,omitempty"`
}

// ContentModerationImage 保留图片引用与其在结构化输入中的来源位置。
type ContentModerationImage struct {
	SourceIndex int    `json:"source_index"`
	Source      string `json:"source"`
	Reference   string `json:"reference"`
}

type ContentModerationInput struct {
	Text       string
	Images     []string
	Items      []ContentModerationInputItem
	ImageItems []ContentModerationImage
	Source     string
}

func (in *ContentModerationInput) Normalize() {
	if in == nil {
		return
	}
	// 完整原文用于分块审核和管理员复审，不能在这里截断或折叠空白。
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

func (in ContentModerationInput) IsEmpty() bool {
	return strings.TrimSpace(in.Text) == "" && len(in.Images) == 0
}

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

func normalizeContentModerationSource(source string) string {
	if strings.EqualFold(strings.TrimSpace(source), ContentModerationSourceTool) {
		return ContentModerationSourceTool
	}
	return ContentModerationSourceUser
}

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

func (in ContentModerationInput) ExcerptText() string {
	return in.Text
}

func (in ContentModerationInput) Hash() string {
	h := sha256.New()
	_, _ = h.Write([]byte("text:"))
	_, _ = h.Write([]byte(in.Text))
	for _, image := range in.Images {
		imageHash := sha256.Sum256([]byte(image))
		_, _ = h.Write([]byte("\nimage:"))
		_, _ = h.Write([]byte(hex.EncodeToString(imageHash[:])))
	}
	return hex.EncodeToString(h.Sum(nil))
}

type ContentModerationDecision struct {
	Allowed         bool               `json:"allowed"`
	Blocked         bool               `json:"blocked"`
	Flagged         bool               `json:"flagged"`
	Message         string             `json:"message"`
	StatusCode      int                `json:"status_code"`
	InputHash       string             `json:"input_hash,omitempty"`
	HighestCategory string             `json:"highest_category"`
	HighestScore    float64            `json:"highest_score"`
	CategoryScores  map[string]float64 `json:"category_scores"`
	Action          string             `json:"action"`
}

// ContentModerationFailedUnit 记录未完成审核的单元及错误原因。
type ContentModerationFailedUnit struct {
	Type        string `json:"type"`
	Index       int    `json:"index"`
	SourceIndex int    `json:"source_index"`
	Error       string `json:"error"`
}

// ContentModerationMedia 保存管理员复审所需的图片快照及获取状态。
type ContentModerationMedia struct {
	ID             int64     `json:"id"`
	LogID          *int64    `json:"-"`
	CyberWarningID *int64    `json:"-"`
	SourceIndex    int       `json:"source_index"`
	Source         string    `json:"source"`
	MIMEType       string    `json:"mime_type"`
	SHA256         string    `json:"sha256"`
	ByteSize       int64     `json:"byte_size"`
	OriginalRef    string    `json:"original_ref"`
	SnapshotStatus string    `json:"snapshot_status"`
	SnapshotError  string    `json:"snapshot_error"`
	Content        []byte    `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
}

// ContentModerationLog 保留实际处置对象、付款用户和团队三层审计身份。
type ContentModerationLog struct {
	ID                int64                         `json:"id"`
	RequestID         string                        `json:"request_id"`
	UserID            *int64                        `json:"user_id,omitempty"`
	UserEmail         string                        `json:"user_email"`
	BillingUserID     *int64                        `json:"billing_user_id,omitempty"`
	TeamID            *int64                        `json:"team_id,omitempty"`
	APIKeyID          *int64                        `json:"api_key_id,omitempty"`
	APIKeyName        string                        `json:"api_key_name"`
	GroupID           *int64                        `json:"group_id,omitempty"`
	GroupName         string                        `json:"group_name"`
	Endpoint          string                        `json:"endpoint"`
	Provider          string                        `json:"provider"`
	Model             string                        `json:"model"`
	Mode              string                        `json:"mode"`
	Action            string                        `json:"action"`
	Flagged           bool                          `json:"flagged"`
	HighestCategory   string                        `json:"highest_category"`
	HighestScore      float64                       `json:"highest_score"`
	MatchedKeyword    string                        `json:"matched_keyword"`
	CategoryScores    map[string]float64            `json:"category_scores"`
	ThresholdSnapshot map[string]float64            `json:"threshold_snapshot"`
	InputExcerpt      string                        `json:"input_excerpt"`
	Source            string                        `json:"source"`
	InputItems        []ContentModerationInputItem  `json:"input_items,omitempty"`
	ContentComplete   bool                          `json:"content_complete"`
	AuditComplete     bool                          `json:"audit_complete"`
	TextUnitCount     int                           `json:"text_unit_count"`
	ImageUnitCount    int                           `json:"image_unit_count"`
	FailedUnitCount   int                           `json:"failed_unit_count"`
	FailedUnits       []ContentModerationFailedUnit `json:"failed_units,omitempty"`
	Media             []ContentModerationMedia      `json:"media,omitempty"`
	UpstreamLatencyMS *int                          `json:"upstream_latency_ms,omitempty"`
	Error             string                        `json:"error"`
	ViolationCount    int                           `json:"violation_count"`
	AutoBanned        bool                          `json:"auto_banned"`
	EmailSent         bool                          `json:"email_sent"`
	UserStatus        string                        `json:"user_status"`
	QueueDelayMS      *int                          `json:"queue_delay_ms,omitempty"`
	CreatedAt         time.Time                     `json:"created_at"`
}

// ContentModerationCyberWarning 表示一次 OpenAI 上游 cyber 风控拒绝事件。
type ContentModerationCyberWarning struct {
	ID              int64                         `json:"id"`
	RequestID       string                        `json:"request_id"`
	UserID          *int64                        `json:"user_id,omitempty"`
	UserEmail       string                        `json:"user_email"`
	BillingUserID   *int64                        `json:"billing_user_id,omitempty"`
	TeamID          *int64                        `json:"team_id,omitempty"`
	APIKeyID        *int64                        `json:"api_key_id,omitempty"`
	APIKeyName      string                        `json:"api_key_name"`
	GroupID         *int64                        `json:"group_id,omitempty"`
	GroupName       string                        `json:"group_name"`
	AccountID       *int64                        `json:"account_id,omitempty"`
	AccountName     string                        `json:"account_name"`
	Endpoint        string                        `json:"endpoint"`
	Model           string                        `json:"model"`
	UpstreamStatus  int                           `json:"upstream_status"`
	WarningText     string                        `json:"warning_text"`
	PromptExcerpt   string                        `json:"prompt_excerpt"`
	Source          string                        `json:"source"`
	InputItems      []ContentModerationInputItem  `json:"input_items,omitempty"`
	ContentComplete bool                          `json:"content_complete"`
	AuditComplete   bool                          `json:"audit_complete"`
	TextUnitCount   int                           `json:"text_unit_count"`
	ImageUnitCount  int                           `json:"image_unit_count"`
	FailedUnitCount int                           `json:"failed_unit_count"`
	FailedUnits     []ContentModerationFailedUnit `json:"failed_units,omitempty"`
	Media           []ContentModerationMedia      `json:"media,omitempty"`
	ViolationCount  int                           `json:"violation_count"`
	AutoBanned      bool                          `json:"auto_banned"`
	EmailSent       bool                          `json:"email_sent"`
	UserStatus      string                        `json:"user_status"`
	CreatedAt       time.Time                     `json:"created_at"`
}

// ContentModerationCyberWarningPolicy 描述 cyber 警告落库后的窗口计数和封禁策略。
type ContentModerationCyberWarningPolicy struct {
	AutoBanEnabled bool
	BanThreshold   int
	WindowHours    int
}

// ContentModerationCyberWarningInput 是网关记录 cyber 警告时传入的上下文。
type ContentModerationCyberWarningInput struct {
	RequestID      string
	UserID         int64
	UserEmail      string
	BillingUserID  int64
	TeamID         *int64
	APIKeyID       int64
	APIKeyName     string
	GroupID        *int64
	GroupName      string
	AccountID      int64
	AccountName    string
	Endpoint       string
	Model          string
	UpstreamStatus int
	ResponseBody   []byte
	WarningText    string
	PromptExcerpt  string
	Content        ContentModerationInput
}

type ContentModerationLogFilter struct {
	Pagination pagination.PaginationParams
	Result     string
	GroupID    *int64
	Endpoint   string
	Search     string
	From       *time.Time
	To         *time.Time
}

// ContentModerationCyberWarningFilter 描述 cyber 警告列表和统计的筛选条件。
type ContentModerationCyberWarningFilter struct {
	Pagination pagination.PaginationParams
	UserID     *int64
	AccountID  *int64
	Search     string
	From       *time.Time
	To         *time.Time
}

// ContentModerationCyberSummary 是 cyber 警告统计总览。
type ContentModerationCyberSummary struct {
	Events    int64                                  `json:"events"`
	Requests  int64                                  `json:"requests"`
	Users     int64                                  `json:"users"`
	Accounts  int64                                  `json:"accounts"`
	ByUser    []ContentModerationCyberUserSummary    `json:"by_user"`
	ByAccount []ContentModerationCyberAccountSummary `json:"by_account"`
}

// ContentModerationCyberUserSummary 是按用户聚合的 cyber 警告统计。
type ContentModerationCyberUserSummary struct {
	Count     int64  `json:"count"`
	UserID    *int64 `json:"user_id,omitempty"`
	UserEmail string `json:"user_email"`
	APIKeys   string `json:"api_keys"`
	LastSeen  string `json:"last_seen"`
}

// ContentModerationCyberAccountSummary 是按上游账号聚合的 cyber 警告统计。
type ContentModerationCyberAccountSummary struct {
	Count       int64  `json:"count"`
	AccountID   *int64 `json:"account_id,omitempty"`
	AccountName string `json:"account_name"`
	Users       int64  `json:"users"`
	LastSeen    string `json:"last_seen"`
}

type ContentModerationCleanupResult struct {
	DeletedHit    int64     `json:"deleted_hit"`
	DeletedNonHit int64     `json:"deleted_non_hit"`
	DeletedCyber  int64     `json:"deleted_cyber"`
	FinishedAt    time.Time `json:"finished_at"`
}

type ContentModerationRuntimeStatus struct {
	Enabled                      bool                            `json:"enabled"`
	RiskControlEnabled           bool                            `json:"risk_control_enabled"`
	Mode                         string                          `json:"mode"`
	WorkerCount                  int                             `json:"worker_count"`
	MaxWorkers                   int                             `json:"max_workers"`
	ActiveWorkers                int                             `json:"active_workers"`
	IdleWorkers                  int                             `json:"idle_workers"`
	QueueSize                    int                             `json:"queue_size"`
	QueueLength                  int                             `json:"queue_length"`
	QueueUsagePercent            float64                         `json:"queue_usage_percent"`
	Enqueued                     int64                           `json:"enqueued"`
	Dropped                      int64                           `json:"dropped"`
	Processed                    int64                           `json:"processed"`
	Errors                       int64                           `json:"errors"`
	PreBlockActive               int                             `json:"pre_block_active"`
	PreBlockChecked              int64                           `json:"pre_block_checked"`
	PreBlockAllowed              int64                           `json:"pre_block_allowed"`
	PreBlockBlocked              int64                           `json:"pre_block_blocked"`
	PreBlockErrors               int64                           `json:"pre_block_errors"`
	PreBlockAvgLatencyMS         int64                           `json:"pre_block_avg_latency_ms"`
	PreBlockAPIKeyActive         int64                           `json:"pre_block_api_key_active"`
	PreBlockAPIKeyAvailableCount int64                           `json:"pre_block_api_key_available_count"`
	PreBlockAPIKeyTotalCalls     int64                           `json:"pre_block_api_key_total_calls"`
	PreBlockAPIKeyLoads          []ContentModerationAPIKeyLoad   `json:"pre_block_api_key_loads"`
	APIKeyStatuses               []ContentModerationAPIKeyStatus `json:"api_key_statuses"`
	FlaggedHashCount             int64                           `json:"flagged_hash_count"`
	LastCleanupAt                *time.Time                      `json:"last_cleanup_at,omitempty"`
	LastCleanupDeletedHit        int64                           `json:"last_cleanup_deleted_hit"`
	LastCleanupDeletedNonHit     int64                           `json:"last_cleanup_deleted_non_hit"`
}

type ContentModerationUnbanUserResult struct {
	UserID int64  `json:"user_id"`
	Status string `json:"status"`
}

type ContentModerationDeleteHashResult struct {
	InputHash string `json:"input_hash"`
	Deleted   bool   `json:"deleted"`
}

type ContentModerationClearHashesResult struct {
	Deleted int64 `json:"deleted"`
}

type ContentModerationRepository interface {
	CreateLog(ctx context.Context, log *ContentModerationLog) error
	ListLogs(ctx context.Context, filter ContentModerationLogFilter) ([]ContentModerationLog, *pagination.PaginationResult, error)
	CountFlaggedByUserSince(ctx context.Context, userID int64, since time.Time) (int, error)
	CreateCyberWarning(ctx context.Context, warning *ContentModerationCyberWarning) error
	CreateCyberWarningAndApplyUserBan(ctx context.Context, warning *ContentModerationCyberWarning, policy ContentModerationCyberWarningPolicy) (bool, error)
	ListCyberWarnings(ctx context.Context, filter ContentModerationCyberWarningFilter) ([]ContentModerationCyberWarning, *pagination.PaginationResult, error)
	CountCyberWarningsByUserSince(ctx context.Context, userID int64, since time.Time) (int, error)
	GetCyberSummary(ctx context.Context, filter ContentModerationCyberWarningFilter) (*ContentModerationCyberSummary, error)
	MarkCyberWarningEmailSent(ctx context.Context, id int64) error
	CleanupExpiredLogs(ctx context.Context, hitBefore time.Time, nonHitBefore time.Time) (*ContentModerationCleanupResult, error)
}

// ContentModerationReviewRepository 提供只用于管理员详情页的完整复审材料读取能力。
type ContentModerationReviewRepository interface {
	GetLog(ctx context.Context, id int64) (*ContentModerationLog, error)
	GetCyberWarning(ctx context.Context, id int64) (*ContentModerationCyberWarning, error)
	GetMediaContent(ctx context.Context, id int64) (*ContentModerationMedia, error)
}

type ContentModerationHashCache interface {
	RecordFlaggedInputHash(ctx context.Context, inputHash string) error
	HasFlaggedInputHash(ctx context.Context, inputHash string) (bool, error)
	DeleteFlaggedInputHash(ctx context.Context, inputHash string) (bool, error)
	ClearFlaggedInputHashes(ctx context.Context) (int64, error)
	CountFlaggedInputHashes(ctx context.Context) (int64, error)
}

type ContentModerationService struct {
	settingRepo              SettingRepository
	repo                     ContentModerationRepository
	hashCache                ContentModerationHashCache
	groupRepo                GroupRepository
	userRepo                 UserRepository
	proxyRepo                ProxyRepository
	authCacheInvalidator     APIKeyAuthCacheInvalidator
	emailService             *EmailService
	httpClient               *http.Client
	moderationProxyCache     atomic.Pointer[moderationProxyURLCacheEntry]
	asyncQueue               chan contentModerationTask
	workerCount              int
	asyncActive              atomic.Int64
	asyncEnqueued            atomic.Int64
	asyncDropped             atomic.Int64
	asyncProcessed           atomic.Int64
	asyncErrors              atomic.Int64
	asyncBufferedBytes       atomic.Int64
	preBlockActive           atomic.Int64
	preBlockChecked          atomic.Int64
	preBlockAllowed          atomic.Int64
	preBlockBlocked          atomic.Int64
	preBlockErrors           atomic.Int64
	preBlockLatencyTotalMS   atomic.Int64
	lastCleanupUnix          atomic.Int64
	lastCleanupDeletedHit    atomic.Int64
	lastCleanupDeletedNonHit atomic.Int64
	runtimeSnapshot          atomic.Pointer[contentModerationRuntimeSnapshot]
	runtimeRefreshMu         sync.Mutex
	runtimeCacheTTL          time.Duration
	runtimeRefreshRetryAt    atomic.Int64
	keyHealthMu              sync.Mutex
	keyHealth                map[string]*contentModerationKeyHealth
	keySchedule              map[string]int64
}

// contentModerationRuntimeSnapshot 将请求热路径所需的开关、配置和关键词索引作为不可变快照发布。
type contentModerationRuntimeSnapshot struct {
	riskControlEnabled bool
	config             *ContentModerationConfig
	keywordMatcher     *contentModerationKeywordMatcher
	configDigest       [sha256.Size]byte
	loadedAt           time.Time
}

type contentModerationTask struct {
	input            ContentModerationCheckInput
	content          ContentModerationInput
	inputHash        string
	log              *ContentModerationLog
	config           *ContentModerationConfig
	recordHash       bool
	applySideEffects bool
	bufferedBytes    int64
	enqueuedAt       time.Time
}

type contentModerationKeyHealth struct {
	Hash           string
	Masked         string
	FailureCount   int
	SuccessCount   int64
	LastError      string
	LastCheckedAt  time.Time
	FrozenUntil    time.Time
	LastLatencyMS  int
	LastHTTPStatus int
	LastTested     bool
	SyncActive     int64
	SyncTotal      int64
	SyncSuccess    int64
	SyncErrors     int64
	SyncLatencyMS  int64
}

func NewContentModerationService(
	settingRepo SettingRepository,
	repo ContentModerationRepository,
	hashCache ContentModerationHashCache,
	groupRepo GroupRepository,
	userRepo UserRepository,
	authCacheInvalidator APIKeyAuthCacheInvalidator,
	emailService *EmailService,
) *ContentModerationService {
	svc := &ContentModerationService{
		settingRepo:          settingRepo,
		repo:                 repo,
		hashCache:            hashCache,
		groupRepo:            groupRepo,
		userRepo:             userRepo,
		authCacheInvalidator: authCacheInvalidator,
		emailService:         emailService,
		httpClient:           servertiming.InstrumentClient(nil),
		workerCount:          maxContentModerationWorkerCount,
		asyncQueue:           make(chan contentModerationTask, maxContentModerationQueueSize),
		keyHealth:            make(map[string]*contentModerationKeyHealth),
		keySchedule:          make(map[string]int64),
	}
	if settingRepo != nil && repo != nil {
		for i := 0; i < svc.workerCount; i++ {
			go svc.worker(i)
		}
		go svc.cleanupWorker()
	}
	return svc
}

// SetProxyRepository 注入内容审计代理仓储；不启用代理的旧构造路径可保持为空。
func (s *ContentModerationService) SetProxyRepository(proxyRepo ProxyRepository) {
	if s != nil {
		s.proxyRepo = proxyRepo
		s.moderationProxyCache.Store(nil)
	}
}

func (s *ContentModerationService) GetConfig(ctx context.Context) (*ContentModerationConfigView, error) {
	cfg, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	return s.configView(cfg), nil
}

func (s *ContentModerationService) UpdateConfig(ctx context.Context, input UpdateContentModerationConfigInput) (*ContentModerationConfigView, error) {
	cfg, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateContentModerationAPIKeyInputs(input.APIKeyEntries, input.APIKeyUpdates); err != nil {
		return nil, err
	}
	if err := validateContentModerationAuditTextLimit(input.AuditUserTextMaxChars, "用户文本"); err != nil {
		return nil, err
	}
	if err := validateContentModerationAuditTextLimit(input.AuditToolOutputMaxChars, "工具结果"); err != nil {
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
		// 输入区显式填写的属性优先于旧 Key 草稿，便于用同一 Key 更新其权重和备注。
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
	if err := s.settingRepo.Set(ctx, SettingKeyContentModerationConfig, string(raw)); err != nil {
		return nil, fmt.Errorf("save content moderation config: %w", err)
	}
	s.replaceRuntimeConfig(cfg, raw)
	// 代理选择可能已变化，下次审计调用必须重新解析。
	s.moderationProxyCache.Store(nil)
	return s.configView(cfg), nil
}

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
	content := ExtractContentModerationInput(input.Protocol, input.Body)
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
				message = fmt.Sprintf("%s（hash: %s）", message, hashText)
			}
			scores := map[string]float64{"hash": 1.0}
			log := s.buildStructuredLog(input, cfg, ContentModerationActionHashBlock, true, "hash", 1.0, scores, content, nil, nil, "", nil)
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
	// 当前范围没有任何需要发往上游的内容时直接放行，避免空任务占用 Key 或异步队列。
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
	if trackPreBlock {
		s.preBlockActive.Add(1)
		defer s.preBlockActive.Add(-1)
	}
	start := time.Now()
	// 单次审核共享总期限，避免超长输入按批次串行累加上游超时。
	auditCtx, cancel := context.WithTimeout(ctx, contentModerationAuditTotalTimeout)
	result := s.auditContentModerationInput(auditCtx, cfg, auditInput, trackPreBlock)
	cancel()
	latency := int(time.Since(start).Milliseconds())
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
		// 审核失败采用 fail-open，但必须无条件保存不完整记录供管理员排查。
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
		log.Media = contentModerationHitMedia(auditInput, result.FlaggedImageIndexes)
		if queueDelay == nil && cfg.Mode == ContentModerationModePreBlock {
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

type contentModerationAuditResult struct {
	CategoryScores      map[string]float64
	FailedUnits         []ContentModerationFailedUnit
	FlaggedImageIndexes []int
	TextUnitCount       int
	ImageUnitCount      int
	SuccessfulUnits     int
	AuditComplete       bool
}

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

type contentModerationUnitResult struct {
	unitType    string
	index       int
	sourceIndex int
	result      *moderationAPIResult
	err         error
}

type contentModerationAPIError struct {
	StatusCode int
	Message    string
}

func (e *contentModerationAPIError) Error() string {
	if e == nil {
		return "moderation api error"
	}
	return fmt.Sprintf("moderation api status %d: %s", e.StatusCode, e.Message)
}

// auditContentModerationInput 汇总全部文本分块和图片单元，任一单元命中即可命中整个请求。
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

	if len(content.ImageItems) > 0 {
		workerCount := min(contentModerationImageConcurrency, len(content.ImageItems))
		results := make(chan contentModerationUnitResult, workerCount)
		jobs := make(chan int)
		var wg sync.WaitGroup
		for worker := 0; worker < workerCount; worker++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
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
			}()
		}
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
	// 仅在上游明确拒绝批量形态时逐条重试，避免服务故障期间放大请求量。
	out := make([]contentModerationUnitResult, 0, len(chunks))
	for index, chunk := range chunks {
		item, singleErr := s.callModeration(ctx, cfg, chunk, trackKeyLoad)
		out = append(out, contentModerationUnitResult{unitType: ContentModerationItemTypeText, index: startIndex + index, result: item, err: singleErr})
	}
	return out
}

func isContentModerationBatchUnsupportedError(err error) bool {
	var apiErr *contentModerationAPIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
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

func mergeContentModerationUnitResult(target *contentModerationAuditResult, unit contentModerationUnitResult, thresholds map[string]float64) {
	if target == nil {
		return
	}
	if unit.err != nil || unit.result == nil {
		errText := "moderation api returned no result"
		if unit.err != nil {
			errText = unit.err.Error()
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

func splitContentModerationText(text string, chunkSize int, overlap int) []string {
	chunks := make([]string, 0, countContentModerationTextChunks(text, chunkSize, overlap))
	forEachContentModerationTextBatch(text, chunkSize, overlap, contentModerationTextBatchSize, func(_ int, batch []string) {
		chunks = append(chunks, batch...)
	})
	return chunks
}

func normalizeContentModerationChunkOptions(chunkSize int, overlap int) (int, int) {
	if chunkSize <= 0 {
		chunkSize = maxModerationInputRunes
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= chunkSize {
		overlap = chunkSize - 1
	}
	return chunkSize, overlap
}

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
	return 1 + (runeCount-chunkSize+step-1)/step
}

// forEachContentModerationTextBatch 仅保留当前批次和重叠窗口，避免超长输入整体展开为 []rune。
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
			if err != nil {
				reachedEOF = true
				break
			}
			chunkRunes = append(chunkRunes, r)
		}
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
		carry = append(carry[:0], chunkRunes[len(chunkRunes)-overlap:]...)
	}
	if len(batch) > 0 {
		visit(batchStart, batch)
	}
}

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

func (s *ContentModerationService) recordPreBlockSyncMetric(latencyMS int, action string) {
	if s == nil {
		return
	}
	s.preBlockChecked.Add(1)
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

func (s *ContentModerationService) enqueueAsync(input ContentModerationCheckInput, cfg *ContentModerationConfig, content ContentModerationInput, hashText string) bool {
	if s == nil || s.asyncQueue == nil {
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
		s.releaseContentModerationBufferedBytes(task.bufferedBytes)
		slog.Warn("content_moderation.async_queue_full", "user_id", input.UserID, "endpoint", input.Endpoint)
		s.asyncDropped.Add(1)
		return false
	}
}

func (s *ContentModerationService) enqueueRecord(input ContentModerationCheckInput, cfg *ContentModerationConfig, log *ContentModerationLog, inputHash string, recordHash bool, applySideEffects bool) bool {
	if s == nil || s.asyncQueue == nil || log == nil {
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
		s.releaseContentModerationBufferedBytes(task.bufferedBytes)
		slog.Warn("content_moderation.record_queue_full",
			"user_id", input.UserID,
			"endpoint", input.Endpoint,
			"action", log.Action)
		s.asyncDropped.Add(1)
		return false
	}
}

func (s *ContentModerationService) worker(id int) {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), maxContentModerationTimeoutMS*time.Millisecond+10*time.Second)
		runtimeSnapshot, err := s.loadRuntimeSnapshot(ctx)
		if err != nil || runtimeSnapshot == nil || runtimeSnapshot.config == nil || id >= runtimeSnapshot.config.WorkerCount {
			cancel()
			time.Sleep(time.Second)
			continue
		}
		cfg := runtimeSnapshot.config
		task, ok := s.dequeueAsyncTask(ctx, time.Second)
		if !ok {
			cancel()
			continue
		}
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
				queueDelay := int(time.Since(task.enqueuedAt).Milliseconds())
				task.log.QueueDelayMS = &queueDelay
				taskCfg := task.config
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
			queueDelay := int(time.Since(task.enqueuedAt).Milliseconds())
			_ = s.checkSync(ctx, task.input, cfg, task.content, task.inputHash, &queueDelay, false)
			s.asyncProcessed.Add(1)
		}()
	}
}

// compactContentModerationInputForQueue 从队列副本移除可由结构化条目恢复的聚合文本，减少重复常驻内存。
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

func contentModerationTextFromItems(items []ContentModerationInputItem) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		if item.Type == ContentModerationItemTypeText && strings.TrimSpace(item.Text) != "" {
			parts = append(parts, item.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func estimateContentModerationTaskBytes(task contentModerationTask) int64 {
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
	// 执行阶段会从条目恢复一次聚合文本，预算同时覆盖这份短期副本。
	return size + textBytes
}

func (s *ContentModerationService) reserveContentModerationBufferedBytes(size int64) bool {
	if s == nil || size <= 0 || size > maxContentModerationBufferedBytes {
		return size <= 0
	}
	for {
		current := s.asyncBufferedBytes.Load()
		if current > maxContentModerationBufferedBytes-size {
			return false
		}
		if s.asyncBufferedBytes.CompareAndSwap(current, current+size) {
			return true
		}
	}
}

func (s *ContentModerationService) releaseContentModerationBufferedBytes(size int64) {
	if s == nil || size <= 0 {
		return
	}
	s.asyncBufferedBytes.Add(-size)
}

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
	select {
	case task, ok := <-s.asyncQueue:
		return task, ok
	case <-ctx.Done():
		return zero, false
	case <-timer.C:
		return zero, false
	}
}

func (s *ContentModerationService) ListLogs(ctx context.Context, filter ContentModerationLogFilter) ([]ContentModerationLog, *pagination.PaginationResult, error) {
	if filter.Pagination.Page <= 0 {
		filter.Pagination.Page = 1
	}
	if filter.Pagination.PageSize <= 0 {
		filter.Pagination.PageSize = 20
	}
	if filter.Pagination.PageSize > 100 {
		filter.Pagination.PageSize = 100
	}
	if filter.Pagination.SortOrder == "" {
		filter.Pagination.SortOrder = pagination.SortOrderDesc
	}
	return s.repo.ListLogs(ctx, filter)
}

func (s *ContentModerationService) GetLog(ctx context.Context, id int64) (*ContentModerationLog, error) {
	if id <= 0 {
		return nil, infraerrors.BadRequest("INVALID_CONTENT_MODERATION_LOG_ID", "审计记录 ID 无效")
	}
	repo, ok := s.repo.(ContentModerationReviewRepository)
	if !ok {
		return nil, infraerrors.InternalServer("CONTENT_MODERATION_REVIEW_UNAVAILABLE", "完整复审材料仓储不可用")
	}
	item, err := repo.GetLog(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.NotFound("CONTENT_MODERATION_LOG_NOT_FOUND", "审计记录不存在")
	}
	return item, err
}

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
	bodyWarningText := extractCyberWarningText(input.ResponseBody)
	warningTextMatched := IsOpenAICyberWarningText(warningText)
	bodyMatched := IsOpenAICyberWarningText(bodyWarningText) || IsOpenAICyberWarningText(string(input.ResponseBody))
	cyberPolicyMatched, _, cyberPolicyMessage := detectOpenAICyberPolicy(input.ResponseBody)
	if !warningTextMatched && !bodyMatched && !cyberPolicyMatched {
		return nil, nil
	}
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
		warning.Media = s.snapshotContentModerationMedia(ctx, warning.Media)
	}
	autoBanJustApplied, err := s.repo.CreateCyberWarningAndApplyUserBan(ctx, warning, ContentModerationCyberWarningPolicy{
		AutoBanEnabled: cfg.CyberAutoBanEnabled,
		BanThreshold:   cfg.CyberBanThreshold,
		WindowHours:    cfg.CyberWindowHours,
	})
	if err != nil {
		return nil, err
	}
	s.applyCyberWarningPostCommitSideEffects(ctx, cfg, warning, autoBanJustApplied)
	return warning, nil
}

// CyberWarningInScope 判断 OpenAI Cyber 相关副作用是否落在风控检测范围内。
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

// CyberSessionBlockGroupInScope 判断当前分组是否已纳入启用中的风控范围，供会话屏蔽热路径使用。
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

func (cfg *ContentModerationConfig) cyberInputInScope(input ContentModerationCyberWarningInput, logPrefix string) bool {
	if cfg == nil {
		return true
	}
	if logPrefix == "" {
		logPrefix = "content_moderation.cyber_scope"
	}
	// Cyber 告警和 cyber_policy 副作用沿用普通风控检测范围，避免范围外分组或模型进入告警、封禁和 OPS 统计。
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

func (s *ContentModerationService) ListCyberWarnings(ctx context.Context, filter ContentModerationCyberWarningFilter) ([]ContentModerationCyberWarning, *pagination.PaginationResult, error) {
	if filter.Pagination.Page <= 0 {
		filter.Pagination.Page = 1
	}
	if filter.Pagination.PageSize <= 0 {
		filter.Pagination.PageSize = 20
	}
	if filter.Pagination.PageSize > 100 {
		filter.Pagination.PageSize = 100
	}
	if filter.Pagination.SortOrder == "" {
		filter.Pagination.SortOrder = pagination.SortOrderDesc
	}
	return s.repo.ListCyberWarnings(ctx, filter)
}

func (s *ContentModerationService) GetCyberWarning(ctx context.Context, id int64) (*ContentModerationCyberWarning, error) {
	if id <= 0 {
		return nil, infraerrors.BadRequest("INVALID_CONTENT_MODERATION_CYBER_WARNING_ID", "Cyber 记录 ID 无效")
	}
	repo, ok := s.repo.(ContentModerationReviewRepository)
	if !ok {
		return nil, infraerrors.InternalServer("CONTENT_MODERATION_REVIEW_UNAVAILABLE", "完整复审材料仓储不可用")
	}
	item, err := repo.GetCyberWarning(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.NotFound("CONTENT_MODERATION_CYBER_WARNING_NOT_FOUND", "Cyber 记录不存在")
	}
	return item, err
}

func (s *ContentModerationService) GetMediaContent(ctx context.Context, id int64) (*ContentModerationMedia, error) {
	if id <= 0 {
		return nil, infraerrors.BadRequest("INVALID_CONTENT_MODERATION_MEDIA_ID", "媒体 ID 无效")
	}
	repo, ok := s.repo.(ContentModerationReviewRepository)
	if !ok {
		return nil, infraerrors.InternalServer("CONTENT_MODERATION_REVIEW_UNAVAILABLE", "完整复审材料仓储不可用")
	}
	item, err := repo.GetMediaContent(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.NotFound("CONTENT_MODERATION_MEDIA_NOT_FOUND", "媒体不存在")
	}
	if err != nil {
		return nil, err
	}
	if item.SnapshotStatus != "ready" || len(item.Content) == 0 {
		return nil, infraerrors.NotFound("CONTENT_MODERATION_MEDIA_NOT_READY", "媒体快照不可用")
	}
	return item, nil
}

func (s *ContentModerationService) GetCyberSummary(ctx context.Context, filter ContentModerationCyberWarningFilter) (*ContentModerationCyberSummary, error) {
	if s == nil || s.repo == nil {
		return &ContentModerationCyberSummary{}, nil
	}
	return s.repo.GetCyberSummary(ctx, filter)
}

func (s *ContentModerationService) UnbanUser(ctx context.Context, userID int64) (*ContentModerationUnbanUserResult, error) {
	if s == nil || s.userRepo == nil {
		return nil, infraerrors.InternalServer("CONTENT_MODERATION_USER_REPOSITORY_UNAVAILABLE", "用户仓储不可用")
	}
	if userID <= 0 {
		return nil, infraerrors.BadRequest("INVALID_USER_ID", "用户 ID 无效")
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, infraerrors.NotFound("USER_NOT_FOUND", "用户不存在")
		}
		return nil, fmt.Errorf("get content moderation unban user: %w", err)
	}
	if user.Status != StatusActive {
		user.Status = StatusActive
		if err := s.userRepo.Update(ctx, user, UserUpdateFields{Status: true}); err != nil {
			return nil, fmt.Errorf("update content moderation unban user: %w", err)
		}
	}
	if s.authCacheInvalidator != nil {
		s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, userID)
	}
	return &ContentModerationUnbanUserResult{
		UserID: userID,
		Status: StatusActive,
	}, nil
}

func (s *ContentModerationService) DeleteFlaggedInputHash(ctx context.Context, inputHash string) (*ContentModerationDeleteHashResult, error) {
	inputHash = normalizeContentModerationHash(inputHash)
	if inputHash == "" {
		return nil, infraerrors.BadRequest("INVALID_CONTENT_MODERATION_HASH", "风险输入哈希无效")
	}
	if s == nil || s.hashCache == nil {
		return nil, infraerrors.InternalServer("CONTENT_MODERATION_HASH_CACHE_UNAVAILABLE", "内容审计哈希缓存不可用")
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

func (s *ContentModerationService) ClearFlaggedInputHashes(ctx context.Context) (*ContentModerationClearHashesResult, error) {
	if s == nil || s.hashCache == nil {
		return nil, infraerrors.InternalServer("CONTENT_MODERATION_HASH_CACHE_UNAVAILABLE", "内容审计哈希缓存不可用")
	}
	deleted, err := s.hashCache.ClearFlaggedInputHashes(ctx)
	if err != nil {
		return nil, fmt.Errorf("clear content moderation flagged hashes: %w", err)
	}
	return &ContentModerationClearHashesResult{Deleted: deleted}, nil
}

func (s *ContentModerationService) GetStatus(ctx context.Context) (*ContentModerationRuntimeStatus, error) {
	if s == nil {
		return &ContentModerationRuntimeStatus{}, nil
	}
	cfg, err := s.loadConfig(ctx)
	if err != nil {
		return nil, err
	}
	riskEnabled := s.isRiskControlEnabled(ctx)
	active := int(s.asyncActive.Load())
	if active < 0 {
		active = 0
	}
	if active > cfg.WorkerCount {
		active = cfg.WorkerCount
	}
	preBlockActive := int(s.preBlockActive.Load())
	if preBlockActive < 0 {
		preBlockActive = 0
	}
	preBlockChecked := s.preBlockChecked.Load()
	preBlockAvgLatency := int64(0)
	if preBlockChecked > 0 {
		preBlockAvgLatency = s.preBlockLatencyTotalMS.Load() / preBlockChecked
	}
	queueLength := 0
	if s.asyncQueue != nil {
		queueLength = len(s.asyncQueue)
	}
	queueUsage := 0.0
	if cfg.QueueSize > 0 {
		queueUsage = float64(queueLength) * 100 / float64(cfg.QueueSize)
	}
	var flaggedHashCount int64
	if s.hashCache != nil {
		if n, err := s.hashCache.CountFlaggedInputHashes(ctx); err == nil {
			flaggedHashCount = n
		} else {
			slog.Warn("content_moderation.hash_count_failed", "error", err)
		}
	}
	var lastCleanupAt *time.Time
	if unix := s.lastCleanupUnix.Load(); unix > 0 {
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

func (s *ContentModerationService) cleanupWorker() {
	timer := time.NewTimer(contentModerationCleanupDelay)
	defer timer.Stop()
	for {
		<-timer.C
		s.runCleanupOnce()
		timer.Reset(contentModerationCleanupInterval)
	}
}

func (s *ContentModerationService) runCleanupOnce() {
	if s == nil || s.repo == nil || s.settingRepo == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), contentModerationCleanupTimeout)
	defer cancel()
	cfg, err := s.loadConfig(ctx)
	if err != nil {
		slog.Warn("content_moderation.cleanup_load_config_failed", "error", err)
		return
	}
	now := time.Now()
	hitBefore := now.AddDate(0, 0, -cfg.HitRetentionDays)
	nonHitBefore := now.AddDate(0, 0, -cfg.NonHitRetentionDays)
	result, err := s.repo.CleanupExpiredLogs(ctx, hitBefore, nonHitBefore)
	if err != nil {
		slog.Warn("content_moderation.cleanup_failed", "error", err)
		return
	}
	if result == nil {
		return
	}
	s.lastCleanupUnix.Store(result.FinishedAt.Unix())
	s.lastCleanupDeletedHit.Store(result.DeletedHit)
	s.lastCleanupDeletedNonHit.Store(result.DeletedNonHit)
}

func (s *ContentModerationService) loadConfig(ctx context.Context) (*ContentModerationConfig, error) {
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyContentModerationConfig)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return parseContentModerationConfig("")
		}
		return nil, fmt.Errorf("get content moderation config: %w", err)
	}
	return parseContentModerationConfig(raw)
}

// parseContentModerationConfig 统一处理数据库中的空配置和配置归一化，供直接读取与快照刷新复用。
func parseContentModerationConfig(raw string) (*ContentModerationConfig, error) {
	cfg := defaultContentModerationConfig()
	if strings.TrimSpace(raw) == "" {
		cfg.normalize()
		return cfg, nil
	}
	if err := json.Unmarshal([]byte(raw), cfg); err != nil {
		return nil, infraerrors.BadRequest("INVALID_CONTENT_MODERATION_CONFIG", "内容审计配置不是有效 JSON")
	}
	cfg.normalize()
	return cfg, nil
}

// loadRuntimeSnapshot 首次同步加载配置；快照过期后仍立即返回旧值，并触发后台刷新。
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

	s.runtimeRefreshMu.Lock()
	defer s.runtimeRefreshMu.Unlock()
	if snapshot := s.runtimeSnapshot.Load(); snapshot != nil {
		return snapshot, nil
	}
	return s.refreshRuntimeSnapshot(ctx)
}

func (s *ContentModerationService) runtimeSnapshotTTL() time.Duration {
	if s != nil && s.runtimeCacheTTL > 0 {
		return s.runtimeCacheTTL
	}
	return contentModerationRuntimeCacheTTL
}

// triggerRuntimeSnapshotRefresh 只允许一个后台刷新任务运行，并在失败后按缓存周期退避。
func (s *ContentModerationService) triggerRuntimeSnapshotRefresh() {
	if s == nil || s.runtimeRefreshDeferred() || !s.runtimeRefreshMu.TryLock() {
		return
	}
	if s.runtimeRefreshDeferred() {
		s.runtimeRefreshMu.Unlock()
		return
	}
	go func() {
		defer s.runtimeRefreshMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), contentModerationRuntimeRefreshTimeout)
		defer cancel()
		if _, err := s.refreshRuntimeSnapshot(ctx); err != nil {
			s.runtimeRefreshRetryAt.Store(time.Now().Add(s.runtimeSnapshotTTL()).UnixNano())
			slog.Warn("content_moderation.runtime_snapshot_refresh_failed", "error", err)
		}
	}()
}

func (s *ContentModerationService) runtimeRefreshDeferred() bool {
	if s == nil {
		return false
	}
	return time.Now().UnixNano() < s.runtimeRefreshRetryAt.Load()
}

// refreshRuntimeSnapshot 一次读取开关与配置；配置未变时复用已构建的关键词索引。
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
	s.runtimeSnapshot.Store(snapshot)
	s.runtimeRefreshRetryAt.Store(0)
	return snapshot, nil
}

// replaceRuntimeConfig 在配置写入成功后立即替换已有快照，避免等待缓存自然过期。
func (s *ContentModerationService) replaceRuntimeConfig(cfg *ContentModerationConfig, raw []byte) {
	if s == nil || cfg == nil {
		return
	}
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
	s.runtimeSnapshot.Store(&contentModerationRuntimeSnapshot{
		riskControlEnabled: current.riskControlEnabled,
		config:             config,
		keywordMatcher:     keywordMatcher,
		configDigest:       configDigest,
		loadedAt:           time.Now(),
	})
}

// matchBlockedKeyword 优先使用预构建索引，并保留旧实现作为空索引时的兼容路径。
func (s *contentModerationRuntimeSnapshot) matchBlockedKeyword(text string) (string, bool) {
	if s == nil || s.config == nil {
		return "", false
	}
	if s.keywordMatcher != nil {
		return s.keywordMatcher.Match(text)
	}
	return matchBlockedKeyword(text, s.config.BlockedKeywords)
}

func (s *ContentModerationService) isRiskControlEnabled(ctx context.Context) bool {
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyRiskControlEnabled)
	if err != nil {
		return false
	}
	return raw == "true"
}

func (s *ContentModerationService) validateConfig(ctx context.Context, cfg *ContentModerationConfig) error {
	if cfg == nil {
		return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_CONFIG", "内容审计配置不能为空")
	}
	cfg.normalize()
	switch cfg.Mode {
	case ContentModerationModeOff, ContentModerationModeObserve, ContentModerationModePreBlock:
	default:
		return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_MODE", "内容审计模式无效")
	}
	if _, err := url.ParseRequestURI(cfg.BaseURL); err != nil {
		return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_BASE_URL", "OpenAI Base URL 无效")
	}
	if cfg.ProxyID != nil {
		if s.proxyRepo == nil {
			return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_PROXY", "代理仓储不可用")
		}
		if _, err := s.proxyRepo.GetByID(ctx, *cfg.ProxyID); err != nil {
			return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_PROXY", fmt.Sprintf("代理服务器不存在: %d", *cfg.ProxyID))
		}
	}
	if cfg.BlockStatus < 400 || cfg.BlockStatus > 599 {
		return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_BLOCK_STATUS", "拦截 HTTP 状态码必须在 400-599 之间")
	}
	if cfg.ModelFilter.Type != ContentModerationModelFilterAll && len(cfg.ModelFilter.Models) == 0 {
		return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_MODEL_FILTER", "指定或排除模型时至少需要配置 1 个模型")
	}
	if !cfg.AllGroups && len(cfg.GroupIDs) > 0 && s.groupRepo != nil {
		for _, groupID := range cfg.GroupIDs {
			if _, err := s.groupRepo.GetByIDLite(ctx, groupID); err != nil {
				return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_GROUP", fmt.Sprintf("审计分组不存在: %d", groupID))
			}
		}
	}
	return nil
}

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

func (s *ContentModerationService) callModerationBatch(ctx context.Context, cfg *ContentModerationConfig, input any, trackKeyLoad ...bool) ([]moderationAPIResult, error) {
	attempts := cfg.RetryCount + 1
	if attempts <= 0 {
		attempts = 1
	}
	if attempts > maxContentModerationRetryCount+1 {
		attempts = maxContentModerationRetryCount + 1
	}
	trackLoad := len(trackKeyLoad) > 0 && trackKeyLoad[0]
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
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
		if httpStatus == http.StatusBadRequest {
			break
		}
		if attempt == attempts-1 {
			break
		}
		wait := time.Duration(100*(attempt+1)) * time.Millisecond
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
	return nil, lastErr
}

func (s *ContentModerationService) callModerationOnceWithInput(ctx context.Context, cfg *ContentModerationConfig, apiKey string, input any, httpStatus *int) ([]moderationAPIResult, error) {
	base := strings.TrimRight(cfg.BaseURL, "/")
	endpoint, err := url.JoinPath(base, "/v1/moderations")
	if err != nil {
		return nil, err
	}
	payload := moderationAPIRequest{
		Model: cfg.Model,
		Input: input,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(cfg.TimeoutMS) * time.Millisecond
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client, err := s.moderationHTTPClient(ctx, cfg)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if httpStatus != nil {
		*httpStatus = resp.StatusCode
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, &contentModerationAPIError{StatusCode: resp.StatusCode, Message: strings.TrimSpace(string(body))}
	}
	var out moderationAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Results) == 0 {
		return nil, errors.New("moderation api returned empty results")
	}
	return out.Results, nil
}

// moderationProxyURLCacheEntry 缓存代理 ID 到 URL 的解析，避免审核热路径逐请求查库。
type moderationProxyURLCacheEntry struct {
	proxyID   int64
	url       string
	expiresAt time.Time
}

const contentModerationProxyURLCacheTTL = time.Minute

// moderationHTTPClient 返回审计请求客户端。代理解析或构建失败时直接报错，
// 绝不回退直连，避免在管理员预期走代理时泄露真实出口地址。
func (s *ContentModerationService) moderationHTTPClient(ctx context.Context, cfg *ContentModerationConfig) (*http.Client, error) {
	if cfg == nil || cfg.ProxyID == nil {
		if s.httpClient == nil {
			return http.DefaultClient, nil
		}
		return s.httpClient, nil
	}
	proxyURL, err := s.resolveModerationProxyURL(ctx, *cfg.ProxyID)
	if err != nil {
		return nil, err
	}
	client, err := httpclient.GetClient(httpclient.Options{ProxyURL: proxyURL})
	if err != nil {
		return nil, fmt.Errorf("build moderation proxy client: %w", err)
	}
	return client, nil
}

func (s *ContentModerationService) resolveModerationProxyURL(ctx context.Context, proxyID int64) (string, error) {
	now := time.Now()
	previous := s.moderationProxyCache.Load()
	if previous != nil && previous.proxyID == proxyID && now.Before(previous.expiresAt) {
		return previous.url, nil
	}
	if s.proxyRepo == nil {
		return "", errors.New("moderation proxy repository unavailable")
	}
	proxy, err := s.proxyRepo.GetByID(ctx, proxyID)
	if err != nil {
		return "", fmt.Errorf("resolve moderation proxy %d: %w", proxyID, err)
	}
	if !proxy.IsActive() || proxy.IsExpired(now) {
		slog.Warn("content_moderation.proxy_not_active",
			"proxy_id", proxyID,
			"proxy_name", proxy.Name,
			"status", proxy.Status,
			"expired", proxy.IsExpired(now))
	}
	proxyURL := proxy.URL()
	if previous == nil || previous.proxyID != proxyID || previous.url != proxyURL {
		// 日志只记录无凭据地址，不能输出完整代理 URL。
		slog.Info("content_moderation.proxy_enabled",
			"proxy_id", proxyID,
			"proxy_name", proxy.Name,
			"proxy_addr", fmt.Sprintf("%s://%s:%d", proxy.Protocol, proxy.Host, proxy.Port))
	}
	s.moderationProxyCache.Store(&moderationProxyURLCacheEntry{
		proxyID:   proxyID,
		url:       proxyURL,
		expiresAt: now.Add(contentModerationProxyURLCacheTTL),
	})
	return proxyURL, nil
}

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
		InputExcerpt:      sanitizeContentModerationExcerpt(content.Text, maxModerationExcerptRunes),
		Source:            content.Source,
		InputItems:        append([]ContentModerationInputItem(nil), content.Items...),
		ContentComplete:   true,
		AuditComplete:     true,
		TextUnitCount:     countContentModerationTextChunks(content.Text, maxModerationInputRunes, contentModerationChunkOverlap),
		ImageUnitCount:    len(content.ImageItems),
		UpstreamLatencyMS: latency,
		QueueDelayMS:      queueDelay,
		Error:             errText,
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

// buildLog 保留旧测试和内部调用兼容，新的审核路径统一使用结构化版本。
func (s *ContentModerationService) buildLog(input ContentModerationCheckInput, cfg *ContentModerationConfig, action string, flagged bool, highestCategory string, highestScore float64, scores map[string]float64, text string, latency *int, queueDelay *int, errText string) *ContentModerationLog {
	content := ContentModerationInput{Text: text}
	content.Normalize()
	return s.buildStructuredLog(input, cfg, action, flagged, highestCategory, highestScore, scores, content, latency, queueDelay, errText, nil)
}

func (s *ContentModerationService) persistContentModerationLog(ctx context.Context, cfg *ContentModerationConfig, log *ContentModerationLog, hashText string, recordHash bool, applySideEffects bool) {
	if s == nil || log == nil {
		return
	}
	if recordHash && s.hashCache != nil {
		if err := s.hashCache.RecordFlaggedInputHash(ctx, hashText); err != nil {
			slog.Warn("content_moderation.record_hash_failed", "user_id", contentModerationEmailUserID(log), "endpoint", log.Endpoint, "error", err)
		}
	}
	autoBanJustApplied := false
	if applySideEffects {
		autoBanJustApplied = s.applyFlaggedAccountSideEffects(ctx, cfg, log)
		s.sendFlaggedNotificationSideEffects(ctx, cfg, log, autoBanJustApplied)
	}
	if len(log.Media) > 0 {
		log.Media = s.snapshotContentModerationMedia(ctx, log.Media)
	}
	if s.repo != nil {
		if err := s.repo.CreateLog(ctx, log); err != nil {
			slog.Warn("content_moderation.create_log_failed", "user_id", contentModerationEmailUserID(log), "endpoint", log.Endpoint, "action", log.Action, "error", err)
			return
		}
	}
}

func (s *ContentModerationService) applyFlaggedAccountSideEffects(ctx context.Context, cfg *ContentModerationConfig, log *ContentModerationLog) bool {
	if s == nil || cfg == nil || log == nil || !log.Flagged || log.UserID == nil || *log.UserID <= 0 {
		return false
	}
	count := 1
	if s.repo != nil && cfg.ViolationWindowHours > 0 {
		since := time.Now().Add(-time.Duration(cfg.ViolationWindowHours) * time.Hour)
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
		if user.IsAdmin() {
			slog.Warn("content_moderation.autoban_skipped_admin", "user_id", *log.UserID, "role", user.Role, "count", count, "threshold", cfg.BanThreshold)
			// TODO: 这里接入 API Key 写能力后，应改为禁用触发本次审计命中的 API Key。
			return false
		}
		if user.Status != StatusDisabled {
			user.Status = StatusDisabled
			if err := s.userRepo.Update(ctx, user, UserUpdateFields{Status: true}); err != nil {
				slog.Warn("content_moderation.ban_update_user_failed", "user_id", *log.UserID, "error", err)
				return false
			}
			if s.authCacheInvalidator != nil {
				s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, *log.UserID)
			}
			autoBanJustApplied = true
		}
		log.AutoBanned = true
	}
	return autoBanJustApplied
}

func (s *ContentModerationService) sendFlaggedNotificationSideEffects(ctx context.Context, cfg *ContentModerationConfig, log *ContentModerationLog, autoBanJustApplied bool) {
	if s == nil || cfg == nil || log == nil || !log.Flagged {
		return
	}
	if s.emailService == nil || strings.TrimSpace(log.UserEmail) == "" {
		return
	}
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
	var accountID *int64
	if input.AccountID > 0 {
		accountID = &input.AccountID
	}
	content := input.Content
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
		AccountID:      accountID,
		AccountName:    strings.TrimSpace(input.AccountName),
		Endpoint:       strings.TrimSpace(input.Endpoint),
		Model:          strings.TrimSpace(input.Model),
		UpstreamStatus: input.UpstreamStatus,
		// Cyber 警告用于管理员复盘上游误杀/命中原因，需要保留原始文本；这里只做长度裁剪，避免 UI 和存储被超长内容撑爆。
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

func (s *ContentModerationService) applyCyberWarningPostCommitSideEffects(ctx context.Context, cfg *ContentModerationConfig, warning *ContentModerationCyberWarning, autoBanJustApplied bool) {
	if s == nil || cfg == nil || warning == nil || warning.UserID == nil || *warning.UserID <= 0 || !autoBanJustApplied {
		return
	}
	if s.authCacheInvalidator != nil {
		s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, *warning.UserID)
	}
	if s.emailService == nil || strings.TrimSpace(warning.UserEmail) == "" {
		return
	}
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

func (s *ContentModerationService) sendViolationEmail(ctx context.Context, cfg *ContentModerationConfig, log *ContentModerationLog) error {
	siteName := s.siteName(ctx)
	if s.emailService.notificationEmailService != nil {
		if err := s.emailService.notificationEmailService.Send(ctx, NotificationEmailSendInput{
			Event:          NotificationEmailEventContentModerationViolation,
			RecipientEmail: log.UserEmail,
			RecipientName:  emailRecipientName(log.UserEmail),
			UserID:         contentModerationEmailUserID(log),
			SourceType:     "content_moderation",
			SourceID:       contentModerationEmailSourceID(log),
			Variables:      contentModerationEmailVariables(log, cfg),
		}); err == nil {
			return nil
		} else {
			if !shouldFallbackNotificationEmail(err) {
				return err
			}
			slog.Warn("template content moderation violation email failed; falling back to built-in body", "log_id", log.ID, "recipient_hash", notificationEmailHash(log.UserEmail), "err", err.Error())
		}
	}
	subject := fmt.Sprintf("[%s] 账户风控提醒 / Risk Control Notice", sanitizeEmailHeader(siteName))
	body := buildContentModerationViolationEmailBody(siteName, log, cfg)
	return s.emailService.SendEmail(ctx, log.UserEmail, subject, body)
}

func (s *ContentModerationService) sendAccountDisabledEmail(ctx context.Context, cfg *ContentModerationConfig, log *ContentModerationLog) error {
	siteName := s.siteName(ctx)
	if s.emailService.notificationEmailService != nil {
		if err := s.emailService.notificationEmailService.Send(ctx, NotificationEmailSendInput{
			Event:          NotificationEmailEventContentModerationDisabled,
			RecipientEmail: log.UserEmail,
			RecipientName:  emailRecipientName(log.UserEmail),
			UserID:         contentModerationEmailUserID(log),
			SourceType:     "content_moderation",
			SourceID:       contentModerationEmailSourceID(log),
			Variables:      contentModerationEmailVariables(log, cfg),
		}); err == nil {
			return nil
		} else {
			if !shouldFallbackNotificationEmail(err) {
				return err
			}
			slog.Warn("template content moderation disabled email failed; falling back to built-in body", "log_id", log.ID, "recipient_hash", notificationEmailHash(log.UserEmail), "err", err.Error())
		}
	}
	subject := fmt.Sprintf("[%s] 账户已被禁用 / Account Disabled", sanitizeEmailHeader(siteName))
	body := buildContentModerationAccountDisabledEmailBody(siteName, log, cfg)
	return s.emailService.SendEmail(ctx, log.UserEmail, subject, body)
}

func (s *ContentModerationService) sendCyberAccountDisabledEmail(ctx context.Context, cfg *ContentModerationConfig, warning *ContentModerationCyberWarning) error {
	siteName := s.siteName(ctx)
	if s.emailService.notificationEmailService != nil {
		if err := s.emailService.notificationEmailService.Send(ctx, NotificationEmailSendInput{
			Event:          NotificationEmailEventContentModerationDisabled,
			RecipientEmail: warning.UserEmail,
			RecipientName:  emailRecipientName(warning.UserEmail),
			UserID:         contentModerationCyberEmailUserID(warning),
			SourceType:     "content_moderation_cyber",
			SourceID:       contentModerationCyberEmailSourceID(warning),
			Variables:      contentModerationCyberEmailVariables(warning, cfg),
		}); err == nil {
			return nil
		} else {
			if !shouldFallbackNotificationEmail(err) {
				return err
			}
			slog.Warn("template cyber content moderation disabled email failed; falling back to built-in body", "warning_id", warning.ID, "recipient_hash", notificationEmailHash(warning.UserEmail), "err", err.Error())
		}
	}
	subject := fmt.Sprintf("[%s] 账户已被禁用 / Account Disabled", sanitizeEmailHeader(siteName))
	body := buildContentModerationCyberAccountDisabledEmailBody(siteName, warning, cfg)
	return s.emailService.SendEmail(ctx, warning.UserEmail, subject, body)
}

func contentModerationEmailUserID(log *ContentModerationLog) int64 {
	if log == nil || log.UserID == nil {
		return 0
	}
	return *log.UserID
}

func contentModerationEmailSourceID(log *ContentModerationLog) string {
	if log == nil || log.ID <= 0 {
		return ""
	}
	return fmt.Sprintf("%d", log.ID)
}

func contentModerationEmailVariables(log *ContentModerationLog, cfg *ContentModerationConfig) map[string]string {
	variables := map[string]string{
		"triggered_at":        time.Now().UTC().Format(time.RFC3339),
		"group_name":          "-",
		"moderation_category": "-",
		"moderation_score":    "0.000",
		"violation_count":     "0",
		"ban_threshold":       "0",
	}
	if log != nil {
		if !log.CreatedAt.IsZero() {
			variables["triggered_at"] = log.CreatedAt.UTC().Format(time.RFC3339)
		}
		if strings.TrimSpace(log.GroupName) != "" {
			variables["group_name"] = strings.TrimSpace(log.GroupName)
		}
		if strings.TrimSpace(log.HighestCategory) != "" {
			variables["moderation_category"] = strings.TrimSpace(log.HighestCategory)
		}
		variables["moderation_score"] = fmt.Sprintf("%.3f", log.HighestScore)
		variables["violation_count"] = fmt.Sprintf("%d", log.ViolationCount)
	}
	if cfg != nil {
		variables["ban_threshold"] = fmt.Sprintf("%d", cfg.BanThreshold)
	}
	return variables
}

func contentModerationCyberEmailUserID(warning *ContentModerationCyberWarning) int64 {
	if warning == nil || warning.UserID == nil {
		return 0
	}
	return *warning.UserID
}

func contentModerationCyberEmailSourceID(warning *ContentModerationCyberWarning) string {
	if warning == nil || warning.ID <= 0 {
		return ""
	}
	return fmt.Sprintf("%d", warning.ID)
}

func contentModerationCyberEmailVariables(warning *ContentModerationCyberWarning, cfg *ContentModerationConfig) map[string]string {
	variables := map[string]string{
		"triggered_at":        time.Now().UTC().Format(time.RFC3339),
		"group_name":          "-",
		"moderation_category": "cyber",
		"moderation_score":    "0.000",
		"violation_count":     "0",
		"ban_threshold":       "0",
	}
	if warning != nil {
		if !warning.CreatedAt.IsZero() {
			variables["triggered_at"] = warning.CreatedAt.UTC().Format(time.RFC3339)
		}
		if strings.TrimSpace(warning.GroupName) != "" {
			variables["group_name"] = strings.TrimSpace(warning.GroupName)
		}
	}
	if cfg != nil {
		variables["ban_threshold"] = fmt.Sprintf("%d", cfg.CyberBanThreshold)
	}
	return variables
}

func (s *ContentModerationService) siteName(ctx context.Context) string {
	if s == nil || s.settingRepo == nil {
		return "Sub2API"
	}
	name, err := s.settingRepo.GetValue(ctx, SettingKeySiteName)
	if err != nil || strings.TrimSpace(name) == "" {
		return "Sub2API"
	}
	return strings.TrimSpace(name)
}

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
	if cfg.TimeoutMS > maxContentModerationTimeoutMS {
		cfg.TimeoutMS = maxContentModerationTimeoutMS
	}
	if cfg.SampleRate < 0 {
		cfg.SampleRate = 0
	}
	if cfg.SampleRate > 100 {
		cfg.SampleRate = 100
	}
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = defaultContentModerationWorkerCount
	}
	if cfg.WorkerCount > maxContentModerationWorkerCount {
		cfg.WorkerCount = maxContentModerationWorkerCount
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultContentModerationQueueSize
	}
	if cfg.QueueSize > maxContentModerationQueueSize {
		cfg.QueueSize = maxContentModerationQueueSize
	}
	if strings.TrimSpace(cfg.BlockMessage) == "" {
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
	if cfg.RetryCount > maxContentModerationRetryCount {
		cfg.RetryCount = maxContentModerationRetryCount
	}
	if cfg.HitRetentionDays <= 0 {
		cfg.HitRetentionDays = defaultContentModerationHitRetentionDays
	}
	if cfg.HitRetentionDays > maxContentModerationRetentionDays {
		cfg.HitRetentionDays = maxContentModerationRetentionDays
	}
	if cfg.NonHitRetentionDays <= 0 {
		cfg.NonHitRetentionDays = defaultContentModerationNonHitRetentionDays
	}
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
	if cfg.AuditUserTextMaxChars > maxContentModerationAuditTextChars {
		cfg.AuditUserTextMaxChars = maxContentModerationAuditTextChars
	}
	if cfg.AuditToolOutputMaxChars <= 0 {
		cfg.AuditToolOutputMaxChars = defaultContentModerationAuditTextChars
	}
	if cfg.AuditToolOutputMaxChars > maxContentModerationAuditTextChars {
		cfg.AuditToolOutputMaxChars = maxContentModerationAuditTextChars
	}
	cfg.GroupIDs = normalizeInt64IDs(cfg.GroupIDs)
	cfg.Thresholds = mergeContentModerationThresholds(ContentModerationDefaultThresholds(), cfg.Thresholds)
	cfg.BlockedKeywords = normalizeBlockedKeywords(cfg.BlockedKeywords)
	cfg.KeywordBlockingMode = normalizeKeywordBlockingMode(cfg.KeywordBlockingMode)
	cfg.ModelFilter = normalizeContentModerationModelFilter(cfg.ModelFilter)
}

func (cfg *ContentModerationConfig) includesGroup(groupID *int64) bool {
	if cfg.AllGroups {
		return true
	}
	if groupID == nil {
		return false
	}
	for _, id := range cfg.GroupIDs {
		if id == *groupID {
			return true
		}
	}
	return false
}

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

func contentModerationLogGroupID(groupID *int64) int64 {
	if groupID == nil {
		return 0
	}
	return *groupID
}

func (cfg *ContentModerationConfig) shouldSample(hashText string) bool {
	if cfg.SampleRate >= 100 {
		return true
	}
	if cfg.SampleRate <= 0 {
		return false
	}
	raw, err := hex.DecodeString(hashText)
	if err != nil || len(raw) < 2 {
		return true
	}
	return int(binary.BigEndian.Uint16(raw[:2])%100) < cfg.SampleRate
}

func (cfg *ContentModerationConfig) apiKeys() []string {
	if cfg == nil {
		return nil
	}
	return normalizeModerationAPIKeys(cfg.APIKeys)
}

type contentModerationAPIKeyRuntimeEntry struct {
	Key      string
	KeyHash  string
	Priority int
	Note     string
}

func (cfg *ContentModerationConfig) apiKeyEntries() []contentModerationAPIKeyRuntimeEntry {
	if cfg == nil {
		return nil
	}
	keys := cfg.apiKeys()
	metadata := make(map[string]ContentModerationAPIKeyMetadata, len(cfg.APIKeyMetadata))
	for _, item := range normalizeContentModerationAPIKeyMetadata(keys, cfg.APIKeyMetadata) {
		metadata[item.KeyHash] = item
	}
	entries := make([]contentModerationAPIKeyRuntimeEntry, 0, len(keys))
	for _, key := range keys {
		hash := moderationAPIKeyHash(key)
		item := metadata[hash]
		entries = append(entries, contentModerationAPIKeyRuntimeEntry{
			Key:      key,
			KeyHash:  hash,
			Priority: normalizeContentModerationAPIKeyPriority(item.Priority),
			Note:     trimRunes(strings.TrimSpace(item.Note), maxContentModerationAPIKeyNoteRunes),
		})
	}
	return entries
}

func (s *ContentModerationService) nextUsableAPIKey(cfg *ContentModerationConfig) (string, bool) {
	entries := cfg.apiKeyEntries()
	if len(entries) == 0 || s == nil {
		return "", false
	}
	now := time.Now()
	s.keyHealthMu.Lock()
	defer s.keyHealthMu.Unlock()
	if s.keySchedule == nil {
		s.keySchedule = make(map[string]int64)
	}
	// 平滑加权轮询避免高优先级 Key 集中突发，同时自动排除仍在冻结期内的 Key。
	var selected *contentModerationAPIKeyRuntimeEntry
	var selectedWeight int64
	var totalWeight int64
	for index := range entries {
		entry := &entries[index]
		state := s.keyHealth[entry.KeyHash]
		if state != nil && state.FrozenUntil.After(now) {
			continue
		}
		weight := int64(entry.Priority)
		totalWeight += weight
		s.keySchedule[entry.KeyHash] += weight
		current := s.keySchedule[entry.KeyHash]
		if selected == nil || current > selectedWeight {
			selected = entry
			selectedWeight = current
		}
	}
	if selected == nil || totalWeight <= 0 {
		return "", false
	}
	s.keySchedule[selected.KeyHash] -= totalWeight
	return selected.Key, true
}

func (s *ContentModerationService) isAPIKeyFrozen(key string, now time.Time) bool {
	hash := moderationAPIKeyHash(key)
	if hash == "" || s == nil {
		return false
	}
	s.keyHealthMu.Lock()
	defer s.keyHealthMu.Unlock()
	state := s.keyHealth[hash]
	return state != nil && state.FrozenUntil.After(now)
}

func (s *ContentModerationService) beginModerationAPIKeyCall(key string) {
	hash := moderationAPIKeyHash(key)
	if hash == "" || s == nil {
		return
	}
	s.keyHealthMu.Lock()
	defer s.keyHealthMu.Unlock()
	state := s.ensureAPIKeyHealthLocked(hash, maskSecretTail(key))
	state.SyncActive++
}

func (s *ContentModerationService) finishModerationAPIKeyCall(key string, latencyMS int, success bool) {
	hash := moderationAPIKeyHash(key)
	if hash == "" || s == nil {
		return
	}
	if latencyMS < 0 {
		latencyMS = 0
	}
	s.keyHealthMu.Lock()
	defer s.keyHealthMu.Unlock()
	state := s.ensureAPIKeyHealthLocked(hash, maskSecretTail(key))
	if state.SyncActive > 0 {
		state.SyncActive--
	}
	state.SyncTotal++
	state.SyncLatencyMS += int64(latencyMS)
	if success {
		state.SyncSuccess++
		return
	}
	state.SyncErrors++
}

func (s *ContentModerationService) markAPIKeySuccess(key string, latencyMS int, httpStatus int) {
	hash := moderationAPIKeyHash(key)
	if hash == "" || s == nil {
		return
	}
	s.keyHealthMu.Lock()
	defer s.keyHealthMu.Unlock()
	state := s.ensureAPIKeyHealthLocked(hash, maskSecretTail(key))
	state.FailureCount = 0
	state.SuccessCount++
	state.LastError = ""
	state.LastCheckedAt = time.Now()
	state.FrozenUntil = time.Time{}
	state.LastLatencyMS = latencyMS
	state.LastHTTPStatus = httpStatus
	state.LastTested = true
}

func (s *ContentModerationService) markAPIKeyError(key string, errText string, latencyMS int, httpStatus int) {
	hash := moderationAPIKeyHash(key)
	if hash == "" || s == nil {
		return
	}
	s.keyHealthMu.Lock()
	defer s.keyHealthMu.Unlock()
	state := s.ensureAPIKeyHealthLocked(hash, maskSecretTail(key))
	if contentModerationFreezeDurationForHTTPStatus(httpStatus) > 0 {
		state.FailureCount++
	}
	state.LastError = trimRunes(errText, 180)
	state.LastCheckedAt = time.Now()
	state.LastLatencyMS = latencyMS
	state.LastHTTPStatus = httpStatus
	state.LastTested = true
	if freezeDuration := contentModerationFreezeDurationForHTTPStatus(httpStatus); freezeDuration > 0 {
		state.FrozenUntil = time.Now().Add(freezeDuration)
	}
}

func contentModerationFreezeDurationForHTTPStatus(httpStatus int) time.Duration {
	switch httpStatus {
	case 0, http.StatusBadRequest:
		return 0
	case http.StatusUnauthorized, http.StatusForbidden:
		return contentModerationKeyAuthFreezeDuration
	case http.StatusTooManyRequests, 529:
		return contentModerationKeyRateLimitFreezeDuration
	default:
		return contentModerationKeyHTTPErrorFreezeDuration
	}
}

func (s *ContentModerationService) ensureAPIKeyHealthLocked(hash string, masked string) *contentModerationKeyHealth {
	if s.keyHealth == nil {
		s.keyHealth = make(map[string]*contentModerationKeyHealth)
	}
	state := s.keyHealth[hash]
	if state == nil {
		state = &contentModerationKeyHealth{Hash: hash}
		s.keyHealth[hash] = state
	}
	if strings.TrimSpace(masked) != "" {
		state.Masked = masked
	}
	return state
}

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

func (s *ContentModerationService) apiKeyStatuses(cfg *ContentModerationConfig) []ContentModerationAPIKeyStatus {
	entries := cfg.apiKeyEntries()
	out := make([]ContentModerationAPIKeyStatus, 0, len(entries))
	for idx, entry := range entries {
		out = append(out, s.apiKeyStatusForHash(idx, entry.KeyHash, maskSecretTail(entry.Key), true, entry.Priority, entry.Note))
	}
	return out
}

func (s *ContentModerationService) preBlockAPIKeyLoads(cfg *ContentModerationConfig) []ContentModerationAPIKeyLoad {
	entries := cfg.apiKeyEntries()
	out := make([]ContentModerationAPIKeyLoad, 0, len(entries))
	for idx, entry := range entries {
		out = append(out, s.preBlockAPIKeyLoadForHash(idx, entry.KeyHash, maskSecretTail(entry.Key), entry.Priority, entry.Note))
	}
	return out
}

func (s *ContentModerationService) preBlockAPIKeyActive(cfg *ContentModerationConfig) int64 {
	var total int64
	for _, item := range s.preBlockAPIKeyLoads(cfg) {
		total += item.Active
	}
	return total
}

func (s *ContentModerationService) preBlockAPIKeyAvailableCount(cfg *ContentModerationConfig) int64 {
	now := time.Now()
	var count int64
	for _, entry := range cfg.apiKeyEntries() {
		if !s.isAPIKeyFrozen(entry.Key, now) {
			count++
		}
	}
	return count
}

func (s *ContentModerationService) preBlockAPIKeyTotalCalls(cfg *ContentModerationConfig) int64 {
	var total int64
	for _, item := range s.preBlockAPIKeyLoads(cfg) {
		total += item.Total
	}
	return total
}

func (s *ContentModerationService) preBlockAPIKeyLoadForHash(index int, hash string, masked string, priority int, note string) ContentModerationAPIKeyLoad {
	load := ContentModerationAPIKeyLoad{
		Index:    index,
		KeyHash:  hash,
		Masked:   masked,
		Status:   "unknown",
		Priority: priority,
		Note:     note,
	}
	status := s.apiKeyStatusForHash(index, hash, masked, true, priority, note)
	load.Status = status.Status
	load.LastLatencyMS = status.LastLatencyMS
	load.LastHTTPStatus = status.LastHTTPStatus
	if hash == "" || s == nil {
		return load
	}
	s.keyHealthMu.Lock()
	defer s.keyHealthMu.Unlock()
	state := s.keyHealth[hash]
	if state == nil {
		return load
	}
	load.Active = state.SyncActive
	load.Total = state.SyncTotal
	load.Success = state.SyncSuccess
	load.Errors = state.SyncErrors
	if state.SyncTotal > 0 {
		load.AvgLatencyMS = state.SyncLatencyMS / state.SyncTotal
	}
	return load
}

func (s *ContentModerationService) apiKeyStatusForHash(index int, hash string, masked string, configured bool, priority int, note string) ContentModerationAPIKeyStatus {
	status := ContentModerationAPIKeyStatus{
		Index:      index,
		KeyHash:    hash,
		Masked:     masked,
		Status:     "unknown",
		Configured: configured,
		Priority:   normalizeContentModerationAPIKeyPriority(priority),
		Note:       note,
	}
	if hash == "" || s == nil {
		return status
	}
	now := time.Now()
	s.keyHealthMu.Lock()
	defer s.keyHealthMu.Unlock()
	state := s.keyHealth[hash]
	if state == nil {
		return status
	}
	status.FailureCount = state.FailureCount
	status.SuccessCount = state.SuccessCount
	status.LastError = state.LastError
	status.LastLatencyMS = state.LastLatencyMS
	status.LastHTTPStatus = state.LastHTTPStatus
	status.LastTested = state.LastTested
	if !state.LastCheckedAt.IsZero() {
		t := state.LastCheckedAt
		status.LastCheckedAt = &t
	}
	if state.FrozenUntil.After(now) {
		t := state.FrozenUntil
		status.FrozenUntil = &t
		status.Status = "frozen"
		return status
	}
	if state.LastError != "" {
		status.Status = "error"
		return status
	}
	if state.SuccessCount > 0 || state.LastTested {
		status.Status = "ok"
	}
	return status
}

func moderationAPIKeyHash(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func buildModerationTestInput(prompt string, images []string) (any, int, error) {
	prompt = trimRunes(normalizeContentModerationText(prompt), maxModerationInputRunes)
	normalizedImages := make([]string, 0, len(images))
	for _, image := range images {
		image = strings.TrimSpace(image)
		if image == "" {
			continue
		}
		if len(normalizedImages) >= maxContentModerationTestImages {
			return nil, 0, infraerrors.BadRequest("TOO_MANY_MODERATION_TEST_IMAGES", fmt.Sprintf("最多上传 %d 张测试图片", maxContentModerationTestImages))
		}
		if err := validateModerationTestImageDataURL(image); err != nil {
			return nil, 0, err
		}
		normalizedImages = append(normalizedImages, image)
	}
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

func validateModerationTestImageDataURL(value string) error {
	if len(value) > maxContentModerationTestImageDataURLBytes {
		return infraerrors.BadRequest("MODERATION_TEST_IMAGE_TOO_LARGE", "测试图片不能超过 8MB")
	}
	if !strings.HasPrefix(value, "data:image/") {
		return infraerrors.BadRequest("INVALID_MODERATION_TEST_IMAGE", "测试图片必须是 data:image/* base64")
	}
	parts := strings.SplitN(value, ",", 2)
	if len(parts) != 2 || !strings.Contains(parts[0], ";base64") {
		return infraerrors.BadRequest("INVALID_MODERATION_TEST_IMAGE", "测试图片必须是 base64 data URL")
	}
	raw, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return infraerrors.BadRequest("INVALID_MODERATION_TEST_IMAGE", "测试图片 base64 无效")
	}
	if len(raw) > maxContentModerationTestImageBytes {
		return infraerrors.BadRequest("MODERATION_TEST_IMAGE_TOO_LARGE", "测试图片不能超过 8MB")
	}
	return nil
}

func buildContentModerationTestAuditResult(result *moderationAPIResult, thresholds map[string]float64) *ContentModerationTestAuditResult {
	if result == nil {
		return nil
	}
	scores := make(map[string]float64, len(result.CategoryScores))
	for category, score := range result.CategoryScores {
		scores[category] = score
	}
	thresholdSnapshot := mergeContentModerationThresholds(ContentModerationDefaultThresholds(), thresholds)
	flagged, highestCategory, highestScore := evaluateModerationScores(scores, thresholdSnapshot)
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

type moderationAPIRequest struct {
	Model string `json:"model"`
	Input any    `json:"input"`
}

type moderationAPIInputPart struct {
	Type     string                    `json:"type"`
	Text     string                    `json:"text,omitempty"`
	ImageURL *moderationAPIImageURLRef `json:"image_url,omitempty"`
}

type moderationAPIImageURLRef struct {
	URL string `json:"url"`
}

type moderationAPIResponse struct {
	Results []moderationAPIResult `json:"results"`
}

type moderationAPIResult struct {
	Flagged        bool               `json:"flagged"`
	CategoryScores map[string]float64 `json:"category_scores"`
}

func evaluateModerationScores(scores map[string]float64, thresholds map[string]float64) (bool, string, float64) {
	flagged := false
	highestCategory := ""
	highestScore := 0.0
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
	for category, score := range scores {
		if score > highestScore || highestCategory == "" {
			highestScore = score
			highestCategory = category
		}
	}
	return flagged, highestCategory, highestScore
}

func mergeContentModerationThresholds(base map[string]float64, override map[string]float64) map[string]float64 {
	out := cloneFloatMap(base)
	if out == nil {
		out = map[string]float64{}
	}
	for _, category := range contentModerationCategoryOrder {
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

// IsOpenAICyberWarningText 判断上游错误文本是否属于 OpenAI cyber 风控拒绝。
func IsOpenAICyberWarningText(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	// 只用明确的 cyber 风控锚点命中，避免普通 usage policy/flagged 错误被当成 cyber 拒绝。
	if strings.Contains(lower, "cybersecurity risk") ||
		strings.Contains(lower, "chatgpt.com/cyber") ||
		strings.Contains(lower, "cyber abuse") ||
		strings.Contains(lower, "trusted access for cyber") {
		return true
	}
	return strings.Contains(lower, "cyber") &&
		(strings.Contains(lower, "risk") ||
			strings.Contains(lower, "abuse") ||
			strings.Contains(lower, "security work"))
}

func extractCyberWarningText(body []byte) string {
	text := strings.TrimSpace(extractUpstreamErrorMessage(body))
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
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

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
		kw = trimRunes(kw, maxContentModerationBlockedKeywordRunes)
		key := strings.ToLower(kw)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, kw)
		if len(out) >= maxContentModerationBlockedKeywords {
			break
		}
	}
	return out
}

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

func cloneContentModerationModelFilter(filter ContentModerationModelFilter) ContentModerationModelFilter {
	normalized := normalizeContentModerationModelFilter(filter)
	normalized.Models = append([]string(nil), normalized.Models...)
	return normalized
}

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

func normalizeContentModerationModelNames(models []string) []string {
	if len(models) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, raw := range models {
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
		if len(out) >= maxContentModerationModelFilterModels {
			break
		}
	}
	return out
}

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

func normalizeContentModerationAPIKeyPriority(priority int) int {
	if priority <= 0 {
		return defaultContentModerationAPIKeyPriority
	}
	if priority > maxContentModerationAPIKeyPriority {
		return maxContentModerationAPIKeyPriority
	}
	return priority
}

// normalizeContentModerationAPIKeyMetadata 按当前 Key 顺序补齐元数据，并移除已删除 Key 的残留属性。
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
			Note:     trimRunes(strings.TrimSpace(item.Note), maxContentModerationAPIKeyNoteRunes),
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
		item.Note = trimRunes(strings.TrimSpace(item.Note), maxContentModerationAPIKeyNoteRunes)
		if index, ok := indexes[item.APIKey]; ok {
			out[index] = item
			continue
		}
		indexes[item.APIKey] = len(out)
		out = append(out, item)
	}
	return out
}

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
	cfg.APIKeyMetadata = normalizeContentModerationAPIKeyMetadata(cfg.APIKeys, items)
}

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
	for _, update := range updates {
		hash := normalizeContentModerationHash(update.KeyHash)
		if _, ok := configured[hash]; !ok {
			return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_API_KEY", "要更新的审核 Key 不存在")
		}
		metadata[hash] = ContentModerationAPIKeyMetadata{
			KeyHash:  hash,
			Priority: normalizeContentModerationAPIKeyPriority(update.Priority),
			Note:     trimRunes(strings.TrimSpace(update.Note), maxContentModerationAPIKeyNoteRunes),
		}
	}
	items := make([]ContentModerationAPIKeyMetadata, 0, len(metadata))
	for _, item := range metadata {
		items = append(items, item)
	}
	cfg.APIKeyMetadata = normalizeContentModerationAPIKeyMetadata(cfg.APIKeys, items)
	return nil
}

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

func validateContentModerationAPIKeyInputs(entries *[]ContentModerationAPIKeyEntryInput, updates *[]ContentModerationAPIKeyMetadata) error {
	if entries != nil {
		for _, item := range *entries {
			if item.Priority < 0 || item.Priority > maxContentModerationAPIKeyPriority {
				return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_API_KEY_PRIORITY", "审核 Key 优先级必须在 1-1000 之间")
			}
			if utf8.RuneCountInString(strings.TrimSpace(item.Note)) > maxContentModerationAPIKeyNoteRunes {
				return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_API_KEY_NOTE", "审核 Key 备注不能超过 200 个字符")
			}
		}
	}
	if updates != nil {
		for _, item := range *updates {
			if item.Priority <= 0 || item.Priority > maxContentModerationAPIKeyPriority {
				return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_API_KEY_PRIORITY", "审核 Key 优先级必须在 1-1000 之间")
			}
			if normalizeContentModerationHash(item.KeyHash) == "" {
				return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_API_KEY", "审核 Key 标识无效")
			}
			if utf8.RuneCountInString(strings.TrimSpace(item.Note)) > maxContentModerationAPIKeyNoteRunes {
				return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_API_KEY_NOTE", "审核 Key 备注不能超过 200 个字符")
			}
		}
	}
	return nil
}

func validateContentModerationAuditTextLimit(limit *int, label string) error {
	if limit == nil {
		return nil
	}
	if *limit <= 0 || *limit > maxContentModerationAuditTextChars {
		return infraerrors.BadRequest("INVALID_CONTENT_MODERATION_AUDIT_TEXT_LIMIT", fmt.Sprintf("%s审核字符数必须在 1-%d 之间", label, maxContentModerationAuditTextChars))
	}
	return nil
}

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

func normalizeContentModerationAPIKeysMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case contentModerationAPIKeysModeReplace:
		return contentModerationAPIKeysModeReplace
	default:
		return contentModerationAPIKeysModeAppend
	}
}

func normalizeContentModerationHash(inputHash string) string {
	inputHash = strings.ToLower(strings.TrimSpace(inputHash))
	if len(inputHash) != sha256.Size*2 {
		return ""
	}
	if _, err := hex.DecodeString(inputHash); err != nil {
		return ""
	}
	return inputHash
}

func cloneFloatMap(in map[string]float64) map[string]float64 {
	if in == nil {
		return map[string]float64{}
	}
	out := make(map[string]float64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneInt64Ptr(in *int64) *int64 {
	if in == nil {
		return nil
	}
	v := *in
	return &v
}

func sanitizeContentModerationExcerpt(text string, max int) string {
	// 管理端只需要可读摘要，这里统一脱敏并截断，避免把密钥或超长提示词写入记录。
	return trimRunes(redactContentModerationSecrets(text), max)
}

func trimRawContentModerationText(text string, max int) string {
	return trimRunes(strings.TrimSpace(text), max)
}

func trimRunes(text string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max])
}

func maskSecretTail(secret string) string {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return ""
	}
	if len(secret) <= 4 {
		return "****"
	}
	return strings.Repeat("*", 8) + secret[len(secret)-4:]
}
