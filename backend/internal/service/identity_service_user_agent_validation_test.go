package service

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/claude"
	"github.com/stretchr/testify/require"
)

type stubIdentityCache struct {
	fingerprint *Fingerprint
	setCalls    int
	lastSet     *Fingerprint
}

func (s *stubIdentityCache) GetFingerprint(_ context.Context, _ int64) (*Fingerprint, error) {
	if s.fingerprint == nil {
		return nil, nil
	}
	clone := *s.fingerprint
	return &clone, nil
}

func (s *stubIdentityCache) SetFingerprint(_ context.Context, _ int64, fingerprint *Fingerprint) error {
	s.setCalls++
	clone := *fingerprint
	s.lastSet = &clone
	s.fingerprint = &clone
	return nil
}

func (s *stubIdentityCache) GetMaskedSessionID(_ context.Context, _ int64) (string, error) {
	return "", nil
}

func (s *stubIdentityCache) SetMaskedSessionID(_ context.Context, _ int64, _ string) error {
	return nil
}

func fingerprintHeadersWithUA(userAgent string) http.Header {
	headers := http.Header{}
	if userAgent != "" {
		headers.Set("User-Agent", userAgent)
	}
	return headers
}

func TestIsAcceptableFingerprintUserAgent(t *testing.T) {
	cases := []struct {
		name      string
		userAgent string
		want      bool
	}{
		{name: "official_cli", userAgent: "claude-cli/2.1.220 (external, cli)", want: true},
		{name: "official_cli_no_meta", userAgent: "claude-cli/2.1.220", want: true},
		{name: "next_major_still_allowed", userAgent: "claude-cli/4.0.0 (external, cli)", want: true},
		{name: "other_product_valid_form", userAgent: "some-sdk/1.2.3 (node)", want: true},
		{name: "local_build_suffix", userAgent: "claude-cli/999.0.0-local (undefined, cli)", want: false},
		{name: "dev_build_suffix", userAgent: "claude-cli/2.1.220-dev (external, cli)", want: false},
		{name: "build_metadata_suffix", userAgent: "claude-cli/2.1.220+build1 (external, cli)", want: false},
		{name: "sentinel_major", userAgent: "claude-cli/999.0.0 (external, cli)", want: false},
		{name: "overflowing_major", userAgent: "claude-cli/999999999999999999999.0.0 (external, cli)", want: false},
		{name: "empty", userAgent: "", want: false},
		{name: "no_version", userAgent: "claude-cli (external, cli)", want: false},
		{name: "two_segment_version", userAgent: "claude-cli/2.1 (external, cli)", want: false},
		{name: "browser_ua", userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)", want: false},
		{name: "leading_junk", userAgent: "x claude-cli/2.1.220 (external, cli)", want: false},
		{name: "too_long", userAgent: "claude-cli/2.1.220 (" + strings.Repeat("a", 300) + ")", want: false},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.want, isAcceptableFingerprintUserAgent(testCase.userAgent))
		})
	}
}

func TestGetOrCreateFingerprintRejectsMalformedUserAgentOnCreate(t *testing.T) {
	cache := &stubIdentityCache{}
	service := NewIdentityService(cache)

	fingerprint, err := service.GetOrCreateFingerprint(
		context.Background(), 1,
		fingerprintHeadersWithUA("claude-cli/999.0.0-local (undefined, cli)"),
	)

	require.NoError(t, err)
	require.Equal(t, defaultFingerprint.UserAgent, fingerprint.UserAgent)
	require.NotContains(t, cache.lastSet.UserAgent, "999.0.0")
}

func TestGetOrCreateFingerprintRejectsSentinelVersionOnUpgrade(t *testing.T) {
	cache := &stubIdentityCache{fingerprint: &Fingerprint{
		UserAgent: "claude-cli/2.1.22 (external, cli)",
		ClientID:  "cid-1",
		UpdatedAt: time.Now().Unix(),
	}}
	service := NewIdentityService(cache)

	fingerprint, err := service.GetOrCreateFingerprint(
		context.Background(), 1,
		fingerprintHeadersWithUA("claude-cli/999.0.0-local (undefined, cli)"),
	)

	require.NoError(t, err)
	require.Equal(t, "claude-cli/2.1.22 (external, cli)", fingerprint.UserAgent)
	require.Zero(t, cache.setCalls)
}

func TestGetOrCreateFingerprintStillUpgradesOnValidNewerVersion(t *testing.T) {
	cache := &stubIdentityCache{fingerprint: &Fingerprint{
		UserAgent: "claude-cli/2.1.22 (external, cli)",
		ClientID:  "cid-1",
		UpdatedAt: time.Now().Unix(),
	}}
	service := NewIdentityService(cache)

	newUserAgent := "claude-cli/2.1.223 (external, cli)"
	fingerprint, err := service.GetOrCreateFingerprint(context.Background(), 1, fingerprintHeadersWithUA(newUserAgent))

	require.NoError(t, err)
	require.Equal(t, newUserAgent, fingerprint.UserAgent)
	require.Equal(t, 1, cache.setCalls)
}

func TestGetOrCreateFingerprintAcceptsValidUserAgentOnCreate(t *testing.T) {
	cache := &stubIdentityCache{}
	service := NewIdentityService(cache)
	userAgent := "claude-cli/" + claude.CLICurrentVersion + " (external, cli)"

	fingerprint, err := service.GetOrCreateFingerprint(context.Background(), 1, fingerprintHeadersWithUA(userAgent))

	require.NoError(t, err)
	require.Equal(t, userAgent, fingerprint.UserAgent)
	require.NotEmpty(t, fingerprint.ClientID)
	require.Equal(t, 1, cache.setCalls)
}

func TestDefaultFingerprintUserAgentIsAcceptable(t *testing.T) {
	require.True(t, isAcceptableFingerprintUserAgent(defaultFingerprint.UserAgent))
}

func TestGetOrCreateFingerprintHealsPoisonedCacheUsingValidClientUA(t *testing.T) {
	cache := &stubIdentityCache{fingerprint: &Fingerprint{
		UserAgent: "claude-cli/999.0.0-local (undefined, cli)",
		ClientID:  "cid-1",
		UpdatedAt: time.Now().Unix(),
	}}
	service := NewIdentityService(cache)
	realUserAgent := "claude-cli/2.1.22 (external, cli)"

	fingerprint, err := service.GetOrCreateFingerprint(context.Background(), 1, fingerprintHeadersWithUA(realUserAgent))

	require.NoError(t, err)
	require.Equal(t, realUserAgent, fingerprint.UserAgent)
	require.Equal(t, 1, cache.setCalls)
	require.NotContains(t, cache.lastSet.UserAgent, "999.0.0")
	require.Equal(t, "cid-1", fingerprint.ClientID)
}

func TestGetOrCreateFingerprintHealsPoisonedCacheWithoutValidClientUA(t *testing.T) {
	cache := &stubIdentityCache{fingerprint: &Fingerprint{
		UserAgent: "claude-cli/999.0.0-local (undefined, cli)",
		ClientID:  "cid-1",
		UpdatedAt: time.Now().Unix(),
	}}
	service := NewIdentityService(cache)

	fingerprint, err := service.GetOrCreateFingerprint(
		context.Background(), 1,
		fingerprintHeadersWithUA("claude-cli/999.0.0-local (undefined, cli)"),
	)

	require.NoError(t, err)
	require.Equal(t, defaultFingerprint.UserAgent, fingerprint.UserAgent)
	require.Equal(t, 1, cache.setCalls)
}

func TestGetOrCreateFingerprintDoesNotRewriteHealthyCache(t *testing.T) {
	cache := &stubIdentityCache{fingerprint: &Fingerprint{
		UserAgent: "claude-cli/2.1.220 (external, cli)",
		ClientID:  "cid-1",
		UpdatedAt: time.Now().Unix(),
	}}
	service := NewIdentityService(cache)

	fingerprint, err := service.GetOrCreateFingerprint(
		context.Background(), 1,
		fingerprintHeadersWithUA("claude-cli/2.1.22 (external, cli)"),
	)

	require.NoError(t, err)
	require.Equal(t, "claude-cli/2.1.220 (external, cli)", fingerprint.UserAgent)
	require.Zero(t, cache.setCalls)
}

func TestGetOrCreateFingerprintMissingUserAgentKeepsDefault(t *testing.T) {
	cache := &stubIdentityCache{}
	service := NewIdentityService(cache)

	fingerprint, err := service.GetOrCreateFingerprint(context.Background(), 1, http.Header{})

	require.NoError(t, err)
	require.Equal(t, defaultFingerprint.UserAgent, fingerprint.UserAgent)
}
