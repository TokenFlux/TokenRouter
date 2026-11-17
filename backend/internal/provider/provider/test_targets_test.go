package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
)

// test_targets_test.go checks capability admission for automatic and explicit
// provider probes. Controlled group lookups distinguish image and text endpoint
// declarations, so a capability from one endpoint cannot authorize a different
// request format.

// TestResolveProbeTypeUsesEndpointAttributes scopes custom output declarations
// to their actual probe endpoint. Automatic text probes consult their selected
// text route, while explicit images require the image route's positive declaration.
func TestResolveProbeTypeUsesEndpointAttributes(t *testing.T) {
	for _, target := range []struct {
		platform string // tested provider platform
		kind     string // tested credential type
		protocol string // actual probe text protocol
	}{
		{"openai", "apikey", "openai_chat_completions"},
		{"grok", "apikey", "openai_responses"},
		{"grok", "oauth", "openai_responses"},
	} {
		for _, tc := range []struct {
			name          string              // test scenario name
			kind          string              // explicit kind selection
			outputs       map[string][]string // output declarations per endpoint
			want          string              // expected admitted kind
			wantProtocols []string            // expected ordered endpoint lookups
		}{
			{"automatic image", "", map[string][]string{"openai_images_generations": {"image"}}, "image", []string{"openai_images_generations"}},
			{"automatic text", "", map[string][]string{target.protocol: {"text"}}, "text", []string{"openai_images_generations", target.protocol}},
			{"explicit text", "text", nil, "text", nil},
			{"mixed output", "", map[string][]string{"openai_images_generations": {"text", "image"}}, "image", []string{"openai_images_generations"}},
			{"explicit image", "image", map[string][]string{"openai_images_generations": {"image"}}, "image", []string{"openai_images_generations"}},
			{"image declared on text route", "image", map[string][]string{target.protocol: {"image"}}, "", []string{"openai_images_generations"}},
		} {
			t.Run(target.platform+"/"+target.kind+"/"+tc.name, func(t *testing.T) {
				var protocols []string
				lookup := func(_ context.Context, _ *provider.Record, groupID int64, model, protocol string) (modelrules.Resolution, error) {
					require.Equal(t, int64(59), groupID)
					require.Equal(t, "mapped-model", model)
					protocols = append(protocols, protocol)
					result := modelrules.Resolution{}
					if outputs, ok := tc.outputs[protocol]; ok {
						result.Attributes.OutputModalities = &outputs
					}
					return result, nil
				}
				value := &provider.Record{Platform: target.platform, Type: target.kind}
				kind, err := resolveProbeType(t.Context(), lookup, value, 59, "mapped-model", target.protocol, tc.kind)
				require.Equal(t, tc.want, kind)
				if tc.want == "" {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
				require.Equal(t, tc.wantProtocols, protocols)
			})
		}
	}
}

// TestResolveProbeTypeStopsOnProfileFailure rejects a failed image attribute read.
// Automatic selection propagates the storage error before trying another endpoint or dispatching a request.
func TestResolveProbeTypeStopsOnProfileFailure(t *testing.T) {
	failed := errors.New("profile unavailable")
	calls := 0
	lookup := func(context.Context, *provider.Record, int64, string, string) (modelrules.Resolution, error) {
		calls++
		return modelrules.Resolution{}, failed
	}
	kind, err := resolveProbeType(t.Context(), lookup, &provider.Record{Platform: "openai", Type: "apikey"}, 59, "mapped-model", "openai_responses")
	require.ErrorIs(t, err, failed)
	require.Empty(t, kind)
	require.Equal(t, 1, calls)
}
