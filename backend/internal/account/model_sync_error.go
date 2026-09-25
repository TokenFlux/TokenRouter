// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"strings"
)

// UpstreamModelSyncErrorKind 对模型同步失败类型做分类，便于安全映射到 HTTP 状态码。
type UpstreamModelSyncErrorKind string

const (
	// UpstreamModelSyncErrorConfiguration 表示账号或服务端配置不足，无法执行同步。
	UpstreamModelSyncErrorConfiguration UpstreamModelSyncErrorKind = "configuration"
	// UpstreamModelSyncErrorUnsupported 表示该账号形态暂不支持实时模型同步。
	UpstreamModelSyncErrorUnsupported UpstreamModelSyncErrorKind = "unsupported"
	// UpstreamModelSyncErrorUpstream 表示已配置的上游失败或返回不可用响应。
	UpstreamModelSyncErrorUpstream UpstreamModelSyncErrorKind = "upstream"
)

// UpstreamModelSyncError 包装内部失败细节，同时提供可安全返回给客户端的消息。
type UpstreamModelSyncError struct {
	Kind    UpstreamModelSyncErrorKind
	Message string
	Err     error
}

func (e *UpstreamModelSyncError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return e.Message
	}
	return e.Message + ": " + e.Err.Error()
}

func (e *UpstreamModelSyncError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// SafeMessage 返回可发送给 API 客户端的脱敏消息。
func (e *UpstreamModelSyncError) SafeMessage() string {
	if e == nil || strings.TrimSpace(e.Message) == "" {
		return "Failed to sync upstream models"
	}
	return e.Message
}
