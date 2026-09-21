package service

// 本文件由 openai_gateway_service.go 纯移动拆分而来：用量记录、计费成本计算与
// Codex 用量快照。仅做代码搬迁，无任何行为变更。

import (
	"context"
	"net/http"
	"time"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	openaiupstream "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	quotaAccount "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// OpenAIRecordUsageInput input for recording usage
type OpenAIRecordUsageInput struct {
	Result             *forwardcore.OpenAIResult
	APIKey             *apikey.APIKey
	User               *identity.User
	Account            *Account
	Subscription       *billing.UserSubscription
	InboundEndpoint    string
	UpstreamEndpoint   string
	UserAgent          string // 请求的 User-Agent
	IPAddress          string // 请求的客户端 IP 地址
	ClientSessionID    string // 客户端显式会话标识（session_id / X-Session-Id 等请求头），仅用于用量行会话关联
	RequestPayloadHash string
	RequestBody        []byte // 原始请求体，用于解析客户端请求的计费推理档位
	// PricingAt 是 WS turn 开始时刻；普通 HTTP 调用留空并在记录时取当前时间。
	PricingAt     time.Time
	APIKeyService APIKeyQuotaUpdater
	QuotaPlatform string // user×platform 配额计量平台，由 handler 在请求 ctx 内算定后传入。
	CyberBlocked  bool
	// NativeCompactionV2 表示请求体运行时被识别为原生远程 compaction v2。
	NativeCompactionV2 bool
	routing.ChannelUsageFields
}

// CyberPolicyUsageInput 是 forward 错误路径中 cyber_policy 命中的补记用量入参。
type CyberPolicyUsageInput struct {
	APIKey             *apikey.APIKey
	Account            *Account
	Subscription       *billing.UserSubscription
	RequestID          string
	Model              string
	Stream             bool
	InputTokens        int
	OutputTokens       int
	InboundEndpoint    string
	UpstreamEndpoint   string
	UserAgent          string
	IPAddress          string
	ClientSessionID    string
	RequestPayloadHash string
	APIKeyService      APIKeyQuotaUpdater
	QuotaPlatform      string
	// NativeCompactionV2 保留错误路径中原生 compaction 标记。
	NativeCompactionV2 bool
	routing.ChannelUsageFields
}

// RecordCyberPolicyUsageLog 为未进入正常成功用量路径的 cyber_policy 命中补记用量。
func (s *OpenAIGatewayService) RecordCyberPolicyUsageLog(ctx context.Context, in CyberPolicyUsageInput) {
	if s == nil {
		return
	}
	s.CompletionRecorder(in.APIKeyService).RecordCyber(ctx, CompletionCyberInput(ctx, in))
}

// RecordUsage records usage and deducts balance
func (s *OpenAIGatewayService) RecordUsage(ctx context.Context, input *OpenAIRecordUsageInput) error {
	var updater APIKeyQuotaUpdater
	if input != nil {
		updater = input.APIKeyService
	}
	return s.CompletionRecorder(updater).Record(ctx, CompletionOpenAIInput(ctx, input), true)
}

// updateCodexUsageSnapshot saves the Codex usage snapshot to account's Extra field
// updateCodexUsageSnapshot 把 /responses 的 x-codex-* 全局头快照写入账号 codex_* Extra。
// ⚠️ 调用方必须排除 spark 影子账号(account.IsShadow()):影子的 codex_* 仅由 QueryUsage
// (/wham/usage bengalfox 道)更新,不能被全局头口径污染(外审第7轮 P1)。本函数仅持 accountID,
// 无法在此自检影子,故守卫前置到各调用点。
func (s *OpenAIGatewayService) updateCodexUsageSnapshot(ctx context.Context, accountID int64, snapshot *openai.OpenAICodexUsageSnapshot) {
	if snapshot == nil {
		return
	}
	if s == nil || s.accountRepo == nil {
		return
	}

	now := time.Now()
	updates := quotaAccount.BuildCodexUsageExtraUpdates(snapshot, now)
	if len(updates) == 0 {
		return
	}
	if !s.getCodexSnapshotThrottle().Allow(accountID, now) {
		return
	}
	RunBackgroundTask("service/openai_gateway_usage.go:updateCodexUsageSnapshot", BackgroundCall0(func() {
		updateCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.accountRepo.UpdateExtra(updateCtx, accountID, updates)
	}))
}

func (s *OpenAIGatewayService) UpdateCodexUsageSnapshotFromHeaders(ctx context.Context, accountID int64, headers http.Header) {
	if accountID <= 0 || headers == nil {
		return
	}
	if snapshot := openaiupstream.ParseCodexRateLimitHeaders(headers); snapshot != nil {
		s.updateCodexUsageSnapshot(ctx, accountID, snapshot)
	}
}
