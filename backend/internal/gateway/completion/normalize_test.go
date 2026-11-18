package completion

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// TestCacheOverrideProjection starts with aggregate cache writes and then
// selects each TTL bucket. It checks the initialization return value, subsequent
// reclassification, and retained aggregate, ordinary-input, and image-input counts.
func TestCacheOverrideProjection(t *testing.T) {
	value := TokenUsage{InputTokens: 2, ImageInputTokens: 8, CacheCreationInputTokens: 12}
	require.False(t, applyCacheOverride(&value, "5m"))
	require.Equal(t, 12, value.CacheCreation5mTokens)
	require.True(t, applyCacheOverride(&value, "1h"))
	require.Zero(t, value.CacheCreation5mTokens)
	require.Equal(t, 12, value.CacheCreation1hTokens)
	require.Equal(t, 12, value.CacheCreationInputTokens)
	require.Equal(t, 8, value.ImageInputTokens)
	require.Equal(t, 2, value.InputTokens)
}

// TestNormalizeResultUsesDecodedImageDimensions supplies a large input hint
// and smaller decoded output dimensions. It checks that output evidence determines
// the stored billing tier, dimensions, and source classification.
func TestNormalizeResultUsesDecodedImageDimensions(t *testing.T) {
	result := &Result{ImageCount: 1, ImageInputSize: "3840x2160", ImageOutputSizes: []string{"1672x941"}}
	(&Recorder{}).normalizeResult(result, &ProviderSnapshot{OAuthLike: true}, true, nil, "local-request")
	require.Equal(t, pricing.ImageBillingSize2K, result.ImageSize)
	require.Equal(t, "1672x941", result.ImageOutputSize)
	require.Equal(t, pricing.ImageSizeSourceOutput, result.ImageSizeSource)
}

// TestResolveBillingServiceTier compares requested and observed tier names
// across missing, unknown, equivalent, cheaper, and more expensive responses.
// It protects normalized charge selection and the downgrade flag for each fixture.
func TestResolveBillingServiceTier(t *testing.T) {
	tests := []struct {
		name       string // tier scenario label
		requested  string // outbound tier evidence
		observed   string // response tier evidence
		billing    string // expected charge tier
		downgraded bool   // expected downgrade decision
	}{
		{name: "openai priority served as default", requested: "priority", observed: "default", billing: "default", downgraded: true},
		{name: "anthropic fast served as standard", requested: "fast", observed: "standard", billing: "standard", downgraded: true},
		{name: "priority honoured", requested: "priority", observed: "priority", billing: "priority"},
		{name: "ultrafast served as default", requested: "ultrafast", observed: "default", billing: "default", downgraded: true},
		{name: "ultrafast served as priority", requested: "ultrafast", observed: "priority", billing: "priority", downgraded: true},
		{name: "ultrafast honoured", requested: "ultrafast", observed: "ultrafast", billing: "ultrafast"},
		{name: "unexpected ultrafast is never billed", requested: "priority", observed: "ultrafast", billing: "priority"},
		{name: "no declaration keeps request", requested: "priority", observed: "", billing: "priority"},
		{name: "no request no declaration", requested: "", observed: "", billing: ""},
		{name: "response never raises the tier", requested: "", observed: "priority", billing: ""},
		{name: "flex never raised to default", requested: "flex", observed: "default", billing: "flex"},
		{name: "default echoed for untiered request", requested: "", observed: "default", billing: ""},
		{name: "unknown response tier ignored", requested: "priority", observed: "turbo", billing: "priority"},
		{name: "case and whitespace normalised", requested: " Priority ", observed: "DEFAULT", billing: "default", downgraded: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveBillingServiceTier(tt.requested, tt.observed)
			require.Equal(t, tt.billing, got.Billing)
			require.Equal(t, tt.downgraded, got.Downgraded)
		})
	}
}

// TestApplyServiceTierBillingResolutionOnlyRewritesDowngrades exercises generic
// and OAuth-like response evidence on captured results. It checks accepted cheaper
// tiers, retained request pointers or nil, default-echo exemption, and nil-result handling.
func TestApplyServiceTierBillingResolutionOnlyRewritesDowngrades(t *testing.T) {
	t.Run("codex exception only covers OpenAI default", func(t *testing.T) {
		require.True(t, CodexOAuthResponseTierIsNonAuthoritative("default"))
		require.False(t, CodexOAuthResponseTierIsNonAuthoritative("standard"))
		require.False(t, CodexOAuthResponseTierIsNonAuthoritative("flex"))
	})

	t.Run("openai downgrade rewrites tier", func(t *testing.T) {
		requested := "priority"
		result := &Result{ServiceTier: &requested, UpstreamResponseServiceTier: "default"}
		resolution := (&Recorder{}).normalizeResult(result, &ProviderSnapshot{OAuthLike: false}, true, nil, "local-request")
		require.True(t, resolution.Downgraded)
		require.NotNil(t, result.ServiceTier)
		require.Equal(t, "default", *result.ServiceTier)
	})

	t.Run("openai honoured tier keeps pointer", func(t *testing.T) {
		requested := "priority"
		result := &Result{ServiceTier: &requested, UpstreamResponseServiceTier: "priority"}
		require.False(t, (&Recorder{}).normalizeResult(result, &ProviderSnapshot{OAuthLike: false}, true, nil, "local-request").Downgraded)
		require.Same(t, &requested, result.ServiceTier)
	})

	t.Run("openai untiered request stays nil", func(t *testing.T) {
		result := &Result{UpstreamResponseServiceTier: "priority"}
		require.False(t, (&Recorder{}).normalizeResult(result, &ProviderSnapshot{OAuthLike: false}, true, nil, "local-request").Downgraded)
		require.Nil(t, result.ServiceTier)
	})

	for _, providerType := range []string{capability.ProviderTypeOAuth, capability.ProviderTypeSetupToken} {
		t.Run("codex "+providerType+" keeps outbound priority despite default echo", func(t *testing.T) {
			requested := "priority"
			result := &Result{ServiceTier: &requested, UpstreamResponseServiceTier: "default"}
			resolution := (&Recorder{}).normalizeResult(result, &ProviderSnapshot{OAuthLike: true}, true, nil, "local-request")
			require.False(t, resolution.Downgraded)
			require.Equal(t, "priority", resolution.Requested)
			require.Equal(t, "default", resolution.Observed)
			require.Equal(t, "priority", resolution.Billing)
			require.Same(t, &requested, result.ServiceTier)
		})

		t.Run("codex "+providerType+" still accepts an explicit flex downgrade", func(t *testing.T) {
			requested := "priority"
			result := &Result{ServiceTier: &requested, UpstreamResponseServiceTier: "flex"}
			resolution := (&Recorder{}).normalizeResult(result, &ProviderSnapshot{OAuthLike: true}, true, nil, "local-request")
			require.True(t, resolution.Downgraded)
			require.Equal(t, "flex", resolution.Billing)
			require.Equal(t, "flex", *result.ServiceTier)
		})

		t.Run("codex "+providerType+" response never promotes an untiered request", func(t *testing.T) {
			result := &Result{UpstreamResponseServiceTier: "priority"}
			resolution := (&Recorder{}).normalizeResult(result, &ProviderSnapshot{OAuthLike: true}, true, nil, "local-request")
			require.False(t, resolution.Downgraded)
			require.Empty(t, resolution.Billing)
			require.Nil(t, result.ServiceTier)
		})
	}

	t.Run("non-openai oauth still uses the generic response contract", func(t *testing.T) {
		requested := "priority"
		result := &Result{ServiceTier: &requested, UpstreamResponseServiceTier: "default"}
		resolution := (&Recorder{}).normalizeResult(result, &ProviderSnapshot{OAuthLike: false}, true, nil, "local-request")
		require.True(t, resolution.Downgraded)
		require.Equal(t, "default", resolution.Billing)
		require.Equal(t, "default", *result.ServiceTier)
	})

	t.Run("anthropic standard speed rewrites fast", func(t *testing.T) {
		requested := "fast"
		result := &Result{ServiceTier: &requested, UpstreamResponseServiceTier: "standard"}
		require.True(t, (&Recorder{}).normalizeResult(result, nil, false, nil, "local-request").Downgraded)
		require.Equal(t, "standard", *result.ServiceTier)
	})

	t.Run("nil results are ignored", func(t *testing.T) {
		require.False(t, (&Recorder{}).normalizeResult(nil, nil, true, nil, "local-request").Downgraded)
		require.False(t, (&Recorder{}).normalizeResult(nil, nil, false, nil, "local-request").Downgraded)
	})
}
