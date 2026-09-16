// 旧网关中间件只委托 gateway/httpapi，后续随入口清零删除。
package middleware

import (
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
)

const compositeKeyNoGroupContextKey = gatewayhttp.CompositeKeyNoGroupContextKey

func resolveCompositeAPIKeyRequest(c *gin.Context, keys *service.APIKeyService, key *service.APIKey) (*service.APIKey, error) {
	if key == nil || !key.IsComposite {
		return key, nil
	}
	value, err := gatewayhttp.ResolveCompositeAPIKeyRequest(c, keys.APIKeyService, service.APIKeyView(key))
	return service.APIKeyFromView(value), err
}
func SetCompositeModelContext(c *gin.Context, clientModel, actualModel string) {
	gatewayhttp.SetCompositeModelContext(c, clientModel, actualModel)
}
func replaceCompositeResponseModel(data []byte, actualModel, clientModel string) []byte {
	return gatewayhttp.ReplaceCompositeResponseModel(data, actualModel, clientModel)
}
func GetCompositeModelFromContext(c *gin.Context) (clientModel, actualModel string, ok bool) {
	return gatewayhttp.GetCompositeModelFromContext(c)
}

func isCompositeKeyBillingBypassEndpoint(method, path string) bool {
	return gatewayhttp.IsCompositeKeyBillingBypassEndpoint(method, path)
}

func isCompositeKeyUnsupportedEndpoint(method, path, routePath string) bool {
	return gatewayhttp.IsCompositeKeyUnsupportedEndpoint(method, path, routePath)
}

func abortCompositeKeyError(c *gin.Context, err error) { gatewayhttp.AbortCompositeKeyError(c, err) }
