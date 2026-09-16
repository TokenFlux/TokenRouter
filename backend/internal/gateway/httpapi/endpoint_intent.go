package httpapi

import (
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
)

// IsBatchImageManagementRequest 标识不会创建新生成任务的批任务管理请求。
func IsBatchImageManagementRequest(method, path string) bool {
	path = strings.TrimRight(path, "/")
	const root = "/v1/images/batches"
	if method == http.MethodGet && (path == root || strings.HasPrefix(path, root+"/")) {
		return true
	}
	if method == http.MethodDelete && strings.HasPrefix(path, root+"/") {
		return true
	}
	return method == http.MethodPost && strings.HasPrefix(path, root+"/") && strings.HasSuffix(path, "/cancel")
}

// IsBatchImageBillingBypassRequest 排除模型列表，仅让既有批任务的查询和管理绕过计费。
func IsBatchImageBillingBypassRequest(method, path string) bool {
	path = strings.TrimRight(path, "/")
	return path != "/v1/images/batches/models" && IsBatchImageManagementRequest(method, path)
}

// IsAPIKeyNonConsumingRequest 标识只读取已有状态或释放资源的网关请求。
// 未知路径默认视为会产生消费，避免新增路由自动绕过团队限额。
func IsAPIKeyNonConsumingRequest(method, path string) bool {
	path = strings.TrimRight(path, "/")
	if IsAPIKeyUsageRequest(method, path) {
		return true
	}
	if method == http.MethodGet {
		if strings.HasSuffix(path, "/models") || IsBatchImageManagementRequest(method, path) || IsGrokVideoTaskRead(method, path) {
			return true
		}
	}
	if IsBatchImageManagementRequest(method, path) {
		return true
	}
	if method == http.MethodPost && strings.HasSuffix(path, "/messages/count_tokens") {
		return true
	}
	return false
}

// IsAPIKeyUsageRequest 统一识别两套 Claude 风格的 Key 用量查询入口。
// 这类接口只读取 Key 自身状态，不能因为订阅或 Key 配额已耗尽而被消费准入拦截。
func IsAPIKeyUsageRequest(method, path string) bool {
	if method != http.MethodGet {
		return false
	}
	switch strings.TrimRight(path, "/") {
	case "/v1/usage", "/antigravity/v1/usage":
		return true
	default:
		return false
	}
}

// ShouldResolveAPIKeyBillingInSimpleMode 为不执行资金预检的简易模式保留必要的权益上下文。
// 严格指定订阅的复合 Key 必须据此过滤模型列表，避免在简易模式泄露套餐外映射。
func ShouldResolveAPIKeyBillingInSimpleMode(apiKey *apikey.APIKey, method, path string) bool {
	if IsAPIKeyUsageRequest(method, path) {
		return true
	}
	return apiKey != nil &&
		apiKey.IsComposite &&
		apikey.APIKeyEffectiveBillingMode(apiKey) == apikey.APIKeyBillingModeSubscription &&
		IsCompositeKeyModelListEndpoint(method, path)
}
