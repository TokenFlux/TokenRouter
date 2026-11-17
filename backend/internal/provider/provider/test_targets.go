package provider

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	"github.com/TokenFlux/TokenRouter/internal/upstream/deepseek"
	"github.com/TokenFlux/TokenRouter/internal/upstream/kimi"
	"github.com/TokenFlux/TokenRouter/internal/upstream/zhipu"
)

// test_targets.go loads provider records and selects the platform executor for
// administrative tests.
// Provider test handles use the stored platform to choose execution after
// loading the requested provider ID. OpenCode, Jev, Qoder, Gemini, Grok, OpenAI,
// Kimi, Zhipu, DeepSeek, and Antigravity have explicit routes; remaining
// platforms enter the Anthropic
// executor, which validates supported provider types during execution. Automatic
// text and image probes select their payload using declared final-model output;
// explicit image probes require the same positive capability evidence.
//
// Attribute lookups carry the selected group through both endpoint checks.
// OpenAI API-key and Grok image and text requests use different protocols, so each
// declaration authorizes its own route. Explicit group routes keep their selected
// endpoint and validate output there before sending any request. The complete resolution retains target
// identity and conditional rules for executors that adjust sampling parameters.

// TestTargets loads provider records and binds platform executors for prepared
// probes.
type TestTargets struct {
	Jev *JevProviderTest // Jev SystemOne probe executor
	// Read is required for target loading. A successful callback returns a nonnil
	// provider whose platform selects the executor.
	Read        func(context.Context, int64) (*provider.Record, error)
	Qoder       *QoderProviderTest       // Qoder session probe executor
	Gemini      *GeminiProviderTest      // Gemini platform probe executor
	Grok        *GrokProviderTest        // Grok platform probe executor
	Anthropic   *AnthropicProviderTest   // default Anthropic probe executor
	OpenAI      *OpenAIProviderTest      // OpenAI platform probe executor
	Native      *NativeProtocolTest      // shared API-key protocol probe executor
	Antigravity *AntigravityProviderTest // Antigravity platform probe executor
}

// ModelAttributeLookup resolves the final provider model for a group and execution protocol.
// Routed probes retain the client entry in context for protocol-scoped profile matching.
// The result retains target identity and conditional rules for sampling checks.
// Profile failures propagate before the executor sends its upstream request.
type ModelAttributeLookup func(context.Context, *provider.Record, int64, string, string) (modelrules.Resolution, error)

// LoadTestTarget reads the requested provider and returns its platform-bound
// test handle. Read errors propagate; successful reads supply a nonnil record,
// and callers configure the executor selected by that platform, including the
// Anthropic default.
func (t *TestTargets) LoadTestTarget(ctx context.Context, request provider.TestRequest) (provider.TestTarget, error) {
	value, err := t.Read(ctx, request.ProviderID)
	if err != nil {
		return nil, err
	}
	switch value.Platform {
	case provider.PlatformOpenCode:
		return openCodeTestTarget{executor: t.Native, record: value}, nil
	case provider.PlatformJev:
		return t.Jev.Target(value), nil
	case provider.PlatformQoder:
		return t.Qoder.Target(value), nil
	case provider.PlatformGemini:
		return t.Gemini.Target(value), nil
	case provider.PlatformGrok:
		return t.Grok.Target(value), nil
	case provider.PlatformOpenAI:
		return t.OpenAI.Target(value), nil
	case provider.PlatformKimi:
		return t.Native.Target(value, kimi.DefaultTestModel), nil
	case provider.PlatformDeepseek:
		return t.Native.Target(value, deepseek.DefaultTestModel), nil
	case provider.PlatformZhipu:
		return t.Native.Target(value, zhipu.DefaultTestModel), nil
	case provider.PlatformAntigravity:
		return t.Antigravity.Target(value), nil
	default:
		return t.Anthropic.Target(value), nil
	}
}

// resolveProbeType selects automatic test payloads from declared output.
// Explicit text probes retain their chosen operation. Image probes require
// confirmed image support even when selected explicitly; unknown automatic
// probes require a type selection or a model attribute declaration.
//
// Callers complete group and provider mapping before this lookup. A mixed
// text/image declaration selects the image probe automatically, while explicit
// text keeps its chosen payload. Resolver errors stop preparation immediately;
// an empty output set cannot be rescued by a model name or a selected image
// test type. OpenAI API keys and Grok check image capability on the images endpoint;
// automatic text probes then check their selected text endpoint independently.
// Routed group tests validate the selected upstream endpoint directly. Explicit
// text permits unknown output, but a declared unsupported output fails.
func resolveProbeType(ctx context.Context, lookup ModelAttributeLookup, value *provider.Record, groupID int64, model, protocol string, testTypes ...string) (string, error) {
	kind, explicit := provider.ProviderTestTypeFromArgs(testTypes...)
	if route := provider.TestProtocolRouteFromContext(ctx); route.ClientProtocol != "" {
		resolved := modelrules.Resolution{}
		if lookup != nil {
			var err error
			resolved, err = lookup(ctx, value, groupID, model, string(route.UpstreamProtocol))
			if err != nil {
				return "", err
			}
		}
		// A routed test reads only its selected endpoint. Image capability on a
		// different endpoint cannot authorize this request or change its test kind.
		if !explicit {
			if image := resolved.Attributes.OutputSupport("image"); image != nil && *image {
				return provider.ProviderTestTypeImage, nil
			}
			kind = provider.ProviderTestTypeText
		}
		output := "text"
		if kind == provider.ProviderTestTypeImage {
			output = "image"
		}
		support := resolved.Attributes.OutputSupport(output)
		if support != nil && !*support {
			return "", apperror.BadRequest("MODEL_CAPABILITY_UNSUPPORTED", "the selected probe route does not support the required output")
		}
		if support == nil && (!explicit || kind == provider.ProviderTestTypeImage) {
			return "", apperror.BadRequest("MODEL_CAPABILITY_UNKNOWN", "the selected probe route has no declared output capability")
		}
		return kind, nil
	}
	if explicit && kind != provider.ProviderTestTypeImage {
		return kind, nil
	}
	imageProtocol := protocol
	if value.Platform == provider.PlatformGrok || (value.Platform == provider.PlatformOpenAI && value.Type == provider.ProviderTypeAPIKey) {
		imageProtocol = "openai_images_generations"
	}
	resolved := modelrules.Resolution{}
	if lookup != nil {
		var err error
		resolved, err = lookup(ctx, value, groupID, model, imageProtocol)
		if err != nil {
			return "", err
		}
	}
	image := resolved.Attributes.OutputSupport("image")
	if image != nil && *image {
		return provider.ProviderTestTypeImage, nil
	}
	if !explicit {
		// OpenAI API keys and Grok use separate image and text endpoints. If the image route
		// lacks support, resolve the actual text route before admitting an automatic text probe.
		if lookup != nil && imageProtocol != protocol {
			var err error
			resolved, err = lookup(ctx, value, groupID, model, protocol)
			if err != nil {
				return "", err
			}
		}
		if text := resolved.Attributes.OutputSupport("text"); text != nil && *text {
			return provider.ProviderTestTypeText, nil
		}
	}
	if image == nil {
		return "", apperror.BadRequest("MODEL_CAPABILITY_UNKNOWN", "model output capability is unknown; configure image attributes or explicitly select a text test")
	}
	return "", apperror.BadRequest("MODEL_CAPABILITY_UNSUPPORTED", "the model does not declare the output required by this test")
}

// applyProbeSampling removes forbidden sampling defaults from a routed test payload.
// The caller supplies the final upstream model and a mutable native payload.
// Automatic legacy tests retain their existing builders; routed tests propagate
// profile failures before transport and reuse the test's attribute session.
func applyProbeSampling(run *TestRun, lookup ModelAttributeLookup, value *provider.Record, model, protocol string, payload map[string]any) error {
	if run.ProtocolRoute.ClientProtocol == "" || lookup == nil {
		return nil
	}
	resolved, err := lookup(run.Context, value, run.GroupID, model, protocol)
	if err != nil {
		return err
	}
	resolved = resolved.ForRequest(modelrules.RequestState{})
	if resolved.Attributes.Temperature != nil && !*resolved.Attributes.Temperature {
		delete(payload, "temperature")
	}
	if resolved.Attributes.TopP != nil && !*resolved.Attributes.TopP {
		delete(payload, "top_p")
	}
	return nil
}
