package handler

import (
	"context"
	"time"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	middleware2 "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"

	"github.com/gin-gonic/gin"
)

// 并发槽位等待相关常量
//
// 性能优化说明：
// 原实现使用固定间隔（100ms）轮询并发槽位，存在以下问题：
// 1. 高并发时频繁轮询增加 Redis 压力
// 2. 固定间隔可能导致多个请求同时重试（惊群效应）
//
// 新实现使用指数退避 + 抖动算法：
// 1. 初始退避 100ms，每次乘以 1.5，最大 2s
// 2. 添加 ±20% 的随机抖动，分散重试时间点
// 3. 减少 Redis 压力，避免惊群效应
const (
	// defaultPingInterval 流式响应等待时发送 ping 的默认间隔
	defaultPingInterval = 10 * time.Second
	// initialBackoff 初始退避时间
	initialBackoff = scheduler.InitialBackoff
	// maxBackoff 最大退避时间
	maxBackoff = scheduler.MaxBackoff
)

// wrapReleaseOnDone ensures release runs at most once and still triggers on context cancellation.
// 用于避免客户端断开或上游超时导致的并发槽位泄漏。
// 优化：基于 context.AfterFunc 注册回调，避免每请求额外守护 goroutine。
func wrapReleaseOnDone(ctx context.Context, releaseFunc func()) func() {
	return scheduler.WrapRelease(ctx, scheduler.ReleaseOnCancel, releaseFunc)
}

// nextBackoff 计算下一次退避时间
// 性能优化：使用指数退避 + 随机抖动，避免惊群效应
// current: 当前退避时间
// 返回值：下一次退避时间（100ms ~ 2s 之间）
func nextBackoff(current time.Duration) time.Duration { return scheduler.NextBackoff(current) }

// 并发兼容入口只转接唯一 HTTP 实现，S15/S16 清理。
type ConcurrencyHelper = gatewayhttp.ConcurrencyHelper
type ConcurrencyError = scheduler.ConcurrencyError
type WaitQueueFullError = scheduler.WaitQueueFullError
type SSEPingFormat = gatewayhttp.SSEPingFormat

const SSEPingFormatClaude = gatewayhttp.SSEPingFormatClaude
const SSEPingFormatNone = gatewayhttp.SSEPingFormatNone
const SSEPingFormatComment = gatewayhttp.SSEPingFormatComment

func NewConcurrencyHelper(s *service.ConcurrencyService, f SSEPingFormat, d time.Duration) *ConcurrencyHelper {
	return gatewayhttp.NewConcurrencyHelper(s, f, d, func(c *gin.Context) int64 {
		if key, ok := middleware2.GetAPIKeyFromContext(c); ok && key != nil {
			return key.ID
		}
		return 0
	})
}

func gatewayStreamHasOnlyHeartbeats(c *gin.Context) bool {
	return gatewayhttp.StreamHasOnlyHeartbeats(c)
}
func gatewayWaitObserver(c *gin.Context, f SSEPingFormat, d time.Duration, stream bool, started *bool, strict bool) scheduler.WaitObserver {
	return gatewayhttp.WaitObserver(c, f, d, stream, started, strict)
}

// SetClaudeCodeClientContext 只把显式识别结果投影给剩余旧请求消费者。
func SetClaudeCodeClientContext(c *gin.Context, body []byte, parsed *service.ParsedRequest) {
	if c == nil || c.Request == nil {
		return
	}
	probe, _ := service.IsMaxTokensOneHaikuRequestFromContext(c.Request.Context())
	result := gatewayhttp.DetectClaudeCodeRequest(c, body, parsed, probe)
	ctx := service.SetClaudeCodeClient(c.Request.Context(), result.ClaudeCode)
	if result.ClaudeCode && result.Version != "" {
		ctx = service.SetClaudeCodeVersion(ctx, result.Version)
	}
	c.Request = c.Request.WithContext(ctx)
}
