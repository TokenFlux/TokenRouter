package openai

import (
	_ "embed"
	"strings"
)

// constants.go selects embedded Codex instructions by model and defines
// default provider probe models.
// GPT-6 Astra, GPT-6.1 Sol, and GPT-6.1 Terra use their own embedded prompts
// when their model names match the supported spellings. The GPT-5 rules give
// Codex names precedence over GPT-5 version prefixes, with GPT-5.5 as the
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
	// Normalize the GPT-6 lookup separately so the new aliases do not change
	// how existing GPT-5 and Codex names are classified.
	gpt6Model := strings.TrimSpace(m[strings.LastIndexByte(m, '/')+1:])
	gpt6Model = strings.ReplaceAll(gpt6Model, "_", "-")
	gpt6Model = strings.Join(strings.Fields(gpt6Model), "-")
	for strings.Contains(gpt6Model, "--") {
		gpt6Model = strings.ReplaceAll(gpt6Model, "--", "-")
	}

	switch {
	case isGPT61InstructionModel(gpt6Model, "gpt-6.1-sol"):
		if strings.TrimSpace(instructionsGPT61Sol) != "" {
			return instructionsGPT61Sol
		}
	case isGPT61InstructionModel(gpt6Model, "gpt-6.1-terra"):
		if strings.TrimSpace(instructionsGPT61Terra) != "" {
			return instructionsGPT61Terra
		}
	case gpt6Model == "gpt-6", gpt6Model == "gpt-6-astra", strings.HasPrefix(gpt6Model, "gpt-6-astra-"):
		if strings.TrimSpace(instructionsGPT6Astra) != "" {
			return instructionsGPT6Astra
		}
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
	// Unknown families and blank dedicated prompts use the latest available
	// instruction body so synthesized OAuth requests receive a non-family-specific default.
	return latestCodexInstructions()
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
