// 完成输入在同步提交边界冻结；仅投影既有主体、计量与请求身份，不执行资金操作。
package provider

import (
	"context"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// MessagesCapture 仅供同步捕获使用；CaptureMessages 返回的快照才可以提交给异步 worker。
type MessagesCapture struct {
	Result             *forwardcore.MessagesResult
	APIKey             *apikey.APIKey
	User               *identity.User
	Account            *account.Record
	Subscription       *billing.UserSubscription // 可选：订阅信息
	InboundEndpoint    string                    // 入站端点（客户端请求路径）
	UpstreamEndpoint   string                    // 上游端点（标准化后的上游路径）
	UserAgent          string                    // 请求的 User-Agent
	IPAddress          string                    // 请求的客户端 IP 地址
	ClientSessionID    string                    // 客户端显式会话标识（session_id / X-Session-Id 等请求头），仅用于用量行会话关联
	RequestPayloadHash string                    // 请求体语义哈希，用于降低 request_id 误复用时的静默误去重风险
	RequestBody        []byte                    // 原始请求体，用于解析客户端请求的计费推理档位
	ForceCacheBilling  bool                      // 强制缓存计费：将 input_tokens 转为 cache_read 计费（用于粘性会话切换）
	APIKeyService      QuotaUpdater              // 可选：用于更新API Key配额
	QuotaPlatform      string                    // user×platform 配额计量平台：handler 在请求 ctx 内经 admission.QuotaPlatform() 算定后传入（后扣运行在 worker 池 background ctx 上，取不到 ForcePlatform）

	routing.ChannelUsageFields // 渠道映射信息（由 handler 在 Forward 前解析）
}

// QuotaUpdater 保留调用方已提供额度更新能力的判定，捕获过程不会调用它。
type QuotaUpdater interface {
	UpdateQuotaUsed(ctx context.Context, apiKeyID int64, cost float64) error
	UpdateRateLimitUsage(ctx context.Context, apiKeyID int64, cost float64) error
}

func CompletionRequestID(ctx context.Context, upstreamRequestID string) string {
	return completion.ResolveRequestID(RequestIdentity(ctx, upstreamRequestID, ""), scheduler.GenerateRequestID)
}

// StableAudioBillingRequestID 为单次 TTS/STT HTTP 调用生成持久用量去重键，优先沿用上游请求 ID。
func StableAudioBillingRequestID(upstreamRequestID string) string {
	return completion.StableAudioRequestID(upstreamRequestID, scheduler.GenerateRequestID)
}

// StableRealtimeBillingRequestID 为单个 Realtime WebSocket 会话生成持久用量去重键。
func StableRealtimeBillingRequestID(sessionID string) string {
	return completion.StableRealtimeRequestID(sessionID, scheduler.GenerateRequestID)
}

func CompletionPayloadFingerprint(ctx context.Context, requestPayloadHash string) string {
	return completion.PayloadFingerprint(RequestIdentity(ctx, "", requestPayloadHash))
}

// OpenAICapture 提供同步捕获输入，保留 HTTP 与 WS turn 的原计费时刻。
type OpenAICapture struct {
	Result             *forwardcore.OpenAIResult
	APIKey             *apikey.APIKey
	User               *identity.User
	Account            *account.Record
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
	APIKeyService QuotaUpdater
	QuotaPlatform string // user×platform 配额计量平台，由 handler 在请求 ctx 内算定后传入。
	CyberBlocked  bool
	// NativeCompactionV2 表示请求体运行时被识别为原生远程 compaction v2。
	NativeCompactionV2 bool
	routing.ChannelUsageFields
}

// CyberCapture 是 forward 错误路径中 cyber_policy 命中的补记用量入参。
type CyberCapture struct {
	APIKey             *apikey.APIKey
	Account            *account.Record
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
	APIKeyService      QuotaUpdater
	QuotaPlatform      string
	// NativeCompactionV2 保留错误路径中原生 compaction 标记。
	NativeCompactionV2 bool
	routing.ChannelUsageFields
}

// CaptureMessages 在完成提交边界固化实际用量和主体，不持有原始请求体。
func CaptureMessages(ctx context.Context, in *MessagesCapture) *completion.Input {
	if in == nil {
		return nil
	}
	out := &completion.Input{
		Result:             ProjectMessagesCompletionResult(in.Result, in.Account),
		APIKey:             ProjectCompletionKey(in.APIKey),
		User:               ProjectCompletionPayer(in.User),
		Account:            ProjectCompletionAccount(in.Account),
		Subscription:       in.Subscription,
		InboundEndpoint:    in.InboundEndpoint,
		UpstreamEndpoint:   in.UpstreamEndpoint,
		UserAgent:          in.UserAgent,
		IPAddress:          in.IPAddress,
		ClientSessionID:    in.ClientSessionID,
		RequestPayloadHash: CompletionPayloadFingerprint(ctx, in.RequestPayloadHash),
		QuotaPlatform:      in.QuotaPlatform,
		ForceCacheBilling:  in.ForceCacheBilling,
		QuotaUpdates:       in.APIKeyService != nil,
		ChannelUsageFields: in.ChannelUsageFields,
	}
	if in.Result != nil {
		out.RequestID = CompletionRequestID(ctx, in.Result.RequestID)
		out.RequestedReasoningEffort = requeststate.CanonicalRequestedReasoningEffort(in.RequestBody, in.Result.Model)
	}
	return completion.Snapshot(out)
}

// CaptureOpenAI 保留 WS turn 固定时刻及独立账单模型链。
func CaptureOpenAI(ctx context.Context, in *OpenAICapture) *completion.Input {
	if in == nil {
		return nil
	}
	out := &completion.Input{
		Result:                   ProjectOpenAICompletionResult(in.Result, in.Account),
		APIKey:                   ProjectCompletionKey(in.APIKey),
		User:                     ProjectCompletionPayer(in.User),
		Account:                  ProjectCompletionAccount(in.Account),
		Subscription:             in.Subscription,
		InboundEndpoint:          in.InboundEndpoint,
		UpstreamEndpoint:         in.UpstreamEndpoint,
		UserAgent:                in.UserAgent,
		IPAddress:                in.IPAddress,
		ClientSessionID:          in.ClientSessionID,
		RequestPayloadHash:       CompletionPayloadFingerprint(ctx, in.RequestPayloadHash),
		QuotaPlatform:            in.QuotaPlatform,
		QuotaUpdates:             in.APIKeyService != nil,
		CyberBlocked:             in.CyberBlocked,
		NativeCompactionV2:       in.NativeCompactionV2,
		PricingAt:                in.PricingAt,
		ChannelUsageFields:       in.ChannelUsageFields,
		RequestedReasoningEffort: requeststate.CanonicalRequestedReasoningEffort(in.RequestBody, in.OriginalModel, in.ChannelMappedModel),
	}
	if in.Result != nil {
		out.RequestID = CompletionRequestID(ctx, in.Result.RequestID)
	}
	return completion.Snapshot(out)
}

func RequestIdentity(ctx context.Context, upstream, payload string) completion.RequestIdentity {
	out := completion.RequestIdentity{Upstream: upstream, PayloadHash: payload}
	if ctx != nil {
		out.Client, _ = ctx.Value(telemetry.ClientRequestID).(string)
		out.Local, _ = ctx.Value(telemetry.RequestID).(string)
	}
	return out
}

// CaptureCyber 在请求提交时投影并冻结，异步任务不再持有旧实体。
func CaptureCyber(ctx context.Context, in CyberCapture) *completion.Input {
	if in.APIKey == nil || in.APIKey.User == nil || in.Account == nil || strings.TrimSpace(in.Model) == "" {
		return nil
	}
	result := &forwardcore.OpenAIResult{
		RequestID: in.RequestID,
		Model:     strings.TrimSpace(in.Model),
		Usage: openai.ForwardUsage{
			InputTokens:  in.InputTokens,
			OutputTokens: in.OutputTokens,
		},
		Stream:   in.Stream,
		Duration: 0,
	}
	return CaptureOpenAI(ctx, &OpenAICapture{
		Result:             result,
		APIKey:             in.APIKey,
		User:               in.APIKey.User,
		Account:            in.Account,
		Subscription:       in.Subscription,
		InboundEndpoint:    in.InboundEndpoint,
		UpstreamEndpoint:   in.UpstreamEndpoint,
		UserAgent:          in.UserAgent,
		IPAddress:          in.IPAddress,
		ClientSessionID:    in.ClientSessionID,
		RequestPayloadHash: in.RequestPayloadHash,
		APIKeyService:      in.APIKeyService,
		QuotaPlatform:      in.QuotaPlatform,
		ChannelUsageFields: in.ChannelUsageFields,
		CyberBlocked:       true,
		NativeCompactionV2: in.NativeCompactionV2,
	})
}
