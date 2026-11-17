package modelrules

import (
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
)

// deepseek.go defines DeepSeek sampling conditions for Chat and Messages routes.
// Thinking state controls temperature and top-p independently, and a canonical
// service alias inherits these conditions when its selected upstream protocol
// supports them.

// applyDeepSeekEncoding selects payload controls for the final DeepSeek author identity.
func (r *Resolution) applyDeepSeekEncoding(model string, patch *modelcatalog.Attributes, conditions modelcatalog.ParameterConditions) {
	t := r.Target
	// DeepSeek sampling rules cover Chat and Messages requests. Responses
	// overrides lack a registered model encoding for these conditional controls.
	if t.usesCanonicalIdentity() && t.UpstreamProtocol == "openai_responses" {
		return
	}
	if slices.Contains([]string{"deepseek-v4-pro", "deepseek-v4-flash", "deepseek-flash", "deepseek-v4-flash-vision-exp"}, model) {
		// These sampling controls select opposite thinking states. Keep
		// both conditions so ForRequest disables whichever parameter
		// conflicts with the request's evaluated thinking choice.
		conditions["temperature"] = modelcatalog.ParameterCondition{Thinking: new(false)}
		conditions["top_p"] = modelcatalog.ParameterCondition{Thinking: new(true)}
		patch.TopP = new(true)
	}
}
