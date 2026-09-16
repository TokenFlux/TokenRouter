package forward

import (
	"context"
	"encoding/json"
)

// ConversionInput 固化本次平台类型，凭据由受控端口按原时机取得。
type ConversionInput struct{ OAuth bool }

// ErrorDecision 是健康策略的决定投影，不把账号实体传入核心。
type ErrorDecision struct{ Generic, Failover, RetrySameAccount bool }

// ConversionPorts 将网络和账号能力限制为单步操作；转换和失败顺序由核心拥有。
type ConversionPorts interface {
	NormalizeResponses([]byte) ([]byte, bool, error)
	ResolveModel(context.Context, string) string
	Effort([]byte, bool, ...string) *string
	ThinkingFallback(*string, []byte, string) *string
	ModelNotice(string, string, string, bool)
	Mimic(context.Context, []byte, json.RawMessage, string) []byte
	CacheLimit([]byte) []byte
	Credential(context.Context) error
	Build(context.Context, []byte, string, bool, bool) ([]byte, error)
	Send(context.Context) (Response, error)
	ReadErrorBody() ([]byte, error)
	ErrorMessage([]byte) string
	Health(context.Context, int, []byte, string) ErrorDecision
	FailoverNotice(int, string)
	FailoverError(int, []byte, bool) error
	Output() Output
}

// MapStatus 保留上游五百类状态对客户端的映射。
func MapStatus(status int) int {
	if status >= 500 {
		return 502
	}
	return status
}
