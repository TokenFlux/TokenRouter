package middleware

import (
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	"github.com/gin-gonic/gin"
)

// 历史测试函数只委托原生网关。
func resolveCompositeAPIKeyRequest(c *gin.Context, keys *apikey.APIKeyService, key *apikey.APIKey) (*apikey.APIKey, error) {
	if key == nil || !key.IsComposite {
		return key, nil
	}
	value, err := gatewayhttp.ResolveCompositeAPIKeyRequest(c, keys, apikey.CopyAPIKey(key))
	return apikey.CopyAPIKey(value), err
}

func replaceCompositeResponseModel(data []byte, actualModel, clientModel string) []byte {
	return gatewayhttp.ReplaceCompositeResponseModel(data, actualModel, clientModel)
}

func isCompositeKeyBillingBypassEndpoint(method, path string) bool {
	return gatewayhttp.IsCompositeKeyBillingBypassEndpoint(method, path)
}

func isCompositeKeyUnsupportedEndpoint(method, path, routePath string) bool {
	return gatewayhttp.IsCompositeKeyUnsupportedEndpoint(method, path, routePath)
}

func abortCompositeKeyError(c *gin.Context, err error) { gatewayhttp.AbortCompositeKeyError(c, err) }
