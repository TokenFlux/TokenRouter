package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	apikeytestkit "github.com/TokenFlux/TokenRouter/internal/apikey/testkit"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	httptestkit "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/testkit"
	gatewaymedia "github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	catalogtestkit "github.com/TokenFlux/TokenRouter/internal/modelcatalog/testkit"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/routing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// Image permission and concurrency checks interpret the current request after
// group model mapping. These scenarios distinguish passive tool declarations
// from image intent, exercise provider exhaustion with safe diagnostics, and
// check early unavailable or shutdown rejection across media and auxiliary
// entry methods.

// openAIImagesFailoverProviderRepo supplies platform-filtered candidates for
// image failover scenarios.
type openAIImagesFailoverProviderRepo struct {
	// ExecutionProviderStore supplies unused methods; this fixture implements
	// the candidate and ID reads consumed by image provider selection in this scenario.
	gatewayprovider.ExecutionProviderStore

	providers []gatewayprovider.ExecutionProvider // configured execution candidates
}

// openAIImagesFailoverHTTPUpstream records attempted providers and returns
// image stream errors.
type openAIImagesFailoverHTTPUpstream struct {
	// mu protects providerIDs during transport recording and calls snapshots.
	// The snapshot method copies the slice before unlocking so assertion readers can keep it independently.
	mu          sync.Mutex // provider recording lock
	providerIDs []int64    // attempted provider identities
}

// TestOpenAIGatewayHandlerResponses_ImageIntentRejectedByImageConcurrency fills
// the only image slot before a Responses image request. It checks
// TooManyRequests, rate_limit_error, and the image concurrency rejection
// message.
func TestOpenAIGatewayHandlerResponses_ImageIntentRejectedByImageConcurrency(t *testing.T) {
	body := `{"model":"gpt-5.4","input":"draw","tools":[{"type":"image_generation"}]}`
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	groupID := int64(1)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		ID:      10,
		GroupID: &groupID,
		Group: &routing.Group{
			ID:                   groupID,
			AllowImageGeneration: true,
		},
		User: &identity.User{ID: 20},
	})
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 20, Concurrency: 1})

	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{
		Source:  &gatewayExecutionFixture{},
		Funding: &admission.FundingAdmission{},
		Keys:    &apikey.APIKeyService{},
		Concurrency: gatewayhttp.NewConcurrencyHelper(scheduler.NewConcurrencyService(&httptestkit.ConcurrencySequence{UserSeq: []bool{true}}, scheduler.Diagnostics{
			Logf:  logging.LegacyPrintf,
			Event: logging.Event,
		},
		), gatewayhttp.SSEPingFormatNone, 0),
		Rules: nil,
		Config: &config.Config{Gateway: config.GatewayConfig{Images: config.GatewayImagesConfig{Concurrency: config.ImageConcurrencyConfig{
			Enabled:               true,
			MaxConcurrentRequests: 1,
			OverflowMode:          config.ImageConcurrencyOverflowModeReject,
		}}}},
		Images: &scheduler.ImageConcurrencyLimiter{}, Availability: newExecutionAvailabilityForTest(nil, nil, nil), Choices: newEmptyCompatibleSelectionFixture(),
	})
	// Occupy the sole image slot before invoking Responses so the next image
	// admission observes saturation; deferred release returns this fixture reservation afterward.
	release, acquired := h.httpResources().AcquireImage(c, false)
	require.True(t, acquired)
	require.NotNil(t, release)
	defer release()
	rec.Body.Reset()
	rec.Code = 0

	h.Responses(c)

	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.Equal(t, "rate_limit_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
	require.Contains(t, rec.Body.String(), "Image generation concurrency limit exceeded")
}

// TestOpenAIGatewayHandlerResponses_TextOnlyNotRejectedByImageConcurrency fills
// the image slot before a text-only Responses request. It checks the request
// avoids the image concurrency status and message, without asserting a
// successful upstream result.
func TestOpenAIGatewayHandlerResponses_TextOnlyNotRejectedByImageConcurrency(t *testing.T) {
	body := `{"model":"gpt-5.4","input":"write code"}`
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	groupID := int64(1)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		ID:      10,
		GroupID: &groupID,
		Group: &routing.Group{
			ID:                   groupID,
			AllowImageGeneration: true,
		},
		User: &identity.User{ID: 20},
	})
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 20, Concurrency: 1})

	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{
		Source:  &gatewayExecutionFixture{},
		Funding: newFundingAdmissionFixture(newBillingEligibilityFixture(&config.Config{}), &config.Config{}),
		Keys:    &apikey.APIKeyService{},
		Concurrency: gatewayhttp.NewConcurrencyHelper(scheduler.NewConcurrencyService(&httptestkit.ConcurrencySequence{UserSeq: []bool{true}}, scheduler.Diagnostics{
			Logf:  logging.LegacyPrintf,
			Event: logging.Event,
		},
		), gatewayhttp.SSEPingFormatNone, 0),
		Config: &config.Config{Gateway: config.GatewayConfig{Images: config.GatewayImagesConfig{Concurrency: config.ImageConcurrencyConfig{
			Enabled:               true,
			MaxConcurrentRequests: 1,
			OverflowMode:          config.ImageConcurrencyOverflowModeReject,
		}}}},
		Images: &scheduler.ImageConcurrencyLimiter{}, Availability: newExecutionAvailabilityForTest(nil, nil, nil), Choices: newEmptyCompatibleSelectionFixture(),
	})
	// Occupy the sole image slot before invoking Responses so the next image
	// admission observes saturation; deferred release returns this fixture reservation afterward.
	release, acquired := h.httpResources().AcquireImage(c, false)
	require.True(t, acquired)
	require.NotNil(t, release)
	defer release()
	rec.Body.Reset()
	rec.Code = 0

	h.Responses(c)

	require.NotEqual(t, http.StatusTooManyRequests, rec.Code)
	require.NotContains(t, rec.Body.String(), "Image generation concurrency limit exceeded")
}

// TestOpenAIGatewayHandlerResponses_GrokPassiveImageToolDeclarationBypassesPermissionGate
// declares a passive Grok namespace with automatic tool choice. It checks the
// text request avoids Forbidden and the image permission message in an
// image-disabled group.
func TestOpenAIGatewayHandlerResponses_GrokPassiveImageToolDeclarationBypassesPermissionGate(t *testing.T) {
	body := `{"model":"grok-4.5","tools":[{"type":"namespace","name":"image_gen","tools":[{"type":"function","name":"imagegen"}]}],"tool_choice":"auto","input":"write code"}`
	rec := runOpenAIResponsesImagePermissionGateTest(t, capability.PlatformGrok, body)

	require.NotEqual(t, http.StatusForbidden, rec.Code)
	require.NotContains(t, rec.Body.String(), gatewaymedia.ImageGenerationPermissionMessage)
}

// TestOpenAIGatewayHandlerResponses_GrokResponsesLiteImageToolDeclarationBypassesPermissionGate
// places a passive Grok namespace inside Responses additional_tools input. It
// checks this text request avoids image permission rejection under automatic
// tool choice.
func TestOpenAIGatewayHandlerResponses_GrokResponsesLiteImageToolDeclarationBypassesPermissionGate(t *testing.T) {
	body := `{"model":"grok-4.5","tool_choice":"auto","input":[{"type":"additional_tools","tools":[{"type":"namespace","name":"image_gen","tools":[{"type":"function","name":"imagegen"}]}]},{"type":"message","role":"user","content":"write code"}]}`
	rec := runOpenAIResponsesImagePermissionGateTest(t, capability.PlatformGrok, body)

	require.NotEqual(t, http.StatusForbidden, rec.Code)
	require.NotContains(t, rec.Body.String(), gatewaymedia.ImageGenerationPermissionMessage)
}

// TestOpenAIGatewayHandlerResponses_ImagePermissionHardSignalsStillRejected
// supplies explicit image tools, tool choice, and an image model. Grok and
// OpenAI cases must return Forbidden with the shared image permission message.
func TestOpenAIGatewayHandlerResponses_ImagePermissionHardSignalsStillRejected(t *testing.T) {
	tests := []struct {
		name     string // permission scenario label
		platform string // unused scenario platform label
		body     string // image intent request
	}{
		{
			name:     "Grok native image_generation declaration",
			platform: capability.PlatformGrok,
			body:     `{"model":"grok-4.5","tools":[{"type":"image_generation"}],"input":"draw"}`,
		},
		{
			name:     "Grok explicit image_gen tool choice",
			platform: capability.PlatformGrok,
			body:     `{"model":"grok-4.5","tools":[{"type":"namespace","name":"image_gen"}],"tool_choice":{"type":"namespace","name":"image_gen"},"input":"draw"}`,
		},
		{
			name:     "OpenAI native image_generation tool",
			platform: capability.PlatformOpenAI,
			body:     `{"model":"gpt-5.5","tools":[{"type":"image_generation","model":"gpt-image-2"}],"input":"draw a cat"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := runOpenAIResponsesImagePermissionGateTest(t, tt.platform, tt.body)

			require.Equal(t, http.StatusForbidden, rec.Code)
			require.Contains(t, rec.Body.String(), gatewaymedia.ImageGenerationPermissionMessage)
		})
	}
}

// TestOpenAIGatewayHandlerResponses_PassiveNamespaceDoesNotTrigger403 declares
// an OpenAI image namespace with automatic choice for text input. It checks
// that passive declaration does not itself produce Forbidden in an
// image-disabled group.
func TestOpenAIGatewayHandlerResponses_PassiveNamespaceDoesNotTrigger403(t *testing.T) {
	passiveNamespace := `{"model":"gpt-5.5","tools":[{"type":"namespace","name":"image_gen","tools":[{"type":"function","name":"imagegen"}]}],"tool_choice":"auto","input":"write code"}`
	rec := runOpenAIResponsesImagePermissionGateTest(t, capability.PlatformOpenAI, passiveNamespace)

	require.NotEqual(t, http.StatusForbidden, rec.Code,
		"passive image_gen namespace with tool_choice=auto should not trigger 403 (#4447)")
}

// runOpenAIResponsesImagePermissionGateTest serves Responses with image
// permission disabled and supplied body. It assembles stub admission and empty
// selection, then returns the recorder so callers can inspect permission gating
// independently of later dependency failure. The body drives the case;
// platform remains an unused scenario label.
func runOpenAIResponsesImagePermissionGateTest(t *testing.T, platform string, body string) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	groupID := int64(6301)
	userID := int64(6302)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		ID:      6303,
		GroupID: &groupID,
		Group: &routing.Group{
			ID:                   groupID,
			AllowImageGeneration: false,
		},
		User: &identity.User{ID: userID, Status: billing.StatusActive},
	})
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: userID, Concurrency: 1})

	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{
		Source:  &gatewayExecutionFixture{},
		Funding: newFundingAdmissionFixture(newBillingEligibilityFixture(&config.Config{}), &config.Config{}),
		Keys:    &apikey.APIKeyService{},
		Concurrency: gatewayhttp.NewConcurrencyHelper(scheduler.NewConcurrencyService(
			&httptestkit.ConcurrencySequence{UserSeq: []bool{true}}, scheduler.Diagnostics{
				Logf:  logging.LegacyPrintf,
				Event: logging.Event,
			},
		), gatewayhttp.SSEPingFormatNone, 0),
		Config: &config.Config{},
		Images: &scheduler.ImageConcurrencyLimiter{}, Availability: newExecutionAvailabilityForTest(nil, nil, nil), Choices: newEmptyCompatibleSelectionFixture(),
	})

	h.Responses(c)
	return rec
}

// TestOpenAIGatewayHandlerImages_DisabledGroupRejectsBeforeScheduling sends
// image generation through a group that disables images. It checks Forbidden,
// permission_error, and the shared permission message before later scheduling
// dependencies are usable.
func TestOpenAIGatewayHandlerImages_DisabledGroupRejectsBeforeScheduling(t *testing.T) {
	body := []byte(`{"model":"gpt-image-2","prompt":"draw","size":"1024x1024"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	groupID := int64(111)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		ID:      222,
		GroupID: &groupID,
		Group: &routing.Group{
			ID:                   groupID,
			AllowImageGeneration: false,
		},
		User: &identity.User{ID: 333},
	})
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 333, Concurrency: 1})

	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{
		Source:      &gatewayExecutionFixture{},
		Funding:     &admission.FundingAdmission{},
		Keys:        &apikey.APIKeyService{},
		Concurrency: gatewayhttp.NewConcurrencyHelper(&scheduler.ConcurrencyService{}, gatewayhttp.SSEPingFormatNone, 0), Availability: newExecutionAvailabilityForTest(nil, nil, nil), Choices: newEmptyCompatibleSelectionFixture(),
	})

	h.Images(c)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "permission_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
	require.Contains(t, rec.Body.String(), gatewaymedia.ImageGenerationPermissionMessage)
}

// TestOpenAIGatewayHandlerImagesValidatesGroupMappedModel submits an alias
// mapped to an image model with group image permission disabled. It checks the
// mapped request still receives the expected Forbidden status and permission
// message.
func TestOpenAIGatewayHandlerImagesValidatesGroupMappedModel(t *testing.T) {
	groupID := int64(112)
	pricingConfigService := newGatewayExecutionPricingConfigServiceForTest(groupID, capability.PlatformOpenAI, testkit.Configuration{
		ID:           112,
		Status:       billing.StatusActive,
		ModelMapping: map[string]string{"draw-alias": "gpt-image-1", "gpt-image-2": "gpt-5.4"},
	})

	tests := []struct {
		name       string // mapped permission scenario
		model      string // submitted model name
		allowImage bool   // group image permission
		wantStatus int    // expected HTTP status
		wantText   string // expected rejection message
	}{
		{name: "普通别名映射为生图模型", model: "draw-alias", wantStatus: http.StatusForbidden, wantText: gatewaymedia.ImageGenerationPermissionMessage},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(`{"model":"` + tt.model + `","prompt":"draw"}`)
			req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = req
			apiKey := &apikey.APIKey{
				ID:      223,
				GroupID: &groupID,
				Group: &routing.Group{
					ID:                   groupID,
					AllowImageGeneration: tt.allowImage,
				},
				User: &identity.User{ID: 334},
			}
			c.Set(string(keyhttp.ContextKeyAPIKey), apiKey)
			c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 334, Concurrency: 1})

			newOpenAIImageChatRejectionHandlerWithPricingConfig(t, pricingConfigService).Images(c)

			require.Equal(t, tt.wantStatus, rec.Code)
			require.Contains(t, gjson.GetBytes(rec.Body.Bytes(), "error.message").String(), tt.wantText)
		})
	}
}

// TestImagesGroupMappedTextModelIsRejectedByActualCandidate maps an image name
// to a text model allowed by the provider. Native image selection must still
// return an error and no selected candidate.
func TestImagesGroupMappedTextModelIsRejectedByActualCandidate(t *testing.T) {
	groupID := int64(112)
	policies := newGatewayExecutionPricingConfigServiceForTest(groupID, "openai", testkit.Configuration{ID: 112, Status: billing.StatusActive, ModelMapping: map[string]string{"gpt-image-2": "gpt-5.4"}})
	provider := gatewayprovider.NewExecutionProvider(&providercore.Record{ID: 1, Platform: "openai", Type: "apikey", Status: "active", Schedulable: true, GroupIDs: []int64{groupID}, Credentials: map[string]any{"model_whitelist": []string{"gpt-5.4"}}})
	store := &mixedHTTPProviders{values: []gatewayprovider.ExecutionProvider{*provider}}
	choices := selection.NewCompatible(selection.CompatibleDependencies{Reads: selection.Reads{Providers: store}, Shared: selection.Shared{GroupPolicies: policies}}, selection.DefaultOptions())
	ctx := requeststate.WithClientProtocol(requeststate.WithGroup(context.Background(), &routing.Group{ID: groupID, Hydrated: true, Status: "active", AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolImagesGenerations}}), protocol.ProtocolImagesGenerations)
	selected, _, err := choices.SelectProviderWithSchedulerForImages(ctx, &groupID, "", "gpt-image-2", nil, gatewaymedia.ImageCapabilityNative)
	require.Error(t, err)
	require.Nil(t, selected)
}

// GetByID returns an outer value copy of the matching provider. Nested
// references remain shared with the fixture records, and absent IDs return
// ErrNoAvailableProviders for selection fallback.
func (r openAIImagesFailoverProviderRepo) GetByID(_ context.Context, id int64) (*gatewayprovider.ExecutionProvider, error) {
	for i := range r.providers {
		if r.providers[i].Record.ID == id {
			provider := r.providers[i]
			return &provider, nil
		}
	}
	return nil, scheduler.ErrNoAvailableProviders
}

// ListSchedulableByGroupIDAndPlatform returns platform-filtered fixture
// providers and ignores group identity. Nested data remains shared because
// providersForPlatform copies values into a new outer result slice.
func (r openAIImagesFailoverProviderRepo) ListSchedulableByGroupIDAndPlatform(_ context.Context, _ int64, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	return r.providersForPlatform(platform), nil
}

// ListSchedulableByPlatform returns the fixture candidates filtered by their
// platform.
func (r openAIImagesFailoverProviderRepo) ListSchedulableByPlatform(_ context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	return r.providersForPlatform(platform), nil
}

// ListSchedulableUngroupedByPlatform returns platform-filtered fixture
// candidates without checking memberships. The fixture reuses the same provider
// list so image selection can exercise its ungrouped query path.
func (r openAIImagesFailoverProviderRepo) ListSchedulableUngroupedByPlatform(_ context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	return r.providersForPlatform(platform), nil
}

// providersForPlatform returns a new slice of matching outer provider values.
// Empty platform includes all records, and nested credentials or extras retain
// the references stored in the fixture.
func (r openAIImagesFailoverProviderRepo) providersForPlatform(platform string) []gatewayprovider.ExecutionProvider {
	out := make([]gatewayprovider.ExecutionProvider, 0, len(r.providers))
	for _, provider := range r.providers {
		if platform == "" || provider.Record.Platform == platform {
			out = append(out, provider)
		}
	}
	return out
}

// Do records the provider ID under the fixture mutex and returns an image error
// stream. The response uses HTTP OK with an SSE error body, letting production
// output classify the image backend failure.
func (u *openAIImagesFailoverHTTPUpstream) Do(_ *http.Request, _ string, providerID int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.providerIDs = append(u.providerIDs, providerID)
	u.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
			"X-Request-Id": []string{"req_img_failover"},
		},
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"type\":\"error\",\"error\":{\"type\":\"server_error\",\"code\":\"server_error\",\"message\":\"image backend unavailable\"}}\n\n",
		)),
	}, nil
}

// DoWithTLS forwards to the ordinary fixture exchange while ignoring TLS
// profile.
func (u *openAIImagesFailoverHTTPUpstream) DoWithTLS(req *http.Request, proxyURL string, providerID int64, providerConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, providerID, providerConcurrency)
}

// calls returns an independent copy of the recorded provider-ID slice. It locks
// the same mutex as Do so tests can inspect a consistent list without sharing
// mutable backing storage.
func (u *openAIImagesFailoverHTTPUpstream) calls() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.providerIDs...)
}

// TestOpenAIGatewayHandlerImages_ServerErrorFailsOverAndReturnsClearErrorWhenExhausted
// makes two providers return image SSE errors. It checks both attempts, final
// BadGateway presentation, failover observations, and image-quality/size
// diagnostics that omit the prompt.
func TestOpenAIGatewayHandlerImages_ServerErrorFailsOverAndReturnsClearErrorWhenExhausted(t *testing.T) {
	groupID := int64(3130)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 1,
				Name:        "image-provider-1",
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeOAuth,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 0,
				Priority:    0,
				Credentials: map[string]any{"access_token": "token-1"},
			},
		},
		{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 2,
				Name:        "image-provider-2",
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeOAuth,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 0,
				Priority:    1,
				Credentials: map[string]any{"access_token": "token-2"},
			},
		},
	}
	providerRepo := openAIImagesFailoverProviderRepo{providers: providers}
	upstream := &openAIImagesFailoverHTTPUpstream{}
	cfg := &config.Config{}
	gatewayService, gatewayServiceChoices, gatewayServiceCredentialPort := newOpenAIExecutionAndSelectionFixture(
		providerRepo,
		nil,
		cfg,
		nil,
		nil,

		nil,

		upstream,
		nil,
		nil, newOpenAIExecutionCredentialsForTest(providerRepo,

			nil), nil,
		nil,
		nil,

		nil,
		nil, responseHeaderFilterForTest(cfg), nil, nil, nil,
	)
	gatewayService.Recorder = newHTTPCompletionFixture(cfg, nil,

		nil,

		nil,

		nil,

		nil, nil, true)

	billingService := newBillingEligibilityFixture(cfg)
	billingService.Start()
	t.Cleanup(billingService.Stop)
	concurrencyService := scheduler.NewConcurrencyService(&fakeConcurrencyCache{}, scheduler.Diagnostics{
		Logf:  logging.LegacyPrintf,
		Event: logging.Event,
	},
	)
	handler := newGatewayHTTPEndpointsFromDeps(
		gatewayService, gatewayServiceCredentialPort,
		concurrencyService, newFundingAdmissionFixture(billingService, cfg), apikeytestkit.NewService(nil, nil, nil, nil, nil, nil, cfg),
		nil,
		nil,
		nil,
		nil,
		cfg, nil, newExecutionAvailabilityForTest(providerRepo,

			nil, cfg), gatewayServiceChoices,
	)
	handler.Input.MaxSwitches = 10

	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","quality":"high","size":"1536x1024"}`)
	core, observedLogs := observer.New(zap.DebugLevel)
	requestCtx := routing.WithModelAttributes(logging.IntoContext(context.Background(), zap.New(core)), &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{View: catalogtestkit.OfflineView}})
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body)).WithContext(requestCtx)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		ID:      99,
		GroupID: &groupID,
		Group: &routing.Group{
			ID:                   groupID,
			AllowImageGeneration: true,
		},
		User: &identity.User{ID: 100},
	})
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 100, Concurrency: 0})

	handler.Images(c)
	providerSelectingLogs := observedLogs.FilterMessage("openai.images.provider_selecting").All()
	require.NotEmpty(t, providerSelectingLogs)
	loggedFields := make(map[string]string)
	for _, field := range providerSelectingLogs[0].Context {
		loggedFields[field.Key] = field.String
	}
	require.Equal(t, "high", loggedFields["img_quality"])
	require.Equal(t, "1536x1024", loggedFields["img_size"])
	require.NotContains(t, loggedFields, "prompt")

	require.Equal(t, []int64{1, 2}, upstream.calls())
	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Equal(t, "upstream_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
	require.Equal(t, "Upstream service temporarily unavailable", gjson.GetBytes(rec.Body.Bytes(), "error.message").String())

	rawEvents, ok := c.Get(gatewayhttp.OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := rawEvents.([]*ops.OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 2)
	require.Equal(t, "failover", events[0].Kind)
	require.Equal(t, "failover", events[1].Kind)
}

// TestMediaAssemblyKeepsReadAndStopBoundaries invokes image and auxiliary
// handlers with missing execution before and after shared activity stops. It
// checks ServiceUnavailable, valid protocol JSON, the appropriate message, and
// no body read.
func TestMediaAssemblyKeepsReadAndStopBoundaries(t *testing.T) {
	activity := &gatewayRequestActivity{Operations: lifecycle.NewOperations("media-entry-contract")}
	common := provideOpenAIAttemptBindings(nil, nil, nil, nil, nil, nil, GatewayCompletionRecorders{}, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	runtime := provideMediaRuntime(nil, nil, nil, nil, nil, common, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	media := provideMediaHTTP(runtime, activity)
	auxiliary := provideAuxiliaryHTTP(runtime, activity)
	for _, stopped := range []bool{false, true} {
		if stopped {
			// Both handlers retain this shared barrier. Seal it before the stopped
			// iteration so each existing entry rejects before dependency checks and request-body reads.
			require.NoError(t, activity.StopContext(context.Background()))
		}
		for _, entry := range []struct {
			name string          // media entry label
			run  gin.HandlerFunc // assembled entry method
		}{
			{"images", media.Images},
			{"embeddings", auxiliary.Embeddings},
			{"systemone", auxiliary.SystemOne},
			{"alpha-search", auxiliary.AlphaSearch},
		} {
			t.Run(entry.name+map[bool]string{true: "-stopped", false: "-running"}[stopped], func(t *testing.T) {
				writer := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(writer)
				body := &protocolGateTrackingReader{}
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+entry.name, body)
				c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{ID: 9, UserID: 7, Group: &routing.Group{AllowImageGeneration: true}})
				c.Set(authctx.ContextKeyUser, authctx.AuthSubject{UserID: 7})
				entry.run(c)
				require.Equal(t, http.StatusServiceUnavailable, writer.Code)
				require.False(t, body.read)
				require.True(t, json.Valid(writer.Body.Bytes()), writer.Body.String())
				if stopped {
					require.Contains(t, writer.Body.String(), "Service is shutting down")
				} else {
					require.Contains(t, writer.Body.String(), "Service temporarily unavailable")
				}
			})
		}
	}
}
