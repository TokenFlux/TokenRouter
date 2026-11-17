package qoder

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	"github.com/TokenFlux/TokenRouter/internal/provider/qodermodel"
)

// TestResolveObservedThinkingDefaultsAndProjection exercises absent, explicit,
// and unsupported controls against current account metadata. It preserves
// xhigh, inherits defaults, and rejects disabling an always-reasoning route.
func TestResolveObservedThinkingDefaultsAndProjection(t *testing.T) {
	model := qodermodel.Model{Key: "qmodel", Reasoning: new(true), CanDisable: new(true), DefaultThinking: true, DefaultEffort: "medium", Efforts: []string{"low", "medium", "xhigh"}}
	resolution := modelrules.Resolution{Target: modelrules.Target{Model: model.Key, ProviderModels: qodermodel.NewCatalog("test", []qodermodel.Model{model})}, Attributes: model.Attributes()}
	for _, test := range []struct {
		body   string // native client request
		effort string // expected encoded effort
	}{{`{}`, "medium"}, {`{"reasoning_effort":"high"}`, "xhigh"}, {`{"reasoning_effort":"max"}`, "xhigh"}, {`{"reasoning_effort":"xhigh"}`, "xhigh"}, {`{"reasoning_effort":"low"}`, "low"}, {`{"thinking":{"type":"disabled"}}`, "none"}, {`{"thinking":{"budget_tokens":1024}}`, "xhigh"}} {
		directive := QoderThinkingDirectiveFromBody([]byte(test.body), "reasoning_effort")
		resolved, err := ResolveObservedThinking(directive, &resolution)
		require.NoError(t, err)
		require.Equal(t, test.effort, resolved.Effort, test.body)
		payload := QoderBasePayload()
		applyObservedThinking(payload, resolved)
		require.Equal(t, test.effort, qoderFixtureValue[map[string]any](t, payload["parameters"])["reasoning_effort"])
	}
	model.CanDisable = new(false)
	resolution.Target.ProviderModels = qodermodel.NewCatalog("always-on", []qodermodel.Model{model})
	resolution.Attributes = model.Attributes()
	_, err := ResolveObservedThinking(QoderThinkingDirective{Specified: true}, &resolution)
	var apiErr *RequestError
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, "invalid_reasoning_parameter", apiErr.Code)
}

// TestToggleOnlyThinkingOmitsEffort sends both switch positions to a model
// whose catalog declares no intensity levels. Neither wire payload may add
// an effort name that the model cannot accept.
func TestToggleOnlyThinkingOmitsEffort(t *testing.T) {
	model := qodermodel.Model{Key: "toggle", Reasoning: new(true), CanDisable: new(true)}
	resolution := modelrules.Resolution{Target: modelrules.Target{Model: model.Key, ProviderModels: qodermodel.NewCatalog("test", []qodermodel.Model{model})}, Attributes: model.Attributes()}
	for _, enabled := range []bool{true, false} {
		payload := QoderBasePayload()
		resolved, err := ResolveObservedThinking(QoderThinkingDirective{Specified: true, Enabled: enabled}, &resolution)
		require.NoError(t, err)
		applyObservedThinking(payload, resolved)
		parameters := qoderFixtureValue[map[string]any](t, payload["parameters"])
		require.Equal(t, enabled, parameters["enable_thinking"])
		require.NotContains(t, parameters, "reasoning_effort")
	}
}

// TestQoderMandatoryThinkingSurvivesGroupOverride applies a profile that disables
// mandatory reasoning. Preview exposes the configured and executable choices,
// and an explicit disable fails before the encoder can create an upstream request.
func TestQoderMandatoryThinkingSurvivesGroupOverride(t *testing.T) {
	model := qodermodel.Model{Key: "required", Reasoning: new(true), CanDisable: new(false), DefaultThinking: true}
	index := qodermodel.NewCatalog("account", []qodermodel.Model{model})
	resolution := modelrules.View{}.Resolve(modelrules.Target{Model: model.Key, ProviderModels: index}, modelcatalog.Attributes{Reasoning: new(false)})
	require.Equal(t, new(true), resolution.Attributes.Reasoning)
	require.NotNil(t, resolution.ConfiguredAttributes)
	require.Equal(t, new(false), resolution.ConfiguredAttributes.Reasoning)
	payload, _, err := BuildQoderPayloadWithOptions(QoderPayloadRequest{Model: model.Key, Capabilities: &resolution, Thinking: QoderThinkingDirective{Specified: true}}, "", nil, true, true)
	var invalid *RequestError
	require.ErrorAs(t, err, &invalid)
	require.Equal(t, "invalid_reasoning_parameter", invalid.Code)
	require.Nil(t, payload)
}
