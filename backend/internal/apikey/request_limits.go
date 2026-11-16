package apikey

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/TokenFlux/TokenRouter/internal/pkg/requestcontext"
)

// request_limits.go manages API key request admission and the lifetime of
// renewable concurrency leases.
// A consuming request reserves the Key's configured active-request and rolling
// RPM capacity before forwarding. Unlimited keys proceed directly, while a
// limited Key requires the cache's atomic reservation operations. The admission
// gate remains counted for the lifetime of an active concurrency lease so
// service shutdown waits for the request's cleanup.
//
// Renewal continues after client cancellation because handlers can collect
// upstream results after disconnect. The returned context carries a separate
// server-abort signal for that detached work; renewal failure aborts it, and
// the caller releases the lease after processing finishes. Release runs once,
// stops and joins renewal work, then removes the Redis active member using an
// independent cleanup timeout. RPM records remain counted after completion.

// Admission errors distinguish exhausted request capacity from unavailable
// limiter storage so gateway callers can select retry or service-failure responses.
var (
	// ErrKeyConcurrencyExceeded reports that the key has reached its active request limit.
	ErrKeyConcurrencyExceeded = errors.New("API key concurrency limit exceeded")
	// ErrKeyRPMExceeded reports that the key has reached its rolling one-minute request limit.
	ErrKeyRPMExceeded = errors.New("API key RPM limit exceeded")
	// ErrKeyLimiterUnavailable reports an unavailable request-limit store or lease.
	ErrKeyLimiterUnavailable = errors.New("API key request limiter unavailable")
)

// RequestLimitCache reserves admitted requests and maintains their active
// concurrency leases.
type RequestLimitCache interface {
	// ReserveRequest atomically checks configured limits for a Key and request
	// identity, returning a retry delay on rejection. Replays retain the original
	// admission result while its records remain available.
	ReserveRequest(context.Context, int64, string, int, int) (time.Duration, error)
	// RefreshRequest renews an existing unexpired active lease for the supplied
	// Key and request identity. A missing or elapsed lease reports unavailable
	// capacity so processing can stop using its lost reservation.
	RefreshRequest(context.Context, int64, string) error
	// ReleaseRequest removes the active lease for the Key and request identity.
	// RPM history remains counted for its window; callers may retry cleanup
	// when the removal result is uncertain.
	ReleaseRequest(context.Context, int64, string) error
}

// ValidateRequestLimits rejects negative request limits or values outside the
// PostgreSQL int32 range. Nil values mean omitted configuration and zero
// disables a limit; when both supplied fields are invalid, map iteration
// determines the reported field.
func ValidateRequestLimits(concurrency, rpm *int) error {
	for field, value := range map[string]*int{"concurrency_limit": concurrency, "rpm_limit": rpm} {
		if value != nil && (*value < 0 || int64(*value) > math.MaxInt32) {
			return ErrAPIKeyLimitInvalid.WithMetadata(map[string]string{"field": field})
		}
	}
	return nil
}

// AcquireRequest reserves the Key's request limits and returns an admitted
// context, release callback and retry delay. Unlimited keys return a no-op
// release; rejected or unavailable reservations return an error and no release
// callback. Callers supply a nonnil Key and service.
//
// For concurrency-limited work, callers release after all request processing
// finishes, including detached result collection. Renewal uses
// client-independent timeouts, and failures cancel the server-abort signal
// carried by the returned context. Release is idempotent, joins renewal before
// independent cleanup, and completes operation-gate accounting even when Redis
// removal fails. RPM-only requests need no renewal callback work.
// Protected keys also check their current Token rate before reserving capacity.
// @project-doc docs/domains/routing_and_billing.md#api_key_request_limits
func (s *APIKeyService) AcquireRequest(ctx context.Context, key *APIKey) (context.Context, func(), time.Duration, error) {
	if err := s.CheckTokenRateLimit(ctx, key); err != nil {
		return ctx, nil, 0, err
	}
	if key.ConcurrencyLimit <= 0 && key.RPMLimit <= 0 {
		return ctx, func() {}, 0, nil
	}
	cache, ok := s.cache.(RequestLimitCache)
	if !ok {
		return ctx, nil, 0, ErrKeyLimiterUnavailable
	}
	if !s.operations.enter() {
		return ctx, nil, 0, ErrKeyLimiterUnavailable
	}
	id := uuid.NewString()
	reserveCtx, cancelReserve := context.WithTimeout(ctx, 3*time.Second)
	retry, err := cache.ReserveRequest(reserveCtx, key.ID, id, key.ConcurrencyLimit, key.RPMLimit)
	cancelReserve()
	if err != nil {
		// A transport failure can follow successful Redis reservation. Attempt
		// independent cleanup by request ID to remove an uncertain active lease.
		if !errors.Is(err, ErrKeyConcurrencyExceeded) && !errors.Is(err, ErrKeyRPMExceeded) {
			releaseCtx, cancelRelease := context.WithTimeout(context.Background(), 3*time.Second)
			_ = cache.ReleaseRequest(releaseCtx, key.ID, id)
			cancelRelease()
		}
		s.operations.leave()
		return ctx, nil, retry, err
	}
	if key.ConcurrencyLimit <= 0 {
		s.operations.leave()
		return ctx, func() {}, 0, nil
	}
	requestCtx, abort := requestcontext.WithAbort(ctx)
	stop := make(chan struct{})
	done := make(chan struct{})
	// This worker retains the service operation count until the caller
	// releases. Client cancellation alone therefore cannot return an occupied lease.
	go func() {
		defer close(done)
		// Renew before the adapter's two-minute lease duration elapses.
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				// Detached handlers can still collect admitted upstream results after
				// disconnect. Renewal retains the slot until their release callback runs.
				refreshCtx, cancelRefresh := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
				err := cache.RefreshRequest(refreshCtx, key.ID, id)
				cancelRefresh()
				// A lost or uncertain lease ends both ordinary and detached processing
				// through the server signal; the caller still performs final release.
				if err != nil {
					abort()
					return
				}
			}
		}
	}()
	var once sync.Once
	release := func() {
		// Completion and error cleanup can both reach release. Join renewal
		// once before deleting the lease so a later refresh cannot extend it again.
		once.Do(func() {
			defer s.operations.leave()
			close(stop)
			abort()
			<-done
			// Cleanup proceeds after client cancellation under its own timeout.
			// Failed removal leaves expiration to reclaim this active request member.
			releaseCtx, cancelRelease := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancelRelease()
			_ = cache.ReleaseRequest(releaseCtx, key.ID, id)
		})
	}
	return requestCtx, release, 0, nil
}
