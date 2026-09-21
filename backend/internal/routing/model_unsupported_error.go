package routing

import (
	"fmt"
	"strings"
)

const groupModelUnsupportedAvailableModelsLimit = 20

// GroupModelUnsupportedError 表示当前分组没有支持本次请求模型的可调度账号。
type GroupModelUnsupportedError struct {
	Platform        string
	RequestedModel  string
	AvailableModels []string
}

func (e *GroupModelUnsupportedError) Error() string {
	if e == nil {
		return ""
	}
	return buildGroupModelUnsupportedMessage(e.RequestedModel, truncateModelListForError(e.AvailableModels))
}

// buildGroupModelUnsupportedMessage 生成对外返回的英文错误文案。
func buildGroupModelUnsupportedMessage(requestedModel string, availableModels []string) string {
	requestedModel = strings.TrimSpace(requestedModel)
	message := fmt.Sprintf("The current group does not support the requested model %q", requestedModel)
	if len(availableModels) == 0 {
		return message
	}
	return fmt.Sprintf("%s. Available models: %s", message, strings.Join(availableModels, ", "))
}

// truncateModelListForError 限制错误文案中的模型数量，避免响应体过长。
func truncateModelListForError(models []string) []string {
	if len(models) <= groupModelUnsupportedAvailableModelsLimit {
		return append([]string(nil), models...)
	}
	out := append([]string(nil), models[:groupModelUnsupportedAvailableModelsLimit]...)
	out = append(out, fmt.Sprintf("and %d more", len(models)-groupModelUnsupportedAvailableModelsLimit))
	return out
}
