package provider

import (
	"strings"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

func (e *openAIUpstreamWarningError) Error() string {
	if e == nil || e.err == nil {
		return "openai upstream warning"
	}
	return e.err.Error()
}

func (e *openAIUpstreamWarningError) OpenAIUpstreamWarning() *forwardcore.UpstreamWarning {
	if e == nil {
		return nil
	}
	return e.warning
}

func (e *openAIUpstreamWarningError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// ExtractOpenAICyberWarningMessage 提取可直接回传给下游客户端的 cyber 风控提示。
func ExtractOpenAICyberWarningMessage(responseBody []byte, warningText string) string {
	if hit, _, message := openai.DetectOpenAICyberPolicy(responseBody); hit && strings.TrimSpace(message) != "" {
		return logredact.TruncateLine([]byte(logredact.SanitizeUpstreamQueries(message)), 2048)
	}
	for _, candidate := range []string{
		strings.TrimSpace(warningText),
		strings.TrimSpace(moderation.ExtractCyberWarningText(responseBody)),
	} {
		if openai.IsOpenAICyberWarningText(candidate) {
			return logredact.TruncateLine([]byte(logredact.SanitizeUpstreamQueries(candidate)), 2048)
		}
	}
	if fallback := strings.TrimSpace(warningText); fallback != "" {
		return logredact.TruncateLine([]byte(logredact.SanitizeUpstreamQueries(fallback)), 2048)
	}
	if fallback := strings.TrimSpace(moderation.ExtractCyberWarningText(responseBody)); fallback != "" && !strings.HasPrefix(fallback, "{") {
		return logredact.TruncateLine([]byte(logredact.SanitizeUpstreamQueries(fallback)), 2048)
	}
	return "OpenAI rejected this request because it may violate cyber safety policy."
}

// IsOpenAICyberWarningPayload 判断上游响应体或错误文本是否属于 OpenAI cyber 风控拒绝。
func IsOpenAICyberWarningPayload(responseBody []byte, warningText string) bool {
	if openai.IsOpenAICyberWarningText(warningText) {
		return true
	}
	if len(responseBody) == 0 {
		return false
	}
	if hit, _, _ := openai.DetectOpenAICyberPolicy(responseBody); hit {
		return true
	}
	return openai.IsOpenAICyberWarningText(moderation.ExtractCyberWarningText(responseBody)) ||
		openai.IsOpenAICyberWarningText(string(responseBody))
}

func OpenAIUpstreamWarningIsCyber(warning *forwardcore.UpstreamWarning) bool {
	if warning == nil {
		return false
	}
	// 保持 WS 重试决策与 cyber 落库识别规则一致，避免重试覆盖可统计的上游风控拒绝。
	return IsOpenAICyberWarningPayload(warning.ResponseBody, warning.Message)
}

func WrapOpenAIUpstreamWarningIfCyber(statusCode int, responseBody []byte, message string, err error) error {
	if err == nil {
		return nil
	}
	warning := &forwardcore.UpstreamWarning{
		StatusCode:   statusCode,
		ResponseBody: append([]byte(nil), responseBody...),
		Message:      strings.TrimSpace(message),
	}
	if !OpenAIUpstreamWarningIsCyber(warning) {
		return err
	}
	return &openAIUpstreamWarningError{warning: warning, err: err}
}

type openAIUpstreamWarningError struct {
	warning *forwardcore.UpstreamWarning
	err     error
}

// 实际包装必须满足共享错误链契约。
var _ forwardcore.UpstreamWarningCarrier = (*openAIUpstreamWarningError)(nil)
