// 本文件为阶段迁移兼容入口；剩余消费者和退出阶段见 refactor/S01-foundation.md。
package response

import (
	foundation "github.com/TokenFlux/TokenRouter/internal/server/httpx"

	gin "github.com/gin-gonic/gin"
)

// Response 保留旧调用方的类型身份；实现归目标包。
type Response = foundation.Response

// PaginatedData 保留旧调用方的类型身份；实现归目标包。
type PaginatedData = foundation.PaginatedData

// Success 兼容旧入口；仅转发到目标实现。
func Success(c *gin.Context, data any) {
	foundation.Success(c, data)
}

// Created 兼容旧入口；仅转发到目标实现。
func Created(c *gin.Context, data any) {
	foundation.Created(c, data)
}

// Accepted 兼容旧入口；仅转发到目标实现。
func Accepted(c *gin.Context, data any) {
	foundation.Accepted(c, data)
}

// Error 兼容旧入口；仅转发到目标实现。
func Error(c *gin.Context, statusCode int, message string) {
	foundation.Error(c, statusCode, message)
}

// ErrorWithDetails 兼容旧入口；仅转发到目标实现。
func ErrorWithDetails(c *gin.Context, statusCode int, message, reason string, metadata map[string]string) {
	foundation.ErrorWithDetails(c, statusCode, message, reason, metadata)
}

// ErrorFrom 兼容旧入口；仅转发到目标实现。
func ErrorFrom(c *gin.Context, err error) bool {
	return foundation.ErrorFrom(c, err)
}

// BadRequest 兼容旧入口；仅转发到目标实现。
func BadRequest(c *gin.Context, message string) {
	foundation.BadRequest(c, message)
}

// Unauthorized 兼容旧入口；仅转发到目标实现。
func Unauthorized(c *gin.Context, message string) {
	foundation.Unauthorized(c, message)
}

// Forbidden 兼容旧入口；仅转发到目标实现。
func Forbidden(c *gin.Context, message string) {
	foundation.Forbidden(c, message)
}

// NotFound 兼容旧入口；仅转发到目标实现。
func NotFound(c *gin.Context, message string) {
	foundation.NotFound(c, message)
}

// InternalError 兼容旧入口；仅转发到目标实现。
func InternalError(c *gin.Context, message string) {
	foundation.InternalError(c, message)
}

// Paginated 兼容旧入口；仅转发到目标实现。
func Paginated(c *gin.Context, items any, total int64, page, pageSize int) {
	foundation.Paginated(c, items, total, page, pageSize)
}

// PaginationResult 保留旧调用方的类型身份；实现归目标包。
type PaginationResult = foundation.PaginationResult

// PaginatedWithResult 兼容旧入口；仅转发到目标实现。
func PaginatedWithResult(c *gin.Context, items any, pagination *PaginationResult) {
	foundation.PaginatedWithResult(c, items, pagination)
}

// ParsePagination 兼容旧入口；仅转发到目标实现。
func ParsePagination(c *gin.Context) (page, pageSize int) {
	return foundation.ParsePagination(c)
}
