package creative

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// run.go defines creative task metadata, allowed state transitions, and public
// response conversion.
// Creative task rows distinguish public request identity, execution provider
// identity, funding snapshots, and transient output metadata. User-facing
// access pairs the owner with a normalized browser workspace; background
// recovery uses the run ID directly. Plaintext prompts and image bytes belong
// in temporary payload or media storage, while persistent rows retain their
// hashes and delivery state.
//
// Task status tracks execution and funds recovery separately from per-output
// receipt. A succeeded task can later become result_lost when its transient
// image disappears, while other terminal states cannot restart through the
// transition helper. Optional amounts and timestamps retain their absent state
// in public conversion, which allocates public output records but keeps the
// run ActualCost pointer. Reservation, capture, release, and usage identities
// are formed outside these metadata structures.

const (
	// Creative operations identify generation, reference-image editing, and
	// masked inpainting for model admission and selected platform request
	// construction.
	// prompt image generation operation.
	CreativeOperationGenerate = "generate"
	// reference image editing operation.
	CreativeOperationEdit = "edit"
	// masked image editing operation.
	CreativeOperationInpaint = "inpaint"

	// Provisioning phases are persisted after each step so recovery can resume or
	// release a partial task.
	// initial task metadata recorded.
	CreativeProvisioningPhaseCreated = "created"
	// funding reservation step completed.
	CreativeProvisioningPhaseHoldReserved = "hold_reserved"
	// temporary input writes completed.
	CreativeProvisioningPhaseTransientSaved = "transient_saved"
	// task queue admission completed.
	CreativeProvisioningPhaseEnqueued = "enqueued"
	// provisioning failure phase marker.
	CreativeProvisioningPhaseFailed = "failed"
	// provisioning recovery completion marker.
	CreativeProvisioningPhaseComplete = "complete"

	// Task states distinguish queued execution, recorded provider work, funding
	// recovery, and terminal delivery results used by workers and public queries.
	// awaiting execution admission state.
	CreativeRunStatusQueued = "queued"
	// provider attempt in progress.
	CreativeRunStatusRunning = "running"
	// provider work durably recorded.
	CreativeRunStatusProviderSucceeded = "provider_succeeded"
	// provider work awaiting settlement.
	CreativeRunStatusSettlementPending = "settlement_pending"
	// unused funds awaiting release.
	CreativeRunStatusReleasePending = "release_pending"
	// settled successful delivery state.
	CreativeRunStatusSucceeded = "succeeded"
	// terminal failed task state.
	CreativeRunStatusFailed = "failed"
	// terminal cancelled task state.
	CreativeRunStatusCancelled = "cancelled"
	// terminal lost result state.
	CreativeRunStatusResultLost = "result_lost"

	// Output states distinguish execution metadata from client receipt, allowing
	// acknowledgment and transient cleanup to progress independently of task
	// funds.
	// awaiting provider output metadata.
	CreativeRunOutputStatusPending = "pending"
	// provider output success metadata.
	CreativeRunOutputStatusSucceeded = "succeeded"
	// provider output failure metadata.
	CreativeRunOutputStatusFailed = "failed"
	// unavailable output state marker.
	CreativeRunOutputStatusLost = "lost"
	// client output receipt recorded.
	CreativeRunOutputStatusAcked = "acked"

	// CreativeManagedBy identifies hidden creative execution Keys in managed_by,
	// which normal Key APIs exclude from user listing and direct management.
	CreativeManagedBy = "creative_studio"

	// CreativeWorkspaceHeader selects the browser workspace UUID header used with
	// authenticated user identity to scope task creation, queries, output, and
	// receipt.
	CreativeWorkspaceHeader = "X-Creative-Workspace-ID"

	// recover creation input provisioning. Recovery operations persist creation,
	// capture, or release work separately from queued execution so durable
	// actions can restore their next phase.
	CreativeRunOutboxProvision CreativeRunOutboxOperation = "provision"
	// recover confirmed work settlement.
	CreativeRunOutboxSettle CreativeRunOutboxOperation = "settle"
	// recover unused reservation release.
	CreativeRunOutboxRelease CreativeRunOutboxOperation = "release"

	// Outbox statuses distinguish due work, claimed ownership, and terminal
	// actions so claim and repeated Ensure operations retain the correct recovery
	// lifetime.
	// available recovery action state.
	CreativeRunOutboxPending CreativeRunOutboxStatus = "pending"
	// claimed recovery action state.
	CreativeRunOutboxLeased CreativeRunOutboxStatus = "leased"
	// completed recovery action state.
	CreativeRunOutboxDone CreativeRunOutboxStatus = "done"
	// cancelled recovery action state.
	CreativeRunOutboxCancelled CreativeRunOutboxStatus = "cancelled"
)

// Creative errors classify feature and access gates, invalid inputs, delivery
// availability, and funds failures so handlers and workers can select the
// matching response or recovery path.
var (
	// Workers distinguish missing, unavailable, and corrupt data; storage outages
	// must remain retryable.
	ErrCreativeTransientNotFound    = errors.New("creative transient data is missing or expired")
	ErrCreativeTransientUnavailable = errors.New("creative transient store is unavailable")
	ErrCreativeTransientCorrupt     = errors.New("creative transient data is corrupt")
	ErrCreativeDisabled             = infraerrors.New(infraerrors.CategoryNotFound, "CREATIVE_DISABLED", "creative studio is disabled")
	ErrCreativeRunNotFound          = infraerrors.New(infraerrors.CategoryNotFound, "CREATIVE_RUN_NOT_FOUND", "creative run not found")
	ErrCreativeRunExists            = infraerrors.New(infraerrors.CategoryConflict, "CREATIVE_RUN_EXISTS", "creative run already exists")
	ErrCreativeOutputExists         = infraerrors.New(infraerrors.CategoryConflict, "CREATIVE_OUTPUT_EXISTS", "creative run output already exists")
	ErrCreativeWorkspaceRequired    = infraerrors.New(infraerrors.CategoryBadRequest, "CREATIVE_WORKSPACE_REQUIRED", "creative workspace id is required")
	ErrCreativeWorkspaceInvalid     = infraerrors.New(infraerrors.CategoryBadRequest, "CREATIVE_WORKSPACE_INVALID", "creative workspace id is invalid")

	ErrCreativeInvalidTransition    = infraerrors.New(infraerrors.CategoryBadRequest, "CREATIVE_INVALID_TRANSITION", "invalid creative run status transition")
	ErrCreativeInvalidParams        = infraerrors.New(infraerrors.CategoryBadRequest, "CREATIVE_INVALID_PARAMS", "invalid creative run parameters")
	ErrCreativePromptTooLong        = infraerrors.New(infraerrors.CategoryBadRequest, "CREATIVE_PROMPT_TOO_LONG", "creative prompt is too long")
	ErrCreativeInvalidMime          = infraerrors.New(infraerrors.CategoryBadRequest, "CREATIVE_INVALID_MIME", "creative source image mime type is invalid")
	ErrCreativeAssetTooLarge        = infraerrors.New(infraerrors.Category(413), "CREATIVE_ASSET_TOO_LARGE", "creative asset is too large")
	ErrCreativeInputTooLarge        = infraerrors.New(infraerrors.Category(413), "CREATIVE_INPUT_TOO_LARGE", "creative total input is too large")
	ErrCreativeMaskRequired         = infraerrors.New(infraerrors.CategoryBadRequest, "CREATIVE_MASK_REQUIRED", "creative inpaint requires source image and png mask")
	ErrCreativeMaskSizeMismatch     = infraerrors.New(infraerrors.CategoryBadRequest, "CREATIVE_MASK_SIZE_MISMATCH", "creative mask dimensions must match the first source image")
	ErrCreativeOperationUnsupported = infraerrors.New(infraerrors.CategoryBadRequest, "CREATIVE_OPERATION_UNSUPPORTED", "creative operation is not supported by this group")
	ErrCreativeInvalidModel         = infraerrors.New(infraerrors.CategoryBadRequest, "CREATIVE_INVALID_MODEL", "creative model is not available in this group")
	ErrCreativeGroupForbidden       = infraerrors.New(infraerrors.CategoryForbidden, "CREATIVE_GROUP_FORBIDDEN", "creative studio is not available for this group")
	ErrCreativeGroupImageDisabled   = infraerrors.New(infraerrors.CategoryForbidden, "CREATIVE_GROUP_IMAGE_DISABLED", "image generation is disabled for this group")

	ErrCreativeRunIdempotencyConflict = infraerrors.New(infraerrors.CategoryConflict, "CREATIVE_IDEMPOTENCY_CONFLICT", "idempotency key reused with different creative request")
	ErrCreativeOutputNotFound         = infraerrors.New(infraerrors.CategoryNotFound, "CREATIVE_OUTPUT_NOT_FOUND", "creative run output not found")
	ErrCreativeOutputNotReady         = infraerrors.New(infraerrors.CategoryConflict, "CREATIVE_OUTPUT_NOT_READY", "creative run output is not ready")
	ErrCreativeOutputExpired          = infraerrors.New(infraerrors.Category(410), "CREATIVE_OUTPUT_EXPIRED", "creative run output has expired")
	ErrCreativeResultLost             = infraerrors.New(infraerrors.Category(410), "CREATIVE_RESULT_LOST", "creative run result has been lost")
	ErrCreativeTransientFailed        = infraerrors.New(infraerrors.CategoryBadGateway, "CREATIVE_TRANSIENT_FAILED", "creative transient store operation failed")

	ErrCreativeBillingHoldFailed     = infraerrors.New(infraerrors.CategoryBadGateway, "CREATIVE_BILLING_HOLD_FAILED", "creative balance hold failed")
	ErrCreativeInsufficientBalance   = infraerrors.New(infraerrors.Category(402), "CREATIVE_INSUFFICIENT_BALANCE", "insufficient balance for creative run")
	ErrCreativeSettlementBillingFail = infraerrors.New(infraerrors.CategoryBadGateway, "CREATIVE_SETTLEMENT_BILLING_FAILED", "creative settlement billing failed")
)

// CreativeRunScope pairs an authenticated owner with the normalized browser
// workspace used for task access.
type CreativeRunScope struct {
	// UserID and WorkspaceID jointly scope public task access. The workspace UUID
	// is normalized separately from authentication and cannot grant another user
	// access to a task whose owner does not match.
	UserID      int64  // authenticated task owner identifier
	WorkspaceID string // browser workspace UUID
}

// CreativeRun retains persistent identity, hashed inputs, price snapshots, and
// asynchronous recovery metadata. Plaintext prompts, source bytes, masks, and
// generated bytes belong in transient payload or media storage.
type CreativeRun struct {
	FrameCount      int `json:"frame_count,omitempty"`       // requested animation frame count
	FrameDurationMS int `json:"frame_duration_ms,omitempty"` // milliseconds per animation frame
	// ActualFrameCount is rebuilt from persisted successful outputs before capture.
	// It never substitutes the requested count and is not part of request metadata.
	ActualFrameCount int `json:"-"` // recovered successful frame units

	ID     int64  // stored metadata row identifier
	RunID  string // public creative task identifier
	UserID int64  // authenticated task owner identifier
	// WorkspaceID is absent for legacy tasks. Scoped public queries exclude those
	// rows, while internal background lookup can still recover their task state.
	WorkspaceID *string // browser workspace UUID
	GroupID     int64   // saved target group identifier
	APIKeyID    int64   // managed execution Key identifier
	// ProviderID is absent before provider selection. Worker preparation saves the
	// chosen identifier before transport, and outcome recovery retains that fact.
	ProviderID *int64 // selected execution provider identifier
	// Platform confines preparation to the saved execution family when nonempty.
	// Legacy tasks with an empty value try the executor supported platform order.
	Platform             string // saved execution route platform
	Model                string // normalized public request model
	RequestedModel       string // supplied request model spelling
	Operation            string // creative image operation name
	RequestedOutputCount int    // saved requested output count
	ImageSize            string // selected image size tier
	AspectRatio          string // selected image aspect ratio
	// ResponseMIMEType supplies the task OutputFormat during public conversion.
	// Actual provider image MIME belongs to each indexed output metadata record.
	ResponseMIMEType string // saved default response MIME

	PromptHash         string // SHA-256 prompt digest
	RequestFingerprint string // SHA-256 normalized request digest
	// IdempotencyKey identifies replay within this run user and browser workspace.
	// An absent key leaves creation outside the scoped client replay lookup.
	IdempotencyKey *string // optional client replay key

	Status string // persisted creative task state

	// EstimatedCost, HoldAmount, ActualCost, BalanceHoldAmount, BaseUnitPrice,
	// SubscriptionHoldAllocations, SubscriptionRateMultiplier, and
	// BalanceRateMultiplier retain the pricing and funding snapshot. Reservation
	// can update hold amounts, while later capture uses the saved base price and
	// source multipliers.
	EstimatedCost     float64  // USD
	HoldAmount        *float64 // USD
	ActualCost        *float64 // USD
	BalanceHoldAmount float64  // USD
	// reserved subscription funding allocation records
	SubscriptionHoldAllocations []billing.BillingAllocation
	// AllowanceReserved records whether reservation pre-counted task allowances.
	// Capture and release pass it to billing and clear the stored marker after
	// completing the corresponding funds action.
	AllowanceReserved          bool    // funding allowance reservation marker
	BaseUnitPrice              float64 // USD per image
	SubscriptionRateMultiplier float64 // saved subscription price multiplier
	BalanceRateMultiplier      float64 // saved balance price multiplier
	PlanGroupRateEnabled       bool    // plan group multiplier enabled flag

	ErrorCode           *string // optional public failure classification
	ErrorMessage        *string // optional formatted public failure text
	ReleaseTargetStatus string  // terminal target after funds release
	AttemptCount        int     // retryable execution failure count
	ProvisioningPhase   string  // last saved provisioning step
	// SettlementAttemptCount and ReleaseAttemptCount count separate funding
	// recovery failures, while AttemptCount tracks retryable preparation or
	// execution failures; neither funding counter limits another provider call.
	SettlementAttemptCount   int        // failed settlement recovery count
	ReleaseAttemptCount      int        // failed release recovery count
	ProviderResultRecordedAt *time.Time // provider work recorded time
	NextReconcileAt          *time.Time // saved next recovery time hint
	LastReconcileError       *string    // saved recovery diagnostic text

	// Version fences status transitions and provider outcome updates that compare
	// a previously read value. Specialized identity writes use their own predicates.
	Version int64 // optimistic transition version counter

	CreatedAt   time.Time  // task metadata creation time
	UpdatedAt   time.Time  // latest metadata update time
	StartedAt   *time.Time // first execution admission time
	CompletedAt *time.Time // terminal task completion time
	CancelledAt *time.Time // task cancellation transition time
}

// CreativeRunOutput retains delivery metadata and client receipt for an
// indexed transient image result.
type CreativeRunOutput struct {
	FrameCount int `json:"frame_count,omitempty"` // observed animation frame count

	ID          int64   // stored metadata row identifier
	RunID       string  // public creative task identifier
	OutputIndex int     // zero-based image delivery index
	Status      string  // provider output delivery state
	MimeType    *string // stored image MIME label
	ByteSize    *int64  // bytes
	// TransientExpiresAt is the TTL upper bound for stored bytes, while AckedAt
	// records client receipt that can delete bytes sooner. Metadata persists
	// after byte removal so history can identify an unavailable or received
	// image.
	TransientExpiresAt *time.Time // temporary image expiry upper bound
	AckedAt            *time.Time // client image receipt time
	ErrorCode          *string    // optional public failure classification
	ErrorMessage       *string    // optional formatted public failure text
	CreatedAt          time.Time  // output metadata creation time
	UpdatedAt          time.Time  // latest output metadata update time
}

// CreateCreativeRunParams supplies validated task metadata and its initial
// pending output count for persistence.
type CreateCreativeRunParams struct {
	FrameCount      int `json:"frame_count,omitempty"`       // requested animation frame count
	FrameDurationMS int `json:"frame_duration_ms,omitempty"` // milliseconds per animation frame

	Platform                   string  // saved execution route platform
	RunID                      string  // public creative task identifier
	UserID                     int64   // authenticated task owner identifier
	WorkspaceID                string  // browser workspace UUID
	GroupID                    int64   // saved target group identifier
	APIKeyID                   int64   // managed execution Key identifier
	Model                      string  // normalized public request model
	RequestedModel             string  // supplied request model spelling
	Operation                  string  // creative image operation name
	RequestedOutputCount       int     // saved requested output count
	ImageSize                  string  // selected image size tier
	AspectRatio                string  // selected image aspect ratio
	ResponseMIMEType           string  // saved default response MIME
	PromptHash                 string  // SHA-256 prompt digest
	RequestFingerprint         string  // SHA-256 normalized request digest
	IdempotencyKey             *string // optional client replay key
	EstimatedCost              float64 // USD
	HoldAmount                 float64 // USD
	BaseUnitPrice              float64 // USD per image
	SubscriptionRateMultiplier float64 // saved subscription price multiplier
	BalanceRateMultiplier      float64 // saved balance price multiplier
	PlanGroupRateEnabled       bool    // plan group multiplier enabled flag
}

// CreativeRunTransitionOptions supplies error details, a release target, and
// an optional transition clock.
type CreativeRunTransitionOptions struct {
	ErrorCode           *string // optional public failure classification
	ErrorMessage        *string // optional formatted public failure text
	ReleaseTargetStatus string  // terminal target after funds release
	// Now overrides the transition clock when supplied. A nil value lets the
	// persistence implementation use system time for state and event timestamps.
	Now *time.Time // optional transition clock value
}

// CreativeRunOutboxOperation identifies durable provisioning, settlement, or
// unused-funds recovery work for a task.
type CreativeRunOutboxOperation string

// CreativeRunOutboxStatus identifies pending, leased, completed, or cancelled
// processing of a recovery action.
type CreativeRunOutboxStatus string

// CreativeRunOutbox carries a persisted recovery action and its currently
// assigned claim identity.
type CreativeRunOutbox struct {
	ID        int64                      // stored metadata row identifier
	RunID     string                     // public creative task identifier
	Operation CreativeRunOutboxOperation // persisted task recovery operation
	// LeaseToken and LeaseUntil identify the current leased claim; Status
	// distinguishes it from pending and terminal actions. AvailableAt governs due
	// selection, while matching row/token ownership gates completion, retry, and
	// cancellation.
	Status       CreativeRunOutboxStatus // recovery action processing state
	AvailableAt  time.Time               // recovery action claim eligibility time
	LeaseToken   string                  // recovery action claim token
	LeaseUntil   *time.Time              // recovery claim expiration time
	AttemptCount int                     // recovery action claim attempt count
	LastError    string                  // latest recovery attempt error text
	CreatedAt    time.Time               // recovery action creation time
	UpdatedAt    time.Time               // latest recovery action update time
}

// CreativeRunOutboxRepository persists recovery actions and fences updates
// using their claimed lease token.
type CreativeRunOutboxRepository interface {
	// Ensure records recovery work for the run and operation at the supplied
	// availability. Implementations retain terminal or already leased actions
	// when a repeated request should not replace their processing state.
	Ensure(ctx context.Context, runID string, operation CreativeRunOutboxOperation, availableAt time.Time) error
	// Claim leases due recovery actions and returns their row/token identities
	// for later updates. Limit and lease inputs select a bounded batch; abandoned
	// leased rows become eligible when their lease expires. Callers complete,
	// retry, or cancel each claimed action with its returned token, and errors
	// during event processing leave persistent lease or retry state available to
	// another recovery attempt.
	Claim(ctx context.Context, workerID string, limit int, lease time.Duration) ([]CreativeRunOutbox, error)
	// Complete marks a leased action done using the claimed row ID and token.
	// Missing or replaced ownership is reported as an error so an old worker
	// cannot report another owner action complete.
	Complete(ctx context.Context, id int64, leaseToken string) error
	// Retry restores a matching leased action to pending at the supplied
	// availability with its latest error. Stale ownership remains an error,
	// allowing a later claimant to keep its processing state.
	Retry(ctx context.Context, id int64, leaseToken string, availableAt time.Time, lastError string) error
	// Cancel marks a matching leased action cancelled and removes claim
	// ownership. Missing or replaced tokens remain errors so a worker cannot
	// cancel a claim now owned by another processor.
	Cancel(ctx context.Context, id int64, leaseToken string) error
}

// CreativeRunFilter supplies an exact or active status selection with limit
// and offset pagination.
type CreativeRunFilter struct {
	// Status selects an exact task state or the active shortcut used by history
	// readers. Empty selects the repository unfiltered task list.
	Status string // task status selection
	// Limit is normalized by Queries to twenty when outside one through one
	// hundred before it reaches the task repository.
	Limit int // requested maximum page row count
	// Offset is normalized by Queries to zero when negative before it reaches
	// the task repository, keeping public pages at a valid starting position.
	Offset int // requested number of skipped rows
}

// CreativeRunOutputBatchReader supplies page-wide output metadata for history
// and active task queries.
type CreativeRunOutputBatchReader interface {
	// ListCreativeRunOutputsForRuns returns indexed metadata grouped by supplied
	// run IDs for a public page. A run with no output rows can be absent from the
	// returned map, and callers do not issue separate reads for missing entries.
	ListCreativeRunOutputsForRuns(ctx context.Context, runIDs []string) (map[string][]*CreativeRunOutput, error)
}

// CreativeRunAllowanceMarker persists the reserved allowance flag used after
// capture or release recovery.
type CreativeRunAllowanceMarker interface {
	// SetCreativeRunAllowanceReserved persists whether funds allowances remain
	// reserved for this task. Capture and release callers clear the marker after
	// their funds action so later recovery uses current reservation metadata.
	SetCreativeRunAllowanceReserved(ctx context.Context, runID string, reserved bool) error
}

// CreativeRunRepository persists task metadata, indexed outputs, and worker
// recovery state for creative processing.
type CreativeRunRepository interface {
	// CreateCreativeRun persists validated task metadata and initial output
	// indexes for worker processing. The resulting run is queued with creation
	// recovery available; plaintext prompts and media bytes are supplied through
	// separate transient storage.
	CreateCreativeRun(ctx context.Context, params CreateCreativeRunParams) (*CreativeRun, error)
	// GetCreativeRunByRunID reads an internal run identifier for worker and
	// recovery processing. This operation has no user/workspace gate, so public
	// access uses the owner-scoped lookup instead.
	GetCreativeRunByRunID(ctx context.Context, runID string) (*CreativeRun, error)
	// GetCreativeRunByRunIDForOwner reads an exact run only within the supplied
	// user/workspace scope. Missing or mismatched ownership is reported as
	// absent, and callers normalize the scope before lookup.
	GetCreativeRunByRunIDForOwner(ctx context.Context, scope CreativeRunScope, runID string) (*CreativeRun, error)
	// GetCreativeRunByIdempotencyKey reads the task associated with a key in the
	// supplied user/workspace scope. Missing keys remain absent, and callers
	// compare request fingerprints before accepting an existing task replay.
	GetCreativeRunByIdempotencyKey(ctx context.Context, scope CreativeRunScope, key string) (*CreativeRun, error)
	// ListCreativeRunsForOwner returns task rows within the supplied
	// user/workspace scope and filter. The repository applies status and
	// pagination rules; callers load corresponding output metadata separately
	// before public conversion.
	ListCreativeRunsForOwner(ctx context.Context, scope CreativeRunScope, filter CreativeRunFilter) ([]*CreativeRun, error)
	// TransitionCreativeRunStatus checks the allowed status pair and fences the
	// update against the read version. Invalid or displaced transitions remain
	// errors, so callers reread current state before choosing recovery.
	TransitionCreativeRunStatus(ctx context.Context, runID, toStatus string, opts CreativeRunTransitionOptions) error
	// MarkCreativeRunRunning records execution admission and optional provider
	// identity for the task. Repeated running calls follow repository update
	// rules, while invalid source states remain transition errors for the caller.
	MarkCreativeRunRunning(ctx context.Context, runID string, providerID int64, now time.Time) error
	// SetCreativeRunExecution records the selected provider and platform before
	// the worker upstream call. This saved identity supports later output-based
	// recovery independently from the original public request model.
	SetCreativeRunExecution(ctx context.Context, runID string, providerID int64, platform string, now time.Time) error
	// MarkCreativeRunSucceeded records cost and completion for a supported
	// execution or settlement state. Repeated completion and concurrent
	// cancellation follow repository rules rather than restarting a completed
	// task.
	MarkCreativeRunSucceeded(ctx context.Context, runID string, actualCost float64, now time.Time) error
	// UpdateCreativeRunOutput applies indexed delivery metadata while retaining
	// acknowledged receipt. Callers supply optional values according to
	// repository presence rules; empty error text can retain an earlier stored
	// code or message.
	UpdateCreativeRunOutput(ctx context.Context, runID string, outputIndex int, status, mimeType string, byteSize int64, transientExpiresAt *time.Time, errorCode, errorMessage string) error
	// GetCreativeRunOutput reads exact indexed output metadata after the caller
	// establishes task access. Internal worker calls can use it directly, while
	// public queries verify user and workspace ownership first.
	GetCreativeRunOutput(ctx context.Context, runID string, outputIndex int) (*CreativeRunOutput, error)
	// ListCreativeRunOutputs returns indexed metadata for a run after caller
	// access checks when public. Worker and result recovery use the same internal
	// operation for counting succeeded provider work.
	ListCreativeRunOutputs(ctx context.Context, runID string) ([]*CreativeRunOutput, error)
	// MarkCreativeRunOutputAcked persists receipt for a succeeded indexed output
	// and accepts repeated acknowledgment. Callers perform this metadata
	// operation before deleting its transient bytes so a failed write cannot
	// remove unrecorded delivery.
	MarkCreativeRunOutputAcked(ctx context.Context, runID string, outputIndex int, now time.Time) error
	// ListCreativeRunsDueForTransientCleanup selects terminal tasks completed by
	// the cutoff for a bounded cleanup batch. The read leaves metadata eligible,
	// and callers delete transient keys while retaining run and output rows.
	ListCreativeRunsDueForTransientCleanup(ctx context.Context, cutoff time.Time, limit int) ([]*CreativeRun, error)
	// IncrementCreativeRunAttempt adds one retryable execution failure and
	// returns the subsequently read count. The value can include a later
	// concurrent increment, so callers treat it as the stored retry count.
	IncrementCreativeRunAttempt(ctx context.Context, runID string) (int, error)
	// IncrementCreativeRunSettlementAttempt adds one funding-capture recovery
	// failure and returns its subsequently read count. This counter remains
	// separate from execution retries and serves repeated-settlement diagnostics
	// in the worker.
	IncrementCreativeRunSettlementAttempt(ctx context.Context, runID string) (int, error)
	// IncrementCreativeRunReleaseAttempt adds one unused-funds recovery failure
	// and returns its subsequently read count. Its failures propagate for retry,
	// while this counter remains independent from another provider execution
	// attempt.
	IncrementCreativeRunReleaseAttempt(ctx context.Context, runID string) (int, error)
	// SetCreativeRunProvisioningPhase persists a provisioning progress marker
	// after its caller completes a creation step. Recovery reads that marker to
	// decide whether transient input can resume queue admission or funds release.
	SetCreativeRunProvisioningPhase(ctx context.Context, runID, phase string) error
	// MarkCreativeRunProviderSucceeded persists confirmed provider work and
	// identity for settlement recovery. Callers use completed output metadata to
	// restore this phase and then ensure settlement recovery instead of
	// submitting generation again.
	MarkCreativeRunProviderSucceeded(ctx context.Context, runID string, providerID int64, now time.Time) error
	// SetCreativeRunReconcileError records the latest recovery diagnostic and
	// next-attempt hint. Callers can clear either value using blank message or
	// zero time after funds recovery finishes successfully.
	SetCreativeRunReconcileError(ctx context.Context, runID, message string, next time.Time) error
}

// CreativeRunPayload stores plaintext execution input temporarily with
// references to separately stored media. Sources and Mask are excluded from
// JSON and rebuilt by the worker before provider execution.
type CreativeRunPayload struct {
	FrameCount      int `json:"frame_count,omitempty"`       // requested animation frame count
	FrameDurationMS int `json:"frame_duration_ms,omitempty"` // milliseconds per animation frame

	// SourceCount and HasMask identify separate input keys used to reconstruct
	// Sources and Mask for execution. Prompt remains plaintext here only for the
	// transient lifetime; persistent task metadata stores its digest.
	RunID       string `json:"run_id"`       // public creative task identifier
	UserID      int64  `json:"user_id"`      // authenticated task owner identifier
	GroupID     int64  `json:"group_id"`     // saved target group identifier
	APIKeyID    int64  `json:"api_key_id"`   // managed execution Key identifier
	Model       string `json:"model"`        // normalized public request model
	Operation   string `json:"operation"`    // creative image operation name
	Prompt      string `json:"prompt"`       // trimmed plaintext execution prompt
	ImageSize   string `json:"image_size"`   // selected image size tier
	AspectRatio string `json:"aspect_ratio"` // selected image aspect ratio
	// selected image background option
	Background string `json:"background,omitempty"`
	// selected image reasoning option
	ThinkingLevel string `json:"thinking_level,omitempty"`
	// selected image quality option
	Quality string `json:"quality,omitempty"`
	// separately stored input image count
	SourceCount int  `json:"source_count"`
	HasMask     bool `json:"has_mask"` // separately stored mask presence flag
	// SHA-256 normalized request digest
	RequestFingerprint string `json:"request_fingerprint"`

	// Sources and Mask retain reconstructed transient byte storage during execution.
	// Worker Process fills these values after loading the SourceCount input keys
	// and optional HasMask key, then provider builders read the retained bytes.
	// loaded execution source image values
	Sources []CreativeInputImage `json:"-"`
	Mask    *CreativeInputImage  `json:"-"` // optional loaded mask image value
}

// CreativeInputImage pairs caller-owned image bytes with the MIME label used
// by provider request builders.
type CreativeInputImage struct {
	// Bytes remains caller-owned through normalization, which retains the same
	// storage. Keep it stable until request construction and transient saving
	// finish reading the image.
	Bytes []byte // caller supplied image byte buffer
	Mime  string // source image MIME label
}

// CreateCreativeRunParamsPublic supplies user-submitted fields and uploaded
// media for creative task validation.
type CreateCreativeRunParamsPublic struct {
	OutputFormat    string // requested output format name
	FrameCount      *int   // optional animation frame count
	FrameDurationMS *int   // optional frame delay milliseconds

	GroupID   int64  // submitted target group identifier
	Model     string // submitted public model spelling
	Operation string // submitted image operation spelling
	Prompt    string // submitted plaintext prompt text
	// SourceImages and Mask retain caller-owned byte slices. Validation returns
	// those bytes with normalized MIME labels, so callers keep the uploads stable
	// until creation finishes its separate transient storage writes.
	SourceImages  []CreativeInputImage // submitted image reference values
	Mask          *CreativeInputImage  // submitted optional mask image
	ImageSize     string               // submitted image size tier
	AspectRatio   string               // submitted image shape option
	Quality       string               // submitted image quality option
	Background    string               // submitted image background option
	ThinkingLevel string               // submitted image reasoning option
	// OutputCount requests the image count; zero selects one before validation
	// checks the chosen operation route maximum and rejects unsupported counts.
	OutputCount int // submitted requested output count
}

// CreativeRunPublic exposes task metadata with optional amounts and
// Unix-second timestamps for the client.
type CreativeRunPublic struct {
	FrameCount      int `json:"frame_count,omitempty"`       // requested animation frame count
	FrameDurationMS int `json:"frame_duration_ms,omitempty"` // milliseconds per animation frame

	ID     string `json:"id"`     // public creative task identifier
	Status string `json:"status"` // client creative task state
	Model  string `json:"model"`  // normalized public request model
	// supplied request model spelling
	RequestedModel string `json:"requested_model"`
	Operation      string `json:"operation"` // creative image operation name
	// saved requested output count
	RequestedOutputCount int    `json:"requested_output_count"`
	ImageSize            string `json:"image_size"`   // selected image size tier
	AspectRatio          string `json:"aspect_ratio"` // selected image aspect ratio
	// format from task default MIME
	OutputFormat  string  `json:"output_format"`
	GroupID       int64   `json:"group_id"`       // saved target group identifier
	EstimatedCost float64 `json:"estimated_cost"` // USD
	HoldAmount    float64 `json:"hold_amount"`    // USD
	// ActualCost retains the task cost pointer during conversion. Callers keep
	// that amount immutable while sharing this response with the task record.
	ActualCost *float64 `json:"actual_cost,omitempty"` // USD
	// optional public failure classification
	ErrorCode string `json:"error_code,omitempty"`
	// optional formatted public failure text
	ErrorMessage string `json:"error_message,omitempty"`
	// client indexed output metadata values
	Outputs []CreativeRunOutputPublic `json:"outputs,omitempty"`
	// CreatedAt, StartedAt, CompletedAt, and CancelledAt use Unix seconds. The
	// optional event pointers retain absent timestamps while conversion allocates
	// each present event value independently from the task time pointers.
	// task metadata creation time
	CreatedAt int64 `json:"created_at"`
	// first execution admission time
	StartedAt *int64 `json:"started_at,omitempty"`
	// terminal task completion time
	CompletedAt *int64 `json:"completed_at,omitempty"`
	// task cancellation transition time
	CancelledAt *int64 `json:"cancelled_at,omitempty"`
	// IdempotentReplay reports whether the response reuses a task for the
	// supplied idempotency key and normalized request fingerprint in this scope.
	IdempotentReplay bool `json:"idempotent_replay,omitempty"`
}

// CreativeRunOutputPublic exposes indexed output metadata and Unix-second
// expiry or client receipt timestamps.
type CreativeRunOutputPublic struct {
	FrameCount int `json:"frame_count,omitempty"` // observed animation frame count

	Index    int    `json:"output_index"`        // zero-based client output index
	Status   string `json:"status"`              // client output delivery state
	MimeType string `json:"mime_type,omitempty"` // stored image MIME label
	ByteSize int64  `json:"byte_size,omitempty"` // bytes
	// temporary image expiry upper bound
	TransientExpiresAt *int64 `json:"transient_expires_at,omitempty"`
	AckedAt            *int64 `json:"acked_at,omitempty"` // client image receipt time
	// optional public failure classification
	ErrorCode string `json:"error_code,omitempty"`
	// optional formatted public failure text
	ErrorMessage string `json:"error_message,omitempty"`
}

// CreativeModelPublic exposes one admitted group/model route, supported
// parameters, and size-specific display prices.
type CreativeModelPublic struct {
	GroupID   int64  `json:"group_id"`   // saved target group identifier
	GroupName string `json:"group_name"` // current group display name
	Model     string `json:"model"`      // normalized public request model
	// admitted model operation names
	Operations []string `json:"operations"`
	// supported image size tier names
	ImageSizes []string `json:"image_sizes"`
	// supported image shape option names
	AspectRatios []string `json:"aspect_ratios"`
	Qualities    []string `json:"qualities"` // supported quality option names
	// exposed output format option names
	OutputFormats []string `json:"output_formats"`
	// OutputFormatsByOperation limits each admitted operation to its available
	// routes. Clients use this map before the combined legacy OutputFormats list.
	OutputFormatsByOperation map[string][]string   `json:"output_formats_by_operation"`
	GIFFrameCount            *CreativeNumericRange `json:"gif_frame_count,omitempty"`       // supported frame count range
	GIFFrameDurationMS       *CreativeNumericRange `json:"gif_frame_duration_ms,omitempty"` // supported frame delay range
	// optional compression option numeric range
	OutputCompression *CreativeNumericRange `json:"output_compression"`
	// supported background option names
	BackgroundOptions []string `json:"background_options"`
	// supported reasoning option names
	ThinkingLevels []string `json:"thinking_levels"`
	// maximum admitted image output count
	MaxOutputCount int `json:"max_output_count"`
	// maximum admitted source reference count
	MaxReferenceImages int     `json:"max_reference_images"`
	Price512           float64 `json:"price_512"` // USD per image
	Price1K            float64 `json:"price_1k"`  // USD per image
	Price2K            float64 `json:"price_2k"`  // USD per image
	Price4K            float64 `json:"price_4k"`  // USD per image
}

// CreativeNumericRange represents inclusive minimum and maximum values with a
// declared numeric option step.
type CreativeNumericRange struct {
	Min  int `json:"min"`  // inclusive numeric option minimum
	Max  int `json:"max"`  // inclusive numeric option maximum
	Step int `json:"step"` // numeric option increment value
}

// CreativeModelsResponse contains the admitted creative model entries returned
// by public discovery.
type CreativeModelsResponse struct {
	Data []CreativeModelPublic `json:"data"` // admitted model directory entries
}

// CreativeCapabilitiesResponse exposes server upload byte limits and
// Unicode-code-point prompt limits to clients.
type CreativeCapabilitiesResponse struct {
	MaxPromptChars     int   `json:"max_prompt_chars"`      // Unicode code points
	MaxAssetBytes      int64 `json:"max_asset_bytes"`       // bytes
	MaxTotalInputBytes int64 `json:"max_total_input_bytes"` // bytes
	MaxMaskBytes       int64 `json:"max_mask_bytes"`        // bytes
	// accepted uploaded image MIME labels
	AllowedMIMETypes []string `json:"allowed_mime_types"`
}

// CreativeListRunsResponse contains one scoped history page and whether it
// filled the selected limit.
type CreativeListRunsResponse struct {
	Data    []*CreativeRunPublic `json:"data"`     // returned public task metadata
	HasMore bool                 `json:"has_more"` // page filled selected limit flag
}

// NormalizeCreativeWorkspaceID returns the canonical lowercase form of a
// nonzero UUID after trimming. Blank input reports workspace required, while
// malformed or nil UUID values report workspace invalid for public request
// handling.
func NormalizeCreativeWorkspaceID(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ErrCreativeWorkspaceRequired
	}
	parsed, err := uuid.Parse(raw)
	if err != nil || parsed == uuid.Nil {
		return "", ErrCreativeWorkspaceInvalid
	}
	return strings.ToLower(parsed.String()), nil
}

// NormalizeCreativeRunScope returns a positive-user scope with a canonical
// workspace UUID. Nonpositive users report run not found before workspace
// validation, while workspace errors retain their distinct required or invalid
// classification.
func NormalizeCreativeRunScope(scope CreativeRunScope) (CreativeRunScope, error) {
	if scope.UserID <= 0 {
		return CreativeRunScope{}, ErrCreativeRunNotFound
	}
	normalizedWorkspaceID, err := NormalizeCreativeWorkspaceID(scope.WorkspaceID)
	if err != nil {
		return CreativeRunScope{}, err
	}
	scope.WorkspaceID = normalizedWorkspaceID
	return scope, nil
}

// NewCreativeRunID returns crun_ followed by sixteen random bytes encoded as
// lowercase hex. A returned identifier has a nil error. crypto/rand.Read
// terminates the process when its io.ReadFull operation reports an error.
func NewCreativeRunID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "crun_" + hex.EncodeToString(b[:]), nil
}

// IsValidCreativeRunID reports whether the supplied value has crun_ and a
// nonempty suffix. It does not validate the suffix alphabet or length, so
// callers use NewCreativeRunID for constructed task identities.
func IsValidCreativeRunID(runID string) bool {
	return len(runID) > len("crun_") && runID[:len("crun_")] == "crun_"
}

// IsTerminalCreativeRunStatus reports whether status is succeeded, failed,
// cancelled, or result_lost.
func IsTerminalCreativeRunStatus(status string) bool {
	switch status {
	case CreativeRunStatusSucceeded, CreativeRunStatusFailed, CreativeRunStatusCancelled, CreativeRunStatusResultLost:
		return true
	default:
		return false
	}
}

// IsCreativeRunSettlementPending reports whether provider success, settlement,
// or release recovery remains pending.
func IsCreativeRunSettlementPending(status string) bool {
	return status == CreativeRunStatusProviderSucceeded ||
		status == CreativeRunStatusSettlementPending ||
		status == CreativeRunStatusReleasePending
}

// CanTransitionCreativeRun reports whether the exact source and destination
// pair is allowed. Blank states fail before any status selection.
//
// Terminal states permit only succeeded to result_lost, allowing delivery loss
// after completed execution. Every other nonempty source permits failed,
// including an unrecognized source label. Remaining transitions follow the
// listed execution and funding-recovery paths, which prevent completed tasks
// from restarting. Repository callers separately compare the stored version
// when applying an admitted pair.
func CanTransitionCreativeRun(from, to string) bool {
	if from == "" || to == "" {
		return false
	}
	if IsTerminalCreativeRunStatus(from) {
		// Delivery bytes can expire after successful settlement. Permit only the
		// success-to-loss terminal transition so public retrieval can report that
		// later loss while completed execution and funds state remain terminal.
		return from == CreativeRunStatusSucceeded && to == CreativeRunStatusResultLost
	}
	if to == CreativeRunStatusFailed {
		return true
	}
	switch from {
	case CreativeRunStatusQueued:
		// A queued task can lose its payload before it ever reaches running.
		return to == CreativeRunStatusRunning || to == CreativeRunStatusCancelled || to == CreativeRunStatusResultLost || to == CreativeRunStatusFailed || to == CreativeRunStatusReleasePending
	case CreativeRunStatusRunning:
		switch to {
		case CreativeRunStatusProviderSucceeded, CreativeRunStatusSettlementPending, CreativeRunStatusSucceeded, CreativeRunStatusCancelled, CreativeRunStatusResultLost, CreativeRunStatusReleasePending:
			return true
		default:
			return false
		}
	case CreativeRunStatusProviderSucceeded:
		return to == CreativeRunStatusSettlementPending || to == CreativeRunStatusSucceeded || to == CreativeRunStatusCancelled || to == CreativeRunStatusResultLost
	case CreativeRunStatusSettlementPending:
		return to == CreativeRunStatusSucceeded || to == CreativeRunStatusCancelled || to == CreativeRunStatusResultLost
	case CreativeRunStatusReleasePending:
		return to == CreativeRunStatusFailed || to == CreativeRunStatusCancelled || to == CreativeRunStatusResultLost
	default:
		return false
	}
}

// CreativeRunToPublic converts task and output metadata with Unix-second
// timestamps and a MIME-derived format. Nil input returns nil, missing
// HoldAmount uses EstimatedCost, output records are newly allocated, and
// ActualCost retains its supplied pointer.
func CreativeRunToPublic(run *CreativeRun, outputs []*CreativeRunOutput) *CreativeRunPublic {
	if run == nil {
		return nil
	}
	holdAmount := run.EstimatedCost
	if run.HoldAmount != nil {
		holdAmount = *run.HoldAmount
	}
	out := &CreativeRunPublic{
		ID:                   run.RunID,
		FrameCount:           run.FrameCount,
		FrameDurationMS:      run.FrameDurationMS,
		Status:               run.Status,
		Model:                run.Model,
		RequestedModel:       run.RequestedModel,
		Operation:            run.Operation,
		RequestedOutputCount: run.RequestedOutputCount,
		ImageSize:            run.ImageSize,
		AspectRatio:          run.AspectRatio,
		OutputFormat:         OutputFormatFromMIME(run.ResponseMIMEType),
		GroupID:              run.GroupID,
		EstimatedCost:        run.EstimatedCost,
		HoldAmount:           holdAmount,
		ActualCost:           run.ActualCost,
		ErrorCode:            CreativeDerefString(run.ErrorCode),
		ErrorMessage:         CreativeDerefString(run.ErrorMessage),
		// Public timestamps use whole Unix seconds so HTTP metadata
		// matches browser
		// task time fields; optional events retain nil through CreativeUnixPtr.
		CreatedAt:   run.CreatedAt.Unix(),
		StartedAt:   CreativeUnixPtr(run.StartedAt),
		CompletedAt: CreativeUnixPtr(run.CompletedAt),
		CancelledAt: CreativeUnixPtr(run.CancelledAt),
	}
	if len(outputs) > 0 {
		out.Outputs = make([]CreativeRunOutputPublic, 0, len(outputs))
		for _, output := range outputs {
			out.Outputs = append(out.Outputs, CreativeRunOutputToPublic(output))
		}
	}
	return out
}

// CreativeRunOutputToPublic converts one indexed metadata record and allocates
// optional Unix timestamp values. Nil input yields a zero public output;
// optional strings and byte size become their empty or zero scalar values.
func CreativeRunOutputToPublic(output *CreativeRunOutput) CreativeRunOutputPublic {
	if output == nil {
		return CreativeRunOutputPublic{}
	}
	out := CreativeRunOutputPublic{
		Index:              output.OutputIndex,
		FrameCount:         output.FrameCount,
		Status:             output.Status,
		MimeType:           CreativeDerefString(output.MimeType),
		ByteSize:           CreativeDerefInt64(output.ByteSize),
		TransientExpiresAt: CreativeUnixPtr(output.TransientExpiresAt),
		AckedAt:            CreativeUnixPtr(output.AckedAt),
		ErrorCode:          CreativeDerefString(output.ErrorCode),
		ErrorMessage:       CreativeDerefString(output.ErrorMessage),
	}
	return out
}

// CreativeDerefString returns the pointed-to text or empty when the optional
// pointer is nil. Public metadata conversion uses that scalar result, so an
// absent pointer and present empty text have the same returned value.
func CreativeDerefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// CreativeDerefInt64 returns the pointed-to number or zero when the optional
// pointer is nil. Public output conversion uses that scalar result, so an
// absent pointer and a present zero have the same returned value.
func CreativeDerefInt64(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

// CreativeUnixPtr returns a newly allocated Unix-second value for the supplied
// time. Nil input remains nil so public conversion retains the absence of an
// observed task or delivery event.
func CreativeUnixPtr(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	v := t.Unix()
	return &v
}

// CreativeStringPtr returns a pointer to nonempty text or nil for an empty
// string. Metadata callers use nil to leave optional error or idempotency
// fields absent.
func CreativeStringPtr(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

// OutputFormatFromMIME returns a client format label from trimmed lowercase
// MIME. JPEG and JPG map to jpeg, WebP and GIF keep their format, and every other spelling
// uses png.
func OutputFormatFromMIME(mime string) string {
	// Public task metadata uses format names. Normalize JPEG aliases and select
	// PNG when the saved MIME supplies no recognized JPEG, WebP, or GIF label.
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/jpeg", "image/jpg":
		return "jpeg"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	default:
		return "png"
	}
}
