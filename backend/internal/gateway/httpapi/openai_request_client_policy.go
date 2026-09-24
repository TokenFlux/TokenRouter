package httpapi

import (
	"context"
	"errors"
	"net/http"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (s *OpenAIRequests) DetectClient(c *gin.Context, account *gatewayprovider.ExecutionAccount, tlsRouterMatch egress.TLSFingerprintRouterMatchResult) accountcore.CodexClientRestrictionDetectionResult {
	ctx := context.Background()
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	return s.DetectClientInput(ctx, func() (string, string) {
		if c == nil {
			return "", ""
		}
		return c.GetHeader("User-Agent"), c.GetHeader("originator")
	}, account, tlsRouterMatch)
}

// DetectClientInput 保留动态全局设置的读取时机，仅分离客户端数据来源。
func (s *OpenAIRequests) DetectClientInput(ctx context.Context, readClient func() (string, string), account *gatewayprovider.ExecutionAccount, tlsRouterMatch egress.TLSFingerprintRouterMatchResult) accountcore.CodexClientRestrictionDetectionResult {
	var globalAllowedClients []string
	if account != nil && account.View().IsCodexCLIOnlyEnabled() && s != nil && s.Readers != nil {
		if s.Readers.Gateway.IsOpenAIAllowClaudeCodeCodexPluginEnabled(ctx) {
			globalAllowedClients = []string{openai.AllowedClientClaudeCode}
		}
	}
	return s.clientDetector().DetectClient(readClient, gatewayprovider.ExecutionRecord(account), globalAllowedClients, tlsRouterMatch.Matched)
}
func LogCodexCLIOnlyDetection(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, apiKeyID int64, result accountcore.CodexClientRestrictionDetectionResult, body []byte) {
	if !result.Enabled {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	accountID := int64(0)
	if account != nil {
		accountID = account.Record.ID
	}
	fields := []zap.Field{
		zap.String("component", "service.openai_gateway"),
		zap.Int64("account_id", accountID),
		zap.Bool("codex_cli_only_enabled", result.Enabled),
		zap.Bool("codex_official_client_match", result.Matched),
		zap.String("reject_reason", result.Reason),
	}
	if apiKeyID > 0 {
		fields = append(fields, zap.Int64("api_key_id", apiKeyID))
	}
	if !result.Matched {
		fields = AppendCodexRejectedRequestFields(fields, c, body)
	}
	log := logging.FromContext(ctx).With(fields...)
	if result.Matched {
		log.Info("OpenAI codex_cli_only 放行请求")
		return
	}
	log.Warn("OpenAI codex_cli_only 拒绝非官方客户端请求")
}

// EnforceClient 在非 /responses 主入口上复用 OpenAI OAuth 客户端访问策略。
func (s *OpenAIRequests) EnforceClient(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, body []byte, tlsRouterMatch egress.TLSFingerprintRouterMatchResult) error {
	result := s.DetectClient(c, account, tlsRouterMatch)
	apiKeyID := APIKeyIDFromContext(c)
	LogCodexCLIOnlyDetection(ctx, c, account, apiKeyID, result, body)
	if !result.Enabled || result.Matched {
		return nil
	}
	MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
	if c != nil && GetOpenAIClientTransport(c) != OpenAIClientTransportWS {
		c.JSON(http.StatusForbidden, gin.H{
			"error": gin.H{
				"type":    "forbidden_error",
				"message": openAIClientPolicyForbiddenMessage(result),
			},
		})
	}
	return errors.New("openai oauth client policy restriction: client is not allowed")
}

// ApplyUserAgentHeader 在 WebSocket 握手头上复用 HTTP 上游 UA 规则。
func (s *OpenAIRequests) ApplyUserAgentHeader(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	headers http.Header,
	passthrough bool,
	routerMatch ...egress.TLSFingerprintRouterMatchResult,
) {
	if headers == nil {
		return
	}
	req := &http.Request{Header: headers}
	s.ApplyUserAgent(ctx, c, account, req, passthrough, routerMatch...)
}
func (s *OpenAIRequests) MatchTLS(c *gin.Context, account *gatewayprovider.ExecutionAccount) egress.TLSFingerprintRouterMatchResult {
	return s.MatchTLSInput(func() string {
		if c == nil {
			return ""
		}
		return c.GetHeader("User-Agent")
	}, account)
}

// MatchTLSInput 在账号确有 Router 后才读取 User-Agent。
func (s *OpenAIRequests) MatchTLSInput(readUserAgent func() string, account *gatewayprovider.ExecutionAccount) egress.TLSFingerprintRouterMatchResult {
	if s == nil || s.Routers == nil || account == nil || account.View().GetTLSFingerprintRouterID() <= 0 {
		return egress.TLSFingerprintRouterMatchResult{}
	}
	userAgent := readUserAgent()
	return s.Routers.MatchUserAgent(account.View().GetTLSFingerprintRouterID(), userAgent)
}

// TLSProfile 保留未装配时的短路，选择规则唯一归 egress。
func (s *OpenAIRequests) TLSProfile(value *gatewayprovider.ExecutionAccount, routerMatch ...egress.TLSFingerprintRouterMatchResult) *tlsfingerprint.Profile {
	if s == nil || s.Profiles == nil {
		return nil
	}
	return s.Profiles.ResolveRequestTLS(gatewayprovider.ExecutionTLSSelection(value, routerMatch))
}
func (s *OpenAIRequests) WSTLSProfile(account *gatewayprovider.ExecutionAccount, routerMatch ...egress.TLSFingerprintRouterMatchResult) (*tlsfingerprint.Profile, string) {
	profile := s.TLSProfile(account, routerMatch...)
	if profile == nil {
		return nil, ""
	}
	// Responses WebSocket 是 HTTP/1.1 Upgrade，连接池键也按剥离 h2 后的模板隔离。
	profile = tlsfingerprint.HTTP1OnlyProfile(profile)
	return profile, egress.WebSocketTLSIdentity(gatewayprovider.ExecutionTLSSelection(account, routerMatch), true, tlsfingerprint.CacheKey(profile))
}
func openAIClientPolicyForbiddenMessage(result accountcore.CodexClientRestrictionDetectionResult) string {
	// 按策略返回更明确的拒绝原因，同时保留旧 codex_cli_only 测试和客户端提示语义。
	if result.Policy == accountcore.OpenAIOAuthClientPolicyCodexOnly {
		return "This account only allows Codex official clients"
	}
	if result.Policy == accountcore.OpenAIOAuthClientPolicyTLSRouterMatchedOnly {
		return "This account only allows clients matched by the configured TLS router"
	}
	return "This account only allows configured OpenAI OAuth clients"
}
func (s *OpenAIRequests) ApplyUserAgent(ctx context.Context, _ *gin.Context, value *gatewayprovider.ExecutionAccount, req *http.Request, passthrough bool, matches ...egress.TLSFingerprintRouterMatchResult) {
	var match egress.TLSFingerprintRouterMatchResult
	if len(matches) > 0 {
		match = matches[0]
	}
	s.ClientPolicy.ApplyUserAgent(ctx, gatewayprovider.ExecutionRecord(value), req, passthrough, match)
}
func (s *OpenAIRequests) clientDetector() accountcore.ClientRestrictionDetector {
	if s != nil && s.Detector != nil {
		return s.Detector
	}
	force := s != nil && s.Options.ForceCLI
	return &accountcore.CodexClientDetector{Options: accountcore.CodexClientOptions{ForceCLI: force, OfficialUserAgent: openai.IsCodexOfficialClientRequestStrict, OfficialOriginator: openai.IsCodexOfficialClientOriginator, AllowedClients: openai.MatchAllowedClients}}
}
