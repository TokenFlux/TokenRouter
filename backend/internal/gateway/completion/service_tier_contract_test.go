package completion

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestResolveBillingServiceTier(t *testing.T) {
	tests := []struct {
		name       string
		requested  string
		observed   string
		billing    string
		downgraded bool
	}{
		{name: "openai priority served as default", requested: "priority", observed: "default", billing: "default", downgraded: true},
		{name: "anthropic fast served as standard", requested: "fast", observed: "standard", billing: "standard", downgraded: true},
		{name: "priority honoured", requested: "priority", observed: "priority", billing: "priority"},
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

func TestApplyServiceTierBillingResolutionOnlyRewritesDowngrades(t *testing.T) {
	t.Run("codex exception only covers OpenAI default", func(t *testing.T) {
		require.True(t, CodexOAuthResponseTierIsNonAuthoritative("default"))
		require.False(t, CodexOAuthResponseTierIsNonAuthoritative("standard"))
		require.False(t, CodexOAuthResponseTierIsNonAuthoritative("flex"))
	})

	t.Run("openai downgrade rewrites tier", func(t *testing.T) {
		requested := "priority"
		result := &Result{ServiceTier: &requested, UpstreamResponseServiceTier: "default"}
		resolution := (&Recorder{}).normalizeResult(result, &AccountSnapshot{OAuthLike: false}, true, nil)
		require.True(t, resolution.Downgraded)
		require.NotNil(t, result.ServiceTier)
		require.Equal(t, "default", *result.ServiceTier)
	})

	t.Run("openai honoured tier keeps pointer", func(t *testing.T) {
		requested := "priority"
		result := &Result{ServiceTier: &requested, UpstreamResponseServiceTier: "priority"}
		require.False(t, (&Recorder{}).normalizeResult(result, &AccountSnapshot{OAuthLike: false}, true, nil).Downgraded)
		require.Same(t, &requested, result.ServiceTier)
	})

	t.Run("openai untiered request stays nil", func(t *testing.T) {
		result := &Result{UpstreamResponseServiceTier: "priority"}
		require.False(t, (&Recorder{}).normalizeResult(result, &AccountSnapshot{OAuthLike: false}, true, nil).Downgraded)
		require.Nil(t, result.ServiceTier)
	})

	for _, accountType := range []string{capability.AccountTypeOAuth, capability.AccountTypeSetupToken} {
		t.Run("codex "+accountType+" keeps outbound priority despite default echo", func(t *testing.T) {
			requested := "priority"
			result := &Result{ServiceTier: &requested, UpstreamResponseServiceTier: "default"}
			resolution := (&Recorder{}).normalizeResult(result, &AccountSnapshot{OAuthLike: true}, true, nil)
			require.False(t, resolution.Downgraded)
			require.Equal(t, "priority", resolution.Requested)
			require.Equal(t, "default", resolution.Observed)
			require.Equal(t, "priority", resolution.Billing)
			require.Same(t, &requested, result.ServiceTier)
		})

		t.Run("codex "+accountType+" still accepts an explicit flex downgrade", func(t *testing.T) {
			requested := "priority"
			result := &Result{ServiceTier: &requested, UpstreamResponseServiceTier: "flex"}
			resolution := (&Recorder{}).normalizeResult(result, &AccountSnapshot{OAuthLike: true}, true, nil)
			require.True(t, resolution.Downgraded)
			require.Equal(t, "flex", resolution.Billing)
			require.Equal(t, "flex", *result.ServiceTier)
		})

		t.Run("codex "+accountType+" response never promotes an untiered request", func(t *testing.T) {
			result := &Result{UpstreamResponseServiceTier: "priority"}
			resolution := (&Recorder{}).normalizeResult(result, &AccountSnapshot{OAuthLike: true}, true, nil)
			require.False(t, resolution.Downgraded)
			require.Empty(t, resolution.Billing)
			require.Nil(t, result.ServiceTier)
		})
	}

	t.Run("non-openai oauth still uses the generic response contract", func(t *testing.T) {
		requested := "priority"
		result := &Result{ServiceTier: &requested, UpstreamResponseServiceTier: "default"}
		resolution := (&Recorder{}).normalizeResult(result, &AccountSnapshot{OAuthLike: false}, true, nil)
		require.True(t, resolution.Downgraded)
		require.Equal(t, "default", resolution.Billing)
		require.Equal(t, "default", *result.ServiceTier)
	})

	t.Run("anthropic standard speed rewrites fast", func(t *testing.T) {
		requested := "fast"
		result := &Result{ServiceTier: &requested, UpstreamResponseServiceTier: "standard"}
		require.True(t, (&Recorder{}).normalizeResult(result, nil, false, nil).Downgraded)
		require.Equal(t, "standard", *result.ServiceTier)
	})

	t.Run("nil results are ignored", func(t *testing.T) {
		require.False(t, (&Recorder{}).normalizeResult(nil, nil, true, nil).Downgraded)
		require.False(t, (&Recorder{}).normalizeResult(nil, nil, false, nil).Downgraded)
	})
}
