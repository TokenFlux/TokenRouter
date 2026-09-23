package service

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/bedrock"
	"github.com/TokenFlux/TokenRouter/internal/upstream/vertex"

	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/gin-gonic/gin"
)

func (s *GatewayService) buildUpstreamRequest(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, body []byte, token, tokenType, modelID string, reqStream bool, mimicClaudeCode bool) (*http.Request, []byte, error) {
	if account.Record.Platform == capability.PlatformAnthropic && account.Record.Type == capability.AccountTypeServiceAccount {
		body = claude.StripDeferredToolCacheControl(body)
		req, err := s.buildUpstreamRequestAnthropicVertex(ctx, c, account, body, token, modelID, reqStream)
		return req, body, err
	}
	return claude.BuildRequest(ctx, body, token, tokenType, modelID, reqStream, mimicClaudeCode, s.anthropicRequestOptions(ctx, c, account, modelID, tokenType, mimicClaudeCode))
}

// betaPolicyResult holds the evaluated result of beta policy rules for a single request.
type betaPolicyResult struct {
	blockErr  *claude.BetaBlockedError // non-nil if a block rule matched
	filterSet map[string]struct{}      // tokens to filter (may be nil)
}

func (s *GatewayService) evaluateBetaPolicy(ctx context.Context, betaHeader string, account *gatewayprovider.ExecutionAccount, model string) betaPolicyResult {
	if s.settingService == nil {
		return betaPolicyResult{}
	}
	settings, err := s.settingService.Gateway.GetBetaPolicySettings(ctx)
	if err != nil || settings == nil {
		return betaPolicyResult{}
	}
	r := claude.EvaluateBetaPolicy(gatewayprovider.AnthropicBetaPolicy(settings), betaHeader, account.View().IsOAuth(), account.View().IsBedrock(), model)
	return betaPolicyResult{blockErr: r.BlockErr, filterSet: r.FilterSet}
}

// betaPolicyFilterSetKey is the gin.Context key for caching the policy filter set within a request.
const betaPolicyFilterSetKey = "betaPolicyFilterSet"

// getBetaPolicyFilterSet returns the beta policy filter set, using the gin context cache if available.
// In the /v1/messages path, Forward() evaluates the policy first and caches the result;
// buildUpstreamRequest reuses it (zero extra DB calls). In the count_tokens path, this
// evaluates on demand (one DB call).
func (s *GatewayService) getBetaPolicyFilterSet(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, model string) map[string]struct{} {
	if c != nil {
		if v, ok := c.Get(betaPolicyFilterSetKey); ok {
			if fs, ok := v.(map[string]struct{}); ok {
				return fs
			}
		}
	}
	return s.evaluateBetaPolicy(ctx, "", account, model).filterSet
}

func (s *GatewayService) resolveBedrockBetaTokensForRequest(
	ctx context.Context,
	account *gatewayprovider.ExecutionAccount,
	betaHeader string,
	body []byte,
	modelID string,
) ([]string, error) {
	// 1. 对原始 header 中的 beta token 做 block 检查（快速失败）
	policy := s.evaluateBetaPolicy(ctx, betaHeader, account, modelID)
	if policy.blockErr != nil {
		return nil, policy.blockErr
	}

	// 2. 解析 header + body 自动注入 + Bedrock 转换/过滤
	betaTokens := bedrock.ResolveBedrockBetaTokens(betaHeader, body, modelID)

	// 3. 对最终 token 列表再做 block 检查，捕获通过 body 自动注入绕过 header block 的情况。
	//    例如：管理员 block 了 interleaved-thinking，客户端不在 header 中带该 token，
	//    但请求体中包含 thinking 字段 → autoInjectBedrockBetaTokens 会自动补齐 →
	//    如果不做此检查，block 规则会被绕过。
	if blockErr := s.checkBetaPolicyBlockForTokens(ctx, betaTokens, account, modelID); blockErr != nil {
		return nil, blockErr
	}

	return claude.FilterBetaTokens(betaTokens, policy.filterSet), nil
}

func (s *GatewayService) checkBetaPolicyBlockForTokens(ctx context.Context, tokens []string, account *gatewayprovider.ExecutionAccount, model string) *claude.BetaBlockedError {
	if s.settingService == nil || len(tokens) == 0 {
		return nil
	}
	settings, err := s.settingService.Gateway.GetBetaPolicySettings(ctx)
	if err != nil || settings == nil {
		return nil
	}
	return claude.CheckBetaPolicyBlockForTokens(gatewayprovider.AnthropicBetaPolicy(settings), tokens, account.View().IsOAuth(), account.View().IsBedrock(), model)
}

// buildCustomRelayURL 构建自定义中继转发 URL
// 在 path 后附加 beta=true 和可选的 proxy 查询参数
func (s *GatewayService) buildCustomRelayURL(baseURL, path string, account *gatewayprovider.ExecutionAccount) string {
	u := strings.TrimRight(baseURL, "/") + path + "?beta=true"
	if account.Record.ProxyID != nil && account.Record.Proxy != nil {
		proxyURL := account.Record.Proxy.URL()
		if proxyURL != "" {
			u += "&proxy=" + url.QueryEscape(proxyURL)
		}
	}
	return u
}

func (s *GatewayService) validateUpstreamBaseURL(raw string) (string, error) {
	if s.cfg != nil && !s.cfg.Security.URLAllowlist.Enabled {
		normalized, err := egress.ValidateURLFormat(raw, s.cfg.Security.URLAllowlist.AllowInsecureHTTP)
		if err != nil {
			return "", fmt.Errorf("invalid base_url: %w", err)
		}
		return normalized, nil
	}
	normalized, err := egress.ValidateHTTPSURL(raw, egress.ValidationOptions{
		AllowedHosts:     s.cfg.Security.URLAllowlist.UpstreamHosts,
		RequireAllowlist: true,
		AllowPrivate:     s.cfg.Security.URLAllowlist.AllowPrivateHosts,
	})
	if err != nil {
		return "", fmt.Errorf("invalid base_url: %w", err)
	}
	return normalized, nil
}

// 旧入站适配只提供策略和账号投影，不再拥有 Vertex 构造算法。
func (s *GatewayService) buildUpstreamRequestAnthropicVertex(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, body []byte, token, modelID string, reqStream bool) (*http.Request, error) {
	var headers http.Header
	var beta string
	if c != nil && c.Request != nil {
		headers = c.Request.Header
		beta = claude.GetHeaderRaw(headers, "anthropic-beta")
	}
	return vertex.BuildAnthropicRequest(ctx, body, token, modelID, reqStream, vertex.AnthropicRequestOptions{
		ClientBeta: beta, ClientHeaders: headers, AllowedHeaders: allowedHeaders, Project: func() string {
			return gatewayprovider.ExecutionProtocolRecord(account).VertexProjectID(vertex.ServiceAccountProjectID)
		}, Location: func(model string) string {
			return gatewayprovider.ExecutionProtocolRecord(account).VertexLocation(model)
		},
		Policy: func(ctx context.Context, header string) (map[string]struct{}, error) {
			policy := s.evaluateBetaPolicy(ctx, header, account, modelID)
			if policy.blockErr != nil {
				return nil, policy.blockErr
			}
			return claude.MergeDropSets(policy.filterSet), nil
		},
		SanitizeBody: claude.SanitizeAnthropicBodyForBetaTokens, WireCasing: claude.ResolveWireCasing, AddHeader: claude.AddHeaderRaw, SetHeader: claude.SetHeaderRaw, DeleteHeader: claude.DeleteHeaderAllForms,
		Debug: func(header http.Header, data []byte, fields map[string]string) {
			s.debugLogGatewaySnapshot("UPSTREAM_FORWARD_VERTEX_ANTHROPIC", header, data, fields)
		},
	})
}

// 旧日志入口委托唯一通用实现。
func truncateForLog(body []byte, maxBytes int) string { return logredact.TruncateLine(body, maxBytes) }
