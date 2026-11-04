package httpclient

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

// public_dial_test.go checks address validation, fallback scheduling, and socket
// ownership with per-test DNS and connection callbacks. Virtual time exercises
// deadline and cancellation behavior without external DNS or shared resolver
// mutations, while in-memory connections expose ownership and cleanup outcomes.

// fixedTargetLookup returns an immutable resolver fixture for valid IP literals.
// Each caller owns its answer slice; the returned callback performs no DNS and
// may be passed to concurrent dialers without changing package-level state.
func fixedTargetLookup(t *testing.T, answers ...string) lookupIPFunc {
	t.Helper()
	ips := make([]net.IP, 0, len(answers))
	for _, answer := range answers {
		ip := net.ParseIP(answer)
		require.NotNil(t, ip)
		ips = append(ips, ip)
	}
	return func(context.Context, string, string) ([]net.IP, error) {
		return ips, nil
	}
}

// TestPublicTargetDialContextConnectsToCheckedLiteral resolves a host to a public
// address. The connector must receive that literal and the original port, so the
// connection cannot depend on a second DNS answer.
func TestPublicTargetDialContextConnectsToCheckedLiteral(t *testing.T) {
	lookup := fixedTargetLookup(t, "93.184.216.34")
	var dialed string
	dial := publicTargetDialContext(func(_ context.Context, _, address string) (net.Conn, error) {
		dialed = address
		client, server := net.Pipe()
		t.Cleanup(func() { _ = server.Close() })
		return client, nil
	}, lookup)

	conn, err := dial(context.Background(), "tcp", "images.example.com:443")

	require.NoError(t, err)
	require.NoError(t, conn.Close())
	require.Equal(t, "93.184.216.34:443", dialed)
}

// TestPublicTargetDialContextRejectsAnyNonpublicAnswer returns answers containing
// a private or reserved address. The connector must not run, even when another
// answer in the same lookup is public.
func TestPublicTargetDialContextRejectsAnyNonpublicAnswer(t *testing.T) {
	for _, answers := range [][]string{
		{"127.0.0.1"},
		{"169.254.169.254"},
		{"93.184.216.34", "10.0.0.1"},
		{"100.64.0.1"},
	} {
		lookup := fixedTargetLookup(t, answers...)
		dial := publicTargetDialContext(func(context.Context, string, string) (net.Conn, error) {
			t.Fatal("connector must not run for a rejected host")
			return nil, nil
		}, lookup)

		_, err := dial(context.Background(), "tcp", "rebind.example.com:443")

		require.ErrorContains(t, err, "is not allowed")
	}
}

// TestPublicTargetDialContextTriesLaterAddresses fails the first checked address.
// The connector must then try the next literal, and an all-failed lookup must
// report the connection errors rather than a false success.
func TestPublicTargetDialContextTriesLaterAddresses(t *testing.T) {
	lookup := fixedTargetLookup(t, "93.184.216.34", "93.184.216.35")
	var attempts []string
	var attemptsMu sync.Mutex
	dial := publicTargetDialContext(func(_ context.Context, _, address string) (net.Conn, error) {
		attemptsMu.Lock()
		attempts = append(attempts, address)
		attemptsMu.Unlock()
		return nil, errors.New("refused " + address)
	}, lookup)

	_, err := dial(context.Background(), "tcp", "images.example.com:443")

	require.ErrorContains(t, err, "refused 93.184.216.35:443")
	attemptsMu.Lock()
	defer attemptsMu.Unlock()
	require.ElementsMatch(t, []string{"93.184.216.34:443", "93.184.216.35:443"}, attempts)
}

// TestPublicTargetDialContextSkipsBlockedAddress stalls the first checked literal.
// A healthy alternative must connect before that attempt finishes, cancelling the
// blocked connection rather than waiting for its individual network timeout.
func TestPublicTargetDialContextSkipsBlockedAddress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lookup := fixedTargetLookup(t, "93.184.216.34", "93.184.216.35")
		stopped := make(chan struct{})
		peer, server := net.Pipe()
		t.Cleanup(func() { _ = server.Close() })
		dial := publicTargetDialContext(func(ctx context.Context, _, address string) (net.Conn, error) {
			if address == "93.184.216.34:443" {
				<-ctx.Done()
				close(stopped)
				return nil, ctx.Err()
			}
			return peer, nil
		}, lookup)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		conn, err := dial(ctx, "tcp", "images.example.com:443")
		require.NoError(t, err)
		require.NoError(t, conn.Close())
		select {
		case <-stopped:
		case <-ctx.Done():
			t.Fatal("losing connection was not cancelled")
		}
	})
}

// TestPublicTargetDialContextPreservesDeadline cancels a request before connecting.
// The shared budget must honor that earlier deadline and never wait for the
// wrapper's longer timeout while a connection attempt is blocked.
func TestPublicTargetDialContextPreservesDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lookup := fixedTargetLookup(t, "93.184.216.34")
		dial := publicTargetDialContext(func(ctx context.Context, _, _ string) (net.Conn, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}, lookup)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		_, err := dial(ctx, "tcp", "images.example.com:443")
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})
}

// TestPublicTargetDialImmediateFailureStartsFallback gives the first address an
// immediate refusal and the second a successful connection. A shorter caller
// budget than the fallback delay must still reach the healthy alternative.
func TestPublicTargetDialImmediateFailureStartsFallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lookup := fixedTargetLookup(t, "93.184.216.34", "93.184.216.35")
		var fallbackCalls atomic.Int64
		dial := publicTargetDialContext(func(_ context.Context, _, address string) (net.Conn, error) {
			if address == "93.184.216.34:443" {
				return nil, errors.New("connection refused")
			}
			fallbackCalls.Add(1)
			client, server := net.Pipe()
			// The fixture owns the remote endpoint, which must close even if an
			// assertion fails before the returned client connection can be inspected.
			t.Cleanup(func() { _ = server.Close() })
			return client, nil
		}, lookup)
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		defer cancel()
		conn, err := dial(ctx, "tcp", "images.example.com:443")
		require.NoError(t, err)
		require.NoError(t, conn.Close())
		require.Equal(t, int64(1), fallbackCalls.Load())
	})
}

// TestPublicTargetDialClosesLateSuccess makes the first attempt succeed after
// cancellation while another attempt wins. The unused connection must close,
// leaving ownership of only the returned socket with the caller.
func TestPublicTargetDialClosesLateSuccess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		late, latePeer := net.Pipe()
		winner, winnerPeer := net.Pipe()
		// Both peers are fixture-owned; closing every endpoint also prevents a
		// failed ownership assertion from leaving blocked in-memory connection readers.
		t.Cleanup(func() {
			_ = late.Close()
			_ = latePeer.Close()
			_ = winner.Close()
			_ = winnerPeer.Close()
		})
		dial := publicTargetDialContext(func(ctx context.Context, _, address string) (net.Conn, error) {
			if address == "93.184.216.34:443" {
				<-ctx.Done()
				return late, nil
			}
			return winner, nil
		}, fixedTargetLookup(t, "93.184.216.34", "93.184.216.35"))
		conn, err := dial(t.Context(), "tcp", "images.example.com:443")
		require.NoError(t, err)
		require.Same(t, winner, conn)
		// Wait for cancelled workers to release their sockets before observing
		// the peer; virtual time makes this independent of scheduler timing.
		synctest.Wait()
		_, err = latePeer.Read(make([]byte, 1))
		require.ErrorIs(t, err, io.EOF)
	})
}

// TestResolvePublicIPsRejectsEmptyAndFailedLookups supplies unusable DNS results.
// Both cases must report errors before a caller can start connecting to any
// address, preserving fail-closed behavior for incomplete resolver responses.
func TestResolvePublicIPsRejectsEmptyAndFailedLookups(t *testing.T) {
	_, err := resolvePublicIPs(t.Context(), "empty.example", fixedTargetLookup(t))
	require.ErrorContains(t, err, "no addresses")
	failure := errors.New("resolver unavailable")
	_, err = resolvePublicIPs(t.Context(), "failed.example", func(context.Context, string, string) ([]net.IP, error) {
		return nil, failure
	})
	require.ErrorIs(t, err, failure)
}
