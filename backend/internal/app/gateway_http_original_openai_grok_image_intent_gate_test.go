package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"

	httptestkit "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/testkit"
	gatewaymedia "github.com/TokenFlux/TokenRouter/internal/gateway/media"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIGatewayHandlerResponses_GrokPassiveImageToolDeclarationBypassesPermissionGate(t *testing.T) {
	body := `{"model":"grok-4.5","tools":[{"type":"namespace","name":"image_gen","tools":[{"type":"function","name":"imagegen"}]}],"tool_choice":"auto","input":"write code"}`
	rec := runOpenAIResponsesImagePermissionGateTest(t, capability.PlatformGrok, body)

	require.NotEqual(t, http.StatusForbidden, rec.Code)
	require.NotContains(t, rec.Body.String(), gatewaymedia.ImageGenerationPermissionMessage)
}

func TestOpenAIGatewayHandlerResponses_GrokResponsesLiteImageToolDeclarationBypassesPermissionGate(t *testing.T) {
	body := `{"model":"grok-4.5","tool_choice":"auto","input":[{"type":"additional_tools","tools":[{"type":"namespace","name":"image_gen","tools":[{"type":"function","name":"imagegen"}]}]},{"type":"message","role":"user","content":"write code"}]}`
	rec := runOpenAIResponsesImagePermissionGateTest(t, capability.PlatformGrok, body)

	require.NotEqual(t, http.StatusForbidden, rec.Code)
	require.NotContains(t, rec.Body.String(), gatewaymedia.ImageGenerationPermissionMessage)
}

func TestOpenAIGatewayHandlerResponses_ImagePermissionHardSignalsStillRejected(t *testing.T) {
	tests := []struct {
		name     string
		platform string
		body     string
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
		{
			name:     "OpenAI image model",
			platform: capability.PlatformOpenAI,
			body:     `{"model":"gpt-image-2","input":"draw a cat"}`,
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

func TestOpenAIGatewayHandlerResponses_PassiveNamespaceDoesNotTrigger403(t *testing.T) {
	passiveNamespace := `{"model":"gpt-5.5","tools":[{"type":"namespace","name":"image_gen","tools":[{"type":"function","name":"imagegen"}]}],"tool_choice":"auto","input":"write code"}`
	rec := runOpenAIResponsesImagePermissionGateTest(t, capability.PlatformOpenAI, passiveNamespace)

	require.NotEqual(t, http.StatusForbidden, rec.Code,
		"passive image_gen namespace with tool_choice=auto should not trigger 403 (#4447)")
}

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
			Platform:             platform,
			AllowImageGeneration: false,
		},
		User: &identity.User{ID: userID, Status: billing.StatusActive},
	})
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: userID, Concurrency: 1})

	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{
		Source:  &service.OpenAIGatewayService{},
		Funding: newFundingAdmissionFixture(newBillingEligibilityFixture(&config.Config{RunMode: config.RunModeSimple}), &config.Config{RunMode: config.RunModeSimple}),
		Keys:    &apikey.APIKeyService{},
		Concurrency: gatewayhttp.NewConcurrencyHelper(scheduler.NewConcurrencyService(
			&httptestkit.ConcurrencySequence{UserSeq: []bool{true}}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,
				Event: logging.Event},
		), gatewayhttp.SSEPingFormatNone, 0),
		Config: &config.Config{},
		Images: &scheduler.ImageConcurrencyLimiter{}, Availability: newExecutionAvailabilityForTest(nil, nil, nil),
	})

	h.Responses(c)
	return rec
}
