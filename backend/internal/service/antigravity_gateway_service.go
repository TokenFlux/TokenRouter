package service

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	httpclient "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	"github.com/TokenFlux/TokenRouter/internal/gateway/session"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

const (
	antigravityForwardBaseURLEnv  = "GATEWAY_ANTIGRAVITY_FORWARD_BASE_URL"
	antigravityFallbackSecondsEnv = "GATEWAY_ANTIGRAVITY_FALLBACK_COOLDOWN_SECONDS"
)

const antigravityProjectIDFallbackCredentialKey = "antigravity_project_id"

// AntigravityGatewayService 处理 Antigravity 平台的 API 转发
type AntigravityGatewayService struct {
	nativeHealth          *accountcore.AntigravityHealth
	nativeRetry           *accountprovider.AntigravityRetry
	nativeError           *accountprovider.AntigravityErrorObserver
	nativeAttemptActivity func() (func(), error)
	accountRepo           gatewayprovider.ExecutionAccountStore
	tokenProvider         *accountcore.AntigravityTokenSource
	healthObserver        *accountprovider.UpstreamHealth

	httpUpstream      httpclient.UpstreamTransport
	settingService    *gatewayprovider.RuntimeReaders
	cache             session.GatewayCache // 用于模型级限流时清除粘性会话绑定
	schedulerSnapshot *scheduler.SnapshotService
	internal500Cache  accountcore.Internal500CounterCache // INTERNAL 500 渐进惩罚计数器
}

func (s *AntigravityGatewayService) upstreamErrorBodyReadLimit() int64 {
	limit := gatewayUpstreamErrorBodyReadLimit
	if s != nil && s.settingService != nil && s.settingService.Antigravity != nil && s.settingService.Antigravity.LogUpstreamErrorBody && s.settingService.Antigravity.LogUpstreamErrorBodyMaxBytes > int(limit) {
		limit = int64(s.settingService.Antigravity.LogUpstreamErrorBodyMaxBytes)
	}
	return limit
}

func (s *AntigravityGatewayService) readUpstreamErrorBody(resp *http.Response) []byte {
	if resp == nil || resp.Body == nil {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, s.upstreamErrorBodyReadLimit()))
	return body
}

func NewAntigravityGatewayService(
	accountRepo gatewayprovider.ExecutionAccountStore,
	cache session.GatewayCache,
	schedulerSnapshot *scheduler.SnapshotService,
	tokenProvider *accountcore.AntigravityTokenSource,
	healthObserver *accountprovider.UpstreamHealth,
	httpUpstream httpclient.UpstreamTransport,
	settingService *gatewayprovider.RuntimeReaders,
	internal500Cache accountcore.Internal500CounterCache,
) *AntigravityGatewayService {
	return &AntigravityGatewayService{
		accountRepo:       accountRepo,
		tokenProvider:     tokenProvider,
		healthObserver:    healthObserver,
		httpUpstream:      httpUpstream,
		settingService:    settingService,
		cache:             cache,
		schedulerSnapshot: schedulerSnapshot,
		internal500Cache:  internal500Cache,
	}
}

// getLogConfig 获取上游错误日志配置
// 返回是否记录日志体和最大字节数
func (s *AntigravityGatewayService) getLogConfig() (logBody bool, maxBytes int) {
	maxBytes = 2048 // 默认值
	if s.settingService == nil || s.settingService.Antigravity == nil {
		return false, maxBytes
	}
	cfg := s.settingService.Antigravity
	if cfg.LogUpstreamErrorBodyMaxBytes > 0 {
		maxBytes = cfg.LogUpstreamErrorBodyMaxBytes
	}
	return cfg.LogUpstreamErrorBody, maxBytes
}

// getUpstreamErrorDetail 获取上游错误详情（用于日志记录）
func (s *AntigravityGatewayService) getUpstreamErrorDetail(body []byte) string {
	logBody, maxBytes := s.getLogConfig()
	if !logBody {
		return ""
	}
	return logredact.TruncateUTF8(string(body), maxBytes)
}

// getMappedModel 获取映射后的模型名
// 完全依赖映射配置：账户映射（通配符）→ 默认映射兜底
func (s *AntigravityGatewayService) getMappedModel(account *gatewayprovider.ExecutionAccount, requestedModel string) string {
	return mapAntigravityModel(account, requestedModel)
}

func resolveAntigravityProjectID(account *gatewayprovider.ExecutionAccount) (string, error) {
	return accountcore.ResolveAntigravityProjectID(gatewayprovider.ExecutionRecord(account), antigravity.ErrProjectIDRequired)
}

// IsModelSupported 检查模型是否被支持
// 所有 claude- 和 gemini- 前缀的模型都能通过映射或透传支持
func (s *AntigravityGatewayService) IsModelSupported(requestedModel string) bool {
	return strings.HasPrefix(requestedModel, "claude-") ||
		strings.HasPrefix(requestedModel, "gemini-")
}

func (s *AntigravityGatewayService) getClaudeTransformOptions(ctx context.Context) antigravity.TransformOptions {
	opts := antigravity.DefaultTransformOptions()
	if s.settingService == nil {
		return opts
	}
	opts.EnableIdentityPatch = s.settingService.Gateway.IsIdentityPatchEnabled(ctx)
	opts.IdentityPatch = s.settingService.Gateway.GetIdentityPatchPrompt(ctx)
	return opts
}

// BindNativeAttemptActivity 仅在构造阶段绑定同一 app 屏障，停止等待已进入尝试。
func (s *AntigravityGatewayService) BindNativeAttemptActivity(enter func() (func(), error)) {
	s.nativeAttemptActivity = enter
}

// 旧映射入口只转换账号；支持判断与单跳规则由原生适配器拥有。
func mapAntigravityModel(value *gatewayprovider.ExecutionAccount, model string) string {
	return accountprovider.MapAntigravityModel(gatewayprovider.ExecutionRecord(value), model)
}

// BindAntigravityErrorObserver 绑定 app 构造的无状态平台观测器。
func (s *AntigravityGatewayService) BindAntigravityErrorObserver(value *accountprovider.AntigravityErrorObserver) {
	s.nativeError = value
}
