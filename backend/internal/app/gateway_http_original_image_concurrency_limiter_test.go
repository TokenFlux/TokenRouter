package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"

	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	httptestkit "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/testkit"
	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/config"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"

	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

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
		Source:  &service.OpenAIGatewayService{},
		Funding: &admission.FundingAdmission{},
		Keys:    &apikey.APIKeyService{},
		Concurrency: gatewayhttp.NewConcurrencyHelper(scheduler.NewConcurrencyService(&httptestkit.ConcurrencySequence{UserSeq: []bool{true}}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,
			Event: logging.Event},
		), gatewayhttp.SSEPingFormatNone, 0),
		Rules: nil,
		Config: &config.Config{Gateway: config.GatewayConfig{ImageConcurrency: config.ImageConcurrencyConfig{
			Enabled:               true,
			MaxConcurrentRequests: 1,
			OverflowMode:          config.ImageConcurrencyOverflowModeReject,
		}}},
		Images: &scheduler.ImageConcurrencyLimiter{}, Availability: newExecutionAvailabilityForTest(nil, nil, nil),
	})
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
		Source:  &service.OpenAIGatewayService{},
		Funding: newFundingAdmissionFixture(newBillingEligibilityFixture(&config.Config{RunMode: config.RunModeSimple}), &config.Config{RunMode: config.RunModeSimple}),
		Keys:    &apikey.APIKeyService{},
		Concurrency: gatewayhttp.NewConcurrencyHelper(scheduler.NewConcurrencyService(&httptestkit.ConcurrencySequence{UserSeq: []bool{true}}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,
			Event: logging.Event},
		), gatewayhttp.SSEPingFormatNone, 0),
		Config: &config.Config{Gateway: config.GatewayConfig{ImageConcurrency: config.ImageConcurrencyConfig{
			Enabled:               true,
			MaxConcurrentRequests: 1,
			OverflowMode:          config.ImageConcurrencyOverflowModeReject,
		}}},
		Images: &scheduler.ImageConcurrencyLimiter{}, Availability: newExecutionAvailabilityForTest(nil, nil, nil),
	})
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
