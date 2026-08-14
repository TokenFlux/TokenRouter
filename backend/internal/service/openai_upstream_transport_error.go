package service

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// OpenAI 传输层持久故障的临时摘除时长，与 token 刷新临时不可调度时长保持一致。
const openAITransportErrorTempUnschedDuration = 10 * time.Minute

// OpenAI 传输层错误在 failover 耗尽后回给客户端的兼容响应体。
var openAITransportFailoverBody = []byte(`{"error":{"type":"upstream_error","message":"Upstream request failed"}}`)

// openAITransportErrorClass 描述上游 HTTP 请求未拿到响应时的处理策略。
type openAITransportErrorClass struct {
	// Persistent 表示重试同一个账号或代理基本无意义，应临时摘除账号。
	Persistent bool
}

// openAIPersistentTransportErrorMarkers 匹配明确的持久代理或网络故障原因。
var openAIPersistentTransportErrorMarkers = []string{
	"authentication failed",
	"proxy authentication required",
	"connection refused",
	"no route to host",
	"network is unreachable",
	"no such host",
}

// classifyOpenAITransportError 判断传输层错误是否属于持久故障。
func classifyOpenAITransportError(err error) openAITransportErrorClass {
	if err == nil {
		return openAITransportErrorClass{}
	}
	if errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.EHOSTUNREACH) ||
		errors.Is(err, syscall.ENETUNREACH) {
		return openAITransportErrorClass{Persistent: true}
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return openAITransportErrorClass{Persistent: true}
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range openAIPersistentTransportErrorMarkers {
		if strings.Contains(msg, marker) {
			return openAITransportErrorClass{Persistent: true}
		}
	}
	return openAITransportErrorClass{}
}

// handleOpenAIUpstreamTransportError 处理没有 HTTP 响应的上游传输层错误。
//
// 该函数只记录 ops 和返回错误，不直接写响应；非客户端取消错误会转成
// UpstreamFailoverError，让 handler 继续切换账号。持久故障会同步临时摘除账号。
func (s *OpenAIGatewayService) handleOpenAIUpstreamTransportError(ctx context.Context, c *gin.Context, account *Account, err error, passthrough bool) error {
	if err == nil {
		return nil
	}
	safeErr := sanitizeUpstreamErrorMessage(err.Error())
	platform, accountName := "", ""
	var accountID int64
	if account != nil {
		platform = account.Platform
		accountID = account.ID
		accountName = account.Name
	}
	setOpsUpstreamError(c, 0, safeErr, "")
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:           platform,
		AccountID:          accountID,
		AccountName:        accountName,
		UpstreamStatusCode: 0,
		Passthrough:        passthrough,
		Kind:               "request_error",
		Message:            safeErr,
	})

	// 客户端断开不是账号故障，不应切换到其它账号继续消耗请求。
	if errors.Is(err, context.Canceled) {
		return err
	}

	// 请求已进入网络传输路径，应计入 Ollama Cloud 活动。
	if s != nil {
		scheduleOllamaCloudUsageActivity(s.deferredService, account)
	}

	if classifyOpenAITransportError(err).Persistent {
		s.tempUnscheduleOpenAITransportError(ctx, account, safeErr)
	}
	return &UpstreamFailoverError{
		StatusCode:   http.StatusBadGateway,
		ResponseBody: openAITransportFailoverBody,
	}
}

// tempUnscheduleOpenAITransportError 因持久传输层故障临时摘除 OpenAI 账号。
func (s *OpenAIGatewayService) tempUnscheduleOpenAITransportError(ctx context.Context, account *Account, safeErr string) {
	if s == nil || account == nil {
		return
	}
	until := time.Now().Add(openAITransportErrorTempUnschedDuration)
	reason := "upstream transport error (proxy/network): " + safeErr

	// 先做内存阻断，避免等待数据库和账号缓存传播期间继续调度该账号。
	s.BlockAccountScheduling(account, until, "transport_error")

	if s.accountRepo == nil {
		logger.L().With(zap.String("component", "service.openai_gateway")).Warn(
			"openai.account_temp_unscheduled_transport_memory_only",
			zap.Int64("account_id", account.ID),
			zap.String("account_name", account.Name),
			zap.String("platform", account.Platform),
			zap.Time("until", until),
			zap.String("reason", reason),
		)
		return
	}

	bgCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), openAIAccountStateUpdateTimeout)
	defer cancel()
	if err := s.accountRepo.SetTempUnschedulable(bgCtx, account.ID, until, reason); err != nil {
		logger.L().With(zap.String("component", "service.openai_gateway")).Warn(
			"openai.account_temp_unscheduled_transport_failed",
			zap.Int64("account_id", account.ID),
			zap.Error(err),
		)
		return
	}
	logger.L().With(zap.String("component", "service.openai_gateway")).Warn(
		"openai.account_temp_unscheduled_transport",
		zap.Int64("account_id", account.ID),
		zap.String("account_name", account.Name),
		zap.String("platform", account.Platform),
		zap.Time("until", until),
		zap.String("reason", reason),
	)
}
