package app

import (
	"context"

	"github.com/gin-gonic/gin"

	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/googleforward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	catalogprovider "github.com/TokenFlux/TokenRouter/internal/modelcatalog/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
)

// gateway_models.go constructs model-list HTTP handlers and adapts selected
// Gemini providers for model-resource reads.
// Model HTTP display combines requestable routing data with catalog metadata.
// Gemini resource reads use a selected provider and return its status, headers,
// and body, while catalog absence is represented as a nil reader interface for
// optional display enrichment.

// geminiModelReadTarget binds one selected Gemini provider to model-resource
// reads.
type geminiModelReadTarget struct {
	reader *googleforward.Gemini              // Gemini GET execution
	value  *gatewayprovider.ExecutionProvider // selected resource provider
}

// provideModelsHTTP constructs model display using optional catalog metadata
// and requestable routing. A nil catalog is passed as a nil interface, allowing
// modelsHTTP to leave the display source unset rather than retaining a typed
// nil pointer.
func provideModelsHTTP(catalog *catalogprovider.Service, catalogue *routing.RequestableCatalogue, reader *googleforward.Gemini, activity *gatewayRequestActivity, choices *selection.Gemini, attributes *routing.ModelAttributeService) *gatewayhttp.ModelsHandler {
	if catalog == nil {
		return modelsHTTP(nil, catalogue, reader, activity, choices, attributes)
	}
	return modelsHTTP(catalog, catalogue, reader, activity, choices, attributes)
}

// modelsHTTP binds Key, platform, billing, and Gemini selection reads for model
// handlers. Catalogue and catalog display are optional; activity is attached
// when supplied, while Gemini resource callbacks require their selected
// provider reader and choices service.
func modelsHTTP(catalog modelcatalog.Reader, catalogue *routing.RequestableCatalogue, reader *googleforward.Gemini, activity *gatewayRequestActivity, choices *selection.Gemini, attributes ...*routing.ModelAttributeService) *gatewayhttp.ModelsHandler {
	ports := gatewayhttp.ModelsPorts{
		ReadAccess: keyhttp.GetAPIKeyFromContext, ReadPlatform: keyhttp.GetForcePlatformFromContext,
		ReadBilling: func(c *gin.Context) (*billing.APIKeyBillingContext, bool) {
			return gatewayhttp.GetAPIKeyBillingContext(c)
		},
		SafeSegment: gemini.IsSafeGeminiModelPathSegment,
		SelectModel: func(ctx context.Context, id *int64) (gatewayhttp.GeminiModelReader, error) {
			value, err := choices.SelectProviderForAIStudioEndpoints(ctx, id)
			if err != nil {
				return nil, err
			}
			return geminiModelReadTarget{reader, value}, nil
		},
		CheckAntigravity: choices.HasAntigravityProviders,
	}
	if catalogue != nil {
		ports.Catalogue = catalogue
	}
	display := gatewayprovider.ModelDisplayCatalogue{}
	if catalog != nil {
		display.Source = catalog
	}
	result := gatewayhttp.NewModelsHandler(ports, display, attributes...)
	if activity != nil {
		result.BindRequestActivity(activity.Enter)
	}
	return result
}

// Read delegates an AI Studio GET using the selected provider. A nil platform
// response remains nil; available status, headers, and body are passed to HTTP
// together with any forwarding error.
func (p geminiModelReadTarget) Read(ctx context.Context, path string) (*gatewayhttp.ModelHTTPResponse, error) {
	value, err := p.reader.ForwardAIStudioGET(ctx, p.value, path)
	if value == nil {
		return nil, err
	}
	return &gatewayhttp.ModelHTTPResponse{StatusCode: value.StatusCode, Headers: value.Headers, Body: value.Body}, err
}

// resolveModelsListReadLimit returns configured positive model-response bytes
// or the default. Nil cfg and nonpositive values both select the configuration
// constant used for model-list response reads.
func resolveModelsListReadLimit(cfg *config.Config) int64 {
	if cfg != nil && cfg.Gateway.ModelsListResponseMaxBytes > 0 {
		return cfg.Gateway.ModelsListResponseMaxBytes
	}
	return config.DefaultModelsListReadMaxBytes
}
