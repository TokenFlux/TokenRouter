package requeststate

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIReasoningEffortPolicyContext(t *testing.T) {
	body := []byte(`{"reasoning":{"effort":"max"}}`)

	unbound, changed, err := ApplyOpenAIReasoningEffortPolicyFromContext(context.Background(), body)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, unbound)

	mappings := []routing.ReasoningEffortMapping{{From: "max", To: "xhigh"}}
	ctx := WithOpenAIReasoningEffortPolicy(context.Background(), "medium", mappings, "")
	mappings[0].To = "low"
	got, changed, err := ApplyOpenAIReasoningEffortPolicyFromContext(ctx, body)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "medium", gjson.GetBytes(got, "reasoning.effort").String())

	denyCtx := WithOpenAIReasoningEffortPolicy(context.Background(), "medium", nil, routing.ReasoningEffortOverLimitDeny)
	_, _, err = ApplyOpenAIReasoningEffortPolicyFromContext(denyCtx, body)
	require.Error(t, err)
	var overLimit *routing.ReasoningEffortOverLimitError
	require.ErrorAs(t, err, &overLimit)
	require.Equal(t, "max", overLimit.Requested)
	require.Equal(t, "medium", overLimit.Max)
}

func TestApplyOpenAIReasoningEffortPolicy(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		max       string
		overLimit string
		mappings  []routing.ReasoningEffortMapping
		path      string
		want      string
		changed   bool
		deny      bool
	}{
		{name: "nested caps high", body: `{"reasoning":{"effort":"xhigh"}}`, max: "medium", path: "reasoning.effort", want: "medium", changed: true},
		{name: "flat caps high", body: `{"reasoning_effort":"high"}`, max: "low", path: "reasoning_effort", want: "low", changed: true},
		{name: "Anthropic output config caps max", body: `{"output_config":{"effort":"max"}}`, max: "xhigh", path: "output_config.effort", want: "xhigh", changed: true},
		{name: "does not raise omitted", body: `{"model":"gpt-5"}`, max: "low", path: "reasoning_effort", want: "", changed: false},
		{name: "keeps lower value", body: `{"reasoning_effort":"low"}`, max: "high", path: "reasoning_effort", want: "low", changed: false},
		{name: "normalizes request alias", body: `{"reasoning_effort":"x-high"}`, max: "xhigh", path: "reasoning_effort", want: "xhigh", changed: true},
		{name: "caps max below its distinct rank", body: `{"reasoning_effort":"max"}`, max: "xhigh", path: "reasoning_effort", want: "xhigh", changed: true},
		{name: "keeps xhigh below max", body: `{"reasoning_effort":"xhigh"}`, max: "max", path: "reasoning_effort", want: "xhigh", changed: false},
		{name: "ignores stale none ceiling", body: `{"reasoning_effort":"high"}`, max: "none", path: "reasoning_effort", want: "high", changed: false},
		{name: "caps both shapes", body: `{"reasoning":{"effort":"high"},"reasoning_effort":"xhigh"}`, max: "low", path: "reasoning.effort", want: "low", changed: true},
		{name: "maps before cap", body: `{"reasoning":{"effort":"MAX"}}`, max: "medium", mappings: []routing.ReasoningEffortMapping{{From: "max", To: "xhigh"}}, path: "reasoning.effort", want: "medium", changed: true},
		{name: "maps none when configured", body: `{"reasoning":{"effort":"none"}}`, mappings: []routing.ReasoningEffortMapping{{From: "none", To: "low"}}, path: "reasoning.effort", want: "low", changed: true},
		{name: "does not chain mappings", body: `{"reasoning_effort":"max"}`, mappings: []routing.ReasoningEffortMapping{{From: "max", To: "xhigh"}, {From: "xhigh", To: "low"}}, path: "reasoning_effort", want: "xhigh", changed: true},
		{name: "keeps unknown without mapping", body: `{"reasoning_effort":"future"}`, max: "low", path: "reasoning_effort", want: "future", changed: false},
		{name: "keeps non string value", body: `{"reasoning_effort":{"level":"high"}}`, max: "low", path: "reasoning_effort.level", want: "high", changed: false},
		{name: "deny rejects over ceiling", body: `{"reasoning_effort":"high"}`, max: "low", overLimit: routing.ReasoningEffortOverLimitDeny, deny: true},
		{name: "deny keeps value at ceiling", body: `{"reasoning_effort":"low"}`, max: "low", overLimit: routing.ReasoningEffortOverLimitDeny, path: "reasoning_effort", want: "low"},
		{name: "deny after mapping still over ceiling", body: `{"reasoning":{"effort":"max"}}`, max: "medium", overLimit: routing.ReasoningEffortOverLimitDeny, mappings: []routing.ReasoningEffortMapping{{From: "max", To: "xhigh"}}, deny: true},
		{name: "deny allows mapping under ceiling", body: `{"reasoning":{"effort":"max"}}`, max: "medium", overLimit: routing.ReasoningEffortOverLimitDeny, mappings: []routing.ReasoningEffortMapping{{From: "max", To: "low"}}, path: "reasoning.effort", want: "low", changed: true},
		{name: "deny ignored without ceiling", body: `{"reasoning_effort":"high"}`, overLimit: routing.ReasoningEffortOverLimitDeny, path: "reasoning_effort", want: "high"},
		{
			name:     "prefix mapping applies to matching model",
			body:     `{"model":"gpt-5.4","reasoning_effort":"max"}`,
			mappings: []routing.ReasoningEffortMapping{{From: "max", To: "low", MatchType: routing.ReasoningEffortMatchPrefix, Model: "gpt"}},
			path:     "reasoning_effort",
			want:     "low",
			changed:  true,
		},
		{
			name:     "prefix mapping skips non matching model",
			body:     `{"model":"o3","reasoning_effort":"max"}`,
			mappings: []routing.ReasoningEffortMapping{{From: "max", To: "low", MatchType: routing.ReasoningEffortMatchPrefix, Model: "gpt"}},
			path:     "reasoning_effort",
			want:     "max",
			changed:  false,
		},
		{
			name: "exact mapping beats prefix",
			body: `{"model":"gpt-5.4","reasoning":{"effort":"max"}}`,
			mappings: []routing.ReasoningEffortMapping{
				{From: "max", To: "low", MatchType: routing.ReasoningEffortMatchPrefix, Model: "gpt"},
				{From: "max", To: "medium", MatchType: routing.ReasoningEffortMatchExact, Model: "gpt-5.4"},
			},
			path:    "reasoning.effort",
			want:    "medium",
			changed: true,
		},
		{
			name: "longer prefix beats shorter prefix",
			body: `{"model":"gpt-5.4","reasoning_effort":"max"}`,
			mappings: []routing.ReasoningEffortMapping{
				{From: "max", To: "low", MatchType: routing.ReasoningEffortMatchPrefix, Model: "gpt"},
				{From: "max", To: "high", MatchType: routing.ReasoningEffortMatchPrefix, Model: "gpt-5"},
			},
			path:    "reasoning_effort",
			want:    "high",
			changed: true,
		},
		{
			name: "falls back to global mapping when no model scope hits",
			body: `{"model":"o3","reasoning_effort":"max"}`,
			mappings: []routing.ReasoningEffortMapping{
				{From: "max", To: "low", MatchType: routing.ReasoningEffortMatchPrefix, Model: "gpt"},
				{From: "max", To: "medium"},
			},
			path:    "reasoning_effort",
			want:    "medium",
			changed: true,
		},
		{
			name: "model scoped mapping still respects ceiling",
			body: `{"model":"gpt-5.4","reasoning":{"effort":"max"}}`,
			max:  "medium",
			mappings: []routing.ReasoningEffortMapping{
				{From: "max", To: "xhigh", MatchType: routing.ReasoningEffortMatchPrefix, Model: "gpt"},
			},
			path:    "reasoning.effort",
			want:    "medium",
			changed: true,
		},
		{
			name:     "model match is case insensitive",
			body:     `{"model":"GPT-5.4","reasoning_effort":"MAX"}`,
			mappings: []routing.ReasoningEffortMapping{{From: "max", To: "low", MatchType: routing.ReasoningEffortMatchExact, Model: "gpt-5.4"}},
			path:     "reasoning_effort",
			want:     "low",
			changed:  true,
		},
		{
			name:     "suffix mapping applies to matching model",
			body:     `{"model":"gpt-5.4-mini","reasoning_effort":"max"}`,
			mappings: []routing.ReasoningEffortMapping{{From: "max", To: "low", MatchType: routing.ReasoningEffortMatchSuffix, Model: "mini"}},
			path:     "reasoning_effort",
			want:     "low",
			changed:  true,
		},
		{
			name:     "suffix mapping skips non matching model",
			body:     `{"model":"gpt-5.4","reasoning_effort":"max"}`,
			mappings: []routing.ReasoningEffortMapping{{From: "max", To: "low", MatchType: routing.ReasoningEffortMatchSuffix, Model: "mini"}},
			path:     "reasoning_effort",
			want:     "max",
			changed:  false,
		},
		{
			name: "exact mapping beats suffix",
			body: `{"model":"gpt-5.4-mini","reasoning":{"effort":"max"}}`,
			mappings: []routing.ReasoningEffortMapping{
				{From: "max", To: "low", MatchType: routing.ReasoningEffortMatchSuffix, Model: "mini"},
				{From: "max", To: "medium", MatchType: routing.ReasoningEffortMatchExact, Model: "gpt-5.4-mini"},
			},
			path:    "reasoning.effort",
			want:    "medium",
			changed: true,
		},
		{
			name: "longer suffix beats shorter suffix",
			body: `{"model":"gpt-5.4-mini","reasoning_effort":"max"}`,
			mappings: []routing.ReasoningEffortMapping{
				{From: "max", To: "low", MatchType: routing.ReasoningEffortMatchSuffix, Model: "i"},
				{From: "max", To: "high", MatchType: routing.ReasoningEffortMatchSuffix, Model: "mini"},
			},
			path:    "reasoning_effort",
			want:    "high",
			changed: true,
		},
		{
			name: "longer affix beats the other affix",
			body: `{"model":"gpt-5.4-mini","reasoning_effort":"max"}`,
			mappings: []routing.ReasoningEffortMapping{
				{From: "max", To: "low", MatchType: routing.ReasoningEffortMatchSuffix, Model: "mini"},
				{From: "max", To: "high", MatchType: routing.ReasoningEffortMatchPrefix, Model: "gpt-5.4"},
			},
			path:    "reasoning_effort",
			want:    "high",
			changed: true,
		},
		{
			name:     "empty type and model apply to every model",
			body:     `{"model":"o3","reasoning_effort":"max"}`,
			mappings: []routing.ReasoningEffortMapping{{From: "max", To: "low"}},
			path:     "reasoning_effort",
			want:     "low",
			changed:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed, err := ApplyOpenAIReasoningEffortPolicy([]byte(tt.body), tt.max, tt.mappings, tt.overLimit)
			if tt.deny {
				require.Error(t, err)
				var overLimit *routing.ReasoningEffortOverLimitError
				require.ErrorAs(t, err, &overLimit)
				require.False(t, changed)
				require.Equal(t, tt.body, string(got))
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.changed, changed)
			if tt.path != "" {
				require.Equal(t, tt.want, gjson.GetBytes(got, tt.path).String())
			}
		})
	}
}
