package httpclient

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// pool_test.go checks shared-client validation, cache expiry, and direct
// connection enforcement with controlled validators and local servers. These tests run
// serially because the validator is shared; validation failure is checked
// before any request reaches the fake base transport.

// roundTripFunc adapts a test-owned callback to the HTTP transport interface.
type roundTripFunc func(*http.Request) (*http.Response, error)

// RoundTrip delegates the fixture request to its supplied function and
// returns that function's response and error. Each test owns the callback and
// any captured counters or response body.
func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// TestValidatedTransport_CacheHostValidation sends two same-host requests at
// a fixed clock value through a fake base transport. It requires one
// validation call and two base calls, protecting successful-host cache reuse.
func TestValidatedTransport_CacheHostValidation(t *testing.T) {
	// Restore the shared validator after this serial test so later fixtures
	// and production-default cases do not inherit the injected DNS decision.
	originalValidate := validateResolvedIP
	defer func() {
		validateResolvedIP = originalValidate
	}()

	var validateCalls int32
	validateResolvedIP = func(host string) error {
		atomic.AddInt32(&validateCalls, 1)
		require.Equal(t, "api.openai.com", host)
		return nil
	}

	var baseCalls int32
	base := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		atomic.AddInt32(&baseCalls, 1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     make(http.Header),
		}, nil
	})

	now := time.Unix(1730000000, 0)
	transport := newValidatedTransport(base)
	transport.now = func() time.Time {
		return now
	}

	req, err := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/responses", nil)
	require.NoError(t, err)

	_, err = transport.RoundTrip(req)
	require.NoError(t, err)
	_, err = transport.RoundTrip(req)
	require.NoError(t, err)

	require.Equal(t, int32(1), atomic.LoadInt32(&validateCalls))
	require.Equal(t, int32(2), atomic.LoadInt32(&baseCalls))
}

// TestValidatedTransport_ExpiredCacheTriggersRevalidation advances its
// injected clock beyond the stored validation lifetime. It requires a second
// validator call on the next same-host request, protecting lazy expiry
// revalidation.
func TestValidatedTransport_ExpiredCacheTriggersRevalidation(t *testing.T) {
	// This serial fixture owns the package validator until its deferred restore.
	// The injected clock makes expiry independent of wall-clock scheduling.
	originalValidate := validateResolvedIP
	defer func() {
		validateResolvedIP = originalValidate
	}()

	var validateCalls int32
	validateResolvedIP = func(_ string) error {
		atomic.AddInt32(&validateCalls, 1)
		return nil
	}

	base := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     make(http.Header),
		}, nil
	})

	now := time.Unix(1730001000, 0)
	transport := newValidatedTransport(base)
	transport.now = func() time.Time {
		return now
	}

	req, err := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/responses", nil)
	require.NoError(t, err)

	_, err = transport.RoundTrip(req)
	require.NoError(t, err)

	now = now.Add(validatedHostTTL + time.Second)
	_, err = transport.RoundTrip(req)
	require.NoError(t, err)

	require.Equal(t, int32(2), atomic.LoadInt32(&validateCalls))
}

// TestValidatedTransport_ValidationErrorStopsRoundTrip injects a validator
// failure for an outbound host. It requires the identical error and zero base
// transport calls, protecting rejection before the request is sent.
func TestValidatedTransport_ValidationErrorStopsRoundTrip(t *testing.T) {
	// Hold the injected package validator for this serial failure case and
	// restore it before later tests can perform their own host checks.
	originalValidate := validateResolvedIP
	defer func() {
		validateResolvedIP = originalValidate
	}()

	expectedErr := errors.New("dns rebinding rejected")
	validateResolvedIP = func(_ string) error {
		return expectedErr
	}

	var baseCalls int32
	base := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		atomic.AddInt32(&baseCalls, 1)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})

	transport := newValidatedTransport(base)
	req, err := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/responses", nil)
	require.NoError(t, err)

	_, err = transport.RoundTrip(req)
	require.ErrorIs(t, err, expectedErr)
	require.Equal(t, int32(0), atomic.LoadInt32(&baseCalls))
}

// TestGetClientPinsDirectValidatedDial reproduces a host passing the pre-request
// check and then resolving to a loopback server when dialing. The request must
// fail at the dialer without reaching that server.
func TestGetClientPinsDirectValidatedDial(t *testing.T) {
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits.Add(1)
	}))
	t.Cleanup(server.Close)
	originalValidate := validateResolvedIP
	t.Cleanup(func() { validateResolvedIP = originalValidate })
	validateResolvedIP = func(string) error { return nil }
	client, err := GetClient(Options{ValidateResolvedIP: true, Timeout: 7 * time.Second})
	require.NoError(t, err)
	parsed, err := url.Parse(server.URL)
	require.NoError(t, err)

	response, err := client.Get("http://localhost:" + parsed.Port())
	if response != nil {
		_ = response.Body.Close()
	}

	require.ErrorContains(t, err, "is not allowed")
	require.Zero(t, hits.Load())
}
