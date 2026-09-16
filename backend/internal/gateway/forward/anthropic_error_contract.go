package forward

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// ErrorInput 是当前错误响应的事实与配置投影，不包含连接或旧实体。
type ErrorInput struct {
	AccountID                                     int64
	AccountName, AccountType, Platform, RequestID string
	Status                                        int
	LogBody                                       bool
	LogBodyMaxBytes                               int
	RequestedModels                               []string
}

// ErrorPorts 把健康命令、规则查找和 HTTP 输出分开，核心决定调用顺序。
type ErrorPorts interface {
	ScheduleActivity()
	ReadBody() ([]byte, error)
	ResetBody([]byte)
	Health(context.Context, int, []string) ErrorDecision
	RetryHealth(context.Context, []string) ErrorDecision
	Failover(int, []byte, bool) error
	Commit()
	Message(int, string, string)
	Raw(int, []byte)
	MatchRule(string, int, []byte) *errorpolicy.ErrorPassthroughRule
	SkipMonitoring()
	ScopeDiagnostic(string, int, string)
	SetError(int, string, string)
	Observe(Notice)
	Log(string)
	Truncate(string, int) string
	TruncateBytes([]byte, int) string
	Sanitize(string) string
}

// applyAnthropicDisplayRule 复用唯一匹配服务，仅选择当前协议响应参数。
// 透传消息继续用原提取器，不扩大原始 body 暴露，也不改变监控/SLA 标记。
func applyAnthropicDisplayRule(p ErrorPorts, platform string, upstreamStatus int, body []byte, defaultStatus int, defaultType, defaultMessage string) (int, string, string, bool) {
	rule := p.MatchRule(platform, upstreamStatus, body)
	if rule == nil {
		return defaultStatus, defaultType, defaultMessage, false
	}
	status := upstreamStatus
	if !rule.PassthroughCode && rule.ResponseCode != nil {
		status = *rule.ResponseCode
	}
	message := upstream.ExtractErrorMessage(body)
	if !rule.PassthroughBody && rule.CustomMessage != nil {
		message = *rule.CustomMessage
	}
	if rule.SkipMonitoring {
		p.SkipMonitoring()
	}
	return status, "upstream_error", message, true
}
