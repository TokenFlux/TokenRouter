// 本文件为阶段迁移兼容入口；剩余消费者和退出阶段见 refactor/S01-foundation.md。
package errors

import (
	foundation "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// BadRequest 兼容旧入口；仅转发到目标实现。
func BadRequest(reason, message string) *ApplicationError {
	return foundation.BadRequest(reason, message)
}

// IsBadRequest 兼容旧入口；仅转发到目标实现。
func IsBadRequest(err error) bool {
	return foundation.IsBadRequest(err)
}

// TooManyRequests 兼容旧入口；仅转发到目标实现。
func TooManyRequests(reason, message string) *ApplicationError {
	return foundation.TooManyRequests(reason, message)
}

// IsTooManyRequests 兼容旧入口；仅转发到目标实现。
func IsTooManyRequests(err error) bool {
	return foundation.IsTooManyRequests(err)
}

// Unauthorized 兼容旧入口；仅转发到目标实现。
func Unauthorized(reason, message string) *ApplicationError {
	return foundation.Unauthorized(reason, message)
}

// IsUnauthorized 兼容旧入口；仅转发到目标实现。
func IsUnauthorized(err error) bool {
	return foundation.IsUnauthorized(err)
}

// Forbidden 兼容旧入口；仅转发到目标实现。
func Forbidden(reason, message string) *ApplicationError {
	return foundation.Forbidden(reason, message)
}

// IsForbidden 兼容旧入口；仅转发到目标实现。
func IsForbidden(err error) bool {
	return foundation.IsForbidden(err)
}

// NotFound 兼容旧入口；仅转发到目标实现。
func NotFound(reason, message string) *ApplicationError {
	return foundation.NotFound(reason, message)
}

// IsNotFound 兼容旧入口；仅转发到目标实现。
func IsNotFound(err error) bool {
	return foundation.IsNotFound(err)
}

// Conflict 兼容旧入口；仅转发到目标实现。
func Conflict(reason, message string) *ApplicationError {
	return foundation.Conflict(reason, message)
}

// IsConflict 兼容旧入口；仅转发到目标实现。
func IsConflict(err error) bool {
	return foundation.IsConflict(err)
}

// InternalServer 兼容旧入口；仅转发到目标实现。
func InternalServer(reason, message string) *ApplicationError {
	return foundation.InternalServer(reason, message)
}

// IsInternalServer 兼容旧入口；仅转发到目标实现。
func IsInternalServer(err error) bool {
	return foundation.IsInternalServer(err)
}

// ServiceUnavailable 兼容旧入口；仅转发到目标实现。
func ServiceUnavailable(reason, message string) *ApplicationError {
	return foundation.ServiceUnavailable(reason, message)
}

// IsServiceUnavailable 兼容旧入口；仅转发到目标实现。
func IsServiceUnavailable(err error) bool {
	return foundation.IsServiceUnavailable(err)
}

// GatewayTimeout 兼容旧入口；仅转发到目标实现。
func GatewayTimeout(reason, message string) *ApplicationError {
	return foundation.GatewayTimeout(reason, message)
}

// IsGatewayTimeout 兼容旧入口；仅转发到目标实现。
func IsGatewayTimeout(err error) bool {
	return foundation.IsGatewayTimeout(err)
}

// ClientClosed 兼容旧入口；仅转发到目标实现。
func ClientClosed(reason, message string) *ApplicationError {
	return foundation.ClientClosed(reason, message)
}

// IsClientClosed 兼容旧入口；仅转发到目标实现。
func IsClientClosed(err error) bool {
	return foundation.IsClientClosed(err)
}
