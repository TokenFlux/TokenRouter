package openai

import (
	_ "embed"
	"strings"
)

// constants.go selects embedded Codex instructions by model and defines default
// provider probe models. GPT-6 Astra, GPT-6.1 Sol, and GPT-6.1 Terra share their
// alias lookup with passthrough, where recognizing a model also controls
// whether the gateway fills omitted instructions or rejects an invalid value.
//
// The lookup identifies a family independently of prompt availability. A
// recognized model still requires instructions if its embedded text is blank,
// so selection then uses the general fallback. For other models, Codex names
// take precedence over GPT-5 version prefixes, and GPT-5.5 supplies the
// fallback for models without dedicated text.

// Probe models select ordinary inference and quota-query compatibility targets;
// callers may override the inference model when testing a configured provider.
const (
	// DefaultTestModel is the default model for OpenAI provider probes.
	DefaultTestModel = "gpt-5.4"

	// CodexUsageProbeModel is the compatibility model for OAuth quota probes.
	CodexUsageProbeModel = "codex-auto-review"
)

// Embedded instruction strings retain their authored contents; model selection
// checks availability and returns the prompt appropriate to the requested family.
var (
	// DefaultInstructions contains the embedded GPT-5-Codex base prompt.
	//
	//go:embed instructions.txt
	DefaultInstructions string

	// instructionsGPT51 is the embedded GPT-5.1 coding prompt. Model selection also
	// uses the GPT-5.2 and GPT-5.5 prompts below; GPT-5.5 covers models without a dedicated prompt.
	//
	//go:embed instructions_gpt5_1.txt
	instructionsGPT51 string

	//go:embed instructions_gpt5_2.txt
	instructionsGPT52 string

	//go:embed instructions_gpt5_5.txt
	instructionsGPT55 string

	// instructionsGPT6Astra comes from openai/codex models.json at 121f91fd5d9d,
	// as vendored in sub2api's instructions_gpt6_astra.txt.
	//
	//go:embed instructions_gpt6_astra.txt
	instructionsGPT6Astra string

	// instructionsGPT61Sol is model_messages.instructions_template from
	// openai/codex models.json at b1e72963c3b71a9265a551e54beff078384efed9,
	// extracted from sub2api's codex_gpt61_sol.json for direct embedding.
	//
	//go:embed instructions_gpt6_1_sol.txt
	instructionsGPT61Sol string

	// instructionsGPT61Terra is model_messages.instructions_template from
	// openai/codex models.json at 126a0ee92ed2c5e73cd8a8f7e37563e8527552d7,
	// extracted from sub2api's codex_gpt61_terra.json for direct embedding.
	//
	//go:embed instructions_gpt6_1_terra.txt
	instructionsGPT61Terra string
)

// latestCodexInstructions returns the latest embedded prompt, selecting
// DefaultInstructions when it is blank. The selected string is returned as
// embedded, so whitespace checking does not rewrite prompt content.
func latestCodexInstructions() string {
	if v := strings.TrimSpace(instructionsGPT55); v != "" {
		return instructionsGPT55
	}
	// A blank latest prompt still needs usable synthesized instructions; the
	// Codex base prompt supplies that recovery value without altering embedded text.
	return DefaultInstructions
}

// CodexBaseInstructionsForModel selects an embedded prompt for the supplied
// model name. GPT-6 Astra, GPT-6.1 Sol, and GPT-6.1 Terra accept provider
// prefixes and use their dedicated text when available. For the remaining names,
// a Codex name takes precedence over the GPT-5 version prefixes. Unknown models
// and matched families with missing dedicated text use latestCodexInstructions
// so callers can still synthesize the request's instructions.
func CodexBaseInstructionsForModel(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if instructions, matched := gpt6CodexInstructions(m); matched {
		if strings.TrimSpace(instructions) != "" {
			return instructions
		}
		// A matched family with a blank prompt still needs usable instructions,
		// so use the same fallback as for models without dedicated text.
		return latestCodexInstructions()
	}

	switch {
	case strings.Contains(m, "codex"):
		return DefaultInstructions
	case strings.HasPrefix(m, "gpt-5.5"):
		return latestCodexInstructions()
	case strings.HasPrefix(m, "gpt-5.2"):
		if v := strings.TrimSpace(instructionsGPT52); v != "" {
			return instructionsGPT52
		}
	case strings.HasPrefix(m, "gpt-5.1"):
		if v := strings.TrimSpace(instructionsGPT51); v != "" {
			return instructionsGPT51
		}
	}
	// Unknown families and blank GPT-5 prompts use the latest available
	// instruction body so OAuth requests still receive a usable default.
	return latestCodexInstructions()
}

// gpt6CodexInstructions returns the embedded prompt and whether a lowercase
// name belongs to a supported GPT-6 family. The match remains true for empty
// embedded text so passthrough still requires instructions for that model.
func gpt6CodexInstructions(model string) (string, bool) {
	// Normalize GPT-6 spellings in this lookup so callers keep the original
	// name for existing GPT-5 and Codex matching.
	model = strings.TrimSpace(model[strings.LastIndexByte(model, '/')+1:])
	model = strings.ReplaceAll(model, "_", "-")
	model = strings.Join(strings.Fields(model), "-")
	for strings.Contains(model, "--") {
		model = strings.ReplaceAll(model, "--", "-")
	}

	switch {
	case isGPT61InstructionModel(model, "gpt-6.1-sol"):
		return instructionsGPT61Sol, true
	case isGPT61InstructionModel(model, "gpt-6.1-terra"):
		return instructionsGPT61Terra, true
	case model == "gpt-6", model == "gpt-6-astra", strings.HasPrefix(model, "gpt-6-astra-"):
		return instructionsGPT6Astra, true
	default:
		return "", false
	}
}

// isGPT61InstructionModel reports whether a normalized name selects the prompt
// for the supplied GPT-6.1 base model. It recognizes the effort and Compact
// suffixes used by sub2api for identification, independently of effort validation.
func isGPT61InstructionModel(model, base string) bool {
	if model == base {
		return true
	}
	suffix, ok := strings.CutPrefix(model, base+"-")
	if !ok {
		return false
	}
	switch suffix {
	case "none", "minimal", "low", "medium", "high", "xhigh", "max", "openai-compact":
		return true
	default:
		return false
	}
}
