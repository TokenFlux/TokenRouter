// 本文件为阶段迁移兼容入口；剩余消费者和退出阶段见 refactor/S01-foundation.md。
package errors

import (
	foundation "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// UnknownCode 兼容旧入口，值由唯一实现提供。
const UnknownCode = foundation.UnknownCode

// UnknownReason 兼容旧入口，值由唯一实现提供。
const UnknownReason = foundation.UnknownReason

// UnknownMessage 兼容旧入口，值由唯一实现提供。
const UnknownMessage = foundation.UnknownMessage

// Status 保留旧调用方的类型身份；实现归目标包。
type Status = foundation.Status

// ApplicationError 保留旧调用方的类型身份；实现归目标包。
type ApplicationError = foundation.ApplicationError

// Error 保留旧调用方的类型身份；实现归目标包。
type Error = foundation.Error

// New 兼容旧入口；仅转发到目标实现。
func New(code int, reason, message string) *ApplicationError {
	return foundation.New(foundation.Category(code), reason, message)
}

// Newf 兼容旧入口；仅转发到目标实现。
func Newf(code int, reason, format string, a ...any) *ApplicationError {
	return foundation.Newf(foundation.Category(code), reason, format, a...)
}

// Errorf 兼容旧入口；仅转发到目标实现。
func Errorf(code int, reason, format string, a ...any) error {
	return foundation.Errorf(foundation.Category(code), reason, format, a...)
}

// Code 兼容旧入口；仅转发到目标实现。
func Code(err error) int {
	return httpx.ErrorCode(err)
}

// Reason 兼容旧入口；仅转发到目标实现。
func Reason(err error) string {
	return foundation.Reason(err)
}

// Message 兼容旧入口；仅转发到目标实现。
func Message(err error) string {
	return foundation.Message(err)
}

// Clone 兼容旧入口；仅转发到目标实现。
func Clone(err *ApplicationError) *ApplicationError {
	return foundation.Clone(err)
}

// FromError 兼容旧入口；仅转发到目标实现。
func FromError(err error) *ApplicationError {
	return foundation.FromError(err)
}
