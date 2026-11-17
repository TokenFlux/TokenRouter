package qoder

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
)

// TestResolveObservedThinkingDefaultsAndProjection exercises absent, explicit,
// and unsupported controls against current account metadata. It preserves
// xhigh, inherits defaults, and rejects disabling an always-reasoning route.
func TestResolveObservedThinkingDefaultsAndProjection(t *testing.T) {
	model := modelcatalog.QoderModel{Key: "qmodel", Reasoning: true, CanDisable: true, DefaultThinking: true, DefaultEffort: "medium", Efforts: []string{"low", "medium", "xhigh"}}
	resolution := modelcatalog.Resolution{Target: modelcatalog.Target{QoderModel: &model}, Attributes: model.Attributes()}
	for _, test := range []struct {
		body   string // native client request
		effort string // expected encoded effort
	}{{`{}`, "medium"}, {`{"reasoning_effort":"high"}`, "xhigh"}, {`{"reasoning_effort":"max"}`, "xhigh"}, {`{"reasoning_effort":"xhigh"}`, "xhigh"}, {`{"reasoning_effort":"low"}`, "low"}, {`{"thinking":{"type":"disabled"}}`, "none"}, {`{"thinking":{"budget_tokens":1024}}`, "xhigh"}} {
		directive := QoderThinkingDirectiveFromBody([]byte(test.body), "reasoning_effort")
		resolved, err := ResolveObservedThinking(directive, &resolution)
		require.NoError(t, err)
		require.Equal(t, test.effort, resolved.Effort, test.body)
		payload := QoderBasePayload()
		ApplyQoderThinkingDirective(payload, SiteGlobal, model.Key, directive, &resolution)
		require.Equal(t, test.effort, qoderFixtureValue[map[string]any](t, payload["parameters"])["reasoning_effort"])
	}
	model.CanDisable = false
	resolution.Attributes = model.Attributes()
	_, err := ResolveObservedThinking(QoderThinkingDirective{Specified: true}, &resolution)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, 400, apiErr.StatusCode)
}

// TestToggleOnlyThinkingOmitsEffort sends both switch positions to a model
// whose catalog declares no intensity levels. Neither wire payload may add
// an effort name that the model cannot accept.
func TestToggleOnlyThinkingOmitsEffort(t *testing.T) {
	model := modelcatalog.QoderModel{Key: "toggle", Reasoning: true, CanDisable: true}
	resolution := modelcatalog.Resolution{Target: modelcatalog.Target{QoderModel: &model}, Attributes: model.Attributes()}
	for _, enabled := range []bool{true, false} {
		payload := QoderBasePayload()
		ApplyQoderThinkingDirective(payload, SiteGlobal, model.Key, QoderThinkingDirective{Specified: true, Enabled: enabled}, &resolution)
		parameters := qoderFixtureValue[map[string]any](t, payload["parameters"])
		require.Equal(t, enabled, parameters["enable_thinking"])
		require.NotContains(t, parameters, "reasoning_effort")
	}
}
