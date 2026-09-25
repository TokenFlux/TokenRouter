package httpapi

import (
	"context"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
)

// OpenAIWSOptions 只保存WS执行的静态参数；nil保留未配置时的原缺省语义。
type OpenAIWSOptions struct {
	Enabled, OAuthEnabled, APIKeyEnabled, ForceHTTP                                       bool
	ResponsesWebsockets, ResponsesWebsocketsV2, ModeRouterV2Enabled                       bool
	IngressModeDefault                                                                    string
	ClientFirstMessageTimeoutSeconds, IngressInterTurnIdleTimeoutSeconds                  int
	ClientReadLimitBytes, HTTPBridgeThresholdBytes                                        int64
	HTTPBridgeEnabled                                                                     bool
	AllowStoreRecovery, IngressPreviousResponseRecoveryEnabled                            bool
	StoreDisabledConnMode                                                                 string
	StoreDisabledForceNewConn, PrewarmGenerateEnabled                                     bool
	DialTimeoutSeconds, ReadTimeoutSeconds, WriteTimeoutSeconds                           int
	EventFlushBatchSize, EventFlushIntervalMS, PrewarmCooldownMS                          int
	FallbackCooldownSeconds, RetryBackoffInitialMS, RetryBackoffMaxMS, RetryTotalBudgetMS int
	RetryJitterRatio, PayloadLogSampleRate                                                float64
	StickyResponseIDTTLSeconds                                                            int
}

// OpenAIWSSelection 只提供已经选中账号的传输选择与会话预算。
type OpenAIWSSelection interface {
	ResolveTransport(*provider.ExecutionAccount) egress.OpenAIWSProtocolDecision
	SessionStickyTTL() time.Duration
}

// OpenAIWSDependencies 固定技术端口，不持有连接池、取消表或重试计数的副本。
type OpenAIWSDependencies struct {
	Options     *OpenAIWSOptions
	Connections *OpenAIWSConnections
	Requests    *OpenAIRequests
	Output      *OpenAIResponseOutput
	Grok        *GrokExecutor
	FastPolicy  *provider.ExecutionFastPolicy
	Prompts     *promptpolicy.Service
	Selection   OpenAIWSSelection
	State       session.OpenAIWSStateStore
	Lineage     *OpenAIEncryptedLineage
	ImageBridge *provider.ResponseImagePolicy
	Cache       session.GatewayCache
}

// OpenAIWebSocketExecutor 适配帧、凭据、健康与HTTP桥接，逐轮循环由gateway/ws唯一拥有。
type OpenAIWebSocketExecutor struct {
	OpenAIWSDependencies
	openaiWSSessionPreemptions openAIWSSessionPreemptRegistry
	openaiWSFallbackUntil      sync.Map
	openaiWSRetryMetrics       openAIWSRetryMetrics
}

// NewOpenAIWebSocketExecutor 构造不启动连接或会话任务。
func NewOpenAIWebSocketExecutor(deps OpenAIWSDependencies) *OpenAIWebSocketExecutor {
	out := &OpenAIWebSocketExecutor{OpenAIWSDependencies: deps}
	out.logOpenAIWSModeBootstrap()
	return out
}

// EnsureSessionIsolation 投影当前认证Key，不重新读取用户或分组。
func (s *OpenAIWebSocketExecutor) EnsureSessionIsolation(ctx context.Context, key *apikey.APIKey, userID int64, source, hash string) error {
	if key == nil {
		return nil
	}
	var group int64
	if key.GroupID != nil {
		group = *key.GroupID
	}
	return session.EnsureIsolation(ctx, s.Cache, session.IsolationInput{UserID: userID, GroupID: group, Source: source, Hash: hash, TTL: time.Hour, Enabled: key.Group != nil && key.Group.SessionIsolationEnabled})
}
