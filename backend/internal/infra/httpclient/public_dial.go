package httpclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/ipmatch"
)

// public_dial.go keeps public-only outbound requests on addresses checked at
// connection time.
// A check made before a request and the lookup made while dialing are separate
// DNS answers, so a host under an attacker's control can pass the first and
// return a private address for the second. Resolving once in the dialer and
// connecting to those literal addresses closes that gap, and TLS still verifies
// the original hostname because the transport passes it separately.
//
// Any nonpublic answer rejects the whole host, matching ValidateResolvedIP.
// These dialers apply to direct connections only. A proxy needs a private address
// to be reachable for legitimate deployments and resolves the target itself, so
// proxied requests keep only the pre-request check.
//
// Checked connections start at staggered intervals under one overall deadline.
// A successful connection transfers through an unbuffered channel; cancelling
// the remaining workers closes sockets that have not transferred ownership.
// This keeps DNS pinning independent of address reachability without allowing
// failed addresses to multiply the upstream connection timeout.

// publicTargetLookupTimeout bounds one public-target DNS lookup, because the
// dialer's own timeout does not cover a lookup performed before dialing.
const publicTargetLookupTimeout = 5 * time.Second

// lookupTargetIPs resolves addresses using production DNS; serial tests replace
// it while no concurrent request can read this package-level function value.
var lookupTargetIPs = func(ctx context.Context, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

// dialContextFunc matches the connection callback used by HTTP transports.
type dialContextFunc = func(ctx context.Context, network, address string) (net.Conn, error)

// publicDialResult transfers one checked address attempt to the winning receiver.
type publicDialResult struct {
	conn net.Conn // successfully connected socket
	err  error    // failed connection attempt
}

// resolvePublicIPs returns every address for host, or an error when the lookup
// fails, finds nothing, or returns any nonpublic address. The context bounds the
// lookup; the returned addresses are the only ones a caller may connect to.
func resolvePublicIPs(ctx context.Context, host string) ([]net.IP, error) {
	ips, err := lookupTargetIPs(ctx, host)
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

// publicTargetDialContext wraps dial so each new connection resolves the target
// once, rejects nonpublic answers, and connects only to the checked literals.
// Checked addresses race with staggered starts so an unreachable address cannot
// delay a healthy alternative until its individual connection timeout expires.
// DNS and every connection attempt share the upstream connection budget. Losing
// attempts are cancelled, and connections completed after a winner are closed.
// The caller supplies a non-nil dial and uses this for direct routes only.
func publicTargetDialContext(dial dialContextFunc) dialContextFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		ctx, cancelDial := context.WithTimeout(ctx, defaultUpstreamDialTimeout)
		defer cancelDial()
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		lookupCtx, cancel := context.WithTimeout(ctx, publicTargetLookupTimeout)
		defer cancel()
		ips, err := resolvePublicIPs(lookupCtx, host)
		if err != nil {
			return nil, err
		}
		// Stagger checked literals by the standard Happy Eyeballs fallback delay
		// so a blocked first address cannot consume the entire connection budget.
		results := make(chan publicDialResult)
		for index, ip := range ips {
			go func() {
				timer := time.NewTimer(time.Duration(index) * 300 * time.Millisecond)
				defer timer.Stop()
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
				}
				conn, err := dial(ctx, network, net.JoinHostPort(ip.String(), port))
				// An unbuffered handoff transfers ownership only to a live receiver;
				// cancellation closes late successes instead of leaking unused sockets.
				select {
				case results <- publicDialResult{conn: conn, err: err}:
				case <-ctx.Done():
					if conn != nil {
						_ = conn.Close()
					}
				}
			}()
		}
		var dialErrors []error
		for range ips {
			select {
			case <-ctx.Done():
				return nil, errors.Join(append(dialErrors, ctx.Err())...)
			case result := <-results:
				if result.err == nil {
					return result.conn, nil
				}
				dialErrors = append(dialErrors, result.err)
			}
		}
		return nil, errors.Join(dialErrors...)
	}
}
