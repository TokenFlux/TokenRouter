package httpclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/ipmatch"
)

// public_dial.go resolves public-only destinations and connects to checked IP
// literals. Pre-request validation and connection establishment can observe
// different DNS answers. Resolving at the connection boundary and passing only
// validated literals to the connector prevents a later lookup from selecting a
// private address. The HTTP transport retains the original hostname for TLS
// verification, while explicit proxies retain their own target resolution.
//
// One coordinator starts connection attempts and owns the fallback timer. A
// pending connection gives the next address a staggered start; a failed attempt
// advances immediately. DNS and every attempt share one connection deadline.
// An unbuffered result transfers ownership of the winning socket; cancellation
// closes late successes and stops attempts that have not completed their dial.

const (
	// Public-target budgets bound DNS lookup and stagger pending connection
	// attempts without delaying a replacement after an explicit connection failure.
	publicTargetLookupTimeout = 5 * time.Second
	publicTargetFallbackDelay = 300 * time.Millisecond
)

// dialContextFunc connects a transport to the supplied network address.
type dialContextFunc = func(context.Context, string, string) (net.Conn, error)

// lookupIPFunc resolves hostname addresses within the supplied context budget.
type lookupIPFunc = func(context.Context, string, string) ([]net.IP, error)

// publicDialResult transfers one checked address attempt to the winning receiver.
type publicDialResult struct {
	conn net.Conn // successfully connected socket
	err  error    // failed connection attempt
}

// publicTargetDialContext builds a public-only connector with caller-supplied
// dialing and DNS functions. Both callbacks must honor cancellation and remain
// safe for concurrent calls; production uses the system resolver. Each call
// rejects the entire answer set if any address is nonpublic, then connects only
// to checked literals. DNS and connection attempts share the upstream deadline,
// bounded by any earlier caller deadline. The returned connection belongs to
// the caller, while cancellation closes successful attempts that lose the race.
func publicTargetDialContext(dial dialContextFunc, lookup lookupIPFunc) dialContextFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		// One budget covers resolution and all attempts, so additional addresses
		// cannot extend the connection phase beyond the caller's remaining deadline.
		ctx, cancel := context.WithTimeout(ctx, defaultUpstreamDialTimeout)
		defer cancel()
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		lookupCtx, cancelLookup := context.WithTimeout(ctx, publicTargetLookupTimeout)
		ips, err := resolvePublicIPs(lookupCtx, host, lookup)
		cancelLookup()
		if err != nil {
			return nil, err
		}
		return dialPublicAddresses(ctx, network, port, ips, dial)
	}
}

// resolvePublicIPs returns every address for host, or an error when the lookup
// fails, finds nothing, or returns any nonpublic address. Supply a nonnil lookup
// callback that honors ctx; callers connect only to the returned literal addresses.
func resolvePublicIPs(ctx context.Context, host string, lookup lookupIPFunc) ([]net.IP, error) {
	ips, err := lookup(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("dns resolution failed: %w", err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("dns resolution returned no addresses for %s", host)
	}
	for _, ip := range ips {
		if ipmatch.IsNonPublic(ip) {
			return nil, fmt.Errorf("resolved ip %s is not allowed", ip.String())
		}
	}
	return ips, nil
}

// dialPublicAddresses races a nonempty list of validated literals within ctx.
// The caller cancels ctx when this function returns and supplies a connector
// that honors it. Only this coordinator advances the address index and timer;
// workers transfer results through an unbuffered channel. A failed attempt
// immediately starts another address, while a pending attempt permits a delayed
// alternative. The first success transfers socket ownership to the caller;
// cancellation makes workers close any success that no receiver can accept.
func dialPublicAddresses(ctx context.Context, network, port string, ips []net.IP, dial dialContextFunc) (net.Conn, error) {
	results := make(chan publicDialResult)
	next, active := 0, 0
	startNext := func() {
		address := net.JoinHostPort(ips[next].String(), port)
		next++
		active++
		// Start only admitted attempts; workers never select another address or
		// reset the shared timer, keeping fallback ordering in this coordinator.
		go func() {
			conn, err := dial(ctx, network, address)
			select {
			case results <- publicDialResult{conn: conn, err: err}:
			case <-ctx.Done():
				// No receiver owns a late success after cancellation, so close it
				// here instead of retaining an unused socket in the transport pool.
				if conn != nil {
					_ = conn.Close()
				}
			}
		}()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	startNext()
	timer := time.NewTimer(publicTargetFallbackDelay)
	defer timer.Stop()
	var dialErrors []error
	for active > 0 {
		var fallback <-chan time.Time
		if next < len(ips) {
			fallback = timer.C
		}
		select {
		case <-ctx.Done():
			return nil, errors.Join(append(dialErrors, ctx.Err())...)
		case result := <-results:
			active--
			if result.err == nil {
				return result.conn, nil
			}
			dialErrors = append(dialErrors, result.err)
			if next < len(ips) {
				// A known failure needs no head start; immediately use the next
				// checked address before a short remaining caller budget can expire.
				startNext()
				timer.Reset(publicTargetFallbackDelay)
			}
		case <-fallback:
			// The active attempt has not completed within its head start. Launch
			// another checked address while retaining the same overall connection budget.
			startNext()
			timer.Reset(publicTargetFallbackDelay)
		}
	}
	return nil, errors.Join(dialErrors...)
}
