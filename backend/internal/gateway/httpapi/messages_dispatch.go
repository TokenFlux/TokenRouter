package httpapi

import (
	"context"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
)

func ResolveOpenAIMessagesDispatchMappedModel(apiKey *apikey.APIKey, requestedModel string) string {
	if apiKey == nil || apiKey.Group == nil {
		return ""
	}
	return strings.TrimSpace(gatewayprovider.ResolveMessagesDispatchModel(apiKey.Group, requestedModel))
}

// ResolveOpenAIMessagesAccountLayerModel 在分组映射 C 之后执行分组映射 D，并保留协议模型规范化。
func ResolveOpenAIMessagesAccountLayerModel(apiKey *apikey.APIKey, groupMappedModel string) string {
	groupMappedModel = strings.TrimSpace(groupMappedModel)
	if mappedModel := ResolveOpenAIMessagesDispatchMappedModel(apiKey, groupMappedModel); mappedModel != "" {
		return mappedModel
	}
	return gatewayprovider.NormalizeOpenAICompatRequestedModel(groupMappedModel)
}

// ResolveOpenAIMessagesAccountLayerModelForRequest 登记分组派发后的模型，供响应恢复与映射链记录使用。
func ResolveOpenAIMessagesAccountLayerModelForRequest(ctx context.Context, apiKey *apikey.APIKey, groupMappedModel string) string {
	model := ResolveOpenAIMessagesAccountLayerModel(apiKey, groupMappedModel)
	modeltrace.RegisterStage(ctx, model)
	return model
}
