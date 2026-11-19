package moderation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

// service.go orchestrates synchronous and asynchronous moderation checks,
// flagged-user risk side effects, and administrator log, media, unban, and hash
// operations.
// Ordinary admission combines risk enablement, group and model scope, pre-audit
// input hashes, ordered keyword rules, sampling, and external category audits
// while delegating configuration snapshots to config.go, external API execution
// to audit.go, background queueing to queue.go, and upstream Cyber warnings to
// cyber.go.
//
// Confirmed pre-block hits reject requests with safe client errors and record
// hashes, whereas observe mode queues background work without blocking traffic.

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
