package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// email.go separates durable email intent from external transport delivery.
// The ledger commits quota consumption and a pending claim before any sender
// runs. Completed or skipped results are persisted afterward; transport uncertainty,
// abandoned claims, and failure to persist an outcome never authorize a resend.
// Known pre-transport failures retain their terminal error for later replay.
//
// A local key guard spans claim, send, and result persistence so concurrent calls
// wait for the original outcome. Waiter cancellation does not change the owner's
// claim. Guards retain queued references before waiting and disappear after their
// final owner or waiter leaves. A pending row observed after acquiring this guard
// has no active sender in this single instance and becomes an unknown outcome.

// EmailInput contains one account-targeted plain-text notification intent before validation.
type EmailInput struct {
	UserID   int64  // target recipient account identifier
	Subject  string // bounded plain-text message subject
	BodyText string // bounded plain-text message body
	Key      string // plugin-wide operation replay key
}

// EmailResult stores the durable delivery decision and transient replay observation.
type EmailResult struct {
	Delivered   bool   `json:"delivered"`              // known transport acceptance or existing marker
	Replayed    bool   `json:"replayed"`               // original operation result was reused
	ErrorReason string `json:"error_reason,omitempty"` // safe terminal failure classification
}

// EmailDelivery distinguishes accepted transport from skipped or unattempted delivery.
type EmailDelivery struct {
	Delivered bool // known transport acceptance result
	Attempted bool // external sender was invoked
}

// EmailSender resolves account recipients and applies the existing notification delivery rules.
type EmailSender interface {
	// Deliver reports transport acceptance independently of a later marker persistence error.
	// Skipped recipients return false without an error. Attempted distinguishes
	// transport uncertainty from failure before the external sender was invoked.
	Deliver(context.Context, Binding, EmailInput) (EmailDelivery, error)
}

// EmailResultStore finalizes pending email claims without creating another operation.
type EmailResultStore interface {
	// FinalizeEmail replaces one pending claim with its terminal state and result.
	// The operation already exists under this plugin's replay key. Changed or
	// missing claims fail instead of overwriting an independently completed outcome.
	FinalizeEmail(context.Context, string, string, LedgerState, json.RawMessage) error
}

// Emails owns single-instance delivery coordination around the durable claim ledger.
type Emails struct {
	writes   *Writes          // shared claim and quota transaction engine
	store    EmailResultStore // durable terminal result writer
	sender   EmailSender      // account-aware notification transport adapter
	location *time.Location   // host calendar used for daily quotas
	logger   *slog.Logger     // outcome persistence diagnostic destination
	// mu protects calls and reference counts. Guard ownership spans actual sender return and result persistence independently of waiting callers' cancellation.
	mu    sync.Mutex
	calls map[string]*emailGuard // retained key coordination entries
}

// emailGuard retains one admission token and every registered owner or waiter.
type emailGuard struct {
	token chan struct{} // one available admission token
	refs  int           // registered owner and waiter count
}

// NewEmails binds durable claims, recipient delivery, and the host quota calendar.
// Nil calendar and logger select time.Local and slog. Construction starts no
// delivery; the application retains these dependencies until plugin callbacks drain.
func NewEmails(writes *Writes, store EmailResultStore, sender EmailSender, location *time.Location, logger *slog.Logger) *Emails {
	if location == nil {
		location = time.Local
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Emails{writes: writes, store: store, sender: sender, location: location, logger: logger, calls: make(map[string]*emailGuard)}
}

// Send claims one normalized message before delivery and returns its persisted outcome.
// Permission and argument checks precede the key guard. Concurrent calls wait
// for the original operation; different intent under its key fails through the
// shared ledger comparison without invoking the external sender again.
//
// SMTP runs only after a new pending claim commits. Errors after transport
// invocation become unknown, while earlier failures are terminal known failures.
// Acceptance remains known even if the notification service's marker write fails.
// Failure to store this method's authoritative outcome returns unknown, retaining
// replay protection and preventing another automatic attempt at external delivery.
func (s *Emails) Send(ctx context.Context, binding Binding, input EmailInput) (*EmailResult, error) {
	if !slices.Contains(binding.Permissions, "notify.email") {
		return nil, ErrPermissionNotGranted
	}
	// Canonicalize text before comparing replay intent so equivalent line endings and surrounding subject whitespace share the recorded operation.
	input.Subject = strings.TrimSpace(input.Subject)
	input.BodyText = strings.ReplaceAll(strings.ReplaceAll(input.BodyText, "\r\n", "\n"), "\r", "\n")
	if err := validateEmailInput(input); err != nil {
		return nil, err
	}
	release, err := s.acquire(ctx, binding.PluginID+":"+input.Key)
	if err != nil {
		return nil, err
	}
	defer release()
	intent := struct {
		UserID   int64  `json:"user_id"`   // target account identifier
		Subject  string `json:"subject"`   // normalized message subject text
		BodyText string `json:"body_text"` // normalized message body text
	}{UserID: input.UserID, Subject: input.Subject, BodyText: input.BodyText}
	request, err := json.Marshal(intent)
	if err != nil {
		return nil, err
	}
	mutation := Mutation{Key: input.Key, Kind: LedgerEmailSend, UserID: &input.UserID, Amount: "1", Request: request, State: LedgerPending, Reason: input.Subject}
	// Commit the quota-bearing pending row before invoking SMTP; a crash after this boundary must leave durable evidence that blocks resending.
	claim, err := s.writes.Apply(ctx, binding, mutation, func(bound context.Context, tx LedgerTransaction, limits Limits, now time.Time) error {
		return s.checkQuota(bound, tx, limits.Email, now, input.UserID)
	}, func(context.Context) (json.RawMessage, error) { return json.RawMessage(`{"delivered":false}`), nil })
	if err != nil {
		return nil, err
	}
	if claim.Replayed {
		return s.replay(ctx, binding.PluginID, input.Key, claim.Entry)
	}
	delivery, deliveryErr := s.sender.Deliver(ctx, binding, input)
	result := &EmailResult{Delivered: delivery.Delivered}
	state := LedgerCompleted
	// A notification marker failure cannot reverse known SMTP acceptance, so record delivery while logging the auxiliary persistence problem.
	if deliveryErr != nil && delivery.Delivered {
		s.logger.Warn("Plugin email was accepted but its notification marker failed", "plugin_id", binding.PluginID)
	}
	if deliveryErr != nil && !delivery.Delivered {
		state = LedgerFailed
		result.ErrorReason = "HOST_UNAVAILABLE"
		if errors.Is(deliveryErr, ErrHostUserNotFound) {
			result.ErrorReason = "USER_NOT_FOUND"
		}
		if delivery.Attempted {
			state = LedgerUnknown
			result.ErrorReason = "EMAIL_DELIVERY_UNKNOWN"
		}
	}
	if err := s.finish(ctx, binding.PluginID, input.Key, state, result); err != nil {
		return &EmailResult{ErrorReason: "EMAIL_DELIVERY_UNKNOWN"}, ErrEmailDeliveryUnknown
	}
	return result, emailResultError(result.ErrorReason)
}

// replay returns a terminal result or converts an abandoned pending claim to unknown.
// The caller owns the key guard, so a pending row has no active sender in this
// instance. Corrupt stored outcomes remain unknown and cannot trigger delivery.
//
// Unknown conversion receives a fresh bounded context because the original
// caller may already be canceled. Persistence failure leaves the pending claim
// intact and returns the same no-resend error. Terminal failures retain their
// original classification, while successful or skipped outcomes reuse the recorded
// delivered flag without consuming quota or calling the notification service again.
func (s *Emails) replay(ctx context.Context, id, key string, entry *LedgerEntry) (*EmailResult, error) {
	result := &EmailResult{Replayed: true}
	if entry.State == LedgerPending {
		result.ErrorReason = "EMAIL_DELIVERY_UNKNOWN"
		// Persisting uncertainty improves later inspection; failure still leaves a claim that blocks every future resend of this operation.
		_ = s.finish(ctx, id, key, LedgerUnknown, result)
		return result, ErrEmailDeliveryUnknown
	}
	// Unreadable durable results cannot establish safe delivery state; report uncertainty while preserving the original row and its replay protection.
	if err := json.Unmarshal(entry.Result, result); err != nil {
		return &EmailResult{Replayed: true, ErrorReason: "EMAIL_DELIVERY_UNKNOWN"}, ErrEmailDeliveryUnknown
	}
	result.Replayed = true
	if entry.State == LedgerUnknown || (entry.State == LedgerFailed && result.ErrorReason == "") || (entry.State != LedgerCompleted && entry.State != LedgerFailed) {
		result.Delivered = false
		result.ErrorReason = "EMAIL_DELIVERY_UNKNOWN"
	}
	return result, emailResultError(result.ErrorReason)
}

// finish persists the authoritative terminal result after external delivery has stopped.
// Its context survives caller cancellation for at most five seconds. Failure is
// logged without message content and leaves the claim available for conservative replay.
func (s *Emails) finish(ctx context.Context, id, key string, state LedgerState, result *EmailResult) error {
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}
	// Outcome recording receives its own budget so a canceled plugin request cannot suppress the authoritative decision after SMTP has returned.
	final, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.store.FinalizeEmail(final, id, key, state, data); err != nil {
		s.logger.Warn("Plugin email outcome could not be persisted", "plugin_id", id, "error", err)
		return err
	}
	return nil
}

// checkQuota counts all email claims, including skipped, failed, and uncertain attempts.
// The ledger lock protects both per-user and aggregate comparisons. Calendar-day
// windows use the same accounting timestamp stored on this accepted delivery claim.
func (s *Emails) checkQuota(ctx context.Context, tx LedgerTransaction, limits EmailLimits, now time.Time, userID int64) error {
	since, until := dayWindow(now, s.location)
	query := LedgerQuery{Kind: LedgerEmailSend, UserID: userID, Since: since, Until: until}
	user, err := tx.Totals(ctx, query)
	if err != nil {
		return err
	}
	if user.Count >= limits.MaxPerUserPerDay {
		return ErrLimitExceeded.WithMetadata(map[string]string{"limit": "email.max_per_user_per_day"})
	}
	query.UserID = 0
	all, err := tx.Totals(ctx, query)
	if err != nil {
		return err
	}
	if all.Count >= limits.MaxPerDay {
		return ErrLimitExceeded.WithMetadata(map[string]string{"limit": "email.max_per_day"})
	}
	return nil
}

// acquire retains one guard through its owner and every registered waiting caller.
// Cancellation before or after admission returns without starting a sender.
// The returned release function is safe to invoke more than once.
//
// Registration happens under the map lock before waiting, preventing deletion
// from creating a second token for an already queued key. The final reference
// deletes only its own entry. Owner release returns the token before dropping
// its reference, so waiting callers keep the same serialization point across
// claim lookup, external delivery, and durable outcome persistence.
func (s *Emails) acquire(ctx context.Context, key string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Retain queued references before waiting so owner cleanup cannot replace this key with an independently usable admission token.
	s.mu.Lock()
	entry := s.calls[key]
	if entry == nil {
		entry = &emailGuard{token: make(chan struct{}, 1)}
		entry.token <- struct{}{}
		s.calls[key] = entry
	}
	entry.refs++
	s.mu.Unlock()
	drop := func() {
		s.mu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(s.calls, key)
		}
		s.mu.Unlock()
	}
	select {
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	case <-entry.token:
	}
	if err := ctx.Err(); err != nil {
		entry.token <- struct{}{}
		drop()
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			entry.token <- struct{}{}
			drop()
		})
	}, nil
}

// validateEmailInput checks recipient identity, replay syntax, and plain-text content limits.
// Subjects cannot contain header controls. Body line breaks remain valid, while
// malformed UTF-8 and NUL fail before PostgreSQL JSONB or SMTP sees the request.
func validateEmailInput(input EmailInput) error {
	// Protocol text limits count Unicode characters, not bytes.
	subjectLength := utf8.RuneCountInString(input.Subject)
	bodyLength := utf8.RuneCountInString(input.BodyText)
	if input.UserID <= 0 || !utf8.ValidString(input.Subject) || !utf8.ValidString(input.BodyText) ||
		subjectLength < 1 || subjectLength > 120 || bodyLength < 1 || bodyLength > 8000 ||
		strings.ContainsRune(input.BodyText, 0) {
		return ErrInvalidArgument
	}
	for _, character := range input.Subject {
		if unicode.IsControl(character) {
			return ErrInvalidArgument
		}
	}
	return validateOperation(input.Key, "")
}

// emailResultError restores the safe protocol error associated with a terminal outcome.
func emailResultError(reason string) error {
	switch reason {
	case "":
		return nil
	case "USER_NOT_FOUND":
		return ErrHostUserNotFound
	case "EMAIL_DELIVERY_UNKNOWN":
		return ErrEmailDeliveryUnknown
	default:
		return ErrHostUnavailable
	}
}
