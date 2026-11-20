package provider

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFieldPolicyExcludesSecretsOtherThanAPIKeyFromSchedulerMetadata inspects
// every registered credential policy. Only api_key may combine secret redaction
// with scheduler metadata inclusion, keeping OAuth, session, password, and
// cloud signing tokens out of slim cache payloads.
func TestFieldPolicyExcludesSecretsOtherThanAPIKeyFromSchedulerMetadata(t *testing.T) {
	policies := CredentialFieldPolicies()
	require.Len(t, credentialFieldPolicyEntries, len(policies), "credential policy entries must not contain duplicate keys")

	for key, policy := range policies {
		if policy.Secret && key != "api_key" {
			require.Falsef(t, policy.IncludeInSchedulerMeta, "secret credential %q must never enter scheduler metadata", key)
		}
		if policy.EphemeralLoginInput {
			require.Truef(t, policy.Secret, "ephemeral login credential %q must also be marked secret", key)
		}
	}

	apiKeyPolicy, ok := LookupCredentialFieldPolicy("api_key")
	require.True(t, ok)
	require.True(t, apiKeyPolicy.Secret)
	require.True(t, apiKeyPolicy.IncludeInSchedulerMeta)
}

// TestFieldPolicyExtraUniquenessAndManagedIsolation verifies unique Extra
// registrations and checks that RestoreManagedConfigurationExtra clones nested
// maps instead of aliasing the current provider record.
func TestFieldPolicyExtraUniquenessAndManagedIsolation(t *testing.T) {
	policies := ExtraFieldPolicies()
	require.Len(t, extraFieldPolicyEntries, len(policies), "extra policy entries must not contain duplicate keys")

	privacyPolicy, ok := LookupExtraFieldPolicy("privacy_mode")
	require.True(t, ok)
	require.False(t, privacyPolicy.ManagedConfiguration)
	require.True(t, privacyPolicy.IncludeInSchedulerMeta)

	current := map[string]any{
		"model_rate_limits": map[string]any{
			"AICredits": map[string]any{"rate_limit_reset_at": "2026-12-01T00:00:00Z"},
		},
		"quota_used": float64(12),
	}
	dst := map[string]any{
		"quota_limit":          float64(50),
		"grok_observed_models": []any{"stale"},
	}
	RestoreManagedConfigurationExtra(dst, current)
	require.Equal(t, float64(12), dst["quota_used"])
	require.NotContains(t, dst, "grok_observed_models")

	// Mutating the restored map must not alter the source record's nested container.
	dstLimits, ok := dst["model_rate_limits"].(map[string]any)
	require.True(t, ok)
	delete(dstLimits, "AICredits")
	srcLimits, ok := current["model_rate_limits"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, srcLimits, "AICredits")
}

// TestFieldPolicyIncludesEffortModelMappingInSchedulerMeta verifies that
// effort_model_mapping and its wire-stripping flag are nonsecret scheduler
// metadata keys retained by FilterSchedulerCredentials.
func TestFieldPolicyIncludesEffortModelMappingInSchedulerMeta(t *testing.T) {
	filtered := FilterSchedulerCredentials(map[string]any{
		"api_key":                       "secret",
		"refresh_token":                 "hidden",
		EffortModelMappingCredentialKey: map[string]any{"claude-opus-5-5": map[string]any{"high": "claude-opus-5-5-high"}},
		EffortModelMappingStripEffortCredentialKey: true,
	})
	require.Contains(t, filtered, EffortModelMappingCredentialKey)
	require.Contains(t, filtered, EffortModelMappingStripEffortCredentialKey)
	require.NotContains(t, filtered, "refresh_token")
}
