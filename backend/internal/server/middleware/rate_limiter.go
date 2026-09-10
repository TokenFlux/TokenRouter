// 本文件拥有 HTTP 限流维度、故障策略与响应，计数通过调用方契约注入。
package middleware

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"time"

	ippkg "github.com/TokenFlux/TokenRouter/internal/server/clientip"

	"github.com/gin-gonic/gin"
)

const (
	RateLimitFailOpen RateLimitFailureMode = iota
	RateLimitFailClose
)

// FixedWindowAllower 只返回标量计数结果，避免 HTTP Adapter 引用具体 Redis 包或共享业务实体。
type FixedWindowAllower interface {
	Allow(context.Context, string, int, time.Duration) (bool, int64, time.Duration, error)
}

// RateLimiter 为固定窗口结果提供 HTTP 适配。
type RateLimiter struct {
	allower FixedWindowAllower
	prefix  string
}

// NewRateLimiter 接受计数能力，HTTP 层不构造数据库客户端。
func NewRateLimiter(allower FixedWindowAllower) *RateLimiter {
	return &RateLimiter{allower: allower, prefix: "rate_limit:"}
}

// Allow 保留面板调用方的结果形状，错误时继续返回零值。
func (r *RateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (AllowResult, error) {
	allowed, count, retry, err := r.allower.Allow(ctx, key, limit, window)
	if err != nil {
		return AllowResult{}, err
	}
	return AllowResult{Allowed: allowed, Count: count, RetryAfter: retry}, nil
}

// RateLimitFailureMode Redis 故障策略
type RateLimitFailureMode int

// RateLimitOptions 限流可选配置
type RateLimitOptions struct {
	FailureMode RateLimitFailureMode
}

// AllowResult 单次固定窗口限流判定结果。
type AllowResult struct {
	// Allowed 是否放行
	Allowed bool
	// Count 当前窗口内累计请求数（含本次）
	Count int64
	// RetryAfter 超限时距窗口重置的剩余时间（尽力而为；PTTL 不可用时回退为完整窗口）
	RetryAfter time.Duration
}

// clientIPForRateLimit 返回 IP 维度限流使用的客户端地址。
// 与审计日志/会话绑定/API Key IP ACL 共用同一套安全客户端 IP 解析
// （SessionBindingContext 快照：兼容开关开启时信任反代转发头，关闭时走
// server.trusted_proxies 可信链）。避免默认反代部署下 Gin ClientIP 恒等于
// 代理地址、所有用户坍缩进同一个限流桶造成整体误拦截。
func clientIPForRateLimit(c *gin.Context) string {
	if resolved := ippkg.GetSecurityClientIP(c, false); resolved != "" {
		return resolved
	}
	return c.ClientIP()
}

// Limit 返回速率限制中间件
// key: 限制类型标识
// limit: 时间窗口内最大请求数
// window: 时间窗口
func (r *RateLimiter) Limit(key string, limit int, window time.Duration) gin.HandlerFunc {
	return r.LimitWithOptions(key, limit, window, RateLimitOptions{})
}

// LimitWithOptions 返回速率限制中间件（带可选配置）
func (r *RateLimiter) LimitWithOptions(key string, limit int, window time.Duration, opts RateLimitOptions) gin.HandlerFunc {
	failureMode := opts.FailureMode
	if failureMode != RateLimitFailClose {
		failureMode = RateLimitFailOpen
	}

	return func(c *gin.Context) {
		result, err := r.Allow(c.Request.Context(), key+":"+clientIPForRateLimit(c), limit, window)
		if err != nil {
			log.Printf("[RateLimit] redis error: key=%s mode=%s err=%v", r.prefix+key, failureModeLabel(failureMode), err)
			if failureMode == RateLimitFailClose {
				abortRateLimit(c, window)
				return
			}
			// Redis 错误时放行，避免影响正常服务
			c.Next()
			return
		}

		// 超过限制
		if !result.Allowed {
			abortRateLimit(c, result.RetryAfter)
			return
		}

		c.Next()
	}
}

func abortRateLimit(c *gin.Context, retryAfter time.Duration) {
	if retryAfter > 0 {
		seconds := int64(retryAfter / time.Second)
		if retryAfter%time.Second > 0 {
			seconds++
		}
		c.Header("Retry-After", strconv.FormatInt(seconds, 10))
	}
	c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
		"error":   "rate limit exceeded",
		"message": "Too many requests, please try again later",
	})
}

func failureModeLabel(mode RateLimitFailureMode) string {
	if mode == RateLimitFailClose {
		return "fail-close"
	}
	return "fail-open"
}
