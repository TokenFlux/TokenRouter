package googleforward

import (
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// model_attributes.go applies resolved sampling, thinking, and output-limit
// policy to Gemini request bodies.
// Gemini controls can originate in Anthropic or OpenAI input before a Gemini
// request exists. Attribute application uses the final route with those
// original controls, removes known unsupported sampling fields under both
// spellings, and caps converted Antigravity output where that route requires
// it.
//
// Thinking conversion is optional because native input already carries its
// protocol controls. Unknown sampling support retains a supplied field, while a
// known false flag removes it from either generationConfig spelling. Converted
// Antigravity applies its output cap to camelCase maxOutputTokens; native
// Gemini protocol retains the separate request path. JSON update errors return
// before another field change is attempted.

// applyGeminiModelParameters applies final-route attributes using controls from
// the original client body. convertThinking selects translation of those
// controls into Gemini thinkingConfig, and JSON update errors are returned
// immediately.
//
// Sampling support can be unknown, true, or false. Unknown and supported values
// retain supplied fields; known false values remove only present fields under
// both generation config and topP spellings. Converted Antigravity routes also
// cap camelCase maxOutputTokens when their resolved output limit is exceeded,
// while native Gemini input follows its own control handling.
func applyGeminiModelParameters(body, controls []byte, resolution modelrules.Resolution, convertThinking bool) ([]byte, error) {
	result := resolution.ForRequest(gatewayprovider.ModelRequestState(controls))
	var err error
	if convertThinking {
		thinking, thinkingErr := gatewayprovider.GeminiThinking(resolution, controls)
		if thinkingErr != nil {
			return nil, thinkingErr
		}
		if thinking != nil {
			body, err = sjson.SetBytes(body, "generationConfig.thinkingConfig", thinking)
		}
		if err != nil {
			return nil, err
		}
	}
	// Check both accepted config and topP spellings so a client alternate
	// spelling cannot retain a sampling control that the final selected
	// route marks unsupported.
	for _, configName := range []string{"generationConfig", "generation_config"} {
		config := gjson.GetBytes(body, configName)
		for _, field := range []string{"responseModalities", "response_modalities"} {
			for _, modality := range config.Get(field).Array() {
				if strings.EqualFold(modality.String(), "image") {
					if err := routing.RequireImageOutput(result.Attributes); err != nil {
						return nil, err
					}
				}
			}
		}
		for key, support := range map[string]*bool{"temperature": result.Attributes.Temperature, "topP": result.Attributes.TopP, "top_p": result.Attributes.TopP} {
			if support == nil || *support || !config.Get(key).Exists() {
				continue
			}
			body, err = sjson.DeleteBytes(body, configName+"."+key)
			if err != nil {
				return nil, err
			}
		}
	}
	// Apply the converted Antigravity output ceiling after sampling cleanup
	// so the translated request respects that route limit while native
	// Gemini controls use their separate policy.
	if resolution.Target.Platform == "antigravity" && resolution.Target.ClientProtocol != "gemini_generate_content" {
		if limit := result.Attributes.OutputLimit; limit != nil && gjson.GetBytes(body, "generationConfig.maxOutputTokens").Int() > int64(*limit) {
			body, err = sjson.SetBytes(body, "generationConfig.maxOutputTokens", *limit)
		}
	}
	return body, err
}
