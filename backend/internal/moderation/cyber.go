package moderation

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// cyber.go records upstream Cyber policy warnings, evaluates group and model
// scope gates, queries warning summaries, and dispatches post-commit user
// notifications. Scope predicates check both global risk enablement and
// configured group or model filters before gateway session blocks or warning
// persistence take effect.
//
// Transactional Cyber warning persistence locks the actor, stores warning and
// media records, and applies threshold bans atomically in storage before this
// layer invalidates authentication caches and sends alert emails. Post-commit
// cache invalidation or email failures never roll back a committed user ban.

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

// GetCyberSummary delegates grouped warning observations with the supplied
// filter and context. Nil services or missing repositories return an empty
// summary; storage errors propagate when a repository is available.
func (s *ContentModerationService) GetCyberSummary(ctx context.Context, filter ContentModerationCyberWarningFilter) (*ContentModerationCyberSummary, error) {
	if s == nil || s.repo == nil {
		return &ContentModerationCyberSummary{}, nil
	}
	return s.repo.GetCyberSummary(ctx, filter)
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
