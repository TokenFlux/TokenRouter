package usage

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

// export.go defines persisted usage-export tasks, half-open filter contracts,
// storage interfaces, and short-lived HMAC download tickets.
// Usage exports persist an immutable snapshot of time bounds, explicit IANA
// timezone, display locale, currency unit, and authorized query filters before
// background workers scan usage_logs in keyset batches. Separating filter
// normalization and authorization scope at task creation ensures reclaimed or
// retried tasks render identical CSV columns and enforce creator isolation.
//
// Browser downloads exchange an authenticated request for a short-lived signed
// ticket bound to task identity, scope, actor, and expiry instant. That ticket
// authorizes a native streaming GET response without buffering large CSV files
// in JavaScript memory or placing long-lived bearer tokens into query strings.

// Export scope, status, locale, currency, and ticket constants govern task
// persistence, worker state transitions, and short-lived download verification.
const (
	UsageExportScopeUser  = "user"  // regular user export scope
	UsageExportScopeAdmin = "admin" // administrator export scope

	UsageExportStatusPending   = "pending"   // persisted and awaiting worker claim
	UsageExportStatusRunning   = "running"   // claimed and streaming CSV rows
	UsageExportStatusSucceeded = "succeeded" // CSV committed and available for download
	UsageExportStatusFailed    = "failed"    // execution failed with diagnostic message
	UsageExportStatusCanceled  = "canceled"  // canceled by creator or administrator
	UsageExportStatusExpired   = "expired"   // retention window elapsed and artifact removed

	UsageExportLocaleZH = "zh" // Simplified Chinese CSV header and label locale
	UsageExportLocaleEN = "en" // English CSV header and label locale

	defaultUsageExportBalanceUnit = "USD"            // fallback currency unit label
	usageExportTicketTTL          = 60 * time.Second // signed browser download ticket lifetime
	usageExportSecretSize         = 32               // HMAC signing key byte length
)

// UsageExportFilters contains the half-open time window, explicit IANA timezone,
// presentation snapshot, and optional query selectors persisted with an export task.
type UsageExportFilters struct {
	// StartTime and EndTime define a required half-open [StartTime, EndTime)
	// interval matched as created_at >= StartTime AND created_at < EndTime.
	StartTime time.Time `json:"start_time"` // inclusive export start instant
	EndTime   time.Time `json:"end_time"`   // exclusive export end instant
	// Timezone holds a validated IANA location name used to format every exported
	// created_at timestamp with its exact UTC offset at that instant.
	Timezone string `json:"timezone"` // validated IANA timezone identifier
	// Locale and BalanceUnitName freeze the header language and user-charge unit
	// active when the task was submitted so background workers produce stable text.
	Locale          string `json:"locale,omitempty"`            // normalized zh or en export locale
	BalanceUnitName string `json:"balance_unit_name,omitempty"` // snapshotted user balance unit name
	RequestSource   string `json:"request_source,omitempty"`    // optional administrator request source selector
	// UserID and IncludeOwnedTeam enforce user-scope ownership when Scope is user
	// and act as optional actor selectors when Scope is admin.
	UserID            *int64  `json:"user_id,omitempty"`             // acting user selector
	IncludeOwnedTeam  bool    `json:"include_owned_team,omitempty"`  // include owned team member usage
	APIKeyID          *int64  `json:"api_key_id,omitempty"`          // optional API key selector
	ProviderID        *int64  `json:"provider_id,omitempty"`         // optional administrator provider selector
	GroupID           *int64  `json:"group_id,omitempty"`            // optional routed group selector
	TeamID            *int64  `json:"team_id,omitempty"`             // optional team selector
	PersonalOnly      bool    `json:"personal_only,omitempty"`       // restrict to personal non-team usage
	RequestID         string  `json:"request_id,omitempty"`          // optional local or upstream request ID
	Model             *string `json:"model,omitempty"`               // optional model selector
	ModelFilterSource string  `json:"model_filter_source,omitempty"` // requested, upstream, or mapping model dimension
	// RequestType takes precedence over Stream after sanitization so explicit
	// category selection is not narrowed by legacy transport flags.
	RequestType        *int16  `json:"request_type,omitempty"`         // optional normalized request category
	Stream             *bool   `json:"stream,omitempty"`               // optional legacy stream transport flag
	NativeCompactionV2 *bool   `json:"native_compaction_v2,omitempty"` // optional remote compaction v2 flag
	BillingType        *int8   `json:"billing_type,omitempty"`         // optional wallet or subscription selector
	BillingMode        *string `json:"billing_mode,omitempty"`         // optional token, per_request, image, or video mode
}

// UsageExportTask records persisted export state, progress counters, artifact
// metadata, and lifecycle timestamps.
type UsageExportTask struct {
	ID           int64              // persisted export task identifier
	Scope        string             // user or admin authorization scope
	Status       string             // current lifecycle status label
	Filters      UsageExportFilters // immutable filter and formatting snapshot
	CreatedBy    int64              // creator user identifier
	TotalRows    int64              // initial matching row count snapshot
	ExportedRows int64              // cumulative rows written to CSV
	// FileName stores the user-facing attachment filename while FilePath stores the
	// relative artifact key inside UsageExportArtifactStore. Both stay nil until commit.
	FileName      *string // optional attachment download filename
	FilePath      *string // optional store-relative artifact key
	FileSizeBytes int64   // committed CSV byte size
	ErrorMsg      *string // optional terminal failure diagnostic
	// CanceledBy and CanceledAt record explicit cancellation; StartedAt, FinishedAt,
	// and ExpiresAt describe execution and post-completion retention windows.
	CanceledBy *int64     // optional canceling actor user ID
	CanceledAt *time.Time // optional cancellation recording instant
	StartedAt  *time.Time // latest worker claim instant
	FinishedAt *time.Time // optional terminal completion instant
	ExpiresAt  *time.Time // optional artifact expiration instant
	CreatedAt  time.Time  // task creation instant
	UpdatedAt  time.Time  // latest task update instant
}

// UsageExportListFilter restricts task listings by authorization scope and optional creator.
type UsageExportListFilter struct {
	Scope     string // required user or admin task scope
	CreatedBy *int64 // optional creator filter; required for user scope
}

// UsageExportTaskListResult bundles one paginated task slice with scope-wide
// active task identifiers and optional watched task records so progress polling
// and completion notifications continue when older tasks move past page one.
type UsageExportTaskListResult struct {
	Items        []UsageExportTask            // requested page of export tasks
	Pagination   *pagination.PaginationResult // exact page and total metadata
	ActiveCount  int64                        // total pending or running tasks in filter scope
	ActiveIDs    []int64                      // all pending or running task IDs in filter scope
	WatchedItems []UsageExportTask            // scoped tasks matching caller-supplied watch IDs
}

// UsageExportCursor identifies the last exported row in descending (created_at, id) order.
type UsageExportCursor struct {
	CreatedAt time.Time // previous row creation timestamp
	ID        int64     // previous row primary key
}

// UsageExportDownloadStream bundles an opened CSV reader with attachment metadata.
type UsageExportDownloadStream struct {
	Reader        io.ReadCloser // opened CSV stream caller must close
	FileName      string        // sanitized attachment download filename
	FileSizeBytes int64         // artifact length in bytes
}

// UsageExportDownloadTicketClaims encodes the signed authorization payload embedded
// in a short-lived browser download ticket.
type UsageExportDownloadTicketClaims struct {
	TaskID        int64  `json:"tid"` // authorized export task identifier
	Scope         string `json:"scp"` // authorized user or admin scope
	ActorUserID   int64  `json:"uid"` // authenticated user requesting download
	ExpiresAtUnix int64  `json:"exp"` // UTC expiration unix timestamp in seconds
}

// UsageBalanceUnitReader reads the configured display currency label for user charges.
type UsageBalanceUnitReader interface {
	// GetBalanceUnitName returns the configured currency unit name or a default label.
	GetBalanceUnitName(ctx context.Context) string
}

// UsageExportRepository persists export task metadata, atomic quota-checked
// admission, worker claims, progress updates, and expiration transitions.
type UsageExportRepository interface {
	// CreateTask atomically enforces maxActive when positive for task.Scope and
	// task.CreatedBy, returning ErrExportActiveLimitExceeded when pending and
	// running tasks already reach maxActive, and otherwise inserts the task and
	// populates its ID and timestamps.
	CreateTask(ctx context.Context, task *UsageExportTask, maxActive int) error
	// GetTask loads one persisted export task by ID or returns ErrExportTaskNotFound.
	GetTask(ctx context.Context, taskID int64) (*UsageExportTask, error)
	// ListTaskPage reads one paginated slice of scoped export tasks, all active
	// task IDs in that scope, and any off-page watchIDs from a single consistent
	// snapshot so task statuses and active_ids never disagree under concurrent writes.
	ListTaskPage(ctx context.Context, filter UsageExportListFilter, params pagination.PaginationParams, watchIDs []int64) (*UsageExportTaskListResult, error)
	// CountActiveTasks counts pending and running tasks in scope for createdBy;
	// nonpositive createdBy counts across all creators in that scope.
	CountActiveTasks(ctx context.Context, scope string, createdBy int64) (int64, error)
	// ClaimNextPendingTask atomically claims the oldest pending or stale running task
	// with SKIP LOCKED, resetting ExportedRows to zero for a clean file rewrite.
	ClaimNextPendingTask(ctx context.Context, staleRunningAfterSeconds int64) (*UsageExportTask, error)
	// RequeueTask conditionally resets an interrupted running task back to pending
	// with zeroed progress and cleared claim timestamps during graceful shutdown.
	RequeueTask(ctx context.Context, taskID int64) error
	// GetTaskStatus returns the current status of taskID or ErrExportTaskNotFound.
	GetTaskStatus(ctx context.Context, taskID int64) (string, error)
	// UpdateTaskProgress persists totalRows and cumulative exportedRows for a running task.
	UpdateTaskProgress(ctx context.Context, taskID int64, totalRows, exportedRows int64) error
	// CancelTask conditionally transitions a pending or running task to canceled.
	CancelTask(ctx context.Context, taskID int64, canceledBy int64) (bool, error)
	// MarkTaskSucceeded conditionally transitions a non-canceled task to succeeded
	// with its committed artifact metadata and expiration instant; false means the
	// task was canceled concurrently so the caller should delete the newly written file.
	MarkTaskSucceeded(ctx context.Context, taskID int64, exportedRows int64, fileName, filePath string, fileSizeBytes int64, expiresAt time.Time) (bool, error)
	// MarkTaskFailed stores failed status and diagnostic text when taskID is not canceled.
	MarkTaskFailed(ctx context.Context, taskID int64, exportedRows int64, errorMsg string) error
	// ListExpiredSucceededTasks returns up to limit succeeded tasks whose ExpiresAt <= now.
	ListExpiredSucceededTasks(ctx context.Context, now time.Time, limit int) ([]UsageExportTask, error)
	// MarkTaskExpired transitions a succeeded task to expired and clears its FilePath.
	MarkTaskExpired(ctx context.Context, taskID int64) error
}

// UsageExportBatchReader streams keyset-paginated usage rows for a single export
// task while keeping per-task association caches isolated.
type UsageExportBatchReader interface {
	// NextBatch returns up to limit matching rows strictly older than cursor in
	// descending (created_at, id) order with display associations hydrated.
	NextBatch(ctx context.Context, filters UsageExportFilters, cursor *UsageExportCursor, limit int) ([]UsageLog, error)
}

// UsageExportRowReader counts matching usage rows and creates per-task keyset readers.
type UsageExportRowReader interface {
	// CountExportLogs returns the exact row count matching filters at task start.
	CountExportLogs(ctx context.Context, filters UsageExportFilters) (int64, error)
	// NewExportBatchReader creates an isolated batch reader for one export task execution.
	NewExportBatchReader(scope string) UsageExportBatchReader
}

// UsageExportArtifactWriter writes an export artifact to temporary storage until
// Commit atomically publishes it or Abort discards partial output.
type UsageExportArtifactWriter interface {
	io.Writer
	// Commit syncs buffered bytes, atomically publishes the final artifact, and returns its size.
	Commit() (int64, error)
	// Abort closes and deletes any uncommitted temporary file; calling Abort after Commit is a no-op.
	Abort() error
}

// UsageExportArtifactStore confines export CSV files inside a dedicated root directory.
type UsageExportArtifactStore interface {
	// CreateWriter opens a new temporary .part writer for the relative artifact key.
	CreateWriter(key string) (UsageExportArtifactWriter, error)
	// Open returns a reader and byte size for key or ErrExportArtifactNotFound when absent.
	Open(key string) (io.ReadCloser, int64, error)
	// Delete removes the final artifact and any leftover .part file for key; missing files succeed.
	Delete(key string) error
	// SweepStalePartFiles deletes uncommitted .part files older than maxAge and returns removed count.
	SweepStalePartFiles(maxAge time.Duration) (int, error)
}

// usageExportTicketSigner signs and verifies short-lived HMAC-SHA256 download tickets.
type usageExportTicketSigner struct {
	secret []byte           // 32-byte HMAC signing secret
	now    func() time.Time // clock function for deterministic testing
}

// newUsageExportTicketSigner creates a signer backed by a random 32-byte secret
// when secret is empty. Panics only if the operating system CSPRNG fails.
func newUsageExportTicketSigner(secret []byte, now func() time.Time) *usageExportTicketSigner {
	key := make([]byte, len(secret))
	copy(key, secret)
	if len(key) == 0 {
		key = make([]byte, usageExportSecretSize)
		if _, err := rand.Read(key); err != nil {
			panic(fmt.Sprintf("usage export ticket secret generation failed: %v", err))
		}
	}
	if now == nil {
		now = time.Now
	}
	return &usageExportTicketSigner{secret: key, now: now}
}

// Sign encodes claims with a URL-safe base64 payload and HMAC-SHA256 signature
// separated by a dot. Callers supply validated task, scope, actor, and expiry fields.
func (s *usageExportTicketSigner) Sign(claims UsageExportDownloadTicketClaims) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal export download ticket: %w", err)
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	sig := s.computeMAC(encodedPayload)
	encodedSig := base64.RawURLEncoding.EncodeToString(sig)
	return encodedPayload + "." + encodedSig, nil
}

// Verify checks the HMAC signature, structure, and expiration timestamp of ticket,
// returning decoded claims or a structured 401/400 application error.
func (s *usageExportTicketSigner) Verify(ticket string) (UsageExportDownloadTicketClaims, error) {
	var zero UsageExportDownloadTicketClaims
	parts := strings.Split(strings.TrimSpace(ticket), ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return zero, infraerrors.Unauthorized("USAGE_EXPORT_INVALID_TICKET", "invalid export download ticket")
	}
	gotSig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return zero, infraerrors.Unauthorized("USAGE_EXPORT_INVALID_TICKET", "invalid export download ticket")
	}
	expectedSig := s.computeMAC(parts[0])
	if !hmac.Equal(gotSig, expectedSig) {
		return zero, infraerrors.Unauthorized("USAGE_EXPORT_INVALID_TICKET", "invalid export download ticket")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return zero, infraerrors.Unauthorized("USAGE_EXPORT_INVALID_TICKET", "invalid export download ticket")
	}
	var claims UsageExportDownloadTicketClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return zero, infraerrors.Unauthorized("USAGE_EXPORT_INVALID_TICKET", "invalid export download ticket")
	}
	if claims.TaskID <= 0 || claims.ActorUserID <= 0 || (claims.Scope != UsageExportScopeUser && claims.Scope != UsageExportScopeAdmin) {
		return zero, infraerrors.Unauthorized("USAGE_EXPORT_INVALID_TICKET", "invalid export download ticket")
	}
	if s.now().Unix() > claims.ExpiresAtUnix {
		return zero, infraerrors.Unauthorized("USAGE_EXPORT_TICKET_EXPIRED", "export download ticket has expired")
	}
	return claims, nil
}

// computeMAC calculates HMAC-SHA256 over the ASCII base64url payload string.
func (s *usageExportTicketSigner) computeMAC(encodedPayload string) []byte {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(encodedPayload))
	return mac.Sum(nil)
}

// LoadUsageExportLocation validates an explicit IANA timezone name without
// falling back to Local or UTC when the input is blank or unrecognized.
func LoadUsageExportLocation(tz string) (*time.Location, string, error) {
	trimmed := strings.TrimSpace(tz)
	if trimmed == "" || strings.EqualFold(trimmed, "Local") {
		return nil, "", infraerrors.BadRequest("INVALID_TIMEZONE", "timezone must be a valid IANA timezone name")
	}
	loc, err := time.LoadLocation(trimmed)
	if err != nil {
		return nil, "", infraerrors.BadRequest("INVALID_TIMEZONE", fmt.Sprintf("invalid IANA timezone %q", trimmed))
	}
	return loc, trimmed, nil
}

// NormalizeUsageExportLocale maps arbitrary locale tags to UsageExportLocaleZH
// or UsageExportLocaleEN, defaulting to Simplified Chinese when unspecified
// unless an English prefix is supplied.
func NormalizeUsageExportLocale(locale string) string {
	normalized := strings.ToLower(strings.TrimSpace(locale))
	if strings.HasPrefix(normalized, "en") {
		return UsageExportLocaleEN
	}
	return UsageExportLocaleZH
}

// SanitizeAndValidateUsageExportFilters normalizes optional selectors, enforces
// scope-specific ownership rules, and validates the half-open time range and IANA timezone.
//
// For user scope, UserID is pinned to actorUserID, IncludeOwnedTeam is set true,
// and administrator-only ProviderID and RequestSource selectors are cleared so a
// user request cannot probe provider attribution or system usage rows.
func SanitizeAndValidateUsageExportFilters(scope string, actorUserID int64, filters *UsageExportFilters) (*time.Location, error) {
	if filters == nil {
		return nil, infraerrors.BadRequest("INVALID_EXPORT_FILTERS", "export filters are required")
	}
	if scope != UsageExportScopeUser && scope != UsageExportScopeAdmin {
		return nil, infraerrors.BadRequest("INVALID_EXPORT_SCOPE", "invalid export scope")
	}
	if actorUserID <= 0 {
		return nil, infraerrors.BadRequest("USAGE_EXPORT_INVALID_CREATOR", "invalid export task creator")
	}

	loc, canonicalTZ, err := LoadUsageExportLocation(filters.Timezone)
	if err != nil {
		return nil, err
	}
	filters.Timezone = canonicalTZ

	if filters.StartTime.IsZero() || filters.EndTime.IsZero() {
		return nil, infraerrors.BadRequest("INVALID_TIME_RANGE", "start_time and end_time are required")
	}
	if !filters.EndTime.After(filters.StartTime) {
		return nil, infraerrors.BadRequest("INVALID_TIME_RANGE", "end_time must be after start_time")
	}
	filters.StartTime = filters.StartTime.UTC()
	filters.EndTime = filters.EndTime.UTC()

	filters.Locale = NormalizeUsageExportLocale(filters.Locale)
	filters.BalanceUnitName = strings.TrimSpace(filters.BalanceUnitName)
	if filters.BalanceUnitName == "" {
		filters.BalanceUnitName = defaultUsageExportBalanceUnit
	}

	sanitizeOptionalExportPointers(filters)

	if scope == UsageExportScopeUser {
		// Pin user exports to the authenticated caller and owned-team scope while
		// stripping administrator-only selectors that could leak provider or system rows.
		uid := actorUserID
		filters.UserID = &uid
		filters.IncludeOwnedTeam = true
		filters.ProviderID = nil
		filters.RequestSource = ""
	} else {
		// Administrator exports cover all users unless a specific user filter is chosen;
		// IncludeOwnedTeam applies only when UserID is unset or explicitly requested.
		filters.IncludeOwnedTeam = false
		if !ValidRequestSource(filters.RequestSource) {
			return nil, infraerrors.BadRequest("INVALID_REQUEST_SOURCE", "invalid request_source")
		}
	}

	return loc, nil
}

// sanitizeOptionalExportPointers clears nonpositive IDs, trims optional strings,
// and resolves RequestType precedence over legacy Stream flags in place.
func sanitizeOptionalExportPointers(filters *UsageExportFilters) {
	if filters.UserID != nil && *filters.UserID <= 0 {
		filters.UserID = nil
	}
	if filters.APIKeyID != nil && *filters.APIKeyID <= 0 {
		filters.APIKeyID = nil
	}
	if filters.ProviderID != nil && *filters.ProviderID <= 0 {
		filters.ProviderID = nil
	}
	if filters.GroupID != nil && *filters.GroupID <= 0 {
		filters.GroupID = nil
	}
	if filters.TeamID != nil && *filters.TeamID <= 0 {
		filters.TeamID = nil
	}
	if filters.PersonalOnly && filters.TeamID != nil {
		// Explicit team selection and personal-only restriction are mutually exclusive;
		// clear PersonalOnly when a positive TeamID is supplied, matching usage list queries.
		filters.PersonalOnly = false
	}
	filters.RequestID = strings.TrimSpace(filters.RequestID)
	if filters.Model != nil {
		trimmed := strings.TrimSpace(*filters.Model)
		if trimmed == "" {
			filters.Model = nil
		} else {
			filters.Model = &trimmed
		}
	}
	filters.ModelFilterSource = normalizeExportModelFilterSource(filters.ModelFilterSource)
	if filters.RequestType != nil {
		rt := RequestType(*filters.RequestType)
		if !rt.IsValid() {
			filters.RequestType = nil
		} else {
			v := int16(rt.Normalize())
			filters.RequestType = &v
			filters.Stream = nil
		}
	}
	if filters.BillingType != nil && *filters.BillingType < 0 {
		filters.BillingType = nil
	}
	if filters.BillingMode != nil {
		mode := strings.ToLower(strings.TrimSpace(*filters.BillingMode))
		switch mode {
		case "token", "per_request", "image", "video":
			filters.BillingMode = &mode
		default:
			filters.BillingMode = nil
		}
	}
}

// normalizeExportModelFilterSource restricts model dimension selectors to
// supported values so persisted filters match list query semantics.
func normalizeExportModelFilterSource(source string) string {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "requested", "upstream", "mapping":
		return strings.ToLower(strings.TrimSpace(source))
	default:
		return ""
	}
}

// ToUsageLogFilters converts persisted export filters into the repository's
// UsageLogFilters query representation.
func (f UsageExportFilters) ToUsageLogFilters() UsageLogFilters {
	start := f.StartTime
	end := f.EndTime
	out := UsageLogFilters{
		StartTime:          &start,
		EndTime:            &end,
		IncludeOwnedTeam:   f.IncludeOwnedTeam,
		PersonalOnly:       f.PersonalOnly,
		RequestID:          f.RequestID,
		RequestSource:      f.RequestSource,
		ModelFilterSource:  f.ModelFilterSource,
		RequestType:        f.RequestType,
		Stream:             f.Stream,
		NativeCompactionV2: f.NativeCompactionV2,
		BillingType:        f.BillingType,
		ExactTotal:         true,
	}
	if f.UserID != nil {
		out.UserID = *f.UserID
	}
	if f.APIKeyID != nil {
		out.APIKeyID = *f.APIKeyID
	}
	if f.ProviderID != nil {
		out.ProviderID = *f.ProviderID
	}
	if f.GroupID != nil {
		out.GroupID = *f.GroupID
	}
	if f.TeamID != nil {
		out.TeamID = *f.TeamID
	}
	if f.Model != nil {
		out.Model = *f.Model
	}
	if f.BillingMode != nil {
		out.BillingMode = *f.BillingMode
	}
	return out
}
