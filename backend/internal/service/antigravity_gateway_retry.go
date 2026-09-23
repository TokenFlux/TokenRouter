package service

import (
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"

	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	httpclient "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/gin-gonic/gin"
)

// antigravityRetryLoopParams 重试循环的参数
type antigravityRetryLoopParams struct {
	userAgent       string // 指定账号探测显式提供，普通转发保持空值。
	ctx             context.Context
	prefix          string
	account         *gatewayprovider.ExecutionAccount
	proxyURL        string
	accessToken     string
	action          string
	body            []byte
	c               *gin.Context
	httpUpstream    httpclient.UpstreamTransport
	settingService  *gatewayprovider.RuntimeReaders
	accountRepo     gatewayprovider.ExecutionAccountStore // 用于智能重试的模型级别限流
	handleError     func(ctx context.Context, prefix string, account *gatewayprovider.ExecutionAccount, statusCode int, headers http.Header, body []byte, requestedModel string, groupID int64, sessionHash string, isStickySession bool) *handleModelRateLimitResult
	requestedModel  string // 用于限流检查的原始请求模型
	isStickySession bool   // 是否为粘性会话（用于账号切换时的缓存计费判断）
	groupID         int64  // 用于模型级限流时清除粘性会话
	sessionHash     string // 用于模型级限流时清除粘性会话
}

// getSessionID 从 gin.Context 获取 session_id（用于日志追踪）
func getSessionID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	return c.GetHeader("session_id")
}

// logPrefix 生成统一的日志前缀
func logPrefix(sessionID, accountName string) string {
	if sessionID != "" {
		return fmt.Sprintf("[antigravity-Forward] session=%s account=%s", sessionID, accountName)
	}
	return fmt.Sprintf("[antigravity-Forward] account=%s", accountName)
}

func (s *AntigravityGatewayService) shouldFailoverUpstreamError(statusCode int) bool {
	switch statusCode {
	case 401, 403, 429, 529:
		return true
	default:
		return statusCode >= 500
	}
}

// googleConfigErrorCooldown 服务端配置类 400 错误的临时封禁时长

// emptyResponseCooldown 空流式响应的临时封禁时长

// isSingleAccountRetry 检查 context 中是否设置了单账号退避重试标记
func isSingleAccountRetry(ctx context.Context) bool {
	v, _ := requeststate.SingleAccountRetryFromContext(ctx)
	return v
}

func (s *AntigravityGatewayService) clearStickySession(ctx context.Context, groupID int64, sessionHash string) {
	if s == nil || s.cache == nil || strings.TrimSpace(sessionHash) == "" {
		return
	}
	if err := s.cache.DeleteSessionAccountID(ctx, groupID, sessionHash); err != nil {
		logging.LegacyPrintf("service.antigravity_gateway", "[antigravity-Forward] sticky_session_clear_failed group_id=%d session=%s err=%v", groupID, shortSessionHash(sessionHash), err)
	}
}

// getDefaultRateLimitDuration 获取默认限流时间
func (s *AntigravityGatewayService) getDefaultRateLimitDuration() time.Duration {
	minutes := 0
	if s.settingService != nil && s.settingService.Antigravity != nil {
		minutes = s.settingService.Antigravity.AntigravityFallbackCooldownMinutes
	}
	return accountprovider.AntigravityFallbackDuration(minutes, os.Getenv(antigravityFallbackSecondsEnv))
}

// 旧端点入口只投影账号，付费资格仍由原生平台适配器判断。
func accountHasAntigravityPaidTier(value *gatewayprovider.ExecutionAccount) bool {
	return accountprovider.AntigravityPaidTier(gatewayprovider.ExecutionRecord(value))
}

// 旧转发只转换请求观测；平台判断和写入顺序由原生 Adapter 拥有。
type handleModelRateLimitResult = accountprovider.AntigravityModelLimitResult

func (s *AntigravityGatewayService) handleUpstreamError(ctx context.Context, prefix string, value *gatewayprovider.ExecutionAccount, status int, headers http.Header, body []byte, model string, groupID int64, sessionHash string, sticky bool) *handleModelRateLimitResult {
	view := gatewayprovider.ExecutionRecord(value)
	observer := s.nativeError
	if observer == nil {
		observer = &accountprovider.AntigravityErrorObserver{Health: s.antigravityHealth(), LogConfig: s.getLogConfig, TruncateString: logredact.TruncateUTF8, ResetTime: ParseGeminiRateLimitResetTime, DefaultDuration: s.getDefaultRateLimitDuration}
		if s.accountRepo != nil {
			observer.SetRateLimited = s.accountRepo.SetRateLimited
		}
		if s.healthObserver != nil {
			observer.Other = s.healthObserver
		}
	}
	input := accountprovider.AntigravityErrorInput{Context: ctx, Account: view, Prefix: prefix, Status: status, Headers: headers, Body: body, RequestedModel: model, Thinking: requeststate.HealthThinking(ctx), OtherObservation: gatewayprovider.HealthObservationFromContext(ctx, status, headers, body, nil), Sticky: sticky}
	if s.cache != nil && sessionHash != "" {
		input.ClearSticky = func() { _ = s.cache.DeleteSessionAccountID(ctx, groupID, sessionHash) }
	}
	result := observer.Observe(input)
	if value != nil && view != nil {
		value.Record.Extra, value.Record.Credentials = view.Extra, view.Credentials
	}
	return result
}
