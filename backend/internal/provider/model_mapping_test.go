package provider

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestModelMappingDefaultsAreLazyAndIsolated supplies explicit Antigravity rules
// before clearing credentials. It checks that defaults are called on demand and
// mutating a returned default map leaves subsequent reads unchanged.
func TestModelMappingDefaultsAreLazyAndIsolated(t *testing.T) {
	calls := 0
	defaults := map[string]string{"alias": "default-model"}
	options := ModelMappingDefaults{Antigravity: func() map[string]string { calls++; return defaults }}
	r := &Record{Platform: PlatformAntigravity, Credentials: map[string]any{"model_mapping": map[string]any{"explicit": "target"}}}
	value := ResolveModelMapping(r, options)
	require.Zero(t, calls)
	require.Equal(t, "target", value["explicit"])
	r.Credentials = nil
	value = ResolveModelMapping(r, options)
	require.Equal(t, 1, calls)
	value["alias"] = "changed"
	require.Equal(t, "default-model", defaults["alias"])
	require.Equal(t, "default-model", ResolveModelMapping(r, options)["alias"])
}

// TestResolveMappedModelPreservesMatchOrder combines whitespace-bearing requests
// with exact keys, wildcards, and a mapping chain. It checks raw-match precedence,
// trimmed retry, unchanged misses, and the returned one-hop match flag.
func TestResolveMappedModelPreservesMatchOrder(t *testing.T) {
	cases := []struct {
		name      string            // matching scenario display name
		mapping   map[string]string // configured source target pairs
		requested string            // raw supplied request spelling
		want      string            // expected returned model spelling
		matched   bool              // expected mapping match flag
	}{
		{name: "原始精确值优先", mapping: map[string]string{" model ": "raw", "model": "trimmed", "*": "wildcard"}, requested: " model ", want: "raw", matched: true},
		{name: "原始通配符优先", mapping: map[string]string{"model": "trimmed", "*": "wildcard"}, requested: " model ", want: "wildcard", matched: true},
		{name: "去空白后匹配", mapping: map[string]string{"model": "trimmed"}, requested: " model ", want: "trimmed", matched: true},
		{name: "未命中保留输入", mapping: map[string]string{"other": "target"}, requested: " model ", want: " model "},
		{name: "映射执行一次", mapping: map[string]string{"first": "second", "second": "third"}, requested: "first", want: "second", matched: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model, matched := ResolveMappedModel(tc.mapping, tc.requested)
			require.Equal(t, tc.want, model)
			require.Equal(t, tc.matched, matched)
		})
	}
}

// TestExplicitAntigravityMappingIsNotAugmented supplies a custom target and an
// identity mapping with no default factory. It checks the exact copied map,
// source isolation, and unchanged resolution through a nil mapping.
func TestExplicitAntigravityMappingIsNotAugmented(t *testing.T) {
	raw := map[string]any{"gemini-pro-agent": "custom", "gemini-3.1-pro-high": "gemini-3.1-pro-high"}
	value := &Record{Platform: PlatformAntigravity, Credentials: map[string]any{"model_mapping": raw}}
	defaults := ModelMappingDefaults{}
	got := ResolveModelMapping(value, defaults)
	require.Equal(t, map[string]string{"gemini-pro-agent": "custom", "gemini-3.1-pro-high": "gemini-3.1-pro-high"}, got)
	got["gemini-pro-agent"] = "changed"
	require.Equal(t, "custom", raw["gemini-pro-agent"])
	model, matched := ResolveMappedModel(nil, "gemini-3.1-pro-preview-customtools")
	require.False(t, matched)
	require.Equal(t, "gemini-3.1-pro-preview-customtools", model)
}

// TestResolveMappedModelForEffort exercises exact effort rules, trailing
// wildcards, case and separator normalization, fallback to ordinary mappings,
// and single-hop isolation. Effort-specific matches must take precedence over
// ordinary mappings without remapping targets.
func TestResolveMappedModelForEffort(t *testing.T) {
	base := map[string]string{
		"claude-opus-5-5":      "claude-opus-5-5-medium",
		"claude-opus-5-5-high": "must-not-remap",
		"claude-*":             "claude-default",
	}
	effortMap := EffortModelMapping{
		"claude-opus-5-5": {
			"low":   "claude-opus-5-5-low",
			"high":  "claude-opus-5-5-high",
			"xhigh": "claude-opus-5-5-xhigh",
		},
		"claude-*": {
			"high": "claude-wildcard-high",
		},
		"claude-sonnet-*": {
			"high": "claude-sonnet-high",
		},
	}

	cases := []struct {
		name          string // scenario description
		model         string // requested model spelling
		effort        string // raw request effort
		wantModel     string // expected resolved model
		wantMatched   bool   // expected overall match flag
		wantEffortHit bool   // expected effort-rule match flag
	}{
		{name: "exact model and effort hit", model: "claude-opus-5-5", effort: "high", wantModel: "claude-opus-5-5-high", wantMatched: true, wantEffortHit: true},
		{name: "effort normalization folds case and hyphens", model: "claude-opus-5-5", effort: "X-High", wantModel: "claude-opus-5-5-xhigh", wantMatched: true, wantEffortHit: true},
		{name: "longest wildcard wins for effort", model: "claude-sonnet-5", effort: "HIGH", wantModel: "claude-sonnet-high", wantMatched: true, wantEffortHit: true},
		{name: "unmatched effort falls back to ordinary mapping", model: "claude-opus-5-5", effort: "max", wantModel: "claude-opus-5-5-medium", wantMatched: true, wantEffortHit: false},
		{name: "empty effort falls back to ordinary mapping", model: "claude-opus-5-5", effort: "", wantModel: "claude-opus-5-5-medium", wantMatched: true, wantEffortHit: false},
		{name: "single hop prevents remapping effort target", model: "claude-opus-5-5", effort: "high", wantModel: "claude-opus-5-5-high", wantMatched: true, wantEffortHit: true},
		{name: "miss retains original model", model: "gpt-5", effort: "high", wantModel: "gpt-5", wantMatched: false, wantEffortHit: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, matched, effortMatched := ResolveMappedModelForEffort(base, effortMap, tc.model, tc.effort)
			require.Equal(t, tc.wantModel, got)
			require.Equal(t, tc.wantMatched, matched)
			require.Equal(t, tc.wantEffortHit, effortMatched)
		})
	}
	require.Equal(t, []string{"low", "high", "xhigh"}, EffortMappingMatchedEfforts(effortMap, "claude-opus-5-5"))
}

// TestNormalizeEffortModelMappingCredentials validates valid normalization,
// empty-map retention for bulk clears, nil cleanup, and rejection of invalid
// wildcards, unknown effort labels, duplicate normalized keys, and nonboolean
// strip flags.
func TestNormalizeEffortModelMappingCredentials(t *testing.T) {
	creds := map[string]any{
		EffortModelMappingCredentialKey: map[string]any{
			" claude-opus-5-5 ": map[string]any{
				" HIGH ":  " claude-opus-5-5-high ",
				"x-high":  "claude-opus-5-5-xhigh",
				"minimal": "claude-opus-5-5-minimal",
			},
		},
		EffortModelMappingStripEffortCredentialKey: true,
	}
	require.NoError(t, NormalizeEffortModelMappingCredentials(creds))
	normalized, ok := creds[EffortModelMappingCredentialKey].(map[string]any)
	require.True(t, ok)
	entry, ok := normalized["claude-opus-5-5"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, map[string]any{
		"high":    "claude-opus-5-5-high",
		"xhigh":   "claude-opus-5-5-xhigh",
		"minimal": "claude-opus-5-5-minimal",
	}, entry)

	emptyCreds := map[string]any{EffortModelMappingCredentialKey: map[string]any{}}
	require.NoError(t, NormalizeEffortModelMappingCredentials(emptyCreds))
	require.Contains(t, emptyCreds, EffortModelMappingCredentialKey)

	nilCreds := map[string]any{
		EffortModelMappingCredentialKey:            nil,
		EffortModelMappingStripEffortCredentialKey: nil,
	}
	require.NoError(t, NormalizeEffortModelMappingCredentials(nilCreds))
	require.NotContains(t, nilCreds, EffortModelMappingCredentialKey)
	require.NotContains(t, nilCreds, EffortModelMappingStripEffortCredentialKey)

	invalidCases := []struct {
		name  string         // invalid payload description
		creds map[string]any // credentials submitted for validation
	}{
		{name: "non-bool strip flag", creds: map[string]any{EffortModelMappingStripEffortCredentialKey: "true"}},
		{name: "non-map mapping", creds: map[string]any{EffortModelMappingCredentialKey: []string{"bad"}}},
		{name: "mid wildcard source", creds: map[string]any{EffortModelMappingCredentialKey: map[string]any{"claude-*-5": map[string]any{"high": "t"}}}},
		{name: "wildcard target", creds: map[string]any{EffortModelMappingCredentialKey: map[string]any{"claude-opus-5-5": map[string]any{"high": "claude-*"}}}},
		{name: "unsupported effort", creds: map[string]any{EffortModelMappingCredentialKey: map[string]any{"claude-opus-5-5": map[string]any{"ultra": "claude-opus-5-5-ultra"}}}},
		{name: "duplicate normalized effort", creds: map[string]any{EffortModelMappingCredentialKey: map[string]any{"claude-opus-5-5": map[string]any{"high": "a", "HIGH": "b"}}}},
		{name: "empty target", creds: map[string]any{EffortModelMappingCredentialKey: map[string]any{"claude-opus-5-5": map[string]any{"high": "   "}}}},
	}
	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, NormalizeEffortModelMappingCredentials(tc.creds))
		})
	}
}
