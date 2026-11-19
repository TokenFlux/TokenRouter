package live

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
)

// controller.go coordinates Live sideband proxying, background observation,
// lease renewal and final call cleanup.
// A Live call can move between background observation and a client sideband
// proxy while its persistent concurrency lease continues. Controller labels in
// the call store decide who opens a control connection, and polling lets an
// observer notice a proxy takeover; waiting one polling interval is not a join
// with the earlier observer.
//
// Relay reads progress independently of lease renewal, controller checks and
// logical expiry. Transient control failures can reopen observation, while
// lease loss and terminal call signals enter closure. Only an acknowledged
// first close claim permits lease-release and zero-usage callback attempts.
// Those later effects are separate operations, so a winning claim does not
// establish recording delivery or retry failed cleanup. Observer shutdown
// stops local work while saved remote calls retain their own lifetime.

// These durations and counts govern lease renewal, controller takeover polling,
// bounded storage recovery, cleanup command budgets, and retained closed-call
// lookup.
const (
	liveLeaseRefreshInterval    = 20 * time.Second       // period between persistent lease renewals
	liveRedisOperationTimeout   = 3 * time.Second        // detached lease and cleanup command budget
	liveClosedRecordTTL         = 24 * time.Hour         // retention after acknowledged call closure
	liveObserverPollInterval    = 250 * time.Millisecond // period between observer controller checks
	liveObserverStoreRetryLimit = 5                      // consecutive failed record reads before expiry wait
)

// ProxyLiveSideband claims a saved call for authenticated sideband control and
// relays client and upstream frames. Nil call or downstream values return not
// found; the caller owns downstream transport closure and supplies a usable
// context.
//
// After claiming, it waits one observer poll interval rather than joining the
// prior observer. Two relay goroutines share cancellation and have one
// buffered terminal result each. Controller expiry or lease loss finalizes;
// another failure relinquishes proxy control and requests a detached observer.
// The upstream closes on return, while canceled relays are not joined here and
// best-effort controller release may fail.
func (s *Service) ProxyLiveSideband(
	ctx context.Context,
	record *session.LiveCallRecord,
	downstream FrameConn,
) error {
	if record == nil || downstream == nil {
		return session.ErrLiveCallNotFound
	}
	store, err := s.ports.Store()
	if err != nil {
		return err
	}
	owner := uuid.NewString()
	claimed, err := store.ClaimLiveController(ctx, record.CallHash, session.LiveControllerProxy, owner)
	if err != nil {
		return err
	}
	if !claimed {
		return session.ErrLiveControllerChanged
	}

	// Use a detached cleanup budget after proxy cancellation. The release result
	// is discarded, so a later observer must claim stored ownership rather than
	// assume this cleanup succeeded.
	releaseController := func() {
		cleanup, done := context.WithTimeout(context.Background(), liveRedisOperationTimeout)
		defer done()
		_, _ = store.ReleaseLiveController(cleanup, record.CallHash, owner)
	}
	// Wait one polling interval to give an earlier observer a chance to notice
	// takeover. This delay supplies no completion acknowledgement or guarantee
	// that its connection has closed.
	// Relinquish this claimed proxy on canceled handoff, then request
	// application-owned observation so the saved call can continue outside the
	// canceled HTTP controller context.
	if !waitLiveObserver(ctx, liveObserverPollInterval) || ctx.Err() != nil {
		releaseController()
		go s.Observe(record)
		return context.Cause(ctx)
	}
	provider, err := s.ports.Target(ctx, record)
	// Target lookup failure ends proxy setup; release its claim and let a
	// detached observer retry the persisted call through its own provider and
	// expiry handling.
	if err != nil {
		releaseController()
		go s.Observe(record)
		return err
	}
	// Stop after a target lookup that outlasted client cancellation, releasing
	// proxy ownership before another background task can attempt observing this
	// existing call.
	if err := ctx.Err(); err != nil {
		releaseController()
		go s.Observe(record)
		return context.Cause(ctx)
	}
	upstream, err := provider.Dial(ctx)
	// A failed sideband dial leaves the created call available; relinquish proxy
	// control and let detached observation reconnect while the saved session
	// still has lifetime remaining.
	if err != nil {
		releaseController()
		go s.Observe(record)
		return err
	}
	// Close the upstream on proxy exit even when relay or lease handling fails;
	// transport-close errors are discarded after the controller result has been
	// selected.
	defer func() { _ = upstream.Close() }()
	downstream.SetReadLimit(s.readLimit)

	proxyCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Buffer one terminal result for each relay direction, so both can report
	// exit even after RunController stops consuming results and cancellation is
	// signaled.
	errCh := make(chan error, 2)
	modelState := newLiveSidebandModelState(record)
	go func() {
		for {
			messageType, payload, readErr := downstream.ReadFrame(proxyCtx)
			if readErr != nil {
				errCh <- readErr
				return
			}
			if messageType == TextFrame || isLiveJSONObjectFrame(payload) {
				rewritten, clientModel, internalModels, rewriteErr := provider.Rewrite(proxyCtx, payload)
				if rewriteErr != nil {
					errCh <- rewriteErr
					return
				}
				payload = rewritten
				// Publish the locally rewritten model pair before forwarding this frame.
				// It describes the current rewrite even if the subsequent upstream write
				// fails, not an upstream acknowledgement.
				if clientModel != "" {
					modelState.update(clientModel, internalModels...)
				}
			}
			if writeErr := upstream.WriteFrame(proxyCtx, messageType, payload); writeErr != nil {
				errCh <- writeErr
				return
			}
		}
	}()
	go func() {
		for {
			messageType, payload, readErr := upstream.ReadFrame(proxyCtx)
			if readErr != nil {
				errCh <- readErr
				return
			}
			if messageType == TextFrame {
				// Restore this event from the current coherent model pair; the state
				// follows the latest local rewrite and does not associate a server frame
				// with an earlier turn.
				clientModel, internalModels := modelState.snapshot()
				payload = RestoreServerPayload(payload, clientModel, internalModels)
			}
			if writeErr := downstream.WriteFrame(proxyCtx, messageType, payload); writeErr != nil {
				errCh <- writeErr
				return
			}
			if messageType == TextFrame {
				eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
				if eventType == "session.closed" || eventType == "session.ended" {
					errCh <- session.ErrLiveCallNotFound
					return
				}
			}
		}
	}()

	// Cancel both relay contexts before relinquishing proxy ownership. Their
	// goroutines are not joined here; terminal calls finalize, while another
	// controller failure can restart observation.
	runErr := s.RunController(proxyCtx, record, upstream, errCh)
	cancel()
	releaseController()
	if SessionEnded(runErr) || !time.Now().Before(record.ExpiresAt) {
		s.Finalize(record)
		return runErr
	}
	go s.Observe(record)
	return runErr
}

// isLiveJSONObjectFrame reports whether payload begins with a valid JSON object
// so binary control frames carrying JSON session updates still enter rewriting.
func isLiveJSONObjectFrame(payload []byte) bool {
	trimmed := bytes.TrimSpace(payload)
	return len(trimmed) > 0 && trimmed[0] == '{' && gjson.ValidBytes(trimmed)
}

// SessionEnded reports whether a terminal call signal, unavailable lease, or
// exceeded deadline requires finalization. Ordinary context cancellation is
// excluded from this decision so a canceled proxy can resume background
// observation for the saved call.
func SessionEnded(err error) bool {
	return errors.Is(err, session.ErrLiveCallNotFound) ||
		errors.Is(err, session.ErrLiveUnavailable) ||
		errors.Is(err, context.DeadlineExceeded)
}

// RunController renews a usable call lease while waiting for relay
// termination, cancellation, or logical expiry. Callers retain record and
// upstream until return and provide an open terminal-result channel; a closed
// channel can return its zero error.
//
// The renewal ticker and expiry timer share the select with independently
// progressing relay work. Expiry attempts a session.close write under a
// detached two-second budget before returning DeadlineExceeded, while renewal
// failure returns LiveUnavailable to end unreserved control. The caller owns
// cancellation and connection cleanup after this result; simultaneous ready
// cases have no priority guarantee.
func (s *Service) RunController(
	ctx context.Context,
	record *session.LiveCallRecord,
	upstream FrameConn,
	errCh <-chan error,
) error {
	refreshTicker := time.NewTicker(liveLeaseRefreshInterval)
	defer refreshTicker.Stop()
	// Use the saved expiry on each control connection, so reconnecting retains
	// the original call's remaining lifetime rather than starting another full
	// session budget.
	maxTimer := time.NewTimer(time.Until(record.ExpiresAt))
	defer maxTimer.Stop()
	for {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case err := <-errCh:
			return err
		case <-maxTimer.C:
			// Attempt the close under an independent short budget because logical
			// expiry already ends the call; a write error cannot reopen its expired
			// lease lifetime.
			closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = upstream.WriteFrame(closeCtx, TextFrame, []byte(`{"type":"session.close"}`))
			cancel()
			return context.DeadlineExceeded
		case <-refreshTicker.C:
			if !s.RefreshLease(record) {
				return session.ErrLiveUnavailable
			}
		}
	}
}

// Observe registers a unique background task and claims observer control for
// the supplied saved call. Nil records and rejected task admission are
// ignored, and accepted cleanup runs once when this observer exits.
//
// Claim errors or five consecutive record-read errors retain the last snapshot
// for an expiry finalization attempt. Successful reads replace that snapshot,
// while confirmed controller changes stop observation before dialing.
// Transient controller-read failures permit another outer record read without
// confirming ownership. Lease loss or terminal connection signals finalize;
// each opened connection closes after its loop, and application cancellation
// ends local work while the saved call remains available.
func (s *Service) Observe(record *session.LiveCallRecord) {
	if record == nil {
		return
	}
	owner := uuid.NewString()
	ctx, finish, ok := s.ports.BeginObserver(owner)
	if !ok {
		return
	}
	defer finish()

	store, err := s.ports.Store()
	if err != nil {
		return
	}
	claimed, claimErr := store.ClaimLiveController(ctx, record.CallHash, session.LiveControllerObserver, owner)
	if ctx.Err() != nil {
		return
	}
	if claimErr != nil {
		// Keep the last snapshot when the claim errors and attempt closure at its
		// retained expiry. Finalize can still skip its effects if storage is
		// unavailable then.
		s.FinalizeAfterExpiry(ctx, record)
		return
	}
	if !claimed {
		return
	}
	storeErrStreak := 0
	for {
		if ctx.Err() != nil {
			return
		}
		latest, getErr := store.GetLiveCall(ctx, record.CallHash)
		if ctx.Err() != nil {
			return
		}
		if getErr != nil {
			if errors.Is(getErr, session.ErrLiveCallNotFound) {
				return
			}
			// Bound consecutive record-read failures before waiting on the retained
			// expiry snapshot, so unavailable storage cannot drive an unlimited rapid
			// lookup loop for this observer.
			storeErrStreak++
			if storeErrStreak >= liveObserverStoreRetryLimit {
				s.FinalizeAfterExpiry(ctx, record)
				return
			}
			if !waitLiveObserver(ctx, s.retryInterval) {
				return
			}
			continue
		}
		storeErrStreak = 0
		record = latest
		if record.Controller != session.LiveControllerObserver {
			return
		}
		if !time.Now().Before(record.ExpiresAt) {
			s.Finalize(record)
			return
		}
		// Open one observer connection from the latest saved record, then close any
		// returned connection if application cancellation wins before its controller
		// loop can begin.
		upstream, dialErr := s.dial(ctx, record)
		if ctx.Err() != nil {
			if upstream != nil {
				_ = upstream.Close()
			}
			return
		}
		// Retry a transient observer dial while stored controller ownership remains
		// observer; missing calls or takeover end the retry path instead of opening
		// another control connection.
		if dialErr != nil {
			if !s.WaitForObserverRetry(ctx, record) {
				return
			}
			continue
		}
		// Close the completed observer transport before interpreting its result or
		// retrying, keeping each reconnect attempt responsible for the connection it
		// successfully opened.
		runErr := s.RunObserverConnection(ctx, record, upstream)
		_ = upstream.Close()
		if ctx.Err() != nil {
			return
		}
		if errors.Is(runErr, session.ErrLiveControllerChanged) {
			return
		}
		if SessionEnded(runErr) {
			s.Finalize(record)
			return
		}
		if !s.WaitForObserverRetry(ctx, record) {
			return
		}
	}
}

// RunObserverConnection monitors one owned upstream connection while
// controller polling, lease renewal, and expiry run independently. The caller
// provides a usable call record, shared store, and context, and closes the
// upstream after this method returns.
//
// A reader goroutine sends text frames or a terminal error through bounded
// channels with cancellation alternatives. Controller changes stop the loop,
// terminal frames report not found, and lease loss reports unavailable.
// Controller store reads use Background directly, so those calls have no
// timeout here. Expiry attempts session.close with a separate two-second
// budget; deferred cancellation signals the reader but does not join it.
func (s *Service) RunObserverConnection(parent context.Context, record *session.LiveCallRecord, upstream FrameConn) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	frameCh := make(chan []byte, 1)
	errCh := make(chan error, 1)
	// Read independently of controller timers and store polling, with
	// cancellation-aware channel sends so a stopped monitoring loop does not
	// retain a blocked result sender.
	go func() {
		for {
			messageType, payload, err := upstream.ReadFrame(ctx)
			if err != nil {
				select {
				case errCh <- err:
				case <-ctx.Done():
				}
				return
			}
			if messageType == TextFrame {
				select {
				case frameCh <- payload:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	refreshTicker := time.NewTicker(liveLeaseRefreshInterval)
	defer refreshTicker.Stop()
	controllerTicker := time.NewTicker(liveObserverPollInterval)
	defer controllerTicker.Stop()
	// Use the saved expiry on each control connection, so reconnecting retains
	// the original call's remaining lifetime rather than starting another full
	// session budget.
	maxTimer := time.NewTimer(time.Until(record.ExpiresAt))
	defer maxTimer.Stop()
	// Observe already checked shared-store availability before this loop. This
	// reacquisition error is discarded, so direct callers likewise supply
	// adapters returning a usable store here.
	store, _ := s.ports.Store()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case payload := <-frameCh:
			eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
			if eventType == "session.closed" || eventType == "session.ended" {
				return session.ErrLiveCallNotFound
			}
		case err := <-errCh:
			return err
		// Check persisted controller state to detect proxy takeover. Background
		// polling here has no command deadline, so this storage call can delay later
		// timer or cancellation handling.
		case <-controllerTicker.C:
			controller, err := store.GetLiveController(context.Background(), record.CallHash)
			if err != nil {
				return err
			}
			if controller != session.LiveControllerObserver {
				return session.ErrLiveControllerChanged
			}
		case <-refreshTicker.C:
			if !s.RefreshLease(record) {
				return session.ErrLiveUnavailable
			}
		case <-maxTimer.C:
			// Attempt the close under an independent short budget because logical
			// expiry already ends the call; a write error cannot reopen its expired
			// lease lifetime.
			closeCtx, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = upstream.WriteFrame(closeCtx, TextFrame, []byte(`{"type":"session.close"}`))
			closeCancel()
			return context.DeadlineExceeded
		}
	}
}

// WaitForObserverRetry waits one second and decides whether the observer
// should reenter its outer loop. Callers supply a usable context and record
// for the CallHash lookup; ExpiresAt remains the next loop's responsibility.
//
// Missing infrastructure, cancellation, a missing call, or changed controller
// ends retry. Another controller-read error returns true so the outer
// record-read path can count storage failures and eventually finalize from its
// saved snapshot. A confirmed observer controller also returns true, including
// after logical expiry, letting that outer loop execute its explicit
// finalization check.
func (s *Service) WaitForObserverRetry(ctx context.Context, record *session.LiveCallRecord) bool {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
	}
	store, err := s.ports.Store()
	if err != nil {
		return false
	}
	controller, getErr := store.GetLiveController(context.Background(), record.CallHash)
	if getErr != nil && !errors.Is(getErr, session.ErrLiveCallNotFound) {
		// Return to the outer record-read path after a transient controller-read
		// error; that path owns consecutive failure counting and finalization from
		// the last usable snapshot.
		return true
	}
	// A confirmed observer returns to the outer expiry check, which can claim
	// closure and release its lease; this helper leaves that snapshot lifetime
	// decision there.
	return getErr == nil && controller == session.LiveControllerObserver
}

// FinalizeAfterExpiry waits until the snapshot's logical expiry before
// attempting closure. Nil records are ignored; cancellation skips Finalize
// when it wins the positive wait, while an already expired record proceeds
// directly even with a canceled context.
func (s *Service) FinalizeAfterExpiry(ctx context.Context, record *session.LiveCallRecord) {
	if record == nil {
		return
	}
	if wait := time.Until(record.ExpiresAt); wait > 0 {
		if !waitLiveObserver(ctx, wait) {
			return
		}
	}
	s.Finalize(record)
}

// RefreshLease renews all Live lease dimensions using a detached three-second
// operation budget. The record must be usable; unavailable cache, command
// errors, and refused renewal return false so controllers stop using an
// unreserved connection.
func (s *Service) RefreshLease(record *session.LiveCallRecord) bool {
	// Treat unavailable lease storage as unconfirmed renewal so the controller
	// stops using this connection; another dial cannot substitute for a
	// reservation that was not refreshed.
	cache, err := s.ports.Leases()
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), liveRedisOperationTimeout)
	defer cancel()
	refreshed, err := cache.RefreshLiveLease(ctx, record.ProviderID, record.UserID, record.APIKeyID, record.LeaseID)
	return err == nil && refreshed
}

// ReleaseLease attempts provider, user, and API Key lease cleanup under a
// detached three-second budget. Unavailable cache and command errors are
// ignored; callers receive no confirmation and datastore expiry remains
// separate cleanup behavior.
func (s *Service) ReleaseLease(providerID, userID, apiKeyID int64, leaseID string) {
	// Skip the cleanup attempt when lease storage is unavailable; the caller
	// receives no release acknowledgement and any stored reservation remains
	// governed by its own expiry.
	cache, err := s.ports.Leases()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), liveRedisOperationTimeout)
	defer cancel()
	// Attempt cleanup despite controller cancellation and ignore its command
	// error; any remaining datastore lease entries keep the expiration behavior
	// configured by their acquisition or renewal.
	_ = cache.ReleaseLiveLease(ctx, providerID, userID, apiKeyID, leaseID)
}

// Finalize attempts one close claim for the supplied call and, on acknowledged
// success, releases its lease and requests zero-cost usage recording. Nil
// records, unavailable storage, errors, and previously closed calls skip those
// effects.
//
// The stored close marker wins before cleanup so competing paths cannot both
// invoke recording. ReleaseLease is best effort and RecordZeroUsage exposes no
// delivery result; after a successful claim, another Finalize will not retry
// those later operations. Duration is elapsed whole milliseconds clamped to
// zero, and recording receives Background so the controller's cancellation
// does not suppress the callback attempt.
func (s *Service) Finalize(record *session.LiveCallRecord) {
	if record == nil {
		return
	}
	store, err := s.ports.Store()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), liveRedisOperationTimeout)
	// Claim closure before releasing reservation or invoking usage recording, so
	// another finalizer sees the closed marker and cannot issue the same callback
	// attempt for this call.
	first, err := store.MarkLiveCallClosed(ctx, record.CallHash, liveClosedRecordTTL)
	cancel()
	if err != nil || !first {
		return
	}
	s.ReleaseLease(record.ProviderID, record.UserID, record.APIKeyID, record.LeaseID)
	// Record whole elapsed milliseconds and clamp future-created snapshots to
	// zero.
	duration := max(int(time.Since(record.CreatedAt).Milliseconds()), 0)
	s.ports.RecordZeroUsage(context.Background(), record, duration)
}

// waitLiveObserver waits for the supplied timer or context cancellation and
// reports the winning case. Nonpositive delays make the timer ready;
// cancellation can still win, and callers provide a nonnil context.
func waitLiveObserver(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// Lookup loads the exact hashed call identifier and checks its API Key, payer
// user, and optional group against the requester. Closed records return not
// found; logical expiry is handled by controller timers rather than rejected
// in this lookup.
func (s *Service) Lookup(ctx context.Context, callID string, identity session.LiveCallIdentity) (*session.LiveCallRecord, error) {
	store, err := s.ports.Store()
	if err != nil {
		return nil, err
	}
	record, err := store.GetLiveCall(ctx, HashCallID(callID))
	if err != nil {
		return nil, err
	}
	if record.CallID != callID || record.APIKeyID != identity.APIKeyID || record.UserID != identity.UserID || record.GroupID != liveGroupID(identity.GroupID) {
		return nil, session.ErrLiveIdentityMismatch
	}
	if record.Controller == session.LiveControllerClosed {
		return nil, session.ErrLiveCallNotFound
	}
	return record, nil
}
