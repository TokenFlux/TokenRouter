package qoder

import (
	"net/http"
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
)

// thinking.go resolves client reasoning intent against an account model's
// observed controls. A missing request inherits upstream defaults, while an
// explicit disable remains distinct and is rejected for models that cannot
// disable reasoning. Administrator attributes narrow or replace the exposed
// effort options before request encoding.
//
// The same resolved directive is mirrored into every Qoder configuration copy.
// Effort projection uses a fixed intensity ordering, preserves supported input,
// and promotes an unsupported intermediate choice to the next supported level.
// The highest available choice bounds requests above the model's range. This
// keeps xhigh separate from max and avoids sending a level that the selected
// account's model does not accept.

// ResolveObservedThinking validates and projects one request onto observed
// model controls. Missing observations leave the directive unchanged. An
// unsupported explicit disable returns a client parameter error before any
// inference request is started, and supported controls preserve explicit intent.
//
// Toggle and effort support follow the effective administrator attributes,
// while defaults come from the captured account observation. Explicit custom
// routes can use configured controls without acquiring invented defaults. The
// selected intensity is projected through the canonical order so absent
// intermediate levels cannot accidentally select a weaker level or collapse
// xhigh into max before the model's own supported set is known.
func ResolveObservedThinking(directive QoderThinkingDirective, resolution *modelcatalog.Resolution) (QoderThinkingDirective, error) {
	model := effectiveQoderThinkingModel(resolution)
	if model == nil {
		return directive, nil
	}
	specified := directive.Specified || directive.Enabled || directive.Effort != ""
	if resolution.Target.QoderModel == nil && !specified {
		return directive, nil
	}
	if !specified {
		directive.Enabled, directive.Effort = model.DefaultThinking, model.DefaultEffort
	}
	if !directive.Enabled {
		if specified && model.Reasoning && !model.CanDisable {
			return directive, &APIError{StatusCode: http.StatusBadRequest, Code: "invalid_reasoning_parameter", Message: "The selected Qoder model does not support disabling reasoning"}
		}
		directive.Effort = ""
		if len(model.Efforts) > 0 {
			directive.Effort = "none"
		}
		return directive, nil
	}
	if resolution.Attributes.Reasoning != nil && !*resolution.Attributes.Reasoning && model.CanDisable {
		directive.Enabled = false
		directive.Effort = ""
		if len(model.Efforts) > 0 {
			directive.Effort = "none"
		}
		return directive, nil
	}
	efforts := model.Efforts
	if len(efforts) == 0 {
		directive.Effort = ""
		return directive, nil
	}
	if directive.Effort == "" {
		directive.Effort = model.DefaultEffort
		if directive.Effort == "" {
			for _, effort := range qoderEffortOrder {
				if slices.Contains(efforts, effort) {
					directive.Effort = effort
				}
			}
		}
	}
	if slices.Contains(efforts, directive.Effort) {
		return directive, nil
	}
	// Project only onto levels present in the effective capability set. Walking
	// the canonical order avoids depending on administrator or JSON map ordering.
	requested := slices.Index(qoderEffortOrder, directive.Effort)
	highest := ""
	for rank, effort := range qoderEffortOrder {
		if !slices.Contains(efforts, effort) {
			continue
		}
		highest = effort
		if rank >= requested {
			directive.Effort = effort
			return directive, nil
		}
	}
	directive.Effort = highest
	return directive, nil
}

// applyObservedThinking writes a previously validated directive into payload.
// Executor validates explicit unsupported controls before constructing this
// payload; direct callers must follow the same validation contract.
func applyObservedThinking(payload map[string]any, directive QoderThinkingDirective, resolution *modelcatalog.Resolution) {
	model := effectiveQoderThinkingModel(resolution)
	if model == nil || resolution.Target.QoderModel == nil && !directive.Specified && !directive.Enabled && directive.Effort == "" {
		return
	}
	if !model.Reasoning && !model.CanDisable && len(model.Efforts) == 0 {
		return
	}
	resolved, err := ResolveObservedThinking(directive, resolution)
	if err != nil {
		// Validation belongs before transport and payload construction. Leave the
		// unvalidated low-level payload unchanged instead of encoding forbidden intent.
		return
	}
	parameters, _ := payload["parameters"].(map[string]any)
	modelConfig, _ := payload["model_config"].(map[string]any)
	chatContext, _ := payload["chat_context"].(map[string]any)
	extra, _ := chatContext["extra"].(map[string]any)
	extraModel, _ := extra["modelConfig"].(map[string]any)
	runtime, _ := extra["ideModelConfigOverride"].(map[string]any)
	if runtime == nil {
		runtime = map[string]any{}
		extra["ideModelConfigOverride"] = runtime
	}
	modelConfig["is_reasoning"], extraModel["is_reasoning"] = resolved.Enabled, resolved.Enabled
	parameters["enable_thinking"] = resolved.Enabled
	for _, config := range []map[string]any{parameters, modelConfig, extraModel, runtime} {
		if resolved.Effort != "" {
			config["reasoning_effort"] = resolved.Effort
		} else {
			delete(config, "reasoning_effort")
		}
	}
}

// effectiveQoderThinkingModel combines captured defaults with effective controls.
// Administrator attributes replace the advertised toggle and effort set, while
// observed upstream defaults remain available for ordinary requests. Explicit
// custom routes can use configured controls without receiving invented defaults.
//
// Known models provide their defaults here, and copied attributes supply the
// final option set. A missing observation remains distinct from a model whose
// controls are explicitly empty. This prevents a custom route from acquiring
// another route's thinking policy while allowing an administrator to describe
// controls for a newly introduced raw route.
func effectiveQoderThinkingModel(resolution *modelcatalog.Resolution) *modelcatalog.QoderModel {
	if resolution == nil || resolution.Target.QoderModel == nil && resolution.Attributes.ReasoningOptions == nil {
		return nil
	}
	model := modelcatalog.QoderModel{}
	if resolution.Target.QoderModel != nil {
		model = resolution.Target.QoderModel.Clone()
	}
	model.Efforts = resolution.Attributes.Efforts()
	model.CanDisable = resolution.Attributes.HasReasoningOption("toggle")
	if resolution.Attributes.Reasoning != nil {
		model.Reasoning = *resolution.Attributes.Reasoning
	} else {
		model.Reasoning = model.CanDisable || len(model.Efforts) > 0
	}
	return &model
}
