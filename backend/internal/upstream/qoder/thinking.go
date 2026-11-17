package qoder

import (
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	"github.com/TokenFlux/TokenRouter/internal/provider/qodermodel"
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
// model controls. Custom routes need configured controls to emit a directive. An
// unsupported explicit disable returns a client parameter error before any
// inference request is started, and supported controls preserve explicit intent.
//
// Toggle and effort support follow the effective administrator attributes,
// while defaults come from the captured account observation. Explicit custom
// routes can use configured controls without acquiring invented defaults. The
// selected intensity is projected through the canonical order so absent
// intermediate levels cannot accidentally select a weaker level or collapse
// xhigh into max before the model's own supported set is known.
func ResolveObservedThinking(directive QoderThinkingDirective, resolution *modelrules.Resolution) (QoderThinkingDirective, error) {
	if resolution == nil {
		return QoderThinkingDirective{}, nil
	}
	model, observed := observedModel(resolution)
	attrs := resolution.Attributes
	explicit := directive.Specified || directive.Enabled || directive.Effort != ""
	if !explicit {
		if !observed || model.Reasoning == nil {
			return QoderThinkingDirective{}, nil
		}
		directive.Enabled, directive.Effort = model.DefaultThinking, model.DefaultEffort
	}
	// A group may narrow configurable controls. Actual mandatory reasoning remains
	// a wire constraint even when the profile removes every advertised option.
	reasoning := attrs.Reasoning != nil && *attrs.Reasoning
	toggle := attrs.HasReasoningOption("toggle")
	disallowDisable := model.RequiresThinking() || reasoning && attrs.ReasoningOptions != nil && !toggle
	if explicit && !directive.Enabled && disallowDisable {
		return directive, &RequestError{Code: "invalid_reasoning_parameter", Message: "The selected Qoder model does not support disabling reasoning"}
	}
	if attrs.Reasoning != nil && !*attrs.Reasoning {
		directive.Enabled = false
	}
	efforts := attrs.Efforts()
	if !directive.Enabled {
		directive.Effort = ""
		if len(efforts) > 0 || observed && model.CanDisable != nil && *model.CanDisable && len(model.Efforts) > 0 {
			directive.Effort = "none"
		}
	} else if len(efforts) == 0 {
		directive.Effort = ""
	}
	// Only emit switches for an observed reasoning control or a configured custom
	// route. Unspecified controls on unknown models retain the wire defaults.
	directive.Specified = toggle || len(efforts) > 0 || reasoning || model.Reasoning != nil && *model.Reasoning
	if !directive.Enabled || len(efforts) == 0 {
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

// applyObservedThinking mirrors one validated directive into the native payload.
// An absent directive leaves the payload's defaults intact. Callers resolve
// profile limits and upstream constraints before invoking this wire writer.
func applyObservedThinking(payload map[string]any, resolved QoderThinkingDirective) {
	if !resolved.Specified {
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

// observedModel reads the execution model from the attempt's pinned publication.
// After profile reference resolution the target still names the actual wire
// route, whose defaults and native settings govern payload encoding.
func observedModel(resolution *modelrules.Resolution) (qodermodel.Model, bool) {
	if resolution != nil {
		if index, ok := resolution.Target.ProviderModels.(*qodermodel.Catalog); ok {
			return index.Lookup(resolution.Target.Model)
		}
	}
	return qodermodel.Model{}, false
}
