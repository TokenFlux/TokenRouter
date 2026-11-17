package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/bedrock"
	"github.com/TokenFlux/TokenRouter/internal/upstream/vertex"
)

// anthropic_test_execution.go executes Anthropic probes through Messages,
// Vertex, and Bedrock request formats. API-key mappings choose the tested
// target before request construction, Vertex can normalize an unmapped model
// into its service spelling, and Bedrock resolves a model together with its
// invocation region.
//
// The chosen service model also drives sampling capability checks, keeping
// temperature and top_p removal aligned with the target sent upstream.
// Messages and Vertex stream test content; Bedrock uses a nonstreaming
// invocation and converts its first content block into test events. Token
// acquisition is required for Vertex, while ordinary OAuth reads its stored
// access token. Forbidden Messages and Vertex responses attempt an error-state
// write before reporting the probe failure.

// AnthropicProviderTest prepares provider-authenticated Messages, Vertex, and
// Bedrock connection probes.
type AnthropicProviderTest struct {
	// Attributes resolves sampling permissions for the final model and probe group.
	// A nil callback uses catalogue defaults, and lookup errors stop the request.
	Attributes ModelAttributeLookup
	// Tokens supplies Vertex service-account access tokens. A missing source makes
	// the Vertex path emit a configuration failure.
	Tokens    *providercore.ClaudeTokenSource
	Transport QoderTransport // provider-aware probe HTTP sender
	// Profiles optionally resolves request fingerprints for Messages and Vertex.
	// Bedrock sends a nil profile through its dedicated invocation path.
	Profiles    *egressprovider.TLSProfiles
	ValidateURL func(string) (string, error) // API-key upstream root validator
	// Store records forbidden Messages and Vertex probe failures. Configure it for
	// those paths, which invoke SetError without a nil-store guard.
	Store interface {
		// SetError records the supplied provider authentication or access failure.
		// Probe execution ignores storage errors and still returns the upstream
		// failure to its test caller.
		SetError(context.Context, int64, string) error
	}
	// UserAgent replaces the prepared request agent when its trimmed value is
	// nonempty. Automatic targets install this on an executor copy.
	UserAgent string
}

// Execute selects the provider's Anthropic transport and emits probe events for
// the selected model. An empty model uses the Claude test default, and API-key
// records apply one provider mapping before request preparation.
//
// Bedrock and service-account records delegate to their dedicated request
// paths. Ordinary OAuth reads the stored access token, while API keys use a
// validated configured root and selected authentication header scheme. Sampling
// controls follow the service model; responses are closed here, and forbidden
// Messages replies attempt a best-effort provider error write before returning
// their diagnostic failure.
func (s *AnthropicProviderTest) Execute(c *TestRun, value *providercore.Record, modelID string, prompt string) error {
	ctx := c.Context

	testModelID := modelID
	if testModelID == "" {
		testModelID = claude.DefaultTestModel
	}

	// Apply the API-key mapping before sampling and payload construction, so the
	// test observes the configured upstream target under the same alias rules as
	// requests.
	if value.Type == "apikey" {
		testModelID = mappedTestModel(value, testModelID)
	}

	// Bedrock uses a signed, non-streaming invocation.
	if value.IsBedrock() {
		return s.ExecuteBedrock(c, ctx, value, testModelID, prompt)
	}
	if value.Type == capability.ProviderTypeServiceAccount {
		return s.executeVertex(c, ctx, value, testModelID, prompt)
	}

	var authToken string
	var apiURL string

	if value.IsOAuth() {
		apiURL = "https://api.anthropic.com/v1/messages?beta=true"
		authToken = value.GetCredential("access_token")
		if authToken == "" {
			return (TestStreamOutput{}).Error(c, "No access token available")
		}
	} else if value.Type == "apikey" {
		authToken = value.GetCredential("api_key")
		if authToken == "" {
			return (TestStreamOutput{}).Error(c, "No API key available")
		}

		baseURL := value.GetBaseURL()
		if baseURL == "" {
			baseURL = "https://api.anthropic.com"
		}
		normalizedBaseURL, err := s.ValidateURL(baseURL)
		if err != nil {
			return (TestStreamOutput{}).Error(c, fmt.Sprintf("Invalid base URL: %s", err.Error()))
		}
		apiURL = strings.TrimSuffix(normalizedBaseURL, "/") + "/v1/messages?beta=true"
	} else {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Unsupported provider type: %s", value.Type))
	}

	c.Begin(true)

	payload, err := claude.TestPayloadWithPrompt(testModelID, prompt)
	if err != nil {
		return (TestStreamOutput{}).Error(c, "Failed to create test payload")
	}
	if err := s.applyModelAttributes(ctx, value, c.GroupID, testModelID, payload); err != nil {
		return (TestStreamOutput{}).Error(c, err.Error())
	}
	// The probe builder returns JSON-compatible scalar and collection values.
	// Sampling removal retains that shape, so encoding cannot encounter an
	// unsupported value type here.
	payloadBytes, _ := json.Marshal(payload)

	(TestStreamOutput{}).SendEvent(c, providercore.TestEvent{Type: "test_start", Model: testModelID})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return (TestStreamOutput{}).Error(c, "Failed to create request")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")

	for key, value := range claude.DefaultHeaders {
		req.Header.Set(key, value)
	}

	if value.IsOAuth() {
		req.Header.Set("anthropic-beta", claude.DefaultBetaHeader)
		req.Header.Set("Authorization", "Bearer "+authToken)
	} else {
		req.Header.Set("anthropic-beta", claude.APIKeyBetaHeader)
		claude.SetAPIKeyAuthHeader(req.Header, value.GetAnthropicAPIKeyAuthScheme() == providercore.AnthropicAPIKeyAuthSchemeAuthorizationBearer, authToken)
	}
	s.applyUserAgent(req)

	// Apply provider header overrides after defaults and authentication, as forwarding does.
	ApplyProviderHeaderOverrides(value, req.Header)

	proxyURL := ""
	if value.ProxyID != nil && value.Proxy != nil {
		proxyURL = value.Proxy.URL()
	}

	resp, err := s.Transport.DoWithTLS(req, proxyURL, value.ID, value.Concurrency, s.resolveTLSProfile(value))
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// Keep the available non-OK body for endpoint diagnostics even when reading
		// fails; this path reports the upstream status as its probe failure.
		body, _ := io.ReadAll(resp.Body)
		errMsg := fmt.Sprintf("API returned %d: %s", resp.StatusCode, string(body))

		if resp.StatusCode == http.StatusForbidden {
			// The forbidden reply is already the probe failure. Attempt to retain
			// provider error state, but return that reply even when its storage update
			// fails.
			_ = s.Store.SetError(ctx, value.ID, errMsg)
		}

		return (TestStreamOutput{}).Error(c, errMsg)
	}

	return (TestStreamOutput{}).Anthropic(c, resp.Body)
}

// executeVertex sends a streaming Anthropic probe through Vertex using
// service-account authentication. Explicit provider mapping wins; an unmapped
// test model uses Vertex's normalized model ID before sampling resolution and
// resource URL construction.
//
// The model resides in the resource URL, so body conversion removes its
// duplicate field and sets Vertex's Anthropic version. Tokens supplies the
// access token, and proxy and request TLS choices come from the record. The
// method closes its response and attempts provider error persistence for
// forbidden replies before emitting the probe failure.
func (s *AnthropicProviderTest) executeVertex(c *TestRun, ctx context.Context, value *providercore.Record, testModelID string, prompt string) error {
	if mappedModel, matched := providercore.ResolveMappedModel(providercore.ResolveModelMapping(value, ModelDefaults()), testModelID); matched {
		testModelID = mappedModel
	} else {
		// An unmapped Claude ID needs Vertex's service spelling for its resource URL.
		// Explicit administrator targets have already won and bypass this
		// normalization.
		testModelID = vertex.NormalizeVertexAnthropicModelID(testModelID)
	}

	c.Begin(true)

	payload, err := claude.TestPayloadWithPrompt(testModelID, prompt)
	if err != nil {
		return (TestStreamOutput{}).Error(c, "Failed to create test payload")
	}
	if err := s.applyModelAttributes(ctx, value, c.GroupID, testModelID, payload); err != nil {
		return (TestStreamOutput{}).Error(c, err.Error())
	}
	// The probe builder returns JSON-compatible scalar and collection values.
	// Sampling removal retains that shape, so encoding cannot encounter an
	// unsupported value type here.
	payloadBytes, _ := json.Marshal(payload)
	// Vertex carries the model in the resource URL. Convert the probe body to its
	// Anthropic version and remove the duplicate body model field before sending.
	vertexBody, err := vertex.BuildVertexAnthropicRequestBody(payloadBytes)
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Failed to create Vertex request body: %s", err.Error()))
	}

	if s.Tokens == nil {
		return (TestStreamOutput{}).Error(c, "Claude token provider not configured")
	}
	accessToken, err := s.Tokens.GetAccessToken(ctx, value)
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Failed to get service account access token: %s", err.Error()))
	}

	fullURL, err := vertex.BuildVertexAnthropicURL(value.VertexProjectID(vertex.ServiceAccountProjectID), value.VertexLocation(testModelID), testModelID, true)
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Failed to build Vertex URL: %s", err.Error()))
	}

	(TestStreamOutput{}).SendEvent(c, providercore.TestEvent{Type: "test_start", Model: testModelID})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, bytes.NewReader(vertexBody))
	if err != nil {
		return (TestStreamOutput{}).Error(c, "Failed to create request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	s.applyUserAgent(req)

	proxyURL := ""
	if value.ProxyID != nil && value.Proxy != nil {
		proxyURL = value.Proxy.URL()
	}

	resp, err := s.Transport.DoWithTLS(req, proxyURL, value.ID, value.Concurrency, s.resolveTLSProfile(value))
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// Keep the available non-OK body for endpoint diagnostics even when reading
		// fails; this path reports the upstream status as its probe failure.
		body, _ := io.ReadAll(resp.Body)
		errMsg := fmt.Sprintf("API returned %d: %s", resp.StatusCode, string(body))
		if resp.StatusCode == http.StatusForbidden {
			// The forbidden reply is already the probe failure. Attempt to retain
			// provider error state, but return that reply even when its storage update
			// fails.
			_ = s.Store.SetError(ctx, value.ID, errMsg)
		}
		return (TestStreamOutput{}).Error(c, errMsg)
	}

	return (TestStreamOutput{}).Anthropic(c, resp.Body)
}

// ExecuteBedrock sends a nonstreaming probe using the resolved Bedrock model
// and source region. It applies provider mapping before route resolution, then
// authenticates with an API key or signs the exact request body with the
// record's AWS credentials.
//
// A blank prompt becomes hi, and service sampling rules can remove unsupported
// controls from the fixed payload. The upstream response is closed and its
// first text block becomes test content, with an empty-response label when
// absent. Test events retain the streaming UI format despite the nonstreaming
// invocation.
func (s *AnthropicProviderTest) ExecuteBedrock(c *TestRun, ctx context.Context, value *providercore.Record, testModelID string, prompt string) error {
	route, err := bedrock.ResolveBedrockModelRoute(&bedrock.RouteInput{Region: value.GetCredential("aws_region"), ForceGlobal: value.GetCredential("aws_force_global") == "true", Model: mappedTestModel(value, testModelID)}, testModelID)
	if err != nil {
		return (TestStreamOutput{}).Error(c, bedrock.BedrockRoutingDiagnostic(err))
	}
	region := route.SourceRegion
	testModelID = route.ModelID

	// The test sink consumes typed streaming events for every platform. Commit
	// output before the nonstreaming invocation so its final content uses that
	// same presentation path.
	c.Begin(true)

	// Bedrock's nonstreaming invoke payload uses its own Anthropic version and
	// carries the routed model in the URL, so the streaming probe fields are
	// omitted.
	testPrompt := strings.TrimSpace(prompt)
	if testPrompt == "" {
		testPrompt = "hi"
	}
	bedrockPayload := map[string]any{
		"anthropic_version": "bedrock-2023-05-31",
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{
						"type": "text",
						"text": testPrompt,
					},
				},
			},
		},
		// Bound the Bedrock probe output to 256 tokens.
		"max_tokens":  256,
		"temperature": 1,
	}
	if err := s.applyModelAttributes(ctx, value, c.GroupID, testModelID, bedrockPayload); err != nil {
		return (TestStreamOutput{}).Error(c, err.Error())
	}
	// The fixed Bedrock payload contains strings, numbers, and JSON collections.
	// Sampling removal retains those supported types before the body is signed or
	// authenticated.
	bedrockBody, _ := json.Marshal(bedrockPayload)

	apiURL := bedrock.BuildBedrockURL(region, testModelID, false)

	(TestStreamOutput{}).SendEvent(c, providercore.TestEvent{Type: "test_start", Model: testModelID})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(bedrockBody))
	if err != nil {
		return (TestStreamOutput{}).Error(c, "Failed to create request")
	}
	req.Header.Set("Content-Type", "application/json")

	if value.IsBedrockAPIKey() {
		apiKey := value.GetCredential("api_key")
		if apiKey == "" {
			return (TestStreamOutput{}).Error(c, "No API key available")
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
	} else {
		signer, err := NewBedrockSignerFromProvider(value)
		if err != nil {
			return (TestStreamOutput{}).Error(c, fmt.Sprintf("Failed to create Bedrock signer: %s", err.Error()))
		}
		if err := signer.SignRequest(ctx, req, bedrockBody); err != nil {
			return (TestStreamOutput{}).Error(c, fmt.Sprintf("Failed to sign request: %s", err.Error()))
		}
	}
	s.applyUserAgent(req)

	proxyURL := ""
	if value.ProxyID != nil && value.Proxy != nil {
		proxyURL = value.Proxy.URL()
	}

	resp, err := s.Transport.DoWithTLS(req, proxyURL, value.ID, value.Concurrency, nil)
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Request failed: %s", err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	// The available response prefix is parsed even if its read fails. Status
	// errors use that prefix for diagnostics; successful replies proceed through
	// JSON decoding.
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("API returned %d: %s", resp.StatusCode, string(body)))
	}

	// Convert the first nonstreaming content block into one test event. Additional
	// blocks remain outside this preview, and an absent first text receives the
	// empty-response label below.
	var result struct {
		// Content holds the nonstreaming response blocks in their supplied order.
		// ExecuteBedrock previews the first block and ignores later content.
		Content []struct {
			Text string `json:"text"` // content block text value
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Failed to parse response: %s", err.Error()))
	}

	text := ""
	if len(result.Content) > 0 {
		text = result.Content[0].Text
	}
	// A successful invocation can have no first text block. Supply a visible
	// placeholder while keeping the success event tied to the accepted HTTP
	// response.
	if text == "" {
		text = "(empty response)"
	}

	(TestStreamOutput{}).SendEvent(c, providercore.TestEvent{Type: "content", Text: text})
	(TestStreamOutput{}).SendEvent(c, providercore.TestEvent{Type: "test_complete", Success: true})
	return nil
}

// applyUserAgent writes the trimmed executor agent when it is nonblank. Blank
// configuration leaves the request's existing agent intact, and callers provide
// a request with writable headers.
func (s *AnthropicProviderTest) applyUserAgent(req *http.Request) {
	if agent := strings.TrimSpace(s.UserAgent); agent != "" {
		req.Header.Set("User-Agent", agent)
	}
}

// resolveTLSProfile resolves request fingerprinting from the record's enabled
// setting and direct profile ID. A nil Profiles service returns nil; callers
// supply a nonnil record when fingerprint selection is configured.
func (s *AnthropicProviderTest) resolveTLSProfile(value *providercore.Record) *tlsfingerprint.Profile {
	if s.Profiles == nil {
		return nil
	}
	return s.Profiles.ResolveRequestTLS(egress.TLSSelection{Enabled: value.IsTLSFingerprintEnabled(), DirectProfileID: value.GetTLSFingerprintProfileID()})
}

// mappedTestModel returns the result of one provider mapping for a probe model.
// Unmatched input is retained, and mapped targets are returned directly so
// alias chains are not applied repeatedly during preparation.
func mappedTestModel(value *providercore.Record, model string) string {
	mapped, _ := providercore.ResolveMappedModel(providercore.ResolveModelMapping(value, ModelDefaults()), model)
	return mapped
}

// applyModelAttributes removes sampling fields whose resolved service
// capability is false from the mutable payload. It uses the selected Anthropic
// model and group; absent capability flags leave existing temperature and
// top_p fields intact. Routed probes also validate text output on their selected
// endpoint. Profile lookup errors propagate before payload changes.
func (s *AnthropicProviderTest) applyModelAttributes(ctx context.Context, value *providercore.Record, groupID int64, model string, payload map[string]any) error {
	if providercore.TestProtocolRouteFromContext(ctx).ClientProtocol != "" {
		if _, err := resolveProbeType(ctx, s.Attributes, value, groupID, model, "anthropic_messages", providercore.ProviderTestTypeText); err != nil {
			return err
		}
	}
	resolved := modelrules.View{}.Resolve(value.ModelAttributeTarget(model, "anthropic_messages"), modelcatalog.Attributes{})
	if s.Attributes != nil {
		var err error
		resolved, err = s.Attributes(ctx, value, groupID, model, "anthropic_messages")
		if err != nil {
			return err
		}
	}
	resolved = resolved.ForRequest(modelrules.RequestState{})
	// The selected service model supplies these sampling permissions. Remove
	// explicitly disallowed fields while unknown flags retain the probe builder's
	// defaults for that target.
	if resolved.Attributes.Temperature != nil && !*resolved.Attributes.Temperature {
		delete(payload, "temperature")
	}
	if resolved.Attributes.TopP != nil && !*resolved.Attributes.TopP {
		delete(payload, "top_p")
	}
	return nil
}
