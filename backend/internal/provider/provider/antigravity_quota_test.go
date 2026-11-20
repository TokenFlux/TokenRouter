package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

// Antigravity quota display depends on bounded model responses, forbidden-error
// classification and cache identity. These cases exercise antigravity_quota.go
// options and upstream parsers, then change access credentials under one provider
// ID to check that cached usage does not cross identities.

// agUsageIdentityTransport counts quota exchanges and returns
// endpoint-specific fixture JSON.
type agUsageIdentityTransport struct {
	calls atomic.Int32 /* concurrent-safe request count */
}

// TestFetchQuotaUsesConfiguredModelsListBodyLimit serves a models response beyond
// the configured eight-byte allowance. It checks that the bound quota options
// propagate the size error from the upstream reader.
func TestFetchQuotaUsesConfiguredModelsListBodyLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":{"model-a":{}}}`))
	}))
	defer server.Close()

	oldBaseURLs := append([]string(nil), antigravity.BaseURLs...)
	oldAvailability := antigravity.DefaultURLAvailability
	// Restore both process-wide endpoint selectors after the local server case;
	// subsequent quota tests depend on their original URL ordering and
	// availability state.
	t.Cleanup(func() {
		antigravity.BaseURLs = oldBaseURLs
		antigravity.DefaultURLAvailability = oldAvailability
	})
	antigravity.BaseURLs = []string{server.URL}
	antigravity.DefaultURLAvailability = antigravity.NewURLAvailability(time.Minute)

	fetcher := &providercore.AntigravityQuota{Options: AntigravityQuotaOptions(8, nil)}
	_, err := fetcher.FetchQuota(context.Background(), &providercore.Record{
		Platform: capability.PlatformAntigravity,
		Type:     capability.ProviderTypeOAuth,
		Credentials: map[string]any{
			"access_token": "token",
			"project_id":   "project",
		},
	}, "")
	require.ErrorContains(t, err, "response exceeds 8 bytes")
}

// TestFetchQuota_ForbiddenReturnsIsForbidden constructs the typed forbidden error
// used by quota parsing. It checks errors.As, status and body retention together
// with a status-bearing diagnostic string.
func TestFetchQuota_ForbiddenReturnsIsForbidden(t *testing.T) {
	// Check the typed 403 error that quota queries use to classify forbidden
	// responses.
	forbiddenErr := &antigravity.ForbiddenError{
		StatusCode: 403,
		Body:       "Access denied",
	}

	var target *antigravity.ForbiddenError
	require.True(t, errors.As(forbiddenErr, &target))
	require.Equal(t, 403, target.StatusCode)
	require.Equal(t, "Access denied", target.Body)
	require.Contains(t, forbiddenErr.Error(), "403")
}

// TestClassifyForbiddenType supplies validation, violation and generic forbidden
// texts to the platform classifier. It checks the display category returned
// for structured hints, plain messages and an empty body.
func TestClassifyForbiddenType(t *testing.T) {
	tests := []struct {
		name     string // subtest display label
		body     string // upstream forbidden-response text
		expected string // expected parsed display value
	}{
		{
			name:     "VALIDATION_REQUIRED keyword",
			body:     `{"error":{"message":"VALIDATION_REQUIRED"}}`,
			expected: "validation",
		},
		{
			name:     "verify your account",
			body:     `Please verify your account to continue`,
			expected: "validation",
		},
		{
			name:     "contains validation_url field",
			body:     `{"error":{"details":[{"metadata":{"validation_url":"https://..."}}]}}`,
			expected: "validation",
		},
		{
			name:     "terms of service violation",
			body:     `Your account has been suspended for Terms of Service violation`,
			expected: "violation",
		},
		{
			name:     "violation keyword",
			body:     `Provider suspended due to policy violation`,
			expected: "violation",
		},
		{
			name:     "generic 403",
			body:     `Access denied`,
			expected: "forbidden",
		},
		{
			name:     "empty body",
			body:     "",
			expected: "forbidden",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := antigravity.ClassifyForbiddenType(tt.body)
			require.Equal(t, tt.expected, got)
		})
	}
}

// TestExtractValidationURL compares structured verification and appeal links
// with plain error text. It checks structured precedence, keyword-based URL
// extraction, escaped ampersands, and empty results when verification evidence
// is absent.
func TestExtractValidationURL(t *testing.T) {
	tests := []struct {
		name     string // subtest display label
		body     string // upstream forbidden-response text
		expected string // expected parsed display value
	}{
		{
			name:     "structured validation_url",
			body:     `{"error":{"details":[{"metadata":{"validation_url":"https://accounts.google.com/verify?token=abc"}}]}}`,
			expected: "https://accounts.google.com/verify?token=abc",
		},
		{
			name:     "structured appeal_url",
			body:     `{"error":{"details":[{"metadata":{"appeal_url":"https://support.google.com/appeal/123"}}]}}`,
			expected: "https://support.google.com/appeal/123",
		},
		{
			name:     "validation_url takes priority over appeal_url",
			body:     `{"error":{"details":[{"metadata":{"validation_url":"https://v.com","appeal_url":"https://a.com"}}]}}`,
			expected: "https://v.com",
		},
		{
			name:     "fallback regex with verify keyword",
			body:     `Please verify your account at https://accounts.google.com/verify`,
			expected: "https://accounts.google.com/verify",
		},
		{
			name:     "no URL in generic forbidden",
			body:     `Access denied`,
			expected: "",
		},
		{
			name:     "empty body",
			body:     "",
			expected: "",
		},
		{
			name:     "URL present but no validation keywords",
			body:     `Error at https://example.com/something`,
			expected: "",
		},
		{
			name:     "unicode escaped ampersand",
			body:     `validation required: https://accounts.google.com/verify?a=1\u0026b=2`,
			expected: "https://accounts.google.com/verify?a=1&b=2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := antigravity.ExtractValidationURL(tt.body)
			require.Equal(t, tt.expected, got)
		})
	}
}

// TestAntigravityUsageCacheDoesNotCrossCredentialIdentity changes an access token
// while retaining the provider ID. It checks that the second usage lookup repeats
// all three platform queries (models, tier, and quota summary) instead of using
// the first identity's cached result.
func TestAntigravityUsageCacheDoesNotCrossCredentialIdentity(t *testing.T) {
	transport := &agUsageIdentityTransport{}
	old := http.DefaultTransport
	http.DefaultTransport = transport
	// The platform client uses the process-wide default transport. Restore it after
	// both credential identities have been observed so later clients use their
	// own transport.
	t.Cleanup(func() { http.DefaultTransport = old })
	quota := &providercore.AntigravityQuota{Options: AntigravityQuotaOptions(8*1024*1024, nil)}
	svc := providercore.NewOAuthUsageService(nil, providercore.NewOAuthUsageCache(), nil, providercore.OAuthUsageOptions{Antigravity: providercore.AntigravityUsageOptions{
		CanFetch: quota.CanFetch,
		Fetch: func(ctx context.Context, value *providercore.Record) (*providercore.UsageInfo, error) {
			result, err := quota.FetchQuota(ctx, value, quota.GetProxyURL(ctx, value))
			if result == nil {
				return nil, err
			}
			return result.UsageInfo, err
		},
		Degrade: AntigravityDegradedUsage, Enrich: EnrichUsageWithProviderError,
	}})
	a := &providercore.Record{ID: 885, Platform: capability.PlatformAntigravity, Type: capability.ProviderTypeOAuth, Status: providercore.StatusActive, Credentials: map[string]any{"access_token": "first", "project_id": "fixture"}}
	_, err := svc.GetAntigravityUsage(context.Background(), a)
	require.NoError(t, err)
	require.Equal(t, int32(3), transport.calls.Load())
	a.Credentials = map[string]any{"access_token": "second", "project_id": "fixture"}
	_, err = svc.GetAntigravityUsage(context.Background(), a)
	require.NoError(t, err)
	require.Equal(t, int32(6), transport.calls.Load())
}

// RoundTrip counts a request and returns tier, quota-summary, or model JSON for
// its endpoint. The returned response retains the request and has a caller-owned
// body without transport errors or additional quota headers.
func (t *agUsageIdentityTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.calls.Add(1)
	body := `{"models":{}}`
	switch {
	case strings.Contains(r.URL.Path, "loadCodeAssist"):
		body = `{"currentTier":{"id":"FREE"}}`
	case strings.Contains(r.URL.Path, "retrieveUserQuotaSummary"):
		body = `{"groups":[]}`
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}
