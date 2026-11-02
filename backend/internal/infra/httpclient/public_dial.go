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
// Addresses are tried in lookup order until one connects, and all failures are
// joined. The caller supplies a non-nil dial and uses this for direct routes only.
func publicTargetDialContext(dial dialContextFunc) dialContextFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
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
		var dialErrors []error
		for _, ip := range ips {
			conn, err := dial(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			dialErrors = append(dialErrors, err)
		}
		return nil, errors.Join(dialErrors...)
	}
}
