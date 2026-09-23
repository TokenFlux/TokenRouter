//go:build unit

package app

import (
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
)

// 夹具只转换原 HTTP 结果，不复制展示或回退规则。
func modelHTTPResponse(value *gemini.HTTPResult) *gatewayhttp.ModelHTTPResponse {
	if value == nil {
		return nil
	}
	return &gatewayhttp.ModelHTTPResponse{StatusCode: value.StatusCode, Headers: value.Headers, Body: value.Body}
}
func customGeminiModelsList(group *routing.Group) (gemini.ModelsListResponse, bool) {
	value, ok := gatewayhttp.NewModelsHandler(nil, gatewayprovider.ModelDisplayCatalogue{}).CustomGeminiModelsList(apikey.GroupFromRouting(group))
	var out []gemini.Model
	if value.Models != nil {
		out = make([]gemini.Model, len(value.Models))
	}
	for i, m := range value.Models {
		out[i] = gemini.Model(m)
	}
	return gemini.ModelsListResponse{Models: out}, ok
}
func shouldFallbackGeminiModel(modelName string, res *gemini.HTTPResult) bool {
	return gatewayhttp.NewModelsHandler(nil, gatewayprovider.ModelDisplayCatalogue{}).ShouldFallbackGeminiModel(modelName, modelHTTPResponse(res))
}
